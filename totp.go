package main

// Two-factor sign-in with authenticator-app codes (RFC 6238 TOTP: SHA-1, 6 digits,
// 30-second steps) plus single-use recovery codes. API tokens are unaffected.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func totpCode(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000)
}

// totpMatch returns the matching time step (current ±1) or -1.
func totpMatch(secretB32, code string, t time.Time) int64 {
	secret, err := b32.DecodeString(strings.ToUpper(secretB32))
	if err != nil || len(code) != 6 {
		return -1
	}
	step := t.Unix() / 30
	for d := int64(-1); d <= 1; d++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, uint64(step+d))), []byte(code)) == 1 {
			return step + d
		}
	}
	return -1
}

// checkSecondFactor accepts a current TOTP code (each time step only once) or an
// unused recovery code, which is then burned.
func (a *App) checkSecondFactor(u *User, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if u.totp == "" {
		return false
	}
	a.st.wmu.Lock() // serialize so two requests can't both use the same step or recovery code
	defer a.st.wmu.Unlock()
	if len(code) == 6 {
		step := totpMatch(u.totp, code, time.Now())
		if step < 0 {
			return false
		}
		res, err := a.st.db.Exec(`UPDATE users SET totp_last=? WHERE id=? AND totp_last < ?`, step, u.ID, step)
		if err != nil {
			return false
		}
		n, _ := res.RowsAffected()
		return n == 1 // 0 rows: replay of a code that was already used
	}
	var rec string
	_ = a.st.db.QueryRow(`SELECT totp_recovery FROM users WHERE id=?`, u.ID).Scan(&rec)
	var codes []string
	_ = json.Unmarshal([]byte(rec), &codes)
	h := sha(strings.ToLower(strings.ReplaceAll(code, "-", "")))
	for i, c := range codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(h)) == 1 {
			codes = append(codes[:i], codes[i+1:]...)
			b, _ := json.Marshal(codes)
			_, _ = a.st.db.Exec(`UPDATE users SET totp_recovery=? WHERE id=?`, string(b), u.ID)
			return true
		}
	}
	return false
}

func newRecoveryCodes() (plain []string, hashed []string) {
	const alpha = "abcdefghjkmnpqrstuvwxyz23456789"
	for i := 0; i < 10; i++ {
		b := make([]byte, 10)
		_, _ = rand.Read(b)
		for j := range b {
			b[j] = alpha[int(b[j])%len(alpha)]
		}
		s := string(b)
		plain = append(plain, s[:5]+"-"+s[5:])
		hashed = append(hashed, sha(s))
	}
	return
}

// POST /api/2fa/start: make a pending secret and return it with a QR code.
func (a *App) api2FAStart(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.TwoFA {
		httpErr(w, 409, "two-factor sign-in is already on")
		return
	}
	raw := make([]byte, 20)
	_, _ = rand.Read(raw)
	secret := b32.EncodeToString(raw)
	_, _ = a.st.exec(`UPDATE users SET totp_pending=? WHERE id=?`, secret, u.ID)
	issuer := "Burrow"
	if pu, err := url.Parse(a.st.get("public_url")); err == nil && pu.Host != "" {
		issuer = "Burrow (" + pu.Host + ")"
	}
	uri := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.PathEscape(issuer), url.PathEscape(u.Name), secret, url.QueryEscape(issuer))
	png, err := qrPNG(uri, 400)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"secret": secret, "uri": uri, "qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
}

// POST /api/2fa/enable {code}: confirm the app works, turn 2FA on, hand out recovery codes once.
func (a *App) api2FAEnable(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct{ Code string }
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	if u.pending == "" {
		httpErr(w, 400, "start setup first")
		return
	}
	step := totpMatch(u.pending, strings.TrimSpace(in.Code), time.Now())
	if step < 0 {
		httpErr(w, 400, "that code didn't match; check the time on your phone and try the next code")
		return
	}
	plain, hashed := newRecoveryCodes()
	b, _ := json.Marshal(hashed)
	_, _ = a.st.exec(`UPDATE users SET totp_secret=?, totp_pending='', totp_last=?, totp_recovery=? WHERE id=?`, u.pending, step, string(b), u.ID)
	writeJSON(w, map[string]any{"recovery_codes": plain})
}

// POST /api/2fa/disable {password, code}
func (a *App) api2FADisable(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var in struct{ Password, Code string }
	if err := readJSON(r, &in); err != nil {
		httpErr(w, 400, "bad request")
		return
	}
	key := "2fa:" + a.clientIP(r)
	if !a.limiter.allow(key, 8, 15*time.Minute) {
		httpErr(w, 429, "too many attempts; wait 15 minutes")
		return
	}
	if _, ok := a.checkPassword(u.Name, in.Password); !ok || !a.checkSecondFactor(u, in.Code) {
		a.limiter.fail(key)
		httpErr(w, 401, "password or code is wrong")
		return
	}
	_, _ = a.st.exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0, totp_recovery='[]' WHERE id=?`, u.ID)
	writeJSON(w, map[string]bool{"ok": true})
}
