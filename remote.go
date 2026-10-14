package main

// Connected NAS: other Burrow servers and SMB shares that this Burrow reaches
// itself and shows as extra libraries. An admin adds them in Settings; who sees
// one is decided by the same folder grants as any library (its name is the top
// folder). Their passwords and tokens are sealed with a key kept in the state
// folder, apart from the database, and are never sent back to a browser.
//
// They are reached live: browsing, previews, uploads, new folders, renames and
// deletes (into a trash on that NAS) all work, and files move between any two
// libraries. They aren't indexed, so search, links and sync stay with local
// libraries for now.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	smb2 "github.com/cloudsoda/go-smb2"
)

// remoteFS is one connected NAS, addressed with slash paths inside it ("" is its top).
type remoteFS interface {
	Stat(p string) (fs.FileInfo, error)
	ReadDir(p string) ([]fs.FileInfo, error)
	Open(p string) (io.ReadSeekCloser, fs.FileInfo, error)
	// Put writes a new file; it never replaces one (fs.ErrExist).
	Put(p string, r io.Reader, size int64, mtime time.Time) error
	Mkdir(p string) error
	// Rename never replaces anything at to (fs.ErrExist).
	Rename(from, to string) error
	// Trash takes p out of the folder: into a trash on that NAS. It returns where
	// it went for a restore here, or "" when the other NAS keeps its own trash.
	Trash(p, id string) (string, error)
	RemoveAll(p string) error
	Close() error
}

type remoteConfig struct {
	Kind string `json:"kind"` // smb or burrow
	// SMB share
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Share    string `json:"share,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Domain   string `json:"domain,omitempty"`
	// another Burrow
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
	Pin   string `json:"pin,omitempty"` // SHA-256 of its certificate, for a self-signed one
	// both: start inside this folder of it
	Folder string `json:"folder,omitempty"`
}

// public is the config without its secrets, for the settings page.
func (c remoteConfig) public() map[string]any {
	out := map[string]any{"kind": c.Kind, "folder": c.Folder}
	if c.Kind == "smb" {
		out["host"], out["port"], out["share"], out["user"], out["domain"] = c.Host, c.Port, c.Share, c.User, c.Domain
		out["has_password"] = c.Password != ""
	} else {
		out["url"], out["pin"] = c.URL, c.Pin
		out["has_token"] = c.Token != ""
	}
	return out
}

func (c *remoteConfig) check() error {
	c.Folder = strings.Trim(cleanRel(c.Folder), "/")
	switch c.Kind {
	case "smb":
		c.Host = strings.TrimSpace(c.Host)
		c.Share = strings.Trim(strings.TrimSpace(c.Share), `\/`)
		if c.Host == "" || strings.ContainsAny(c.Host, "/\\ ") {
			return errors.New("enter the NAS's address, like 192.168.1.20")
		}
		if c.Share == "" || strings.ContainsAny(c.Share, `/\`) {
			return errors.New("enter the share's name, like Media")
		}
		if c.Port == 0 {
			c.Port = 445
		}
		if c.Port < 1 || c.Port > 65535 {
			return errors.New("that port isn't valid")
		}
	case "burrow":
		u, err := normalizeURL(c.URL)
		if err != nil {
			return err
		}
		c.URL = u
		c.Pin = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(c.Pin), ":", ""))
		if c.Pin != "" && len(c.Pin) != 64 {
			return errors.New("the certificate fingerprint should be 64 hex digits")
		}
		if c.Token == "" {
			return errors.New("paste an API token made on that Burrow")
		}
	default:
		return errors.New("kind must be smb or burrow")
	}
	return nil
}

func (c remoteConfig) dial() (remoteFS, error) {
	if c.Kind == "smb" {
		return dialSMB(c)
	}
	return dialBurrow(c)
}

// ---------- the registry ----------

type remoteLib struct {
	ID   int64
	Name string
	cfg  remoteConfig

	mu      sync.Mutex
	conn    remoteFS
	lastErr string
}

// do runs fn against the connection, dialing first if needed. A failure that
// isn't about the file itself drops the connection and tries once more on a fresh one.
func (l *remoteLib) do(fn func(remoteFS) error) error {
	for try := 0; ; try++ {
		l.mu.Lock()
		c := l.conn
		if c == nil {
			var err error
			if c, err = l.cfg.dial(); err != nil {
				l.lastErr = err.Error()
				l.mu.Unlock()
				return fmt.Errorf("can't reach %s: %w", l.Name, errRemoteDown)
			}
			l.conn, l.lastErr = c, ""
		}
		l.mu.Unlock()
		err := fn(c)
		if err == nil || fileErr(err) || try > 0 {
			return err
		}
		l.mu.Lock()
		if l.conn == c {
			_ = c.Close()
			l.conn = nil
		}
		l.lastErr = err.Error()
		l.mu.Unlock()
	}
}

func (l *remoteLib) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil {
		_ = l.conn.Close()
		l.conn = nil
	}
}

var errRemoteDown = errors.New("the connected NAS isn't answering")

// fileErr: the error is about the file (missing, exists, refused), not the connection.
func fileErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrExist) || errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, errNotAllowed) || errors.Is(err, errNoWrite) || errors.Is(err, errBadName) || errors.Is(err, errExists)
}

type remoteSet struct {
	mu   sync.RWMutex
	libs map[string]*remoteLib
}

func (a *App) loadRemotes() {
	if a.remotes == nil {
		a.remotes = &remoteSet{}
	}
	libs := map[string]*remoteLib{}
	rows, err := a.st.db.Query(`SELECT id,name,sealed FROM remotes`)
	if err == nil {
		for rows.Next() {
			var l remoteLib
			var sealed []byte
			if rows.Scan(&l.ID, &l.Name, &sealed) != nil {
				continue
			}
			if b, err := a.unseal(sealed); err == nil && json.Unmarshal(b, &l.cfg) == nil {
				libs[l.Name] = &l
			}
		}
		rows.Close()
	}
	a.remotes.mu.Lock()
	old := a.remotes.libs
	a.remotes.libs = libs
	a.remotes.mu.Unlock()
	for _, l := range old {
		l.close()
	}
}

// remoteOf splits a library path on a connected NAS into it and the path inside.
func (a *App) remoteOf(rel string) (*remoteLib, string, bool) {
	if a.remotes == nil {
		return nil, "", false
	}
	rel = cleanRel(rel)
	top, inner, _ := strings.Cut(rel, "/")
	a.remotes.mu.RLock()
	l := a.remotes.libs[top]
	a.remotes.mu.RUnlock()
	if l == nil {
		return nil, "", false
	}
	return l, inner, true
}

func (a *App) remoteNames() []string {
	if a.remotes == nil {
		return nil
	}
	a.remotes.mu.RLock()
	defer a.remotes.mu.RUnlock()
	out := make([]string, 0, len(a.remotes.libs))
	for n := range a.remotes.libs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// remotePathOK: every segment is a plain name that isn't hidden.
func (a *App) remotePathOK(inner string) bool {
	if inner == "" {
		return true
	}
	for _, s := range strings.Split(inner, "/") {
		if a.validName(s) != nil {
			return false
		}
	}
	return true
}

// ---------- sealing ----------

func (a *App) remoteKey() ([]byte, error) {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	p := filepath.Join(a.cfg.StateDir, "remotes.key")
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(k); err != nil {
		f.Close()
		return nil, err
	}
	return k, f.Close()
}

func (a *App) seal(b []byte) ([]byte, error) {
	k, err := a.remoteKey()
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	n := make([]byte, g.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return g.Seal(n, n, b, []byte("burrow-remote")), nil
}

func (a *App) unseal(b []byte) ([]byte, error) {
	k, err := a.remoteKey()
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	if len(b) < g.NonceSize() {
		return nil, errors.New("sealed config is too short")
	}
	return g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("burrow-remote"))
}

// ---------- admin API ----------

type remoteView struct {
	ID     int64          `json:"id"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
	Error  string         `json:"error,omitempty"`
}

func (a *App) apiRemotes(w http.ResponseWriter, r *http.Request) {
	out := []remoteView{}
	for _, n := range a.remoteNames() {
		l, _, _ := a.remoteOf(n)
		l.mu.Lock()
		e := l.lastErr
		l.mu.Unlock()
		out = append(out, remoteView{ID: l.ID, Name: l.Name, Config: l.cfg.public(), Error: e})
	}
	writeJSON(w, out)
}

type remoteInput struct {
	Name string `json:"name"`
	remoteConfig
}

// checkRemote validates a new or changed connection and tries it, so a wrong
// password is caught when it is typed, not when someone browses.
func (a *App) checkRemote(in *remoteInput, id int64) error {
	in.Name = strings.TrimSpace(in.Name)
	if err := a.validName(in.Name); err != nil {
		return errors.New("give it a name that works as a folder name")
	}
	for _, ri := range a.availableRoots() {
		if strings.EqualFold(ri.Name, in.Name) {
			return errors.New("a folder on this NAS already has that name")
		}
	}
	var other int64
	if a.st.db.QueryRow(`SELECT id FROM remotes WHERE name=? COLLATE NOCASE`, in.Name).Scan(&other) == nil && other != id {
		return errors.New("another connection already has that name")
	}
	if err := in.remoteConfig.check(); err != nil {
		return err
	}
	c, err := in.remoteConfig.dial()
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ReadDir(""); err != nil {
		return fmt.Errorf("connected, but couldn't list it: %v", err)
	}
	return nil
}

func (a *App) apiAddRemote(w http.ResponseWriter, r *http.Request) {
	var in remoteInput
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if err := a.checkRemote(&in, 0); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	b, _ := json.Marshal(in.remoteConfig)
	sealed, err := a.seal(b)
	if err != nil {
		httpErr(w, 500, "could not store it")
		return
	}
	res, err := a.st.exec(`INSERT INTO remotes(name,kind,sealed,created) VALUES(?,?,?,?)`, in.Name, in.Kind, sealed, now())
	if err != nil {
		httpErr(w, 400, "could not add it: "+err.Error())
		return
	}
	id, _ := res.LastInsertId()
	a.loadRemotes()
	writeJSON(w, remoteView{ID: id, Name: in.Name, Config: in.remoteConfig.public()})
}

// apiPatchRemote changes a connection; secrets left empty are kept.
func (a *App) apiPatchRemote(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var oldName string
	var sealed []byte
	if a.st.db.QueryRow(`SELECT name,sealed FROM remotes WHERE id=?`, id).Scan(&oldName, &sealed) != nil {
		httpErr(w, 404, "no such connection")
		return
	}
	var cur remoteConfig
	if b, err := a.unseal(sealed); err != nil || json.Unmarshal(b, &cur) != nil {
		httpErr(w, 500, "could not read the stored connection")
		return
	}
	var in remoteInput
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if in.Kind == "" {
		in.Kind = cur.Kind
	}
	if in.Password == "" && in.Kind == cur.Kind {
		in.Password = cur.Password
	}
	if in.Token == "" && in.Kind == cur.Kind {
		in.Token = cur.Token
	}
	if in.Name == "" {
		in.Name = oldName
	}
	if in.Name != oldName {
		// grants, favorites and trash rows name the old path; renaming would strand them
		httpErr(w, 400, "a connection can't be renamed; remove it and add it again")
		return
	}
	if err := a.checkRemote(&in, id); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	b, _ := json.Marshal(in.remoteConfig)
	if sealed, err := a.seal(b); err != nil {
		httpErr(w, 500, "could not store it")
		return
	} else if _, err := a.st.exec(`UPDATE remotes SET kind=?, sealed=? WHERE id=?`, in.Kind, sealed, id); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	a.loadRemotes()
	writeJSON(w, remoteView{ID: id, Name: in.Name, Config: in.remoteConfig.public()})
}

func (a *App) apiDeleteRemote(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	res, err := a.st.exec(`DELETE FROM remotes WHERE id=?`, id)
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		httpErr(w, 404, "no such connection")
		return
	}
	a.loadRemotes()
	writeJSON(w, map[string]bool{"ok": true})
}

// apiRemoteShares lists the shares an SMB server offers, to pick from while adding one.
func (a *App) apiRemoteShares(w http.ResponseWriter, r *http.Request) {
	var in remoteConfig
	if err := readJSON(r, &in); err != nil || in.Host == "" {
		httpErr(w, 400, "enter the NAS's address")
		return
	}
	in.Kind, in.Share = "smb", "IPC$"
	if in.Port == 0 {
		in.Port = 445
	}
	s, closeFn, err := smbSession(in)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	defer closeFn()
	list, err := s.ListShares()
	if err != nil {
		httpErr(w, 400, "signed in, but couldn't list the shares: "+err.Error())
		return
	}
	names := []string{}
	for _, sh := range list {
		if !strings.HasSuffix(sh.Name, "$") {
			names = append(names, sh.Name)
		}
	}
	sort.Strings(names)
	writeJSON(w, names)
}

// ---------- SMB ----------

type smbFS struct {
	sess  *smb2.Session
	share *smb2.Share
	base  string
}

func smbSession(c remoteConfig) (*smb2.Session, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := &smb2.Dialer{Initiator: &smb2.NTLMInitiator{User: c.User, Password: c.Password, Domain: c.Domain}}
	s, err := d.Dial(ctx, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "logon") || strings.Contains(strings.ToLower(msg), "password") {
			return nil, nil, errors.New("the NAS turned down that user name and password")
		}
		return nil, nil, fmt.Errorf("couldn't connect to %s: %v", c.Host, err)
	}
	return s, func() { _ = s.Logoff() }, nil
}

func dialSMB(c remoteConfig) (remoteFS, error) {
	s, closeFn, err := smbSession(c)
	if err != nil {
		return nil, err
	}
	sh, err := s.Mount(c.Share)
	if err != nil {
		closeFn()
		return nil, fmt.Errorf("couldn't open the share %s: %v", c.Share, err)
	}
	return &smbFS{sess: s, share: sh, base: c.Folder}, nil
}

// p is the share path for a path inside the connection. Paths are already
// checked to be plain names; SMB uses backslashes, which names can't contain.
func (s *smbFS) p(p string) string {
	full := path.Join(s.base, p)
	if full == "." {
		return ""
	}
	return strings.ReplaceAll(full, "/", `\`)
}

func (s *smbFS) Stat(p string) (fs.FileInfo, error) { return s.share.Stat(s.p(p)) }

func (s *smbFS) ReadDir(p string) ([]fs.FileInfo, error) {
	list, err := s.share.ReadDir(s.p(p))
	if err != nil {
		return nil, err
	}
	out := list[:0]
	for _, fi := range list {
		if fi.IsDir() || fi.Mode().IsRegular() { // no links or devices
			out = append(out, fi)
		}
	}
	return out, nil
}

func (s *smbFS) Open(p string) (io.ReadSeekCloser, fs.FileInfo, error) {
	f, err := s.share.Open(s.p(p))
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, fs.ErrNotExist
	}
	return f, fi, nil
}

func (s *smbFS) Put(p string, r io.Reader, size int64, mtime time.Time) error {
	f, err := s.share.OpenFile(s.p(p), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return err
	}
	bp := copyBufs.Get().(*[]byte)
	n, err := io.CopyBuffer(f, r, *bp)
	copyBufs.Put(bp)
	if err == nil && size >= 0 && n != size {
		err = io.ErrUnexpectedEOF
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = s.share.Remove(s.p(p))
		return err
	}
	if !mtime.IsZero() {
		_ = s.share.Chtimes(s.p(p), mtime, mtime)
	}
	return nil
}

func (s *smbFS) Mkdir(p string) error { return s.share.Mkdir(s.p(p), 0o775) }

func (s *smbFS) Rename(from, to string) error {
	// SMB's rename replaces by default; look first so nothing is lost
	if _, err := s.share.Lstat(s.p(to)); err == nil {
		return fs.ErrExist
	}
	return s.share.Rename(s.p(from), s.p(to))
}

func (s *smbFS) Trash(p, id string) (string, error) {
	slot := path.Join(trashName, id)
	if err := s.share.MkdirAll(s.p(slot), 0o770); err != nil {
		return "", errors.New("could not make room in the trash")
	}
	dst := path.Join(slot, path.Base(p))
	if err := s.share.Rename(s.p(p), s.p(dst)); err != nil {
		_ = s.share.Remove(s.p(slot))
		return "", errors.New("could not move it to the trash")
	}
	return dst, nil
}

func (s *smbFS) RemoveAll(p string) error { return s.share.RemoveAll(s.p(p)) }

func (s *smbFS) Close() error {
	_ = s.share.Umount()
	return s.sess.Logoff()
}

// ---------- another Burrow ----------

type burrowFS struct {
	base   string // its URL
	token  string
	folder string
	hc     *http.Client
}

func dialBurrow(c remoteConfig) (remoteFS, error) {
	b := &burrowFS{base: c.URL, token: c.Token, folder: c.Folder, hc: pinnedClient(c.Pin)}
	resp, err := b.call("GET", "/api/ls?path="+urlQ(b.folder), nil, nil)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return b, nil
}

func (b *burrowFS) full(p string) string { return strings.Trim(path.Join(b.folder, p), "/") }

func (b *burrowFS) call(method, u string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, b.base+u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("X-Burrow", "1")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := b.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errRemoteDown, err)
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	var e struct{ Error string }
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
	switch resp.StatusCode {
	case 401:
		return nil, errors.New("that Burrow didn't accept the token; make a new one there")
	case 403:
		return nil, fmt.Errorf("%w: %s", errNoWrite, e.Error)
	case 404:
		return nil, fs.ErrNotExist
	case 409:
		return nil, fs.ErrExist
	}
	if e.Error == "" {
		e.Error = resp.Status
	}
	return nil, errors.New(e.Error)
}

func (b *burrowFS) json(method, u string, in, out any) error {
	var body io.Reader
	hdr := map[string]string{}
	if in != nil {
		j, _ := json.Marshal(in)
		body = strings.NewReader(string(j))
		hdr["Content-Type"] = "application/json"
	}
	resp, err := b.call(method, u, body, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// remoteInfo is a file or folder another Burrow described.
type remoteInfo struct{ e Entry }

func (i remoteInfo) Name() string       { return i.e.Name }
func (i remoteInfo) Size() int64        { return i.e.Size }
func (i remoteInfo) ModTime() time.Time { return time.Unix(i.e.Mtime, 0) }
func (i remoteInfo) IsDir() bool        { return i.e.IsDir }
func (i remoteInfo) Sys() any           { return nil }
func (i remoteInfo) Mode() fs.FileMode {
	if i.e.IsDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}

func (b *burrowFS) Stat(p string) (fs.FileInfo, error) {
	full := b.full(p)
	if full == "" {
		return remoteInfo{Entry{Name: "", IsDir: true}}, nil
	}
	var out struct{ Entry Entry }
	if err := b.json("GET", "/api/info?path="+urlQ(full), nil, &out); err != nil {
		return nil, err
	}
	return remoteInfo{out.Entry}, nil
}

func (b *burrowFS) ReadDir(p string) ([]fs.FileInfo, error) {
	var out struct{ Entries []Entry }
	if err := b.json("GET", "/api/ls?path="+urlQ(b.full(p)), nil, &out); err != nil {
		return nil, err
	}
	list := make([]fs.FileInfo, 0, len(out.Entries))
	for _, e := range out.Entries {
		list = append(list, remoteInfo{e})
	}
	return list, nil
}

func (b *burrowFS) Open(p string) (io.ReadSeekCloser, fs.FileInfo, error) {
	fi, err := b.Stat(p)
	if err != nil {
		return nil, nil, err
	}
	if fi.IsDir() {
		return nil, nil, fs.ErrNotExist
	}
	return &httpSeeker{b: b, path: b.full(p), size: fi.Size()}, fi, nil
}

// httpSeeker reads a file from another Burrow, with a range request after each seek.
type httpSeeker struct {
	b    *burrowFS
	path string
	size int64
	pos  int64
	body io.ReadCloser
}

func (h *httpSeeker) Read(p []byte) (int, error) {
	if h.pos >= h.size {
		return 0, io.EOF
	}
	if h.body == nil {
		hdr := map[string]string{}
		if h.pos > 0 {
			hdr["Range"] = fmt.Sprintf("bytes=%d-", h.pos)
		}
		resp, err := h.b.call("GET", "/api/raw?path="+urlQ(h.path), nil, hdr)
		if err != nil {
			return 0, err
		}
		if h.pos > 0 && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			return 0, errors.New("the other Burrow didn't answer a range request")
		}
		h.body = resp.Body
	}
	n, err := h.body.Read(p)
	h.pos += int64(n)
	return n, err
}

func (h *httpSeeker) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		off += h.pos
	case io.SeekEnd:
		off += h.size
	}
	if off < 0 {
		return 0, errors.New("negative position")
	}
	if off != h.pos && h.body != nil {
		h.body.Close()
		h.body = nil
	}
	h.pos = off
	return off, nil
}

func (h *httpSeeker) Close() error {
	if h.body != nil {
		return h.body.Close()
	}
	return nil
}

// Put uploads through the other Burrow's resumable uploads, in one chunk at a
// time, so it checks the file end to end like any upload.
func (b *burrowFS) Put(p string, r io.Reader, size int64, mtime time.Time) error {
	dir, name := path.Split(b.full(p))
	var up struct {
		ID      string `json:"id"`
		Skipped bool   `json:"skipped"`
	}
	if err := b.json("POST", "/api/uploads", map[string]any{"dir": strings.TrimSuffix(dir, "/"), "name": name, "size": size, "mtime": mtime.UnixMilli(), "conflict": "fail"}, &up); err != nil {
		return err
	}
	var off int64
	for off < size {
		n := min(size-off, uploadChunkMax)
		resp, err := b.call("PUT", fmt.Sprintf("/api/uploads/%s?offset=%d", up.ID, off), io.LimitReader(r, n), map[string]string{"Content-Type": "application/octet-stream"})
		if err != nil {
			_ = b.json("DELETE", "/api/uploads/"+up.ID, nil, nil)
			return err
		}
		resp.Body.Close()
		off += n
	}
	return b.json("POST", "/api/uploads/"+up.ID+"/finish", map[string]any{"conflict": "fail"}, nil)
}

func (b *burrowFS) Mkdir(p string) error {
	return b.json("POST", "/api/fs/mkdir", map[string]any{"path": b.full(p)}, nil)
}

func (b *burrowFS) Rename(from, to string) error {
	return b.json("POST", "/api/fs/move", map[string]any{"from": b.full(from), "to": b.full(to)}, nil)
}

// Trash deletes it on the other Burrow, which keeps it in its own trash.
func (b *burrowFS) Trash(p, _ string) (string, error) {
	var out []struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := b.json("POST", "/api/fs/delete", map[string]any{"paths": []string{b.full(p)}}, &out); err != nil {
		return "", err
	}
	if len(out) != 1 || !out[0].OK {
		msg := "could not delete it"
		if len(out) == 1 && out[0].Error != "" {
			msg = out[0].Error
		}
		return "", errors.New(msg)
	}
	return "", nil
}

func (b *burrowFS) RemoveAll(p string) error {
	_, err := b.Trash(p, "")
	return err
}

func (b *burrowFS) Close() error {
	b.hc.CloseIdleConnections()
	return nil
}

// pinnedClient talks to another Burrow: with a pin, only a certificate with that
// SHA-256 fingerprint is accepted (for a self-signed one); without, it must be
// publicly trusted.
func pinnedClient(pin string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if pin != "" {
		tr.TLSClientConfig.InsecureSkipVerify = true // replaced by the fingerprint check below
		tr.TLSClientConfig.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if !strings.EqualFold(hex.EncodeToString(sum[:]), pin) {
				return errors.New("that Burrow's certificate doesn't match the fingerprint")
			}
			return nil
		}
	}
	return &http.Client{Transport: tr, Timeout: 0, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func urlQ(s string) string { return url.QueryEscape(s) }
