package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// smbTarget reads BURROW_TEST_SMB (from ci/smb-server.sh): host:port:share:user:password:dir.
func smbTarget(t *testing.T) (map[string]any, string) {
	v := os.Getenv("BURROW_TEST_SMB")
	if v == "" {
		t.Skip("no test SMB server; run: eval \"$(sudo ci/smb-server.sh)\"")
	}
	p := strings.SplitN(v, ":", 6)
	if len(p) != 6 {
		t.Fatalf("BURROW_TEST_SMB should be host:port:share:user:password:dir, got %q", v)
	}
	var port int
	fmt.Sscan(p[1], &port)
	// a fresh folder per test, so tests don't see each other's files
	sub := fmt.Sprintf("t-%d", time.Now().UnixNano())
	dir := filepath.Join(p[5], sub)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"kind": "smb", "host": p[0], "port": port, "share": p[2], "user": p[3], "password": p[4], "folder": sub}, dir
}

// waitJob follows a background move until it ends.
func (e *tenv) waitJob(id string) job {
	e.t.Helper()
	var j job
	for i := 0; i < 200; i++ {
		_ = json.Unmarshal(e.do("GET", "/api/jobs/"+id, nil, 200), &j)
		if j.State == "done" || j.State == "failed" {
			return j
		}
		time.Sleep(25 * time.Millisecond)
	}
	e.t.Fatalf("job %s never finished: %+v", id, j)
	return j
}

func (e *tenv) moveTo(from, to string) (int, map[string]string) {
	e.t.Helper()
	b, _ := json.Marshal(map[string]string{"from": from, "to": to})
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/fs/move", bytes.NewReader(b))
	req.Header.Set("X-Burrow", "1")
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func entryNames(es []Entry) string {
	var n []string
	for _, e := range es {
		n = append(n, e.Name)
	}
	return strings.Join(n, ",")
}

func tinyPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// exerciseRemote runs the same checks against any connected NAS named Office,
// whose files the test can see on disk at dir.
func exerciseRemote(t *testing.T, e *tenv, dir string) {
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o777)
		if err := os.WriteFile(p, []byte(body), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	write("Movies/a.txt", "hello remote")
	write("pic.png", string(tinyPNG()))

	// it shows up as a library and can be browsed
	var ls struct {
		Entries  []Entry
		CanWrite bool `json:"can_write"`
	}
	_ = json.Unmarshal(e.do("GET", "/api/ls?path=", nil, 200), &ls)
	if !strings.Contains(entryNames(ls.Entries), "Office") {
		t.Fatalf("roots: %s", entryNames(ls.Entries))
	}
	_ = json.Unmarshal(e.do("GET", "/api/ls?path=Office", nil, 200), &ls)
	if entryNames(ls.Entries) != "Movies,pic.png" || !ls.CanWrite {
		t.Fatalf("Office: %s write=%v", entryNames(ls.Entries), ls.CanWrite)
	}
	if got := string(e.do("GET", "/api/raw?path=Office/Movies/a.txt", nil, 200)); got != "hello remote" {
		t.Fatalf("raw: %q", got)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/raw?path=Office/Movies/a.txt", nil)
	req.Header.Set("Range", "bytes=1-3")
	resp, err := e.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || string(body) != "ell" {
		t.Fatalf("range: %d %q", resp.StatusCode, body)
	}
	if th := e.do("GET", "/api/thumb?path=Office/pic.png", nil, 200); len(th) < 100 || th[0] != 0xFF {
		t.Fatalf("thumb: %d bytes", len(th))
	}
	var info struct {
		Entry     Entry
		CanChange bool `json:"can_change"`
	}
	_ = json.Unmarshal(e.do("GET", "/api/info?path=Office/Movies/a.txt", nil, 200), &info)
	if info.Entry.Size != 12 || !info.CanChange {
		t.Fatalf("info: %+v", info)
	}
	// what isn't a plain path stays out
	for _, p := range []string{"Office/.burrow-trash", "Office/.burrow-trash/x", "Office/Movies/../../x"} {
		e.do("GET", "/api/raw?path="+url.QueryEscape(p), nil, 404)
	}

	// changes
	e.do("POST", "/api/fs/mkdir", map[string]any{"path": "Office/New"}, 200)
	e.do("POST", "/api/fs/mkdir", map[string]any{"path": "Office/Deep/er/est", "parents": true}, 200)
	if fi, err := os.Stat(filepath.Join(dir, "Deep/er/est")); err != nil || !fi.IsDir() {
		t.Fatal("parents not made")
	}
	body2 := bytes.Repeat([]byte("burrow!"), 5000)
	up := e.upload("Office/New", "up.bin", body2, 7000, nil, 200)
	if up["path"] != "Office/New/up.bin" || readFile(t, filepath.Join(dir, "New/up.bin")) != string(body2) {
		t.Fatalf("upload: %v", up)
	}
	up = e.upload("Office/New", "up.bin", []byte("second"), 100, nil, 200)
	if up["name"] != "up (2).bin" || readFile(t, filepath.Join(dir, "New/up (2).bin")) != "second" {
		t.Fatalf("rename on conflict: %v", up)
	}
	e.upload("Office/New", "up.bin", []byte("third"), 100, map[string]any{"conflict": "fail"}, 409)
	if code, _ := e.moveTo("Office/New/up (2).bin", "Office/Movies/up.bin"); code != 200 {
		t.Fatalf("rename inside: %d", code)
	}
	if readFile(t, filepath.Join(dir, "Movies/up.bin")) != "second" {
		t.Fatal("not renamed")
	}
	if code, _ := e.moveTo("Office/Movies/up.bin", "Office/Movies/a.txt"); code != 409 {
		t.Fatalf("rename over a file: %d", code)
	}

	// moves between libraries run as jobs and leave the original in the trash
	code, mv := e.moveTo("DOCS/notes.txt", "Office/notes.txt")
	if code != 202 || mv["job"] == "" {
		t.Fatalf("local to remote: %d %v", code, mv)
	}
	if j := e.waitJob(mv["job"]); j.State != "done" || j.FilesDone != 1 {
		t.Fatalf("job: %+v", j)
	}
	if readFile(t, filepath.Join(dir, "notes.txt")) != "hello notes" {
		t.Fatal("not copied over")
	}
	if _, err := os.Stat(filepath.Join(e.data, "DOCS/notes.txt")); !os.IsNotExist(err) {
		t.Fatal("the original is still there")
	}
	code, mv = e.moveTo("Office/Movies", "PROJECTS/Movies")
	if code != 202 {
		t.Fatalf("remote to local: %d %v", code, mv)
	}
	if j := e.waitJob(mv["job"]); j.State != "done" || j.FilesDone != 2 {
		t.Fatalf("job: %+v", j)
	}
	if readFile(t, filepath.Join(e.data, "PROJECTS/Movies/a.txt")) != "hello remote" {
		t.Fatal("folder not copied")
	}
	_ = json.Unmarshal(e.do("GET", "/api/ls?path=Office", nil, 200), &ls)
	if strings.Contains(entryNames(ls.Entries), "Movies") {
		t.Fatalf("the moved folder is still there: %s", entryNames(ls.Entries))
	}
	// something already at the destination stops a move before it starts
	write("taken.txt", "x")
	if err := os.WriteFile(filepath.Join(e.data, "DOCS/taken.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.moveTo("DOCS/taken.txt", "Office/taken.txt"); code != 409 {
		t.Fatalf("move onto a file: %d", code)
	}
}

func checkTrashRoundTrip(t *testing.T, e *tenv, dir string) {
	var del []struct {
		OK      bool   `json:"ok"`
		TrashID string `json:"trash_id"`
	}
	_ = json.Unmarshal(e.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"Office/pic.png"}}, 200), &del)
	if len(del) != 1 || !del[0].OK || del[0].TrashID == "" {
		t.Fatalf("delete: %+v", del)
	}
	if _, err := os.Stat(filepath.Join(dir, "pic.png")); !os.IsNotExist(err) {
		t.Fatal("still there")
	}
	e.do("POST", "/api/trash/"+del[0].TrashID+"/restore", nil, 200)
	if _, err := os.Stat(filepath.Join(dir, "pic.png")); err != nil {
		t.Fatal("not restored")
	}
	_ = json.Unmarshal(e.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"Office/pic.png"}}, 200), &del)
	slot := filepath.Join(dir, trashName, del[0].TrashID)
	if _, err := os.Stat(filepath.Join(slot, "pic.png")); err != nil {
		t.Fatal("not in the trash on the NAS")
	}
	e.do("DELETE", "/api/trash/"+del[0].TrashID, nil, 200)
	if _, err := os.Stat(slot); !os.IsNotExist(err) {
		t.Fatal("purge left it")
	}
}

func TestRemoteSMB(t *testing.T) {
	cfg, dir := smbTarget(t)
	e := setup(t)

	bad := map[string]any{"name": "Office"}
	for k, v := range cfg {
		bad[k] = v
	}
	bad["password"] = "wrong"
	if b := e.do("POST", "/api/remotes", bad, 400); !strings.Contains(string(b), "turned down") && !strings.Contains(string(b), "couldn") {
		t.Fatalf("wrong password: %s", b)
	}
	for _, n := range []string{"DOCS", "a/b", ".burrow-x", ""} {
		cfg["name"] = n
		e.do("POST", "/api/remotes", cfg, 400)
	}
	cfg["name"] = "Office"
	e.do("POST", "/api/remotes", cfg, 200)
	e.do("POST", "/api/remotes", cfg, 400) // the name is taken

	// the password never comes back, and isn't in the database in the clear
	if b := e.do("GET", "/api/remotes", nil, 200); strings.Contains(string(b), cfg["password"].(string)) || !strings.Contains(string(b), `"has_password":true`) {
		t.Fatalf("remotes listing: %s", b)
	}
	e.app.st.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	raw, _ := os.ReadFile(filepath.Join(e.app.cfg.StateDir, "t.db"))
	if bytes.Contains(raw, []byte(cfg["password"].(string))) {
		t.Fatal("the password is stored in the clear")
	}

	exerciseRemote(t, e, dir)
	checkTrashRoundTrip(t, e, dir)

	// someone without a grant on it doesn't see it
	e.mkUser("guest", false, Grant{Path: "DOCS", Read: true})
	g := e.as("guest", "longenough-guest")
	var ls struct{ Entries []Entry }
	_ = json.Unmarshal(g.do("GET", "/api/ls?path=", nil, 200), &ls)
	if strings.Contains(entryNames(ls.Entries), "Office") {
		t.Fatal("guest sees Office")
	}
	g.do("GET", "/api/ls?path=Office", nil, 403) // as for any folder outside their grants
	g.do("GET", "/api/raw?path=Office/notes.txt", nil, 404)
	g.do("POST", "/api/fs/mkdir", map[string]any{"path": "Office/x"}, 0)
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Fatal("guest made a folder")
	}
	g.do("GET", "/api/remotes", nil, 403)
	// read-only access to it reads but doesn't change
	e.mkUser("viewer", false, Grant{Path: "Office", Read: true})
	v := e.as("viewer", "longenough-viewer")
	v.do("GET", "/api/raw?path=Office/notes.txt", nil, 200)
	v.do("POST", "/api/fs/mkdir", map[string]any{"path": "Office/y"}, 403)
	if code, _ := v.moveTo("Office/notes.txt", "Office/n2.txt"); code != 403 {
		t.Fatalf("viewer renamed: %d", code)
	}
	// links to it aren't offered yet
	e.do("POST", "/api/shares", map[string]any{"path": "Office/notes.txt"}, 400)
}

func TestRemoteBurrow(t *testing.T) {
	b := setupOn(t, true) // the other Burrow
	var tok struct{ Token string }
	_ = json.Unmarshal(b.do("POST", "/api/tokens", map[string]any{"name": "from home"}, 200), &tok)
	sum := sha256.Sum256(b.srv.Certificate().Raw)
	pin := hex.EncodeToString(sum[:])

	e := setup(t)
	cfg := map[string]any{"name": "Office", "kind": "burrow", "url": b.srv.URL, "token": tok.Token, "folder": "PROJECTS"}
	e.do("POST", "/api/remotes", cfg, 400) // self-signed and not pinned
	cfg["pin"] = strings.Repeat("ab", 32)
	e.do("POST", "/api/remotes", cfg, 400) // the wrong pin
	cfg["pin"], cfg["token"] = pin, "brw_nope"
	e.do("POST", "/api/remotes", cfg, 400) // a token it doesn't know
	cfg["token"] = tok.Token
	e.do("POST", "/api/remotes", cfg, 200)
	if l := e.do("GET", "/api/remotes", nil, 200); strings.Contains(string(l), tok.Token) {
		t.Fatal("the token came back")
	}
	dir := filepath.Join(b.data, "PROJECTS")
	_ = os.RemoveAll(filepath.Join(dir, "lamp"))
	b.app.idx.fullScan()
	exerciseRemote(t, e, dir)

	// a delete goes to the other Burrow's own trash
	var del []struct{ OK bool }
	_ = json.Unmarshal(e.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"Office/pic.png"}}, 200), &del)
	var trash []TrashItem
	_ = json.Unmarshal(b.do("GET", "/api/trash", nil, 200), &trash)
	if len(del) != 1 || !del[0].OK || len(trash) == 0 || trash[0].Path != "PROJECTS/pic.png" {
		t.Fatalf("delete: %+v, their trash %+v", del, trash)
	}
}
