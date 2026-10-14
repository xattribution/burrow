package main

// Changing the library: new folders, moves and renames, deletes into a trash,
// resumable uploads, and the listings the phone app syncs against.
//
// Writing needs a write grant and a folder mounted writable (resolveFor checks
// both: the kernel has the last word). A user can't move or delete a folder
// holding something they may not change, so carve-outs can't be removed or
// carried off by someone who can't see them. Moves made here take grants,
// favorites, links and cached hashes along to the new path.

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const writeSchema = `
CREATE TABLE IF NOT EXISTS trash (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  path TEXT NOT NULL,
  stored TEXT NOT NULL,
  is_dir INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS trash_user ON trash(user_id, deleted);
CREATE TABLE IF NOT EXISTS uploads (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  dir TEXT NOT NULL,
  name TEXT NOT NULL,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL DEFAULT 0,
  sha256 TEXT NOT NULL DEFAULT '',
  conflict TEXT NOT NULL DEFAULT 'rename',
  expect_size INTEGER NOT NULL DEFAULT -1,
  expect_mtime INTEGER NOT NULL DEFAULT -1,
  off INTEGER NOT NULL DEFAULT 0,
  state BLOB,
  created INTEGER NOT NULL,
  updated INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS file_hashes (path TEXT PRIMARY KEY, size INTEGER NOT NULL, mtime INTEGER NOT NULL, sha256 TEXT NOT NULL);
`

const (
	trashName      = ".burrow-trash" // hidden (".burrow-" names always are)
	trashKeep      = 30 * 24 * time.Hour
	uploadKeep     = 7 * 24 * time.Hour // unfinished uploads
	uploadChunkMax = 64 << 20
	uploadsOpenMax = 100 // per user
	syncListMax    = 300000
)

var (
	errExists     = errors.New("something with that name is already there")
	errBadName    = errors.New("that isn't a valid name")
	errReserved   = errors.New("names starting with .burrow- are reserved")
	errHiddenName = errors.New("Burrow hides names like that (Settings > Hidden names), so it can't be created here")
	errNoWrite    = errors.New("you can't change this folder")
	errHasLocked  = errors.New("this folder holds something you can't change, so it can't be moved or deleted")
	errLibRoot    = errors.New("library folders can't be moved or deleted from Burrow")
	errMountPoint = errors.New("this folder is a separate mount; it can't be moved or deleted from Burrow")
	errCrossMount = errors.New("those two folders are on different mounts; move it from the NAS instead")
	errChanged    = errors.New("it was changed in the meantime")
)

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotAllowed), errors.Is(err, fs.ErrNotExist):
		httpErr(w, 404, "not found")
	case errors.Is(err, errNoWrite), errors.Is(err, errReadOnly), errors.Is(err, errHasLocked):
		httpErr(w, 403, err.Error())
	case errors.Is(err, errExists), errors.Is(err, errChanged):
		httpErr(w, 409, err.Error())
	case errors.Is(err, errNoSpace):
		httpErr(w, 507, err.Error())
	default:
		httpErr(w, 400, err.Error())
	}
}

// ---- checks ----

func (a *App) validName(n string) error {
	if n == "" || n == "." || n == ".." || len(n) > 255 || strings.ContainsAny(n, "/\\\x00") || strings.TrimSpace(n) == "" {
		return errBadName
	}
	for _, r := range n {
		if r < 32 || r == 127 {
			return errBadName
		}
	}
	if strings.HasPrefix(strings.ToLower(n), ".burrow-") {
		return errReserved
	}
	if a.idx.excluded(n) {
		return errHiddenName
	}
	return nil
}

// writableDir resolves an existing folder the user may write into.
func (a *App) writableDir(u *User, dir string) (string, string, error) {
	abs, rel, err := a.resolve(dir)
	if err != nil || !u.canReach(rel, permRead) {
		return "", "", errNotAllowed
	}
	if !u.can(rel, permWrite) {
		return "", "", errNoWrite
	}
	if !mountWritable(abs) {
		return "", "", errReadOnly
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", "", errNotAllowed
	}
	return abs, rel, nil
}

// newEntry checks a name for something new in dir.
func (a *App) newEntry(u *User, dir, name string) (dirAbs, dirRel, rel string, err error) {
	if dirAbs, dirRel, err = a.writableDir(u, dir); err != nil {
		return
	}
	if err = a.validName(name); err != nil {
		return
	}
	rel = dirRel + "/" + name
	if !u.can(rel, permWrite) {
		err = errNoWrite
	}
	return
}

// existingWritable resolves an existing file or folder the user may change,
// moved or deleted as a whole.
func (a *App) existingWritable(u *User, p string) (string, string, os.FileInfo, error) {
	abs, rel, err := a.resolve(p)
	if err != nil || !u.canReach(rel, permRead) {
		return "", "", nil, errNotAllowed
	}
	if !strings.Contains(rel, "/") {
		return "", "", nil, errLibRoot
	}
	if !u.can(rel, permWrite) {
		return "", "", nil, errNoWrite
	}
	if !mountWritable(filepath.Dir(abs)) {
		return "", "", nil, errReadOnly
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return "", "", nil, errNotAllowed
	}
	if fi.IsDir() && !u.canWriteTree(rel) {
		return "", "", nil, errHasLocked
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil && mountOf(real) == real {
		return "", "", nil, errMountPoint
	}
	return abs, rel, fi, nil
}

// canWriteTree: may u change rel and everything inside it? A grant inside rel
// that doesn't give write (a read-only or no-access carve-out) says no.
func (u *User) canWriteTree(rel string) bool {
	if !u.can(rel, permWrite) {
		return false
	}
	if u.Admin {
		return true
	}
	for _, g := range u.Grants {
		if strings.HasPrefix(g.Path, rel+"/") && !g.has(permWrite) {
			return false
		}
	}
	return true
}

// mountOf returns the mount point holding abs, from /proc/self/mountinfo.
func mountOf(abs string) string {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	best := ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := unescapeMount(f[4])
		if (abs == mp || mp == "/" || strings.HasPrefix(abs, mp+"/")) && len(mp) > len(best) {
			best = mp
		}
	}
	return best
}

// unescapeMount undoes mountinfo's octal escapes (\040 for a space and so on).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func sameMount(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && mountOf(ra) == mountOf(rb)
}

// renameNoReplace renames without ever overwriting what is at to.
func renameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return errExists
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL):
		if _, err := os.Lstat(to); err == nil {
			return errExists
		}
		return os.Rename(from, to)
	case errors.Is(err, unix.EXDEV):
		return errCrossMount
	}
	return err
}

func mtimeMs(fi os.FileInfo) int64 { return fi.ModTime().UnixMilli() }

// ---- paths follow moves ----

// movePaths rewrites library paths stored in the database when from moves to to,
// so grants, favorites, links and cached hashes keep pointing at the same things.
func (a *App) movePaths(from, to string) error {
	a.st.wmu.Lock()
	defer a.st.wmu.Unlock()
	tx, err := a.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	in := `(path = ? OR (path >= ? AND path < ?))`
	args := []any{to, len(from) + 1, from, from + "/", from + "0"}
	for _, q := range []string{
		`UPDATE OR REPLACE grants SET path = ? || substr(path, ?) WHERE ` + in,
		`UPDATE OR REPLACE favs SET path = ? || substr(path, ?) WHERE ` + in,
		`UPDATE shares SET path = ? || substr(path, ?) WHERE mode='link' AND ` + in,
		`UPDATE OR REPLACE share_items SET path = ? || substr(path, ?) WHERE ` + in,
		`UPDATE OR REPLACE file_hashes SET path = ? || substr(path, ?) WHERE ` + in,
	} {
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- new folder, move, delete ----

func (a *App) apiMkdir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string
		Parents bool
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	e, err := a.mkdir(userOf(r), in.Path, in.Parents)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, e)
}

// mkdir makes the folder p, and any missing parents when parents is set.
func (a *App) mkdir(u *User, p string, parents bool) (Entry, error) {
	if _, _, ok := a.remoteOf(p); ok {
		return a.remoteMkdir(u, p, parents)
	}
	segs := strings.Split(cleanRel(p), "/")
	if len(segs) < 2 {
		return Entry{}, errNoWrite
	}
	// the longest part that exists, then each missing folder in turn
	n := len(segs) - 1
	for ; n > 1; n-- {
		if abs, _, err := a.resolve(strings.Join(segs[:n], "/")); err == nil {
			if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
				break
			}
		}
	}
	if n < len(segs)-1 && !parents {
		return Entry{}, fmt.Errorf("the folder %s doesn't exist", strings.Join(segs[:n+1], "/"))
	}
	created := false
	for i := n; i < len(segs); i++ {
		dir := strings.Join(segs[:i], "/")
		dabs, _, _, err := a.newEntry(u, dir, segs[i])
		if err != nil {
			return Entry{}, err
		}
		err = os.Mkdir(filepath.Join(dabs, segs[i]), 0o775)
		if errors.Is(err, fs.ErrExist) {
			if fi, serr := os.Lstat(filepath.Join(dabs, segs[i])); serr == nil && fi.IsDir() && (i < len(segs)-1 || parents) {
				continue
			}
			return Entry{}, errExists
		}
		if err != nil {
			return Entry{}, errors.New("could not create the folder")
		}
		if !created {
			defer a.idx.Refresh(dir)
			created = true
		}
	}
	abs, rel, _ := a.resolve(p)
	fi, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(rel, fi), nil
}

func (a *App) apiMove(w http.ResponseWriter, r *http.Request) {
	var in struct{ From, To string }
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	to, job, err := a.moveJob(userOf(r), in.From, in.To)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]string{"path": to}
	if job != "" {
		out["job"] = job
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = jsonEncode(w, out)
		return
	}
	writeJSON(w, out)
}

// move moves or renames fromP to toP (in the background when it has to copy).
func (a *App) move(u *User, fromP, toP string) (string, error) {
	to, _, err := a.moveJob(u, fromP, toP)
	return to, err
}

// moveLocal moves or renames fromP to toP on this NAS without ever replacing
// anything there, and returns the new library path.
func (a *App) moveLocal(u *User, fromP, toP string) (string, error) {
	from, fromRel, _, err := a.existingWritable(u, fromP)
	if err != nil {
		return "", err
	}
	to := cleanRel(toP)
	dir, name := path.Split(to)
	dabs, dirRel, toRel, err := a.newEntry(u, strings.TrimSuffix(dir, "/"), name)
	if err != nil {
		return "", err
	}
	if toRel == fromRel {
		return toRel, nil
	}
	if strings.HasPrefix(toRel+"/", fromRel+"/") {
		return "", errors.New("a folder can't be moved into itself")
	}
	if !sameMount(filepath.Dir(from), dabs) {
		return "", errCrossMount
	}
	a.shareMu.Lock() // links are rewritten with the move
	defer a.shareMu.Unlock()
	if err := renameNoReplace(from, filepath.Join(dabs, name)); err != nil {
		return "", err
	}
	if err := a.movePaths(fromRel, toRel); err != nil {
		return "", err
	}
	a.idx.Refresh(path.Dir(fromRel), dirRel)
	return toRel, nil
}

// trashDir is where something at abs goes when deleted: a hidden folder on the
// same mount (so deleting is a rename), at the mount's top or the library folder's.
func (a *App) trashDir(abs, rel string) string {
	root := filepath.Join(a.cfg.DataDir, strings.SplitN(rel, "/", 2)[0])
	base := root
	if real, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		if rroot, err := filepath.EvalSymlinks(root); err == nil {
			if m := mountOf(real); strings.HasPrefix(m, rroot+"/") {
				base = m
			}
		}
	}
	return filepath.Join(base, trashName)
}

type TrashItem struct {
	ID      string `json:"id"`
	UserID  int64  `json:"user_id"`
	User    string `json:"user,omitempty"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	IsDir   bool   `json:"dir"`
	Size    int64  `json:"size"`
	Deleted int64  `json:"deleted"`
	stored  string
}

// toTrash moves an approved path into the trash.
func (a *App) toTrash(u *User, abs, rel string, fi os.FileInfo) (string, error) {
	dir := a.trashDir(abs, rel)
	id := randSlug(12)
	slot := filepath.Join(dir, id)
	if err := os.MkdirAll(slot, 0o770); err != nil {
		return "", errors.New("could not make room in the trash")
	}
	dst := filepath.Join(slot, filepath.Base(abs))
	size := fi.Size()
	if fi.IsDir() {
		size = 0
		_ = a.st.db.QueryRow(`SELECT coalesce(sum(size),0) FROM files WHERE path >= ? AND path < ?`, rel+"/", rel+"0").Scan(&size)
	}
	if err := renameNoReplace(abs, dst); err != nil {
		_ = os.Remove(slot)
		return "", errors.New("could not move it to the trash")
	}
	if _, err := a.st.exec(`INSERT INTO trash(id,user_id,path,stored,is_dir,size,deleted) VALUES(?,?,?,?,?,?,?)`, id, u.ID, rel, dst, fi.IsDir(), size, now()); err != nil {
		_ = os.Rename(dst, abs)
		_ = os.Remove(slot)
		return "", errors.New("could not record the deletion")
	}
	return id, nil
}

func (a *App) apiDelete(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	type item struct {
		Path  string `json:"path"`
		Size  *int64 `json:"size"`
		Mtime *int64 `json:"mtime"` // milliseconds; with size: only if unchanged since
	}
	var in struct {
		Paths []string `json:"paths"`
		Items []item   `json:"items"`
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	for _, p := range in.Paths {
		in.Items = append(in.Items, item{Path: p})
	}
	if len(in.Items) > 1000 {
		httpErr(w, 400, "too many at once (1000 at most)")
		return
	}
	type result struct {
		Path    string `json:"path"`
		OK      bool   `json:"ok"`
		TrashID string `json:"trash_id,omitempty"`
		Error   string `json:"error,omitempty"`
		Code    int    `json:"code,omitempty"`
	}
	out := []result{}
	dirs := map[string]bool{}
	for _, it := range in.Items {
		res := result{Path: cleanRel(it.Path)}
		var rel string
		var err error
		res.TrashID, rel, err = a.trash(u, it.Path, it.Size, it.Mtime)
		if err != nil {
			res.Error = err.Error()
			res.Code = map[bool]int{true: 409, false: 403}[errors.Is(err, errChanged)]
			if errors.Is(err, errNotAllowed) {
				res.Code, res.Error = 404, "not found"
			}
		} else {
			res.OK = true
			dirs[path.Dir(rel)] = true
		}
		out = append(out, res)
	}
	for d := range dirs {
		a.idx.Refresh(d)
	}
	writeJSON(w, out)
}

// trash moves p to the trash (only if it is still the version the caller saw, when
// size and mtime are given) and returns the trash id and p's library path.
func (a *App) trash(u *User, p string, size, mtime *int64) (string, string, error) {
	if _, _, ok := a.remoteOf(p); ok {
		return a.remoteTrash(u, p, size, mtime)
	}
	abs, rel, fi, err := a.existingWritable(u, p)
	if err == nil && size != nil && mtime != nil && !fi.IsDir() && (fi.Size() != *size || mtimeMs(fi) != *mtime) {
		err = errChanged
	}
	if err != nil {
		return "", rel, err
	}
	id, err := a.toTrash(u, abs, rel, fi)
	return id, rel, err
}

// writeText creates the text file p, or replaces it when overwrite is set (the old
// version goes to the trash). Like an upload it is written under a hidden name,
// flushed to disk, then renamed into place.
func (a *App) writeText(u *User, p, text string, overwrite bool) (map[string]any, error) {
	dir, name := path.Split(cleanRel(p))
	dabs, dirRel, rel, err := a.newEntry(u, strings.TrimSuffix(dir, "/"), name)
	if err != nil {
		return nil, err
	}
	if err := a.checkSpaceAt(dabs, int64(len(text))); err != nil {
		return nil, err
	}
	target := filepath.Join(dabs, name)
	cur, statErr := os.Lstat(target)
	exists := statErr == nil
	if exists && (!overwrite || !cur.Mode().IsRegular()) {
		return nil, errExists
	}
	tmp := filepath.Join(dabs, partPrefix+"text-"+randSlug(12))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return nil, errors.New("could not write the file")
	}
	_, werr := f.WriteString(text)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return nil, errors.New("could not write the file")
	}
	if exists {
		if _, err = a.toTrashCopy(u, target, rel, cur); err == nil {
			err = os.Rename(tmp, target)
		}
	} else {
		err = renameNoReplace(tmp, target)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	fi, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(text))
	_, _ = a.st.exec(`INSERT OR REPLACE INTO file_hashes(path,size,mtime,sha256) VALUES(?,?,?,?)`, rel, fi.Size(), mtimeMs(fi), hex.EncodeToString(sum[:]))
	a.idx.Refresh(dirRel)
	return map[string]any{"path": rel, "size": humanSize(fi.Size()), "replaced": exists}, nil
}

// ---- trash ----

func (a *App) trashItems(u *User, all bool) ([]TrashItem, error) {
	q := `SELECT t.id,t.user_id,coalesce(u.username,''),t.path,t.stored,t.is_dir,t.size,t.deleted FROM trash t LEFT JOIN users u ON u.id=t.user_id`
	var args []any
	if !(all && u.Admin) {
		q += ` WHERE t.user_id=?`
		args = append(args, u.ID)
	}
	rows, err := a.st.db.Query(q+` ORDER BY t.deleted DESC LIMIT 2000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashItem{}
	for rows.Next() {
		var t TrashItem
		if err := rows.Scan(&t.ID, &t.UserID, &t.User, &t.Path, &t.stored, &t.IsDir, &t.Size, &t.Deleted); err != nil {
			return out, err
		}
		t.Name = path.Base(t.Path)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (a *App) apiTrash(w http.ResponseWriter, r *http.Request) {
	items, err := a.trashItems(userOf(r), r.URL.Query().Get("all") == "1")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, items)
}

func (a *App) trashItem(u *User, id string) (TrashItem, bool) {
	var t TrashItem
	err := a.st.db.QueryRow(`SELECT id,user_id,path,stored,is_dir,size,deleted FROM trash WHERE id=?`, id).
		Scan(&t.ID, &t.UserID, &t.Path, &t.stored, &t.IsDir, &t.Size, &t.Deleted)
	if err != nil || (t.UserID != u.ID && !u.Admin) {
		return t, false
	}
	t.Name = path.Base(t.Path)
	return t, true
}

// safeStored checks a trash row still points inside a trash folder.
func safeStored(p string) bool {
	return strings.Contains(p, string(os.PathSeparator)+trashName+string(os.PathSeparator)) && !strings.Contains(p, "..")
}

func (a *App) apiRestore(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	t, ok := a.trashItem(u, r.PathValue("id"))
	if ok && strings.HasPrefix(t.stored, remoteStored) {
		a.remoteRestore(w, u, t)
		return
	}
	if !ok || !safeStored(t.stored) {
		httpErr(w, 404, "not in the trash")
		return
	}
	dir, name := path.Split(t.Path)
	dabs, dirRel, _, err := a.newEntry(u, strings.TrimSuffix(dir, "/"), name)
	if err != nil {
		if errors.Is(err, errNotAllowed) {
			err = errors.New("the folder it was in is gone or out of reach; restore or recreate that first")
		}
		writeErr(w, err)
		return
	}
	ext := filepath.Ext(name)
	if t.IsDir {
		ext = ""
	}
	base := strings.TrimSuffix(name, ext)
	var target string
	for i := 1; i < 100; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (restored %d)%s", base, i, ext)
		}
		if a.validName(cand) != nil {
			break
		}
		if err = renameNoReplace(t.stored, filepath.Join(dabs, cand)); err == nil {
			target = dirRel + "/" + cand
			break
		}
		if !errors.Is(err, errExists) {
			break
		}
	}
	if target == "" {
		writeErr(w, errors.New("could not restore it"))
		return
	}
	_ = os.Remove(filepath.Dir(t.stored))
	_, _ = a.st.exec(`DELETE FROM trash WHERE id=?`, t.ID)
	a.idx.Refresh(dirRel)
	writeJSON(w, map[string]string{"path": target})
}

func (a *App) apiPurge(w http.ResponseWriter, r *http.Request) {
	t, ok := a.trashItem(userOf(r), r.PathValue("id"))
	if ok && strings.HasPrefix(t.stored, remoteStored) {
		if err := a.remotePurge(t.stored); err != nil {
			httpErr(w, 500, "could not delete it")
			return
		}
		_, _ = a.st.exec(`DELETE FROM trash WHERE id=?`, t.ID)
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if !ok || !safeStored(t.stored) {
		httpErr(w, 404, "not in the trash")
		return
	}
	if err := os.RemoveAll(filepath.Dir(t.stored)); err != nil {
		httpErr(w, 500, "could not delete it")
		return
	}
	_, _ = a.st.exec(`DELETE FROM trash WHERE id=?`, t.ID)
	writeJSON(w, map[string]bool{"ok": true})
}

// purgeTrash deletes what has been in the trash for 30 days, and unfinished uploads
// left alone for a week.
func (a *App) purgeTrash() {
	rows, err := a.st.db.Query(`SELECT id,stored FROM trash WHERE deleted < ?`, time.Now().Add(-trashKeep).Unix())
	if err == nil {
		var ids, stored []string
		for rows.Next() {
			var id, s string
			_ = rows.Scan(&id, &s)
			ids, stored = append(ids, id), append(stored, s)
		}
		rows.Close()
		for i := range ids {
			if strings.HasPrefix(stored[i], remoteStored) {
				if a.remotePurge(stored[i]) != nil {
					continue // the connected NAS is offline: try again next time
				}
			} else if safeStored(stored[i]) {
				_ = os.RemoveAll(filepath.Dir(stored[i]))
			}
			_, _ = a.st.exec(`DELETE FROM trash WHERE id=?`, ids[i])
		}
	}
	rows, err = a.st.db.Query(`SELECT id,dir FROM uploads WHERE updated < ?`, time.Now().Add(-uploadKeep).Unix())
	if err == nil {
		var ids, dirs []string
		for rows.Next() {
			var id, d string
			_ = rows.Scan(&id, &d)
			ids, dirs = append(ids, id), append(dirs, d)
		}
		rows.Close()
		for i := range ids {
			a.dropUpload(ids[i], dirs[i])
		}
	}
	_, _ = a.st.exec(`DELETE FROM file_hashes WHERE path NOT IN (SELECT path FROM files)`)
}

// ---- resumable uploads ----

type upload struct {
	ID, Dir, Name, SHA, Conflict string
	UserID                       int64
	Size, Mtime, Off             int64
	ExpectSize, ExpectMtime      int64
	State                        []byte
}

func (a *App) loadUpload(u *User, id string) (*upload, bool) {
	up := &upload{ID: id}
	err := a.st.db.QueryRow(`SELECT user_id,dir,name,size,mtime,sha256,conflict,expect_size,expect_mtime,off,state FROM uploads WHERE id=?`, id).
		Scan(&up.UserID, &up.Dir, &up.Name, &up.Size, &up.Mtime, &up.SHA, &up.Conflict, &up.ExpectSize, &up.ExpectMtime, &up.Off, &up.State)
	if err != nil || up.UserID != u.ID {
		return nil, false
	}
	return up, true
}

func (a *App) uploadLock(id string) func() {
	m, _ := a.upLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func partName(id string) string { return partPrefix + "up-" + id }

func (a *App) dropUpload(id, dirRel string) {
	if _, _, ok := a.remoteOf(dirRel); ok {
		if d, err := a.remotePartsDir(); err == nil {
			_ = os.Remove(filepath.Join(d, partName(id)))
		}
	} else if abs, _, err := a.resolve(dirRel); err == nil {
		_ = os.Remove(filepath.Join(abs, partName(id)))
	}
	_, _ = a.st.exec(`DELETE FROM uploads WHERE id=?`, id)
	a.upLocks.Delete(id)
}

var conflictModes = map[string]bool{"rename": true, "overwrite": true, "skip": true, "fail": true}

func (a *App) apiUploadStart(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct {
		Dir         string `json:"dir"`
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		Mtime       int64  `json:"mtime"` // milliseconds
		SHA256      string `json:"sha256"`
		Conflict    string `json:"conflict"`
		ExpectSize  *int64 `json:"expect_size"`
		ExpectMtime *int64 `json:"expect_mtime"`
	}
	if err := readJSON(r, &in); err != nil || in.Size < 0 {
		httpErr(w, 400, "bad request")
		return
	}
	if in.Conflict == "" {
		in.Conflict = "rename"
	}
	if !conflictModes[in.Conflict] {
		httpErr(w, 400, "conflict must be rename, overwrite, skip or fail")
		return
	}
	in.SHA256 = strings.ToLower(in.SHA256)
	if in.SHA256 != "" {
		if b, err := hex.DecodeString(in.SHA256); err != nil || len(b) != 32 {
			httpErr(w, 400, "sha256 must be 64 hex digits")
			return
		}
	}
	var dabs, dirRel, rel string
	var err error
	if _, _, remote := a.remoteOf(in.Dir); remote {
		// the partial file waits in the state folder; the name is checked again at the end
		var l *remoteLib
		var inner string
		if l, dirRel, inner, err = a.remoteUploadTarget(u, in.Dir, in.Name); err == nil {
			rel = dirRel + "/" + in.Name
			if in.Conflict == "skip" {
				var fi fs.FileInfo
				if l.do(func(c remoteFS) (e error) { fi, e = c.Stat(path.Join(inner, in.Name)); return }) == nil {
					writeJSON(w, map[string]any{"skipped": true, "path": rel, "size": fi.Size(), "mtime": mtimeMs(fi)})
					return
				}
			}
			dabs, err = a.remotePartsDir()
		}
		if err != nil {
			writeErr(w, err)
			return
		}
	} else if dabs, dirRel, rel, err = a.newEntry(u, in.Dir, in.Name); err != nil {
		writeErr(w, err)
		return
	}
	if fi, err := os.Lstat(filepath.Join(dabs, in.Name)); err == nil && !strings.HasSuffix(dabs, "remote-parts") {
		if in.Conflict == "skip" {
			writeJSON(w, map[string]any{"skipped": true, "path": rel, "size": fi.Size(), "mtime": mtimeMs(fi)})
			return
		}
		if fi.IsDir() && in.Conflict == "overwrite" {
			writeErr(w, errExists)
			return
		}
	}
	if err := a.checkSpaceAt(dabs, in.Size); err != nil {
		writeErr(w, err)
		return
	}
	var open int
	_ = a.st.db.QueryRow(`SELECT count(*) FROM uploads WHERE user_id=?`, u.ID).Scan(&open)
	if open >= uploadsOpenMax {
		httpErr(w, 429, "too many unfinished uploads; finish or cancel some first")
		return
	}
	id := randSlug(20)
	f, err := os.OpenFile(filepath.Join(dabs, partName(id)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		writeErr(w, errors.New("could not start the upload here"))
		return
	}
	f.Close()
	es, em := int64(-1), int64(-1)
	if in.ExpectSize != nil && in.ExpectMtime != nil {
		es, em = *in.ExpectSize, *in.ExpectMtime
	}
	st, _ := sha256.New().(encoding.BinaryMarshaler).MarshalBinary()
	if _, err := a.st.exec(`INSERT INTO uploads(id,user_id,dir,name,size,mtime,sha256,conflict,expect_size,expect_mtime,off,state,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,0,?,?,?)`,
		id, u.ID, dirRel, in.Name, in.Size, in.Mtime, in.SHA256, in.Conflict, es, em, st, now(), now()); err != nil {
		_ = os.Remove(filepath.Join(dabs, partName(id)))
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id, "offset": 0, "size": in.Size})
}

func (a *App) apiUploadStatus(w http.ResponseWriter, r *http.Request) {
	up, ok := a.loadUpload(userOf(r), r.PathValue("id"))
	if !ok {
		httpErr(w, 404, "no such upload")
		return
	}
	writeJSON(w, map[string]any{"id": up.ID, "offset": up.Off, "size": up.Size, "dir": up.Dir, "name": up.Name})
}

// openPart opens an upload's partial file without following a symlink put in its place.
func openPart(dabs, id string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dabs, partName(id)), os.O_WRONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errNotAllowed
	}
	return f, nil
}

// apiUploadChunk appends one chunk at offset. A chunk that is cut off leaves the
// upload at the last chunk that arrived whole; the app asks for the offset and
// carries on from there.
func (a *App) apiUploadChunk(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id := r.PathValue("id")
	defer a.uploadLock(id)()
	up, ok := a.loadUpload(u, id)
	if !ok {
		httpErr(w, 404, "no such upload")
		return
	}
	off, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || off != up.Off {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_, _ = fmt.Fprintf(w, `{"error":"wrong offset","offset":%d}`, up.Off)
		return
	}
	dabs, err := a.partDir(u, up.Dir)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.checkSpaceAt(dabs, 0); err != nil {
		writeErr(w, err)
		return
	}
	f, err := openPart(dabs, id)
	if err != nil {
		a.dropUpload(id, up.Dir)
		httpErr(w, 410, "the upload's partial file is gone; start again")
		return
	}
	defer f.Close()
	h := sha256.New()
	if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(up.State); err != nil {
		httpErr(w, 500, "upload state is damaged; start again")
		return
	}
	if err := f.Truncate(off); err != nil {
		httpErr(w, 500, "could not write")
		return
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		httpErr(w, 500, "could not write")
		return
	}
	limit := min(up.Size-off, uploadChunkMax)
	bp := copyBufs.Get().(*[]byte)
	n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(r.Body, limit+1), *bp)
	copyBufs.Put(bp)
	if err == nil && n > limit {
		httpErr(w, 400, "chunk goes past the end of the file, or is over 64 MB")
		return
	}
	if err != nil {
		httpErr(w, 400, "chunk interrupted")
		return
	}
	if err := f.Sync(); err != nil {
		httpErr(w, 500, "could not write")
		return
	}
	st, _ := h.(encoding.BinaryMarshaler).MarshalBinary()
	if _, err := a.st.exec(`UPDATE uploads SET off=?, state=?, updated=? WHERE id=?`, off+n, st, now(), id); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]int64{"offset": off + n, "size": up.Size})
}

// apiUploadFinish checks the file is complete and intact, then puts it in place.
func (a *App) apiUploadFinish(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	id := r.PathValue("id")
	defer a.uploadLock(id)()
	up, ok := a.loadUpload(u, id)
	if !ok {
		httpErr(w, 404, "no such upload")
		return
	}
	var in struct {
		SHA256   string `json:"sha256"`
		Conflict string `json:"conflict"`
	}
	_ = readJSON(r, &in)
	if in.Conflict != "" {
		if !conflictModes[in.Conflict] {
			httpErr(w, 400, "conflict must be rename, overwrite, skip or fail")
			return
		}
		up.Conflict = in.Conflict
	}
	if up.Off != up.Size {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_, _ = fmt.Fprintf(w, `{"error":"the upload isn't complete","offset":%d}`, up.Off)
		return
	}
	h := sha256.New()
	_ = h.(encoding.BinaryUnmarshaler).UnmarshalBinary(up.State)
	sum := hex.EncodeToString(h.Sum(nil))
	for _, want := range []string{up.SHA, strings.ToLower(in.SHA256)} {
		if want != "" && want != sum {
			a.dropUpload(id, up.Dir)
			httpErr(w, 422, "the file arrived damaged (checksum mismatch); send it again")
			return
		}
	}
	if _, _, remote := a.remoteOf(up.Dir); remote {
		a.finishRemoteUpload(w, u, up, sum)
		return
	}
	dabs, dirRel, rel, err := a.newEntry(u, up.Dir, up.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	part := filepath.Join(dabs, partName(id))
	if fi, err := os.Lstat(part); err != nil || !fi.Mode().IsRegular() || fi.Size() != up.Size {
		a.dropUpload(id, up.Dir)
		httpErr(w, 410, "the upload's partial file is gone; start again")
		return
	}
	if up.Mtime > 0 {
		t := time.UnixMilli(up.Mtime)
		_ = os.Chtimes(part, t, t)
	}
	target := filepath.Join(dabs, up.Name)
	name := up.Name
	cur, err := os.Lstat(target)
	exists := err == nil
	if up.ExpectSize >= 0 {
		// replacing a version the app saw: it must still be that version
		if !exists || cur.IsDir() || cur.Size() != up.ExpectSize || mtimeMs(cur) != up.ExpectMtime {
			out := map[string]any{"error": errChanged.Error(), "conflict": true, "exists": exists}
			if exists {
				out["size"], out["mtime"] = cur.Size(), mtimeMs(cur)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			_ = jsonEncode(w, out)
			return
		}
	}
	switch {
	case !exists:
		err = renameNoReplace(part, target)
	case up.Conflict == "skip":
		a.dropUpload(id, up.Dir)
		writeJSON(w, map[string]any{"skipped": true, "path": rel, "size": cur.Size(), "mtime": mtimeMs(cur)})
		return
	case up.Conflict == "fail":
		err = errExists
	case up.Conflict == "overwrite":
		if cur.IsDir() || !cur.Mode().IsRegular() {
			err = errExists
			break
		}
		// keep the old version in the trash, then swap the new one in atomically
		if _, terr := a.toTrashCopy(u, target, rel, cur); terr != nil {
			err = terr
			break
		}
		err = os.Rename(part, target)
	default: // rename: "name (2).ext"
		var dst *os.File
		dst, err = createUnique(dabs, up.Name)
		if err == nil {
			dst.Close()
			name = filepath.Base(dst.Name())
			rel = dirRel + "/" + name
			target = dst.Name()
			if err = os.Rename(part, target); err != nil {
				_ = os.Remove(target)
			}
		}
	}
	if err != nil {
		if errors.Is(err, errExists) {
			out := map[string]any{"error": errExists.Error(), "conflict": true, "exists": true}
			if cur != nil {
				out["size"], out["mtime"] = cur.Size(), mtimeMs(cur)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			_ = jsonEncode(w, out)
			return
		}
		writeErr(w, err)
		return
	}
	_, _ = a.st.exec(`DELETE FROM uploads WHERE id=?`, id)
	a.upLocks.Delete(id)
	fi, err := os.Stat(target)
	if err != nil {
		writeErr(w, err)
		return
	}
	_, _ = a.st.exec(`INSERT OR REPLACE INTO file_hashes(path,size,mtime,sha256) VALUES(?,?,?,?)`, rel, fi.Size(), mtimeMs(fi), sum)
	a.idx.Refresh(dirRel)
	writeJSON(w, map[string]any{"path": rel, "name": name, "size": fi.Size(), "mtime": mtimeMs(fi), "sha256": sum})
}

// toTrashCopy keeps the current version of a file that is about to be replaced:
// a hard link in the trash (the replacement is then one atomic rename), or a copy
// where links aren't supported.
func (a *App) toTrashCopy(u *User, abs, rel string, fi os.FileInfo) (string, error) {
	dir := a.trashDir(abs, rel)
	id := randSlug(12)
	slot := filepath.Join(dir, id)
	if err := os.MkdirAll(slot, 0o770); err != nil {
		return "", errors.New("could not make room in the trash")
	}
	dst := filepath.Join(slot, filepath.Base(abs))
	if err := os.Link(abs, dst); err != nil {
		if err := copyFile(abs, dst); err != nil {
			_ = os.RemoveAll(slot)
			return "", errors.New("could not keep the old version in the trash")
		}
	}
	if _, err := a.st.exec(`INSERT INTO trash(id,user_id,path,stored,is_dir,size,deleted) VALUES(?,?,?,?,0,?,?)`, id, u.ID, rel, dst, fi.Size(), now()); err != nil {
		_ = os.RemoveAll(slot)
		return "", errors.New("could not record the old version")
	}
	return id, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (a *App) apiUploadCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	defer a.uploadLock(id)()
	up, ok := a.loadUpload(userOf(r), id)
	if !ok {
		httpErr(w, 404, "no such upload")
		return
	}
	a.dropUpload(id, up.Dir)
	writeJSON(w, map[string]bool{"ok": true})
}

// checkSpaceAt is checkSpace for the volume a library folder is on.
func (a *App) checkSpaceAt(dir string, incoming int64) error {
	avail, total, err := freeSpace(dir)
	if err != nil {
		return nil
	}
	reserve := min(max(total/100, 1<<30), 50<<30)
	if avail < reserve+uint64(max(incoming, 0)) {
		return errNoSpace
	}
	return nil
}

// ---- sync ----

type syncEntry struct {
	Path  string `json:"p"`
	IsDir bool   `json:"d,omitempty"`
	Size  int64  `json:"s"`
	Mtime int64  `json:"m"` // milliseconds
	Hash  string `json:"h,omitempty"`
}

// apiSyncList lists a folder recursively, straight from disk so it is exact the
// moment it is taken, with what the user may read, relative to the folder.
// The cursor is read first: anything that changes during or after the listing
// shows up as a change after it.
func (a *App) apiSyncList(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	abs, rel, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil || !u.canReach(rel, permRead) {
		httpErr(w, 404, "not found")
		return
	}
	var cursor int64
	_ = a.st.db.QueryRow(`SELECT coalesce(max(id),0) FROM changes`).Scan(&cursor)
	out := []syncEntry{}
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == abs {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == abs {
			return nil
		}
		if a.idx.excluded(d.Name()) || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		sub := filepath.ToSlash(p[len(abs)+1:])
		lib := rel + "/" + sub
		if d.IsDir() {
			if !u.canReach(lib, permRead) {
				return fs.SkipDir
			}
		} else if !u.can(lib, permRead) || !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		e := syncEntry{Path: sub, IsDir: d.IsDir(), Mtime: mtimeMs(fi)}
		if !e.IsDir {
			e.Size = fi.Size()
		}
		out = append(out, e)
		if len(out) > syncListMax {
			return errors.New("this folder has too many files to sync (300,000 at most)")
		}
		return nil
	})
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	hashes := map[string]syncEntry{}
	if rows, err := a.st.db.Query(`SELECT path,size,mtime,sha256 FROM file_hashes WHERE path >= ? AND path < ?`, rel+"/", rel+"0"); err == nil {
		for rows.Next() {
			var e syncEntry
			_ = rows.Scan(&e.Path, &e.Size, &e.Mtime, &e.Hash)
			hashes[e.Path] = e
		}
		rows.Close()
	}
	for i := range out {
		if h, ok := hashes[rel+"/"+out[i].Path]; ok && !out[i].IsDir && h.Size == out[i].Size && h.Mtime == out[i].Mtime {
			out[i].Hash = h.Hash
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	a.idx.mu.Lock()
	excl := append([]string{".burrow-*"}, a.idx.excludes...)
	a.idx.mu.Unlock()
	writeJSON(w, map[string]any{
		"path": rel, "cursor": cursor, "entries": out, "excludes": excl,
		"writable": u.can(rel, permWrite) && mountWritable(abs),
	})
}

// apiSyncChanged tells the app whether anything under a folder changed since a
// cursor, so it only lists the folder again when it must.
func (a *App) apiSyncChanged(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	_, rel, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil || !u.canReach(rel, permRead) {
		httpErr(w, 404, "not found")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	var cursor, oldest, hit int64
	_ = a.st.db.QueryRow(`SELECT coalesce(max(id),0), coalesce(min(id),0) FROM changes`).Scan(&cursor, &oldest)
	_ = a.st.db.QueryRow(`SELECT count(*) FROM (SELECT 1 FROM changes WHERE id > ? AND (path = ? OR (path >= ? AND path < ?)) LIMIT 1)`, since, rel, rel+"/", rel+"0").Scan(&hit)
	// a cursor older than the log's start (pruned) can't vouch for anything
	changed := hit > 0 || since <= 0 || (oldest > 0 && since < oldest-1)
	writeJSON(w, map[string]any{"changed": changed, "cursor": cursor, "watching": a.idx.Status().Watching})
}

var hashSem = make(chan struct{}, 2)

// apiSyncHash returns a file's SHA-256, cached by path, size and modification time.
func (a *App) apiSyncHash(w http.ResponseWriter, r *http.Request) {
	abs, rel, err := a.resolveFor(userOf(r), r.URL.Query().Get("path"), permRead)
	if err != nil {
		httpErr(w, 404, "not found")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		httpErr(w, 404, "not found")
		return
	}
	var sum string
	if a.st.db.QueryRow(`SELECT sha256 FROM file_hashes WHERE path=? AND size=? AND mtime=?`, rel, fi.Size(), mtimeMs(fi)).Scan(&sum) != nil {
		hashSem <- struct{}{}
		sum, err = a.hashFile(abs)
		<-hashSem
		if err != nil {
			httpErr(w, 500, "could not read the file")
			return
		}
		_, _ = a.st.exec(`INSERT OR REPLACE INTO file_hashes(path,size,mtime,sha256) VALUES(?,?,?,?)`, rel, fi.Size(), mtimeMs(fi), sum)
	}
	writeJSON(w, map[string]any{"path": rel, "size": fi.Size(), "mtime": mtimeMs(fi), "sha256": sum})
}

func (a *App) hashFile(abs string) (string, error) {
	f, err := a.openNoFollow(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	bp := copyBufs.Get().(*[]byte)
	_, err = io.CopyBuffer(h, f, *bp)
	copyBufs.Put(bp)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
