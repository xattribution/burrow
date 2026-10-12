package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// devClient is a reference client for the phone channel, written from the
// protocol description in devchan.go. The Kotlin client must agree with it.
type devClient struct {
	t     *testing.T
	base  string
	c     *http.Client
	devID []byte
	dk    []byte
	sid   []byte
	sk    []byte
	msg   uint64
	info  map[string]any
	last  []byte // the last tunnel request as sent, for replay tests
}

// kemOffer makes the client's half of a key exchange.
func kemOffer() (*mlkem.DecapsulationKey768, *ecdh.PrivateKey, []byte) {
	dk, _ := mlkem.GenerateKey768()
	x, _ := ecdh.X25519().GenerateKey(rand.Reader)
	nonce := make([]byte, chNonce)
	_, _ = rand.Read(nonce)
	return dk, x, nonce
}

func kemFinish(dk *mlkem.DecapsulationKey768, x *ecdh.PrivateKey, ct, sx []byte) ([]byte, error) {
	ss1, err := dk.Decapsulate(ct)
	if err != nil {
		return nil, err
	}
	pub, err := ecdh.X25519().NewPublicKey(sx)
	if err != nil {
		return nil, err
	}
	ss2, err := x.ECDH(pub)
	if err != nil {
		return nil, err
	}
	return append(ss1, ss2...), nil
}

func postBin(c *http.Client, u string, body []byte) (int, []byte, error) {
	resp, err := c.Post(u, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// pairWith completes pairing from a burrow://pair link against base.
func pairWith(t *testing.T, c *http.Client, base, link, name string) (*devClient, int, error) {
	t.Helper()
	pu, err := url.Parse(link)
	if err != nil {
		return nil, 0, err
	}
	q := pu.Query()
	codeID, _ := base64.RawURLEncoding.DecodeString(q.Get("c"))
	kqr, _ := base64.RawURLEncoding.DecodeString(q.Get("k"))
	dk, x, nonce := kemOffer()
	head := concat([]byte{chVersion}, codeID, nonce, dk.EncapsulationKey().Bytes(), x.PublicKey().Bytes())
	info, _ := json.Marshal(map[string]string{"name": name, "platform": "android", "model": "Test Fold", "app_version": "test"})
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(info)+chTag))
	aad := concat(head, l[:])
	sealed := chSeal(chKey(concat(kqr, nonce), "burrow-v1 pair info"), info, aad)
	body := concat(aad, sealed)
	req := concat(body, chMac(chKey(kqr, "burrow-v1 pair auth"), body))
	code, reply, err := postBin(c, base+"/api/d/pair", req)
	if err != nil || code != 200 {
		return nil, code, err
	}
	const rh = 1 + chIDLen + chNonce + chCT + chX
	if len(reply) <= rh {
		return nil, code, errors.New("short reply")
	}
	ss, err := kemFinish(dk, x, reply[1+chIDLen+chNonce:1+chIDLen+chNonce+chCT], reply[rh-chX:rh])
	if err != nil {
		return nil, code, err
	}
	prk, th := chPRK(kqr, ss, req, reply[:rh])
	payload, err := chOpen(chExpand(prk, "burrow-v1 pair payload"), reply[rh:], th)
	if err != nil {
		return nil, code, err
	}
	d := &devClient{t: t, base: base, c: c, devID: reply[1 : 1+chIDLen], dk: chExpand(prk, "burrow-v1 device key")}
	_ = json.Unmarshal(payload, &d.info)
	return d, code, nil
}

// hello starts a session. It returns the HTTP status.
func (d *devClient) hello() int {
	d.t.Helper()
	dk, x, nonce := kemOffer()
	body := concat([]byte{chVersion}, d.devID, nonce, dk.EncapsulationKey().Bytes(), x.PublicKey().Bytes())
	req := concat(body, chMac(chKey(d.dk, "burrow-v1 hello auth"), body))
	code, reply, err := postBin(d.c, d.base+"/api/d/hello", req)
	if err != nil {
		d.t.Fatal(err)
	}
	if code != 200 {
		return code
	}
	const rh = 1 + chIDLen + chNonce + chCT + chX + 8
	ss, err := kemFinish(dk, x, reply[1+chIDLen+chNonce:1+chIDLen+chNonce+chCT], reply[rh-8-chX:rh-8])
	if err != nil {
		d.t.Fatal(err)
	}
	prk, th := chPRK(d.dk, ss, req, reply[:rh])
	payload, err := chOpen(chExpand(prk, "burrow-v1 hello payload"), reply[rh:], th)
	if err != nil {
		d.t.Fatalf("hello payload: %v", err)
	}
	_ = json.Unmarshal(payload, &d.info)
	d.sid, d.sk, d.msg = reply[1:1+chIDLen], chExpand(prk, "burrow-v1 session"), 0
	return 200
}

type tunnelResp struct {
	outer  int
	status int
	header map[string]string
	body   []byte
	err    error
}

// seal builds one tunnel request.
func (d *devClient) seal(method, path string, hdr map[string]string, body []byte) []byte {
	d.msg++
	head := make([]byte, 1+chIDLen+8)
	head[0] = chVersion
	copy(head[1:], d.sid)
	binary.BigEndian.PutUint64(head[1+chIDLen:], d.msg)
	l := int64(len(body))
	h, _ := json.Marshal(innerReq{Method: method, URL: path, Header: hdr, Length: &l})
	var out bytes.Buffer
	out.Write(head)
	sw := newSegWriter(&out, chExpand(d.sk, "burrow-v1 c2s "+string(head[1+chIDLen:])), head, nil)
	_ = writeFrame(sw, h)
	_, _ = sw.Write(body)
	_ = sw.Close()
	return out.Bytes()
}

// send posts a sealed request and opens the reply.
func (d *devClient) send(raw []byte) tunnelResp {
	d.last = raw
	code, reply, err := postBin(d.c, d.base+"/api/d", raw)
	if err != nil {
		return tunnelResp{err: err}
	}
	if code != 200 {
		return tunnelResp{outer: code, body: reply}
	}
	head := raw[:1+chIDLen+8]
	if len(reply) == 0 || reply[0] != chVersion {
		return tunnelResp{outer: code, err: errChFrame}
	}
	sr := newSegReader(bytes.NewReader(reply[1:]), chExpand(d.sk, "burrow-v1 s2c "+string(head[1+chIDLen:])), head, nil)
	hb, err := readFrame(sr)
	if err != nil {
		return tunnelResp{outer: code, err: err}
	}
	var ih struct {
		S int               `json:"s"`
		H map[string]string `json:"h"`
	}
	_ = json.Unmarshal(hb, &ih)
	b, err := io.ReadAll(sr)
	return tunnelResp{outer: code, status: ih.S, header: ih.H, body: b, err: err}
}

func (d *devClient) do(method, path string, hdr map[string]string, body []byte) tunnelResp {
	return d.send(d.seal(method, path, hdr, body))
}

// pairedDevice pairs a device for the signed-in user of e.
func pairedDevice(t *testing.T, e *tenv, name string) *devClient {
	t.Helper()
	e.app.st.set("lan_url", e.srv.URL)
	var start struct{ ID, Link, QR string }
	if err := json.Unmarshal(e.do("POST", "/api/devices/pair", nil, 200), &start); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(start.QR, "data:image/png;base64,") || !strings.HasPrefix(start.Link, "burrow://pair?") {
		t.Fatalf("pairing start: %+v", start)
	}
	d, code, err := pairWith(t, http.DefaultClient, e.srv.URL, start.Link, name)
	if err != nil || code != 200 {
		t.Fatalf("pair: %d %v", code, err)
	}
	if d.hello() != 200 {
		t.Fatal("hello failed")
	}
	return d
}

func TestDeviceChannelEndToEnd(t *testing.T) {
	e := setup(t)
	e.app.st.set("lan_url", e.srv.URL)
	var start struct{ ID, Link string }
	_ = json.Unmarshal(e.do("POST", "/api/devices/pair", nil, 200), &start)
	var st map[string]any
	_ = json.Unmarshal(e.do("GET", "/api/devices/pair/"+start.ID, nil, 200), &st)
	if st["status"] != "waiting" {
		t.Fatalf("status before pairing: %v", st)
	}

	d, code, err := pairWith(t, http.DefaultClient, e.srv.URL, start.Link, "Pixel Fold")
	if err != nil || code != 200 {
		t.Fatalf("pair: %d %v", code, err)
	}
	if d.info["user"] != "admin" || d.info["lan"] != e.srv.URL {
		t.Fatalf("pair payload: %v", d.info)
	}
	_ = json.Unmarshal(e.do("GET", "/api/devices/pair/"+start.ID, nil, 200), &st)
	if st["status"] != "paired" {
		t.Fatalf("status after pairing: %v", st)
	}
	// the code works once
	if _, code, _ := pairWith(t, http.DefaultClient, e.srv.URL, start.Link, "Again"); code != 403 {
		t.Fatalf("second use of a pairing code: %d", code)
	}

	if d.hello() != 200 {
		t.Fatal("hello")
	}
	r := d.do("GET", "/api/ls?path=DOCS", nil, nil)
	if r.err != nil || r.status != 200 || !strings.Contains(string(r.body), "notes.txt") {
		t.Fatalf("ls through the tunnel: %+v %s", r, r.body)
	}
	// ranges and file contents pass through
	r = d.do("GET", "/api/raw?path=DOCS/notes.txt", map[string]string{"Range": "bytes=6-"}, nil)
	if r.status != 206 || string(r.body) != "notes" || r.header["Content-Range"] == "" {
		t.Fatalf("range: %d %q %v", r.status, r.body, r.header)
	}
	// hidden and disabled roots stay hidden
	if r = d.do("GET", "/api/raw?path=PRIVATE/keys.txt", nil, nil); r.status != 404 {
		t.Fatalf("disabled root through the tunnel: %d", r.status)
	}
	// administration isn't reachable from a device
	if r = d.do("GET", "/api/users", nil, nil); r.status != 404 {
		t.Fatalf("admin API through the tunnel: %d", r.status)
	}
	if r = d.do("GET", "/api/tokens", nil, nil); r.status != 404 {
		t.Fatalf("tokens through the tunnel: %d", r.status)
	}
	// a recorded request can't be replayed
	if r = d.send(d.last); r.outer != 409 {
		t.Fatalf("replay: %d", r.outer)
	}
	// a tampered request is refused
	raw := d.seal("GET", "/api/ls?path=DOCS", nil, nil)
	raw[len(raw)-3] ^= 1
	if r = d.send(raw); r.outer != 400 {
		t.Fatalf("tampered: %d", r.outer)
	}
	// a cut-off request doesn't run as a shorter one
	raw = d.seal("GET", "/api/ls?path=DOCS", nil, nil)
	if r = d.send(raw[:len(raw)-1]); r.outer != 400 {
		t.Fatalf("truncated: %d", r.outer)
	}

	var devs []Device
	_ = json.Unmarshal(e.do("GET", "/api/devices", nil, 200), &devs)
	if len(devs) != 1 || devs[0].Name != "Pixel Fold" || devs[0].Model != "Test Fold" || devs[0].LastVia != "lan" {
		t.Fatalf("device list: %+v", devs)
	}
	// revoking ends the session at once and refuses new ones
	e.do("DELETE", "/api/devices/"+devs[0].ID, nil, 200)
	if r = d.do("GET", "/api/ls?path=DOCS", nil, nil); r.outer != 401 {
		t.Fatalf("revoked device request: %d", r.outer)
	}
	if d.hello() != 401 {
		t.Fatal("revoked device could start a session")
	}
}

func TestDeviceChannelHidesEverythingFromTheProxy(t *testing.T) {
	e := setup(t)
	d := pairedDevice(t, e, "Phone")
	// record exactly what a TLS-terminating proxy would see
	var seen bytes.Buffer
	rec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen.WriteString(r.Method + " " + r.URL.String() + "\n")
		for k, v := range r.Header {
			seen.WriteString(k + ": " + strings.Join(v, ",") + "\n")
		}
		seen.Write(b)
		resp, err := http.Post(e.srv.URL+r.URL.Path, r.Header.Get("Content-Type"), bytes.NewReader(b))
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		seen.Write(out)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(out)
	}))
	defer rec.Close()
	d.base = rec.URL
	r := d.do("GET", "/api/raw?path=DOCS/notes.txt", nil, nil)
	if r.status != 200 || string(r.body) != "hello notes" {
		t.Fatalf("through the recording proxy: %+v", r)
	}
	r = d.do("POST", "/api/favorites", map[string]string{"Content-Type": "application/json"}, []byte(`{"path":"DOCS/Taxes"}`))
	if r.status != 200 {
		t.Fatalf("favorite: %+v", r)
	}
	for _, leak := range []string{"notes", "hello", "DOCS", "Taxes", "/api/raw", "/api/favorites", "GET", "admin", "application/json"} {
		if bytes.Contains(seen.Bytes(), []byte(leak)) {
			t.Errorf("the proxy saw %q", leak)
		}
	}
	if !strings.Contains(seen.String(), "POST /api/d\n") {
		t.Fatal("recorder saw nothing")
	}
}

func TestDeviceHandshakeFailures(t *testing.T) {
	e := setup(t)
	d := pairedDevice(t, e, "Phone")
	// wrong device key: the server won't answer (and a fake server can't, either)
	bad := *d
	bad.dk = make([]byte, 32)
	if bad.hello() != 401 {
		t.Fatal("hello with the wrong key")
	}
	// unknown session: the app is told to handshake again
	stale := *d
	stale.sid = make([]byte, chIDLen)
	if r := stale.do("GET", "/api/ls", nil, nil); r.outer != 401 || !strings.Contains(string(r.body), "session") {
		t.Fatalf("unknown session: %d %s", r.outer, r.body)
	}
	// a disabled account's device stops working
	u := mustUser(t, e, "admin")
	_, _ = e.app.st.exec(`UPDATE users SET disabled=1 WHERE id=?`, u.ID)
	if r := d.do("GET", "/api/ls", nil, nil); r.outer != 403 {
		t.Fatalf("disabled user: %d", r.outer)
	}
	if d.hello() != 403 {
		t.Fatal("disabled user hello")
	}
	_, _ = e.app.st.exec(`UPDATE users SET disabled=0 WHERE id=?`, u.ID)

	// an expired pairing code is refused
	e.app.st.set("lan_url", e.srv.URL)
	var start struct{ ID, Link string }
	_ = json.Unmarshal(e.do("POST", "/api/devices/pair", nil, 200), &start)
	e.app.dev.mu.Lock()
	e.app.dev.codes[start.ID].expires = time.Now().Add(-time.Second)
	e.app.dev.mu.Unlock()
	if _, code, _ := pairWith(t, http.DefaultClient, e.srv.URL, start.Link, "Late"); code != 403 {
		t.Fatalf("expired code: %d", code)
	}
	// a made-up code is refused and counted
	pu, _ := url.Parse(start.Link)
	q := pu.Query()
	q.Set("c", base64.RawURLEncoding.EncodeToString(make([]byte, chIDLen)))
	pu.RawQuery = q.Encode()
	if _, code, _ := pairWith(t, http.DefaultClient, e.srv.URL, pu.String(), "Guess"); code != 403 {
		t.Fatalf("made-up code: %d", code)
	}
}

func TestDeviceActsAsItsUser(t *testing.T) {
	e := setup(t)
	e.mkUser("kid", false, Grant{Path: "PROJECTS/lamp", Read: true})
	k := e.as("kid", "longenough-kid")
	d := pairedDevice(t, k, "Kid's phone")
	if r := d.do("GET", "/api/raw?path=PROJECTS/lamp/shade.stl", nil, nil); r.status != 200 {
		t.Fatalf("granted file: %d", r.status)
	}
	if r := d.do("GET", "/api/raw?path=DOCS/notes.txt", nil, nil); r.status != 404 {
		t.Fatalf("ungranted file: %d", r.status)
	}
	if r := d.do("GET", "/api/search?q=notes", nil, nil); r.status != 200 || strings.Contains(string(r.body), "notes.txt") {
		t.Fatalf("search leaks: %d %s", r.status, r.body)
	}
	// the admin sees the kid's device with ?all=1; the kid only sees their own
	var all []Device
	_ = json.Unmarshal(e.do("GET", "/api/devices?all=1", nil, 200), &all)
	if len(all) != 1 || all[0].User != "kid" {
		t.Fatalf("admin device list: %+v", all)
	}
	// an admin password reset (account recovery) revokes the user's devices
	e.do("PATCH", "/api/users/"+itoa(mustUser(t, e, "kid").ID), map[string]any{"password": "anotherlong1"}, 200)
	if r := d.do("GET", "/api/ls", nil, nil); r.outer != 401 {
		t.Fatalf("device after account recovery: %d", r.outer)
	}
}

func TestDeviceChannelOnPublicPortAndPastAdminList(t *testing.T) {
	e := setup(t)
	d := pairedDevice(t, e, "Phone")
	pub := httptest.NewServer(e.app.publicRoutes())
	defer pub.Close()
	// the public port serves the channel, and still nothing else
	if code, _ := publicGet(t, http.DefaultClient, pub.URL+"/api/ls?path=DOCS"); code != 404 {
		t.Fatalf("public API: %d", code)
	}
	d.base = pub.URL
	if d.hello() != 200 {
		t.Fatal("hello on the public port")
	}
	if r := d.do("GET", "/api/ls?path=DOCS", nil, nil); r.status != 200 {
		t.Fatalf("tunnel on the public port: %+v", r)
	}
	var devs []Device
	_ = json.Unmarshal(e.do("GET", "/api/devices", nil, 200), &devs)
	if devs[0].LastVia != "public" {
		t.Fatalf("last via: %+v", devs[0])
	}
	// the admin network list keeps the web API to the LAN, not the phone
	e.app.st.set("admin_cidrs", "10.9.9.0/24")
	if code, _ := publicGet(t, e.c, e.srv.URL+"/api/ls?path=DOCS"); code != 403 {
		t.Fatalf("admin list on the LAN API: %d", code)
	}
	d.base = e.srv.URL
	if r := d.do("GET", "/api/ls?path=DOCS", nil, nil); r.status != 200 {
		t.Fatalf("tunnel past the admin list: %+v", r)
	}
}

func TestPairingNeedsAnAddress(t *testing.T) {
	e := setup(t)
	// the test server is on loopback and no public address is set
	b := e.do("POST", "/api/devices/pair", nil, 400)
	if !strings.Contains(string(b), "address") {
		t.Fatalf("pairing without an address: %s", b)
	}
	// pairing needs a browser session, not an API token
	var tok struct{ Token string }
	_ = json.Unmarshal(e.do("POST", "/api/tokens", map[string]string{"name": "x"}, 200), &tok)
	if code, _ := e.bearer(tok.Token, "POST", "/api/devices/pair", nil); code != 401 {
		t.Fatalf("pairing with a token: %d", code)
	}
}

func TestSegmentStream(t *testing.T) {
	key, aad := make([]byte, 32), []byte("head")
	_, _ = rand.Read(key)
	for _, n := range []int{0, 1, chSegMax - 1, chSegMax, chSegMax + 1, 3*chSegMax + 77} {
		msg := make([]byte, n)
		_, _ = rand.Read(msg)
		var buf bytes.Buffer
		w := newSegWriter(&buf, key, aad, nil)
		_, _ = w.Write(msg[:n/2])
		_ = w.Flush()
		_, _ = w.Write(msg[n/2:])
		_ = w.Close()
		got, err := io.ReadAll(newSegReader(bytes.NewReader(buf.Bytes()), key, aad, nil))
		if err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("size %d: %v", n, err)
		}
		// cutting off the last segment is detected
		enc := buf.Bytes()
		last := 0
		for off := 0; off < len(enc); {
			l := int(binary.BigEndian.Uint32(enc[off:]) &^ chLast)
			last = off
			off += 4 + l
		}
		if _, err := io.ReadAll(newSegReader(bytes.NewReader(enc[:last]), key, aad, nil)); !errors.Is(err, errChTruncated) {
			t.Fatalf("size %d without its last segment: %v", n, err)
		}
		// marking an earlier segment as the last one fails authentication
		if last > 0 {
			bad := append([]byte{}, enc...)
			bad[0] |= 0x80
			if _, err := io.ReadAll(newSegReader(bytes.NewReader(bad), key, aad, nil)); !errors.Is(err, errChAuth) {
				t.Fatalf("size %d with a forged last flag: %v", n, err)
			}
		}
		// bytes after the last segment are refused
		if _, err := io.ReadAll(newSegReader(bytes.NewReader(append(append([]byte{}, enc...), 0)), key, aad, nil)); !errors.Is(err, errChFrame) {
			t.Fatalf("size %d with trailing bytes: %v", n, err)
		}
		// a different header (another message's) doesn't decrypt
		if _, err := io.ReadAll(newSegReader(bytes.NewReader(enc), key, []byte("other"), nil)); !errors.Is(err, errChAuth) {
			t.Fatalf("size %d under another header: %v", n, err)
		}
	}
}

func TestReplayWindow(t *testing.T) {
	s := &devSession{seen: map[uint64]bool{}}
	if s.accept(0) {
		t.Fatal("message 0")
	}
	for _, n := range []uint64{1, 3, 2, 10} {
		if !s.accept(n) {
			t.Fatalf("fresh %d refused", n)
		}
	}
	if s.accept(3) {
		t.Fatal("replayed 3")
	}
	if !s.accept(replayWindow + 9) {
		t.Fatal("jump ahead")
	}
	if s.accept(9) {
		t.Fatal("too old")
	}
	if !s.accept(20) {
		t.Fatal("inside the window")
	}
	for i := uint64(1); i < 3*replayWindow; i++ {
		s.accept(replayWindow + 9 + i)
	}
	if len(s.seen) > 2*replayWindow+1 {
		t.Fatalf("window not pruned: %d", len(s.seen))
	}
}

func TestDeviceKeysArePrivateAtRest(t *testing.T) {
	e := setup(t)
	pairedDevice(t, e, "Phone")
	fi, err := os.Stat(filepath.Join(e.app.cfg.StateDir, "t.db"))
	if err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("database mode: %v %v", fi.Mode(), err)
	}
	var key []byte
	_ = e.app.st.db.QueryRow(`SELECT key FROM devices`).Scan(&key)
	if len(key) != 32 {
		t.Fatalf("device key: %s", hex.EncodeToString(key))
	}
}
