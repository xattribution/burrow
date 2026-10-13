package main

// Transport security. Burrow only speaks HTTPS: every port terminates TLS 1.3
// itself, so nothing travels in plain text, including the hop from a reverse
// proxy to Burrow. Key exchange prefers the hybrid post-quantum groups Go offers
// (X25519MLKEM768 and friends) and falls back to X25519/P-256 only for clients
// that lack them, unless BURROW_TLS_PQ_ONLY is set.
//
// The certificate comes from BURROW_TLS_CERT/BURROW_TLS_KEY (reloaded when the
// files change, so renewals need no restart) or, if those aren't set, from a
// self-signed certificate generated once into the state folder. A reverse proxy
// can pin that certificate.

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pqGroups are the hybrid post-quantum key exchanges, in preference order.
var pqGroups = []tls.CurveID{tls.X25519MLKEM768, tls.SecP256r1MLKEM768, tls.SecP384r1MLKEM1024}

// certSource serves the certificate, re-reading the files when they change.
type certSource struct {
	certFile, keyFile string
	mu                sync.Mutex
	cert              *tls.Certificate
	stamp             string    // contentStamp of both files when loaded
	checked           time.Time // last time the files were looked at
}

// contentStamp fingerprints the files' contents. Size and mtime aren't enough: two
// writes in the same clock tick can leave both unchanged. The files are small.
func contentStamp(contents ...[]byte) string {
	h := sha256.New()
	for _, b := range contents {
		fmt.Fprintf(h, "%d:", len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fileStamp(paths ...string) string {
	var contents [][]byte
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		contents = append(contents, b)
	}
	return contentStamp(contents...)
}

// load reads both files once, so the stamp describes exactly the certificate loaded.
func (c *certSource) load() error {
	certPEM, err := os.ReadFile(c.certFile)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(c.keyFile)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	c.cert, c.stamp = &cert, contentStamp(certPEM, keyPEM)
	return nil
}

// get is tls.Config.GetCertificate. Files are checked at most every 30 seconds;
// a broken renewal keeps the previous certificate.
func (c *certSource) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checked) > 30*time.Second {
		c.checked = time.Now()
		if s := fileStamp(c.certFile, c.keyFile); s != "" && s != c.stamp {
			if err := c.load(); err != nil {
				log.Printf("tls: keeping the previous certificate, the new one didn't load: %v", err)
			} else {
				log.Printf("tls: certificate reloaded (SHA-256 %s)", certFingerprint(c.cert))
			}
		}
	}
	if c.cert == nil {
		return nil, errors.New("no certificate")
	}
	return c.cert, nil
}

func certFingerprint(c *tls.Certificate) string {
	if c == nil || len(c.Certificate) == 0 {
		return ""
	}
	sum := sha256.Sum256(c.Certificate[0])
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// newCertSource uses the configured files, or a self-signed certificate kept in
// <state>/tls so its fingerprint stays the same across restarts.
func newCertSource(cfg Config) (*certSource, error) {
	if cfg.TLSCert != "" || cfg.TLSKey != "" {
		if cfg.TLSCert == "" || cfg.TLSKey == "" {
			return nil, errors.New("set both BURROW_TLS_CERT and BURROW_TLS_KEY, or neither")
		}
		c := &certSource{certFile: cfg.TLSCert, keyFile: cfg.TLSKey}
		if err := c.load(); err != nil {
			return nil, fmt.Errorf("tls certificate: %w", err)
		}
		return c, nil
	}
	dir := filepath.Join(cfg.StateDir, "tls")
	c := &certSource{certFile: filepath.Join(dir, "burrow.crt"), keyFile: filepath.Join(dir, "burrow.key")}
	if err := c.load(); err == nil {
		return c, nil
	}
	if err := writeSelfSigned(c.certFile, c.keyFile); err != nil {
		return nil, fmt.Errorf("self-signed certificate: %w", err)
	}
	return c, c.load()
}

// writeSelfSigned makes an ECDSA P-256 certificate for the name "burrow" (what a
// proxy verifies when it pins it), localhost and this machine's hostname.
func writeSelfSigned(certFile, keyFile string) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	names := []string{"burrow", "localhost"}
	if h, err := os.Hostname(); err == nil && h != "" && h != "localhost" {
		names = append(names, h)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "burrow"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // its own trust anchor, so a pinning proxy can verify it
		DNSNames:              names,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// tlsConfig: TLS 1.3 only (every suite is AEAD with forward secrecy), hybrid
// post-quantum key exchange first.
func tlsConfig(certs *certSource, pqOnly bool) *tls.Config {
	c := &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: certs.get,
		NextProtos:     []string{"h2", "http/1.1"},
	}
	if pqOnly {
		c.CurvePreferences = pqGroups
	} else {
		c.CurvePreferences = append(append([]tls.CurveID{}, pqGroups...), tls.X25519, tls.CurveP256)
	}
	return c
}

// ---- one port, TLS only ----

// tlsListener accepts connections and sorts them by their first byte: a TLS
// handshake goes to the HTTPS server, anything else to plain, which only ever
// answers with a redirect or a refusal (see plainHandler). The sorting happens
// per connection, so a slow client can't hold up accepting others.
type tlsListener struct {
	net.Listener
	cfg     *tls.Config
	tlsCh   chan net.Conn
	plainCh chan net.Conn
	done    chan struct{}
	once    sync.Once
}

func newTLSListener(ln net.Listener, cfg *tls.Config) *tlsListener {
	l := &tlsListener{Listener: ln, cfg: cfg, tlsCh: make(chan net.Conn), plainCh: make(chan net.Conn), done: make(chan struct{})}
	go l.acceptLoop()
	return l
}

type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (l *tlsListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			l.Close()
			return
		}
		go l.sort(conn)
	}
}

func (l *tlsListener) sort(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	pc := &peekedConn{Conn: conn, r: br}
	ch, out := l.plainCh, net.Conn(pc)
	if first[0] == 0x16 { // TLS handshake record
		ch, out = l.tlsCh, tls.Server(pc, l.cfg)
	}
	select {
	case ch <- out:
	case <-l.done:
		conn.Close()
	}
}

func (l *tlsListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// chanListener hands one kind of sorted connection to an http.Server.
type chanListener struct {
	parent *tlsListener
	ch     chan net.Conn
}

func (c chanListener) Accept() (net.Conn, error) {
	select {
	case conn := <-c.ch:
		return conn, nil
	case <-c.parent.done:
		return nil, net.ErrClosed
	}
}
func (c chanListener) Close() error   { return c.parent.Close() }
func (c chanListener) Addr() net.Addr { return c.parent.Addr() }

// plainHandler answers a plain-HTTP request on an HTTPS port without serving
// anything: a browser is redirected to https, and a request that came through a
// proxy is refused, since redirecting it would loop and the proxy should be
// pointed at https instead.
func plainHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "close")
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Proto") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-IP") != "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = io.WriteString(w, "Burrow only accepts HTTPS. Set the proxy's forward scheme to https.\n")
		return
	}
	host := r.Host
	if host == "" || strings.ContainsAny(host, "/\\@ ") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Location", "https://"+host+r.URL.RequestURI())
	w.WriteHeader(http.StatusPermanentRedirect)
}

// serveTLSOnly runs handler on addr, HTTPS only.
func serveTLSOnly(srv *http.Server, ln net.Listener, cfg *tls.Config) error {
	tl := newTLSListener(ln, cfg)
	plain := &http.Server{Handler: http.HandlerFunc(plainHandler), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: quietLog}
	go func() { _ = plain.Serve(chanListener{parent: tl, ch: tl.plainCh}) }()
	srv.TLSConfig = cfg
	return srv.Serve(chanListener{parent: tl, ch: tl.tlsCh})
}

// quietLog drops the per-connection noise (failed handshakes from scanners,
// clients that hang up) that http.Server would otherwise log.
var quietLog = log.New(io.Discard, "", 0)
