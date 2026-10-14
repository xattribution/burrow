package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
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
	"sync/atomic"
	"time"
)

type App struct {
	cfg       Config
	st        *Store
	idx       *Indexer
	limiter   *limiter
	proxyNets []*net.IPNet
	thumbSem  chan struct{}
	setupKey  string
	setupMu   sync.Mutex
	shareMu   sync.Mutex  // serializes share creation (upload folder names)
	publicUI  atomic.Bool // serve the web UI and its API on the public port too
	publicMCP atomic.Bool // serve /mcp on the public port too
	adminMu   sync.Mutex  // serializes changes that could leave no active admin
	searchSem chan struct{}
	keyMu     sync.Mutex
	noteMu    sync.Mutex
	noted     map[string]bool
	certs     *certSource  // for the fingerprint phones pin
	dev       devState     // pairing codes and device sessions
	devMux    http.Handler // what a paired device may call, inside the channel
	upLocks   sync.Map     // upload id -> *sync.Mutex
	remotes   *remoteSet   // connected NAS, by library name
	jobs      jobSet       // copies between libraries
}

func newApp(cfg Config, st *Store) (*App, error) {
	a := &App{cfg: cfg, st: st, limiter: newLimiter(), thumbSem: make(chan struct{}, 3), searchSem: make(chan struct{}, 4)}
	for _, c := range cfg.TrustedProxies {
		if !strings.Contains(c, "/") {
			if strings.Contains(c, ":") {
				c += "/128"
			} else {
				c += "/32"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		a.proxyNets = append(a.proxyNets, n)
	}
	a.idx = newIndexer(cfg, st)
	a.devMux = a.deviceRoutes()
	a.loadRemotes()
	a.publicUI.Store(st.get("public_ui") == "1")
	a.publicMCP.Store(st.get("public_mcp") == "1")
	if !a.setupDone() {
		a.currentSetupKey()
	}
	return a, nil
}

// currentSetupKey returns the first-launch setup key, making (and logging) one if
// there is none yet, so setup is never possible with an empty key.
func (a *App) currentSetupKey() string {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	if a.setupKey == "" {
		a.setupKey = randSlug(12)
		log.Printf("first launch: open the web UI to finish setup. Setup key: %s", a.setupKey)
	}
	return a.setupKey
}

// sameSite guards the routes that work without a session (sign-in, sign-out,
// setup) the way requireUser guards the rest: a page on another site can't send
// the X-Burrow header, so it can't sign a visitor in or out, or run setup.
func sameSite(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Burrow") != "1" {
			httpErr(w, http.StatusForbidden, "missing X-Burrow header")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonEncode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(v)
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	// web UI
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /assets/{file}", a.handleAsset)
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("file", "favicon.svg")
		a.handleAsset(w, r)
	})

	// public shares
	mux.HandleFunc("/s/{id}", a.handleShare)
	mux.HandleFunc("/s/{id}/{action}", a.handleShare)

	// MCP
	mux.HandleFunc("/mcp", a.handleMCP)

	// session / setup
	mux.HandleFunc("GET /api/state", a.apiState)
	mux.HandleFunc("POST /api/setup", sameSite(a.apiSetup))
	mux.HandleFunc("POST /api/login", sameSite(a.apiLogin))
	mux.HandleFunc("POST /api/logout", sameSite(func(w http.ResponseWriter, r *http.Request) {
		a.endSession(w, r)
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/roots/available", a.apiRootsAvailable)

	us := a.requireUser             // any signed-in user or API token; results follow their grants
	adm := a.requireAdmin           // admins, by session or token
	ses := a.requireSession         // account changes need a browser session, not a token
	admSes := a.requireAdminSession // server settings and user management
	mux.HandleFunc("POST /api/2fa/start", ses(a.api2FAStart))
	mux.HandleFunc("POST /api/2fa/enable", ses(a.api2FAEnable))
	mux.HandleFunc("POST /api/2fa/disable", ses(a.api2FADisable))
	mux.HandleFunc("GET /api/ls", us(a.apiLs))
	mux.HandleFunc("GET /api/search", us(a.apiSearch))
	mux.HandleFunc("GET /api/info", us(a.apiInfo))
	mux.HandleFunc("GET /api/raw", us(a.apiRaw))
	mux.HandleFunc("GET /api/thumb", us(a.apiThumb))
	mux.HandleFunc("GET /api/zip", us(a.apiZip))
	mux.HandleFunc("GET /api/changes", us(a.apiChanges))
	mux.HandleFunc("GET /api/status", us(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.statusFor(userOf(r))) }))
	mux.HandleFunc("GET /api/stats", us(a.apiStats))
	mux.HandleFunc("POST /api/rescan", adm(func(w http.ResponseWriter, r *http.Request) {
		a.idx.Rescan()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /api/settings", us(a.apiGetSettings))
	mux.HandleFunc("PUT /api/settings", admSes(a.apiPutSettings))
	mux.HandleFunc("POST /api/password", ses(a.apiPassword))
	mux.HandleFunc("GET /api/tokens", ses(a.apiTokens))
	mux.HandleFunc("POST /api/tokens", ses(a.apiNewToken))
	mux.HandleFunc("DELETE /api/tokens/{id}", ses(a.apiDelToken))
	mux.HandleFunc("PATCH /api/tokens/{id}", ses(a.apiPatchToken))
	mux.HandleFunc("GET /api/tokens/tools", ses(a.apiTokenTools))
	mux.HandleFunc("GET /api/remotes", admSes(a.apiRemotes))
	mux.HandleFunc("POST /api/remotes", admSes(a.apiAddRemote))
	mux.HandleFunc("POST /api/remotes/shares", admSes(a.apiRemoteShares))
	mux.HandleFunc("PATCH /api/remotes/{id}", admSes(a.apiPatchRemote))
	mux.HandleFunc("DELETE /api/remotes/{id}", admSes(a.apiDeleteRemote))
	mux.HandleFunc("GET /api/users", admSes(a.apiUsers))
	mux.HandleFunc("POST /api/users", admSes(a.apiCreateUser))
	mux.HandleFunc("PATCH /api/users/{id}", admSes(a.apiPatchUser))
	mux.HandleFunc("DELETE /api/users/{id}", admSes(a.apiDeleteUser))
	mux.HandleFunc("GET /api/favorites", us(a.apiFavorites))
	mux.HandleFunc("POST /api/favorites", us(a.apiAddFavorite))
	mux.HandleFunc("DELETE /api/favorites", us(a.apiDelFavorite))
	mux.HandleFunc("GET /api/shares", us(a.apiShares))
	mux.HandleFunc("POST /api/shares", us(a.apiCreateShare))
	mux.HandleFunc("PATCH /api/shares/{id}", us(a.apiPatchShare))
	mux.HandleFunc("DELETE /api/shares/{id}", us(a.apiDeleteShare))
	mux.HandleFunc("GET /api/shares/{id}/items", us(a.apiCollection))
	mux.HandleFunc("POST /api/shares/{id}/items", us(a.apiAddItems))
	mux.HandleFunc("DELETE /api/shares/{id}/items", us(a.apiRemoveItem))
	mux.HandleFunc("POST /api/shares/{id}/upload", us(a.apiOwnerUpload))
	mux.HandleFunc("DELETE /api/shares/{id}/uploads", us(a.apiDeleteUpload))
	mux.HandleFunc("GET /api/shares/{id}/events", us(a.apiShareEvents))
	mux.HandleFunc("GET /api/events", us(a.apiEvents))
	mux.HandleFunc("GET /api/shares/{id}/qr.png", us(a.apiShareQR))

	// changing the library (write grant and a writable mount)
	for _, rt := range a.writeRoutes() {
		mux.HandleFunc(rt.pattern, us(rt.h))
	}

	// paired phones: pairing and the device list need a browser session
	mux.HandleFunc("POST /api/devices/pair", ses(a.apiPairStart))
	mux.HandleFunc("GET /api/devices/pair/{id}", ses(a.apiPairStatus))
	mux.HandleFunc("DELETE /api/devices/pair/{id}", ses(a.apiPairCancel))
	mux.HandleFunc("GET /api/devices", ses(a.apiDevices))
	mux.HandleFunc("PATCH /api/devices/{id}", ses(a.apiRenameDevice))
	mux.HandleFunc("DELETE /api/devices/{id}", ses(a.apiRevokeDevice))
	a.channelRoutes(mux)
	return a.secure(mux)
}

// channelRoutes are the phone channel's endpoints, on both ports. They carry
// their own authentication (devchan.go), so the admin network list doesn't apply.
func (a *App) channelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/d/pair", a.handlePair)
	mux.HandleFunc("POST /api/d/hello", a.handleHello)
	mux.HandleFunc("POST /api/d", a.handleTunnel)
}

// deviceRoutes is what a paired device can call through the channel: the same
// handlers the web UI uses, acting as the device's user. Account and server
// administration stay in the web UI.
func (a *App) deviceRoutes() http.Handler {
	m := http.NewServeMux()
	for _, r := range []struct {
		pattern string
		h       http.HandlerFunc
	}{
		{"GET /api/ls", a.apiLs},
		{"GET /api/search", a.apiSearch},
		{"GET /api/info", a.apiInfo},
		{"GET /api/raw", a.apiRaw},
		{"GET /api/thumb", a.apiThumb},
		{"GET /api/zip", a.apiZip},
		{"GET /api/changes", a.apiChanges},
		{"GET /api/settings", a.apiGetSettings},
		{"GET /api/favorites", a.apiFavorites},
		{"POST /api/favorites", a.apiAddFavorite},
		{"DELETE /api/favorites", a.apiDelFavorite},
		{"GET /api/shares", a.apiShares},
		{"POST /api/shares", a.apiCreateShare},
		{"PATCH /api/shares/{id}", a.apiPatchShare},
		{"DELETE /api/shares/{id}", a.apiDeleteShare},
		{"GET /api/shares/{id}/items", a.apiCollection},
		{"POST /api/shares/{id}/items", a.apiAddItems},
		{"DELETE /api/shares/{id}/items", a.apiRemoveItem},
		{"GET /api/shares/{id}/events", a.apiShareEvents},
		{"GET /api/shares/{id}/qr.png", a.apiShareQR},
		{"GET /api/device/me", a.apiDeviceMe},
		{"PATCH /api/device/me", a.apiDeviceUpdate},
		{"DELETE /api/device/me", a.apiDeviceUnpair},
		{"POST /api/device/link-token", a.apiDeviceLinkToken},
		{"GET /api/remotes", deviceAdmin(a.apiRemotes)},
		{"POST /api/remotes", deviceAdmin(a.apiAddRemote)},
		{"POST /api/remotes/shares", deviceAdmin(a.apiRemoteShares)},
	} {
		m.HandleFunc(r.pattern, r.h)
	}
	for _, rt := range a.writeRoutes() {
		m.HandleFunc(rt.pattern, rt.h)
	}
	return m
}

type route struct {
	pattern string
	h       http.HandlerFunc
}

// writeRoutes change the library or read it for syncing; the web UI and paired
// devices both use them.
func (a *App) writeRoutes() []route {
	return []route{
		{"POST /api/fs/mkdir", a.apiMkdir},
		{"POST /api/fs/move", a.apiMove},
		{"POST /api/fs/delete", a.apiDelete},
		{"GET /api/trash", a.apiTrash},
		{"POST /api/trash/{id}/restore", a.apiRestore},
		{"DELETE /api/trash/{id}", a.apiPurge},
		{"POST /api/uploads", a.apiUploadStart},
		{"GET /api/uploads/{id}", a.apiUploadStatus},
		{"PUT /api/uploads/{id}", a.apiUploadChunk},
		{"POST /api/uploads/{id}/finish", a.apiUploadFinish},
		{"DELETE /api/uploads/{id}", a.apiUploadCancel},
		{"GET /api/jobs", a.apiJobs},
		{"GET /api/jobs/{id}", a.apiJob},
		{"GET /api/sync/list", a.apiSyncList},
		{"GET /api/sync/changed", a.apiSyncChanged},
		{"GET /api/sync/hash", a.apiSyncHash},
	}
}

func (a *App) apiShareEvents(w http.ResponseWriter, r *http.Request) {
	ev, err := a.shareEvents(userOf(r), r.PathValue("id"), atoiDef(r.URL.Query().Get("limit"), 200))
	if err != nil {
		httpErr(w, 404, "share not found")
		return
	}
	writeJSON(w, ev)
}

func (a *App) apiEvents(w http.ResponseWriter, r *http.Request) {
	ev, err := a.shareEvents(userOf(r), "", atoiDef(r.URL.Query().Get("limit"), 300))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, ev)
}

func (a *App) apiShareQR(w http.ResponseWriter, r *http.Request) {
	s, err := a.ownedShare(userOf(r), r.PathValue("id"))
	if err != nil {
		httpErr(w, 404, "share not found")
		return
	}
	png, err := qrPNG(a.baseURL(r)+"/s/"+s.ID, atoiDef(r.URL.Query().Get("size"), 512))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="share-`+s.ID+`-qr.png"`)
	}
	_, _ = w.Write(png)
}

// secure adds baseline headers and an optional admin IP allow-list.
func (a *App) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		baseHeaders(w, r)
		p := r.URL.Path
		public := strings.HasPrefix(p, "/s/") || p == "/healthz" || strings.HasPrefix(p, "/assets/") || p == "/favicon.svg" ||
			p == "/api/d" || strings.HasPrefix(p, "/api/d/")
		if !public && !a.adminAllowed(r) {
			http.Error(w, "not available from this network", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func baseHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	if r.TLS != nil {
		// no includeSubDomains: other sites under the same domain aren't Burrow's to decide
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// publicRoutes is what the public port serves: share links and the few assets
// their pages use, and the phone channel (/api/d, which authenticates and
// encrypts every request itself). The web UI with its API, and MCP, are only added when an admin
// turns them on (Settings > Public port); everything else is 404, so the admin
// side isn't reachable from the internet at all by default.
func (a *App) publicRoutes() http.Handler {
	full := a.routes()
	pub := http.NewServeMux()
	pub.HandleFunc("/s/{id}", a.handleShare)
	pub.HandleFunc("/s/{id}/{action}", a.handleShare)
	pub.HandleFunc("GET /assets/{file}", func(w http.ResponseWriter, r *http.Request) {
		if !publicAssets[r.PathValue("file")] {
			http.NotFound(w, r)
			return
		}
		a.handleAsset(w, r)
	})
	pub.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("file", "favicon.svg")
		a.handleAsset(w, r)
	})
	a.channelRoutes(pub) // the phone app, end-to-end encrypted
	return markPublic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if a.publicUI.Load() || (p == "/mcp" && a.publicMCP.Load()) {
			full.ServeHTTP(w, r)
			return
		}
		baseHeaders(w, r)
		pub.ServeHTTP(w, r)
	}))
}

// publicAssets are the files share pages load; the admin UI's code isn't among them.
var publicAssets = map[string]bool{
	"base.css": true, "share.css": true, "share.js": true, "favicon.svg": true,
	"atkinson-hyperlegible-latin-400-normal.woff2": true, "atkinson-hyperlegible-latin-700-normal.woff2": true,
}

func (a *App) adminAllowed(r *http.Request) bool {
	list := strings.TrimSpace(a.st.get("admin_cidrs"))
	return list == "" || cidrsAllow(list, net.ParseIP(a.clientIP(r)))
}

func splitCIDRs(list string) []string {
	return strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' })
}

func parseCIDR(c string) (*net.IPNet, error) {
	if !strings.Contains(c, "/") {
		if strings.Contains(c, ":") {
			c += "/128"
		} else {
			c += "/32"
		}
	}
	_, n, err := net.ParseCIDR(c)
	return n, err
}

// cidrsAllow reports whether ip is in the comma-separated list of IPs and ranges.
func cidrsAllow(list string, ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, c := range splitCIDRs(list) {
		if n, err := parseCIDR(c); err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// badCIDR returns the first entry in the list that isn't an IP or range.
func badCIDR(list string) string {
	for _, c := range splitCIDRs(list) {
		if _, err := parseCIDR(c); err != nil {
			return c
		}
	}
	return ""
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "missing UI", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'")
	_, _ = w.Write(b)
}

var startTime = time.Now()

func (a *App) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.PathValue("file"))
	b, err := fs.ReadFile(webFS, "web/"+name)
	if err != nil || name == "index.html" || name == "share.html" {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	http.ServeContent(w, r, name, startTime, strings.NewReader(string(b)))
}

// ---- state, setup, login ----

func (a *App) apiState(w http.ResponseWriter, r *http.Request) {
	done := a.setupDone()
	var u *User
	if done {
		u = a.sessionUser(r)
	}
	out := map[string]any{"setup_done": done, "signed_in": u != nil, "version": version}
	if !done {
		out["needs_key"] = a.needsSetupKey(r)
	}
	if u != nil {
		out["user"] = u.Name
		out["admin"] = u.Admin
		out["public_url"] = a.st.get("public_url")
	}
	writeJSON(w, out)
}

type rootInfo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (a *App) availableRoots() []rootInfo {
	des, _ := os.ReadDir(a.cfg.DataDir)
	en := map[string]bool{}
	for _, r := range a.idx.enabledRoots() {
		en[r] = true
	}
	out := []rootInfo{}
	for _, d := range des {
		if !d.IsDir() || a.idx.excluded(d.Name()) || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		out = append(out, rootInfo{Name: d.Name(), Enabled: en[d.Name()]})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// apiRootsAvailable lists the top-level folders of the library mount: for setup
// (from the LAN or with the setup key), and afterwards for admins only.
func (a *App) apiRootsAvailable(w http.ResponseWriter, r *http.Request) {
	if a.setupDone() {
		u := a.sessionUser(r)
		if u == nil {
			u, _ = a.apiTokenUser(r)
		}
		if u == nil {
			httpErr(w, 401, "sign in required")
			return
		}
		if !u.Admin {
			httpErr(w, 403, "admins only")
			return
		}
	}
	if !a.setupDone() && a.needsSetupKey(r) && !hmacEqual(r.URL.Query().Get("key"), a.currentSetupKey()) {
		httpErr(w, 403, "setup key required")
		return
	}
	writeJSON(w, a.availableRoots())
}

func (a *App) apiSetup(w http.ResponseWriter, r *http.Request) {
	a.setupMu.Lock() // two racing first-launch requests can't both claim the admin account
	defer a.setupMu.Unlock()
	if a.setupDone() {
		httpErr(w, 409, "already set up")
		return
	}
	var in struct {
		User      string   `json:"user"`
		Password  string   `json:"password"`
		PublicURL string   `json:"public_url"`
		Roots     []string `json:"roots"`
		Key       string   `json:"key"`
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if a.needsSetupKey(r) && !hmacEqual(in.Key, a.currentSetupKey()) {
		httpErr(w, 403, "setup key required: it is printed in the container log")
		return
	}
	pubURL, err := normalizeURL(in.PublicURL)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	u, err := a.createUser(in.User, in.Password, true)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	a.st.set("public_url", pubURL)
	a.saveRoots(in.Roots)
	if a.st.get("default_expiry_hours") == "" {
		a.st.set("default_expiry_hours", "168")
	}
	a.st.set("setup_done", "1")
	a.newSession(w, r, u.ID)
	a.idx.Rescan()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) saveRoots(roots []string) {
	valid := map[string]bool{}
	for _, ri := range a.availableRoots() {
		valid[ri.Name] = true
	}
	clean := []string{}
	for _, r := range roots {
		if _, _, remote := a.remoteOf(r); valid[r] && !remote {
			clean = append(clean, r)
		}
	}
	a.st.setJSON("roots", clean)
	a.idx.reloadSettings()
}

// normalizeURL checks the public address links are built from. It must be https:
// links are sent to other people, so they never point at a plain-text address.
func normalizeURL(raw string) (string, error) {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		return "", nil
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" || p.User != nil || p.RawQuery != "" || p.Fragment != "" {
		return "", errors.New("public address: use the form https://share.example.com")
	}
	if p.Scheme != "https" {
		return "", errors.New("public address must start with https://")
	}
	return u, nil
}

// needsSetupKey: first-launch setup needs the key printed in the container log,
// unless the browser runs on the server itself. Addresses and forwarded headers
// can't prove a request is local (a TCP proxy makes internet traffic look like
// the LAN), so nothing else skips the key.
func (a *App) needsSetupKey(r *http.Request) bool {
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(peer)
	proxied := r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" || r.Header.Get("Forwarded") != ""
	return ip == nil || !ip.IsLoopback() || proxied
}

func (a *App) apiLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ User, Password, Code string }
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	// Guesses are limited per client (8 per 15 minutes) and per account (30 per
	// hour, so many addresses can't share the work). A correct password is refunded.
	ipKey := "login:" + limitIP(a.clientIP(r))
	userKey := "login-user:" + strings.ToLower(trunc(strings.TrimSpace(in.User), 64))
	if !a.limiter.take(ipKey, 8, 15*time.Minute) {
		httpErr(w, 429, "too many attempts; wait 15 minutes")
		return
	}
	if !a.limiter.take(userKey, 30, time.Hour) {
		a.limiter.refund(ipKey)
		httpErr(w, 429, "too many attempts for this account; wait an hour")
		return
	}
	u, ok := a.checkPassword(in.User, in.Password)
	if !ok {
		httpErr(w, 401, "wrong username or password")
		return
	}
	a.limiter.refund(ipKey)
	a.limiter.refund(userKey)
	if u.TwoFA {
		if strings.TrimSpace(in.Code) == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"enter the code from your authenticator app","need_code":true}`))
			return
		}
		// codes are limited per account, wherever the guesses come from
		codeKey := "2fa-code:" + strconv.FormatInt(u.ID, 10)
		if !a.limiter.take(codeKey, 10, 15*time.Minute) {
			httpErr(w, 429, "too many codes tried; wait 15 minutes")
			return
		}
		if !a.checkSecondFactor(u, in.Code) {
			httpErr(w, 401, "that code is wrong or was already used")
			return
		}
		a.limiter.refund(codeKey)
	}
	a.newSession(w, r, u.ID)
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- browsing ----

func (a *App) apiLs(w http.ResponseWriter, r *http.Request) {
	es, err := a.listDir(userOf(r), r.URL.Query().Get("path"))
	if errors.Is(err, errRemoteDown) {
		httpErr(w, 502, err.Error())
		return
	}
	if err != nil {
		code := 404
		if errors.Is(err, errNotAllowed) {
			code = 403
		}
		httpErr(w, code, "can't open this folder")
		return
	}
	p := cleanRel(r.URL.Query().Get("path"))
	canWrite := false
	if _, _, ok := a.remoteOf(p); ok {
		canWrite = userOf(r).can(p, permWrite)
	} else if abs, _, err := a.resolve(p); err == nil && p != "" {
		canWrite = userOf(r).can(p, permWrite) && mountWritable(abs)
	}
	_, _, remote := a.remoteOf(p)
	writeJSON(w, map[string]any{"path": p, "entries": es, "can_share": p != "" && !remote && userOf(r).can(p, permShare), "can_write": canWrite})
}

func (a *App) apiSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sq := SearchQuery{Q: q.Get("q"), Kind: q.Get("kind"), Root: q.Get("root"), Sort: q.Get("sort"), Limit: atoiDef(q.Get("limit"), 200)}
	if err := checkQuery(sq.Q); err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	sq.After, _ = strconv.ParseInt(q.Get("after"), 10, 64)
	sq.MinSize, _ = strconv.ParseInt(q.Get("min"), 10, 64)
	sq.MaxSize, _ = strconv.ParseInt(q.Get("max"), 10, 64)
	res, err := a.search(userOf(r), sq)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	writeJSON(w, res)
}

// apiInfo describes one file or folder. A folder the user can only walk through
// (they may read something inside it) is described, with counts limited to what they may read.
func (a *App) apiInfo(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if _, _, ok := a.remoteOf(r.URL.Query().Get("path")); ok {
		a.remoteInfo(w, u, r.URL.Query().Get("path"))
		return
	}
	abs, rel, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil || !u.canReach(rel, permRead) {
		httpErr(w, 404, "not found")
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		httpErr(w, 404, "not found")
		return
	}
	e := entryFromInfo(rel, info)
	es := []Entry{e}
	a.decorate(u, es)
	shares, _ := a.listShares(r, u, rel, false)
	canRead := u.can(rel, permRead)
	canChange := strings.Contains(rel, "/") && u.can(rel, permWrite) && mountWritable(filepath.Dir(abs)) && (!e.IsDir || u.canWriteTree(rel))
	out := map[string]any{"entry": es[0], "shares": shares, "can_read": canRead, "can_share": u.can(rel, permShare), "can_change": canChange}
	if canRead {
		out["preview"] = previewKind(e)
	}
	if e.IsDir {
		var n, sz int64
		cond, args := u.scope("path", permRead)
		_ = a.st.db.QueryRow(`SELECT count(*), coalesce(sum(size),0) FROM files WHERE path >= ? AND path < ? AND `+cond, append([]any{rel + "/", rel + "0"}, args...)...).Scan(&n, &sz)
		out["items"], out["total_size"] = n, sz
	}
	writeJSON(w, out)
}

func (a *App) apiRaw(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.remoteOf(r.URL.Query().Get("path")); ok {
		a.remoteRaw(w, r, userOf(r), r.URL.Query().Get("path"), r.URL.Query().Get("dl") == "1")
		return
	}
	abs, _, err := a.resolveFor(userOf(r), r.URL.Query().Get("path"), permRead)
	if err != nil {
		httpErr(w, 404, "not found")
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		httpErr(w, 404, "not found")
		return
	}
	a.serveFile(w, r, abs, info, r.URL.Query().Get("dl") == "1")
}

func (a *App) apiThumb(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.remoteOf(r.URL.Query().Get("path")); ok {
		a.remoteThumb(w, r, userOf(r), r.URL.Query().Get("path"))
		return
	}
	abs, _, err := a.resolveFor(userOf(r), r.URL.Query().Get("path"), permRead)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.serveThumb(w, r, abs, info)
}

// apiZip streams a folder as a zip, leaving out anything inside the user may not read.
func (a *App) apiZip(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if _, _, ok := a.remoteOf(r.URL.Query().Get("path")); ok {
		httpErr(w, 400, "folders on a connected NAS can't be downloaded as a zip yet")
		return
	}
	abs, rel, err := a.resolve(r.URL.Query().Get("path"))
	if err != nil || !u.canReach(rel, permRead) {
		httpErr(w, 404, "not found")
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		httpErr(w, 400, "not a folder")
		return
	}
	a.streamZipMany(w, info.Name(), []zipSource{{Abs: abs, Name: info.Name(), Lib: rel}}, zipAllow(u, permRead))
}

func (a *App) apiChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	cs, err := a.changes(userOf(r), since, q.Get("root"), q.Get("kind"), atoiDef(q.Get("limit"), 300))
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, cs)
}

// statusFor returns the index status. Library-wide totals are for admins only.
func (a *App) statusFor(u *User) any {
	st := a.idx.Status()
	if u.Admin {
		return st
	}
	return map[string]any{"scanning": st.Scanning, "scanned_dirs": st.ScannedDirs, "last_end": st.LastEnd}
}

// apiStats returns per-root and per-type totals for the overview chart, counting only
// what the user may read.
func (a *App) apiStats(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	cond, cargs := u.scope("path", permRead)
	type row struct {
		Name  string `json:"name"`
		Files int64  `json:"files"`
		Bytes int64  `json:"bytes"`
	}
	byKind := []row{}
	rows, err := a.st.db.Query(`SELECT CASE WHEN kind='' THEN 'other' ELSE kind END k, count(*), coalesce(sum(size),0) FROM files WHERE is_dir=0 AND `+cond+` GROUP BY k ORDER BY 3 DESC`, cargs...)
	if err == nil {
		for rows.Next() {
			var x row
			_ = rows.Scan(&x.Name, &x.Files, &x.Bytes)
			byKind = append(byKind, x)
		}
		rows.Close()
	}
	byRoot := []row{}
	for _, root := range a.idx.enabledRoots() {
		if !u.canReach(root, permRead) {
			continue
		}
		var x row
		x.Name = root
		_ = a.st.db.QueryRow(`SELECT count(*), coalesce(sum(size),0) FROM files WHERE is_dir=0 AND path >= ? AND path < ? AND `+cond, append([]any{root + "/", root + "0"}, cargs...)...).Scan(&x.Files, &x.Bytes)
		byRoot = append(byRoot, x)
	}
	sort.Slice(byRoot, func(i, j int) bool { return byRoot[i].Bytes > byRoot[j].Bytes })
	var week []int64
	y, m, d := time.Now().Date()
	for i := 6; i >= 0; i-- {
		day := time.Date(y, m, d-i, 0, 0, 0, 0, time.Local) // local midnight, not UTC
		var n int64
		_ = a.st.db.QueryRow(`SELECT count(*) FROM changes WHERE ts >= ? AND ts < ? AND `+cond, append([]any{day.Unix(), day.AddDate(0, 0, 1).Unix()}, cargs...)...).Scan(&n)
		week = append(week, n)
	}
	var activeShares int64
	sq, sargs := `SELECT count(*) FROM shares WHERE revoked=0 AND (expires=0 OR expires>?) AND (max_dl=0 OR dl<max_dl)`, []any{now()}
	if !u.Admin {
		sq += ` AND owner_id=?`
		sargs = append(sargs, u.ID)
	}
	_ = a.st.db.QueryRow(sq, sargs...).Scan(&activeShares)
	writeJSON(w, map[string]any{"by_kind": byKind, "by_root": byRoot, "changes_7d": week, "active_shares": activeShares, "status": a.statusFor(u)})
}

// ---- settings ----

// Settings has the signed-in user's account fields, the share defaults everyone
// needs, and, for admins only, the server settings.
type Settings struct {
	User               string     `json:"user"`
	Admin              bool       `json:"admin"`
	TwoFactor          bool       `json:"two_factor"`
	RecoveryLeft       int        `json:"recovery_left"`
	PublicURL          string     `json:"public_url"`
	LanURL             string     `json:"lan_url"`
	DefaultExpiryHours float64    `json:"default_expiry_hours"`
	MaxUploadMB        int64      `json:"max_upload_mb"`
	Roots              []rootInfo `json:"roots"`
	Excludes           string     `json:"excludes"`
	AdminCIDRs         string     `json:"admin_cidrs"`
	PublicUI           bool       `json:"public_ui"`
	PublicMCP          bool       `json:"public_mcp"`
	PublicAddr         string     `json:"public_addr"`
	DataDir            string     `json:"data_dir"`
	DropDir            string     `json:"drop_dir"`
	ScanHours          float64    `json:"scan_hours"`
}

func (a *App) currentSettings(u *User) Settings {
	exp, _ := strconv.ParseFloat(a.st.get("default_expiry_hours"), 64)
	mb, _ := strconv.ParseInt(a.st.get("max_upload_mb"), 10, 64)
	if mb <= 0 {
		mb = 20480
	}
	ex := a.st.get("excludes")
	if ex == "" {
		ex = strings.Join(defaultExcludes, "\n")
	}
	out := Settings{User: u.Name, Admin: u.Admin, TwoFactor: u.TwoFA, RecoveryLeft: len(u.recovery),
		PublicURL: a.st.get("public_url"), DefaultExpiryHours: exp, MaxUploadMB: mb, Roots: []rootInfo{}}
	if u.Admin {
		out.Roots, out.Excludes, out.AdminCIDRs = a.availableRoots(), ex, a.st.get("admin_cidrs")
		out.DataDir, out.DropDir, out.ScanHours = a.cfg.DataDir, a.cfg.DropDir, a.cfg.ScanInterval.Hours()
		out.PublicUI, out.PublicMCP, out.PublicAddr = a.publicUI.Load(), a.publicMCP.Load(), a.cfg.PublicAddr
		out.LanURL = a.st.get("lan_url")
	}
	return out
}

func (a *App) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.currentSettings(userOf(r)))
}

func (a *App) apiPutSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicURL          *string   `json:"public_url"`
		LanURL             *string   `json:"lan_url"`
		PublicUI           *bool     `json:"public_ui"`
		PublicMCP          *bool     `json:"public_mcp"`
		Roots              *[]string `json:"roots"`
		Excludes           *string   `json:"excludes"`
		DefaultExpiryHours *float64  `json:"default_expiry_hours"`
		MaxUploadMB        *int64    `json:"max_upload_mb"`
		AdminCIDRs         *string   `json:"admin_cidrs"`
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	rescan := false
	// check everything that can be refused before changing anything
	var pubURL, lanURL string
	if in.PublicURL != nil {
		var err error
		if pubURL, err = normalizeURL(*in.PublicURL); err != nil {
			httpErr(w, 400, err.Error())
			return
		}
	}
	if in.LanURL != nil {
		var err error
		if lanURL, err = normalizeURL(*in.LanURL); err != nil {
			httpErr(w, 400, strings.Replace(err.Error(), "public address", "home address for phones", 1))
			return
		}
	}
	if in.AdminCIDRs != nil {
		v := strings.TrimSpace(*in.AdminCIDRs)
		if bad := badCIDR(v); bad != "" {
			httpErr(w, 400, "not an IP address or range: "+bad)
			return
		}
		if v != "" && !cidrsAllow(v, net.ParseIP(a.clientIP(r))) {
			httpErr(w, 400, "that list would lock out the network you're on now; include "+a.clientIP(r))
			return
		}
	}
	if in.PublicURL != nil {
		a.st.set("public_url", pubURL)
	}
	if in.LanURL != nil {
		a.st.set("lan_url", lanURL)
	}
	setFlag := func(key string, v *bool, f *atomic.Bool) {
		if v != nil {
			a.st.set(key, map[bool]string{true: "1", false: ""}[*v])
			f.Store(*v)
		}
	}
	setFlag("public_ui", in.PublicUI, &a.publicUI)
	setFlag("public_mcp", in.PublicMCP, &a.publicMCP)
	if in.Excludes != nil {
		a.st.set("excludes", strings.TrimSpace(*in.Excludes))
		a.idx.reloadSettings()
		rescan = true
	}
	if in.Roots != nil {
		a.saveRoots(*in.Roots)
		rescan = true
	}
	if in.DefaultExpiryHours != nil {
		a.st.set("default_expiry_hours", strconv.FormatFloat(max(*in.DefaultExpiryHours, 0), 'f', -1, 64))
	}
	if in.MaxUploadMB != nil {
		a.st.set("max_upload_mb", strconv.FormatInt(max64(*in.MaxUploadMB, 1), 10))
	}
	if in.AdminCIDRs != nil {
		a.st.set("admin_cidrs", strings.TrimSpace(*in.AdminCIDRs))
	}
	if rescan {
		a.idx.Rescan()
	}
	writeJSON(w, a.currentSettings(userOf(r)))
}

func (a *App) apiPassword(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct{ Current, New string }
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	// a stolen session alone shouldn't be able to guess the password at full speed
	key := "pw:" + strconv.FormatInt(u.ID, 10)
	if !a.limiter.take(key, 8, 15*time.Minute) {
		httpErr(w, 429, "too many attempts; wait 15 minutes")
		return
	}
	if _, ok := a.checkPassword(u.Name, in.Current); !ok {
		httpErr(w, 401, "current password is wrong")
		return
	}
	a.limiter.refund(key)
	if err := checkNewPassword(in.New); err != nil {
		httpErr(w, 400, "new "+err.Error())
		return
	}
	if err := a.setPassword(u.ID, in.New); err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	a.dropSessions(u.ID) // sign out everywhere else
	a.newSession(w, r, u.ID)
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- favorites (per user) ----

func (a *App) apiFavorites(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	rows, err := a.st.db.Query(`SELECT path FROM favs WHERE user_id=? ORDER BY lower(path)`, u.ID)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	var paths []string
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		paths = append(paths, p)
	}
	rows.Close()
	out := []Entry{}
	for _, p := range paths {
		if abs, rel, err := a.resolve(p); err == nil && u.canReach(rel, permRead) {
			if info, err := os.Stat(abs); err == nil {
				out = append(out, entryFromInfo(rel, info))
			}
		}
	}
	a.decorate(u, out)
	writeJSON(w, out)
}

func (a *App) apiAddFavorite(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct{ Path string }
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	_, rel, err := a.resolve(in.Path)
	if err != nil || !u.canReach(rel, permRead) {
		httpErr(w, 404, "not found")
		return
	}
	_, _ = a.st.exec(`INSERT OR IGNORE INTO favs(user_id,path,created) VALUES(?,?,?)`, u.ID, rel, now())
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) apiDelFavorite(w http.ResponseWriter, r *http.Request) {
	_, _ = a.st.exec(`DELETE FROM favs WHERE user_id=? AND path=?`, userOf(r).ID, cleanRel(r.URL.Query().Get("path")))
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- shares ----

func (a *App) apiShares(w http.ResponseWriter, r *http.Request) {
	ss, err := a.listShares(r, userOf(r), r.URL.Query().Get("path"), r.URL.Query().Get("active") == "1")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, ss)
}

func (a *App) apiCreateShare(w http.ResponseWriter, r *http.Request) {
	var in ShareInput
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	s, err := a.createShare(r, userOf(r), in)
	if err != nil {
		code := 400
		if errors.Is(err, errAdminsOnly) {
			code = 403
		}
		httpErr(w, code, err.Error())
		return
	}
	writeJSON(w, s)
}

func (a *App) apiPatchShare(w http.ResponseWriter, r *http.Request) {
	var p SharePatch
	if err := readJSON(r, &p); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if err := a.patchShare(userOf(r), r.PathValue("id"), p); err != nil {
		if errors.Is(err, errAdminsOnly) {
			httpErr(w, 403, err.Error())
			return
		}
		httpErr(w, 404, "share not found")
		return
	}
	s, _ := a.getShare(r.PathValue("id"))
	s.State = a.shareState(&s)
	s.URL = a.baseURL(r) + "/s/" + s.ID
	writeJSON(w, s)
}

func (a *App) apiDeleteShare(w http.ResponseWriter, r *http.Request) {
	s, err := a.ownedShare(userOf(r), r.PathValue("id"))
	if err != nil {
		httpErr(w, 404, "share not found")
		return
	}
	if r.URL.Query().Get("purge") == "1" {
		if err := a.purgeUploads(s); err != nil {
			httpErr(w, 500, "could not delete the uploaded files")
			return
		}
	}
	_, _ = a.st.exec(`DELETE FROM share_items WHERE share_id=?`, s.ID)
	_, _ = a.st.exec(`DELETE FROM shares WHERE id=?`, s.ID)
	// share_events are kept: who opened what stays in the admins' activity log
	// (pruned after a year like all link activity)
	writeJSON(w, map[string]bool{"ok": true})
}
