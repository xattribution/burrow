package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
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
	if !a.trusted(host) {
		a.noteUntrustedProxy(host, r)
		return host
	}
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		parts := strings.Split(xf, ",")
		// walk right-to-left past our own proxies; a malformed entry voids the header
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				return host
			}
			if !a.trusted(ip.String()) || i == 0 {
				return ip.String()
			}
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return host
}

// noteUntrustedProxy logs, once per address, a proxy that sends forwarded headers
// without being trusted: Burrow then sees every visitor as that proxy, so rate
// limits are shared and logs show the proxy instead of visitors.
func (a *App) noteUntrustedProxy(peer string, r *http.Request) {
	if r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("X-Real-IP") == "" {
		return
	}
	if ip := net.ParseIP(peer); ip == nil || !(ip.IsPrivate() || ip.IsLoopback()) {
		return // a public address sending these headers is a client, not our proxy
	}
	a.noteMu.Lock()
	defer a.noteMu.Unlock()
	if a.noted == nil {
		a.noted = map[string]bool{}
	}
	if a.noted[peer] || len(a.noted) > 100 {
		return
	}
	a.noted[peer] = true
	log.Printf("requests from %s carry X-Forwarded-For, but %s isn't in BURROW_TRUSTED_PROXIES. If it is your reverse proxy, add it so Burrow sees visitors' real addresses (rate limits and logs use them).", peer, peer)
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

// ---- rate limiting for guesses (passwords, codes, link ids) ----

// limiter counts attempts per key in a sliding window. take records an attempt
// and checks the limit in one step, before the guess is evaluated, so parallel
// requests can't all slip in under the limit; a successful attempt is refunded.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

// take records an attempt for key and reports whether it is within max per window.
// An attempt over the limit is not recorded, so waiting out the window always works.
func (l *limiter) take(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-window)
	h := l.hits[key]
	i := 0
	for ; i < len(h) && h[i].Before(cut); i++ {
	}
	h = h[i:]
	if len(l.hits) > 50000 { // sweep stale keys under heavy spraying
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	if len(h) >= max {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	return true
}

// refund removes the most recent attempt (it succeeded, so it shouldn't count).
func (l *limiter) refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h := l.hits[key]; len(h) > 0 {
		if h = h[:len(h)-1]; len(h) == 0 {
			delete(l.hits, key)
		} else {
			l.hits[key] = h
		}
	}
}

// limitIP is the rate-limit identity of a client address: the address itself, or
// its /64 for IPv6, since one IPv6 user can cycle through a whole /64.
func limitIP(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if p.To4() == nil {
		return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return p.String()
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

// tokenAuth is a bearer token that checked out.
type tokenAuth struct {
	id    int64
	scope TokenScope
}

// tokenUser checks a bearer token (MCP clients, scripts) and returns its user,
// narrowed to what the token's scope allows.
func (a *App) tokenUser(r *http.Request) (*User, *tokenAuth) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(tok) == "" {
		return nil, nil
	}
	var id, uid, last int64
	var lastIP, scope string
	if err := a.st.db.QueryRow(`SELECT id,user_id,coalesce(last_used,0),last_ip,scope FROM tokens WHERE hash=?`, sha(strings.TrimSpace(tok))).Scan(&id, &uid, &last, &lastIP, &scope); err != nil {
		return nil, nil
	}
	u := a.activeUser(uid)
	if u == nil {
		return nil, nil
	}
	// Record use at most once a minute: clients like MCP and the phone app make many
	// requests, and a database write on each one would serialize them for nothing.
	if ip := a.clientIP(r); now()-last >= 60 || ip != lastIP {
		_, _ = a.st.exec(`UPDATE tokens SET last_used=?, last_ip=? WHERE id=?`, now(), ip, id)
	}
	ta := &tokenAuth{id: id, scope: parseScope(scope)}
	return a.narrow(u, ta.scope), ta
}

// apiTokenUser is tokenUser for the REST API: tokens made for MCP only don't count.
func (a *App) apiTokenUser(r *http.Request) (*User, *tokenAuth) {
	if u, ta := a.tokenUser(r); u != nil && ta.scope.API {
		return u, ta
	}
	return nil, nil
}

// requireUser guards the normal API for any signed-in user or token. Cookie-authenticated
// requests that change state must carry X-Burrow: 1, which a cross-site form cannot send.
func (a *App) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, ta := a.tokenUser(r); u != nil {
			if !ta.scope.API {
				httpErr(w, http.StatusForbidden, "this token is for MCP only (Settings > MCP and API tokens)")
				return
			}
			next(w, withAuth(r, authInfo{user: u, viaAPI: true, tokenID: ta.id}))
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
