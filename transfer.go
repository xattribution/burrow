package main

// Copies between libraries. A move inside one library (or one connected NAS) is a
// rename. A move between two of them, or between two mounts of this NAS, is a
// copy followed by a delete into the trash, run in the background as a job the
// web UI and the phone follow. Nothing is replaced on the way: something already
// at the destination stops the move before it starts.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type job struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Move      bool   `json:"move"`
	State     string `json:"state"` // counting, copying, cleaning, done, failed
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Files     int    `json:"files"`
	FilesDone int    `json:"files_done"`
	Skipped   int    `json:"skipped,omitempty"` // files the user may not read, left behind
	Error     string `json:"error,omitempty"`
	Started   int64  `json:"started"`
	Ended     int64  `json:"ended,omitempty"`
	userID    int64
}

type jobSet struct {
	mu  sync.Mutex
	m   map[string]*job
	sem chan struct{}
}

func (js *jobSet) add(j *job) {
	js.mu.Lock()
	defer js.mu.Unlock()
	if js.m == nil {
		js.m = map[string]*job{}
		js.sem = make(chan struct{}, 3)
	}
	// finished jobs are kept an hour so a page that reloads can still show them
	for id, o := range js.m {
		if o.Ended > 0 && o.Ended < now()-3600 {
			delete(js.m, id)
		}
	}
	js.m[j.ID] = j
}

func (js *jobSet) update(j *job, fn func(*job)) {
	js.mu.Lock()
	fn(j)
	js.mu.Unlock()
}

func (js *jobSet) snapshot(u *User, id string) []job {
	js.mu.Lock()
	defer js.mu.Unlock()
	out := []job{}
	for _, j := range js.m {
		if (j.userID == u.ID || u.Admin) && (id == "" || j.ID == id) {
			out = append(out, *j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Started > out[k].Started })
	return out
}

func (a *App) apiJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.jobs.snapshot(userOf(r), ""))
}

func (a *App) apiJob(w http.ResponseWriter, r *http.Request) {
	js := a.jobs.snapshot(userOf(r), r.PathValue("id"))
	if len(js) == 0 {
		httpErr(w, 404, "no such job")
		return
	}
	writeJSON(w, js[0])
}

// ---------- either kind of library ----------

// side is a library seen by the copy: this NAS or a connected one. Paths are
// library paths; each call checks what it needs about the path itself.
type side interface {
	stat(rel string) (fs.FileInfo, error)
	list(rel string) ([]fs.FileInfo, error)
	open(rel string) (io.ReadCloser, error)
	put(rel string, r io.Reader, size int64, mtime time.Time) error
	mkdir(rel string) error
}

func (a *App) sideOf(rel string) side {
	if l, _, ok := a.remoteOf(rel); ok {
		return remoteSide{a, l}
	}
	return localSide{a}
}

type localSide struct{ a *App }

func (s localSide) stat(rel string) (fs.FileInfo, error) {
	abs, _, err := s.a.resolve(rel)
	if err != nil {
		return nil, err
	}
	return os.Lstat(abs)
}

func (s localSide) list(rel string) ([]fs.FileInfo, error) {
	abs, _, err := s.a.resolve(rel)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := []fs.FileInfo{}
	for _, d := range des {
		if d.Type()&os.ModeSymlink != 0 {
			continue
		}
		if fi, err := d.Info(); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			out = append(out, fi)
		}
	}
	return out, nil
}

func (s localSide) open(rel string) (io.ReadCloser, error) {
	abs, _, err := s.a.resolve(rel)
	if err != nil {
		return nil, err
	}
	return s.a.openNoFollow(abs)
}

// put writes under a hidden name, flushes it and renames it in, never over anything.
func (s localSide) put(rel string, r io.Reader, size int64, mtime time.Time) error {
	dir, name := path.Split(rel)
	dabs, _, err := s.a.resolve(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return err
	}
	if err := s.a.checkSpaceAt(dabs, size); err != nil {
		return err
	}
	tmp := filepath.Join(dabs, partName("cp-"+randSlug(12)))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return errors.New("could not write into " + strings.TrimSuffix(dir, "/"))
	}
	bp := copyBufs.Get().(*[]byte)
	n, err := io.CopyBuffer(f, r, *bp)
	copyBufs.Put(bp)
	if err == nil && n != size {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		_ = os.Chtimes(tmp, mtime, mtime)
		err = renameNoReplace(tmp, filepath.Join(dabs, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func (s localSide) mkdir(rel string) error {
	dir, name := path.Split(rel)
	dabs, _, err := s.a.resolve(strings.TrimSuffix(dir, "/"))
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(dabs, name), 0o775)
}

type remoteSide struct {
	a *App
	l *remoteLib
}

func (s remoteSide) inner(rel string) (string, error) {
	_, inner, _ := s.a.remoteOf(rel)
	if !s.a.remotePathOK(inner) {
		return "", errNotAllowed
	}
	return inner, nil
}

func (s remoteSide) stat(rel string) (fi fs.FileInfo, err error) {
	in, err := s.inner(rel)
	if err != nil {
		return nil, err
	}
	err = s.l.do(func(c remoteFS) error { fi, err = c.Stat(in); return err })
	return fi, err
}

func (s remoteSide) list(rel string) (out []fs.FileInfo, err error) {
	in, err := s.inner(rel)
	if err != nil {
		return nil, err
	}
	err = s.l.do(func(c remoteFS) error { out, err = c.ReadDir(in); return err })
	return out, err
}

func (s remoteSide) open(rel string) (rc io.ReadCloser, err error) {
	in, err := s.inner(rel)
	if err != nil {
		return nil, err
	}
	err = s.l.do(func(c remoteFS) error { rc, _, err = c.Open(in); return err })
	return rc, err
}

func (s remoteSide) put(rel string, r io.Reader, size int64, mtime time.Time) error {
	in, err := s.inner(rel)
	if err != nil {
		return err
	}
	// no retry here: half the stream may already be gone
	s.l.mu.Lock()
	c := s.l.conn
	s.l.mu.Unlock()
	if c == nil {
		return s.l.do(func(c remoteFS) error { return c.Put(in, r, size, mtime) })
	}
	return c.Put(in, r, size, mtime)
}

func (s remoteSide) mkdir(rel string) error {
	in, err := s.inner(rel)
	if err != nil {
		return err
	}
	return s.l.do(func(c remoteFS) error { return c.Mkdir(in) })
}

// ---------- moves ----------

// moveJob moves fromP to toP. It returns the new path, and a job id when the
// move runs in the background (between libraries or mounts).
func (a *App) moveJob(u *User, fromP, toP string) (string, string, error) {
	fl, _, fromRemote := a.remoteOf(fromP)
	tl, _, toRemote := a.remoteOf(toP)
	if fromRemote && toRemote && fl == tl {
		to, err := a.remoteRename(u, fl, fromP, toP)
		return to, "", err
	}
	if !fromRemote && !toRemote {
		to, err := a.moveLocal(u, fromP, toP)
		if !errors.Is(err, errCrossMount) {
			return to, "", err
		}
	}
	return a.startCopy(u, fromP, toP, true)
}

// startCopy checks everything that can be checked up front, then copies (and for
// a move, trashes the original) in the background.
func (a *App) startCopy(u *User, fromP, toP string, move bool) (string, string, error) {
	from, to := cleanRel(fromP), cleanRel(toP)
	if !strings.Contains(from, "/") {
		return "", "", errLibRoot
	}
	if to == from || strings.HasPrefix(to+"/", from+"/") {
		return "", "", errors.New("a folder can't be moved into itself")
	}
	src, dst := a.sideOf(from), a.sideOf(to)
	if !u.canReach(from, permRead) {
		return "", "", errNotAllowed
	}
	fi, err := src.stat(from)
	if err != nil {
		return "", "", errNotAllowed
	}
	if move {
		if !u.can(from, permWrite) {
			return "", "", errNoWrite
		}
		if fi.IsDir() && !u.canWriteTree(from) {
			return "", "", errHasLocked
		}
		if _, ok := src.(localSide); ok {
			if _, _, _, err := a.existingWritable(u, from); err != nil {
				return "", "", err
			}
		}
	} else if !fi.IsDir() && !u.can(from, permRead) {
		return "", "", errNotAllowed
	}
	dir, name := path.Split(to)
	dir = strings.TrimSuffix(dir, "/")
	if err := a.validName(name); err != nil {
		return "", "", err
	}
	if !u.canReach(dir, permRead) {
		return "", "", errNotAllowed
	}
	if !u.can(to, permWrite) {
		return "", "", errNoWrite
	}
	if _, ok := dst.(localSide); ok {
		if _, _, err := a.writableDir(u, dir); err != nil {
			return "", "", err
		}
	} else if dfi, err := dst.stat(dir); err != nil || !dfi.IsDir() {
		return "", "", errNotAllowed
	}
	if _, err := dst.stat(to); err == nil {
		return "", "", errExists
	}
	j := &job{ID: randSlug(12), From: from, To: to, Move: move, State: "counting", Started: now(), userID: u.ID}
	a.jobs.add(j)
	go a.runCopy(u, j, src, dst, fi)
	return to, j.ID, nil
}

func (a *App) runCopy(u *User, j *job, src, dst side, top fs.FileInfo) {
	a.jobs.sem <- struct{}{}
	defer func() { <-a.jobs.sem }()
	fail := func(err error) {
		a.jobs.update(j, func(j *job) { j.State, j.Error, j.Ended = "failed", err.Error(), now() })
	}
	// count first, so progress means something
	var total int64
	var files int
	var count func(rel string, fi fs.FileInfo)
	count = func(rel string, fi fs.FileInfo) {
		if !fi.IsDir() {
			if u.can(rel, permRead) {
				total += fi.Size()
				files++
			}
			return
		}
		kids, _ := src.list(rel)
		for _, k := range kids {
			if a.validName(k.Name()) == nil && u.canReach(rel+"/"+k.Name(), permRead) {
				count(rel+"/"+k.Name(), k)
			}
		}
	}
	count(j.From, top)
	a.jobs.update(j, func(j *job) { j.State, j.Total, j.Files = "copying", total, files })

	var copyTree func(from, to string, fi fs.FileInfo) error
	copyTree = func(from, to string, fi fs.FileInfo) error {
		if fi.IsDir() {
			if err := dst.mkdir(to); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("couldn't make the folder %s: %w", to, err)
			}
			kids, err := src.list(from)
			if err != nil {
				return fmt.Errorf("couldn't read %s: %w", from, err)
			}
			for _, k := range kids {
				kf, kt := from+"/"+k.Name(), to+"/"+k.Name()
				if a.validName(k.Name()) != nil || !u.canReach(kf, permRead) {
					continue
				}
				if !u.can(kt, permWrite) {
					return fmt.Errorf("%w: %s", errNoWrite, kt)
				}
				if err := copyTree(kf, kt, k); err != nil {
					return err
				}
			}
			return nil
		}
		if !u.can(from, permRead) {
			a.jobs.update(j, func(j *job) { j.Skipped++ })
			return nil
		}
		rc, err := src.open(from)
		if err != nil {
			return fmt.Errorf("couldn't open %s: %w", from, err)
		}
		defer rc.Close()
		err = dst.put(to, &progressReader{r: rc, add: func(n int) { a.jobs.update(j, func(j *job) { j.Done += int64(n) }) }}, fi.Size(), fi.ModTime())
		if err != nil {
			return fmt.Errorf("couldn't copy %s: %w", path.Base(from), err)
		}
		a.jobs.update(j, func(j *job) { j.FilesDone++ })
		return nil
	}
	err := copyTree(j.From, j.To, top)
	a.refreshAfterCopy(j.To)
	if err != nil {
		fail(err)
		return
	}
	if j.Move {
		skipped := 0
		a.jobs.update(j, func(j *job) { j.State = "cleaning"; skipped = j.Skipped })
		if skipped > 0 {
			// something stayed behind, so the original can't go
			fail(fmt.Errorf("copied, but %d file(s) you can't read were left where they were", skipped))
			return
		}
		if _, _, err := a.trash(u, j.From, nil, nil); err != nil {
			fail(fmt.Errorf("copied, but the original couldn't be deleted: %w", err))
			return
		}
		a.refreshAfterCopy(j.From)
	}
	a.jobs.update(j, func(j *job) { j.State, j.Ended = "done", now() })
}

func (a *App) refreshAfterCopy(rel string) {
	if _, _, ok := a.remoteOf(rel); !ok {
		a.idx.Refresh(path.Dir(rel), rel)
	}
}

type progressReader struct {
	r   io.Reader
	add func(int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.add(n)
	}
	return n, err
}
