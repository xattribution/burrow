package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Requests through the bridge come from loopback; none of that may make them local.
func TestTetherRoutesAreNeverLocal(t *testing.T) {
	e := setup(t)
	d := pairedDevice(t, e, "Phone")
	srv := httptest.NewServer(e.app.tetherRoutes())
	defer srv.Close()

	// only what the public port serves
	if code, _ := publicGet(t, http.DefaultClient, srv.URL+"/api/ls?path=DOCS"); code != 404 {
		t.Fatalf("API through the bridge: %d", code)
	}
	if code, _ := publicGet(t, http.DefaultClient, srv.URL+"/favicon.svg"); code != 200 {
		t.Fatalf("favicon through the bridge: %d", code)
	}

	// a peer can't claim an address, and isn't loopback either
	req, _ := http.NewRequest("GET", srv.URL+"/favicon.svg", nil)
	req.Header.Set("X-Forwarded-For", "10.1.2.3")
	req.RemoteAddr = "127.0.0.1:5555"
	var seen string
	tetherWrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = e.app.clientIP(r)
		if !e.app.needsSetupKey(r) {
			t.Error("a bridged request skipped the setup key")
		}
	})).ServeHTTP(httptest.NewRecorder(), req)
	if seen != tetherPeerIP {
		t.Fatalf("client address through the bridge: %q", seen)
	}

	// the channel works and the device list says how the phone came in
	d.base = srv.URL
	if d.hello() != 200 {
		t.Fatal("hello through the bridge")
	}
	if r := d.do("GET", "/api/ls?path=DOCS", nil, nil); r.status != 200 {
		t.Fatalf("tunnel through the bridge: %+v", r)
	}
	var devs []Device
	_ = json.Unmarshal(e.do("GET", "/api/devices", nil, 200), &devs)
	if devs[0].LastVia != "tether" || devs[0].LastIP != tetherPeerIP {
		t.Fatalf("device list: %+v", devs[0])
	}
}

func TestTetherOffAndMissing(t *testing.T) {
	t.Setenv("BURROW_TETHER_BIN", "")
	t.Setenv("PATH", t.TempDir())
	e := setup(t)
	d := pairedDevice(t, e, "Phone")
	if r := d.do("POST", "/api/device/tether", nil, nil); r.status != 409 {
		t.Fatalf("invite while off: %+v", r)
	}
	var st tetherStatus
	_ = json.Unmarshal(e.do("GET", "/api/tether", nil, 200), &st)
	if st.Available || st.Running || st.Error == "" {
		t.Fatalf("status without the program: %+v", st)
	}
	if v, _ := d.info["tether"].(string); v != "" {
		t.Fatalf("server info while off: %q", v)
	}
}

// The real bridge, end to end: an admin turns it on, a phone gets an invite
// through its channel, joins from another tethersys node, and reaches Burrow
// through the forwarded port; removing the phone cuts it off. Needs
// BURROW_TEST_TETHER_BIN (a built burrow-tether), as CI sets.
func TestTetherBridge(t *testing.T) {
	bin := os.Getenv("BURROW_TEST_TETHER_BIN")
	if bin == "" {
		t.Skip("BURROW_TEST_TETHER_BIN not set")
	}
	t.Setenv("BURROW_TETHER_BIN", bin)
	t.Setenv("BURROW_TETHER_LOCAL", "1")
	e := setup(t)
	certs, err := newCertSource(e.app.cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.app.certs, e.app.tlsCfg = certs, tlsConfig(certs, false)
	t.Cleanup(e.app.tether.stop)
	d := pairedDevice(t, e, "Phone")

	e.do("PUT", "/api/settings", map[string]any{"tether": true}, 200)
	var id string
	for i := 0; i < 100 && id == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		id = e.app.tether.running()
	}
	if id == "" {
		t.Fatalf("the bridge didn't start: %+v", e.app.tether.status())
	}
	if d.hello() != 200 || d.info["tether"] != id {
		t.Fatalf("server info: %v", d.info["tether"])
	}

	r := d.do("POST", "/api/device/tether", nil, nil)
	var inv struct{ ID, Invite string }
	if r.status != 200 || json.Unmarshal(r.body, &inv) != nil || inv.ID != id || inv.Invite == "" {
		t.Fatalf("invite: %d %s", r.status, r.body)
	}

	// the phone's side
	cmd := exec.Command(bin, "connect", "--local", "--data", t.TempDir())
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Wait() })
	b, _ := json.Marshal(map[string]string{"invite": inv.Invite})
	_, _ = in.Write(append(b, '\n'))
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		got <- line
		_, _ = io.Copy(io.Discard, out)
	}()
	var fwd struct{ Port int }
	select {
	case line := <-got:
		if json.Unmarshal([]byte(line), &fwd) != nil || fwd.Port == 0 {
			t.Fatalf("connect said %q", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("connect didn't finish")
	}

	// Burrow's own certificate, checked by its pin as the app does; the name is 127.0.0.1
	pin := d.info["pins"].([]any)[0].(string)
	tc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if base64.RawURLEncoding.EncodeToString(sum[:]) != pin {
				return fmt.Errorf("certificate doesn't match the pin")
			}
			return nil
		},
	}, DisableKeepAlives: true}}
	base := fmt.Sprintf("https://127.0.0.1:%d", fwd.Port)
	if code, _ := publicGet(t, tc, base+"/favicon.svg"); code != 200 {
		t.Fatalf("favicon through tethersys: %d", code)
	}
	d.base, d.c = base, tc
	if d.hello() != 200 {
		t.Fatal("hello through tethersys")
	}
	if r := d.do("GET", "/api/ls?path=DOCS", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "notes.txt") {
		t.Fatalf("listing through tethersys: %+v", r)
	}
	var st tetherStatus
	_ = json.Unmarshal(e.do("GET", "/api/tether", nil, 200), &st)
	if !st.Running || st.Devices != 1 || st.ID != id {
		t.Fatalf("status: %+v", st)
	}

	// removing the phone removes it from the bridge too
	e.do("DELETE", "/api/devices/"+hex.EncodeToString(d.devID), nil, 200)
	cut := false
	for i := 0; i < 50 && !cut; i++ {
		time.Sleep(100 * time.Millisecond)
		_, err := tc.Get(base + "/favicon.svg")
		cut = err != nil
	}
	if !cut {
		t.Fatal("a removed phone still reaches Burrow through tethersys")
	}

	// turned off: no bridge, no invites
	e.do("PUT", "/api/settings", map[string]any{"tether": false}, 200)
	for i := 0; i < 50 && e.app.tether.running() != ""; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if e.app.tether.running() != "" {
		t.Fatal("the bridge is still running")
	}
}
