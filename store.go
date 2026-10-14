package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS settings (k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS remotes (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE COLLATE NOCASE, kind TEXT NOT NULL, sealed BLOB NOT NULL, created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS files (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  parent TEXT NOT NULL,
  ext TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  is_dir INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL DEFAULT 0,
  mtime INTEGER NOT NULL DEFAULT 0,
  gen INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS files_parent ON files(parent);
CREATE INDEX IF NOT EXISTS files_mtime ON files(mtime);
CREATE INDEX IF NOT EXISTS files_kind ON files(kind, mtime);
CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts5(path, content='files', content_rowid='id', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS files_ai AFTER INSERT ON files BEGIN
  INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
END;
CREATE TRIGGER IF NOT EXISTS files_ad AFTER DELETE ON files BEGIN
  INSERT INTO files_fts(files_fts, rowid, path) VALUES ('delete', old.id, old.path);
END;
CREATE TRIGGER IF NOT EXISTS files_au AFTER UPDATE OF path ON files BEGIN
  INSERT INTO files_fts(files_fts, rowid, path) VALUES ('delete', old.id, old.path);
  INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
END;
CREATE TABLE IF NOT EXISTS changes (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  is_dir INTEGER NOT NULL DEFAULT 0,
  size INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS changes_ts ON changes(ts);
CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, created INTEGER, expires INTEGER, ip TEXT, ua TEXT);
CREATE TABLE IF NOT EXISTS tokens (id INTEGER PRIMARY KEY, name TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, created INTEGER, last_used INTEGER DEFAULT 0);
CREATE TABLE IF NOT EXISTS shares (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  is_dir INTEGER NOT NULL DEFAULT 0,
  mode TEXT NOT NULL DEFAULT 'link',
  label TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL,
  expires INTEGER NOT NULL DEFAULT 0,
  pw_hash TEXT NOT NULL DEFAULT '',
  max_dl INTEGER NOT NULL DEFAULT 0,
  dl INTEGER NOT NULL DEFAULT 0,
  revoked INTEGER NOT NULL DEFAULT 0,
  last_access INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS shares_path ON shares(path);
CREATE TABLE IF NOT EXISTS share_events (
  id INTEGER PRIMARY KEY,
  share_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL,
  ip TEXT NOT NULL DEFAULT '',
  ua TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS share_events_share ON share_events(share_id, ts);
`

type Store struct {
	db  *sql.DB
	wmu sync.Mutex // SQLite has one writer; serialize in Go instead of retrying on SQLITE_BUSY
}

func openStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=cache_size(-65536)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// migrations for databases created by older versions (errors mean "already applied")
	_, _ = db.Exec(`ALTER TABLE shares ADD COLUMN allow_upload INTEGER NOT NULL DEFAULT 0`)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS share_items (share_id TEXT NOT NULL, path TEXT NOT NULL, added INTEGER NOT NULL, PRIMARY KEY(share_id, path))`); err != nil {
		return nil, err
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS share_items_path ON share_items(path)`)
	if err := migrateUsers(db); err != nil {
		return nil, err
	}
	if _, err := db.Exec(deviceSchema + writeSchema); err != nil {
		return nil, err
	}
	// the database holds password hashes, 2FA secrets and the key that signs share
	// cookies: readable by Burrow's user only
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(f, 0o600)
	}
	s := &Store{db: db}
	if s.get("secret") == "" {
		s.set("secret", randToken(32))
	}
	return s, nil
}

func (s *Store) exec(q string, args ...any) (sql.Result, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.db.Exec(q, args...)
}

func (s *Store) get(k string) string {
	var v string
	_ = s.db.QueryRow(`SELECT v FROM settings WHERE k=?`, k).Scan(&v)
	return v
}

func (s *Store) set(k, v string) {
	_, _ = s.exec(`INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
}

func (s *Store) getJSON(k string, out any) {
	if v := s.get(k); v != "" {
		_ = json.Unmarshal([]byte(v), out)
	}
}

func (s *Store) setJSON(k string, v any) {
	b, _ := json.Marshal(v)
	s.set(k, string(b))
}

// randToken returns n random bytes as URL-safe base64 without padding.
func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const slugAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randSlug returns a share id without look-alike characters (no 0/O, 1/l/I).
func randSlug(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = slugAlphabet[int(b[i])%len(slugAlphabet)]
	}
	return string(b)
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func now() int64 { return time.Now().Unix() }
