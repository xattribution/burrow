package main

// Shared folders ("collections"): one link whose contents you keep changing.
// A collection is a list of library paths (files or whole folders, referenced live,
// never copied) plus, for admins, an upload folder under /drop for files that aren't
// on the NAS yet. That folder is only created when the first file is uploaded.
// Recipients see one virtual folder; the link, password and history stay the same
// while you add and remove things. Revoke or let it expire when done.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type collSource struct {
	Name     string // display name at the collection root
	Abs      string
	LibPath  string // library path for referenced items; "" for uploads
	IsDir    bool
	Info     os.FileInfo
	Uploaded bool
}

func (a *App) uploadDir(s Share) string {
	return filepath.Join(a.cfg.DropDir, filepath.FromSlash(cleanRel(s.Path)))
}

// partPrefix marks an upload still in progress. Such files are hidden everywhere and
// renamed to their real name only once complete.
const partPrefix = ".burrow-part-"

// folderClaimed reports whether a drop or shared folder already uses /drop/<name>.
func (a *App) folderClaimed(name, mode string) bool {
	var n int
	_ = a.st.db.QueryRow(`SELECT count(*) FROM shares WHERE path=? AND mode=?`, name, mode).Scan(&n)
	return n > 0
}

// newUploadFolder picks an unused folder name under /drop for a shared folder's own
// files without creating it. Names on disk are skipped too, so leftovers from a deleted
// link are never shown to a new one. Callers hold shareMu.
func (a *App) newUploadFolder(label string) (string, error) {
	base := strings.Trim(strings.TrimSpace(unsafeName.ReplaceAllString(label, "-")), ". -")
	if base == "" {
		base = "shared-" + time.Now().Format("2006-01-02")
	}
	base = "Shared - " + base
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s %d", base, i)
		}
		if a.folderClaimed(name, "collection") || a.folderClaimed(name, "drop") {
			continue
		}
		if _, err := os.Lstat(filepath.Join(a.cfg.DropDir, name)); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return name, nil
	}
	return "", errors.New("could not pick an upload folder name")
}

// dropFolderName turns a drop's label into its folder under /drop. Drops with the same
// label share a folder on purpose; a shared folder's upload folder is never reused.
func (a *App) dropFolderName(label string) (string, error) {
	base := strings.Trim(strings.TrimSpace(unsafeName.ReplaceAllString(label, "-")), ". -")
	if base == "" {
		base = "drop-" + time.Now().Format("2006-01-02")
	}
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s %d", base, i)
		}
		if !a.folderClaimed(name, "collection") {
			return name, nil
		}
	}
	return "", errors.New("could not pick a drop folder name")
}

// shareableItem checks that u may put p in a shared folder and returns its library path.
func (a *App) shareableItem(u *User, p string) (string, error) {
	_, rel, err := a.resolve(p)
	if err == nil && !u.can(rel, permShare) {
		err = errNotAllowed
	}
	if err != nil {
		return "", fmt.Errorf("can't add %q: %w", p, err)
	}
	return rel, nil
}

// addItems adds library paths to a shared folder. Each one must be shareable by the
// link's owner (an admin editing someone else's link can't add more than they could).
func (a *App) addItems(owner *User, id string, paths []string) error {
	var rels []string
	for _, p := range paths {
		rel, err := a.shareableItem(owner, p)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
	}
	for _, rel := range rels {
		if _, err := a.st.exec(`INSERT OR IGNORE INTO share_items(share_id,path,added) VALUES(?,?,?)`, id, rel, now()); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) itemPaths(id string) []string {
	rows, err := a.st.db.Query(`SELECT path FROM share_items WHERE share_id=? ORDER BY added, path`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		out = append(out, p)
	}
	return out
}

// sources lists what sits at the collection root, with unique display names.
// Items whose library root was disabled, that no longer exist, or that the owner
// may no longer share are skipped.
func (a *App) sources(s Share, owner *User) []collSource {
	var out []collSource
	seen := map[string]int{}
	add := func(c collSource) {
		n := c.Name
		if k := seen[strings.ToLower(n)]; k > 0 {
			ext := filepath.Ext(n)
			if c.IsDir {
				ext = ""
			}
			n = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(n, ext), k+1, ext)
		}
		seen[strings.ToLower(c.Name)]++
		c.Name = n
		out = append(out, c)
	}
	for _, p := range a.itemPaths(s.ID) {
		abs, rel, err := a.resolve(p)
		if err != nil || !owner.can(rel, permShare) {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		add(collSource{Name: info.Name(), Abs: abs, LibPath: rel, IsDir: info.IsDir(), Info: info})
	}
	dir := a.uploadDir(s)
	des, _ := os.ReadDir(dir) // not there until the first upload
	for _, d := range des {
		if a.idx.excluded(d.Name()) || d.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := d.Info()
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			continue
		}
		add(collSource{Name: d.Name(), Abs: filepath.Join(dir, d.Name()), IsDir: info.IsDir(), Info: info, Uploaded: true})
	}
	return out
}

// collResolve maps "<root name>/<rest>" inside a collection to an absolute path and
// its library path ("" inside the upload folder).
func (a *App) collResolve(s Share, owner *User, sub string) (string, string, error) {
	sub = cleanRel(sub)
	first, rest, _ := strings.Cut(sub, "/")
	for _, c := range a.sources(s, owner) {
		if c.Name != first {
			continue
		}
		if rest == "" {
			return c.Abs, c.LibPath, nil
		}
		if !c.IsDir {
			return "", "", errNotAllowed
		}
		if c.LibPath != "" {
			abs, rel, err := a.resolve(c.LibPath + "/" + rest)
			if err == nil && !owner.can(rel, permShare) {
				err = errNotAllowed
			}
			return abs, rel, err
		}
		for _, seg := range strings.Split(rest, "/") {
			if a.idx.excluded(seg) {
				return "", "", errNotAllowed
			}
		}
		abs := filepath.Join(c.Abs, filepath.FromSlash(rest))
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return "", "", err
		}
		base, _ := filepath.EvalSymlinks(c.Abs)
		if real != filepath.Join(base, filepath.FromSlash(rest)) {
			return "", "", errNotAllowed
		}
		return abs, "", nil
	}
	return "", "", errNotAllowed
}

func (a *App) handleCollection(w http.ResponseWriter, r *http.Request, s Share, owner *User, action string) {
	// recipient uploads write outside every folder grant, so they need an admin owner
	s.AllowUp = s.AllowUp && owner != nil && owner.Admin
	if action == "upload" {
		if !s.AllowUp {
			http.NotFound(w, r)
			return
		}
		a.handleDrop(w, r, s, "upload")
		return
	}
	allow := zipAllow(owner, permShare)
	sub := cleanRel(r.URL.Query().Get("p"))
	if sub == "" {
		switch action {
		case "zip":
			if !a.countDownload(s) {
				a.renderShare(w, http.StatusGone, sharePage{S: s, State: "used-up", Title: s.Label})
				return
			}
			a.logShare(r, s.ID, "zip", "")
			var srcs []zipSource
			for _, c := range a.sources(s, owner) {
				srcs = append(srcs, zipSource{Abs: c.Abs, Name: c.Name, Lib: c.LibPath})
			}
			a.streamZipMany(w, s.Label, srcs, allow)
		case "":
			pg := sharePage{S: s, Title: s.Label, State: "active", Uploaded: atoiDef(r.URL.Query().Get("uploaded"), 0)}
			for _, c := range a.sources(s, owner) {
				e := entryFromInfo(c.Name, c.Info)
				e.Name = c.Name
				pg.Entries = append(pg.Entries, e)
			}
			sortEntries(pg.Entries)
			a.logShare(r, s.ID, "view", "")
			a.renderShare(w, 200, pg)
		default:
			http.NotFound(w, r)
		}
		return
	}
	abs, lib, err := a.collResolve(s, owner, sub)
	if err != nil {
		a.renderShare(w, http.StatusNotFound, sharePage{S: s, Title: s.Label, State: "missing"})
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		a.renderShare(w, http.StatusNotFound, sharePage{S: s, Title: s.Label, State: "missing"})
		return
	}
	switch action {
	case "dl", "raw":
		if info.IsDir() {
			httpErr(w, 400, "use zip for folders")
			return
		}
		if !a.gateDownload(w, r, s, sub, action == "dl") {
			return
		}
		a.serveFile(w, r, abs, info, action == "dl")
		return
	case "thumb":
		a.serveThumb(w, r, abs, info)
		return
	case "zip":
		if !info.IsDir() {
			httpErr(w, 400, "not a folder")
			return
		}
		if !a.countDownload(s) {
			a.renderShare(w, http.StatusGone, sharePage{S: s, State: "used-up", Title: s.Label})
			return
		}
		a.logShare(r, s.ID, "zip", sub)
		a.streamZipMany(w, info.Name(), []zipSource{{Abs: abs, Name: info.Name(), Lib: lib}}, allow)
		return
	case "":
	default:
		http.NotFound(w, r)
		return
	}
	pg := sharePage{S: s, Title: s.Label, Sub: sub, State: "active"}
	pg.Crumbs = []crumb{{Name: s.Label}}
	acc := ""
	for _, seg := range strings.Split(sub, "/") {
		acc = strings.TrimPrefix(acc+"/"+seg, "/")
		pg.Crumbs = append(pg.Crumbs, crumb{Name: seg, Sub: acc})
	}
	if info.IsDir() {
		des, _ := os.ReadDir(abs)
		for _, d := range des {
			if a.idx.excluded(d.Name()) || d.Type()&os.ModeSymlink != 0 {
				continue
			}
			if lib != "" && !owner.canReach(lib+"/"+d.Name(), permShare) {
				continue
			}
			fi, err := d.Info()
			if err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
				continue
			}
			pg.Entries = append(pg.Entries, entryFromInfo(sub+"/"+d.Name(), fi))
		}
		sortEntries(pg.Entries)
	} else {
		e := entryFromInfo(sub, info)
		pg.File = &e
		pg.Preview = previewKind(e)
	}
	a.renderShare(w, 200, pg)
}

// ---- admin side ----

type CollectionView struct {
	Items   []CollItem `json:"items"`
	Uploads []Entry    `json:"uploads"`
	Folder  string     `json:"upload_folder"`
}

type CollItem struct {
	Entry
	Missing bool `json:"missing,omitempty"`
}

func (a *App) collectionView(s Share) CollectionView {
	v := CollectionView{Items: []CollItem{}, Uploads: []Entry{}, Folder: "drop/" + s.Path}
	owner := a.shareOwner(s)
	for _, p := range a.itemPaths(s.ID) {
		abs, rel, err := a.resolve(p)
		if err == nil && !owner.can(rel, permShare) {
			err = errNotAllowed // the owner lost access: recipients no longer see it
		}
		var info os.FileInfo
		if err == nil {
			info, err = os.Stat(abs)
		}
		if err != nil {
			v.Items = append(v.Items, CollItem{Entry: Entry{Path: p, Name: filepath.Base(p)}, Missing: true})
			continue
		}
		v.Items = append(v.Items, CollItem{Entry: entryFromInfo(rel, info)})
	}
	des, _ := os.ReadDir(a.uploadDir(s))
	for _, d := range des {
		info, err := d.Info()
		if err != nil || d.Type()&os.ModeSymlink != 0 || a.idx.excluded(d.Name()) {
			continue
		}
		v.Uploads = append(v.Uploads, entryFromInfo(d.Name(), info))
	}
	sortEntries(v.Uploads)
	return v
}

// collectionOr404 loads a shared folder the signed-in user may manage.
func (a *App) collectionOr404(w http.ResponseWriter, r *http.Request) (Share, bool) {
	s, err := a.ownedShare(userOf(r), r.PathValue("id"))
	if err != nil || s.Mode != "collection" {
		httpErr(w, 404, "shared folder not found")
		return s, false
	}
	return s, true
}

func (a *App) apiCollection(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collectionOr404(w, r)
	if !ok {
		return
	}
	writeJSON(w, a.collectionView(s))
}

func (a *App) apiAddItems(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collectionOr404(w, r)
	if !ok {
		return
	}
	var in struct{ Paths []string }
	if err := readJSON(r, &in); err != nil || len(in.Paths) == 0 {
		httpErr(w, 400, "paths required")
		return
	}
	owner := a.shareOwner(s)
	if owner == nil {
		httpErr(w, 400, "this link's owner is no longer active")
		return
	}
	if err := a.addItems(owner, s.ID, in.Paths); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, a.collectionView(s))
}

func (a *App) apiRemoveItem(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collectionOr404(w, r)
	if !ok {
		return
	}
	_, _ = a.st.exec(`DELETE FROM share_items WHERE share_id=? AND path=?`, s.ID, cleanRel(r.URL.Query().Get("path")))
	writeJSON(w, a.collectionView(s))
}

// apiOwnerUpload lets an admin put new files straight into a shared folder.
func (a *App) apiOwnerUpload(w http.ResponseWriter, r *http.Request) {
	if !userOf(r).Admin {
		httpErr(w, 403, errAdminsOnly.Error())
		return
	}
	s, ok := a.collectionOr404(w, r)
	if !ok {
		return
	}
	n, err := a.receiveUploads(w, r, s, a.uploadDir(s), false)
	if err != nil {
		httpErr(w, uploadErrCode(err), err.Error())
		return
	}
	writeJSON(w, map[string]any{"saved": n, "view": a.collectionView(s)})
}

func (a *App) apiDeleteUpload(w http.ResponseWriter, r *http.Request) {
	s, ok := a.collectionOr404(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		httpErr(w, 400, "bad name")
		return
	}
	p := filepath.Join(a.uploadDir(s), name)
	if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		httpErr(w, 404, "not found")
		return
	}
	if err := os.RemoveAll(p); err != nil {
		httpErr(w, 500, "could not delete")
		return
	}
	writeJSON(w, a.collectionView(s))
}

// receiveUploads streams multipart files into dir, never overwriting. The folder is
// created on the first upload. Each file is written once, straight to its final folder,
// under a hidden temporary name, flushed to disk, then renamed into place, so a half-sent
// file is never visible and an interrupted one leaves nothing behind.
func (a *App) receiveUploads(w http.ResponseWriter, r *http.Request, s Share, dir string, logIt bool) (int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUploadBytes()+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		return 0, errors.New("expected a multipart upload")
	}
	if err := a.checkSpace(r.ContentLength); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return 0, errors.New("upload folder unavailable")
	}
	n := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, errors.New("upload interrupted")
		}
		if part.FileName() == "" {
			part.Close()
			continue
		}
		if err := a.checkSpace(0); err != nil {
			part.Close()
			return n, err
		}
		name := safeFileName(part.FileName())
		saved, written, err := saveUpload(dir, name, part)
		part.Close()
		if err != nil {
			return n, err
		}
		n++
		if logIt {
			a.logShare(r, s.ID, "upload", fmt.Sprintf("%s (%s)", saved, humanSize(written)))
		}
	}
	return n, nil
}

// saveUpload writes src to dir/name (or "name (2)" and so on if taken) and returns the
// name used.
func saveUpload(dir, name string, src io.Reader) (string, int64, error) {
	tmp, err := os.CreateTemp(dir, partPrefix+"*")
	if err != nil {
		return "", 0, errors.New("could not save " + name)
	}
	bp := copyBufs.Get().(*[]byte)
	written, err := io.CopyBuffer(tmp, src, *bp)
	copyBufs.Put(bp)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", 0, errors.New("upload interrupted")
	}
	_ = os.Chmod(tmp.Name(), 0o664) // CreateTemp makes 0600
	// claim a free name, then move the finished file over the empty placeholder
	dst, err := createUnique(dir, name)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", 0, errors.New("could not save " + name)
	}
	dst.Close()
	if err := os.Rename(tmp.Name(), dst.Name()); err != nil {
		_ = os.Remove(tmp.Name())
		_ = os.Remove(dst.Name())
		return "", 0, errors.New("could not save " + name)
	}
	return filepath.Base(dst.Name()), written, nil
}

var errNoSpace = errors.New("the server is almost out of space; try again later")

func uploadErrCode(err error) int {
	if errors.Is(err, errNoSpace) {
		return http.StatusInsufficientStorage
	}
	return http.StatusBadRequest
}

// freeSpace reports bytes available to Burrow and the volume size where uploads land.
var freeSpace = func(dir string) (avail, total uint64, err error) {
	var st unix.Statfs_t
	for p := dir; ; p = filepath.Dir(p) {
		if err = unix.Statfs(p, &st); err == nil {
			return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
		}
		if filepath.Dir(p) == p {
			return 0, 0, err
		}
	}
}

// checkSpace refuses an upload that would leave less than a reserve free (1% of the
// volume, at least 1 GB and at most 50 GB), so a leaked drop link can't fill the disk.
func (a *App) checkSpace(incoming int64) error {
	avail, total, err := freeSpace(a.cfg.DropDir)
	if err != nil {
		return nil // can't tell; the size limit per file still applies
	}
	reserve := min(max(total/100, 1<<30), 50<<30)
	need := reserve
	if incoming > 0 {
		need += uint64(incoming)
	}
	if avail < need {
		return errNoSpace
	}
	return nil
}

// cleanPartials removes upload leftovers older than a day (from a crash mid-upload).
// Upload folders sit directly under /drop, so this only looks one level deep.
func (a *App) cleanPartials() {
	cut := time.Now().Add(-24 * time.Hour)
	dirs, _ := os.ReadDir(a.cfg.DropDir)
	for _, d := range dirs {
		if !d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(a.cfg.DropDir, d.Name()))
		for _, f := range files {
			if !strings.HasPrefix(f.Name(), partPrefix) || !f.Type().IsRegular() {
				continue
			}
			if info, err := f.Info(); err == nil && info.ModTime().Before(cut) {
				_ = os.Remove(filepath.Join(a.cfg.DropDir, d.Name(), f.Name()))
			}
		}
	}
}

// purgeUploads deletes a drop/collection upload folder unless another link uses it.
func (a *App) purgeUploads(s Share) error {
	if s.Mode != "drop" && s.Mode != "collection" {
		return nil
	}
	var others int
	_ = a.st.db.QueryRow(`SELECT count(*) FROM shares WHERE path=? AND mode IN ('drop','collection') AND id<>?`, s.Path, s.ID).Scan(&others)
	if others > 0 {
		return nil
	}
	dir := a.uploadDir(s)
	if dir == a.cfg.DropDir || !strings.HasPrefix(dir, a.cfg.DropDir+string(os.PathSeparator)) {
		return errNotAllowed
	}
	return os.RemoveAll(dir)
}
