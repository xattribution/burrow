package main

// Paired devices (the phone app) and the HTTP side of the phone channel
// (devchan.go has the cryptography). Devices act as the user who paired them,
// with that user's folder grants. They reach Burrow on either port through
// /api/d/*, which is outside the admin network list: the channel authenticates
// every request itself.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const deviceSchema = `
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  platform TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  app_version TEXT NOT NULL DEFAULT '',
  key BLOB NOT NULL,
  created INTEGER NOT NULL,
  last_seen INTEGER NOT NULL DEFAULT 0,
  last_ip TEXT NOT NULL DEFAULT '',
  last_via TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS devices_user ON devices(user_id);
`

const (
	pairTTL         = 10 * time.Minute
	devSessionTTL   = time.Hour
	devSessionsMax  = 8 // per device; the oldest goes first
	replayWindow    = 4096
	pairLimit       = 20 // failed pairing attempts per address per 15 minutes
	helloLimit      = 60 // failed session handshakes per address per 15 minutes
	pairCodesPerUse = 3  // codes a user can have open at once
)

type pairCode struct {
	userID     int64
	key        []byte
	expires    time.Time
	used       bool
	deviceID   string
	deviceName string
}

type devSession struct {
	deviceID string
	userID   int64
	key      []byte
	created  time.Time
	expires  time.Time
	mu       sync.Mutex
	top      uint64
	seen     map[uint64]bool
}

// accept records message number n and reports whether it is new. Numbers more
// than replayWindow below the highest one seen are refused outright.
func (s *devSession) accept(n uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == 0 || (s.top >= replayWindow && n <= s.top-replayWindow) || s.seen[n] {
		return false
	}
	s.seen[n] = true
	if n > s.top {
		s.top = n
	}
	if len(s.seen) > 2*replayWindow {
		for k := range s.seen {
			if k+replayWindow <= s.top {
				delete(s.seen, k)
			}
		}
	}
	return true
}

type devState struct {
	mu       sync.Mutex
	codes    map[string]*pairCode
	sessions map[string]*devSession
}

func (d *devState) init() {
	if d.codes == nil {
		d.codes = map[string]*pairCode{}
		d.sessions = map[string]*devSession{}
	}
}

func (d *devState) sweep() {
	t := time.Now()
	for id, c := range d.codes {
		if t.After(c.expires.Add(pairTTL)) { // kept a while after expiry so the web UI can show the result
			delete(d.codes, id)
		}
	}
	for id, s := range d.sessions {
		if t.After(s.expires) {
			delete(d.sessions, id)
		}
	}
}

// dropDeviceSessions ends a revoked device's sessions (or all of a user's with id "").
func (a *App) dropDeviceSessions(deviceID string, userID int64) {
	a.dev.mu.Lock()
	defer a.dev.mu.Unlock()
	a.dev.init()
	for id, s := range a.dev.sessions {
		if (deviceID != "" && s.deviceID == deviceID) || (deviceID == "" && s.userID == userID) {
			delete(a.dev.sessions, id)
		}
	}
}

// revokeDevices removes every device of a user (account recovery, deleted user).
func (a *App) revokeDevices(userID int64) {
	_, _ = a.st.exec(`DELETE FROM devices WHERE user_id=?`, userID)
	a.dropDeviceSessions("", userID)
}

func (a *App) deviceExists(id string) bool {
	var one int
	return a.st.db.QueryRow(`SELECT 1 FROM devices WHERE id=?`, id).Scan(&one) == nil
}

// ---- addresses the app is given ----

// lanURL is the address phones use at home: the admin's setting, or else the
// address this browser used, unless that is the public address or loopback.
func (a *App) lanURL(r *http.Request) string {
	if v := a.st.get("lan_url"); v != "" {
		return v
	}
	if r == nil {
		return ""
	}
	host := r.Host
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "" || h == "localhost" || strings.ContainsAny(host, "/\\@ ") {
		return ""
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return ""
	}
	if pu, err := url.Parse(a.st.get("public_url")); err == nil && strings.EqualFold(pu.Host, host) {
		return ""
	}
	if a.trusted(peerOf(r)) && r.Header.Get("X-Forwarded-For") != "" {
		return "" // came through the reverse proxy: not an address the phone can use at home
	}
	return "https://" + host
}

func peerOf(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// certPins are the SHA-256 fingerprints of the certificate Burrow presents, for
// the app to pin when the certificate isn't publicly trusted (the self-signed one).
func (a *App) certPins() []string {
	if a.certs == nil {
		return []string{}
	}
	c, _ := a.certs.get(nil)
	if c == nil || len(c.Certificate) == 0 {
		return []string{}
	}
	sum := sha256.Sum256(c.Certificate[0])
	return []string{base64.RawURLEncoding.EncodeToString(sum[:])}
}

// serverInfo is what a device learns about the server, inside the channel.
func (a *App) serverInfo(u *User, deviceID, deviceName string, r *http.Request) map[string]any {
	return map[string]any{
		"device_id":   deviceID,
		"device_name": deviceName,
		"user":        u.Name,
		"admin":       u.Admin,
		"lan":         a.lanURL(r),
		"public":      a.st.get("public_url"),
		"pins":        a.certPins(),
		"version":     version,
		"ttl":         int(devSessionTTL.Seconds()),
	}
}

// ---- pairing, from the web UI ----

func (a *App) apiPairStart(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	lan, pub := a.lanURL(r), a.st.get("public_url")
	if lan == "" && pub == "" {
		httpErr(w, 400, "Burrow doesn't know an address your phone can use. Set the public address, or the home address for phones, in Settings.")
		return
	}
	id, key := make([]byte, chIDLen), make([]byte, 32)
	_, _ = rand.Read(id)
	_, _ = rand.Read(key)
	hid := hex.EncodeToString(id)
	a.dev.mu.Lock()
	a.dev.init()
	a.dev.sweep()
	open := 0
	for _, c := range a.dev.codes {
		if c.userID == u.ID && !c.used && time.Now().Before(c.expires) {
			open++
		}
	}
	if open >= pairCodesPerUse || len(a.dev.codes) > 500 {
		a.dev.mu.Unlock()
		httpErr(w, 429, "too many pairing codes open; close one or wait ten minutes")
		return
	}
	exp := time.Now().Add(pairTTL)
	a.dev.codes[hid] = &pairCode{userID: u.ID, key: key, expires: exp}
	a.dev.mu.Unlock()

	b64 := base64.RawURLEncoding.EncodeToString
	q := url.Values{}
	q.Set("v", "1")
	q.Set("c", b64(id))
	q.Set("k", b64(key))
	if lan != "" {
		q.Set("lan", lan)
	}
	if pub != "" {
		q.Set("pub", pub)
	}
	if pins := a.certPins(); len(pins) > 0 {
		q.Set("pin", pins[0])
	}
	link := "burrow://pair?" + q.Encode()
	png, err := qrcode.Encode(link, qrcode.Medium, 480)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"id": hid, "link": link, "expires": exp.Unix(), "lan": lan, "public": pub,
		"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (a *App) apiPairStatus(w http.ResponseWriter, r *http.Request) {
	a.dev.mu.Lock()
	a.dev.init()
	c := a.dev.codes[r.PathValue("id")]
	var out map[string]any
	switch {
	case c == nil || c.userID != userOf(r).ID:
		out = map[string]any{"status": "expired"}
	case c.used && c.deviceID != "":
		out = map[string]any{"status": "paired", "device": map[string]string{"id": c.deviceID, "name": c.deviceName}}
	case c.used || time.Now().After(c.expires):
		out = map[string]any{"status": "expired"}
	default:
		out = map[string]any{"status": "waiting", "expires": c.expires.Unix()}
	}
	a.dev.mu.Unlock()
	writeJSON(w, out)
}

func (a *App) apiPairCancel(w http.ResponseWriter, r *http.Request) {
	a.dev.mu.Lock()
	a.dev.init()
	if c := a.dev.codes[r.PathValue("id")]; c != nil && c.userID == userOf(r).ID && !c.used {
		delete(a.dev.codes, r.PathValue("id"))
	}
	a.dev.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- device list ----

type Device struct {
	ID         string `json:"id"`
	UserID     int64  `json:"user_id"`
	User       string `json:"user,omitempty"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Model      string `json:"model"`
	AppVersion string `json:"app_version"`
	Created    int64  `json:"created"`
	LastSeen   int64  `json:"last_seen"`
	LastIP     string `json:"last_ip"`
	LastVia    string `json:"last_via"`
}

func (a *App) listDevices(u *User, all bool) ([]Device, error) {
	q := `SELECT d.id,d.user_id,coalesce(u.username,''),d.name,d.platform,d.model,d.app_version,d.created,d.last_seen,d.last_ip,d.last_via FROM devices d LEFT JOIN users u ON u.id=d.user_id`
	var args []any
	if !(all && u.Admin) {
		q += ` WHERE d.user_id=?`
		args = append(args, u.ID)
	}
	rows, err := a.st.db.Query(q+` ORDER BY d.created DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.User, &d.Name, &d.Platform, &d.Model, &d.AppVersion, &d.Created, &d.LastSeen, &d.LastIP, &d.LastVia); err != nil {
			return out, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (a *App) apiDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := a.listDevices(userOf(r), r.URL.Query().Get("all") == "1")
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, ds)
}

// ownedDevice: the user's own device, or any device for an admin.
func (a *App) ownedDevice(u *User, id string) bool {
	var uid int64
	if a.st.db.QueryRow(`SELECT user_id FROM devices WHERE id=?`, id).Scan(&uid) != nil {
		return false
	}
	return uid == u.ID || u.Admin
}

func (a *App) apiRenameDevice(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		httpErr(w, 400, "give the device a name")
		return
	}
	id := r.PathValue("id")
	if !a.ownedDevice(userOf(r), id) {
		httpErr(w, 404, "device not found")
		return
	}
	_, _ = a.st.exec(`UPDATE devices SET name=? WHERE id=?`, trunc(strings.TrimSpace(in.Name), 80), id)
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) apiRevokeDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.ownedDevice(userOf(r), id) {
		httpErr(w, 404, "device not found")
		return
	}
	_, _ = a.st.exec(`DELETE FROM devices WHERE id=?`, id)
	a.dropDeviceSessions(id, 0)
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- the channel endpoints ----

func chBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil || len(b) == 0 || b[0] != chVersion {
		httpErr(w, 400, "bad request")
		return nil, false
	}
	return b, true
}

func chReply(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// handlePair completes a pairing started in the web UI.
func (a *App) handlePair(w http.ResponseWriter, r *http.Request) {
	ipKey := "pair:" + limitIP(a.clientIP(r))
	if !a.limiter.take(ipKey, pairLimit, 15*time.Minute) {
		httpErr(w, 429, "too many attempts; wait 15 minutes")
		return
	}
	req, ok := chBody(w, r, 8<<10)
	if !ok {
		return
	}
	const head = 1 + chIDLen + chNonce + chEK + chX
	if len(req) < head+2+chMAC {
		httpErr(w, 400, "bad request")
		return
	}
	n := int(binary.BigEndian.Uint16(req[head:]))
	if len(req) != head+2+n+chMAC {
		httpErr(w, 400, "bad request")
		return
	}
	codeID := hex.EncodeToString(req[1 : 1+chIDLen])
	nonce := req[1+chIDLen : 1+chIDLen+chNonce]
	ek := req[1+chIDLen+chNonce : 1+chIDLen+chNonce+chEK]
	cx := req[head-chX : head]
	sealedInfo := req[head+2 : head+2+n]
	mac := req[len(req)-chMAC:]

	a.dev.mu.Lock()
	a.dev.init()
	c := a.dev.codes[codeID]
	valid := c != nil && !c.used && time.Now().Before(c.expires) && hmac32Equal(chMac(chKey(c.key, "burrow-v1 pair auth"), req[:len(req)-chMAC]), mac)
	var kqr []byte
	var uid int64
	if valid {
		c.used = true // one try with a correct code, whatever happens next
		kqr, uid = c.key, c.userID
	}
	a.dev.mu.Unlock()
	if !valid {
		httpErr(w, 403, "this pairing code is wrong, expired or already used")
		return
	}
	a.limiter.refund(ipKey)

	infoKey := chKey(append(append([]byte{}, kqr...), nonce...), "burrow-v1 pair info")
	plain, err := chOpen(infoKey, sealedInfo, req[:head+2])
	var info struct{ Name, Platform, Model, AppVersion string }
	if err != nil || json.Unmarshal(plain, &info) != nil {
		httpErr(w, 400, "bad request")
		return
	}
	u := a.activeUser(uid)
	if u == nil {
		httpErr(w, 403, "this account is turned off")
		return
	}
	ct, sx, ss, err := kemReply(ek, cx)
	if err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	devID := make([]byte, chIDLen)
	sNonce := make([]byte, chNonce)
	_, _ = rand.Read(devID)
	_, _ = rand.Read(sNonce)
	replyHead := concat([]byte{chVersion}, devID, sNonce, ct, sx)
	prk, th := chPRK(kqr, ss, req, replyHead)
	dk := chExpand(prk, "burrow-v1 device key")
	hid := hex.EncodeToString(devID)
	name := trunc(strings.TrimSpace(info.Name), 80)
	if name == "" {
		name = "Phone"
	}
	if _, err := a.st.exec(`INSERT INTO devices(id,user_id,name,platform,model,app_version,key,created,last_seen,last_ip,last_via) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		hid, uid, name, trunc(info.Platform, 20), trunc(info.Model, 80), trunc(info.AppVersion, 40), dk, now(), now(), a.clientIP(r), a.via(r)); err != nil {
		httpErr(w, 500, "could not save the device")
		return
	}
	a.dev.mu.Lock()
	c.deviceID, c.deviceName = hid, name
	a.dev.mu.Unlock()
	payload, _ := json.Marshal(a.serverInfo(u, hid, name, r))
	log.Printf("devices: %s paired %q (%s)", u.Name, name, info.Platform)
	chReply(w, concat(replyHead, chSeal(chExpand(prk, "burrow-v1 pair payload"), payload, th)))
}

// handleHello starts a session for a paired device.
func (a *App) handleHello(w http.ResponseWriter, r *http.Request) {
	ipKey := "hello:" + limitIP(a.clientIP(r))
	if !a.limiter.take(ipKey, helloLimit, 15*time.Minute) {
		httpErr(w, 429, "too many attempts; wait 15 minutes")
		return
	}
	req, ok := chBody(w, r, 4<<10)
	if !ok {
		return
	}
	if len(req) != 1+chIDLen+chNonce+chEK+chX+chMAC {
		httpErr(w, 400, "bad request")
		return
	}
	devID := hex.EncodeToString(req[1 : 1+chIDLen])
	ek := req[1+chIDLen+chNonce : 1+chIDLen+chNonce+chEK]
	cx := req[1+chIDLen+chNonce+chEK : len(req)-chMAC]
	var dk []byte
	var uid int64
	var name string
	if err := a.st.db.QueryRow(`SELECT key,user_id,name FROM devices WHERE id=?`, devID).Scan(&dk, &uid, &name); err != nil {
		dk = make([]byte, 32) // same work either way
	}
	if !hmac32Equal(chMac(chKey(dk, "burrow-v1 hello auth"), req[:len(req)-chMAC]), req[len(req)-chMAC:]) || uid == 0 {
		httpErr(w, 401, "this device isn't paired")
		return
	}
	u := a.activeUser(uid)
	if u == nil {
		httpErr(w, 403, "this account is turned off")
		return
	}
	a.limiter.refund(ipKey)
	ct, sx, ss, err := kemReply(ek, cx)
	if err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	sid := make([]byte, chIDLen)
	sNonce := make([]byte, chNonce)
	_, _ = rand.Read(sid)
	_, _ = rand.Read(sNonce)
	exp := time.Now().Add(devSessionTTL)
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], uint64(exp.Unix()))
	replyHead := concat([]byte{chVersion}, sid, sNonce, ct, sx, e[:])
	prk, th := chPRK(dk, ss, req, replyHead)

	s := &devSession{deviceID: devID, userID: uid, key: chExpand(prk, "burrow-v1 session"), created: time.Now(), expires: exp, seen: map[uint64]bool{}}
	a.dev.mu.Lock()
	a.dev.init()
	a.dev.sweep()
	var mine []string
	for id, x := range a.dev.sessions {
		if x.deviceID == devID {
			mine = append(mine, id)
		}
	}
	for len(mine) >= devSessionsMax { // drop the oldest
		oldest := 0
		for i, id := range mine {
			if a.dev.sessions[id].created.Before(a.dev.sessions[mine[oldest]].created) {
				oldest = i
			}
		}
		delete(a.dev.sessions, mine[oldest])
		mine = append(mine[:oldest], mine[oldest+1:]...)
	}
	a.dev.sessions[hex.EncodeToString(sid)] = s
	a.dev.mu.Unlock()
	_, _ = a.st.exec(`UPDATE devices SET last_seen=?, last_ip=?, last_via=? WHERE id=?`, now(), a.clientIP(r), a.via(r), devID)
	payload, _ := json.Marshal(a.serverInfo(u, devID, name, r))
	chReply(w, concat(replyHead, chSeal(chExpand(prk, "burrow-v1 hello payload"), payload, th)))
}

// via tells the device list whether a device came in at home or from outside.
func (a *App) via(r *http.Request) string {
	if publicListener(r) {
		return "public"
	}
	return "lan"
}

type publicKey struct{}

// markPublic tags requests that arrived on the public port.
func markPublic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), publicKey{}, true)))
	})
}

func publicListener(r *http.Request) bool {
	v, _ := r.Context().Value(publicKey{}).(bool)
	return v
}

func hmac32Equal(a, b []byte) bool { return len(a) == len(b) && hmacEqual(string(a), string(b)) }

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// innerReq is the header of a request inside the tunnel.
type innerReq struct {
	Method string            `json:"m"`
	URL    string            `json:"u"`
	Header map[string]string `json:"h"`
	Length *int64            `json:"l"`
}

// innerHeaders are the request headers handlers may see; the rest are dropped.
var innerHeaders = []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match", "Content-Type", "Accept"}

// handleTunnel decrypts one request from a device, runs it as that device's
// user, and encrypts the reply.
func (a *App) handleTunnel(w http.ResponseWriter, r *http.Request) {
	br := bufio.NewReaderSize(r.Body, 64<<10)
	var head [1 + chIDLen + 8]byte
	if _, err := io.ReadFull(br, head[:]); err != nil || head[0] != chVersion {
		httpErr(w, 400, "bad request")
		return
	}
	sid := hex.EncodeToString(head[1 : 1+chIDLen])
	msg := binary.BigEndian.Uint64(head[1+chIDLen:])
	a.dev.mu.Lock()
	a.dev.init()
	s := a.dev.sessions[sid]
	if s != nil && time.Now().After(s.expires) {
		delete(a.dev.sessions, sid)
		s = nil
	}
	a.dev.mu.Unlock()
	// "session" tells the app to run a new handshake and send the request again
	if s == nil || !a.deviceExists(s.deviceID) {
		httpErr(w, 401, "session")
		return
	}
	u := a.activeUser(s.userID)
	if u == nil {
		httpErr(w, 403, "this account is turned off")
		return
	}
	msgNo := string(head[1+chIDLen:])
	in := newSegReader(br, chExpand(s.key, "burrow-v1 c2s "+msgNo), head[:], func() error {
		if !s.accept(msg) {
			return errChReplay
		}
		return nil
	})
	hb, err := readFrame(in)
	var ih innerReq
	if err == nil {
		err = json.Unmarshal(hb, &ih)
	}
	if err != nil {
		if errors.Is(err, errChReplay) {
			httpErr(w, 409, "replayed message")
		} else {
			httpErr(w, 400, "bad message")
		}
		return
	}
	switch ih.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		httpErr(w, 400, "bad message")
		return
	}
	pu, err := url.ParseRequestURI(ih.URL)
	if err != nil || pu.Scheme != "" || pu.Host != "" || !strings.HasPrefix(pu.Path, "/api/") {
		httpErr(w, 400, "bad message")
		return
	}
	ir, err := http.NewRequestWithContext(r.Context(), ih.Method, ih.URL, in)
	if err != nil {
		httpErr(w, 400, "bad message")
		return
	}
	ir.RemoteAddr, ir.TLS, ir.Host, ir.Proto = r.RemoteAddr, r.TLS, r.Host, r.Proto
	for _, k := range innerHeaders {
		if v := ih.Header[k]; v != "" {
			ir.Header.Set(k, v)
		}
	}
	for _, k := range []string{"X-Forwarded-For", "X-Real-IP"} { // so clientIP sees the same address
		if v := r.Header.Get(k); v != "" {
			ir.Header.Set(k, v)
		}
	}
	ir.ContentLength = -1
	if ih.Length != nil && *ih.Length >= 0 {
		ir.ContentLength = *ih.Length
	}
	ir = withAuth(ir, authInfo{user: u, viaAPI: true, deviceID: s.deviceID})
	_ = http.NewResponseController(w).EnableFullDuplex()
	iw := &innerWriter{outer: w, key: chExpand(s.key, "burrow-v1 s2c "+msgNo), aad: head[:], header: http.Header{}}
	a.devMux.ServeHTTP(iw, ir)
	iw.finish()
}

// innerWriter is the http.ResponseWriter handlers see inside the tunnel. The
// reply's status and headers go into the encrypted stream as its JSON header.
type innerWriter struct {
	outer    http.ResponseWriter
	key, aad []byte
	header   http.Header
	seg      *segWriter
	wrote    bool
}

func (w *innerWriter) Header() http.Header { return w.header }

func (w *innerWriter) WriteHeader(code int) {
	if w.wrote || code < 200 {
		return
	}
	w.wrote = true
	oh := w.outer.Header()
	oh.Set("Content-Type", "application/octet-stream")
	oh.Set("Cache-Control", "no-store")
	oh.Del("Content-Length")
	w.outer.WriteHeader(200)
	_, _ = w.outer.Write([]byte{chVersion})
	rc := http.NewResponseController(w.outer)
	w.seg = newSegWriter(w.outer, w.key, w.aad, func() { _ = rc.Flush() })
	h := map[string]string{}
	for k, v := range w.header {
		if len(v) > 0 {
			h[k] = v[0]
		}
	}
	b, _ := json.Marshal(map[string]any{"s": code, "h": h})
	_ = writeFrame(w.seg, b)
}

func (w *innerWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(200)
	}
	return w.seg.Write(p)
}

func (w *innerWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(200)
	}
	_ = w.seg.Flush()
}

func (w *innerWriter) finish() {
	if !w.wrote {
		w.WriteHeader(200)
	}
	_ = w.seg.Close()
}

// ---- endpoints for the device itself (inside the tunnel) ----

func (a *App) apiDeviceMe(w http.ResponseWriter, r *http.Request) {
	ai := authOf(r)
	var name string
	_ = a.st.db.QueryRow(`SELECT name FROM devices WHERE id=?`, ai.deviceID).Scan(&name)
	writeJSON(w, a.serverInfo(ai.user, ai.deviceID, name, r))
}

func (a *App) apiDeviceUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Model string
		AppVersion  string `json:"app_version"`
	}
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	id := authOf(r).deviceID
	if n := strings.TrimSpace(in.Name); n != "" {
		_, _ = a.st.exec(`UPDATE devices SET name=? WHERE id=?`, trunc(n, 80), id)
	}
	if in.Model != "" {
		_, _ = a.st.exec(`UPDATE devices SET model=? WHERE id=?`, trunc(in.Model, 80), id)
	}
	if in.AppVersion != "" {
		_, _ = a.st.exec(`UPDATE devices SET app_version=? WHERE id=?`, trunc(in.AppVersion, 40), id)
	}
	a.apiDeviceMe(w, r)
}

// apiDeviceUnpair lets the app remove itself.
func (a *App) apiDeviceUnpair(w http.ResponseWriter, r *http.Request) {
	id := authOf(r).deviceID
	_, _ = a.st.exec(`DELETE FROM devices WHERE id=?`, id)
	a.dropDeviceSessions(id, 0)
	writeJSON(w, map[string]bool{"ok": true})
}
