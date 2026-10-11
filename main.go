// Burrow: a fast file index, browser and share-link server for a NAS.
package main

import (
	"context"
	"embed"
	"errors"
	"log"
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
	Addr           string        // listen address
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
		DataDir:      env("BURROW_DATA", "/data"),
		DropDir:      env("BURROW_DROP", "/drop"),
		StateDir:     env("BURROW_STATE", "/state"),
		ScanInterval: time.Duration(hours * float64(time.Hour)),
		Watch:        env("BURROW_WATCH", "true") != "false",
	}
	for _, p := range strings.Split(env("BURROW_TRUSTED_PROXIES", "127.0.0.1/32,::1/128,172.16.0.0/12,100.64.0.0/10"), ",") {
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
			resp, err := http.Get("http://" + addr + "/healthz")
			if err != nil || resp.StatusCode != 200 {
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
			println("backup written to " + os.Args[2])
			return
		case "reset-password":
			// burrow reset-password <username>: sets a temporary password and turns off that user's 2FA
			if len(os.Args) < 3 {
				log.Fatal("usage: burrow reset-password <username>")
			}
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
			_, _ = s.exec(`UPDATE users SET totp_secret='', totp_pending='', totp_last=0, totp_recovery='[]', disabled=0 WHERE id=?`, u.ID)
			a.dropSessions(u.ID)
			println("Temporary password for " + u.Name + ": " + tmp + "\nSign in and change it under Settings. Two-factor sign-in was turned off for this user.")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go app.idx.run(ctx)
	go app.maintain(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("burrow %s listening on %s (library %s)", version, cfg.Addr, cfg.DataDir)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	st.db.Close()
}

// maintain runs daily housekeeping: leftovers from interrupted uploads and thumbnails
// nobody has looked at in 90 days.
func (a *App) maintain(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		a.cleanPartials()
		a.pruneThumbs(90 * 24 * time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
