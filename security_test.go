package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tlsEnv runs a handler the way main does: HTTPS only, with the self-signed
// certificate Burrow makes when none is configured.
type tlsEnv struct {
	url   string // https://localhost:port
	addr  string
	roots *x509.CertPool
	certs *certSource
}

func startTLS(t *testing.T, h http.Handler, stateDir string, pqOnly bool) *tlsEnv {
	t.Helper()
	certs, err := newCertSource(Config{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ErrorLog: quietLog}
	go func() { _ = serveTLSOnly(srv, ln, tlsConfig(certs, pqOnly)) }()
	t.Cleanup(func() { _ = srv.Close() })
	pemBytes, _ := os.ReadFile(certs.certFile)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pemBytes)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return &tlsEnv{url: "https://localhost:" + port, addr: ln.Addr().String(), roots: roots, certs: certs}
}

func (te *tlsEnv) client() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: te.roots}, ForceAttemptHTTP2: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       10 * time.Second,
	}
}

func TestTLSOnlyAndPostQuantum(t *testing.T) {
	e := setup(t)
	te := startTLS(t, e.app.routes(), t.TempDir(), false)

	// a current client gets TLS 1.3 with hybrid post-quantum key exchange, over HTTP/2
	conn, err := tls.Dial("tcp", te.addr, &tls.Config{ServerName: "localhost", RootCAs: te.roots, NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatal(err)
	}
	st := conn.ConnectionState()
	conn.Close()
	if st.Version != tls.VersionTLS13 || st.CurveID != tls.X25519MLKEM768 || st.NegotiatedProtocol != "h2" {
		t.Fatalf("got %s, %s, %q", tls.VersionName(st.Version), st.CurveID, st.NegotiatedProtocol)
	}
	// a client without post-quantum support still connects (classical X25519)
	conn, err = tls.Dial("tcp", te.addr, &tls.Config{ServerName: "localhost", RootCAs: te.roots, CurvePreferences: []tls.CurveID{tls.X25519}})
	if err != nil {
		t.Fatalf("classical client refused without PQ-only: %v", err)
	}
	conn.Close()
	// TLS 1.2 and older are refused
	for _, v := range []uint16{tls.VersionTLS12, tls.VersionTLS11} {
		if c, err := tls.Dial("tcp", te.addr, &tls.Config{ServerName: "localhost", RootCAs: te.roots, MinVersion: v, MaxVersion: v}); err == nil {
			c.Close()
			t.Fatalf("%s accepted", tls.VersionName(v))
		}
	}
	// HSTS and the usual headers over HTTPS
	resp, err := te.client().Get(te.url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 || !strings.Contains(resp.Header.Get("Strict-Transport-Security"), "max-age=") {
		t.Fatalf("proto %d, HSTS %q", resp.ProtoMajor, resp.Header.Get("Strict-Transport-Security"))
	}

	// PQ-only mode refuses classical key exchange
	tp := startTLS(t, e.app.routes(), t.TempDir(), true)
	if c, err := tls.Dial("tcp", tp.addr, &tls.Config{ServerName: "localhost", RootCAs: tp.roots, CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}}); err == nil {
		c.Close()
		t.Fatal("PQ-only accepted a classical client")
	}
	if c, err := tls.Dial("tcp", tp.addr, &tls.Config{ServerName: "localhost", RootCAs: tp.roots}); err != nil {
		t.Fatalf("PQ-only refused a PQ client: %v", err)
	} else {
		c.Close()
	}
}

// TestPlainHTTPServesNothing: plain HTTP on an HTTPS port only ever gets a redirect
// (browsers) or a refusal (proxies), never content.
func TestPlainHTTPServesNothing(t *testing.T) {
	e := setup(t)
	te := startTLS(t, e.app.routes(), t.TempDir(), false)
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	httpURL := "http://" + te.addr
	for _, p := range []string{"/", "/api/raw?path=DOCS/notes.txt", "/s/whatever"} {
		resp, err := plain.Get(httpURL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 308 || !strings.HasPrefix(resp.Header.Get("Location"), "https://") || len(body) != 0 {
			t.Fatalf("%s: %d %q %q", p, resp.StatusCode, resp.Header.Get("Location"), body)
		}
	}
	req, _ := http.NewRequest("GET", httpURL+"/s/whatever", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("proxied plain request: %d", resp.StatusCode)
	}
	// a client that connects and says nothing is dropped, not held forever
	c, err := net.Dial("tcp", te.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("silent connection got data")
	}
}

func TestCertificateReload(t *testing.T) {
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := writeSelfSigned(cf, kf); err != nil {
		t.Fatal(err)
	}
	cs, err := newCertSource(Config{TLSCert: cf, TLSKey: kf})
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := cs.get(nil)
	before := certFingerprint(c1)
	// a renewal writes new files; a half-written one must not replace a good certificate
	_ = os.WriteFile(cf, []byte("garbage"), 0o644)
	cs.checked = time.Time{}
	if c, _ := cs.get(nil); certFingerprint(c) != before {
		t.Fatal("broken renewal replaced the certificate")
	}
	_ = writeSelfSigned(cf, kf)
	cs.checked = time.Time{}
	if c, _ := cs.get(nil); certFingerprint(c) == before {
		t.Fatal("renewed certificate not picked up")
	}
	if _, err := newCertSource(Config{TLSCert: cf}); err == nil {
		t.Fatal("certificate without key accepted")
	}
	// the self-signed certificate is reused across restarts, so a proxy can pin it
	sd := t.TempDir()
	a1, _ := newCertSource(Config{StateDir: sd})
	a2, _ := newCertSource(Config{StateDir: sd})
	x1, _ := a1.get(nil)
	x2, _ := a2.get(nil)
	if certFingerprint(x1) != certFingerprint(x2) {
		t.Fatal("self-signed certificate changed on restart")
	}
	kfi, _ := os.Stat(filepath.Join(sd, "tls", "burrow.key"))
	if kfi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", kfi.Mode().Perm())
	}
	b, _ := os.ReadFile(filepath.Join(sd, "tls", "burrow.crt"))
	blk, _ := pem.Decode(b)
	crt, _ := x509.ParseCertificate(blk.Bytes)
	if crt.VerifyHostname("burrow") != nil {
		t.Fatal("self-signed certificate doesn't cover the name proxies pin")
	}
}

// TestPublicPortSurface: the public port serves share links and their assets only,
// unless an admin turns on the web UI or MCP there.
func TestPublicPortSurface(t *testing.T) {
	e := setup(t)
	var s Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}, 200), &s)
	pub := httptest.NewServer(e.app.publicRoutes())
	defer pub.Close()
	status := func(method, p string) int {
		req, _ := http.NewRequest(method, pub.URL+p, strings.NewReader(`{}`))
		req.Header.Set("X-Burrow", "1")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	closed := []string{"GET /", "GET /api/state", "GET /api/ls?path=", "GET /api/raw?path=DOCS/notes.txt", "POST /api/login", "POST /api/setup",
		"GET /api/roots/available", "POST /mcp", "GET /healthz", "GET /assets/app.js", "GET /assets/app.css", "GET /assets/index.html", "GET /assets/FONT-LICENSE.txt"}
	check := func(want int, routes []string) {
		t.Helper()
		for _, rt := range routes {
			m, p, _ := strings.Cut(rt, " ")
			if got := status(m, p); got != want {
				t.Fatalf("%s: %d, want %d", rt, got, want)
			}
		}
	}
	check(404, closed)
	check(200, []string{"GET /s/" + s.ID, "GET /s/" + s.ID + "/raw", "GET /assets/share.css", "GET /assets/base.css", "GET /assets/share.js", "GET /favicon.svg"})

	e.do("PUT", "/api/settings", map[string]any{"public_mcp": true}, 200)
	if got := status("POST", "/mcp"); got != 401 {
		t.Fatalf("MCP on: %d", got)
	}
	check(404, []string{"GET /", "GET /api/state"})
	e.do("PUT", "/api/settings", map[string]any{"public_ui": true}, 200)
	check(200, []string{"GET /", "GET /api/state", "GET /assets/app.js"})
	if got := status("GET", "/api/ls?path="); got != 401 {
		t.Fatalf("API without sign-in: %d", got)
	}
	e.do("PUT", "/api/settings", map[string]any{"public_ui": false, "public_mcp": false}, 200)
	check(404, closed)
	// the setting survives a restart
	e.do("PUT", "/api/settings", map[string]any{"public_ui": true}, 200)
	a2, _ := newApp(e.app.cfg, e.app.st)
	if !a2.publicUI.Load() || a2.publicMCP.Load() {
		t.Fatal("public port settings not reloaded")
	}
}

func TestSetupNeedsKeyUnlessLocal(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{DataDir: dir, DropDir: filepath.Join(dir, "drop"), StateDir: dir, ScanInterval: time.Hour, TrustedProxies: []string{"127.0.0.1/32"}}
	st, _ := openStore(filepath.Join(dir, "s.db"))
	app, _ := newApp(cfg, st)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	post := func(body map[string]any, hdr map[string]string) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", srv.URL+"/api/setup", bytes.NewReader(b))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	in := map[string]any{"user": "admin", "password": "longenough1"}
	// anything relayed by a proxy needs the key, even from a "local" address
	for _, h := range []map[string]string{{"X-Forwarded-For": "192.168.1.20"}, {"X-Real-IP": "10.0.0.2"}, {"Forwarded": "for=10.0.0.2"}} {
		if c := post(in, h); c != 403 {
			t.Fatalf("setup via proxy %v without key: %d", h, c)
		}
	}
	r, _ := http.Get(srv.URL + "/api/state")
	var stt map[string]any
	_ = json.NewDecoder(r.Body).Decode(&stt)
	r.Body.Close()
	if _, ok := stt["data_dir"]; ok {
		t.Fatal("data_dir shown before sign-in")
	}
	in["key"] = app.setupKey
	if c := post(in, map[string]string{"X-Forwarded-For": "203.0.113.9"}); c != 200 {
		t.Fatalf("setup with key: %d", c)
	}
}

func TestClientIPBehindProxy(t *testing.T) {
	a := &App{}
	for _, c := range []string{"127.0.0.1/32"} {
		_, n, _ := net.ParseCIDR(c)
		a.proxyNets = append(a.proxyNets, n)
	}
	for _, tc := range []struct {
		peer, xff, xri, want string
	}{
		{"127.0.0.1:1", "6.6.6.6, 203.0.113.9", "", "203.0.113.9"}, // visitor's own header can't win over the proxy's
		{"127.0.0.1:1", "203.0.113.9", "", "203.0.113.9"},
		{"198.51.100.7:1", "1.2.3.4", "1.2.3.4", "198.51.100.7"}, // untrusted peer: headers ignored
		{"127.0.0.1:1", "", "203.0.113.5", "203.0.113.5"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if tc.xri != "" {
			r.Header.Set("X-Real-IP", tc.xri)
		}
		if got := a.clientIP(r); got != tc.want {
			t.Fatalf("%+v: got %s", tc, got)
		}
	}
}

func TestSettingsValidation(t *testing.T) {
	e := setup(t)
	e.do("PUT", "/api/settings", map[string]any{"public_url": "http://share.example.com"}, 400)
	e.do("PUT", "/api/settings", map[string]any{"public_url": "https://share.example.com/?x=1"}, 400)
	var st Settings
	_ = json.Unmarshal(e.do("PUT", "/api/settings", map[string]any{"public_url": "share.example.com"}, 200), &st)
	if st.PublicURL != "https://share.example.com" {
		t.Fatalf("public url: %q", st.PublicURL)
	}
	// a refused admin list keeps the previous one instead of clearing it
	e.do("PUT", "/api/settings", map[string]any{"admin_cidrs": "127.0.0.0/8"}, 200)
	e.do("PUT", "/api/settings", map[string]any{"admin_cidrs": "10.9.9.0/24"}, 400) // would lock this client out
	e.do("PUT", "/api/settings", map[string]any{"admin_cidrs": "not-an-ip"}, 400)
	_ = json.Unmarshal(e.do("GET", "/api/settings", nil, 200), &st)
	if st.AdminCIDRs != "127.0.0.0/8" {
		t.Fatalf("admin list after refusals: %q", st.AdminCIDRs)
	}
}

func TestGuessingIsThrottled(t *testing.T) {
	e := setup(t)
	var s, pw Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}, 200), &s)
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt", "password": "right-password"}, 200), &pw)
	get := func(p, ip string) int {
		req, _ := http.NewRequest("GET", e.srv.URL+p, nil)
		req.Header.Set("X-Forwarded-For", ip) // the test server's peer (127.0.0.1) is a trusted proxy
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	// unknown link ids: after enumLimit misses, even a real link is refused for that IP
	for i := 0; i < enumLimit; i++ {
		if c := get(fmt.Sprintf("/s/guess%04d", i), "203.0.113.1"); c != 404 {
			t.Fatalf("miss %d: %d", i, c)
		}
	}
	if c := get("/s/"+s.ID, "203.0.113.1"); c != 429 {
		t.Fatalf("after enumeration: %d", c)
	}
	if c := get("/s/"+s.ID, "203.0.113.2"); c != 200 {
		t.Fatalf("other visitor blocked: %d", c)
	}
	// password guesses spread over many IPs still hit the per-link limit
	try := func(ip, password string) int {
		req, _ := http.NewRequest("POST", e.srv.URL+"/s/"+pw.ID+"/auth", strings.NewReader(url.Values{"password": {password}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-For", ip)
		r, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	for i := 0; i < linkPWLimit; i++ {
		if c := try(fmt.Sprintf("198.51.100.%d", i%200+1), "wrong"); c != 401 {
			t.Fatalf("guess %d: %d", i, c)
		}
	}
	if c := try("192.0.2.77", "right-password"); c != 429 {
		t.Fatalf("link not locked after distributed guessing: %d", c)
	}
}

func TestShareResponsesAreNotCached(t *testing.T) {
	e := setup(t)
	var s, x Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS"}, 200), &s)
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}, 200), &x)
	e.do("PATCH", "/api/shares/"+x.ID, map[string]any{"revoked": true}, 200)
	for _, p := range []string{"/s/" + s.ID, "/s/" + s.ID + "/dl?p=notes.txt", "/s/" + s.ID + "/raw?p=notes.txt", "/s/" + s.ID + "/zip", "/s/nope-nope-nope", "/s/" + x.ID} {
		r, err := http.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") || r.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s: Cache-Control %q, Referrer-Policy %q", p, r.Header.Get("Cache-Control"), r.Header.Get("Referrer-Policy"))
		}
	}
	// a turned-off link gives nothing away, its QR code included
	if r, _ := http.Get(e.srv.URL + "/s/" + x.ID + "/qr.png"); r.StatusCode != 410 {
		t.Fatalf("qr of a turned-off link: %d", r.StatusCode)
	}
}

func TestUploadsRefusedWhenDiskNearlyFull(t *testing.T) {
	e := setup(t)
	var d Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "drop", "label": "in"}, 200), &d)
	old := freeSpace
	defer func() { freeSpace = old }()
	freeSpace = func(string) (uint64, uint64, error) { return 512 << 20, 100 << 30, nil } // 512 MB left of 100 GB
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("files", "a.txt")
	_, _ = w.Write([]byte("hello"))
	mw.Close()
	r, err := http.Post(e.srv.URL+"/s/"+d.ID+"/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("upload with a nearly full disk: %d", r.StatusCode)
	}
	if left, _ := os.ReadDir(e.drop); len(left) != 0 {
		t.Fatalf("something was written: %v", left)
	}
}

// TestOpenNoFollow: a symlink swapped in after the path check is not followed.
func TestOpenNoFollow(t *testing.T) {
	e := setup(t)
	abs, _, err := e.app.resolve("DOCS/Taxes/2024-return.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if f, err := e.app.openNoFollow(abs); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}
	// swap the folder for a symlink to a library that isn't enabled
	taxes := filepath.Join(e.data, "DOCS", "Taxes")
	_ = os.Rename(taxes, taxes+".real")
	_ = os.MkdirAll(filepath.Join(e.data, "PRIVATE"), 0o755)
	_ = os.WriteFile(filepath.Join(e.data, "PRIVATE", "2024-return.pdf"), []byte("secret"), 0o644)
	if err := os.Symlink(filepath.Join(e.data, "PRIVATE"), taxes); err != nil {
		t.Fatal(err)
	}
	if f, err := e.app.openNoFollow(abs); err == nil {
		f.Close()
		t.Fatal("followed a symlink swapped in after the check")
	}
	if f, err := e.app.openNoFollow("/etc/passwd"); err == nil {
		f.Close()
		t.Fatal("opened a file outside the library")
	}
}

// TestVerifyPublic runs the verify-public command against a local public port.
func TestVerifyPublic(t *testing.T) {
	e := setup(t)
	te := startTLS(t, e.app.publicRoutes(), t.TempDir(), false)
	verifyRoots = te.roots
	defer func() { verifyRoots = nil }()
	var s Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}, 200), &s)
	var out bytes.Buffer
	if !verifyPublic(&out, te.url, te.url+"/s/"+s.ID) {
		t.Fatalf("verify-public failed:\n%s", out.String())
	}
	for _, want := range []string{"hybrid post-quantum", "TLS 1.3", "HSTS is on", "not reachable from the internet", "path-traversal attempts reached nothing"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
	// pointing the proxy at the full app is flagged
	out.Reset()
	tl := startTLS(t, e.app.routes(), t.TempDir(), false)
	verifyRoots = tl.roots
	verifyPublic(&out, tl.url, "")
	if !strings.Contains(out.String(), "web UI or API is public") {
		t.Fatalf("exposed UI not flagged:\n%s", out.String())
	}
}
