package main

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
)

type Share struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	IsDir      bool   `json:"dir"`
	Mode       string `json:"mode"` // link | drop
	Label      string `json:"label"`
	Note       string `json:"note"`
	Created    int64  `json:"created"`
	Expires    int64  `json:"expires"`
	HasPW      bool   `json:"has_password"`
	MaxDL      int64  `json:"max_downloads"`
	DL         int64  `json:"downloads"`
	Revoked    bool   `json:"revoked"`
	LastAccess int64  `json:"last_access"`
	AllowUp    bool   `json:"allow_upload"` // collection: recipients may add files
	Items      int    `json:"items,omitempty"`
	State      string `json:"state"` // active | expired | revoked | used-up | missing | unavailable
	URL        string `json:"url"`
	OwnerID    int64  `json:"owner_id"`
	Owner      string `json:"owner,omitempty"` // filled in for admins, who see everyone's links
	pwHash     string
}

// A share is only a database row: it points at library paths and never copies files.
const shareCols = `s.id,s.path,s.is_dir,s.mode,s.label,s.note,s.created,s.expires,s.pw_hash,s.max_dl,s.dl,s.revoked,s.last_access,s.allow_upload,s.owner_id`

func scanShare(sc interface{ Scan(...any) error }, extra ...any) (Share, error) {
	var s Share
	err := sc.Scan(append([]any{&s.ID, &s.Path, &s.IsDir, &s.Mode, &s.Label, &s.Note, &s.Created, &s.Expires, &s.pwHash, &s.MaxDL, &s.DL, &s.Revoked, &s.LastAccess, &s.AllowUp, &s.OwnerID}, extra...)...)
	s.HasPW = s.pwHash != ""
	return s, err
}

// shareOwner returns the link's owner if the account still exists and is enabled.
// Links run with their owner's permissions, never more.
func (a *App) shareOwner(s Share) *User { return a.activeUser(s.OwnerID) }

func (a *App) shareState(s *Share) string { return a.shareStateFor(s, a.shareOwner(*s)) }

// shareStateFor works out whether a link still works. A link stops working when its
// owner is gone or disabled, or can no longer share what it points at; a file drop
// needs its owner to still be an admin, since drops write outside every folder grant.
func (a *App) shareStateFor(s *Share, owner *User) string {
	// checked first: nothing below (download resume included) may run without an owner
	if owner == nil {
		return "unavailable"
	}
	switch {
	case s.Revoked:
		return "revoked"
	case s.Expires > 0 && s.Expires <= now():
		return "expired"
	case s.MaxDL > 0 && s.DL >= s.MaxDL:
		return "used-up"
	}
	if s.Mode == "link" {
		if _, _, err := a.resolve(s.Path); err != nil {
			return "missing"
		}
	}
	switch {
	case s.Mode == "link" && !owner.can(s.Path, permShare):
		return "unavailable"
	case s.Mode == "drop" && !owner.Admin:
		return "unavailable"
	}
	return "active"
}

func (a *App) baseURL(r *http.Request) string {
	if u := strings.TrimRight(a.st.get("public_url"), "/"); u != "" {
		return u
	}
	scheme := "http"
	if a.isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	if xf := r.Header.Get("X-Forwarded-Host"); xf != "" && a.trusted(peer) && !strings.ContainsAny(xf, "/\\@ ") {
		host = xf
	}
	return scheme + "://" + host
}

func (a *App) getShare(id string) (Share, error) {
	return scanShare(a.st.db.QueryRow(`SELECT `+shareCols+` FROM shares s WHERE s.id=?`, id))
}

// ownedShare loads a share u may manage: their own, or any share for an admin, and
// through a folder-limited token only links inside its folders. Anyone else gets
// "not found", so share ids can't be probed.
func (a *App) ownedShare(u *User, id string) (Share, error) {
	s, err := a.getShare(id)
	if err != nil {
		return s, err
	}
	if (!u.Admin && s.OwnerID != u.ID) || !a.shareInScope(u, s) {
		return Share{}, sql.ErrNoRows
	}
	return s, nil
}

// listShares lists u's links (every link for an admin), optionally only those for one path.
func (a *App) listShares(r *http.Request, u *User, pathFilter string, activeOnly bool) ([]Share, error) {
	q := `SELECT ` + shareCols + `, coalesce(o.username,'') FROM shares s LEFT JOIN users o ON o.id=s.owner_id WHERE 1`
	var args []any
	if !u.Admin {
		q += ` AND s.owner_id=?`
		args = append(args, u.ID)
	}
	if pathFilter != "" {
		q += ` AND ((s.mode='link' AND s.path=?) OR (s.mode='collection' AND s.id IN (SELECT share_id FROM share_items WHERE path=?)))`
		args = append(args, cleanRel(pathFilter), cleanRel(pathFilter))
	}
	q += ` ORDER BY s.created DESC LIMIT 2000`
	rows, err := a.st.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var list []Share
	for rows.Next() {
		var owner string
		s, err := scanShare(rows, &owner)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if u.Admin {
			s.Owner = owner
			if owner == "" {
				s.Owner = "(deleted user)"
			}
		}
		list = append(list, s)
	}
	rows.Close()
	out := []Share{}
	owners := map[int64]*User{}
	base := a.baseURL(r)
	for _, s := range list {
		o, ok := owners[s.OwnerID]
		if !ok {
			o = a.shareOwner(s)
			owners[s.OwnerID] = o
		}
		s.State = a.shareStateFor(&s, o)
		if (activeOnly && s.State != "active") || !a.shareInScope(u, s) {
			continue
		}
		s.URL = base + "/s/" + s.ID
		if s.Mode == "collection" {
			_ = a.st.db.QueryRow(`SELECT count(*) FROM share_items WHERE share_id=?`, s.ID).Scan(&s.Items)
		}
		out = append(out, s)
	}
	return out, nil
}

type ShareInput struct {
	Path         string   `json:"path"`
	Mode         string   `json:"mode"`
	Label        string   `json:"label"`
	Note         string   `json:"note"`
	ExpiresHours float64  `json:"expires_hours"` // 0 = use default, -1 = never
	ExpiresAt    int64    `json:"expires_at"`    // unix; wins over expires_hours
	Password     string   `json:"password"`
	MaxDownloads int64    `json:"max_downloads"`
	Slug         string   `json:"slug"`
	AllowUpload  bool     `json:"allow_upload"`
	Items        []string `json:"items"` // collection: library paths to include
}

var slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{5,63}$`)
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9 ._()-]+`)

var errAdminsOnly = errors.New("only admins can do that")

// createShare makes a link owned by u. Nothing is copied and no folder is created:
// a drop's or shared folder's upload folder only appears when the first file arrives.
var errRemoteNoLinks = errors.New("links to a connected NAS aren't available yet; move or copy the files here first")

func (a *App) createShare(r *http.Request, u *User, in ShareInput) (Share, error) {
	in.Mode = strings.ToLower(strings.TrimSpace(in.Mode))
	if in.Mode == "" {
		in.Mode = "link"
	}
	// held from choosing an upload folder name until the row exists, so two new
	// shared folders can't claim the same folder
	a.shareMu.Lock()
	defer a.shareMu.Unlock()
	var rel string
	isDir := false
	switch in.Mode {
	case "link":
		if _, _, remote := a.remoteOf(in.Path); remote {
			return Share{}, errRemoteNoLinks
		}
		abs, cr, err := a.resolve(in.Path)
		if err != nil {
			return Share{}, fmt.Errorf("can't share %q: %w", in.Path, err)
		}
		if !u.can(cr, permShare) {
			return Share{}, fmt.Errorf("can't share %q: %w", in.Path, errNotAllowed)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return Share{}, err
		}
		rel, isDir = cr, info.IsDir()
	case "drop":
		if !u.Admin {
			return Share{}, errAdminsOnly
		}
		name, err := a.dropFolderName(in.Label)
		if err != nil {
			return Share{}, err
		}
		rel, isDir = name, true
	case "collection":
		if strings.TrimSpace(in.Label) == "" {
			return Share{}, errors.New("give the shared folder a name")
		}
		if in.AllowUpload && !u.Admin {
			return Share{}, errors.New("only admins can let recipients upload")
		}
		for _, p := range in.Items {
			if _, err := a.shareableItem(u, p); err != nil {
				return Share{}, err
			}
		}
		name, err := a.newUploadFolder(in.Label)
		if err != nil {
			return Share{}, err
		}
		rel, isDir = name, true
	default:
		return Share{}, errors.New("mode must be link, drop or collection")
	}
	id := randSlug(12)
	if s := strings.TrimSpace(in.Slug); s != "" {
		if !slugRe.MatchString(s) {
			return Share{}, errors.New("custom link name: 6-64 letters, digits, - or _")
		}
		var n int
		_ = a.st.db.QueryRow(`SELECT count(*) FROM shares WHERE id=?`, s).Scan(&n)
		if n > 0 {
			return Share{}, errors.New("that link name is taken")
		}
		id = s
	}
	exp := int64(0)
	switch {
	case in.ExpiresAt > 0:
		exp = in.ExpiresAt
	case in.ExpiresHours > 0:
		exp = now() + int64(in.ExpiresHours*3600)
	case in.ExpiresHours == 0:
		if d, _ := strconv.ParseFloat(a.st.get("default_expiry_hours"), 64); d > 0 {
			exp = now() + int64(d*3600)
		}
	}
	pw := ""
	if in.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(in.Password), 10)
		if err != nil {
			return Share{}, err
		}
		pw = string(h)
	}
	label := strings.TrimSpace(in.Label)
	if label == "" && in.Mode == "link" {
		label = path.Base(rel)
	}
	_, err := a.st.exec(`INSERT INTO shares(id,path,is_dir,mode,label,note,created,expires,pw_hash,max_dl,allow_upload,owner_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, rel, isDir, in.Mode, trunc(label, 200), trunc(in.Note, 2000), now(), exp, pw, max64(in.MaxDownloads, 0), in.Mode == "collection" && in.AllowUpload, u.ID)
	if err != nil {
		return Share{}, err
	}
	if in.Mode == "collection" {
		if err := a.addItems(u, id, in.Items, u); err != nil {
			return Share{}, err
		}
	}
	s, err := a.getShare(id)
	s.State = a.shareState(&s)
	s.URL = a.baseURL(r) + "/s/" + s.ID
	return s, err
}

type SharePatch struct {
	Label        *string  `json:"label"`
	Note         *string  `json:"note"`
	ExpiresAt    *int64   `json:"expires_at"`
	ExpiresHours *float64 `json:"expires_hours"`
	Password     *string  `json:"password"` // "" removes it
	MaxDownloads *int64   `json:"max_downloads"`
	Revoked      *bool    `json:"revoked"`
	AllowUpload  *bool    `json:"allow_upload"`
	ResetCount   bool     `json:"reset_count"`
}

// patchShare changes a link u may manage (see ownedShare).
func (a *App) patchShare(u *User, id string, p SharePatch) error {
	if _, err := a.ownedShare(u, id); err != nil {
		return err
	}
	if p.AllowUpload != nil && *p.AllowUpload && !u.Admin {
		return errAdminsOnly
	}
	set := func(col string, v any) { _, _ = a.st.exec(`UPDATE shares SET `+col+`=? WHERE id=?`, v, id) }
	if p.Label != nil {
		set("label", trunc(*p.Label, 200))
	}
	if p.Note != nil {
		set("note", trunc(*p.Note, 2000))
	}
	if p.ExpiresAt != nil {
		set("expires", *p.ExpiresAt)
	} else if p.ExpiresHours != nil {
		if *p.ExpiresHours < 0 {
			set("expires", 0)
		} else {
			set("expires", now()+int64(*p.ExpiresHours*3600))
		}
	}
	if p.Password != nil {
		h := ""
		if *p.Password != "" {
			b, err := bcrypt.GenerateFromPassword([]byte(*p.Password), 10)
			if err != nil {
				return err
			}
			h = string(b)
		}
		set("pw_hash", h)
	}
	if p.MaxDownloads != nil {
		set("max_dl", max64(*p.MaxDownloads, 0))
	}
	if p.Revoked != nil {
		set("revoked", *p.Revoked)
	}
	if p.AllowUpload != nil {
		set("allow_upload", *p.AllowUpload)
	}
	if p.ResetCount {
		set("dl", 0)
	}
	return nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (a *App) logShare(r *http.Request, id, kind, detail string) {
	_, _ = a.st.exec(`INSERT INTO share_events(share_id,ts,kind,ip,ua,detail) VALUES(?,?,?,?,?,?)`, id, now(), kind, a.clientIP(r), trunc(r.UserAgent(), 200), trunc(detail, 500))
	_, _ = a.st.exec(`UPDATE shares SET last_access=? WHERE id=?`, now(), id)
}

type ShareEvent struct {
	TS     int64  `json:"ts"`
	Kind   string `json:"kind"`
	IP     string `json:"ip"`
	UA     string `json:"ua"`
	Detail string `json:"detail"`
	Share  string `json:"share,omitempty"`
}

// shareEvents lists link activity: one link's if id is set, otherwise all of u's links
// (every link for an admin).
func (a *App) shareEvents(u *User, id string, limit int) ([]ShareEvent, error) {
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	q := `SELECT ts,kind,ip,ua,detail,share_id FROM share_events WHERE 1`
	var args []any
	if id != "" {
		if _, err := a.ownedShare(u, id); err != nil {
			return nil, err
		}
		q += ` AND share_id=?`
		args = append(args, id)
	} else {
		if !u.Admin {
			q += ` AND share_id IN (SELECT id FROM shares WHERE owner_id=?)`
			args = append(args, u.ID)
		}
		if fc, fargs := u.lim.foldersCond("path"); fc != "" {
			q += ` AND share_id IN (SELECT id FROM shares WHERE mode='link' AND ` + fc + `)`
			args = append(args, fargs...)
		}
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := a.st.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShareEvent{}
	for rows.Next() {
		var e ShareEvent
		_ = rows.Scan(&e.TS, &e.Kind, &e.IP, &e.UA, &e.Detail, &e.Share)
		out = append(out, e)
	}
	return out, nil
}

func qrPNG(text string, size int) ([]byte, error) {
	if size < 128 || size > 2048 {
		size = 512
	}
	return qrcode.Encode(text, qrcode.Medium, size)
}

// ---- public share pages ----

var shareTmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"size":  humanSize,
	"date":  func(t int64) string { return time.Unix(t, 0).Format("Jan 2, 2006 15:04") },
	"join":  func(a, b string) string { return strings.TrimPrefix(a+"/"+b, "/") },
	"kindv": func(k string) bool { return k == "image" || k == "video" || k == "audio" },
}).ParseFS(webFS, "web/share.html"))

type sharePage struct {
	S         Share
	Title     string
	Sub       string // path inside a folder share
	Crumbs    []crumb
	Entries   []Entry
	File      *Entry
	Preview   string // image | video | audio | pdf | text | ""
	State     string
	NeedPW    bool
	PWError   bool
	Uploaded  int
	MaxUpload int64
}

type crumb struct{ Name, Sub string }

// shareTarget resolves the share's root and an optional sub-path inside it, which
// the owner must still be allowed to share (a carved-out subfolder stays hidden).
func (a *App) shareTarget(s Share, owner *User, sub string) (abs, rel string, err error) {
	sub = cleanRel(sub)
	if s.Mode == "drop" {
		return "", "", errNotAllowed
	}
	if sub != "" && !s.IsDir {
		return "", "", errNotAllowed
	}
	rel = s.Path
	if sub != "" {
		rel = s.Path + "/" + sub
	}
	abs, rel, err = a.resolve(rel)
	if err != nil {
		return "", "", err
	}
	if rel != s.Path && !strings.HasPrefix(rel, s.Path+"/") {
		return "", "", errNotAllowed
	}
	if !owner.can(rel, permShare) {
		return "", "", errNotAllowed
	}
	return abs, rel, nil
}

func (a *App) pwCookieName(id string) string { return "bs_" + id }

func (a *App) sharePWOK(r *http.Request, s Share) bool {
	if s.pwHash == "" {
		return true
	}
	c, err := r.Cookie(a.pwCookieName(s.ID))
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	e, _ := strconv.ParseInt(exp, 10, 64)
	if e < now() {
		return false
	}
	return hmacEqual(sig, a.sign(s.ID+"|"+s.pwHash+"|"+exp))
}

// Limits on guessing, per client IP and per link.
const (
	enumLimit     = 30 // unknown link ids per IP per 15 minutes
	sharePWLimit  = 10 // wrong share passwords per IP per 15 minutes
	linkPWLimit   = 50 // wrong passwords for one link per hour, from all IPs together
	guessWindow   = 15 * time.Minute
	linkPWWindow  = time.Hour
	shareNoStore  = "private, no-store"
	enumKeyPrefix = "enum:"
)

func (a *App) handleShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.PathValue("action")
	// Share content is private: no proxy (Nginx Proxy Manager can cache) and no
	// browser cache should keep a copy.
	w.Header().Set("Cache-Control", shareNoStore)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	// Guessing link ids (custom names are shorter than random ones) is throttled.
	enumKey := enumKeyPrefix + limitIP(a.clientIP(r))
	if !a.limiter.take(enumKey, enumLimit, guessWindow) {
		a.renderShare(w, http.StatusTooManyRequests, sharePage{State: "throttled", Title: "Too many tries"})
		return
	}
	s, err := a.getShare(id)
	if err != nil {
		a.renderShare(w, http.StatusNotFound, sharePage{State: "missing", Title: "Link not found"})
		return
	}
	a.limiter.refund(enumKey) // only misses count
	owner := a.shareOwner(s)
	s.State = a.shareStateFor(&s, owner)
	// A browser that was already counted for a file may keep fetching it after the
	// limit is reached (resuming, seeking in a video).
	resuming := s.State == "used-up" && (action == "dl" || action == "raw") && a.resumeOK(r, s, cleanRel(r.URL.Query().Get("p")))
	if s.State != "active" && !resuming {
		a.renderShare(w, http.StatusGone, sharePage{S: s, State: s.State, Title: s.Label})
		return
	}
	if action == "qr.png" {
		png, err := qrPNG(a.baseURL(r)+"/s/"+s.ID, atoiDef(r.URL.Query().Get("size"), 512))
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
		return
	}

	// password gate
	if action == "auth" && r.Method == http.MethodPost {
		key, linkKey := "share:"+limitIP(a.clientIP(r)), "sharepw:"+s.ID
		if !a.limiter.take(key, sharePWLimit, guessWindow) {
			a.renderShare(w, http.StatusTooManyRequests, sharePage{S: s, Title: s.Label, State: "locked"})
			return
		}
		if !a.limiter.take(linkKey, linkPWLimit, linkPWWindow) {
			a.limiter.refund(key)
			a.renderShare(w, http.StatusTooManyRequests, sharePage{S: s, Title: s.Label, State: "locked"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if bcrypt.CompareHashAndPassword([]byte(s.pwHash), []byte(r.FormValue("password"))) != nil {
			a.logShare(r, s.ID, "password-fail", "")
			a.renderShare(w, http.StatusUnauthorized, sharePage{S: s, Title: s.Label, NeedPW: true, PWError: true, State: "active"})
			return
		}
		a.limiter.refund(key)
		a.limiter.refund(linkKey)
		exp := strconv.FormatInt(now()+12*3600, 10)
		http.SetCookie(w, &http.Cookie{Name: a.pwCookieName(s.ID), Value: exp + "." + a.sign(s.ID+"|"+s.pwHash+"|"+exp), Path: "/s/" + s.ID, HttpOnly: true, Secure: a.isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600})
		http.Redirect(w, r, "/s/"+s.ID, http.StatusSeeOther)
		return
	}
	if !a.sharePWOK(r, s) {
		a.renderShare(w, http.StatusUnauthorized, sharePage{S: s, Title: s.Label, NeedPW: true, State: "active"})
		return
	}

	if s.Mode == "drop" {
		a.handleDrop(w, r, s, action)
		return
	}
	if s.Mode == "collection" {
		a.handleCollection(w, r, s, owner, action)
		return
	}

	sub := r.URL.Query().Get("p")
	abs, rel, err := a.shareTarget(s, owner, sub)
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
		if !a.gateDownload(w, r, s, cleanRel(sub), action == "dl") {
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
		a.logShare(r, s.ID, "zip", strings.TrimPrefix(strings.TrimPrefix(rel, s.Path), "/"))
		a.streamZipMany(w, info.Name(), []zipSource{{Abs: abs, Name: info.Name(), Lib: rel}}, zipAllow(owner, permShare))
		return
	case "":
	default:
		http.NotFound(w, r)
		return
	}

	pg := sharePage{S: s, Title: s.Label, Sub: cleanRel(sub), State: "active"}
	if pg.Title == "" {
		pg.Title = info.Name()
	}
	if s.IsDir {
		pg.Crumbs = []crumb{{Name: path.Base(s.Path), Sub: ""}}
		if pg.Sub != "" {
			acc := ""
			for _, seg := range strings.Split(pg.Sub, "/") {
				acc = strings.TrimPrefix(acc+"/"+seg, "/")
				pg.Crumbs = append(pg.Crumbs, crumb{Name: seg, Sub: acc})
			}
		}
	}
	if info.IsDir() {
		des, _ := os.ReadDir(abs)
		for _, d := range des {
			if a.idx.excluded(d.Name()) || d.Type()&os.ModeSymlink != 0 {
				continue
			}
			if !owner.canReach(rel+"/"+d.Name(), permShare) {
				continue
			}
			fi, err := d.Info()
			if err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
				continue
			}
			e := entryFromInfo(strings.TrimPrefix(strings.TrimPrefix(rel+"/"+d.Name(), s.Path), "/"), fi)
			pg.Entries = append(pg.Entries, e)
		}
		sortEntries(pg.Entries)
	} else {
		e := entryFromInfo(strings.TrimPrefix(strings.TrimPrefix(rel, s.Path), "/"), info)
		pg.File = &e
		pg.Preview = previewKind(e)
	}
	if pg.Sub == "" {
		a.logShare(r, s.ID, "view", "")
	}
	a.renderShare(w, 200, pg)
}

// countsAsDownload is false for range requests that resume past byte 0.
func countsAsDownload(r *http.Request) bool {
	rg := strings.TrimSpace(r.Header.Get("Range"))
	return rg == "" || strings.HasPrefix(rg, "bytes=0-")
}

// gateDownload decides whether a file request (download or in-browser view) uses
// up a download, and refuses it when the limit is reached.
//
// On a link with a limit, each browser counts once per file: its first request
// counts and earns a signed 6-hour resume cookie for that file, and its later
// requests for the same file (resuming, seeking in a video, downloading after a
// preview) are free. Every other request counts, so no Range trick and no preview
// fetches a file without using up the limit. On a link without a limit only
// downloads starting at byte 0 count, for the statistics.
func (a *App) gateDownload(w http.ResponseWriter, r *http.Request, s Share, sub string, download bool) bool {
	if s.MaxDL == 0 {
		if download && countsAsDownload(r) {
			a.countDownload(s)
			a.logShare(r, s.ID, "download", sub)
		}
		return true
	}
	if a.resumeOK(r, s, sub) {
		return true
	}
	if s.State != "active" || !a.countDownload(s) {
		a.renderShare(w, http.StatusGone, sharePage{S: s, State: "used-up", Title: s.Label})
		return false
	}
	a.logShare(r, s.ID, "download", sub)
	a.grantResume(w, r, s, sub)
	return true
}

func (a *App) resumeCookie(id string) string { return "br_" + id }

// grantResume lets this browser fetch the rest of a file it was counted for (6 hours).
func (a *App) grantResume(w http.ResponseWriter, r *http.Request, s Share, sub string) {
	exp := strconv.FormatInt(now()+6*3600, 10)
	http.SetCookie(w, &http.Cookie{Name: a.resumeCookie(s.ID), Value: exp + "." + a.sign("resume|"+s.ID+"|"+sub+"|"+exp), Path: "/s/" + s.ID, HttpOnly: true, Secure: a.isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 6 * 3600})
}

func (a *App) resumeOK(r *http.Request, s Share, sub string) bool {
	c, err := r.Cookie(a.resumeCookie(s.ID))
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if e, _ := strconv.ParseInt(exp, 10, 64); !ok || e < now() {
		return false
	}
	return hmacEqual(sig, a.sign("resume|"+s.ID+"|"+sub+"|"+exp))
}

// countDownload atomically consumes one download from a limited share.
func (a *App) countDownload(s Share) bool {
	res, err := a.st.exec(`UPDATE shares SET dl=dl+1 WHERE id=? AND (max_dl=0 OR dl<max_dl)`, s.ID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func previewKind(e Entry) string {
	switch e.Kind {
	case "image":
		switch e.Ext {
		case "jpg", "jpeg", "png", "gif", "webp", "svg", "avif", "bmp":
			return "image"
		}
	case "video":
		switch e.Ext {
		case "mp4", "webm", "m4v", "mov":
			return "video"
		}
	case "audio":
		switch e.Ext {
		case "mp3", "m4a", "ogg", "opus", "wav", "flac", "aac":
			return "audio"
		}
	}
	if e.Ext == "pdf" {
		return "pdf"
	}
	if (e.Kind == "code" || e.Ext == "txt" || e.Ext == "md" || e.Ext == "csv" || e.Ext == "log") && e.Size < 2<<20 {
		return "text"
	}
	return ""
}

func (a *App) renderShare(w http.ResponseWriter, code int, pg sharePage) {
	if pg.MaxUpload == 0 {
		pg.MaxUpload = a.maxUploadBytes()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
	w.WriteHeader(code)
	if err := shareTmpl.ExecuteTemplate(w, "share.html", pg); err != nil {
		log.Printf("share template: %v", err)
	}
}

func (a *App) maxUploadBytes() int64 {
	mb, _ := strconv.ParseInt(a.st.get("max_upload_mb"), 10, 64)
	if mb <= 0 {
		mb = 20480
	}
	return mb << 20
}

// ---- file drop (upload-only links) ----

func (a *App) handleDrop(w http.ResponseWriter, r *http.Request, s Share, action string) {
	dir := a.uploadDir(s) // created by the first upload
	if action == "upload" && r.Method == http.MethodPost {
		n, err := a.receiveUploads(w, r, s, dir, true)
		if err != nil {
			httpErr(w, uploadErrCode(err), err.Error())
			return
		}
		if r.Header.Get("Accept") == "application/json" {
			writeJSON(w, map[string]int{"saved": n})
			return
		}
		if s.Mode == "collection" {
			http.Redirect(w, r, fmt.Sprintf("/s/%s?uploaded=%d", s.ID, n), http.StatusSeeOther)
			return
		}
		a.renderShare(w, 200, sharePage{S: s, Title: s.Label, State: "active", Uploaded: n})
		return
	}
	if action != "" {
		http.NotFound(w, r)
		return
	}
	a.logShare(r, s.ID, "view", "")
	a.renderShare(w, 200, sharePage{S: s, Title: s.Label, State: "active"})
}

func safeFileName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimLeft(strings.TrimSpace(n), ".")
	if n == "" {
		n = "upload"
	}
	if len(n) > 200 {
		ext := filepath.Ext(n)
		n = n[:200-len(ext)] + ext
	}
	return n
}

func createUnique(dir, name string) (*os.File, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, cand), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	return nil, errors.New("too many files with that name")
}

// ---- file serving helpers ----

func (a *App) serveFile(w http.ResponseWriter, r *http.Request, abs string, info os.FileInfo, attachment bool) {
	f, err := a.openNoFollow(abs)
	if err != nil {
		httpErr(w, 404, "not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", safeContentType(w, abs))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private")
	}
	disp := "inline"
	if attachment {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// safeContentType is the type to serve a file named name as. A shared HTML or
// SVG file never runs script on our origin.
func safeContentType(w http.ResponseWriter, name string) string {
	ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	if strings.HasPrefix(ctype, "text/html") || strings.HasPrefix(ctype, "image/svg") || strings.Contains(ctype, "javascript") || strings.Contains(ctype, "xml") {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
		if strings.HasPrefix(ctype, "text/html") || strings.Contains(ctype, "javascript") {
			ctype = "text/plain; charset=utf-8"
		}
	}
	return ctype
}

// zipSource is one top-level entry in a zip: a file or folder on disk, its name in
// the zip, and its library path ("" for files that live in an upload folder).
type zipSource struct{ Abs, Name, Lib string }

// zipAllow returns the permission filter for zipping on behalf of u (nil: everything).
// Folders are entered if u may reach something inside; files need the permission itself.
func zipAllow(u *User, p perm) func(rel string, dir bool) bool {
	if u == nil {
		return func(string, bool) bool { return false }
	}
	if u.Admin {
		return nil
	}
	return func(rel string, dir bool) bool {
		if dir {
			return u.canReach(rel, p)
		}
		return u.can(rel, p)
	}
}

var copyBufs = sync.Pool{New: func() any { b := make([]byte, 256<<10); return &b }}

// streamZipMany writes each source (file or folder) under its name inside one zip.
// It streams straight from disk with no compression (media is already compressed),
// so it runs at disk speed, needs no temporary file and uses almost no memory.
// allow, if set, is checked for every library path before it goes in.
func (a *App) streamZipMany(w http.ResponseWriter, zipName string, srcs []zipSource, allow func(rel string, dir bool) bool) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": safeFileName(zipName) + ".zip"}))
	zw := zip.NewWriter(w)
	defer zw.Close()
	bp := copyBufs.Get().(*[]byte)
	defer copyBufs.Put(bp)
	addFile := func(p, name string, info os.FileInfo) error {
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		hdr.Name = name
		hdr.Method = zip.Store
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := a.openNoFollow(p)
		if err != nil {
			return nil
		}
		// hide os.File's WriteTo so the large buffer is used instead of 32 KB reads
		_, err = io.CopyBuffer(fw, struct{ io.Reader }{f}, *bp)
		f.Close()
		return err
	}
	for _, src := range srcs {
		root, err := os.Lstat(src.Abs)
		if err != nil || root.Mode()&os.ModeSymlink != 0 {
			continue
		}
		check := allow
		if src.Lib == "" {
			check = nil // upload-folder files: no library permissions apply
		}
		if check != nil && !check(src.Lib, root.IsDir()) {
			continue
		}
		if !root.IsDir() {
			if err := addFile(src.Abs, src.Name, root); err != nil {
				return
			}
			continue
		}
		err = filepath.WalkDir(src.Abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if p == src.Abs {
				return nil
			}
			if a.idx.excluded(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, _ := filepath.Rel(src.Abs, p)
			rel = filepath.ToSlash(rel)
			if check != nil && !check(src.Lib+"/"+rel, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			return addFile(p, src.Name+"/"+rel, info)
		})
		if err != nil {
			return
		}
	}
}

func humanSize(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func atoiDef(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}

var _ = sql.ErrNoRows
