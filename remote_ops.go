package main

// What the file API does when a path is on a connected NAS (see remote.go). Each
// handler checks remoteOf first and comes here; local paths never do.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// remoteRoots are the connected NAS the user can reach, as libraries.
func (a *App) remoteRoots(u *User) []Entry {
	out := []Entry{}
	for _, n := range a.remoteNames() {
		if !u.canReach(n, permRead) {
			continue
		}
		l, _, _ := a.remoteOf(n)
		out = append(out, Entry{Path: n, Name: n, IsDir: true, Kind: "folder", Remote: l.cfg.Kind})
	}
	return out
}

func (a *App) remoteList(u *User, l *remoteLib, rel, inner string) ([]Entry, error) {
	if !a.remotePathOK(inner) || !u.canReach(rel, permRead) {
		return nil, errNotAllowed
	}
	var list []fs.FileInfo
	err := l.do(func(c remoteFS) (err error) { list, err = c.ReadDir(inner); return })
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(list))
	for _, fi := range list {
		if a.validName(fi.Name()) != nil { // hidden, the trash, or a name that isn't plain
			continue
		}
		p := rel + "/" + fi.Name()
		if !u.canReach(p, permRead) {
			continue
		}
		out = append(out, entryFromInfo(p, fi))
	}
	sortEntries(out)
	a.decorate(u, out)
	return out, nil
}

// remoteStat finds something the user may at least walk through.
func (a *App) remoteStat(u *User, p string) (*remoteLib, string, string, fs.FileInfo, error) {
	l, inner, _ := a.remoteOf(p)
	rel := cleanRel(p)
	if !a.remotePathOK(inner) || !u.canReach(rel, permRead) {
		return nil, "", "", nil, errNotAllowed
	}
	var fi fs.FileInfo
	err := l.do(func(c remoteFS) (err error) { fi, err = c.Stat(inner); return })
	if err != nil {
		return nil, "", "", nil, err
	}
	if inner == "" {
		fi = renamedInfo{fi, l.Name}
	}
	return l, rel, inner, fi, nil
}

// renamedInfo is the top of a connection, shown under the connection's name.
type renamedInfo struct {
	fs.FileInfo
	name string
}

func (r renamedInfo) Name() string { return r.name }
func (r renamedInfo) IsDir() bool  { return true }

func (a *App) remoteInfo(w http.ResponseWriter, u *User, p string) {
	l, rel, inner, fi, err := a.remoteStat(u, p)
	if err != nil {
		writeErr(w, err)
		return
	}
	e := entryFromInfo(rel, fi)
	if inner == "" {
		e.Remote = l.cfg.Kind
	}
	es := []Entry{e}
	a.decorate(u, es)
	canRead := u.can(rel, permRead)
	out := map[string]any{"entry": es[0], "shares": []Share{}, "can_read": canRead, "can_share": false,
		"can_change": inner != "" && u.can(rel, permWrite) && (!e.IsDir || u.canWriteTree(rel)), "remote": l.cfg.Kind}
	if canRead {
		out["preview"] = previewKind(e)
	}
	writeJSON(w, out)
}

func (a *App) remoteRaw(w http.ResponseWriter, r *http.Request, u *User, p string, attachment bool) {
	l, rel, inner, _, err := a.remoteStat(u, p)
	if err != nil || !u.can(rel, permRead) {
		httpErr(w, 404, "not found")
		return
	}
	var f io.ReadSeekCloser
	var fi fs.FileInfo
	if err := l.do(func(c remoteFS) (err error) { f, fi, err = c.Open(inner); return }); err != nil {
		httpErr(w, 404, "not found")
		return
	}
	defer f.Close()
	ctype := safeContentType(w, fi.Name())
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private")
	disp := "inline"
	if attachment {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": fi.Name()}))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (a *App) remoteThumb(w http.ResponseWriter, r *http.Request, u *User, p string) {
	l, rel, inner, fi, err := a.remoteStat(u, p)
	if err != nil || !u.can(rel, permRead) || fi.IsDir() || !thumbExts[extOf(fi.Name())] || fi.Size() > 50<<20 {
		http.NotFound(w, r)
		return
	}
	key := sha(fmt.Sprintf("remote|%d|%s|%d|%d", l.ID, inner, fi.Size(), fi.ModTime().UnixNano()))
	cache := filepath.Join(a.cfg.StateDir, "thumbs", key[:2], key+".jpg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", `"`+key[:32]+`"`)
	if serveCachedThumb(w, r, cache) {
		return
	}
	a.thumbSem <- struct{}{}
	defer func() { <-a.thumbSem }()
	var buf bytes.Buffer
	err = l.do(func(c remoteFS) error {
		buf.Reset()
		f, _, err := c.Open(inner)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(&buf, io.LimitReader(f, 50<<20))
		return err
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := a.thumbFrom(bytes.NewReader(buf.Bytes()), fi.Name())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.saveThumb(cache, data)
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

func (a *App) remoteMkdir(u *User, p string, parents bool) (Entry, error) {
	l, inner, _ := a.remoteOf(p)
	rel := cleanRel(p)
	if inner == "" || !a.remotePathOK(inner) {
		return Entry{}, errBadName
	}
	segs := strings.Split(inner, "/")
	for i := range segs {
		sub := strings.Join(segs[:i+1], "/")
		subRel := l.Name + "/" + sub
		last := i == len(segs)-1
		if !last && !parents {
			continue
		}
		if !u.canReach(path.Dir(subRel), permRead) {
			return Entry{}, errNotAllowed
		}
		if !u.can(subRel, permWrite) {
			return Entry{}, errNoWrite
		}
		err := l.do(func(c remoteFS) error { return c.Mkdir(sub) })
		if err != nil && (last && !parents || !errors.Is(err, fs.ErrExist)) {
			if errors.Is(err, fs.ErrExist) {
				return Entry{}, errExists
			}
			return Entry{}, err
		}
	}
	_, _, _, fi, err := a.remoteStat(u, rel)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(rel, fi), nil
}

// remoteWritable is existingWritable for a connected NAS.
func (a *App) remoteWritable(u *User, p string) (*remoteLib, string, string, fs.FileInfo, error) {
	l, rel, inner, fi, err := a.remoteStat(u, p)
	if err != nil {
		return nil, "", "", nil, errNotAllowed
	}
	if inner == "" {
		return nil, "", "", nil, errLibRoot
	}
	if !u.can(rel, permWrite) {
		return nil, "", "", nil, errNoWrite
	}
	if fi.IsDir() && !u.canWriteTree(rel) {
		return nil, "", "", nil, errHasLocked
	}
	return l, rel, inner, fi, nil
}

func (a *App) remoteRename(u *User, l *remoteLib, fromP, toP string) (string, error) {
	_, fromRel, from, _, err := a.remoteWritable(u, fromP)
	if err != nil {
		return "", err
	}
	_, to, _ := a.remoteOf(toP)
	toRel := cleanRel(toP)
	if toRel == fromRel {
		return toRel, nil
	}
	if strings.HasPrefix(toRel+"/", fromRel+"/") {
		return "", errors.New("a folder can't be moved into itself")
	}
	dir, name := path.Split(to)
	if to == "" || a.validName(name) != nil || !a.remotePathOK(to) {
		return "", errBadName
	}
	if !u.canReach(path.Dir(toRel), permRead) {
		return "", errNotAllowed
	}
	if !u.can(toRel, permWrite) {
		return "", errNoWrite
	}
	_ = dir
	if err := l.do(func(c remoteFS) error { return c.Rename(from, to) }); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", errExists
		}
		return "", err
	}
	a.shareMu.Lock() // favorites and grants follow the move
	defer a.shareMu.Unlock()
	if err := a.movePaths(fromRel, toRel); err != nil {
		return "", err
	}
	return toRel, nil
}

const remoteStored = "remote:" // trash rows for a connected NAS: remote:<id>:<path in it>

func (a *App) remoteTrash(u *User, p string, size, mtime *int64) (string, string, error) {
	l, rel, inner, fi, err := a.remoteWritable(u, p)
	if err != nil {
		return "", cleanRel(p), err
	}
	if size != nil && mtime != nil && !fi.IsDir() && (fi.Size() != *size || mtimeMs(fi) != *mtime) {
		return "", rel, errChanged
	}
	id := randSlug(12)
	var stored string
	if err := l.do(func(c remoteFS) (err error) { stored, err = c.Trash(inner, id); return }); err != nil {
		return "", rel, err
	}
	if stored == "" { // another Burrow keeps it in its own trash
		return "", rel, nil
	}
	sz := fi.Size()
	if fi.IsDir() {
		sz = 0
	}
	if _, err := a.st.exec(`INSERT INTO trash(id,user_id,path,stored,is_dir,size,deleted) VALUES(?,?,?,?,?,?,?)`,
		id, u.ID, rel, fmt.Sprintf("%s%d:%s", remoteStored, l.ID, stored), fi.IsDir(), sz, now()); err != nil {
		return "", rel, errors.New("could not record the deletion")
	}
	return id, rel, nil
}

// remoteTrashSlot reads a trash row that points at a connected NAS.
func (a *App) remoteTrashSlot(stored string) (*remoteLib, string, bool) {
	rest, ok := strings.CutPrefix(stored, remoteStored)
	if !ok {
		return nil, "", false
	}
	ids, p, ok := strings.Cut(rest, ":")
	id, err := strconv.ParseInt(ids, 10, 64)
	if !ok || err != nil || !strings.HasPrefix(p, trashName+"/") || strings.Contains(p, "..") {
		return nil, "", true
	}
	for _, n := range a.remoteNames() {
		if l, _, _ := a.remoteOf(n); l != nil && l.ID == id {
			return l, p, true
		}
	}
	return nil, "", true // the connection was removed
}

func (a *App) remoteRestore(w http.ResponseWriter, u *User, t TrashItem) {
	l, stored, _ := a.remoteTrashSlot(t.stored)
	if l == nil {
		httpErr(w, 404, "the connected NAS it was on is gone")
		return
	}
	_, dirInner, _ := a.remoteOf(path.Dir(t.Path))
	dirRel := path.Dir(t.Path)
	if !u.canReach(dirRel, permRead) || !u.can(t.Path, permWrite) {
		writeErr(w, errNoWrite)
		return
	}
	name := path.Base(t.Path)
	ext := filepath.Ext(name)
	if t.IsDir {
		ext = ""
	}
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 100; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (restored %d)%s", base, i, ext)
		}
		err := l.do(func(c remoteFS) error { return c.Rename(stored, path.Join(dirInner, cand)) })
		if err == nil {
			_ = l.do(func(c remoteFS) error { return c.RemoveAll(path.Dir(stored)) })
			_, _ = a.st.exec(`DELETE FROM trash WHERE id=?`, t.ID)
			writeJSON(w, map[string]string{"path": dirRel + "/" + cand})
			return
		}
		if !errors.Is(err, fs.ErrExist) {
			writeErr(w, errors.New("could not restore it"))
			return
		}
	}
	writeErr(w, errors.New("could not restore it"))
}

// remotePurge deletes a trash slot on a connected NAS (or just the row if it's gone).
func (a *App) remotePurge(stored string) error {
	l, p, _ := a.remoteTrashSlot(stored)
	if l == nil || p == "" {
		return nil
	}
	return l.do(func(c remoteFS) error {
		err := c.RemoveAll(path.Dir(p))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	})
}

// ---------- uploads ----------

func (a *App) remotePartsDir() (string, error) {
	d := filepath.Join(a.cfg.StateDir, "remote-parts")
	return d, os.MkdirAll(d, 0o750)
}

// partDir is where an upload into dir keeps its partial file: next to where it
// will land on this NAS, or in the state folder for a connected one.
func (a *App) partDir(u *User, dir string) (string, error) {
	if _, inner, ok := a.remoteOf(dir); ok {
		rel := cleanRel(dir)
		if !a.remotePathOK(inner) || !u.canReach(rel, permRead) {
			return "", errNotAllowed
		}
		if !u.can(rel, permWrite) {
			return "", errNoWrite
		}
		return a.remotePartsDir()
	}
	abs, _, err := a.writableDir(u, dir)
	return abs, err
}

// remoteUploadTarget checks an upload's destination folder and name.
func (a *App) remoteUploadTarget(u *User, dir, name string) (*remoteLib, string, string, error) {
	l, rel, inner, fi, err := a.remoteStat(u, dir)
	if err != nil {
		return nil, "", "", errNotAllowed
	}
	if !fi.IsDir() {
		return nil, "", "", errNotAllowed
	}
	if err := a.validName(name); err != nil {
		return nil, "", "", err
	}
	if !u.can(rel+"/"+name, permWrite) {
		return nil, "", "", errNoWrite
	}
	return l, rel, inner, nil
}

func (a *App) finishRemoteUpload(w http.ResponseWriter, u *User, up *upload, sum string) {
	l, dirRel, dirInner, err := a.remoteUploadTarget(u, up.Dir, up.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	pd, _ := a.remotePartsDir()
	f, err := os.Open(filepath.Join(pd, partName(up.ID)))
	if err != nil {
		a.dropUpload(up.ID, up.Dir)
		httpErr(w, 410, "the upload's partial file is gone; start again")
		return
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() != up.Size {
		a.dropUpload(up.ID, up.Dir)
		httpErr(w, 410, "the upload's partial file is gone; start again")
		return
	}
	mtime := time.Time{}
	if up.Mtime > 0 {
		mtime = time.UnixMilli(up.Mtime)
	}
	put := func(name string) error {
		return l.do(func(c remoteFS) error {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			return c.Put(path.Join(dirInner, name), f, up.Size, mtime)
		})
	}
	name := up.Name
	err = put(name)
	if errors.Is(err, fs.ErrExist) {
		switch up.Conflict {
		case "skip":
			a.dropUpload(up.ID, up.Dir)
			writeJSON(w, map[string]any{"skipped": true, "path": dirRel + "/" + name})
			return
		case "fail":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			_ = jsonEncode(w, map[string]any{"error": errExists.Error(), "conflict": true, "exists": true})
			return
		case "overwrite":
			if _, _, terr := a.remoteTrash(u, dirRel+"/"+name, nil, nil); terr != nil {
				writeErr(w, terr)
				return
			}
			err = put(name)
		default:
			ext := filepath.Ext(up.Name)
			base := strings.TrimSuffix(up.Name, ext)
			for i := 2; i < 1000 && errors.Is(err, fs.ErrExist); i++ {
				name = fmt.Sprintf("%s (%d)%s", base, i, ext)
				err = put(name)
			}
		}
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	a.dropUpload(up.ID, up.Dir)
	writeJSON(w, map[string]any{"path": dirRel + "/" + name, "name": name, "size": up.Size, "mtime": up.Mtime, "sha256": sum})
}
