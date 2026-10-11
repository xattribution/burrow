package main

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

var errNotAllowed = errors.New("path not available")

// cleanRel normalizes a library-relative path ("a/b/c", no leading slash).
func cleanRel(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	c := path.Clean("/" + p)
	return strings.TrimPrefix(c, "/")
}

// resolve maps a library path to an absolute path, refusing anything outside an
// enabled root, inside an excluded folder, or reached through a symlink.
func (a *App) resolve(rel string) (string, string, error) {
	rel = cleanRel(rel)
	if !a.idx.allowed(rel) || !a.exactName(rel) {
		return "", "", errNotAllowed
	}
	abs := filepath.Join(a.cfg.DataDir, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", err
	}
	base, _ := filepath.EvalSymlinks(a.cfg.DataDir)
	if real != abs && real != filepath.Join(base, filepath.FromSlash(rel)) {
		return "", "", errNotAllowed
	}
	return abs, rel, nil
}

// openNoFollow opens a file that resolve (or an upload folder lookup) already
// approved, so that nothing can be swapped in between the check and the open: no
// component may be a symlink and the path can't leave the library or drop folder.
// It uses openat2 (Linux 5.6+); where that is unavailable it at least refuses a
// symlink as the last component.
func (a *App) openNoFollow(abs string) (*os.File, error) {
	under := func(dir string) bool { return dir != "" && strings.HasPrefix(abs, dir+string(os.PathSeparator)) }
	base := a.cfg.DataDir
	// the drop folder may sit inside the library; the deeper base is the tighter one
	if under(a.cfg.DropDir) && (!under(base) || len(a.cfg.DropDir) > len(base)) {
		base = a.cfg.DropDir
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return nil, errNotAllowed
	}
	dir, err := unix.Open(base, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat2(dir, rel, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
		return os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), abs), nil
}

// exactName checks that every segment of rel is spelled exactly as it is on disk.
// On a case-insensitive or Unicode-normalizing dataset "DOCS/hr" opens "DOCS/HR",
// which would slip past a No-access grant or hidden name spelled "DOCS/HR", so a
// path must match byte for byte. The index (built from directory listings, so its
// spellings are exact) answers most lookups; whatever isn't indexed yet is checked
// against the directory listing itself.
func (a *App) exactName(rel string) bool {
	segs := strings.Split(rel, "/")
	known := 1 // allowed() already matched the library name exactly
	for n := len(segs); n > 1; n-- {
		var one int
		if a.st.db.QueryRow(`SELECT 1 FROM files WHERE path=?`, strings.Join(segs[:n], "/")).Scan(&one) == nil {
			known = n
			break
		}
	}
	dir := filepath.Join(a.cfg.DataDir, filepath.FromSlash(strings.Join(segs[:known], "/")))
	for _, seg := range segs[known:] {
		if !dirHasExact(dir, seg) {
			return false
		}
		dir = filepath.Join(dir, seg)
	}
	return true
}

func dirHasExact(dir, name string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(1024)
		for _, n := range names {
			if n == name {
				return true
			}
		}
		if err != nil {
			return false
		}
	}
}

// checkQuery bounds a search so one request can't make the database scan the
// whole index dozens of times over.
func checkQuery(q string) error {
	if len(q) > 256 {
		return errors.New("search is too long (256 characters at most)")
	}
	if len(strings.Fields(q)) > 12 {
		return errors.New("search has too many words (12 at most)")
	}
	return nil
}

type Entry struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	IsDir  bool   `json:"dir"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Kind   string `json:"kind,omitempty"`
	Ext    string `json:"ext,omitempty"`
	Shared int    `json:"shared,omitempty"` // number of active share links on this path
	Fav    bool   `json:"fav,omitempty"`
}

// listDir reads a directory straight from disk so browsing is always current.
// Entries the user can't read, or walk through to something they can, are left out.
func (a *App) listDir(u *User, rel string) ([]Entry, error) {
	if cleanRel(rel) == "" {
		return a.listRoots(u), nil
	}
	abs, rel, err := a.resolve(rel)
	if err != nil {
		return nil, err
	}
	if !u.canReach(rel, permRead) {
		return nil, errNotAllowed
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, d := range des {
		if a.idx.excluded(d.Name()) || d.Type()&os.ModeSymlink != 0 {
			continue
		}
		p := rel + "/" + d.Name()
		if !u.canReach(p, permRead) {
			continue
		}
		info, err := d.Info()
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			continue
		}
		out = append(out, entryFromInfo(p, info))
	}
	sortEntries(out)
	a.decorate(u, out)
	a.idx.Touch(rel)
	return out, nil
}

func (a *App) listRoots(u *User) []Entry {
	out := []Entry{}
	for _, r := range a.idx.enabledRoots() {
		if !u.canReach(r, permRead) {
			continue
		}
		info, err := os.Stat(filepath.Join(a.cfg.DataDir, r))
		if err != nil {
			continue
		}
		out = append(out, entryFromInfo(r, info))
	}
	sortEntries(out)
	a.decorate(u, out)
	return out
}

func entryFromInfo(rel string, info os.FileInfo) Entry {
	e := Entry{Path: rel, Name: info.Name(), IsDir: info.IsDir(), Mtime: info.ModTime().Unix()}
	if e.IsDir {
		e.Kind = "folder"
	} else {
		e.Size = info.Size()
		e.Ext = extOf(e.Name)
		e.Kind = extKind[e.Ext]
	}
	return e
}

func sortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].IsDir != es[j].IsDir {
			return es[i].IsDir
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

// decorate marks the user's favorites and paths that have live share links
// (all links for admins, the user's own links for everyone else).
func (a *App) decorate(u *User, es []Entry) {
	if len(es) == 0 {
		return
	}
	own, args := "", []any{now(), now()}
	if !u.Admin {
		own = " AND s.owner_id=?"
		args = []any{now(), u.ID, now(), u.ID}
	}
	shared := map[string]int{}
	rows, err := a.st.db.Query(`SELECT p, count(*) FROM (
		SELECT s.path AS p FROM shares s WHERE s.mode='link' AND s.revoked=0 AND (s.expires=0 OR s.expires>?)`+own+`
		UNION ALL
		SELECT i.path FROM share_items i JOIN shares s ON s.id=i.share_id WHERE s.revoked=0 AND (s.expires=0 OR s.expires>?)`+own+`
	) GROUP BY p`, args...)
	if err == nil {
		for rows.Next() {
			var p string
			var n int
			_ = rows.Scan(&p, &n)
			shared[p] = n
		}
		rows.Close()
	}
	favs := a.favoriteSet(u)
	for i := range es {
		es[i].Shared = shared[es[i].Path]
		es[i].Fav = favs[es[i].Path]
	}
}

func (a *App) favoriteSet(u *User) map[string]bool {
	m := map[string]bool{}
	rows, err := a.st.db.Query(`SELECT path FROM favs WHERE user_id=?`, u.ID)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		m[p] = true
	}
	return m
}

// ---- search ----

type SearchQuery struct {
	Q        string `json:"q"`
	Kind     string `json:"kind"`  // image, video, audio, doc, archive, code, 3d, folder
	Root     string `json:"root"`  // limit to a folder path (prefix)
	After    int64  `json:"after"` // modified after (unix)
	MinSize  int64  `json:"min_size"`
	MaxSize  int64  `json:"max_size"`
	Sort     string `json:"sort"` // relevance | newest | oldest | largest | name
	Limit    int    `json:"limit"`
	DirsOnly bool   `json:"dirs_only"`
}

type SearchResult struct {
	Entries []Entry `json:"entries"`
	Total   int     `json:"total"`
	TookMS  float64 `json:"took_ms"`
}

// search matches every word of the query as a substring of the full path.
// Words of 3+ characters go through the trigram index; shorter ones use LIKE on the
// rows the index already narrowed down (or the whole table if nothing else narrows).
// Results are limited to what u may read, in SQL (so limits and totals are exact) and
// again in Go as a second check.
func (a *App) search(u *User, sq SearchQuery) (SearchResult, error) {
	start := time.Now()
	if err := checkQuery(sq.Q); err != nil {
		return SearchResult{}, err
	}
	a.searchSem <- struct{}{} // a few searches at a time; the rest wait their turn
	defer func() { <-a.searchSem }()
	if sq.Limit <= 0 || sq.Limit > 1000 {
		sq.Limit = 200
	}
	var where []string
	var args []any
	var ftsTerms []string
	nameBoost := ""
	for _, w := range strings.Fields(strings.ToLower(sq.Q)) {
		neg := strings.HasPrefix(w, "-") && len(w) > 1
		if neg {
			w = w[1:]
		}
		// ext:pdf / type:video shortcuts typed into the box
		if v, ok := strings.CutPrefix(w, "ext:"); ok && !neg {
			where = append(where, "f.ext=?")
			args = append(args, strings.TrimPrefix(v, "."))
			continue
		}
		if v, ok := strings.CutPrefix(w, "type:"); ok && !neg {
			sq.Kind = v
			continue
		}
		if neg {
			where = append(where, "lower(f.path) NOT LIKE ? ESCAPE '\\'")
			args = append(args, "%"+likeEscape(w)+"%")
			continue
		}
		if len([]rune(w)) >= 3 {
			ftsTerms = append(ftsTerms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
		} else {
			where = append(where, "lower(f.path) LIKE ? ESCAPE '\\'")
			args = append(args, "%"+likeEscape(w)+"%")
		}
		if nameBoost == "" {
			nameBoost = w
		}
	}
	from := "files f"
	if len(ftsTerms) > 0 {
		from = "files_fts JOIN files f ON f.id = files_fts.rowid"
		where = append([]string{"files_fts MATCH ?"}, where...)
		args = append([]any{strings.Join(ftsTerms, " AND ")}, args...)
	}
	switch {
	case sq.DirsOnly || sq.Kind == "folder":
		where = append(where, "f.is_dir=1")
	case sq.Kind == "other":
		where = append(where, "f.is_dir=0 AND f.kind=''")
	case sq.Kind != "":
		where = append(where, "f.kind=?")
		args = append(args, sq.Kind)
	}
	if r := cleanRel(sq.Root); r != "" {
		where = append(where, "(f.path >= ? AND f.path < ?)")
		args = append(args, r+"/", r+"0")
	}
	if sq.After > 0 {
		where = append(where, "f.mtime >= ?")
		args = append(args, sq.After)
	}
	if sq.MinSize > 0 {
		where = append(where, "f.size >= ?")
		args = append(args, sq.MinSize)
	}
	if sq.MaxSize > 0 {
		where = append(where, "f.size <= ?")
		args = append(args, sq.MaxSize)
	}
	// only enabled roots
	roots := a.idx.enabledRoots()
	if len(roots) == 0 {
		return SearchResult{Entries: []Entry{}}, nil
	}
	var rc []string
	for _, r := range roots {
		rc = append(rc, "(f.path = ? OR (f.path >= ? AND f.path < ?))")
		args = append(args, r, r+"/", r+"0")
	}
	where = append(where, "("+strings.Join(rc, " OR ")+")")
	if !u.Admin {
		cond, cargs := u.scope("f.path", permRead)
		where = append(where, cond)
		args = append(args, cargs...)
	}
	order := "f.mtime DESC"
	switch sq.Sort {
	case "oldest":
		order = "f.mtime ASC"
	case "largest":
		order = "f.size DESC"
	case "name":
		order = "lower(f.name) ASC"
	case "newest":
	default:
		if nameBoost != "" {
			order = "(instr(lower(f.name), ?) > 0) DESC, (lower(f.name) = ?) DESC, length(f.path) ASC, f.mtime DESC"
		}
	}
	q := "SELECT f.path,f.name,f.is_dir,f.size,f.mtime,f.kind,f.ext FROM " + from + " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order + " LIMIT ?"
	qargs := append([]any{}, args...)
	if strings.HasPrefix(order, "(instr") {
		qargs = append(qargs, nameBoost, nameBoost)
	}
	qargs = append(qargs, sq.Limit)
	rows, err := a.st.db.Query(q, qargs...)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close()
	res := SearchResult{Entries: []Entry{}}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Path, &e.Name, &e.IsDir, &e.Size, &e.Mtime, &e.Kind, &e.Ext); err != nil {
			return res, err
		}
		if e.IsDir {
			e.Kind = "folder"
		}
		if !u.can(e.Path, permRead) {
			continue
		}
		res.Entries = append(res.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	a.decorate(u, res.Entries)
	res.Total = len(res.Entries)
	if res.Total >= sq.Limit {
		var n int
		cq := "SELECT count(*) FROM (SELECT 1 FROM " + from + " WHERE " + strings.Join(where, " AND ") + " LIMIT 10000)"
		if err := a.st.db.QueryRow(cq, args...).Scan(&n); err == nil {
			res.Total = n
		}
	}
	res.TookMS = float64(time.Since(start).Microseconds()) / 1000
	return res, nil
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ---- change log ----

type Change struct {
	ID    int64  `json:"id"`
	TS    int64  `json:"ts"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	IsDir bool   `json:"dir"`
	Size  int64  `json:"size"`
}

// changes lists the change log, newest first, limited to paths u may read.
func (a *App) changes(u *User, since int64, root, kind string, limit int) ([]Change, error) {
	if limit <= 0 || limit > 2000 {
		limit = 300
	}
	cond, args := u.scope("path", permRead)
	q := `SELECT id,ts,kind,path,is_dir,size FROM changes WHERE ts >= ? AND ` + cond
	args = append([]any{since}, args...)
	if r := cleanRel(root); r != "" {
		q += ` AND (path >= ? AND path < ?)`
		args = append(args, r+"/", r+"0")
	}
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := a.st.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.ID, &c.TS, &c.Kind, &c.Path, &c.IsDir, &c.Size); err != nil {
			return out, err
		}
		if !a.idx.allowed(c.Path) { // since hidden, or in a library turned off
			continue
		}
		out = append(out, c)
	}
	return out, nil
}
