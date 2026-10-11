package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "burrow_session"
const sessionTTL = 30 * 24 * time.Hour

// clientIP trusts X-Forwarded-For only when the direct peer is a trusted proxy.
func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if a.trusted(host) {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			parts := strings.Split(xf, ",")
			// walk right-to-left past our own proxies
			for i := len(parts) - 1; i >= 0; i-- {
				ip := strings.TrimSpace(parts[i])
				if ip != "" && (!a.trusted(ip) || i == 0) {
					return ip
				}
			}
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	return host
}

func (a *App) trusted(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, n := range a.proxyNets {
		if n.Contains(p) {
			return true
		}
	}
	return false
}

// isHTTPS decides the cookie Secure flag per request, so logging in over plain
// http on the LAN works while the public https hostname still gets Secure cookies.
func (a *App) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return a.trusted(host) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ---- rate limiting for password guesses (login and share passwords) ----

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// allow reports whether key may try again: at most max failures per window.
func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-window)
	h := l.hits[key]
	i := 0
	for ; i < len(h) && h[i].Before(cut); i++ {
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = h
	}
	if len(l.hits) > 50000 { // sweep stale keys under heavy spraying
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return len(h) < max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	l.hits[key] = append(l.hits[key], time.Now())
	l.mu.Unlock()
}

func (l *limiter) clear(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

// ---- accounts and sessions ----

// setupDone also needs a user, so a database whose accounts were all removed by hand
// goes back to first-launch setup instead of locking everyone out.
func (a *App) setupDone() bool { return a.st.get("setup_done") == "1" && a.userCount() > 0 }

func (a *App) newSession(w http.ResponseWriter, r *http.Request, uid int64) {
	tok := randToken(32)
	exp := time.Now().Add(sessionTTL)
	_, _ = a.st.exec(`INSERT INTO sessions(id,created,expires,ip,ua,user_id) VALUES(?,?,?,?,?,?)`, sha(tok), now(), exp.Unix(), a.clientIP(r), trunc(r.UserAgent(), 200), uid)
	_, _ = a.st.exec(`DELETE FROM sessions WHERE expires < ?`, now())
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true, Secure: a.isHTTPS(r), SameSite: http.SameSiteLaxMode})
}

func (a *App) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = a.st.exec(`DELETE FROM sessions WHERE id=?`, sha(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.isHTTPS(r), SameSite: http.SameSiteLaxMode})
}

func (a *App) sessionUser(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	var exp, uid int64
	if err := a.st.db.QueryRow(`SELECT expires,user_id FROM sessions WHERE id=?`, sha(c.Value)).Scan(&exp, &uid); err != nil || exp <= now() {
		return nil
	}
	return a.activeUser(uid)
}

// tokenUser checks a bearer token (MCP clients, scripts, paired phones).
func (a *App) tokenUser(r *http.Request) (*User, int64) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(tok) == "" {
		return nil, 0
	}
	var id, uid, last int64
	var lastIP string
	if err := a.st.db.QueryRow(`SELECT id,user_id,coalesce(last_used,0),last_ip FROM tokens WHERE hash=?`, sha(strings.TrimSpace(tok))).Scan(&id, &uid, &last, &lastIP); err != nil {
		return nil, 0
	}
	u := a.activeUser(uid)
	if u == nil {
		return nil, 0
	}
	// Record use at most once a minute: clients like MCP and the phone app make many
	// requests, and a database write on each one would serialize them for nothing.
	if ip := a.clientIP(r); now()-last >= 60 || ip != lastIP {
		_, _ = a.st.exec(`UPDATE tokens SET last_used=?, last_ip=? WHERE id=?`, now(), ip, id)
	}
	return u, id
}

// requireUser guards the normal API for any signed-in user or token. Cookie-authenticated
// requests that change state must carry X-Burrow: 1, which a cross-site form cannot send.
func (a *App) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, tid := a.tokenUser(r); u != nil {
			next(w, withAuth(r, authInfo{user: u, viaAPI: true, tokenID: tid}))
			return
		}
		u := a.sessionUser(r)
		if u == nil {
			httpErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Burrow") != "1" {
			httpErr(w, http.StatusForbidden, "missing X-Burrow header")
			return
		}
		next(w, withAuth(r, authInfo{user: u}))
	}
}

// requireSession is for account-level changes (password, 2FA, tokens, devices):
// a token alone can't do these, so a leaked token can't lock you out or mint more tokens.
func (a *App) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.sessionUser(r)
		if u == nil {
			httpErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Burrow") != "1" {
			httpErr(w, http.StatusForbidden, "missing X-Burrow header")
			return
		}
		next(w, withAuth(r, authInfo{user: u}))
	}
}

// requireAdmin: admin-only actions that an admin's API token may also do (rescan).
func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return a.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !userOf(r).Admin {
			httpErr(w, http.StatusForbidden, "admins only")
			return
		}
		next(w, r)
	})
}

// requireAdminSession: server settings and user management, browser session of an admin only.
func (a *App) requireAdminSession(next http.HandlerFunc) http.HandlerFunc {
	return a.requireSession(func(w http.ResponseWriter, r *http.Request) {
		if !userOf(r).Admin {
			httpErr(w, http.StatusForbidden, "admins only")
			return
		}
		next(w, r)
	})
}

// sign makes a short HMAC used for share-password cookies.
func (a *App) sign(msg string) string {
	m := hmac.New(sha256.New, []byte(a.st.get("secret")))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func hmacEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
