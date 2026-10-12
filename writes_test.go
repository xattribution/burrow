package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// put sends raw bytes (an upload chunk) as the signed-in user.
func (e *tenv) put(path string, body []byte) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest("PUT", e.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("X-Burrow", "1")
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func sum256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// upload sends body in chunks of n bytes and finishes; it returns the finish reply.
func (e *tenv) upload(dir, name string, body []byte, n int, extra map[string]any, want int) map[string]any {
	e.t.Helper()
	in := map[string]any{"dir": dir, "name": name, "size": len(body), "mtime": int64(1700000000123), "sha256": sum256(body)}
	for k, v := range extra {
		in[k] = v
	}
	var st map[string]any
	_ = json.Unmarshal(e.do("POST", "/api/uploads", in, 200), &st)
	if st["skipped"] == true {
		return st
	}
	id := st["id"].(string)
	for off := 0; off < len(body); off += n {
		end := min(off+n, len(body))
		if code, b := e.put(fmt.Sprintf("/api/uploads/%s?offset=%d", id, off), body[off:end]); code != 200 {
			e.t.Fatalf("chunk at %d: %d %s", off, code, b)
		}
	}
	var out map[string]any
	_ = json.Unmarshal(e.do("POST", "/api/uploads/"+id+"/finish", map[string]any{}, want), &out)
	out["id"] = id
	return out
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFolderMoveAndTrash(t *testing.T) {
	e := setup(t)
	_ = os.MkdirAll(filepath.Join(e.data, "PROJECTS/lamp/secret"), 0o755)
	_ = os.WriteFile(filepath.Join(e.data, "PROJECTS/lamp/secret/plan.txt"), []byte("plan"), 0o644)
	e.app.idx.fullScan()
	e.mkUser("ed", false,
		Grant{Path: "PROJECTS/lamp", Read: true, Write: true, Share: true},
		Grant{Path: "PROJECTS/lamp/secret", Read: false},
		Grant{Path: "DOCS", Read: true})
	ed := e.as("ed", "longenough-ed")

	// new folders, nested on request
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/parts"}, 200)
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/parts"}, 409)
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/a/b/c"}, 400)
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/a/b/c", "parents": true}, 200)
	if fi, err := os.Stat(filepath.Join(e.data, "PROJECTS/lamp/a/b/c")); err != nil || !fi.IsDir() {
		t.Fatal("nested folder not created")
	}
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "DOCS/new"}, 403)                // read-only grant
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/.burrow-x"}, 400) // reserved
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/.zfs"}, 400)      // hidden name
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/secret/x"}, 404)  // carve-out
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PRIVATE/x"}, 404)               // disabled root
	ed.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/../../PRIVATE/x"}, 404)

	// the browse listing says whether the folder takes writes
	var ls map[string]any
	_ = json.Unmarshal(ed.do("GET", "/api/ls?path=PROJECTS/lamp", nil, 200), &ls)
	if ls["can_write"] != true {
		t.Fatalf("can_write on a writable grant: %v", ls["can_write"])
	}
	_ = json.Unmarshal(ed.do("GET", "/api/ls?path=DOCS", nil, 200), &ls)
	if ls["can_write"] != false {
		t.Fatal("can_write on a read-only grant")
	}

	// a link on a file follows it when it is renamed
	var sh Share
	_ = json.Unmarshal(ed.do("POST", "/api/shares", map[string]any{"path": "PROJECTS/lamp/shade.stl"}, 200), &sh)
	ed.do("POST", "/api/fs/move", map[string]any{"from": "PROJECTS/lamp/shade.stl", "to": "PROJECTS/lamp/parts/shade-v2.stl"}, 200)
	if readFile(t, filepath.Join(e.data, "PROJECTS/lamp/parts/shade-v2.stl")) != "solid" {
		t.Fatal("move")
	}
	if code, b := publicGet(t, http.DefaultClient, e.srv.URL+"/s/"+sh.ID+"/raw"); code != 200 || string(b) != "solid" {
		t.Fatalf("link after the move: %d %s", code, b)
	}
	ed.do("POST", "/api/fs/move", map[string]any{"from": "PROJECTS/lamp/parts", "to": "PROJECTS/lamp/parts/inside"}, 400)
	ed.do("POST", "/api/fs/move", map[string]any{"from": "PROJECTS/lamp/a", "to": "PROJECTS/lamp/parts"}, 409)
	ed.do("POST", "/api/fs/move", map[string]any{"from": "DOCS/notes.txt", "to": "PROJECTS/lamp/notes.txt"}, 403)
	// a folder holding a carve-out can't be moved or deleted by someone the carve-out keeps out
	ed.do("POST", "/api/fs/move", map[string]any{"from": "PROJECTS/lamp", "to": "PROJECTS/lamp2"}, 403)
	var res []map[string]any
	_ = json.Unmarshal(ed.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"PROJECTS/lamp"}}, 200), &res)
	if res[0]["ok"] == true {
		t.Fatal("deleted a folder with a carve-out")
	}
	// library folders themselves stay put
	e.do("POST", "/api/fs/move", map[string]any{"from": "DOCS", "to": "PROJECTS/DOCS"}, 400)

	// an admin's move takes grants along, so carve-outs keep working
	e.do("POST", "/api/fs/move", map[string]any{"from": "PROJECTS/lamp", "to": "PROJECTS/desk-lamp"}, 200)
	ed.do("GET", "/api/raw?path=PROJECTS/desk-lamp/parts/shade-v2.stl", nil, 200)
	ed.do("GET", "/api/raw?path=PROJECTS/desk-lamp/secret/plan.txt", nil, 404)
	if u := mustUser(t, e, "ed"); len(u.Grants) != 3 || u.Grants[1].Path != "PROJECTS/desk-lamp" {
		t.Fatalf("grants after the move: %+v", u.Grants)
	}

	// deleting goes to the trash; only unchanged files go when asked
	_ = json.Unmarshal(ed.do("POST", "/api/fs/delete", map[string]any{"items": []map[string]any{
		{"path": "PROJECTS/desk-lamp/parts/shade-v2.stl", "size": 5, "mtime": 1},
	}}, 200), &res)
	if res[0]["ok"] == true || res[0]["code"] != float64(409) {
		t.Fatalf("stale delete: %v", res)
	}
	_ = json.Unmarshal(ed.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"PROJECTS/desk-lamp/parts/shade-v2.stl", "DOCS/notes.txt"}}, 200), &res)
	if res[0]["ok"] != true || res[1]["ok"] == true {
		t.Fatalf("delete results: %v", res)
	}
	if _, err := os.Stat(filepath.Join(e.data, "PROJECTS/desk-lamp/parts/shade-v2.stl")); !os.IsNotExist(err) {
		t.Fatal("file still there")
	}
	var trash []TrashItem
	_ = json.Unmarshal(ed.do("GET", "/api/trash", nil, 200), &trash)
	if len(trash) != 1 || trash[0].Path != "PROJECTS/desk-lamp/parts/shade-v2.stl" {
		t.Fatalf("trash: %+v", trash)
	}
	// the trash is hidden from browsing and search
	if b := ed.do("GET", "/api/ls?path=PROJECTS", nil, 200); strings.Contains(string(b), "burrow-trash") {
		t.Fatal("trash folder listed")
	}
	if b := e.do("GET", "/api/search?q=shade", nil, 200); strings.Contains(string(b), "shade-v2") {
		t.Fatalf("trashed file in search: %s", b)
	}
	// restoring puts it back; a name taken in the meantime gets "(restored 2)"
	_ = os.WriteFile(filepath.Join(e.data, "PROJECTS/desk-lamp/parts/shade-v2.stl"), []byte("new"), 0o644)
	var rest map[string]string
	_ = json.Unmarshal(ed.do("POST", "/api/trash/"+trash[0].ID+"/restore", nil, 200), &rest)
	if rest["path"] != "PROJECTS/desk-lamp/parts/shade-v2 (restored 2).stl" || readFile(t, filepath.Join(e.data, rest["path"])) != "solid" {
		t.Fatalf("restore: %v", rest)
	}
	// someone else's trash is out of reach; an admin sees it with ?all=1
	_ = json.Unmarshal(ed.do("POST", "/api/fs/delete", map[string]any{"paths": []string{rest["path"]}}, 200), &res)
	_ = json.Unmarshal(ed.do("GET", "/api/trash", nil, 200), &trash)
	e.mkUser("other", false, Grant{Path: "PROJECTS", Read: true, Write: true})
	other := e.as("other", "longenough-other")
	other.do("POST", "/api/trash/"+trash[0].ID+"/restore", nil, 404)
	other.do("DELETE", "/api/trash/"+trash[0].ID, nil, 404)
	var all []TrashItem
	_ = json.Unmarshal(e.do("GET", "/api/trash?all=1", nil, 200), &all)
	if len(all) != 1 || all[0].User != "ed" {
		t.Fatalf("admin trash view: %+v", all)
	}
	// deleting forever, and the 30-day purge
	ed.do("DELETE", "/api/trash/"+trash[0].ID, nil, 200)
	_ = json.Unmarshal(ed.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"PROJECTS/desk-lamp/a"}}, 200), &res)
	_, _ = e.app.st.exec(`UPDATE trash SET deleted=?`, time.Now().Add(-31*24*time.Hour).Unix())
	e.app.purgeTrash()
	_ = json.Unmarshal(ed.do("GET", "/api/trash", nil, 200), &trash)
	if len(trash) != 0 {
		t.Fatalf("purge left: %+v", trash)
	}
	slots, _ := os.ReadDir(filepath.Join(e.data, "PROJECTS", trashName))
	if len(slots) != 0 {
		t.Fatalf("trash folder not emptied: %d", len(slots))
	}
}

func TestResumableUploads(t *testing.T) {
	e := setup(t)
	body := bytes.Repeat([]byte("0123456789abcdef"), 40000) // 640,000 bytes
	out := e.upload("PROJECTS/lamp", "big.bin", body, 250000, nil, 200)
	if out["path"] != "PROJECTS/lamp/big.bin" || out["sha256"] != sum256(body) || out["mtime"] != float64(1700000000123) {
		t.Fatalf("finish: %v", out)
	}
	got := readFile(t, filepath.Join(e.data, "PROJECTS/lamp/big.bin"))
	if got != string(body) {
		t.Fatal("contents differ")
	}
	fi, _ := os.Stat(filepath.Join(e.data, "PROJECTS/lamp/big.bin"))
	if fi.ModTime().UnixMilli() != 1700000000123 {
		t.Fatalf("mtime not kept: %v", fi.ModTime())
	}

	// a wrong offset is answered with the right one; a cut-off chunk is resumed
	var st map[string]any
	_ = json.Unmarshal(e.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": "resume.bin", "size": len(body)}, 200), &st)
	id := st["id"].(string)
	if code, b := e.put("/api/uploads/"+id+"?offset=0", body[:100000]); code != 200 || !strings.Contains(string(b), "100000") {
		t.Fatalf("first chunk: %d %s", code, b)
	}
	if code, b := e.put("/api/uploads/"+id+"?offset=5", body[5:10]); code != 409 || !strings.Contains(string(b), `"offset":100000`) {
		t.Fatalf("wrong offset: %d %s", code, b)
	}
	// extra bytes past the declared size are refused
	if code, _ := e.put("/api/uploads/"+id+"?offset=100000", append(append([]byte{}, body[100000:]...), 'x')); code != 400 {
		t.Fatalf("too long: %d", code)
	}
	var stat map[string]any
	_ = json.Unmarshal(e.do("GET", "/api/uploads/"+id, nil, 200), &stat)
	if stat["offset"] != float64(100000) {
		t.Fatalf("status: %v", stat)
	}
	// a garbage tail left on disk by a crash is cut off before the next chunk
	f, _ := os.OpenFile(filepath.Join(e.data, "PROJECTS/lamp", partName(id)), os.O_WRONLY|os.O_APPEND, 0)
	_, _ = f.Write([]byte("garbage"))
	f.Close()
	e.put("/api/uploads/"+id+"?offset=100000", body[100000:])
	var fin map[string]any
	_ = json.Unmarshal(e.do("POST", "/api/uploads/"+id+"/finish", map[string]any{"sha256": sum256(body)}, 200), &fin)
	if fin["sha256"] != sum256(body) || readFile(t, filepath.Join(e.data, "PROJECTS/lamp/resume.bin")) != string(body) {
		t.Fatalf("resumed upload: %v", fin)
	}
	// unfinished: hidden while in progress
	_ = json.Unmarshal(e.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": "wip.bin", "size": 10}, 200), &st)
	if b := e.do("GET", "/api/ls?path=PROJECTS/lamp", nil, 200); strings.Contains(string(b), "burrow-part") || strings.Contains(string(b), "wip.bin") {
		t.Fatalf("partial upload listed: %s", b)
	}
	e.do("POST", "/api/uploads/"+st["id"].(string)+"/finish", map[string]any{}, 409)
	e.do("DELETE", "/api/uploads/"+st["id"].(string), nil, 200)
	if left, _ := filepath.Glob(filepath.Join(e.data, "PROJECTS/lamp", partPrefix+"*")); len(left) != 0 {
		t.Fatalf("cancel left %v", left)
	}

	// damaged in transit: refused, nothing kept
	e.upload("PROJECTS/lamp", "bad.bin", []byte("abc"), 10, map[string]any{"sha256": sum256([]byte("abd"))}, 422)
	if _, err := os.Stat(filepath.Join(e.data, "PROJECTS/lamp/bad.bin")); !os.IsNotExist(err) {
		t.Fatal("damaged upload kept")
	}

	// name conflicts
	e.upload("PROJECTS/lamp", "shade.stl", []byte("v2"), 10, nil, 200) // rename (the default)
	if readFile(t, filepath.Join(e.data, "PROJECTS/lamp/shade (2).stl")) != "v2" || readFile(t, filepath.Join(e.data, "PROJECTS/lamp/shade.stl")) != "solid" {
		t.Fatal("rename on conflict")
	}
	if out := e.upload("PROJECTS/lamp", "shade.stl", []byte("v3"), 10, map[string]any{"conflict": "skip"}, 200); out["skipped"] != true {
		t.Fatalf("skip: %v", out)
	}
	e.upload("PROJECTS/lamp", "shade.stl", []byte("v3"), 10, map[string]any{"conflict": "fail"}, 409)
	cur, _ := os.Stat(filepath.Join(e.data, "PROJECTS/lamp/shade.stl"))
	// replacing a version that changed in the meantime is refused
	e.upload("PROJECTS/lamp", "shade.stl", []byte("v3"), 10, map[string]any{"conflict": "overwrite", "expect_size": 5, "expect_mtime": 1}, 409)
	e.upload("PROJECTS/lamp", "shade.stl", []byte("v3"), 10, map[string]any{"conflict": "overwrite", "expect_size": cur.Size(), "expect_mtime": cur.ModTime().UnixMilli()}, 200)
	if readFile(t, filepath.Join(e.data, "PROJECTS/lamp/shade.stl")) != "v3" {
		t.Fatal("overwrite")
	}
	var trash []TrashItem
	_ = json.Unmarshal(e.do("GET", "/api/trash", nil, 200), &trash)
	var stored string
	_ = e.app.st.db.QueryRow(`SELECT stored FROM trash`).Scan(&stored)
	if len(trash) != 1 || trash[0].Path != "PROJECTS/lamp/shade.stl" || readFile(t, stored) != "solid" {
		t.Fatalf("old version not kept in the trash: %+v", trash)
	}

	// where the user can't write
	e.mkUser("ro", false, Grant{Path: "PROJECTS", Read: true})
	ro := e.as("ro", "longenough-ro")
	ro.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": "x", "size": 1}, 403)
	e.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": ".burrow-part-x", "size": 1}, 400)
	e.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": "a/b", "size": 1}, 400)
	e.do("POST", "/api/uploads", map[string]any{"dir": "PRIVATE", "name": "x", "size": 1}, 404)
	// someone else's upload can't be touched
	_ = json.Unmarshal(e.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": "mine.bin", "size": 3}, 200), &st)
	if code, _ := ro.put("/api/uploads/"+st["id"].(string)+"?offset=0", []byte("abc")); code != 404 {
		t.Fatalf("other user's chunk: %d", code)
	}
}

func TestReadOnlyMountRefusesWrites(t *testing.T) {
	e := setup(t)
	orig := mountWritable
	mountWritable = func(string) bool { return false }
	defer func() { mountWritable = orig }()
	b := e.do("POST", "/api/uploads", map[string]any{"dir": "PROJECTS/lamp", "name": "x", "size": 1}, 403)
	if !strings.Contains(string(b), "read-only") {
		t.Fatalf("read-only mount: %s", b)
	}
	e.do("POST", "/api/fs/mkdir", map[string]any{"path": "PROJECTS/lamp/x"}, 403)
	var res []map[string]any
	_ = json.Unmarshal(e.do("POST", "/api/fs/delete", map[string]any{"paths": []string{"PROJECTS/lamp/shade.stl"}}, 200), &res)
	if res[0]["ok"] == true {
		t.Fatal("deleted on a read-only mount")
	}
}

func TestSyncListing(t *testing.T) {
	e := setup(t)
	_ = os.MkdirAll(filepath.Join(e.data, "PROJECTS/lamp/hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(e.data, "PROJECTS/lamp/hidden/x.txt"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(e.data, "PROJECTS/lamp/Thumbs.db"), []byte("x"), 0o644)
	e.app.idx.fullScan()
	e.mkUser("sy", false, Grant{Path: "PROJECTS/lamp", Read: true, Write: true}, Grant{Path: "PROJECTS/lamp/hidden", Read: false})
	sy := e.as("sy", "longenough-sy")
	up := sy.upload("PROJECTS/lamp/sub", "photo.jpg", []byte("jpegdata"), 4, nil, 200)

	var l struct {
		Path     string
		Cursor   int64
		Writable bool
		Excludes []string
		Entries  []syncEntry
	}
	_ = json.Unmarshal(sy.do("GET", "/api/sync/list?path=PROJECTS/lamp", nil, 200), &l)
	got := map[string]syncEntry{}
	for _, en := range l.Entries {
		got[en.Path] = en
	}
	if _, ok := got["shade.stl"]; !ok || !got["sub"].IsDir || got["sub/inner.txt"].Size != 5 || !l.Writable {
		t.Fatalf("listing: %+v", l)
	}
	for _, hidden := range []string{"hidden", "hidden/x.txt", "Thumbs.db"} {
		if _, ok := got[hidden]; ok {
			t.Fatalf("%s listed", hidden)
		}
	}
	if p := got["sub/photo.jpg"]; p.Hash != up["sha256"] || p.Mtime != 1700000000123 {
		t.Fatalf("uploaded file in the listing: %+v", p)
	}
	if len(l.Excludes) == 0 || l.Excludes[0] != ".burrow-*" {
		t.Fatalf("excludes: %v", l.Excludes)
	}

	// changed since the cursor?
	var ch map[string]any
	_ = json.Unmarshal(sy.do("GET", fmt.Sprintf("/api/sync/changed?path=PROJECTS/lamp&since=%d", l.Cursor), nil, 200), &ch)
	if ch["changed"] != false {
		t.Fatalf("nothing changed yet: %v", ch)
	}
	sy.upload("PROJECTS/lamp", "new.txt", []byte("n"), 4, nil, 200)
	_ = json.Unmarshal(sy.do("GET", fmt.Sprintf("/api/sync/changed?path=PROJECTS/lamp&since=%d", l.Cursor), nil, 200), &ch)
	if ch["changed"] != true {
		t.Fatalf("upload not seen as a change: %v", ch)
	}
	_ = json.Unmarshal(sy.do("GET", fmt.Sprintf("/api/sync/changed?path=DOCS&since=%d", l.Cursor), nil, 404), &ch)

	// hashes are computed on request and cached
	var h map[string]any
	_ = json.Unmarshal(sy.do("GET", "/api/sync/hash?path=PROJECTS/lamp/shade.stl", nil, 200), &h)
	if h["sha256"] != sum256([]byte("solid")) {
		t.Fatalf("hash: %v", h)
	}
	sy.do("GET", "/api/sync/hash?path=PROJECTS/lamp/hidden/x.txt", nil, 404)
}

func TestUploadThroughDeviceChannel(t *testing.T) {
	e := setup(t)
	d := pairedDevice(t, e, "Phone")
	body := make([]byte, 3*chSegMax+123)
	for i := range body {
		body[i] = byte(i * 7)
	}
	start, _ := json.Marshal(map[string]any{"dir": "PROJECTS/lamp", "name": "video.mp4", "size": len(body), "mtime": 1700000000000, "sha256": sum256(body)})
	r := d.do("POST", "/api/uploads", map[string]string{"Content-Type": "application/json"}, start)
	var st map[string]any
	_ = json.Unmarshal(r.body, &st)
	id, _ := st["id"].(string)
	if r.status != 200 || id == "" {
		t.Fatalf("start: %+v %s", r, r.body)
	}
	half := len(body) / 2
	for _, c := range [][2]int{{0, half}, {half, len(body)}} {
		if r = d.do("PUT", fmt.Sprintf("/api/uploads/%s?offset=%d", id, c[0]), nil, body[c[0]:c[1]]); r.status != 200 {
			t.Fatalf("chunk: %+v %s", r, r.body)
		}
	}
	if r = d.do("POST", "/api/uploads/"+id+"/finish", nil, []byte(`{}`)); r.status != 200 {
		t.Fatalf("finish: %+v %s", r, r.body)
	}
	r = d.do("GET", "/api/raw?path=PROJECTS/lamp/video.mp4", nil, nil)
	if r.status != 200 || !bytes.Equal(r.body, body) {
		t.Fatalf("download back: %d, %d bytes", r.status, len(r.body))
	}
	if r = d.do("GET", "/api/sync/list?path=PROJECTS/lamp", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "video.mp4") {
		t.Fatalf("sync list: %+v", r)
	}
}
