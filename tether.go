package main

// Reach from anywhere: Burrow over tethersys, so phones reach it away from home
// without a port forwarded on the router or a reverse proxy.
//
// An admin turns it on in Settings. Burrow then runs burrow-tether (a small Rust
// program next to it) and talks to it over stdin and stdout, one JSON object per
// line, so there is no control port. burrow-tether forwards streams from its
// trusted peers to a loopback TLS listener here that serves what the public port
// serves: the phone channel and share links. The phone's channel is end-to-end
// encrypted and pinned inside that, so tethersys only decides who may knock.
//
// Each phone gets a one-use invite through its channel, tagged with its device
// id. Removing the device (or its user) removes the peer: reconcile drops every
// tagged peer whose device is gone, so even the offline reset-password command
// is caught up with at the next start.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const tetherTagPrefix = "device:"

// tetherPeerIP stands in for the address of requests that came through the bridge.
// It is in 100::/64, the IPv6 discard-only range: not loopback, not private and
// never a trusted proxy, so nothing treats bridge traffic as local.
const tetherPeerIP = "100::7e7e"

type tetherKey struct{}

func viaTether(r *http.Request) bool {
	v, _ := r.Context().Value(tetherKey{}).(bool)
	return v
}

type tetherStatus struct {
	Available bool   `json:"available"` // burrow-tether is installed
	On        bool   `json:"on"`        // an admin turned it on
	Running   bool   `json:"running"`
	ID        string `json:"id,omitempty"` // this Burrow's tethersys id
	Online    bool   `json:"online"`       // connected to a relay, so reachable from anywhere
	Devices   int    `json:"devices"`      // phones that can reach it this way
	Restarts  int    `json:"restarts,omitempty"`
	Error     string `json:"error,omitempty"`
}

type tetherPeer struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type tether struct {
	a   *App
	bin string

	mu       sync.Mutex
	on       bool
	cancel   context.CancelFunc
	done     chan struct{}
	stdin    io.WriteCloser
	id       string
	pending  map[uint64]chan map[string]any
	next     uint64
	lastErr  string
	restarts int

	lnOnce sync.Once
	lnAddr string
	lnErr  error
}

// tetherBinary finds burrow-tether: BURROW_TETHER_BIN, the PATH, or next to burrow.
func tetherBinary() string {
	if v := strings.TrimSpace(os.Getenv("BURROW_TETHER_BIN")); v != "" {
		return v
	}
	if p, err := exec.LookPath("burrow-tether"); err == nil {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "burrow-tether")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func newTether(a *App) *tether {
	return &tether{a: a, bin: tetherBinary(), pending: map[uint64]chan map[string]any{}}
}

// set turns the bridge on or off. Turning it off closes its stdin, so it shuts
// down cleanly; peers stay in its trust list for when it is turned back on.
func (t *tether) set(on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.on = on
	if on && t.cancel == nil && t.bin != "" {
		ctx, cancel := context.WithCancel(context.Background())
		t.cancel, t.done = cancel, make(chan struct{})
		go t.supervise(ctx, t.done)
	}
	if !on && t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

// stop turns the bridge off and waits for it to exit (Burrow is shutting down).
func (t *tether) stop() {
	t.mu.Lock()
	done := t.done
	t.mu.Unlock()
	t.set(false)
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// listener starts, once, the loopback TLS listener the bridge forwards to.
func (t *tether) listener() (string, error) {
	t.lnOnce.Do(func() {
		cfg := t.a.tlsCfg
		if cfg == nil {
			t.lnErr = errors.New("no TLS configuration")
			return
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.lnErr = err
			return
		}
		t.lnAddr = ln.Addr().String()
		srv := &http.Server{Handler: t.a.tetherRoutes(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: quietLog}
		go func() { _ = serveTLSOnly(srv, ln, cfg) }()
	})
	return t.lnAddr, t.lnErr
}

func (t *tether) supervise(ctx context.Context, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := t.run(ctx)
		if ctx.Err() != nil {
			return
		}
		t.mu.Lock()
		t.restarts++
		if err != nil {
			t.lastErr = err.Error()
		}
		t.mu.Unlock()
		log.Printf("tether: bridge stopped (%v); starting it again in %s", err, backoff)
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// run starts burrow-tether once and serves its replies until it exits.
func (t *tether) run(ctx context.Context) error {
	target, err := t.listener()
	if err != nil {
		return err
	}
	dir := filepath.Join(t.a.cfg.StateDir, "tether")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	args := []string{"serve", "--target", target, "--data", dir}
	if p := strings.TrimSpace(os.Getenv("BURROW_TETHER_PORT")); p != "" {
		args = append(args, "--port", p)
	}
	if os.Getenv("BURROW_TETHER_LOCAL") == "1" { // tests: no relays, no discovery
		args = append(args, "--local")
	}
	cmd := exec.Command(t.bin, args...)
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "BURROW_TETHER_LOG="+env("BURROW_TETHER_LOG", "warn"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	rctx, rcancel := context.WithCancel(ctx)
	defer rcancel()
	go func() {
		select {
		case <-ctx.Done():
			_ = stdin.Close() // asks it to shut down
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
			}
		case <-exited:
		}
	}()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m["ready"] == true {
			id, _ := m["id"].(string)
			t.mu.Lock()
			t.stdin, t.id, t.lastErr = stdin, id, ""
			t.mu.Unlock()
			log.Printf("tether: reachable from anywhere as %s", shortID(id))
			go t.reconcileEvery(rctx)
			continue
		}
		n, _ := m["id"].(float64)
		t.mu.Lock()
		ch := t.pending[uint64(n)]
		delete(t.pending, uint64(n))
		t.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	t.mu.Lock()
	t.stdin, t.id = nil, ""
	for k, ch := range t.pending {
		close(ch)
		delete(t.pending, k)
	}
	t.mu.Unlock()
	return <-exited
}

var errTetherOff = errors.New("reach from anywhere is off on this Burrow")

// call sends one command to the bridge and waits for its reply.
func (t *tether) call(op string, fields map[string]any) (map[string]any, error) {
	t.mu.Lock()
	if t.stdin == nil {
		t.mu.Unlock()
		return nil, errTetherOff
	}
	t.next++
	id := t.next
	ch := make(chan map[string]any, 1)
	t.pending[id] = ch
	msg := map[string]any{"id": id, "op": op}
	for k, v := range fields {
		msg[k] = v
	}
	b, _ := json.Marshal(msg)
	_, err := t.stdin.Write(append(b, '\n'))
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, errors.New("the bridge stopped")
		}
		if e, _ := m["error"].(string); e != "" {
			return nil, errors.New(e)
		}
		return m, nil
	case <-time.After(10 * time.Second):
		t.mu.Lock()
		delete(t.pending, id)
		t.mu.Unlock()
		return nil, errors.New("the bridge didn't answer")
	}
}

// running is this Burrow's tethersys id while the bridge is up, else "".
func (t *tether) running() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin == nil {
		return ""
	}
	return t.id
}

// invite makes a one-use invite for a device. It is a credential: it goes back
// through the device's encrypted channel and is never logged.
func (t *tether) invite(deviceID string) (id, invite string, err error) {
	m, err := t.call("invite", map[string]any{"tag": tetherTagPrefix + deviceID, "ttl": 600})
	if err != nil {
		return "", "", err
	}
	invite, _ = m["invite"].(string)
	if invite == "" {
		return "", "", errors.New("the bridge made no invite")
	}
	return t.running(), invite, nil
}

func (t *tether) peers() ([]tetherPeer, bool, error) {
	m, err := t.call("status", nil)
	if err != nil {
		return nil, false, err
	}
	var out struct {
		Online bool         `json:"online"`
		Peers  []tetherPeer `json:"peers"`
	}
	b, _ := json.Marshal(m)
	_ = json.Unmarshal(b, &out)
	return out.Peers, out.Online, nil
}

// reconcile removes peers whose device no longer exists. It runs when the bridge
// starts, after a device is removed, and every few minutes.
func (t *tether) reconcile() {
	if t == nil || t.running() == "" {
		return
	}
	ps, _, err := t.peers()
	if err != nil {
		return
	}
	for _, p := range ps {
		for _, tag := range p.Tags {
			dev, ok := strings.CutPrefix(tag, tetherTagPrefix)
			if ok && !t.a.deviceExists(dev) {
				if _, err := t.call("forget", map[string]any{"tag": tag}); err == nil {
					log.Printf("tether: removed %s, whose device is gone", shortID(p.ID))
				}
			}
		}
	}
}

func (t *tether) reconcileEvery(ctx context.Context) {
	tk := time.NewTicker(10 * time.Minute)
	defer tk.Stop()
	for {
		t.reconcile()
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

func (t *tether) status() tetherStatus {
	t.mu.Lock()
	s := tetherStatus{Available: t.bin != "", On: t.on, Error: t.lastErr, Restarts: t.restarts}
	t.mu.Unlock()
	if id := t.running(); id != "" {
		s.Running, s.ID = true, id
		if ps, online, err := t.peers(); err == nil {
			s.Online = online
			for _, p := range ps {
				for _, tag := range p.Tags {
					if strings.HasPrefix(tag, tetherTagPrefix) {
						s.Devices++
						break
					}
				}
			}
		}
	}
	if !s.Available {
		s.Error = "burrow-tether isn't installed next to Burrow. The Docker image includes it."
	}
	return s
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// tetherRoutes is what the bridge forwards to: the public port's routes, with
// requests marked as coming through tethersys. The bridge connects from loopback,
// so the address is replaced and forwarded headers dropped: nothing a peer sends
// can make a request look local or spoof a visitor's address.
func (a *App) tetherRoutes() http.Handler { return tetherWrap(a.publicRoutes()) }

func tetherWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = net.JoinHostPort(tetherPeerIP, "0")
		for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-IP", "Forwarded"} {
			r.Header.Del(h)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tetherKey{}, true)))
	})
}

// ---- API ----

// apiTether is the admin's view in Settings.
func (a *App) apiTether(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.tether.status())
}

// apiDeviceTether gives a paired phone a one-use invite to this Burrow's bridge.
func (a *App) apiDeviceTether(w http.ResponseWriter, r *http.Request) {
	id, inv, err := a.tether.invite(authOf(r).deviceID)
	if err != nil {
		if errors.Is(err, errTetherOff) {
			httpErr(w, 409, err.Error())
			return
		}
		httpErr(w, 503, "the bridge is starting; try again in a minute")
		return
	}
	writeJSON(w, map[string]string{"id": id, "invite": inv})
}
