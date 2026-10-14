package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type tenv struct {
	t    *testing.T
	app  *App
	srv  *httptest.Server
	c    *http.Client
	data string
	drop string
}

func setup(t *testing.T) *tenv { t.Helper(); return setupOn(t, false) }

// setupOn is setup, served over TLS (a self-signed test certificate) when tlsOn.
func setupOn(t *testing.T, tlsOn bool) *tenv {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	write := func(rel, body string) {
		p := filepath.Join(data, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("DOCS/Taxes/2024-return.pdf", "pdf")
	write("DOCS/notes.txt", "hello notes")
	write("DOCS/.zfs/snap/secret.txt", "nope")
	write("PRIVATE/keys.txt", "secret")
	write("PROJECTS/lamp/shade.stl", "solid")
	write("PROJECTS/lamp/sub/inner.txt", "inner")
	_ = os.Symlink(filepath.Join(data, "PRIVATE"), filepath.Join(data, "DOCS", "link-to-private"))
	cfg := Config{DataDir: data, DropDir: filepath.Join(dir, "drop"), StateDir: filepath.Join(dir, "state"), ScanInterval: time.Hour, TrustedProxies: []string{"127.0.0.1/32"}}
	_ = os.MkdirAll(cfg.StateDir, 0o755)
	st, err := openStore(filepath.Join(cfg.StateDir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := newApp(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	if tlsOn {
		srv = httptest.NewTLSServer(app.routes())
	} else {
		srv = httptest.NewServer(app.routes())
	}
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if tlsOn {
		c.Transport = srv.Client().Transport
	}
	e := &tenv{t: t, app: app, srv: srv, c: c, data: data, drop: cfg.DropDir}
	// first-launch setup from loopback needs no key; only DOCS and PROJECTS are enabled
	e.do("POST", "/api/setup", map[string]any{"user": "admin", "password": "longenough1", "roots": []string{"DOCS", "PROJECTS"}}, 200)
	app.idx.fullScan()
	return e
}

func (e *tenv) do(method, path string, body any, want int) []byte {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("X-Burrow", "1")
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if want != 0 && resp.StatusCode != want {
		e.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, out)
	}
	return out
}

func TestPathsStayInsideEnabledRoots(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"PRIVATE/keys.txt", "../etc/passwd", "DOCS/../PRIVATE/keys.txt", "DOCS/.zfs/snap/secret.txt", "DOCS/link-to-private/keys.txt", "/etc/passwd"} {
		e.do("GET", "/api/raw?path="+url.QueryEscape(p), nil, 404)
	}
	if got := string(e.do("GET", "/api/raw?path=DOCS/notes.txt", nil, 200)); got != "hello notes" {
		t.Fatalf("raw: %q", got)
	}
	var ls struct{ Entries []Entry }
	_ = json.Unmarshal(e.do("GET", "/api/ls?path=", nil, 200), &ls)
	if len(ls.Entries) != 2 {
		t.Fatalf("roots listed: %+v", ls.Entries)
	}
	_ = json.Unmarshal(e.do("GET", "/api/ls?path=DOCS", nil, 200), &ls)
	for _, en := range ls.Entries {
		if en.Name == ".zfs" || en.Name == "link-to-private" {
			t.Fatalf("excluded entry listed: %s", en.Name)
		}
	}
}

func TestSearch(t *testing.T) {
	e := setup(t)
	var r SearchResult
	_ = json.Unmarshal(e.do("GET", "/api/search?q=shade", nil, 200), &r)
	if len(r.Entries) != 1 || r.Entries[0].Path != "PROJECTS/lamp/shade.stl" {
		t.Fatalf("shade: %+v", r.Entries)
	}
	_ = json.Unmarshal(e.do("GET", "/api/search?q=keys", nil, 200), &r)
	if len(r.Entries) != 0 {
		t.Fatalf("disabled root leaked into search: %+v", r.Entries)
	}
	_ = json.Unmarshal(e.do("GET", "/api/search?q=secret", nil, 200), &r)
	if len(r.Entries) != 0 {
		t.Fatalf("excluded folder leaked into search: %+v", r.Entries)
	}
	_ = json.Unmarshal(e.do("GET", "/api/search?q=lamp+-stl", nil, 200), &r)
	for _, x := range r.Entries {
		if strings.Contains(x.Path, ".stl") {
			t.Fatalf("negated term matched: %s", x.Path)
		}
	}
	_ = json.Unmarshal(e.do("GET", "/api/search?q=ext:pdf", nil, 200), &r)
	if len(r.Entries) != 1 {
		t.Fatalf("ext filter: %+v", r.Entries)
	}
	_ = json.Unmarshal(e.do("GET", "/api/search?q=24", nil, 200), &r) // short term uses LIKE
	if len(r.Entries) != 1 {
		t.Fatalf("short term: %+v", r.Entries)
	}
}

func TestChangeLogAfterInitialScan(t *testing.T) {
	e := setup(t)
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "new.txt"), []byte("x"), 0o644)
	_ = os.Remove(filepath.Join(e.data, "DOCS", "notes.txt"))
	e.app.idx.fullScan()
	admin, _ := e.app.userByName("admin")
	cs, _ := e.app.changes(admin, 0, "", "", 50)
	kinds := map[string]string{}
	for _, c := range cs {
		kinds[c.Path] = c.Kind
	}
	if kinds["DOCS/new.txt"] != "added" || kinds["DOCS/notes.txt"] != "deleted" {
		t.Fatalf("changes: %+v", cs)
	}
}

func TestShareScopeAndLimits(t *testing.T) {
	e := setup(t)
	var s Share
	// two downloads: the preview of inner.txt below uses one (this client keeps no cookies)
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "PROJECTS/lamp", "expires_hours": 1, "max_downloads": 2}, 200), &s)
	pub := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) int {
		r, err := pub.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if c := get("/s/" + s.ID + "/raw?p=" + url.QueryEscape("../../DOCS/notes.txt")); c != 404 {
		t.Fatalf("escape from share: %d", c)
	}
	if c := get("/s/" + s.ID + "/raw?p=sub/inner.txt"); c != 200 {
		t.Fatalf("inner file: %d", c)
	}
	if c := get("/s/" + s.ID + "/dl?p=shade.stl"); c != 200 {
		t.Fatalf("first download: %d", c)
	}
	if c := get("/s/" + s.ID + "/dl?p=shade.stl"); c != 410 {
		t.Fatalf("limit not enforced: %d", c)
	}
	// password + revoke
	var p Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt", "password": "pw-123456"}, 200), &p)
	if c := get("/s/" + p.ID + "/raw"); c != 401 {
		t.Fatalf("password not required: %d", c)
	}
	e.do("PATCH", "/api/shares/"+p.ID, map[string]any{"revoked": true}, 200)
	if c := get("/s/" + p.ID); c != 410 {
		t.Fatalf("revoked still works: %d", c)
	}
	// expired
	var x Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt", "expires_at": time.Now().Add(-time.Minute).Unix()}, 200), &x)
	if c := get("/s/" + x.ID); c != 410 {
		t.Fatalf("expired still works: %d", c)
	}
	// can't share a disabled root
	e.do("POST", "/api/shares", map[string]any{"path": "PRIVATE/keys.txt"}, 400)
}

func TestFileDropSanitizesNames(t *testing.T) {
	e := setup(t)
	var s Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "drop", "label": "../../From Mom"}, 200), &s)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, n := range []string{"../../evil.sh", "photo.jpg", "photo.jpg"} {
		w, _ := mw.CreateFormFile("files", n)
		_, _ = w.Write([]byte("data"))
	}
	mw.Close()
	resp, err := http.Post(e.srv.URL+"/s/"+s.ID+"/upload", mw.FormDataContentType(), &buf)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload: %v %d", err, resp.StatusCode)
	}
	got, _ := filepath.Glob(filepath.Join(e.drop, "*", "*"))
	if len(got) != 3 {
		t.Fatalf("files: %v", got)
	}
	for _, g := range got {
		if !strings.HasPrefix(g, e.drop+string(os.PathSeparator)) {
			t.Fatalf("escaped drop dir: %s", g)
		}
	}
	if _, err := os.Stat(filepath.Join(e.drop, "From Mom", "photo (2).jpg")); err != nil {
		t.Fatalf("dedupe: %v %v", err, got)
	}
}

func TestAuthAndCSRF(t *testing.T) {
	e := setup(t)
	anon := &http.Client{}
	r, _ := anon.Get(e.srv.URL + "/api/search?q=a")
	if r.StatusCode != 401 {
		t.Fatalf("anon search: %d", r.StatusCode)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/rescan", nil) // cookie but no X-Burrow
	r, _ = e.c.Do(req)
	if r.StatusCode != 403 {
		t.Fatalf("csrf: %d", r.StatusCode)
	}
	e.do("POST", "/api/setup", map[string]any{"user": "x", "password": "longenough2"}, 409)
}

func TestMCP(t *testing.T) {
	e := setup(t)
	var tok struct{ Token string }
	_ = json.Unmarshal(e.do("POST", "/api/tokens", map[string]any{"name": "test"}, 200), &tok)
	call := func(body string) map[string]any {
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return out
	}
	init := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if init["result"] == nil {
		t.Fatalf("init: %v", init)
	}
	list := call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if n := len(list["result"].(map[string]any)["tools"].([]any)); n != len(mcpTools) {
		t.Fatalf("tools: %d", n)
	}
	res := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_files","arguments":{"query":"lamp shade"}}}`)
	txt := res["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(txt, "shade.stl") {
		t.Fatalf("search via mcp: %s", txt)
	}
	res = call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_share_link","arguments":{"path":"DOCS/notes.txt","expires_hours":2}}}`)
	txt = res["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(txt, "/s/") {
		t.Fatalf("share via mcp: %s", txt)
	}
	res = call(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"read_text_file","arguments":{"path":"PRIVATE/keys.txt"}}}`)
	if res["result"].(map[string]any)["isError"] != true {
		t.Fatalf("read outside roots allowed: %v", res)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{}`))
	r, _ := http.DefaultClient.Do(req)
	if r.StatusCode != 401 {
		t.Fatalf("mcp without token: %d", r.StatusCode)
	}
}

func TestExifOrientation(t *testing.T) {
	if exifOrientation([]byte{0xFF, 0xD8, 0xFF, 0xDA}) != 1 {
		t.Fatal("no exif should be 1")
	}
}

func TestSharedFolder(t *testing.T) {
	e := setup(t)
	var s Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "Trip", "items": []string{"DOCS/notes.txt", "PROJECTS/lamp"}, "password": "pw-123456", "allow_upload": true}, 200), &s)
	e.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "Bad", "items": []string{"PRIVATE/keys.txt"}}, 400)
	e.do("POST", "/api/shares/"+s.ID+"/items", map[string]any{"paths": []string{"PRIVATE"}}, 400)
	jar, _ := cookiejar.New(nil)
	pub := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) (int, string) {
		r, err := pub.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if c, _ := get("/s/" + s.ID + "?p=notes.txt"); c != 401 {
		t.Fatalf("password not required: %d", c)
	}
	r, _ := pub.PostForm(e.srv.URL+"/s/"+s.ID+"/auth", url.Values{"password": {"pw-123456"}})
	r.Body.Close()
	if c, body := get("/s/" + s.ID); c != 200 || !strings.Contains(body, "notes.txt") || !strings.Contains(body, "lamp") {
		t.Fatalf("root listing: %d", c)
	}
	if c, body := get("/s/" + s.ID + "/raw?p=lamp/sub/inner.txt"); c != 200 || body != "inner" {
		t.Fatalf("nested item: %d %q", c, body)
	}
	for _, bad := range []string{"lamp/../../DOCS/Taxes/2024-return.pdf", "../PRIVATE/keys.txt", "notes.txt/../../PRIVATE/keys.txt", "PRIVATE/keys.txt"} {
		if c, _ := get("/s/" + s.ID + "/raw?p=" + url.QueryEscape(bad)); c == 200 {
			t.Fatalf("escape via %q", bad)
		}
	}
	// add more later: same link shows it
	e.do("POST", "/api/shares/"+s.ID+"/items", map[string]any{"paths": []string{"DOCS/Taxes"}}, 200)
	if c, body := get("/s/" + s.ID + "/raw?p=Taxes/2024-return.pdf"); c != 200 || body != "pdf" {
		t.Fatalf("added later: %d", c)
	}
	// recipient upload lands in the collection's own folder and is listed
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, _ := mw.CreateFormFile("files", "../hello.txt")
	_, _ = w.Write([]byte("hi"))
	mw.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+"/s/"+s.ID+"/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	r, err := pub.Do(req)
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("upload: %v %d", err, r.StatusCode)
	}
	if c, body := get("/s/" + s.ID + "/raw?p=hello.txt"); c != 200 || body != "hi" {
		t.Fatalf("uploaded file: %d %q", c, body)
	}
	// removing an item hides it
	e.do("DELETE", "/api/shares/"+s.ID+"/items?path=DOCS/notes.txt", nil, 200)
	if c, _ := get("/s/" + s.ID + "/raw?p=notes.txt"); c == 200 {
		t.Fatal("removed item still served")
	}
	// zip of the whole collection
	if c, _ := get("/s/" + s.ID + "/zip"); c != 200 {
		t.Fatalf("zip: %d", c)
	}
	// delete with purge removes the upload folder
	var full Share
	_ = json.Unmarshal(e.do("GET", "/api/shares?path=", nil, 200)[0:0], &full)
	e.do("DELETE", "/api/shares/"+s.ID+"?purge=1", nil, 200)
	if left, _ := filepath.Glob(filepath.Join(e.drop, "Shared - Trip*")); len(left) != 0 {
		t.Fatalf("purge left %v", left)
	}
	// owner upload API needs an existing collection
	e.do("DELETE", "/api/shares/"+s.ID+"/uploads?name=..", nil, 404)
}

func TestTwoFactor(t *testing.T) {
	e := setup(t)
	var st struct{ Secret string }
	_ = json.Unmarshal(e.do("POST", "/api/2fa/start", nil, 200), &st)
	secret, _ := b32.DecodeString(st.Secret)
	code := totpCode(secret, uint64(time.Now().Unix()/30))
	var en struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	e.do("POST", "/api/2fa/enable", map[string]any{"code": code, "password": "wrong-password"}, 401)
	_ = json.Unmarshal(e.do("POST", "/api/2fa/enable", map[string]any{"code": code, "password": "longenough1"}, 200), &en)
	if len(en.RecoveryCodes) != 10 {
		t.Fatal("recovery codes")
	}
	login := func(code string) int {
		b, _ := json.Marshal(map[string]string{"user": "admin", "password": "longenough1", "code": code})
		r, _ := postJSON(e.srv.URL+"/api/login", b)
		r.Body.Close()
		return r.StatusCode
	}
	if c := login(""); c != 401 {
		t.Fatalf("no code accepted: %d", c)
	}
	if c := login("000000"); c != 401 {
		t.Fatalf("bad code accepted: %d", c)
	}
	if c := login(code); c != 401 {
		t.Fatalf("replayed enrollment code accepted: %d", c)
	}
	if c := login(en.RecoveryCodes[0]); c != 200 {
		t.Fatalf("recovery code rejected: %d", c)
	}
	if c := login(en.RecoveryCodes[0]); c != 401 {
		t.Fatalf("recovery code reused: %d", c)
	}
	// API tokens can't manage account security
	var tok struct{ Token string }
	_ = json.Unmarshal(e.do("POST", "/api/tokens", map[string]any{"name": "t"}, 200), &tok)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/tokens", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	r, _ := http.DefaultClient.Do(req)
	if r.StatusCode != 401 {
		t.Fatalf("token minted a token: %d", r.StatusCode)
	}
}

func TestTOTPVector(t *testing.T) {
	// RFC 6238 test vector: secret "12345678901234567890", T=59 -> 94287082 (8 digits) -> last 6: 287082
	if got := totpCode([]byte("12345678901234567890"), 59/30); got != "287082" {
		t.Fatalf("totp: %s", got)
	}
}

func TestServingIsInert(t *testing.T) {
	e := setup(t)
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "evil.html"), []byte("<script>alert(1)</script>"), 0o644)
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "evil.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`), 0o644)
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "a\r\nSet-Cookie: x=1.txt"), []byte("x"), 0o644)
	head := func(p string) http.Header {
		req, _ := http.NewRequest("GET", e.srv.URL+p, nil)
		r, err := e.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", p, r.StatusCode)
		}
		return r.Header
	}
	h := head("/api/raw?path=DOCS/evil.html")
	if !strings.HasPrefix(h.Get("Content-Type"), "text/plain") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("html served as %q", h.Get("Content-Type"))
	}
	if h := head("/api/raw?path=DOCS/evil.svg"); !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("svg without sandbox")
	}
	h = head("/api/raw?path=" + url.QueryEscape("DOCS/a\r\nSet-Cookie: x=1.txt") + "&dl=1")
	if len(h.Values("Set-Cookie")) != 0 || strings.ContainsAny(h.Get("Content-Disposition"), "\r\n") {
		t.Fatalf("header injection: %v", h)
	}
}

// TestDownloadLimit: on a limited link each browser counts once per file, whether it
// previews or downloads; nothing fetches the file without counting.
func TestDownloadLimit(t *testing.T) {
	e := setup(t)
	var s Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt", "max_downloads": 2}, 200), &s)
	newBrowser := func() *http.Client { jar, _ := cookiejar.New(nil); return &http.Client{Jar: jar} }
	a, b, c := newBrowser(), newBrowser(), newBrowser()
	dl := e.srv.URL + "/s/" + s.ID + "/dl"
	raw := e.srv.URL + "/s/" + s.ID + "/raw"
	count := func() int64 { g, _ := e.app.getShare(s.ID); return g.DL }

	// browser a: a range starting mid-file still counts (no free bytes), then resumes free
	if code := rangeGet(t, a, dl, "bytes=4-"); code != 206 || count() != 1 {
		t.Fatalf("mid-file range: %d, count %d", code, count())
	}
	if code := rangeGet(t, a, dl, ""); code != 200 || count() != 1 {
		t.Fatalf("same browser again: %d, count %d", code, count())
	}
	if code := rangeGet(t, a, raw, "bytes=2-"); code != 206 || count() != 1 {
		t.Fatalf("same browser preview: %d, count %d", code, count())
	}
	// browser b: a preview counts too
	if code := rangeGet(t, b, raw, ""); code != 200 || count() != 2 {
		t.Fatalf("preview: %d, count %d", code, count())
	}
	// limit reached: a new browser gets nothing, by any route
	for _, u := range []string{dl, raw} {
		for _, rg := range []string{"", "bytes=0-", "bytes=1-", "bytes=6-"} {
			if code := rangeGet(t, c, u, rg); code != 410 {
				t.Fatalf("%s %q after the limit: %d", u, rg, code)
			}
		}
	}
	// browsers already counted can still resume or seek
	if code := rangeGet(t, a, dl, "bytes=6-"); code != 206 {
		t.Fatalf("resume after the limit: %d", code)
	}
	if code := rangeGet(t, b, raw, "bytes=1-"); code != 206 {
		t.Fatalf("seek after the limit: %d", code)
	}
	if count() != 2 {
		t.Fatalf("count: %d", count())
	}
}

func rangeGet(t *testing.T, c *http.Client, u, rg string) int {
	req, _ := http.NewRequest("GET", u, nil)
	if rg != "" {
		req.Header.Set("Range", rg)
	}
	r, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	return r.StatusCode
}
