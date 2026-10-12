package main

// Users and per-folder permissions.
//
// Admins can do everything. Other users only see what their grants allow:
// a grant gives read / write / share on one library path and everything under it,
// and the most specific grant wins, so a grant without read on a subfolder carves it out.
// Write also needs the folder to be mounted writable into the container, which the
// kernel enforces no matter what Burrow thinks.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sys/unix"
)

// AUTOINCREMENT so a deleted user's id is never handed to a new user, who would
// otherwise inherit the old account's (revoked) share links.
const userSchema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  pw_hash TEXT NOT NULL,
  admin INTEGER NOT NULL DEFAULT 0,
  disabled INTEGER NOT NULL DEFAULT 0,
  created INTEGER NOT NULL,
  totp_secret TEXT NOT NULL DEFAULT '',
  totp_pending TEXT NOT NULL DEFAULT '',
  totp_last INTEGER NOT NULL DEFAULT 0,
  totp_recovery TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS grants (
  user_id INTEGER NOT NULL,
  path TEXT NOT NULL,
  can_read INTEGER NOT NULL DEFAULT 1,
  can_write INTEGER NOT NULL DEFAULT 0,
  can_share INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(user_id, path)
);
CREATE TABLE IF NOT EXISTS favs (user_id INTEGER NOT NULL, path TEXT NOT NULL, created INTEGER, PRIMARY KEY(user_id, path));
`

// oldAdminKeys are the settings rows that held the single admin account before users existed.
var oldAdminKeys = []string{"admin_user", "admin_hash", "totp_enabled", "totp_secret", "totp_pending", "totp_last_step", "totp_recovery"}

// migrateUsers upgrades a single-admin database to the users table (idempotent).
func migrateUsers(db *sql.DB) error {
	if _, err := db.Exec(userSchema); err != nil {
		return err
	}
	for _, q := range []string{
		`ALTER TABLE sessions ADD COLUMN user_id INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE tokens ADD COLUMN user_id INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE tokens ADD COLUMN last_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE shares ADD COLUMN owner_id INTEGER NOT NULL DEFAULT 1`,
	} {
		_, _ = db.Exec(q) // "duplicate column" means already applied
	}
	for _, q := range []string{
		`CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS tokens_user ON tokens(user_id)`,
		`CREATE INDEX IF NOT EXISTS shares_owner ON shares(owner_id)`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	get := func(k string) string {
		var v string
		_ = db.QueryRow(`SELECT v FROM settings WHERE k=?`, k).Scan(&v)
		return v
	}
	u, h := get("admin_user"), get("admin_hash")
	if u == "" || h == "" {
		return nil
	}
	// The old admin becomes user 1, so existing sessions, tokens and links (which
	// the ALTERs above defaulted to user 1) keep working.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	_ = tx.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	if n == 0 {
		rec := get("totp_recovery")
		if rec == "" {
			rec = "[]"
		}
		secret := ""
		if get("totp_enabled") == "1" {
			secret = get("totp_secret")
		}
		last, _ := strconv.ParseInt(get("totp_last_step"), 10, 64)
		if _, err := tx.Exec(`INSERT INTO users(id,username,pw_hash,admin,created,totp_secret,totp_last,totp_recovery) VALUES(1,?,?,1,?,?,?,?)`, u, h, now(), secret, last, rec); err != nil {
			return err
		}
		var hasFavs int
		_ = tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='favorites'`).Scan(&hasFavs)
		if hasFavs > 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO favs(user_id,path,created) SELECT 1,path,created FROM favorites`); err != nil {
				return err
			}
		}
	}
	// don't leave a second copy of the password hash and 2FA secret behind
	for _, k := range oldAdminKeys {
		if _, err := tx.Exec(`DELETE FROM settings WHERE k=?`, k); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE IF EXISTS favorites`); err != nil {
		return err
	}
	return tx.Commit()
}

type Grant struct {
	Path  string `json:"path"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
	Share bool   `json:"share"`
}

type User struct {
	ID       int64   `json:"id"`
	Name     string  `json:"username"`
	Admin    bool    `json:"admin"`
	Disabled bool    `json:"disabled"`
	Created  int64   `json:"created"`
	TwoFA    bool    `json:"two_factor"`
	Grants   []Grant `json:"grants"`
	pwHash   string
	totp     string
	pending  string
	recovery []string
}

type perm int

const (
	permRead perm = iota
	permWrite
	permShare
)

// has reports whether the grant gives p. Write and share both need read.
func (g Grant) has(p perm) bool {
	switch p {
	case permWrite:
		return g.Read && g.Write
	case permShare:
		return g.Read && g.Share
	}
	return g.Read
}

func (a *App) loadUser(id int64) (*User, error) {
	u := &User{ID: id}
	var rec string
	err := a.st.db.QueryRow(`SELECT username,pw_hash,admin,disabled,created,totp_secret,totp_pending,totp_recovery FROM users WHERE id=?`, id).
		Scan(&u.Name, &u.pwHash, &u.Admin, &u.Disabled, &u.Created, &u.totp, &u.pending, &rec)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(rec), &u.recovery)
	u.TwoFA = u.totp != ""
	u.Grants = []Grant{}
	rows, err := a.st.db.Query(`SELECT path,can_read,can_write,can_share FROM grants WHERE user_id=? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Path, &g.Read, &g.Write, &g.Share); err != nil {
			return nil, err
		}
		u.Grants = append(u.Grants, g)
	}
	return u, rows.Err()
}

// activeUser loads a user who exists and isn't disabled, or returns nil.
func (a *App) activeUser(id int64) *User {
	u, err := a.loadUser(id)
	if err != nil || u.Disabled {
		return nil
	}
	return u
}

func (a *App) userByName(name string) (*User, error) {
	var id int64
	if err := a.st.db.QueryRow(`SELECT id FROM users WHERE username=? COLLATE NOCASE`, strings.TrimSpace(name)).Scan(&id); err != nil {
		return nil, err
	}
	return a.loadUser(id)
}

func (a *App) userCount() int {
	var n int
	_ = a.st.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n
}

// dummyHash keeps sign-in timing the same whether or not the username exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("burrow-timing-equalizer"), 12)

func (a *App) checkPassword(name, pass string) (*User, bool) {
	u, err := a.userByName(name)
	if err != nil || u.Disabled {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pass))
		return nil, false
	}
	return u, bcrypt.CompareHashAndPassword([]byte(u.pwHash), []byte(pass)) == nil
}

func checkNewPassword(pass string) error {
	if len(pass) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(pass) > 72 {
		return errors.New("password must be at most 72 characters")
	}
	return nil
}

func (a *App) setPassword(id int64, pass string) error {
	if err := checkNewPassword(pass); err != nil {
		return err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		return err
	}
	_, err = a.st.exec(`UPDATE users SET pw_hash=? WHERE id=?`, string(h), id)
	return err
}

func (a *App) createUser(name, pass string, admin bool) (*User, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "/\\:@ \t\r\n") {
		return nil, errors.New("username: 1-64 characters, no spaces, slashes, colons or @")
	}
	if err := checkNewPassword(pass); err != nil {
		return nil, err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), 12)
	if err != nil {
		return nil, err
	}
	res, err := a.st.exec(`INSERT INTO users(username,pw_hash,admin,created) VALUES(?,?,?,?)`, name, string(h), admin, now())
	if err != nil {
		return nil, errors.New("that username is taken")
	}
	id, _ := res.LastInsertId()
	return a.loadUser(id)
}

// grantFor returns the most specific grant covering rel.
func (u *User) grantFor(rel string) (Grant, bool) {
	best, found := Grant{}, false
	for _, g := range u.Grants {
		if rel == g.Path || strings.HasPrefix(rel, g.Path+"/") {
			if !found || len(g.Path) > len(best.Path) {
				best, found = g, true
			}
		}
	}
	return best, found
}

// can reports whether the user may do p on rel itself.
func (u *User) can(rel string, p perm) bool {
	if u == nil || u.Disabled {
		return false
	}
	if u.Admin {
		return true
	}
	g, ok := u.grantFor(rel)
	return ok && g.has(p)
}

// canReach reports whether the user may do p on rel or on something inside it, so
// rel may be opened to walk down to it (the listing is then filtered). "" is the library.
func (u *User) canReach(rel string, p perm) bool {
	if u.can(rel, p) {
		return true
	}
	if u == nil || u.Disabled {
		return false
	}
	for _, g := range u.Grants {
		if g.has(p) && (rel == "" || strings.HasPrefix(g.Path, rel+"/")) {
			return true
		}
	}
	return false
}

// scope returns an SQL condition on col (a library-path column) that is true exactly
// where u.can(path, p). For each grant that gives p, the path is inside it and not
// inside a more specific grant that doesn't. Since the most specific covering grant
// decides, this is exact, including an allow inside a carve-out inside an allow.
func (u *User) scope(col string, p perm) (string, []any) {
	if u == nil || u.Disabled {
		return "0", nil
	}
	if u.Admin {
		return "1", nil
	}
	inside := func(g string) (string, []any) {
		return "(" + col + " = ? OR (" + col + " >= ? AND " + col + " < ?))", []any{g, g + "/", g + "0"}
	}
	var ors []string
	var args []any
	for _, g := range u.Grants {
		if !g.has(p) {
			continue
		}
		cond, cargs := inside(g.Path)
		var carve []string
		for _, d := range u.Grants {
			if !d.has(p) && strings.HasPrefix(d.Path, g.Path+"/") {
				dc, da := inside(d.Path)
				carve = append(carve, dc)
				cargs = append(cargs, da...)
			}
		}
		if len(carve) > 0 {
			cond = "(" + cond + " AND NOT (" + strings.Join(carve, " OR ") + "))"
		}
		ors = append(ors, cond)
		args = append(args, cargs...)
	}
	if len(ors) == 0 {
		return "0", nil
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

// ---- request context ----

type ctxKey struct{}
type authInfo struct {
	user     *User
	viaAPI   bool // bearer token or paired device, not a browser session
	tokenID  int64
	deviceID string // set for requests from a paired device
}

func withAuth(r *http.Request, ai authInfo) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, ai))
}

func authOf(r *http.Request) authInfo {
	ai, _ := r.Context().Value(ctxKey{}).(authInfo)
	return ai
}

func userOf(r *http.Request) *User { return authOf(r).user }

// resolveFor is resolve plus the user's permission for the action.
func (a *App) resolveFor(u *User, p string, pm perm) (string, string, error) {
	abs, rel, err := a.resolve(p)
	if err != nil {
		return "", "", err
	}
	if !u.can(rel, pm) {
		return "", "", errNotAllowed
	}
	if pm == permWrite && !mountWritable(abs) {
		return "", "", errReadOnly
	}
	return abs, rel, nil
}

var errReadOnly = errors.New("this folder is mounted read-only")

// mountWritable asks the kernel; a read-only bind mount answers EROFS.
var mountWritable = func(abs string) bool {
	p := abs
	for {
		if _, err := os.Stat(p); err == nil {
			return unix.Access(p, unix.W_OK) == nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// ---- admin user management ----

func (a *App) apiUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(`SELECT id FROM users ORDER BY username`)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []*User{}
	for _, id := range ids {
		if u, err := a.loadUser(id); err == nil {
			out = append(out, u)
		}
	}
	writeJSON(w, out)
}

func (a *App) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username, Password string
		Admin              bool
		Grants             []Grant
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	grants, err := a.cleanGrants(in.Grants)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	u, err := a.createUser(in.Username, in.Password, in.Admin)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if err := a.saveGrants(u.ID, grants); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	u, _ = a.loadUser(u.ID)
	writeJSON(w, u)
}

func (a *App) adminCount() int {
	var n int
	_ = a.st.db.QueryRow(`SELECT count(*) FROM users WHERE admin=1 AND disabled=0`).Scan(&n)
	return n
}

func (a *App) apiPatchUser(w http.ResponseWriter, r *http.Request) {
	// one change at a time, so two admins demoting each other can't both pass the
	// "keep one active admin" check
	a.adminMu.Lock()
	defer a.adminMu.Unlock()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u, err := a.loadUser(id)
	if err != nil {
		httpErr(w, 404, "user not found")
		return
	}
	var in struct {
		Password *string
		Admin    *bool
		Disabled *bool
		Reset2FA bool `json:"reset_2fa"`
		Grants   *[]Grant
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	// validate everything before changing anything
	var grants []Grant
	if in.Grants != nil {
		if grants, err = a.cleanGrants(*in.Grants); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
	}
	if in.Password != nil {
		if err := checkNewPassword(*in.Password); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
	}
	losingAdmin := u.Admin && !u.Disabled && ((in.Admin != nil && !*in.Admin) || (in.Disabled != nil && *in.Disabled))
	if losingAdmin && a.adminCount() <= 1 {
		httpErr(w, 400, "keep at least one active admin")
		return
	}
	if in.Password != nil {
		// an admin reset is account recovery: end every session and API token
		if err := a.setPassword(id, *in.Password); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		a.dropSessions(id)
		_, _ = a.st.exec(`DELETE FROM tokens WHERE user_id=?`, id)
		a.revokeDevices(id)
	}
	if in.Admin != nil {
		_, _ = a.st.exec(`UPDATE users SET admin=? WHERE id=?`, *in.Admin, id)
	}
	if in.Disabled != nil {
		// Sign-in, sessions and tokens all refuse a disabled user; tokens are kept
		// so turning the account back on restores its MCP clients.
		_, _ = a.st.exec(`UPDATE users SET disabled=? WHERE id=?`, *in.Disabled, id)
		if *in.Disabled {
			a.dropSessions(id)
		}
	}
	if in.Reset2FA {
		_, _ = a.st.exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0, totp_recovery='[]' WHERE id=?`, id)
	}
	if in.Grants != nil {
		if err := a.saveGrants(id, grants); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	u, _ = a.loadUser(id)
	writeJSON(w, u)
}

func (a *App) apiDeleteUser(w http.ResponseWriter, r *http.Request) {
	a.adminMu.Lock()
	defer a.adminMu.Unlock()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u, err := a.loadUser(id)
	if err != nil {
		httpErr(w, 404, "user not found")
		return
	}
	if id == userOf(r).ID {
		httpErr(w, 400, "you can't delete yourself")
		return
	}
	if u.Admin && !u.Disabled && a.adminCount() <= 1 {
		httpErr(w, 400, "keep at least one active admin")
		return
	}
	a.st.wmu.Lock()
	defer a.st.wmu.Unlock()
	tx, err := a.st.db.Begin()
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	// Links are turned off rather than deleted so their history stays visible to admins.
	for _, q := range []string{`DELETE FROM grants WHERE user_id=?`, `DELETE FROM favs WHERE user_id=?`, `DELETE FROM sessions WHERE user_id=?`, `DELETE FROM tokens WHERE user_id=?`, `DELETE FROM devices WHERE user_id=?`, `UPDATE shares SET revoked=1 WHERE owner_id=?`, `DELETE FROM users WHERE id=?`} {
		if _, err := tx.Exec(q, id); err != nil {
			httpErr(w, 500, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	a.dropDeviceSessions("", id)
	writeJSON(w, map[string]bool{"ok": true})
}

// cleanGrants normalizes grant paths and checks each one is a real library path,
// so a typo doesn't silently grant nothing.
func (a *App) cleanGrants(gs []Grant) ([]Grant, error) {
	if len(gs) > 200 {
		return nil, errors.New("too many folder grants (200 max)")
	}
	clean := map[string]Grant{}
	for _, g := range gs {
		_, rel, err := a.resolve(g.Path)
		if err != nil {
			return nil, errors.New("not a library folder: " + g.Path)
		}
		if g.Write || g.Share {
			g.Read = true // write and share imply read
		}
		g.Path = rel
		clean[rel] = g
	}
	out := make([]Grant, 0, len(clean))
	for _, g := range clean {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (a *App) saveGrants(id int64, gs []Grant) error {
	a.st.wmu.Lock()
	defer a.st.wmu.Unlock()
	tx, err := a.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM grants WHERE user_id=?`, id); err != nil {
		return err
	}
	for _, g := range gs {
		if _, err := tx.Exec(`INSERT INTO grants(user_id,path,can_read,can_write,can_share) VALUES(?,?,?,?,?)`, id, g.Path, g.Read, g.Write, g.Share); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) dropSessions(id int64) { _, _ = a.st.exec(`DELETE FROM sessions WHERE user_id=?`, id) }
