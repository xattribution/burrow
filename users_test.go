package main

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// as returns a copy of the test env whose client is signed in as user.
func (e *tenv) as(user, pass string) *tenv {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	cp := *e
	cp.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cp.do("POST", "/api/login", map[string]any{"user": user, "password": pass}, 200)
	return &cp
}

// mkUser creates a user through the admin API; the password is "longenough-<name>".
func (e *tenv) mkUser(name string, admin bool, grants ...Grant) User {
	e.t.Helper()
	if grants == nil {
		grants = []Grant{}
	}
	var u User
	_ = json.Unmarshal(e.do("POST", "/api/users", map[string]any{"username": name, "password": "longenough-" + name, "admin": admin, "grants": grants}, 200), &u)
	return u
}

func (e *tenv) bearer(tok, method, path string, body any) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	out, _ := io.ReadAll(r.Body)
	return r.StatusCode, out
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

func zipNames(t *testing.T, b []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

func publicGet(t *testing.T, c *http.Client, u string) (int, []byte) {
	t.Helper()
	r, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

// bobGrants: read and share DOCS except the Taxes folder; read-only PROJECTS/lamp/sub.
var bobGrants = []Grant{
	{Path: "DOCS", Read: true, Share: true},
	{Path: "DOCS/Taxes", Read: false},
	{Path: "PROJECTS/lamp/sub", Read: true},
}

func TestGrantsLimitBrowsingAndFiles(t *testing.T) {
	e := setup(t)
	e.mkUser("bob", false, bobGrants...)
	bob := e.as("bob", "longenough-bob")

	var ls struct{ Entries []Entry }
	_ = json.Unmarshal(bob.do("GET", "/api/ls?path=", nil, 200), &ls)
	if got := names(ls.Entries); strings.Join(got, ",") != "DOCS,PROJECTS" {
		t.Fatalf("roots: %v", got)
	}
	_ = json.Unmarshal(bob.do("GET", "/api/ls?path=DOCS", nil, 200), &ls)
	if got := names(ls.Entries); strings.Join(got, ",") != "notes.txt" {
		t.Fatalf("DOCS listing shows carved-out folder: %v", got)
	}
	_ = json.Unmarshal(bob.do("GET", "/api/ls?path=PROJECTS/lamp", nil, 200), &ls)
	if got := names(ls.Entries); strings.Join(got, ",") != "sub" {
		t.Fatalf("walk-through folder shows unreadable files: %v", got)
	}
	bob.do("GET", "/api/ls?path=DOCS/Taxes", nil, 403)

	for p, want := range map[string]int{
		"DOCS/notes.txt":              200,
		"DOCS/Taxes/2024-return.pdf":  404,
		"PROJECTS/lamp/shade.stl":     404,
		"PROJECTS/lamp/sub/inner.txt": 200,
		"PRIVATE/keys.txt":            404,
	} {
		bob.do("GET", "/api/raw?path="+url.QueryEscape(p), nil, want)
		bob.do("GET", "/api/info?path="+url.QueryEscape(p), nil, want)
	}

	var sr SearchResult
	for q, want := range map[string]int{"2024": 0, "taxes": 0, "shade": 0, "inner": 1, "notes": 1, "lamp": 2} {
		_ = json.Unmarshal(bob.do("GET", "/api/search?q="+q, nil, 200), &sr)
		if len(sr.Entries) != want || sr.Total != want {
			t.Fatalf("search %q: %d results (total %d), want %d: %+v", q, len(sr.Entries), sr.Total, want, sr.Entries)
		}
	}

	// The filter is applied in SQL, not only afterwards in Go: the top match for "lamp"
	// is PROJECTS/lamp, which bob can't read, so filtering after LIMIT 1 would return nothing.
	_ = json.Unmarshal(bob.do("GET", "/api/search?q=lamp&limit=1", nil, 200), &sr)
	if len(sr.Entries) != 1 || sr.Total != 2 {
		t.Fatalf("limited search: %+v total %d", sr.Entries, sr.Total)
	}

	// a folder bob can only walk through: described, counts limited to what he can read
	var info struct {
		Items   int64 `json:"items"`
		CanRead bool  `json:"can_read"`
	}
	_ = json.Unmarshal(bob.do("GET", "/api/info?path=PROJECTS", nil, 200), &info)
	if info.CanRead || info.Items != 2 { // lamp/sub and lamp/sub/inner.txt
		t.Fatalf("walk-through info: %+v", info)
	}

	// zips leave out what bob can't read
	if got := zipNames(t, bob.do("GET", "/api/zip?path=DOCS", nil, 200)); strings.Join(got, ",") != "DOCS/notes.txt" {
		t.Fatalf("zip of DOCS: %v", got)
	}
	if got := zipNames(t, bob.do("GET", "/api/zip?path=PROJECTS", nil, 200)); strings.Join(got, ",") != "PROJECTS/lamp/sub/inner.txt" {
		t.Fatalf("zip of PROJECTS: %v", got)
	}
	bob.do("GET", "/api/zip?path=DOCS/Taxes", nil, 404)
	if got := zipNames(t, e.do("GET", "/api/zip?path=DOCS", nil, 200)); len(got) != 2 {
		t.Fatalf("admin zip: %v", got)
	}

	// change log and stats only cover what bob can read
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "Taxes", "2025.pdf"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "seen.txt"), []byte("x"), 0o644)
	e.app.idx.fullScan()
	var cs []Change
	_ = json.Unmarshal(bob.do("GET", "/api/changes", nil, 200), &cs)
	if len(cs) != 1 || cs[0].Path != "DOCS/seen.txt" {
		t.Fatalf("bob's changes: %+v", cs)
	}
	_ = json.Unmarshal(e.do("GET", "/api/changes", nil, 200), &cs)
	if len(cs) != 2 {
		t.Fatalf("admin's changes: %+v", cs)
	}
	var stats struct {
		ByRoot []struct {
			Name  string
			Files int64
		} `json:"by_root"`
		Status map[string]any
	}
	_ = json.Unmarshal(bob.do("GET", "/api/stats", nil, 200), &stats)
	files := map[string]int64{}
	for _, r := range stats.ByRoot {
		files[r.Name] = r.Files
	}
	if files["DOCS"] != 2 || files["PROJECTS"] != 1 {
		t.Fatalf("bob's stats: %+v", stats.ByRoot)
	}
	if _, ok := stats.Status["files"]; ok {
		t.Fatal("library-wide totals shown to a non-admin")
	}

	// favorites are per user and limited to readable paths
	bob.do("POST", "/api/favorites", map[string]any{"path": "DOCS/Taxes"}, 404)
	bob.do("POST", "/api/favorites", map[string]any{"path": "DOCS"}, 200)
	var favs []Entry
	_ = json.Unmarshal(e.do("GET", "/api/favorites", nil, 200), &favs)
	if len(favs) != 0 {
		t.Fatalf("bob's favorite shows for admin: %+v", favs)
	}
}

// TestScopeMatchesCan checks that the SQL filter used by search, stats and the change
// log agrees with the Go permission check for random grant sets, including carve-outs
// inside carve-outs.
func TestScopeMatchesCan(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	paths := []string{"A", "A/B", "A/B/C", "A/B/C/x", "A/B/y", "A/z", "A/B2", "A/B2/q", "A/B!", "AB", "AB/c", "B", "B/A/B", "A/B/C2/x", "A/B/C/D/E"}
	if _, err := db.Exec(`CREATE TABLE p(path TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		_, _ = db.Exec(`INSERT INTO p VALUES(?)`, p)
	}
	cands := []string{"A", "A/B", "A/B/C", "A/B/C/D", "B", "AB", "A/B2", "A/B/C/x"}
	rnd := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 400; i++ {
		u := &User{}
		for _, c := range cands {
			if rnd.IntN(10) < 4 {
				u.Grants = append(u.Grants, Grant{Path: c, Read: rnd.IntN(3) > 0, Share: rnd.IntN(2) == 0})
			}
		}
		for _, p := range []perm{permRead, permShare} {
			cond, args := u.scope("path", p)
			rows, err := db.Query(`SELECT path FROM p WHERE `+cond, args...)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for rows.Next() {
				var s string
				_ = rows.Scan(&s)
				got[s] = true
			}
			rows.Close()
			for _, path := range paths {
				if got[path] != u.can(path, p) {
					t.Fatalf("grants %+v perm %d path %s: sql %v, can %v", u.Grants, p, path, got[path], u.can(path, p))
				}
			}
		}
	}
}

func TestShareOwnership(t *testing.T) {
	e := setup(t)
	bobU := e.mkUser("bob", false, bobGrants...)
	bob := e.as("bob", "longenough-bob")

	// bob can only share what his grants allow
	bob.do("POST", "/api/shares", map[string]any{"path": "DOCS/Taxes/2024-return.pdf"}, 400)
	bob.do("POST", "/api/shares", map[string]any{"path": "PROJECTS/lamp/sub/inner.txt"}, 400) // read-only grant
	bob.do("POST", "/api/shares", map[string]any{"mode": "drop", "label": "x"}, 403)
	bob.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "c", "allow_upload": true}, 400)
	bob.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "c", "items": []string{"DOCS/Taxes"}}, 400)
	var link, coll Share
	_ = json.Unmarshal(bob.do("POST", "/api/shares", map[string]any{"path": "DOCS"}, 200), &link)
	_ = json.Unmarshal(bob.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "Bobs", "items": []string{"DOCS"}}, 200), &coll)
	if link.OwnerID != bobU.ID {
		t.Fatalf("owner: %+v", link)
	}
	bob.do("POST", "/api/shares/"+coll.ID+"/upload", nil, 403)
	bob.do("PATCH", "/api/shares/"+coll.ID, map[string]any{"allow_upload": true}, 403)

	// recipients of bob's links don't see the carved-out folder either
	pub := &http.Client{}
	if c, body := publicGet(t, pub, e.srv.URL+"/s/"+link.ID); c != 200 || strings.Contains(string(body), "Taxes") || !strings.Contains(string(body), "notes.txt") {
		t.Fatalf("link listing: %d %s", c, body)
	}
	for _, p := range []string{"/s/" + link.ID + "/raw?p=Taxes/2024-return.pdf", "/s/" + link.ID + "?p=Taxes", "/s/" + coll.ID + "/raw?p=DOCS/Taxes/2024-return.pdf", "/s/" + coll.ID + "?p=DOCS/Taxes"} {
		if c, _ := publicGet(t, pub, e.srv.URL+p); c != 404 {
			t.Fatalf("%s: %d", p, c)
		}
	}
	if c, b := publicGet(t, pub, e.srv.URL+"/s/"+link.ID+"/zip"); c != 200 || strings.Join(zipNames(t, b), ",") != "DOCS/notes.txt" {
		t.Fatalf("link zip: %d %v", c, zipNames(t, b))
	}
	if c, b := publicGet(t, pub, e.srv.URL+"/s/"+coll.ID+"/zip"); c != 200 || strings.Join(zipNames(t, b), ",") != "DOCS/notes.txt" {
		t.Fatalf("shared folder zip: %d", c)
	}
	if c, b := publicGet(t, pub, e.srv.URL+"/s/"+coll.ID+"/zip?p=DOCS"); c != 200 || strings.Join(zipNames(t, b), ",") != "DOCS/notes.txt" {
		t.Fatalf("shared folder subfolder zip: %d", c)
	}

	// each user sees and manages only their own links; admins see all, with the owner
	var adminLink Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/Taxes"}, 200), &adminLink)
	var list []Share
	_ = json.Unmarshal(bob.do("GET", "/api/shares", nil, 200), &list)
	if len(list) != 2 {
		t.Fatalf("bob sees: %+v", list)
	}
	_ = json.Unmarshal(e.do("GET", "/api/shares", nil, 200), &list)
	owners := map[string]string{}
	for _, s := range list {
		owners[s.ID] = s.Owner
	}
	if len(list) != 3 || owners[link.ID] != "bob" || owners[adminLink.ID] != "admin" {
		t.Fatalf("admin sees: %+v", list)
	}
	bob.do("PATCH", "/api/shares/"+adminLink.ID, map[string]any{"revoked": true}, 404)
	bob.do("DELETE", "/api/shares/"+adminLink.ID, nil, 404)
	bob.do("GET", "/api/shares/"+adminLink.ID+"/events", nil, 404)
	bob.do("GET", "/api/shares/"+adminLink.ID+"/qr.png", nil, 404)
	var ev []ShareEvent
	_ = json.Unmarshal(bob.do("GET", "/api/events", nil, 200), &ev)
	for _, x := range ev {
		if x.Share == adminLink.ID {
			t.Fatal("bob sees activity on the admin's link")
		}
	}
	e.do("PATCH", "/api/shares/"+link.ID, map[string]any{"note": "admin edit"}, 200)

	// taking away bob's share permission turns his links off
	e.do("PATCH", "/api/users/"+itoa(bobU.ID), map[string]any{"grants": []Grant{{Path: "DOCS", Read: true}}}, 200)
	if c, _ := publicGet(t, pub, e.srv.URL+"/s/"+link.ID); c != 410 {
		t.Fatalf("link still works without share permission: %d", c)
	}
	if c, body := publicGet(t, pub, e.srv.URL+"/s/"+coll.ID); c != 200 || strings.Contains(string(body), "notes.txt") {
		t.Fatalf("shared folder still lists items: %d", c)
	}
	_ = json.Unmarshal(bob.do("GET", "/api/shares", nil, 200), &list)
	for _, s := range list {
		if s.ID == link.ID && s.State != "unavailable" {
			t.Fatalf("state: %s", s.State)
		}
	}
	// give it back, then disable bob: links, session and token all stop
	e.do("PATCH", "/api/users/"+itoa(bobU.ID), map[string]any{"grants": bobGrants}, 200)
	if c, _ := publicGet(t, pub, e.srv.URL+"/s/"+link.ID); c != 200 {
		t.Fatalf("link after grant restored: %d", c)
	}
	var tok struct{ Token string }
	_ = json.Unmarshal(bob.do("POST", "/api/tokens", map[string]any{"name": "t"}, 200), &tok)
	e.do("PATCH", "/api/users/"+itoa(bobU.ID), map[string]any{"disabled": true}, 200)
	bob.do("GET", "/api/ls?path=", nil, 401)
	if c, _ := e.bearer(tok.Token, "GET", "/api/ls?path=", nil); c != 401 {
		t.Fatalf("disabled user's token: %d", c)
	}
	if c, _ := publicGet(t, pub, e.srv.URL+"/s/"+link.ID); c != 410 {
		t.Fatalf("disabled owner's link: %d", c)
	}
	e.do("PATCH", "/api/users/"+itoa(bobU.ID), map[string]any{"disabled": false}, 200)
	if c, _ := e.bearer(tok.Token, "GET", "/api/ls?path=", nil); c != 200 {
		t.Fatalf("re-enabled user's token: %d", c)
	}

	// deleting bob revokes his links, and a new user never inherits his id
	e.do("DELETE", "/api/users/"+itoa(bobU.ID), nil, 200)
	if c, _ := publicGet(t, pub, e.srv.URL+"/s/"+link.ID); c != 410 {
		t.Fatalf("deleted owner's link: %d", c)
	}
	carol := e.mkUser("carol", false, bobGrants...)
	if carol.ID == bobU.ID {
		t.Fatal("user id reused")
	}
	_ = json.Unmarshal(e.as("carol", "longenough-carol").do("GET", "/api/shares", nil, 200), &list)
	if len(list) != 0 {
		t.Fatalf("new user inherited links: %+v", list)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestAdminOnlyAndAccountScope(t *testing.T) {
	e := setup(t)
	e.mkUser("bob", false, bobGrants...)
	bob := e.as("bob", "longenough-bob")
	bob.do("GET", "/api/users", nil, 403)
	bob.do("POST", "/api/users", map[string]any{"username": "x", "password": "longenough-x"}, 403)
	bob.do("PUT", "/api/settings", map[string]any{"public_url": "https://evil.example"}, 403)
	bob.do("POST", "/api/rescan", nil, 403)
	bob.do("GET", "/api/roots/available", nil, 403)
	var st Settings
	_ = json.Unmarshal(bob.do("GET", "/api/settings", nil, 200), &st)
	if st.Admin || st.User != "bob" || len(st.Roots) != 0 || st.DataDir != "" {
		t.Fatalf("bob's settings: %+v", st)
	}
	var state map[string]any
	_ = json.Unmarshal(bob.do("GET", "/api/state", nil, 200), &state)
	if state["user"] != "bob" || state["admin"] != false {
		t.Fatalf("state: %v", state)
	}

	// tokens belong to their user
	var tok struct {
		Token string
		ID    int64
	}
	_ = json.Unmarshal(e.do("POST", "/api/tokens", map[string]any{"name": "admin-token"}, 200), &tok)
	var toks []struct{ ID int64 }
	_ = json.Unmarshal(bob.do("GET", "/api/tokens", nil, 200), &toks)
	if len(toks) != 0 {
		t.Fatalf("bob sees the admin's tokens: %+v", toks)
	}
	_ = json.Unmarshal(e.do("GET", "/api/tokens", nil, 200), &toks)
	bob.do("DELETE", "/api/tokens/"+itoa(toks[0].ID), nil, 200)
	if c, _ := e.bearer(tok.Token, "GET", "/api/ls?path=", nil); c != 200 {
		t.Fatal("bob deleted the admin's token")
	}
	// an admin token may rescan but not manage users or settings (those need a session)
	if c, _ := e.bearer(tok.Token, "POST", "/api/rescan", nil); c != 200 {
		t.Fatalf("admin token rescan: %d", c)
	}
	if c, _ := e.bearer(tok.Token, "GET", "/api/users", nil); c != 401 {
		t.Fatalf("token listed users: %d", c)
	}

	// password change: needs the current password, signs out other sessions
	other := e.as("bob", "longenough-bob")
	bob.do("POST", "/api/password", map[string]any{"current": "wrong-password", "new": "a-new-password"}, 401)
	bob.do("POST", "/api/password", map[string]any{"current": "longenough-bob", "new": "short"}, 400)
	bob.do("POST", "/api/password", map[string]any{"current": "longenough-bob", "new": "a-new-password"}, 200)
	bob.do("GET", "/api/ls?path=", nil, 200)
	other.do("GET", "/api/ls?path=", nil, 401)
	e.as("bob", "a-new-password")

	// usernames are unique regardless of case; bad input is refused
	e.do("POST", "/api/users", map[string]any{"username": "BOB", "password": "longenough-x"}, 400)
	e.do("POST", "/api/users", map[string]any{"username": "has space", "password": "longenough-x"}, 400)
	e.do("POST", "/api/users", map[string]any{"username": "dave", "password": "short"}, 400)
	e.do("POST", "/api/users", map[string]any{"username": "dave", "password": "longenough-x", "grants": []Grant{{Path: "DOCS/nope", Read: true}}}, 400)
	e.do("POST", "/api/users", map[string]any{"username": "dave", "password": "longenough-x", "grants": []Grant{{Path: "PRIVATE", Read: true}}}, 400)
}

func TestKeepsAnAdmin(t *testing.T) {
	e := setup(t)
	var users []User
	_ = json.Unmarshal(e.do("GET", "/api/users", nil, 200), &users)
	me := users[0]
	e.do("DELETE", "/api/users/"+itoa(me.ID), nil, 400)
	e.do("PATCH", "/api/users/"+itoa(me.ID), map[string]any{"admin": false}, 400)
	e.do("PATCH", "/api/users/"+itoa(me.ID), map[string]any{"disabled": true}, 400)
	second := e.mkUser("second", true)
	e.do("PATCH", "/api/users/"+itoa(second.ID), map[string]any{"disabled": true}, 200)
	// a disabled admin doesn't count as the one to keep, so it can be deleted
	e.do("DELETE", "/api/users/"+itoa(second.ID), nil, 200)
	// a non-admin can be promoted, after which the first admin may step down
	third := e.mkUser("third", false)
	e.do("PATCH", "/api/users/"+itoa(third.ID), map[string]any{"admin": true}, 200)
	e.do("PATCH", "/api/users/"+itoa(me.ID), map[string]any{"admin": false}, 200)
	e.do("GET", "/api/users", nil, 403) // no longer an admin
}

func TestMigrateSingleAdmin(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	_ = os.MkdirAll(filepath.Join(data, "DOCS"), 0o755)
	_ = os.WriteFile(filepath.Join(data, "DOCS", "notes.txt"), []byte("hello"), 0o644)
	dbp := filepath.Join(dir, "state", "burrow.db")
	_ = os.MkdirAll(filepath.Dir(dbp), 0o755)

	// a database as the single-admin version left it
	db, err := sql.Open("sqlite", "file:"+dbp)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("oldpassword1"), 4)
	for _, q := range []string{
		schema,
		`ALTER TABLE shares ADD COLUMN allow_upload INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE share_items (share_id TEXT NOT NULL, path TEXT NOT NULL, added INTEGER NOT NULL, PRIMARY KEY(share_id, path))`,
		`CREATE TABLE favorites (path TEXT PRIMARY KEY, created INTEGER)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{"admin_user": "alex", "admin_hash": string(h), "setup_done": "1", "roots": `["DOCS"]`, "secret": "s3cret", "totp_recovery": "[]", "totp_enabled": ""} {
		_, _ = db.Exec(`INSERT INTO settings(k,v) VALUES(?,?)`, k, v)
	}
	_, _ = db.Exec(`INSERT INTO sessions(id,created,expires,ip,ua) VALUES(?,?,?,?,?)`, sha("old-session"), now(), now()+3600, "127.0.0.1", "test")
	_, _ = db.Exec(`INSERT INTO tokens(name,hash,created) VALUES('mcp',?,?)`, sha("brw_oldtoken"), now())
	_, _ = db.Exec(`INSERT INTO shares(id,path,is_dir,mode,created) VALUES('oldshare1','DOCS/notes.txt',0,'link',?)`, now())
	_, _ = db.Exec(`INSERT INTO favorites(path,created) VALUES('DOCS',?)`, now())
	db.Close()

	for range 2 { // the upgrade must be safe to run again
		st, err := openStore(dbp)
		if err != nil {
			t.Fatal(err)
		}
		st.db.Close()
	}
	st, err := openStore(dbp)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DataDir: data, DropDir: filepath.Join(dir, "drop"), StateDir: filepath.Dir(dbp), ScanInterval: time.Hour, TrustedProxies: []string{"127.0.0.1/32"}}
	app, err := newApp(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	u, err := app.loadUser(1)
	if err != nil || u.Name != "alex" || !u.Admin {
		t.Fatalf("user 1: %+v %v", u, err)
	}
	for _, k := range oldAdminKeys {
		if st.get(k) != "" {
			t.Fatalf("old setting %s left behind", k)
		}
	}
	if !app.setupDone() {
		t.Fatal("setup no longer done")
	}
	srv := httptest.NewServer(app.routes())
	defer srv.Close()
	app.idx.fullScan()

	// the old session, token, link and favorite all still work
	req, _ := http.NewRequest("GET", srv.URL+"/api/state", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "old-session"})
	r, _ := http.DefaultClient.Do(req)
	var state map[string]any
	_ = json.NewDecoder(r.Body).Decode(&state)
	r.Body.Close()
	if state["signed_in"] != true || state["user"] != "alex" || state["admin"] != true {
		t.Fatalf("old session: %v", state)
	}
	req, _ = http.NewRequest("GET", srv.URL+"/api/favorites", nil)
	req.Header.Set("Authorization", "Bearer brw_oldtoken")
	r, _ = http.DefaultClient.Do(req)
	var favs []Entry
	_ = json.NewDecoder(r.Body).Decode(&favs)
	r.Body.Close()
	if r.StatusCode != 200 || len(favs) != 1 || favs[0].Path != "DOCS" {
		t.Fatalf("old token / favorites: %d %+v", r.StatusCode, favs)
	}
	if c, body := publicGet(t, http.DefaultClient, srv.URL+"/s/oldshare1/raw"); c != 200 || string(body) != "hello" {
		t.Fatalf("old link: %d", c)
	}
	b, _ := json.Marshal(map[string]string{"user": "alex", "password": "oldpassword1"})
	r, _ = http.Post(srv.URL+"/api/login", "application/json", bytes.NewReader(b))
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("old password: %d", r.StatusCode)
	}
}

func TestUploadFoldersAreCreatedLazily(t *testing.T) {
	e := setup(t)
	var drop, coll Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "drop", "label": "From Mom"}, 200), &drop)
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "Trip", "items": []string{"DOCS/notes.txt"}, "allow_upload": true}, 200), &coll)
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "PROJECTS/lamp"}, 200), new(Share))
	if left, _ := os.ReadDir(e.drop); len(left) != 0 {
		t.Fatalf("folders created before any upload: %v", left)
	}
	// the shared folder works without its upload folder
	if c, body := publicGet(t, http.DefaultClient, e.srv.URL+"/s/"+coll.ID); c != 200 || !strings.Contains(string(body), "notes.txt") {
		t.Fatalf("shared folder page: %d", c)
	}
	e.do("GET", "/api/shares/"+coll.ID+"/items", nil, 200)

	// a folder already on disk with the same name is never reused for a new shared folder
	_ = os.MkdirAll(filepath.Join(e.drop, "Shared - Old"), 0o755)
	_ = os.WriteFile(filepath.Join(e.drop, "Shared - Old", "leftover.txt"), []byte("x"), 0o644)
	var old Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "Old"}, 200), &old)
	if old.Path != "Shared - Old 2" {
		t.Fatalf("upload folder: %q", old.Path)
	}
	// two shared folders with the same name get separate upload folders
	var trip2 Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"mode": "collection", "label": "Trip"}, 200), &trip2)
	if trip2.Path == coll.Path {
		t.Fatal("two shared folders share an upload folder")
	}

	upload := func(id, name, body string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		w, _ := mw.CreateFormFile("files", name)
		_, _ = w.Write([]byte(body))
		mw.Close()
		req, _ := http.NewRequest("POST", e.srv.URL+"/s/"+id+"/upload", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Accept", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("upload: %v %d", err, r.StatusCode)
		}
		r.Body.Close()
	}
	upload(drop.ID, "a.txt", "aaa")
	upload(coll.ID, "b.txt", "bbb")
	if got, _ := os.ReadFile(filepath.Join(e.drop, "From Mom", "a.txt")); string(got) != "aaa" {
		t.Fatalf("drop upload: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(e.drop, coll.Path, "b.txt")); string(got) != "bbb" {
		t.Fatalf("shared folder upload: %q", got)
	}
	// nothing but the finished files is left in the upload folders
	all, _ := filepath.Glob(filepath.Join(e.drop, "*", "*"))
	if len(all) != 3 { // a.txt, b.txt and the pre-existing leftover.txt
		t.Fatalf("upload folder contents: %v", all)
	}

	// an upload in progress is invisible: not listed, not served, not zipped, not indexed
	part := filepath.Join(e.drop, coll.Path, partPrefix+"123")
	_ = os.WriteFile(part, []byte("half"), 0o644)
	if _, body := publicGet(t, http.DefaultClient, e.srv.URL+"/s/"+coll.ID); strings.Contains(string(body), partPrefix) {
		t.Fatal("partial upload listed")
	}
	if c, _ := publicGet(t, http.DefaultClient, e.srv.URL+"/s/"+coll.ID+"/raw?p="+partPrefix+"123"); c != 404 {
		t.Fatalf("partial upload served: %d", c)
	}
	if c, b := publicGet(t, http.DefaultClient, e.srv.URL+"/s/"+coll.ID+"/zip"); c != 200 || strings.Contains(strings.Join(zipNames(t, b), ","), partPrefix) {
		t.Fatal("partial upload zipped")
	}
	var view CollectionView
	_ = json.Unmarshal(e.do("GET", "/api/shares/"+coll.ID+"/items", nil, 200), &view)
	if len(view.Uploads) != 1 {
		t.Fatalf("owner view: %+v", view.Uploads)
	}
	if !e.app.idx.excluded(partPrefix + "x") {
		t.Fatal("indexer would index partial uploads")
	}
	// stale partials are cleaned up
	old24 := time.Now().Add(-25 * time.Hour)
	_ = os.Chtimes(part, old24, old24)
	e.app.cleanPartials()
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("stale partial upload kept")
	}
}

func TestThumbnailRevalidates(t *testing.T) {
	e := setup(t)
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 40, 30)))
	_ = os.WriteFile(filepath.Join(e.data, "DOCS", "pic.png"), buf.Bytes(), 0o644)
	get := func(etag string) (int, string) {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/thumb?path=DOCS/pic.png", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		r, err := e.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode, r.Header.Get("ETag")
	}
	c, tag := get("")
	if c != 200 || tag == "" {
		t.Fatalf("first: %d %q", c, tag)
	}
	if c, _ := get(""); c != 200 { // served from the cache
		t.Fatalf("cached: %d", c)
	}
	if c, _ := get(tag); c != 304 {
		t.Fatalf("revalidate: %d", c)
	}
	// unused thumbnails are pruned
	e.app.pruneThumbs(-time.Hour)
	left, _ := filepath.Glob(filepath.Join(e.app.cfg.StateDir, "thumbs", "*", "*"))
	if len(left) != 0 {
		t.Fatalf("thumbnails kept: %v", left)
	}
}

func TestMCPActsAsTokenUser(t *testing.T) {
	e := setup(t)
	e.mkUser("bob", false, bobGrants...)
	bob := e.as("bob", "longenough-bob")
	var adminLink Share
	_ = json.Unmarshal(e.do("POST", "/api/shares", map[string]any{"path": "DOCS/notes.txt"}, 200), &adminLink)
	var tok struct{ Token string }
	_ = json.Unmarshal(bob.do("POST", "/api/tokens", map[string]any{"name": "mcp"}, 200), &tok)
	call := func(body string) map[string]any {
		_, b := e.bearer(tok.Token, "POST", "/mcp", json.RawMessage(body))
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return out["result"].(map[string]any)
	}
	text := func(res map[string]any) string {
		return res["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	tools := call(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)["tools"].([]any)
	for _, x := range tools {
		if x.(map[string]any)["name"] == "create_file_drop" {
			t.Fatal("drop tool offered to a non-admin")
		}
	}
	if s := text(call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_files","arguments":{"query":"2024"}}}`)); strings.Contains(s, "Taxes") {
		t.Fatalf("search leaked: %s", s)
	}
	if res := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read_text_file","arguments":{"path":"DOCS/Taxes/2024-return.pdf"}}}`); res["isError"] != true {
		t.Fatal("read outside grants")
	}
	if res := call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_file_drop","arguments":{"label":"x"}}}`); res["isError"] != true {
		t.Fatal("drop created by non-admin")
	}
	if res := call(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"revoke_share_link","arguments":{"id":"` + adminLink.ID + `"}}}`); res["isError"] != true {
		t.Fatal("revoked someone else's link")
	}
	if s := text(call(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"list_share_links","arguments":{}}}`)); strings.Contains(s, adminLink.ID) {
		t.Fatal("listed someone else's link")
	}
	if s := text(call(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"create_share_link","arguments":{"path":"DOCS/notes.txt"}}}`)); !strings.Contains(s, "/s/") {
		t.Fatalf("share within grants: %s", s)
	}
	if s := text(call(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"index_status","arguments":{}}}`)); strings.Contains(s, "total_size") {
		t.Fatalf("library totals for non-admin: %s", s)
	}
}
