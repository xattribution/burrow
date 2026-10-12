package main

// The phone channel: end-to-end encryption between a paired device and Burrow,
// carried inside HTTPS.
//
// HTTPS alone ends wherever TLS ends. Behind a reverse proxy that is the proxy,
// which then sees every path, file and password in plain text, and its key
// exchange may not be post-quantum. So the app and Burrow encrypt everything a
// second time, with keys nothing in between ever sees:
//
//   - Pairing. The web UI shows a QR code holding a one-time code id and a
//     32-byte secret (Kqr). The secret only ever travels on screen. The app
//     sends an ML-KEM-768 encapsulation key and an X25519 key, authenticated
//     with Kqr. Burrow answers with an ML-KEM ciphertext and its own X25519 key.
//     Both sides derive the device key DK from Kqr and both shared secrets, so
//     someone who photographed the QR code and recorded the traffic would also
//     have to break ML-KEM and X25519. The code works once, for ten minutes.
//   - Sessions. Every hour or so (and after a restart) the app runs the same
//     exchange authenticated by DK ("hello"). The session key SK mixes DK with
//     fresh ML-KEM and X25519 secrets, so recorded traffic stays secret even if
//     DK leaks later. Only a server that holds DK can produce the reply, which
//     is how the app knows it reached the real Burrow; nothing reusable (no
//     password, no token) is ever sent.
//   - Requests. Each request is one HTTPS POST to /api/d whose body is the
//     real request (method, path, headers, body), encrypted with AES-256-GCM in
//     64 KiB segments under a key derived from SK and the message number; the
//     reply comes back the same way. A proxy sees one URL and opaque bytes.
//     Message numbers are accepted once, so a recorded request can't be replayed.
//
// All keys are 256-bit and every derivation is HKDF-SHA-256 with a distinct
// label. Wire formats (big-endian, sizes in bytes):
//
//	pair request   1 ver | 16 code id | 32 nonce | 1184 ek | 32 x25519 | 2 n | n info | 32 mac
//	pair reply     1 ver | 16 device id | 32 nonce | 1088 ct | 32 x25519 | sealed payload
//	hello request  1 ver | 16 device id | 32 nonce | 1184 ek | 32 x25519 | 32 mac
//	hello reply    1 ver | 16 session id | 32 nonce | 1088 ct | 32 x25519 | 8 expires | sealed payload
//	tunnel request 1 ver | 16 session id | 8 message number | segments
//	tunnel reply   1 ver | segments
//	segment        4 (bit 31: last; bits 0-30: ciphertext length) | ciphertext with 16-byte tag
//
// The plaintext of a tunnel message is a 4-byte length, a JSON header, then the
// body. See the "burrow-v1 ..." labels below for each key.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

const (
	chVersion = 1
	chIDLen   = 16
	chNonce   = 32
	chMAC     = 32
	chX       = 32
	chEK      = mlkem.EncapsulationKeySize768
	chCT      = mlkem.CiphertextSize768
	chSegMax  = 64 << 10 // plaintext bytes per segment
	chTag     = 16
	chHdrMax  = 64 << 10 // largest JSON header inside a message
	chLast    = 1 << 31
)

var (
	errChFrame     = errors.New("malformed message")
	errChAuth      = errors.New("message failed authentication")
	errChTruncated = errors.New("message ended early")
	errChReplay    = errors.New("message already received")
)

// chKey derives a 32-byte key from a secret with HKDF-SHA-256.
func chKey(secret []byte, label string) []byte {
	k, err := hkdf.Key(sha256.New, secret, nil, label, 32)
	if err != nil {
		panic(err)
	}
	return k
}

// chExpand derives a 32-byte key from a pseudorandom key (the output of an extract).
func chExpand(prk []byte, label string) []byte {
	k, err := hkdf.Expand(sha256.New, prk, label, 32)
	if err != nil {
		panic(err)
	}
	return k
}

func chMac(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

func chAEAD(key []byte) cipher.AEAD {
	b, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		panic(err)
	}
	return g
}

// chSeal encrypts with a key used for exactly one message, so the nonce can be zero.
func chSeal(key, plain, aad []byte) []byte {
	return chAEAD(key).Seal(nil, make([]byte, 12), plain, aad)
}

func chOpen(key, sealed, aad []byte) ([]byte, error) {
	p, err := chAEAD(key).Open(nil, make([]byte, 12), sealed, aad)
	if err != nil {
		return nil, errChAuth
	}
	return p, nil
}

// kemReply is the server side of the key exchange: given the client's ML-KEM-768
// encapsulation key and X25519 public key, it returns the ML-KEM ciphertext, the
// server's X25519 public key and both shared secrets, concatenated.
func kemReply(ek, cx []byte) (ct, sx, ss []byte, err error) {
	k, err := mlkem.NewEncapsulationKey768(ek)
	if err != nil {
		return nil, nil, nil, errChFrame
	}
	pub, err := ecdh.X25519().NewPublicKey(cx)
	if err != nil {
		return nil, nil, nil, errChFrame
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	ss2, err := priv.ECDH(pub) // fails for low-order points
	if err != nil {
		return nil, nil, nil, errChFrame
	}
	ss1, ct := k.Encapsulate()
	return ct, priv.PublicKey().Bytes(), append(ss1, ss2...), nil
}

// chPRK binds a pre-shared key and the fresh shared secrets to the transcript (the
// whole request and the reply up to the sealed payload). It returns the
// pseudorandom key and the transcript hash, which is also the payload's AAD.
func chPRK(psk, ss, req, replyHead []byte) (prk, th []byte) {
	h := sha256.New()
	h.Write(req)
	h.Write(replyHead)
	th = h.Sum(nil)
	secret := append(append([]byte{}, psk...), ss...)
	prk, err := hkdf.Extract(sha256.New, secret, th)
	if err != nil {
		panic(err)
	}
	return prk, th
}

// ---- tunnel segments ----

func chSegNonce(i uint64, last bool) []byte {
	n := make([]byte, 12)
	if last {
		n[3] = 1
	}
	binary.BigEndian.PutUint64(n[4:], i)
	return n
}

// segWriter encrypts a stream into segments. Close writes the last segment, which
// may be empty; a reader that never sees it knows the stream was cut short.
type segWriter struct {
	w      io.Writer
	aead   cipher.AEAD
	aad    []byte
	buf    []byte
	out    []byte
	n      uint64
	flush  func()
	closed bool
}

func newSegWriter(w io.Writer, key, aad []byte, flush func()) *segWriter {
	return &segWriter{w: w, aead: chAEAD(key), aad: append([]byte{}, aad...), buf: make([]byte, 0, chSegMax), out: make([]byte, 4, 4+chSegMax+chTag), flush: flush}
}

func (s *segWriter) emit(last bool) error {
	out := s.aead.Seal(s.out[:4], chSegNonce(s.n, last), s.buf, s.aad)
	l := uint32(len(out) - 4)
	if last {
		l |= chLast
	}
	binary.BigEndian.PutUint32(out, l)
	s.n++
	s.buf = s.buf[:0]
	_, err := s.w.Write(out)
	return err
}

func (s *segWriter) Write(p []byte) (int, error) {
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	n := 0
	for len(p) > 0 {
		if len(s.buf) == chSegMax {
			if err := s.emit(false); err != nil {
				return n, err
			}
		}
		c := copy(s.buf[len(s.buf):chSegMax], p)
		s.buf = s.buf[:len(s.buf)+c]
		p = p[c:]
		n += c
	}
	return n, nil
}

// Flush sends what is buffered as a segment of its own, for replies that stream.
func (s *segWriter) Flush() error {
	if s.closed || len(s.buf) == 0 {
		return nil
	}
	err := s.emit(false)
	if s.flush != nil {
		s.flush()
	}
	return err
}

func (s *segWriter) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.emit(true)
	if s.flush != nil {
		s.flush()
	}
	return err
}

// segReader decrypts segments. first runs once the first segment has
// authenticated (the replay check happens there, so forged messages can't burn
// message numbers).
type segReader struct {
	r     io.Reader
	aead  cipher.AEAD
	aad   []byte
	buf   []byte
	plain []byte
	off   int
	n     uint64
	done  bool
	err   error
	first func() error
}

func newSegReader(r io.Reader, key, aad []byte, first func() error) *segReader {
	return &segReader{r: r, aead: chAEAD(key), aad: append([]byte{}, aad...), buf: make([]byte, chSegMax+chTag), first: first}
}

func (s *segReader) next() error {
	var h [4]byte
	if _, err := io.ReadFull(s.r, h[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errChTruncated
		}
		return err
	}
	w := binary.BigEndian.Uint32(h[:])
	last, l := w&chLast != 0, int(w&^chLast)
	if l < chTag || l > chSegMax+chTag {
		return errChFrame
	}
	if _, err := io.ReadFull(s.r, s.buf[:l]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errChTruncated
		}
		return err
	}
	p, err := s.aead.Open(s.buf[:0], chSegNonce(s.n, last), s.buf[:l], s.aad)
	if err != nil {
		return errChAuth
	}
	s.n++
	if s.n == 1 && s.first != nil {
		if err := s.first(); err != nil {
			return err
		}
	}
	s.plain, s.off = p, 0
	if last {
		s.done = true
		var one [1]byte
		if n, _ := io.ReadFull(s.r, one[:]); n > 0 {
			return errChFrame // bytes after the last segment
		}
	}
	return nil
}

func (s *segReader) Read(p []byte) (int, error) {
	for s.off >= len(s.plain) {
		if s.err != nil {
			return 0, s.err
		}
		if s.done {
			return 0, io.EOF
		}
		if s.err = s.next(); s.err != nil {
			return 0, s.err
		}
	}
	n := copy(p, s.plain[s.off:])
	s.off += n
	return n, nil
}

// readFrame reads the length-prefixed JSON header at the start of a message.
func readFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > chHdrMax {
		return nil, errChFrame
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func writeFrame(w io.Writer, b []byte) error {
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}
