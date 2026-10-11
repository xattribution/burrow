package main

// burrow verify-public: checks a deployment from the outside, the way a visitor
// or an attacker on the internet would see it. Run it from any machine:
//
//	docker run --rm ghcr.io/xattribution/burrow verify-public https://share.example.com [share link]
//
// It only sends ordinary requests (no load, no password guessing).

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// verifyRoots overrides the system trust store (tests only).
var verifyRoots *x509.CertPool

type verifier struct {
	out           io.Writer
	failed        bool
	client, plain *http.Client
}

func (v *verifier) report(level, msg string, args ...any) {
	if level == "FAIL" {
		v.failed = true
	}
	fmt.Fprintf(v.out, "%-4s  %s\n", level, fmt.Sprintf(msg, args...))
}

func verifyPublic(out io.Writer, base, share string) bool {
	v := &verifier{out: out}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	v.client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: verifyRoots}, ForceAttemptHTTP2: true}}
	v.plain = &http.Client{Timeout: 10 * time.Second, CheckRedirect: noRedirect}

	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" {
		v.report("FAIL", "not a URL: %s", base)
		return false
	}
	if u.Scheme != "https" {
		v.report("FAIL", "the public address must be https://, not %s://", u.Scheme)
		return false
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	fmt.Fprintf(out, "Checking %s\n\n", u)

	v.checkTLS(host, u.Hostname())
	v.checkPlainHTTP(u)
	v.checkSurface(u)
	v.checkTraversal(u)
	if share != "" {
		v.checkShare(share)
	} else {
		v.report("INFO", "add a share link as a second argument to check a share page's headers too")
	}
	fmt.Fprintln(out)
	if v.failed {
		fmt.Fprintln(out, "Some checks FAILED. Fix those before relying on this address.")
	} else {
		fmt.Fprintln(out, "No failures.")
	}
	return !v.failed
}

func (v *verifier) checkTLS(addr, name string) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: name, RootCAs: verifyRoots})
	if err != nil {
		v.report("FAIL", "TLS: %v", err)
		return
	}
	st := conn.ConnectionState()
	conn.Close()
	v.report("PASS", "certificate is valid for %s (issued by %s)", name, st.PeerCertificates[0].Issuer.CommonName)
	if left := time.Until(st.PeerCertificates[0].NotAfter); left < 14*24*time.Hour {
		v.report("WARN", "certificate expires in %d days", int(left.Hours()/24))
	}
	if st.Version == tls.VersionTLS13 {
		v.report("PASS", "TLS 1.3")
	} else {
		v.report("WARN", "negotiated %s, not TLS 1.3", tls.VersionName(st.Version))
	}
	if slices.Contains(pqGroups, st.CurveID) {
		v.report("PASS", "key exchange %s (hybrid post-quantum)", st.CurveID)
	} else {
		v.report("WARN", "key exchange %s: strong today, but not post-quantum. The proxy's TLS library needs OpenSSL 3.5+ for X25519MLKEM768", st.CurveID)
	}
	// Old protocol versions must be refused outright.
	for _, ver := range []uint16{tls.VersionTLS11, tls.VersionTLS10} {
		c, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: name, RootCAs: verifyRoots, MinVersion: ver, MaxVersion: ver})
		if err == nil {
			c.Close()
			v.report("FAIL", "%s is still accepted", tls.VersionName(ver))
		}
	}
}

// checkPlainHTTP: plain HTTP must never return content, only a redirect or an error.
func (v *verifier) checkPlainHTTP(u *url.URL) {
	targets := []string{"http://" + u.Hostname() + "/", "http://" + u.Hostname() + "/s/verify-public-probe"}
	if u.Port() != "" {
		targets = append(targets, "http://"+u.Host+"/") // plain HTTP sent to the HTTPS port itself
	}
	for _, t := range targets {
		resp, err := v.plain.Get(t)
		if err != nil {
			v.report("PASS", "%s: no plain-HTTP service", t)
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			loc := resp.Header.Get("Location")
			if strings.HasPrefix(loc, "https://") {
				v.report("PASS", "%s redirects to https", t)
			} else {
				v.report("FAIL", "%s redirects to %q, not https", t, loc)
			}
		case resp.StatusCode >= 400:
			v.report("PASS", "%s is refused (%d)", t, resp.StatusCode)
		default:
			v.report("FAIL", "%s answers %d over plain HTTP", t, resp.StatusCode)
		}
	}
}

func (v *verifier) get(u string) (*http.Response, string, error) {
	resp, err := v.client.Get(u)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, string(b), nil
}

// checkSurface: by default only share links are public. Anything else answering
// means the web UI (or MCP) was turned on for the public port, or the proxy points
// at the LAN port instead of the public one.
func (v *verifier) checkSurface(u *url.URL) {
	base := u.String()
	resp, _, err := v.get(base + "/s/verify-public-probe")
	if err != nil {
		v.report("FAIL", "can't reach %s: %v", base, err)
		return
	}
	if resp.Header.Get("Strict-Transport-Security") != "" {
		v.report("PASS", "HSTS is on")
	} else {
		v.report("WARN", "no Strict-Transport-Security header; turn on HSTS in the proxy or update Burrow")
	}
	uiOpen := false
	for _, p := range []string{"/", "/api/state", "/api/ls?path=", "/api/users", "/assets/app.js", "/healthz"} {
		resp, body, err := v.get(base + p)
		if err != nil {
			continue
		}
		if resp.StatusCode == 404 {
			continue
		}
		uiOpen = true
		if p == "/api/state" && strings.Contains(body, `"setup_done":false`) {
			v.report("FAIL", "first-launch setup is reachable from the internet: finish setup on the LAN first")
		}
		v.report("WARN", "%s answers %d from the internet", p, resp.StatusCode)
	}
	if uiOpen {
		v.report("WARN", "the web UI or API is public. If you didn't turn on Settings > Public port > web UI, point the proxy at the public port (47824), not the LAN port")
	} else {
		v.report("PASS", "web UI, API and health check are not reachable from the internet")
	}
	req, _ := http.NewRequest("POST", base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if resp, err := v.client.Do(req); err == nil {
		resp.Body.Close()
		if resp.StatusCode == 404 {
			v.report("PASS", "MCP is not reachable from the internet")
		} else {
			v.report("INFO", "MCP is public (%d); it still needs an API token", resp.StatusCode)
		}
	}
}

// checkTraversal: path tricks must not reach anything outside a share.
func (v *verifier) checkTraversal(u *url.URL) {
	base := u.String()
	probes := []string{
		"/s/verify-public-probe/raw?p=../../../../etc/passwd",
		"/s/verify-public-probe/raw?p=..%2f..%2f..%2fetc%2fpasswd",
		"/s/..%2f..%2fetc%2fpasswd",
		"/s/%2e%2e/%2e%2e/etc/passwd",
		"/assets/..%2f..%2f..%2fetc%2fpasswd",
		"/assets/..%2fapp.js",
		"/assets/%2e%2e%2fmain.go",
	}
	bad := 0
	for _, p := range probes {
		resp, body, err := v.get(base + p)
		if err != nil {
			continue
		}
		if resp.StatusCode == 200 && (strings.Contains(body, "root:") || strings.Contains(body, "package main") || strings.Contains(body, "'use strict'")) {
			bad++
			v.report("FAIL", "%s returned a file it shouldn't", p)
		}
	}
	if bad == 0 {
		v.report("PASS", "%d path-traversal attempts reached nothing", len(probes))
	}
}

func (v *verifier) checkShare(link string) {
	resp, _, err := v.get(link)
	if err != nil {
		v.report("FAIL", "share link: %v", err)
		return
	}
	if resp.StatusCode != 200 && resp.StatusCode != 401 {
		v.report("WARN", "share link answers %d (expired, turned off or wrong?)", resp.StatusCode)
	}
	h := resp.Header
	want := map[string]string{
		"Content-Security-Policy": "script-src 'self'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"X-Robots-Tag":            "noindex",
		"Cache-Control":           "no-store",
	}
	for k, sub := range want {
		if strings.Contains(h.Get(k), sub) {
			v.report("PASS", "share page %s: %s", k, h.Get(k))
		} else {
			v.report("FAIL", "share page %s is %q, expected it to contain %q", k, h.Get(k), sub)
		}
	}
	for _, c := range resp.Cookies() {
		if !c.Secure || !c.HttpOnly {
			v.report("FAIL", "cookie %s is missing Secure or HttpOnly", c.Name)
		}
	}
}
