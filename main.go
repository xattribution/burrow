// Burrow: a fast file index, browser and share-link server for a NAS.
package main

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // zone database baked in; the image needs no tzdata package
)

//go:embed web
var webFS embed.FS

var version = "dev"

// Config comes from environment variables. Everything a user is expected to
// change lives in the settings table and is edited in the app itself.
type Config struct {
	Addr           string // listen address for the LAN: the whole app
	PublicAddr     string // listen address to expose to the internet: share links only, by default
	TLSCert        string // certificate and key files; a self-signed pair is made if unset
	TLSKey         string
	PQOnly         bool          // refuse clients without hybrid post-quantum key exchange
	DataDir        string        // read-only library root (e.g. the NAS mounted at /data)
	DropDir        string        // writable folder for file-drop uploads
	StateDir       string        // database, thumbnails, secrets
	ScanInterval   time.Duration // full rescan interval
	Watch          bool          // use inotify for live updates
	TrustedProxies []string      // CIDRs allowed to set X-Forwarded-For / -Proto
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func loadConfig() Config {
	hours, _ := strconv.ParseFloat(env("BURROW_SCAN_HOURS", "6"), 64)
	if hours <= 0 {
		hours = 6
	}
	c := Config{
		Addr:         env("BURROW_ADDR", ":47823"),
		PublicAddr:   env("BURROW_PUBLIC_ADDR", ":47824"),
		TLSCert:      env("BURROW_TLS_CERT", ""),
		TLSKey:       env("BURROW_TLS_KEY", ""),
		PQOnly:       env("BURROW_TLS_PQ_ONLY", "false") == "true",
		DataDir:      env("BURROW_DATA", "/data"),
		DropDir:      env("BURROW_DROP", "/drop"),
		StateDir:     env("BURROW_STATE", "/state"),
		ScanInterval: time.Duration(hours * float64(time.Hour)),
		Watch:        env("BURROW_WATCH", "true") != "false",
	}
	if strings.EqualFold(c.PublicAddr, "off") {
		c.PublicAddr = ""
	}
	// Only loopback by default: a reverse proxy elsewhere (another container, another
	// machine) must be listed, or anyone who can reach Burrow could claim any address.
	for _, p := range strings.Split(env("BURROW_TRUSTED_PROXIES", "127.0.0.1/32,::1/128"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.TrustedProxies = append(c.TrustedProxies, p)
		}
	}
	c.DataDir, _ = filepath.Abs(c.DataDir)
	c.DropDir, _ = filepath.Abs(c.DropDir)
	return c
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			println(version)
			return
		case "healthcheck":
			addr := env("BURROW_ADDR", ":47823")
			if strings.HasPrefix(addr, ":") {
				addr = "127.0.0.1" + addr
			}
			// loopback only, so the certificate (often self-signed) isn't checked
			c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			resp, err := c.Get("https://" + addr + "/healthz")
			if err != nil || resp.StatusCode != 200 {
				os.Exit(1)
			}
			return
		case "verify-public":
			// burrow verify-public https://share.example.com [https://share.example.com/s/<id>]
			if len(os.Args) < 3 {
				log.Fatal("usage: burrow verify-public <public https URL> [a share link]")
			}
			share := ""
			if len(os.Args) > 3 {
				share = os.Args[3]
			}
			if !verifyPublic(os.Stdout, os.Args[2], share) {
				os.Exit(1)
			}
			return
		case "backup":
			// burrow backup /state/backups/burrow-2026-10-11.db  (safe while running)
			if len(os.Args) < 3 {
				log.Fatal("usage: burrow backup <file>")
			}
			cfg := loadConfig()
			s, err := openStore(filepath.Join(cfg.StateDir, "burrow.db"))
			if err != nil {
				log.Fatal(err)
			}
			_ = os.MkdirAll(filepath.Dir(os.Args[2]), 0o750)
			_ = os.Remove(os.Args[2])
			if _, err := s.db.Exec(`VACUUM INTO ?`, os.Args[2]); err != nil {
				log.Fatal(err)
			}
			_ = os.Chmod(os.Args[2], 0o600) // holds password hashes and 2FA secrets
			println("backup written to " + os.Args[2])
			return
		case "reset-password":
			// burrow reset-password <username> [--admin]: account recovery. Sets a temporary
			// password, turns off 2FA, and ends every session and API token of that user.
			// --admin also makes the account an active admin (if no admin is left).
			if len(os.Args) < 3 {
				log.Fatal("usage: burrow reset-password <username> [--admin]")
			}
			makeAdmin := len(os.Args) > 3 && os.Args[3] == "--admin"
			cfg := loadConfig()
			s, err := openStore(filepath.Join(cfg.StateDir, "burrow.db"))
			if err != nil {
				log.Fatal(err)
			}
			a := &App{cfg: cfg, st: s}
			u, err := a.userByName(os.Args[2])
			if err != nil {
				log.Fatal("no user named " + os.Args[2])
			}
			tmp := randSlug(16)
			if err := a.setPassword(u.ID, tmp); err != nil {
				log.Fatal(err)
			}
			_, _ = s.exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0, totp_recovery='[]' WHERE id=?`, u.ID)
			_, _ = s.exec(`DELETE FROM tokens WHERE user_id=?`, u.ID)
			_, _ = s.exec(`DELETE FROM devices WHERE user_id=?`, u.ID)
			a.dropSessions(u.ID)
			if makeAdmin {
				_, _ = s.exec(`UPDATE users SET admin=1, disabled=0 WHERE id=?`, u.ID)
			}
			println("Temporary password for " + u.Name + ": " + tmp + "\nSign in and change it under Settings. Two-factor sign-in was turned off, and API tokens and paired phones were revoked for this user.")
			if a.adminCount() == 0 {
				println("There is no active admin. Run this again with --admin to make " + u.Name + " one.")
			} else if u.Disabled && !makeAdmin {
				println(u.Name + " is turned off; an admin can turn the account back on under Users.")
			}
			return
		}
	}

	cfg := loadConfig()
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		log.Fatalf("state dir: %v", err)
	}
	st, err := openStore(filepath.Join(cfg.StateDir, "burrow.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	app, err := newApp(cfg, st)
	if err != nil {
		log.Fatal(err)
	}

	certs, err := newCertSource(cfg)
	if err != nil {
		log.Fatal(err)
	}
	app.certs = certs
	tcfg := tlsConfig(certs, cfg.PQOnly)
	if c, _ := certs.get(nil); c != nil {
		src := "self-signed, " + certs.certFile
		if cfg.TLSCert != "" {
			src = cfg.TLSCert
		}
		log.Printf("tls: certificate %s (SHA-256 %s)", src, certFingerprint(c))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.idx.run(ctx)
	go app.maintain(ctx)

	newServer := func(h http.Handler) *http.Server {
		return &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    64 << 10,
			ErrorLog:          quietLog,
		}
	}
	type listener struct {
		name, addr string
		srv        *http.Server
	}
	listeners := []listener{{"LAN", cfg.Addr, newServer(app.routes())}}
	if cfg.PublicAddr != "" {
		listeners = append(listeners, listener{"public", cfg.PublicAddr, newServer(app.publicRoutes())})
	}
	errc := make(chan error, len(listeners))
	for _, l := range listeners {
		ln, err := net.Listen("tcp", l.addr)
		if err != nil {
			log.Fatalf("%s listener: %v", l.name, err)
		}
		log.Printf("burrow %s: %s port on %s (HTTPS only)", version, l.name, l.addr)
		go func(l listener) { errc <- serveTLSOnly(l.srv, ln, tcfg) }(l)
	}
	log.Printf("library %s", cfg.DataDir)
	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Print(err)
		}
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, l := range listeners {
		_ = l.srv.Shutdown(sctx)
	}
	st.db.Close()
}

// maintain runs daily housekeeping: leftovers from interrupted uploads, the trash
// after 30 days, and thumbnails nobody has looked at in 90 days.
func (a *App) maintain(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		a.cleanPartials()
		a.purgeTrash()
		a.pruneThumbs(90 * 24 * time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
