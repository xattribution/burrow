package main

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

var kinds = map[string][]string{
	"image":   {"jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "svg", "avif", "raw", "cr2", "cr3", "nef", "arw", "dng", "psd"},
	"video":   {"mp4", "mkv", "mov", "avi", "webm", "m4v", "wmv", "flv", "mpg", "mpeg", "ts", "m2ts", "3gp"},
	"audio":   {"mp3", "flac", "wav", "aac", "m4a", "ogg", "opus", "wma", "aiff", "alac"},
	"doc":     {"pdf", "doc", "docx", "odt", "rtf", "txt", "md", "xls", "xlsx", "ods", "csv", "ppt", "pptx", "odp", "pages", "numbers", "key", "epub", "mobi"},
	"archive": {"zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "iso", "dmg", "img"},
	"code":    {"go", "py", "js", "mjs", "ts", "tsx", "jsx", "html", "css", "json", "yaml", "yml", "xml", "sh", "ps1", "c", "h", "cpp", "hpp", "rs", "java", "kt", "toml", "ini", "conf", "sql", "lua", "cs", "ino"},
	"3d":      {"stl", "3mf", "obj", "step", "stp", "f3d", "gcode", "bgcode", "scad", "blend", "fbx", "glb", "gltf", "ply"},
}

var extKind = func() map[string]string {
	m := map[string]string{}
	for k, exts := range kinds {
		for _, e := range exts {
			m[e] = k
		}
	}
	return m
}()

func extOf(name string) string {
	e := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if len(e) > 10 {
		return ""
	}
	return e
}

var defaultExcludes = []string{".zfs", ".hist", "@eaDir", "#recycle", "#snapshot", ".Trash-*", "$RECYCLE.BIN", "System Volume Information", ".DS_Store", "._*", "Thumbs.db", "desktop.ini", ".ix-apps", "lost+found"}

type IndexStatus struct {
	Scanning     bool   `json:"scanning"`
	Phase        string `json:"phase"`
	ScannedDirs  int64  `json:"scanned_dirs"`
	LastStart    int64  `json:"last_start"`
	LastEnd      int64  `json:"last_end"`
	LastDuration int64  `json:"last_duration_ms"`
	Files        int64  `json:"files"`
	Dirs         int64  `json:"dirs"`
	Bytes        int64  `json:"bytes"`
	Watching     bool   `json:"watching"`
	Watches      int    `json:"watches"`
	WatchNote    string `json:"watch_note,omitempty"`
	Error        string `json:"error,omitempty"`
}

type Indexer struct {
	cfg Config
	st  *Store

	mu       sync.Mutex
	status   IndexStatus
	excludes []string
	roots    map[string]bool

	kick    chan struct{}
	dirtyMu sync.Mutex
	dirty   map[string]bool

	w       *fsnotify.Watcher
	watched map[string]bool
	wFull   bool

	refreshMu sync.Mutex // one shallow rescan at a time
}

func newIndexer(cfg Config, st *Store) *Indexer {
	ix := &Indexer{cfg: cfg, st: st, kick: make(chan struct{}, 1), dirty: map[string]bool{}, watched: map[string]bool{}}
	ix.reloadSettings()
	return ix
}

// reloadSettings reads enabled roots and exclude patterns.
func (ix *Indexer) reloadSettings() {
	var roots []string
	ix.st.getJSON("roots", &roots)
	excl := defaultExcludes
	if v := ix.st.get("excludes"); v != "" {
		excl = nil
		for _, l := range strings.Split(v, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				excl = append(excl, l)
			}
		}
	}
	ix.mu.Lock()
	ix.roots = map[string]bool{}
	for _, r := range roots {
		ix.roots[r] = true
	}
	ix.excludes = excl
	ix.mu.Unlock()
}

func (ix *Indexer) excluded(name string) bool {
	// case-insensitive, so a hidden name can't be reached by changing its case on
	// a case-insensitive dataset
	name = strings.ToLower(name)
	if strings.HasPrefix(name, ".burrow-") { // uploads in progress, the trash
		return true
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, p := range ix.excludes {
		p = strings.ToLower(p)
		if ok, _ := path.Match(p, name); ok || p == name {
			return true
		}
	}
	return false
}

// rootEnabled reports whether rel (a library-relative path) is inside an enabled root
// and contains no excluded segment.
func (ix *Indexer) allowed(rel string) bool {
	if rel == "" {
		return false
	}
	segs := strings.Split(rel, "/")
	ix.mu.Lock()
	ok := ix.roots[segs[0]]
	ix.mu.Unlock()
	if !ok {
		return false
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || ix.excluded(s) {
			return false
		}
	}
	return true
}

func (ix *Indexer) enabledRoots() []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var r []string
	for k := range ix.roots {
		r = append(r, k)
	}
	return r
}

func (ix *Indexer) Status() IndexStatus {
	ix.mu.Lock()
	s := ix.status
	s.Watches = len(ix.watched)
	ix.mu.Unlock()
	return s
}

// Rescan asks for a full rescan as soon as the current one (if any) ends.
func (ix *Indexer) Rescan() {
	select {
	case ix.kick <- struct{}{}:
	default:
	}
}

// Refresh rescans folders Burrow itself just changed, right away, so browsing,
// search and the change log show the change at once. During a full scan (or
// before the first) they are queued instead; the scan reconciles them anyway.
func (ix *Indexer) Refresh(rels ...string) {
	ix.mu.Lock()
	busy := ix.status.Scanning
	ix.mu.Unlock()
	if busy || ix.st.get("initial_scan_done") != "1" {
		for _, r := range rels {
			ix.Touch(r)
		}
		return
	}
	ix.refreshMu.Lock()
	defer ix.refreshMu.Unlock()
	for _, r := range rels {
		_ = ix.scanDir(r, false, true)
	}
	ix.refreshCounts()
}

// Touch marks a directory for a quick shallow rescan (used when someone browses it).
func (ix *Indexer) Touch(rel string) {
	ix.dirtyMu.Lock()
	ix.dirty[rel] = true
	ix.dirtyMu.Unlock()
}

func (ix *Indexer) run(ctx context.Context) {
	if ix.cfg.Watch {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			ix.setWatchNote("live updates unavailable: " + err.Error())
		} else {
			ix.w = w
			ix.mu.Lock()
			ix.status.Watching = true
			ix.mu.Unlock()
			go ix.watchLoop(ctx)
		}
	}
	go ix.dirtyLoop(ctx)
	ix.fullScan()
	t := time.NewTicker(ix.cfg.ScanInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if ix.w != nil {
				ix.w.Close()
			}
			return
		case <-t.C:
			ix.fullScan()
		case <-ix.kick:
			ix.fullScan()
		}
	}
}

func (ix *Indexer) setWatchNote(n string) {
	ix.mu.Lock()
	ix.status.WatchNote = n
	ix.mu.Unlock()
}

func (ix *Indexer) fullScan() {
	ix.reloadSettings()
	start := time.Now()
	ix.mu.Lock()
	ix.status.Scanning = true
	ix.status.Phase = "scanning"
	ix.status.ScannedDirs = 0
	ix.status.LastStart = start.Unix()
	ix.status.Error = ""
	ix.mu.Unlock()

	var count int64
	_ = ix.st.db.QueryRow(`SELECT count(*) FROM files`).Scan(&count)
	logChanges := count > 0 && ix.st.get("initial_scan_done") == "1"

	// Drop rows for roots that were turned off or deleted.
	rows, _ := ix.st.db.Query(`SELECT path FROM files WHERE parent=''`)
	var topRows []string
	for rows != nil && rows.Next() {
		var p string
		_ = rows.Scan(&p)
		topRows = append(topRows, p)
	}
	if rows != nil {
		rows.Close()
	}
	enabled := ix.enabledRoots()
	on := map[string]bool{}
	for _, r := range enabled {
		on[r] = true
	}
	for _, p := range topRows {
		if !on[p] {
			ix.deleteTree(p)
		}
	}

	var scanErr error
	for _, r := range enabled {
		abs := filepath.Join(ix.cfg.DataDir, filepath.FromSlash(r))
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			ix.deleteTree(r)
			continue
		}
		b := ix.newBatch(logChanges)
		b.upsert(r, "", r, info, true)
		b.flush()
		if err := ix.scanDir(r, true, logChanges); err != nil {
			scanErr = err
		}
	}
	if scanErr == nil {
		ix.st.set("initial_scan_done", "1")
	}
	ix.pruneChanges()
	ix.refreshCounts()

	ix.mu.Lock()
	ix.status.Scanning = false
	ix.status.Phase = ""
	ix.status.LastEnd = time.Now().Unix()
	ix.status.LastDuration = time.Since(start).Milliseconds()
	if scanErr != nil {
		ix.status.Error = scanErr.Error()
	}
	ix.mu.Unlock()
	log.Printf("index: full scan finished in %s", time.Since(start).Round(time.Millisecond))
}

func (ix *Indexer) refreshCounts() {
	var files, dirs, bytes int64
	_ = ix.st.db.QueryRow(`SELECT coalesce(sum(is_dir=0),0), coalesce(sum(is_dir=1),0), coalesce(sum(size),0) FROM files`).Scan(&files, &dirs, &bytes)
	ix.mu.Lock()
	ix.status.Files, ix.status.Dirs, ix.status.Bytes = files, dirs, bytes
	ix.mu.Unlock()
}

func (ix *Indexer) pruneChanges() {
	_, _ = ix.st.exec(`DELETE FROM changes WHERE ts < ?`, now()-180*86400)
	_, _ = ix.st.exec(`DELETE FROM changes WHERE id <= (SELECT id FROM changes ORDER BY id DESC LIMIT 1 OFFSET 500000)`)
	_, _ = ix.st.exec(`DELETE FROM share_events WHERE ts < ?`, now()-365*86400)
}

type existingRow struct {
	size, mtime int64
	isDir       bool
}

// scanDir reconciles one directory with the database. With deep=true it walks every
// subdirectory; with deep=false it only descends into directories it has not seen before.
func (ix *Indexer) scanDir(rel string, deep, logChanges bool) error {
	if !ix.allowed(rel) {
		return nil
	}
	abs := filepath.Join(ix.cfg.DataDir, filepath.FromSlash(rel))
	entries, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ix.deleteTree(rel)
			return nil
		}
		return nil // permission errors etc.: keep what we have
	}
	ix.addWatch(abs)

	existing := map[string]existingRow{}
	rows, err := ix.st.db.Query(`SELECT name,size,mtime,is_dir FROM files WHERE parent=?`, rel)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		var r existingRow
		_ = rows.Scan(&n, &r.size, &r.mtime, &r.isDir)
		existing[n] = r
	}
	rows.Close()

	b := ix.newBatch(logChanges)
	var subdirs []string
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if ix.excluded(name) || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		seen[name] = true
		child := rel + "/" + name
		old, had := existing[name]
		if info.IsDir() {
			if !had || !old.isDir {
				if had {
					b.remove(child, old.isDir, old.size)
				}
				b.upsert(child, rel, name, info, !had)
				subdirs = append(subdirs, child)
			} else if deep {
				if old.mtime != info.ModTime().Unix() {
					b.upsertQuiet(child, rel, name, info)
				}
				subdirs = append(subdirs, child)
			}
			continue
		}
		if !had {
			b.upsert(child, rel, name, info, true)
		} else if old.isDir {
			b.remove(child, true, 0)
			b.upsert(child, rel, name, info, true)
		} else if old.size != info.Size() || old.mtime != info.ModTime().Unix() {
			b.upsert(child, rel, name, info, false)
		}
	}
	for n, r := range existing {
		if !seen[n] {
			b.remove(rel+"/"+n, r.isDir, r.size)
		}
	}
	b.flush()

	ix.mu.Lock()
	ix.status.ScannedDirs++
	ix.mu.Unlock()
	for _, d := range subdirs {
		if err := ix.scanDir(d, deep, logChanges); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Indexer) deleteTree(rel string) {
	_, _ = ix.st.exec(`DELETE FROM files WHERE path=? OR (path >= ? AND path < ?)`, rel, rel+"/", rel+"0")
}

// batch groups database writes so a scan takes the write lock briefly and often.
type batch struct {
	ix         *Indexer
	logChanges bool
	ops        []func(tx *sql.Tx)
}

func (ix *Indexer) newBatch(logChanges bool) *batch { return &batch{ix: ix, logChanges: logChanges} }

func (b *batch) upsert(p, parent, name string, info fs.FileInfo, isNew bool) {
	size := info.Size()
	isDir := info.IsDir()
	if isDir {
		size = 0
	}
	mt := info.ModTime().Unix()
	ext := ""
	kind := "folder"
	if !isDir {
		ext = extOf(name)
		kind = extKind[ext]
	}
	b.ops = append(b.ops, func(tx *sql.Tx) {
		_, _ = tx.Exec(`INSERT INTO files(path,name,parent,ext,kind,is_dir,size,mtime) VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime, is_dir=excluded.is_dir, ext=excluded.ext, kind=excluded.kind`,
			p, name, parent, ext, kind, isDir, size, mt)
		if b.logChanges && parent != "" {
			k := "modified"
			if isNew {
				k = "added"
			}
			_, _ = tx.Exec(`INSERT INTO changes(ts,kind,path,is_dir,size) VALUES(?,?,?,?,?)`, now(), k, p, isDir, size)
		}
	})
	b.maybeFlush()
}

// upsertQuiet updates a directory's mtime without logging a change.
func (b *batch) upsertQuiet(p, parent, name string, info fs.FileInfo) {
	mt := info.ModTime().Unix()
	b.ops = append(b.ops, func(tx *sql.Tx) {
		_, _ = tx.Exec(`UPDATE files SET mtime=? WHERE path=?`, mt, p)
	})
	b.maybeFlush()
}

func (b *batch) remove(p string, isDir bool, size int64) {
	b.ops = append(b.ops, func(tx *sql.Tx) {
		_, _ = tx.Exec(`DELETE FROM files WHERE path=? OR (path >= ? AND path < ?)`, p, p+"/", p+"0")
		if b.logChanges {
			_, _ = tx.Exec(`INSERT INTO changes(ts,kind,path,is_dir,size) VALUES(?,?,?,?,?)`, now(), "deleted", p, isDir, size)
		}
	})
	b.maybeFlush()
}

func (b *batch) maybeFlush() {
	if len(b.ops) >= 2000 {
		b.flush()
	}
}

func (b *batch) flush() {
	if len(b.ops) == 0 {
		return
	}
	st := b.ix.st
	st.wmu.Lock()
	defer st.wmu.Unlock()
	tx, err := st.db.Begin()
	if err != nil {
		log.Printf("index: begin: %v", err)
		return
	}
	for _, op := range b.ops {
		op(tx)
	}
	if err := tx.Commit(); err != nil {
		log.Printf("index: commit: %v", err)
	}
	b.ops = b.ops[:0]
}

// ---- live updates ----

func (ix *Indexer) addWatch(abs string) {
	if ix.w == nil {
		return
	}
	ix.mu.Lock()
	if ix.watched[abs] || ix.wFull {
		ix.mu.Unlock()
		return
	}
	ix.mu.Unlock()
	if err := ix.w.Add(abs); err != nil {
		ix.mu.Lock()
		if errors.Is(err, syscall.ENOSPC) {
			ix.wFull = true
			ix.status.WatchNote = "inotify watch limit reached; folders beyond it update on the periodic rescan. Raise fs.inotify.max_user_watches on the host to cover everything."
		}
		ix.mu.Unlock()
		return
	}
	ix.mu.Lock()
	ix.watched[abs] = true
	ix.mu.Unlock()
}

func (ix *Indexer) relFromAbs(abs string) (string, bool) {
	r, err := filepath.Rel(ix.cfg.DataDir, abs)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

func (ix *Indexer) watchLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ix.w.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Chmod) && !ev.Has(fsnotify.Write) {
				continue
			}
			rel, ok := ix.relFromAbs(filepath.Dir(ev.Name))
			if !ok {
				continue
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				ix.mu.Lock()
				delete(ix.watched, ev.Name)
				ix.mu.Unlock()
			}
			ix.Touch(rel)
		case err, ok := <-ix.w.Errors:
			if !ok {
				return
			}
			log.Printf("watch: %v", err)
		}
	}
}

func (ix *Indexer) dirtyLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ix.mu.Lock()
		busy := ix.status.Scanning
		ix.mu.Unlock()
		if busy {
			continue // a full scan is reconciling everything; keep the queue for after
		}
		ix.dirtyMu.Lock()
		if len(ix.dirty) == 0 {
			ix.dirtyMu.Unlock()
			continue
		}
		list := make([]string, 0, len(ix.dirty))
		for d := range ix.dirty {
			list = append(list, d)
		}
		ix.dirty = map[string]bool{}
		ix.dirtyMu.Unlock()
		if ix.st.get("initial_scan_done") != "1" {
			continue // the first full scan will pick these up
		}
		ix.refreshMu.Lock()
		for _, d := range list {
			_ = ix.scanDir(d, false, true)
		}
		ix.refreshMu.Unlock()
		ix.refreshCounts()
	}
}
