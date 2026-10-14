# Burrow design

How Burrow works and why: architecture, data model, security model, and the planned features.

---

## 1. What Burrow is for

Burrow is a self-hosted file index and share-link server for a NAS. It is built for:
- fast indexing and search of a large library
- a log of file changes
- share links with fine-grained control: expiry, password, download limits, QR codes
- shared folders whose contents can change while the link stays the same
- several users, each with their own folder access
- MCP access, so LLM clients can search and share
- speed and low overhead

It takes ideas from copyparty and FileBrowser Quantum, and keeps the footprint small: one binary, one SQLite file, the library mounted read-only.

### Project conventions
- **Docker:** quick start at the top of the README; required config is done in a first-launch screen inside the app; a high, uncommon port (**47823**); install under `~/<project>`, not `/opt`; single-line install, update and uninstall commands; backup and restore.
- **UI:** see section 13.
- **Writing:** American spelling, short plain sentences.
- **Data** is shown as charts rather than prose.

---

## 2. Repository layout

`main` is the released version. CI builds and publishes `ghcr.io/xattribution/burrow:latest` on every push to it.

```
main.go          entry point, env config, CLI subcommands (version, healthcheck, backup, reset-password)
store.go         SQLite open, schema, migrations, settings key/value, random helpers
indexer.go       filesystem scanner, inotify watcher, change log
files.go         path resolution and safety, directory listing, search, change queries
auth.go          client IP and proxy trust, HTTPS detection, rate limiter, sessions, tokens, route guards
users.go         users, folder grants, the permission checks and their SQL form, user admin API, upgrade from single-admin
api.go           HTTP routes and API handlers, settings, tokens, favorites
shares.go        share links, file drops, public share pages, zip streaming, download counting
collections.go   "shared folders": one link with editable contents
totp.go          two-factor sign-in (TOTP + recovery codes)
thumbs.go        image thumbnails with EXIF orientation and cache
mcp.go           Model Context Protocol endpoint (JSON-RPC over HTTP)
tls.go           TLS 1.3 on every port, certificates, plain-HTTP handling
verify.go        `burrow verify-public`: checks a deployment from outside
devchan.go       the phone channel's cryptography: handshakes, sealed segments
devices.go       pairing, paired devices, the channel endpoints and the tunnel
writes.go        new folders, moves, delete to trash, resumable uploads, sync listings
*_test.go        integration tests through real HTTP (httptest): permissions, security, devices, writes
web/             embedded UI: index.html, app.js, app.css, base.css, share.html, share.js, share.css, fonts, favicon
android/         the phone app: shared/ (Kotlin Multiplatform core) and app/ (Jetpack Compose); see section 16
deploy/truenas-app.yaml   TrueNAS "Install via YAML" template
compose.yaml, install.sh, Dockerfile, .github/workflows/release.yml
```

---

## 3. Architecture and core choices

**One Go binary, one SQLite file, and a web UI embedded in the binary.**

| Choice | Why |
|---|---|
| **Go, not Rust** | The workload is disk-bound: walking ZFS metadata, SQLite queries, streaming files. Rust's speed edge wouldn't be visible. Go builds faster and has an excellent standard HTTP server and simple concurrency, which makes iterating faster. Rust would only save about 25 MB of RAM, which doesn't matter on a NAS. |
| **SQLite through `modernc.org/sqlite` (pure Go, no cgo)** | It makes a static binary and a tiny container (34 MB image). WAL mode. One Go mutex serializes writers (`Store.wmu`) instead of retrying on SQLITE_BUSY. |
| **FTS5 with the `trigram` tokenizer** on the full path | It gives substring search ("tax" matches "2024-taxes.pdf" and folder names) in milliseconds. Terms under 3 characters fall back to `LIKE`. |
| **Library mounted read-only into the container** | The kernel guarantees Burrow can't modify or delete library files, whatever bugs it has. Only `/drop` (uploads), `/state` (database, thumbnails) and folders deliberately mounted writable on top of the library can change. |
| **Browsing reads the disk directly; search reads the index** | Folder listings are never stale. Search is instant. Browsing a folder also queues a quick rescan of it, so the index heals itself. |
| **Vanilla JS UI, no framework or build step** | Small, fast and dependency-free. All app state is kept in the URL query (`?view=&path=&q=&sel=&share=&tab=…`), so the back button and links work. |
| **Share links over HTTPS, not FTP** | FTP sends passwords in clear text, needs extra ports that reverse proxies can't pass, and needs client apps. An HTTPS link works in any browser. |

### Configuration
Environment variables cover plumbing only. Everything a user changes lives in the `settings` table and is edited in the app.

| Env | Default | Meaning |
|---|---|---|
| `BURROW_ADDR` | `:47823` | LAN port: the whole app |
| `BURROW_PUBLIC_ADDR` | `:47824` | public port: share links only (`off` disables it) |
| `BURROW_TLS_CERT`, `BURROW_TLS_KEY` | unset | certificate files, reloaded when they change; unset means a self-signed pair in `state/tls` |
| `BURROW_TLS_PQ_ONLY` | `false` | refuse clients without hybrid post-quantum key exchange |
| `BURROW_DATA` | `/data` | library root (read-only mount) |
| `BURROW_DROP` | `/drop` | writable upload folder |
| `BURROW_STATE` | `/state` | database and thumbnail cache |
| `BURROW_SCAN_HOURS` | `6` | full rescan interval |
| `BURROW_WATCH` | `true` | inotify live updates |
| `BURROW_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128` | peers allowed to set X-Forwarded-For; the reverse proxy's address must be added |

Settings keys: `public_url` (must be https), `lan_url` (the home address phones are given; must be https), `public_ui` and `public_mcp` (add the web UI or MCP to the public port), `roots` (JSON list of enabled top-level folders), `excludes` (newline globs), `default_expiry_hours` (default 168), `max_upload_mb` (default 20480), `admin_cidrs`, `secret` (HMAC key, generated), `setup_done`, `initial_scan_done`. Versions before multi-user kept the one admin account in `admin_user`, `admin_hash` and the `totp_*` keys. The upgrade moves that account into the `users` table as user 1 and deletes those keys.

---

## 4. Data model (SQLite)

| Table | Purpose |
|---|---|
| `settings(k,v)` | key/value configuration |
| `files(id, path UNIQUE, name, parent, ext, kind, is_dir, size, mtime, gen)` | the index. `path` is library-relative with `/` separators and no leading slash, e.g. `DOCUMENTS/Taxes/2024.pdf`. The first segment is the "root" (library). Indexes on parent, mtime, (kind, mtime) |
| `files_fts` | FTS5 trigram over `path`, kept in sync by triggers |
| `changes(id, ts, kind, path, is_dir, size)` | change log: added, modified or deleted. Pruned after 180 days or 500k rows |
| `users(id AUTOINCREMENT, username UNIQUE NOCASE, pw_hash, admin, disabled, created, totp_secret, totp_pending, totp_last, totp_recovery)` | accounts. AUTOINCREMENT so a deleted user's id is never reused (a new user would otherwise inherit the old one's links) |
| `grants(user_id, path, can_read, can_write, can_share)` | folder access per user, primary key (user_id, path). See section 12 |
| `sessions(id=sha256(token), created, expires, ip, ua, user_id)` | browser sessions, 30 days |
| `tokens(id, name, hash UNIQUE, created, last_used, user_id, last_ip)` | API/MCP bearer tokens, stored as a sha256 hash and shown once. `last_used` is written at most once a minute |
| `shares(id, path, is_dir, mode, label, note, created, expires, pw_hash, max_dl, dl, revoked, last_access, allow_upload, owner_id)` | modes are `link`, `drop` and `collection` |
| `share_items(share_id, path, added)` | contents of collections (shared folders) |
| `share_events(id, share_id, ts, kind, ip, ua, detail)` | link activity: view, download, zip, upload, password-fail. Pruned after 1 year |
| `favs(user_id, path, created)` | sidebar pins, per user. Replaced the single-user `favorites` table, which the upgrade copies and drops |
| `devices(id, user_id, name, platform, model, app_version, key, created, last_seen, last_ip, last_via)` | paired phones. `key` is the 32-byte device key the channel is built on (the database file is mode 0600) |
| `trash(id, user_id, path, stored, is_dir, size, deleted)` | what was deleted, where it waits (`<mount>/.burrow-trash/<id>/<name>`), kept 30 days |
| `uploads(id, user_id, dir, name, size, mtime, sha256, conflict, expect_size, expect_mtime, off, state, created, updated)` | resumable uploads in progress. `state` is the SHA-256 state at `off`, so a resumed upload is still checked end to end. Dropped after 7 days untouched |
| `file_hashes(path, size, mtime, sha256)` | SHA-256 cache, valid while size and mtime (ms) match |

Migrations are idempotent `ALTER TABLE … ADD COLUMN` statements whose "duplicate column" errors are ignored.

---

## 5. Indexer (`indexer.go`)

- **Per-directory reconciliation (`scanDir`).** It reads the directory and loads the children rows already in the database for that parent with one indexed query. It then compares them in memory, so it only writes rows that are new, changed or gone. A rescan of 250k unchanged files takes about 1.4 s because it's almost all reads.
- **Change detection** is by size or mtime. Files that disappear are deleted along with their whole subtree, using the range `path >= p+"/" AND path < p+"0"` because `'0'` is the character after `'/'`.
- **Change logging is off during the very first index** so the log isn't flooded with "added" entries. It turns on after `initial_scan_done=1`.
- **Writes are batched** (2,000 operations per transaction), taking the write lock briefly and often.
- **Live updates use fsnotify (inotify).** There's a watch per directory. Events mark the parent directory "dirty", and a 2 s ticker does a shallow rescan of dirty directories that only descends into new folders. If the inotify limit is hit (ENOSPC), the status shows a note and those folders rely on the periodic full rescan. Dirty processing is skipped while a full scan runs.
- **Symlinks are never followed or indexed.** Only regular files and directories are indexed.
- **Excluded names** (default list): `.zfs`, `.hist`, `@eaDir`, `#recycle`, `#snapshot`, `.Trash-*`, `$RECYCLE.BIN`, `System Volume Information`, `.DS_Store`, `._*`, `Thumbs.db`, `desktop.ini`, `.ix-apps`, `lost+found`. They're glob-matched against each path segment and editable in Settings.
- **Kinds by extension:** image, video, audio, doc, archive, code, `3d` (stl, 3mf, step, f3d, gcode, scad…), and `folder`.

---

## 6. Path safety (`files.go`)

`cleanRel` normalizes a path: backslashes become `/`, `path.Clean("/"+p)`, leading `/` stripped. So `..` can never climb above the library.

`resolve(rel)` refuses a path unless:
1. its first segment is an enabled root,
2. no segment is excluded,
3. `filepath.EvalSymlinks(abs)` equals the expected path, which means no symlink anywhere in it.

4. every segment is spelled exactly as on disk (`exactName`): the index answers for indexed paths, and directory listings answer for the rest. On a case-insensitive or Unicode-normalizing dataset, `DOCS/hr` would otherwise open `DOCS/HR` and slip past a No-access grant on `DOCS/HR`. Hidden-name patterns also match case-insensitively.

Every file access goes through `resolve`. Share access additionally checks that the result stays inside the shared path.

---

## 7. Search (`files.go: search`)

- **Every word must appear in the path.** Words of 3 or more characters go to FTS (`"word" AND "word2"`). Shorter words become `lower(path) LIKE %w%`.
- **Operators:** `-word` excludes, `ext:pdf` filters by extension, `type:video` filters by kind.
- **Filters:** kind, folder prefix, modified after, minimum and maximum size, dirs only.
- **Results are limited to enabled roots.**
- **Ranking:** name contains the term first, then exact name, then shorter path, then newest. Other sorts are newest, oldest, largest and name.
- **Limit is 300 in the UI.** When the limit is hit, the total is counted up to 10,000 and shown as "10,000+".
- **Measured on 250k files:** a specific query takes 10 ms, 6.3k matches take 47 ms, and 47k matches take 76 ms.

---

## 8. Authentication and web security (`auth.go`, `api.go`)

- **First-launch setup** happens in the UI. It asks for the **setup key** printed in the container log, then an admin username and password (10+ characters), the public URL, and which top-level folders to enable. Only a browser on the server itself (a loopback peer with no forwarded headers) skips the key: addresses can't prove a request is local, because a TCP proxy makes internet traffic look like the LAN. The key is made whenever setup is pending, so it's never empty. Setup takes a mutex so two requests can't race. `/api/roots/available` (the folder names) answers only with the key before setup, and only admins after.
- **Client IP:** `X-Forwarded-For` is used only when the direct peer is in `BURROW_TRUSTED_PROXIES` (loopback by default). It's walked right-to-left past trusted hops, and a malformed entry voids the header. A private-address peer that sends forwarded headers without being trusted gets a one-time log line suggesting it be added.
- **Cookies** are always Secure, since every connection is TLS.
- **Session cookie:** HttpOnly, SameSite=Lax, 30 days, stored as sha256.
- **CSRF:** every non-GET request authenticated by cookie, plus sign-in, sign-out and setup, must carry `X-Burrow: 1`. A cross-site form can't set that header.
- **Rate limits** are kept in memory. `limiter.take` records an attempt and checks the limit in one step, before the guess is evaluated, so parallel requests can't slip under the limit; a successful attempt is refunded. IPv6 clients are keyed by their /64.

  | Guess | Limit |
  |---|---|
  | Sign-in | 8 per 15 min per address, 30 per hour per account |
  | 2FA code | 10 per 15 min per account |
  | Turning 2FA on or off, password change | 8 per 15 min per account |
  | Share password | 10 per 15 min per address, 50 per hour per link |
  | Unknown link id | 30 per 15 min per address |
- **Two-factor sign-in** is per user; turning it on needs the password, so a stolen session can't lock the owner out and follows RFC 6238: SHA-1, 6 digits, 30 s steps, ±1 step. Each step can be used once (`users.totp_last`, updated with a conditional UPDATE under the write lock, so two racing requests can't both use one code). There are 10 single-use recovery codes, stored hashed. Enrollment works by: start (pending secret plus a QR code of the `otpauth://` URI), then confirm with a code, then recovery codes are shown once.
- **Sign-in** is per user. Wrong usernames take as long as wrong passwords (a dummy bcrypt compare), and both get the same message. Changing your password needs the current one (8 tries per 15 min per user), then signs out your other sessions.
- **API tokens** (prefix `brw_`) belong to the user who made them and act as that user, with the same folder grants. They **cannot** change the password, 2FA, tokens, settings or users; those need a browser session.
- **Route guards** (`auth.go`): `requireUser` (session or token, the normal API), `requireAdmin` (admin by session or token, e.g. rescan), `requireSession` (account changes), `requireAdminSession` (server settings, user management). Every guard attaches the user to the request; handlers read it with `userOf(r)`.
- **Admin access CIDRs** (`admin_cidrs`) optionally restrict the admin UI and API to listed networks. Share links (`/s/…`) always work from anywhere. Saving a list that would lock out the current client is refused.
- **Headers:** nosniff, `X-Frame-Options: SAMEORIGIN`, a CSP of `script-src 'self'` on the UI and share pages, and `Referrer-Policy`.
- **Serving library files:** HTML and JS are served as `text/plain`. SVG, HTML and XML get `Content-Security-Policy: sandbox`. Everything gets nosniff. `Content-Disposition` is built with `mime.FormatMediaType`, which prevents header injection through file names.
- **CLI:** `burrow backup <file>` makes a safe online copy (`VACUUM INTO`, mode 0600). `burrow reset-password <username> [--admin]` sets and prints a temporary password, turns off that user's 2FA, revokes their API tokens and signs them out everywhere; `--admin` also makes the account an active admin. `burrow verify-public <url> [link]` checks a deployment from the outside (section 8a).
- **Database file** mode 0600: it holds password hashes, 2FA secrets and the key that signs share cookies.
- **Setup is done** only when `setup_done=1` **and** at least one user exists, so a database with no accounts goes back to first-launch setup instead of locking everyone out.

---

## 8a. Transport and exposure (`tls.go`, `verify.go`)

- **Two ports, both HTTPS only.** 47823 serves the whole app and stays on the LAN. 47824 (`publicRoutes`) serves `/s/…`, the six assets share pages load and the favicon; everything else is 404. `public_ui` and `public_mcp` add the full app or `/mcp` to it.
- **TLS:**
  - TLS 1.3 only, so every cipher suite is AEAD with forward secrecy.
  - Key exchange prefers X25519MLKEM768, SecP256r1MLKEM768 and SecP384r1MLKEM1024, then X25519 and P-256. `BURROW_TLS_PQ_ONLY` drops the classical groups.
  - HTTP/2 is negotiated through ALPN. HSTS is `max-age=31536000`, without includeSubDomains.
- **One port, sorted by first byte** (`tlsListener`). A TLS handshake goes to the HTTPS server. Anything else goes to `plainHandler`, which redirects browsers to https (308, no body) and refuses proxied plain requests (421, since redirecting them would loop), and never serves content. Connections that say nothing are dropped after 10 s.
- **Certificates:** cert and key files are re-read at most every 30 s when they change, and a broken renewal keeps the old certificate. Without them, a self-signed ECDSA P-256 certificate is created once in `state/tls`: 10 years, names `burrow`, `localhost` and the hostname, its own trust anchor so a proxy can pin it, key mode 0600. The SHA-256 fingerprint is logged at start.
- **Behind Nginx Proxy Manager:** either a proxy host with forward scheme https and the self-signed certificate pinned (`proxy_ssl_verify on`, `proxy_ssl_name burrow`), or a TCP stream with Burrow holding the public certificate. The README has both. The stream keeps the proxy from ever seeing plaintext and gives post-quantum key exchange end to end, but Burrow then sees every visitor as the proxy's address, so the per-address limits become shared.
- **Hardening on the share path:**
  - `Cache-Control: private, no-store` on every `/s/` response, so proxy caches can't keep private files. `Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex`.
  - The QR code is served only for active links.
  - The guessing limits in section 8 apply.
  - Uploads are refused with 507 when free space would drop below a reserve (1% of the volume, 1–50 GB).
- **`verify-public`** checks a deployment from the outside:
  - certificate validity, TLS version and negotiated key exchange, and that TLS 1.0/1.1 are refused
  - that plain HTTP never returns content, and that HSTS is on
  - that the web UI, API, health check and MCP are not reachable
  - seven path-traversal attempts
  - a share page's security headers and cookie flags

---

## 9. Sharing (`shares.go`, `collections.go`)

### Three kinds of links
| Mode | What it is |
|---|---|
| `link` | A file or folder. Folders can be browsed inside and downloaded as a zip |
| `drop` | Admins only. Upload-only (Nextcloud's "file drop"). Uploads land in `/drop/<label>`. Drops with the same label share a folder on purpose. The uploader sees nothing else |
| `collection` ("shared folder") | **One link with changeable contents.** It's a list of library paths, linked live and never copied, plus its own upload folder `/drop/Shared - <label>`. Recipients see one virtual folder. The owner can add or remove items at any time, and the same link and password keep working. Admins can also upload files into it, and can let recipients add files (`allow_upload`). Deleting it with `?purge=1` also deletes its upload folder, unless another link uses it |

### Common link features
- **IDs:** 12 characters from a 56-character alphabet without look-alikes (about 70 bits). A custom slug (6–64 characters) is optional; it's easier to guess, so the UI suggests a password with it.
- **Expiry:** 1 hour, 1 day, 7 days (the default from settings), 30 days, never, or a chosen date.
- **Password:** bcrypt. Once entered, the browser gets a 12-hour cookie containing an HMAC of `id|pw_hash|exp`, scoped to `/s/<id>`. Changing the password invalidates it.
- **Download limit** (`gateDownload`): counted atomically (`UPDATE … WHERE dl < max_dl`). On a limited link each browser counts **once per file**, for a preview (`raw`) or a download (`dl`) alike, and gets a signed 6-hour resume cookie for that file. Its later requests for the file (resuming, seeking in a video, downloading after a preview) are free, even after the limit is reached. Every other request counts, whatever its Range header, so nothing fetches a file uncounted. A zip always counts. On links without a limit only downloads from byte 0 count, for the statistics.
- **States:** active, expired, revoked ("turned off"), used-up, missing (target gone), unavailable (the owner is gone, turned off, or can no longer share the target; a drop whose owner is no longer an admin). The UI also shows "expiring" when less than 24 h remains. Non-active links render a clear page explaining why.
- **Note to the recipient** is shown on the page and HTML-escaped by `html/template`.
- **QR code** as PNG: public at `/s/<id>/qr.png`, and from the admin side at `/api/shares/<id>/qr.png`, which can be downloaded at 1024 px.
- **Zip** downloads are streamed with no compression (`zip.Store`), so they run at disk speed, use no memory, and skip hidden names and symlinks.
- **Events** are logged with IP and user agent: view, download, zip, upload, password-fail.
- **Public pages** are server-rendered (`web/share.html`). They work without JavaScript; `share.js` only adds drag-and-drop and per-file upload progress. It sends one request per file, so one failed file doesn't fail the rest. Pages send `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex` and a CSP with `script-src 'self'` and `form-action 'self'`.
- **Opening files** goes through `openNoFollow`: `openat2` with `RESOLVE_NO_SYMLINKS | RESOLVE_BENEATH` relative to the library (or drop) folder, so a symlink swapped in after `resolve` checked the path isn't followed. Where the kernel or seccomp lacks openat2, it falls back to `O_NOFOLLOW`.
- **Serving files** uses `http.ServeContent` on the open file, so Range requests work and whole files go out through `sendfile` (no copy through Go's memory).

### Uploads (drops, shared folders)
- The multipart body is streamed (`MultipartReader`), never buffered. Each request is capped at `max_upload_mb` + 1 MB.
- Names go through `safeFileName`: directories stripped, control and Windows-reserved characters replaced, leading dots removed, 200 characters max.
- Each file is written once, straight into its final folder, under a hidden `.burrow-part-*` name. It is flushed to disk (`fsync`), a free name is claimed with `O_EXCL` (`photo.jpg`, then `photo (2).jpg` and so on), and the finished file is renamed over that placeholder. A half-sent file is never visible, and nothing is ever overwritten.
- `.burrow-part-*` names are always hidden (the indexer's `excluded()` hard-codes the prefix), so they never show up in listings, zips, search or the change log. A daily job deletes leftovers older than a day.

### Lightweight by design
- A link or shared folder is only a database row (plus `share_items` rows) pointing at library paths. Nothing is copied, ever.
- **Upload folders are created on the first upload**, not when the link is made. A shared folder's folder name is reserved in the database: it skips names used by any drop or shared folder, and names that already exist on disk, so a new link never shows a deleted link's leftovers. `shareMu` serializes that choice with the insert.
- QR codes are generated on request, not stored.
- Zips are streamed with a pooled 256 KB copy buffer.

---

## 10. Thumbnails (`thumbs.go`)

- Formats: jpg, jpeg, png, gif, webp, bmp. Files over 200 MB are skipped.
- The image header is read first, and anything over **60 megapixels** is refused, which caps memory per decode at about 240 MB.
- **EXIF orientation** is read from the first 128 KB of JPEGs and applied after scaling, which is cheaper than rotating the full image. This is the fix for sideways phone photos.
- Output: longest side 360 px, bilinear scaling, transparency flattened onto white, JPEG quality 80.
- **Cache:** `/state/thumbs/<2 hex>/<sha256(path|size|mtime_ns)>.jpg`, so editing a file changes its key. The first 32 hex characters are the ETag, and `http.ServeContent` answers `304 Not Modified`. Browsers cache for a day (`private`).
- At most 3 thumbnails are generated at once (`thumbSem`). A request that waited re-checks the cache first. Each write uses its own temp file, then a rename.
- **Pruning:** a cache hit refreshes the file's mtime at most once a week. The daily `maintain` job deletes thumbnails unused for 90 days, plus temp files older than an hour.

---

## 11. MCP endpoint (`mcp.go`)

- Streamable HTTP with JSON responses only (no SSE). JSON-RPC 2.0, batches supported, notifications get `202`. Methods: `initialize`, `ping`, `tools/list`, `tools/call`. Body capped at 1 MB.
- Auth: `Authorization: Bearer brw_…`. A `401` carries `WWW-Authenticate`. The admin network list (section 8) applies.
- **Every tool acts as the user who made the token**, with that user's grants.
- Results come back twice: as text (indented JSON) and as `structuredContent` (always an object).

| Tool | Does | Notes |
|---|---|---|
| `search_files` | search the index | query syntax as in the UI; type, folder, modified_after, min_size_mb, sort, limit (50 default, 500 max) |
| `list_directory` | list a folder | empty path lists the libraries |
| `file_info` | details for one path | includes the user's links for it and `you_can_share` |
| `read_text_file` | first part of a text file | 64 KB default, 512 KB max; binary refused |
| `recent_changes` | change log | hours (24 default), folder, kind, limit |
| `create_share_link` | link to a file or folder | needs share permission |
| `create_file_drop` | upload-only link | admins only; hidden from `tools/list` for others |
| `create_shared_folder` | shared folder | `allow_uploads` admins only |
| `add_to_shared_folder` / `remove_from_shared_folder` | edit contents | own links only (admins: any) |
| `list_share_links` | links with state and counts | own links only (admins: all) |
| `revoke_share_link` | turn a link off | own links only (admins: any) |
| `index_status` | index size and scan state | totals for admins only |

---

## 12. Users and permissions (`users.go`)

### The model
- **Admins** see and do everything. **Users** see only what their grants allow.
- A **grant** is `(path, read, write, share)` on one library path and everything inside it. Write and share both imply read.
- **The most specific grant covering a path wins.** So `DOCUMENTS: View` plus `DOCUMENTS/Taxes: No access` (a grant with read off) hides Taxes inside a folder they can see, and a deeper grant can open something up again inside a carve-out.
- The UI offers three levels per folder: **View**, **View and share**, **No access**. Write is stored and checked by `resolveFor(…, permWrite)`, which also asks the kernel whether the mount is writable (`mountWritable`). But nothing writes to the library yet, so the UI doesn't show it. It comes with writable folders (section 16).

### The checks
| Function | Meaning | Used for |
|---|---|---|
| `u.can(rel, p)` | the most specific grant covering `rel` gives `p` | opening files, sharing, every final check |
| `u.canReach(rel, p)` | `can`, or some grant giving `p` lies inside `rel` | walking down through parent folders to a granted subfolder; listings are then filtered |
| `u.scope(col, p)` | an SQL condition true exactly where `can` is | search, stats, the change log, folder totals |

`scope` builds, for each grant that gives `p`: *inside that grant and not inside a more specific grant that doesn't*. Because the most specific covering grant decides, this is exact, and `TestScopeMatchesCan` checks it against `can` on 400 random grant sets. Doing it in SQL keeps `LIMIT`s and totals right. Search also re-checks each result in Go.

### Where it applies
| Surface | Rule |
|---|---|
| Library listing, folder listings | entries the user can `canReach` |
| Raw files, thumbnails, previews | `can(read)` |
| Zip of a folder | each file `can(read)`; folders entered if `canReach` |
| Search, change log, overview, folder totals | `scope(read)` in SQL |
| Index status | totals for admins only |
| Favorites, API tokens, sessions | per user |
| Links: list, edit, delete, events, QR | own links only; admins see all with the owner's name. Someone else's link answers "not found", so ids can't be probed |
| Creating a link or adding to a shared folder | `can(share)` (for shared folders, the **owner's** permission, even when an admin edits it) |
| File drops, shared-folder uploads (owner and recipient), users, server settings, rescan, library names | admins only |

### Links run with their owner's permission
Every public access re-checks the owner: the link root, sub-paths (`shareTarget`), listings inside a folder link, zips (`zipAllow`), shared-folder items (`sources`) and paths inside them (`collResolve`). A link becomes **unavailable** when its owner is deleted or turned off, or loses share permission on what it points at. A drop becomes unavailable if its owner stops being an admin. Giving the permission back makes the link work again.

### Account lifecycle
- **Turn off:** sessions are deleted. Tokens are kept but refused, and links stop. Turning the account back on restores tokens and links.
- **Delete:** grants, favorites, sessions and tokens are deleted. Links are turned off but kept, so their history stays visible to admins. User ids are never reused (AUTOINCREMENT).
- **There's always an active admin:** you can't delete yourself, and the last active admin can't be demoted, turned off or deleted. A turned-off admin doesn't count.
- **Password reset** by an admin signs that user out everywhere.

### Upgrading from the single-admin version
Runs automatically on start, in one transaction, and is safe to run again:
1. The old admin becomes user 1 (password hash, 2FA secret, last used step and recovery codes carried over).
2. `sessions.user_id`, `tokens.user_id` and `shares.owner_id` are added with default 1, so existing sessions, MCP tokens and links keep working.
3. Favorites are copied to `favs`, and the old `favorites` table and the old admin settings keys are deleted.

`TestMigrateSingleAdmin` builds a database the way the old version left it and checks all of the above.

### Drop folder inside the library
Mounting the drop folder inside the library makes uploads searchable, but anyone with access to its parent folder then sees every drop's uploads. To hide them, give that user **No access** on the drop folder.

### Why drops are admin-only
File drops and recipient uploads write to `/drop`, which sits outside every folder grant, so only admins can make them. Non-admins' shared folders are pure pointer lists.

---

## 13. UI design rules

The rules, as numbered in the CSS comments:

| Rule | Meaning | How it's done |
|---|---|---|
| O1 | No tinted or translucent selected states | selected rows: solid neutral step plus a solid accent bar; selected nav, toggles and segments: solid `--accent` |
| O2 | Accent colors are CSS variables, so sub-themes can be swapped | all colors are tokens in `web/base.css` (`--paper`, `--surface`, `--surface-2`, `--ink`, `--ink-2`, `--rule`, `--accent`, `--on-accent`, `--ok`, `--caution`, `--bad`, `--hit`), with light and dark sets |
| O3 | No explainer or subtitle text on pages | help goes in the small (i) tooltip, `info()` in `app.js` |
| O4 | Don't round every corner or box every section | regions sit flush, separated by single rules; the 4 px radius is only on controls |

- **OLED black:** in dark mode the page background is pure black (`--paper`) on the web, and `background`/`surface` are pure black in the app, with neutral near-blacks for raised surfaces. On OLED screens those pixels are off.
- **Accent:** amber on the web (filled amber folder icons, the current folder in the path bar). The app follows the phone's wallpaper colors (dynamic color), and falls back to the same amber on Android 11 and older.
- **Path bar:** a pill with a home icon and chevrons; the folder you're in is in the accent color. In the app the folder name is a large title that collapses into the bar as you scroll.
- **Font:** Atkinson Hyperlegible 400 and 700, embedded (OFL, `web/FONT-LICENSE.txt`). It was chosen because file names are the data, and I/l/1 and O/0 must be easy to tell apart. Share ids and tokens use the mono font.
- **Status ladder:** `--ok` active, `--caution` expiring within 24 h, `--bad` expired, revoked, used up, missing, unavailable. Color plus a shape (dot, triangle, square).
- **Code:** vanilla JS, no build step. `h()` builds elements, and `put()` replaces children while skipping `null` (plain `replaceChildren` prints "null"). All view state lives in the URL: `view, path, q, kind, sort, sel, share, tab, within, add, user`.
- **Layout:** sidebar | main | side panel. The panel overlays the page below 1100 px, and the phone layout starts below 760 px.
- **Writing:** American spelling, short plain sentences.

---

## 14. Deployment

- **Image:** `Dockerfile` builds a static binary (`CGO_ENABLED=0`, `-trimpath -s -w`, version via `-X main.version`) on `golang:1.26-alpine` and copies it into `alpine:3.22`. About 34 MB. The healthcheck is `burrow healthcheck`.
- **CI:** `.github/workflows/release.yml` runs `go vet` and `go test` on pull requests and on pushes to `main` and `v*` tags. Pushes (not pull requests) then build `linux/amd64` and `linux/arm64` and push `ghcr.io/xattribution/burrow` with `latest` (default branch), semver tags and a short-sha tag.
- **TrueNAS** (`deploy/truenas-app.yaml`, Install via YAML). The app name must be lowercase letters, digits and dashes. Set `user:` to the uid:gid of the account that owns the files (`id <username>`), replace the three host paths with your own, and create the state and drop folders before installing. The image must be public, or TrueNAS must be logged in to GHCR.
- **Other Docker hosts:** `install.sh` with `install <library>`, `update`, `uninstall`, `backup`, `restore <file>`, `status`. It installs into `~/burrow` and writes `.env` (LIBRARY, DROP, PORT, PUID, PGID, TZ) next to `compose.yaml`.
- **Nginx Proxy Manager, Advanced tab:**
  ```nginx
  client_max_body_size 0;
  proxy_request_buffering off;
  proxy_read_timeout 3600s;
  ```
- **Upgrading from the single-admin version** runs on start (section 12). Sign in as before, then add people under **Users**.

---

## 15. Security review, tests and performance

### How the protections are verified
- **Integration tests through real HTTP** (`burrow_test.go`, `users_test.go`, `security_test.go`, 45 tests). They cover path escapes, symlinks, hidden names and disabled libraries; CSRF; 2FA replay and recovery codes; inert serving of HTML/SVG and header injection through file names; download limits and range resumes; every grant rule on every surface in section 12; link ownership and owner changes; admin-only endpoints; token scope; the last-admin guard; the upgrade; lazy upload folders and hidden partial uploads; thumbnail revalidation; and MCP acting as the token's user.
- **Property test:** `TestScopeMatchesCan` (SQL filter against the Go check, 400 random grant sets).
- **Mutation checks:** protections were removed one at a time to confirm a test fails each time. All of these are caught:
  - the zip filter, the owner check on link sub-paths and the SQL filter in search
  - the public-port asset allowlist and the link-id guessing limit
  - download counting, TLS 1.3-only, and `openat2`
  - the atomic rate limiter, the cross-site guard on sign-in, the exact-name check, and the admin-change lock
- **Case-insensitive filesystem:** `TestExactNamesAndHiddenNameCase` mounts one (a casefold tmpfs, or ciopfs over FUSE) where it can. Without the exact-name check, a user with No access on `DOCS/HR` read `DOCS/hr/pay.txt`; with it, every spelling is refused.
- **Independent audits:** two read-only reviews, one of the public surface and one of the signed-in API and UI. Every finding was checked against the code, and the real ones were fixed with a test each. They included parallel guessing past the limits, case-spelling past grants, Range requests past download limits, CSRF on sign-in, a race on the last admin, and MCP inputs.
- **Smoke test with a real reverse proxy:** nginx 1.24 (OpenSSL 3.0, like a typical Nginx Proxy Manager) in front of the public port, set up as a proxy host with the upstream certificate pinned, and separately as a TCP stream.
  - Every flow worked over HTTPS on both hops: the password gate, downloads, folder browsing and a drop upload.
  - The web UI, API, MCP and health check answered 404 through the proxy, traversal attempts 404, and port 80 redirected.
  - A packet capture of both hops during those flows held no file contents, password, link id, path, cookie or HTTP verb. A control capture of a plain-HTTP request did show its path, so the search works.
  - Chromium negotiated TLS 1.3 with X25519MLKEM768 directly with Burrow and through the stream, and X25519 through the proxy host (OpenSSL 3.0).
  - `verify-public` passed everything, with a single warning: the proxy host's key exchange isn't post-quantum.
- **Browser run:** the real binary on a sample library, driven with Playwright as an admin and as a restricted user. 21 checks covered setup, user creation, carve-outs, view-only files, sharing, search, settings, a link stopping when its owner loses access, and the phone layout. 7 more covered public drop uploads, admin uploads into a shared folder, folders appearing only on first upload, and no partial files left. No page or console errors.

### Known limits and remaining risks
1. **Admin access list** (`admin_cidrs`) blocks the whole LAN-port API from outside the list, tokens included. Paired phones use the channel, which is outside it (section 16).
2. **Rate limits** are in memory and reset on restart. The per-account sign-in limit (30 per hour) means someone who knows a username can keep that account from signing in for an hour; that is the price of stopping guesses spread over many addresses.
3. **Custom link names** are guessable. The UI suggests a password with them, and guessing is throttled.
4. **TCP stream mode** hides visitors' addresses from Burrow, so guessing limits are shared by everyone behind the proxy.
5. **Typing `http://` by hand** sends that first request's URL in plain text before the redirect (nothing is served). HSTS prevents it on later visits.
6. **Grants follow paths.** Moves made in Burrow take grants along; renaming a carved-out folder on the NAS directly moves it out from under its No-access grant.
7. **Drop folder inside the library** is visible to anyone with access to its parent (section 12).
8. **Very broad searches** that match most of the index are slower (see below), because results are ranked and the total is counted up to 10,000. Specific searches stay around 15 ms.
9. **Thumbnail memory:** up to 3 decodes at once, each capped at 60 MP (about 240 MB), so roughly 720 MB at the worst moment.
10. **Token "last used"** can lag by up to a minute (written at most once a minute on purpose).

### Performance
Measured on a library of 250,000 files in 3,425 folders:

| Operation | Time |
|---|---|
| First full index | 26.6 s (about 9,400 files/s) |
| Rescan, nothing changed | 1.4 s |
| A change on disk visible in search | about 2 s (inotify) |
| Database / image / memory idle → indexed | 90 MB / 34 MB / 3.4 MB → 147 MB |

Search with per-user filtering, on a synthetic index of 248,200 rows (best of 5):

| Query | Matches | Before multi-user | Admin | Restricted user |
|---|---|---|---|---|
| a specific two-word query | 3,212 | 15 ms | 14 ms | 15 ms |
| a broad query | 10,000+ | 232 ms | 244 ms | 236 ms |
| a word in every row | 10,000+ | 289 ms | 310 ms | 304 ms |

The per-user filter adds nothing measurable. The broad-query cost was already there. If it ever matters, the next step is to skip the relevance sort above a few thousand matches.

---

## 16. Phone app (`android/`, `devchan.go`, `devices.go`, `writes.go`)

### Structure
- **`android/shared`**, Kotlin Multiplatform (JVM target today): the channel client (`Protocol.kt`, `Channel.kt`), the typed API (`Api.kt`), resumable uploads (`Transfers.kt`), the sync engine (`Sync.kt`) and its SQLDelight state. Platform pieces sit behind `expect`: `Crypto` (BouncyCastle ML-KEM and X25519, the JCA for AES-GCM and HMAC) and the OkHttp `Transport`. An iPhone app adds iOS targets with CryptoKit and URLSession versions of those two.
- **`android/app`**, Jetpack Compose with Material 3. `NavigationSuiteScaffold` picks a bottom bar or a rail by window size; Files and Sync use `NavigableListDetailPaneScaffold`, so a phone or folded foldable shows one pane and an unfolded foldable or tablet shows two, avoiding the hinge. In tabletop posture a photo or video takes the top half. The activity handles size changes itself, so folding never rebuilds the screen.

### Pairing and the channel
Section 8a covers TLS. The phone channel adds end-to-end encryption on top of it, because TLS ends at a reverse proxy, which then sees everything, and its key exchange may not be post-quantum.

1. **Pairing.** The web UI makes a one-time code (16-byte id, 32-byte secret `Kqr`, 10 minutes, at most 3 open per user, kept in memory only) and shows it as `burrow://pair?v=1&c=…&k=…&lan=…&pub=…&pin=…`. The app sends an ML-KEM-768 encapsulation key and an X25519 key, MACed with a key derived from `Kqr`; its name and model are sealed under `Kqr` too. Burrow consumes the code on the first correct MAC, encapsulates, and both derive the device key `DK` with HKDF over `Kqr ‖ ss_mlkem ‖ ss_x25519`, salted with the transcript hash. Someone who photographed the code and recorded the traffic still has to break ML-KEM and X25519.
2. **Sessions.** `POST /api/d/hello` runs the same exchange authenticated by `DK` (a MAC over the request). The session key mixes `DK` with fresh ML-KEM and X25519 secrets, so recorded traffic stays secret if `DK` leaks later. Only a holder of `DK` can produce a reply the app can open: that is the server identity check, and no password or token is ever sent. Sessions last an hour, live in memory (8 per device, oldest dropped), and a restart only costs a new handshake.
3. **Requests.** `POST /api/d`: a 25-byte header (version, session id, message number), then the real request (method, path, headers, body) in AES-256-GCM segments of 64 KiB under a key derived from the session key and message number. The last segment is flagged in its nonce, so truncation is detected; segments can't be reordered or moved between messages. Message numbers are accepted once (4096-message window). The reply comes back the same way. Inside, the request runs as the device's user through `deviceRoutes()`, an allowlist of the normal handlers: browsing, search, files, thumbnails, links, favorites, writes, uploads and sync. Account and server administration stay in the web UI.
4. **Both ports, past the admin list.** `/api/d/*` is on the LAN and public ports and outside `admin_cidrs`, since it authenticates every request itself. Every request also checks that the device still exists and its user is active, so removing a phone (or `reset-password` from the CLI, another process) takes effect at once.

The byte layouts are in the comment at the top of `devchan.go`. `devices_test.go` has a reference client in Go and `android/shared`'s interop tests run the Kotlin client against the real server, so the two can't drift apart.

### Addresses
The QR code carries the home address (`lan_url`, or the address the browser used unless that is loopback or the public address) and the public address, plus the SHA-256 of Burrow's certificate. The app trusts publicly trusted certificates as usual and Burrow's self-signed one only by that pin; the hello reply refreshes addresses and pins. On Wi-Fi or Ethernet it tries home first, elsewhere public first (home only as a fallback behind a VPN). Trying the home address on a stranger's network is harmless: nothing reusable is sent and only Burrow can answer the handshake.

### Writes, trash and uploads (`writes.go`)
- **Permission:** an edit grant and a writable mount (`mountWritable` asks the kernel). Moving or deleting a folder also needs edit on everything inside it (`canWriteTree`), so a carve-out can't be removed or carried off by someone it keeps out.
- **Moves** use `renameat2(RENAME_NOREPLACE)` and rewrite grants, favorites, link paths, shared-folder items and cached hashes in one transaction, so permissions and links follow the folder. Moves across mounts are refused.
- **Trash:** a delete is a rename into `.burrow-trash/<id>/` at the top of the item's mount (found in `/proc/self/mountinfo`) or of its library folder, so it is instant and on the same filesystem. Restore puts it back (as "name (restored 2)" if the name was taken). The daily maintenance pass removes entries after 30 days. Names starting with `.burrow-` are always hidden.
- **Uploads:** `POST /api/uploads` reserves a hidden partial file next to the target; `PUT /api/uploads/{id}?offset=N` appends one chunk (up to 64 MB) after truncating to the recorded offset, fsyncs it, and saves the SHA-256 state with the offset; `POST …/finish` checks the hash and puts the file in place. Name conflicts: rename (default), overwrite (the old version goes to the trash through a hard link, then an atomic rename), skip or fail. `expect_size`/`expect_mtime` make an overwrite conditional on the version the app last saw. The file's mtime is set to the phone's.
- **Index:** every write rescans the folders it touched right away (`Indexer.Refresh`), so browsing, search and the change log reflect it.

### Sync
- **Server:** `GET /api/sync/list` walks the folder on disk (exact at that moment), returns paths relative to it with size, mtime in ms and cached hashes, plus the hidden-name patterns and whether the folder is writable. Its cursor is read before the walk. `GET /api/sync/changed?since=` says whether anything under the folder entered the change log since, so an unchanged folder isn't listed again. `GET /api/sync/hash` computes and caches a SHA-256.
- **Engine (`Sync.kt`):** three-way. For every path it compares the phone and Burrow with the base (both sides as they were when they last agreed) and picks from a fixed table per mode: backup, two-way, download. When both sides changed and the contents differ, both versions are kept as "name (conflict <phone> <time>).ext". A file that vanished from one path and appeared unchanged at another is moved on Burrow instead of uploaded again. Deletes and overwrites on Burrow are conditional on the version last seen; on the phone the file is checked again just before. Each step updates the base as it completes, uploads resume through `pending_upload`, and Burrow's side is listed only when its change log moved or every six hours.
- **Phone side:** folders through the Storage Access Framework (`SafTree`), the camera roll through MediaStore (`MediaTree`, backup only, original bytes with locations when allowed). WorkManager runs each pair hourly under its own conditions and the photo backup within two minutes of new media; long runs show a progress notification as a data-sync foreground job.

### On the phone
- The device key is sealed with an AES key that lives in the Android Keystore and can't be exported. `allowBackup` is off and the data-extraction rules exclude everything, so keys never reach cloud backups or a new phone.
- No plain HTTP (network security config), no user-added certificate authorities.
- QR codes are read on the phone with ZXing, without Google Play services.
- Files handed over by other apps (share menu, drag and drop) are copied into the app's cache first, because that access ends when the screen closes; the copy is deleted once sent.
- Files dragged out of the app are `content://` addresses served by `RemoteFileProvider`. It isn't exported, so another app can read an address only with the grant that comes with the drag (`DRAG_FLAG_GLOBAL_URI_READ`). Reads go through a seekable proxy file descriptor that streams over the device channel, keeps one stream open while reads are in order, and reopens with a range request when the reader jumps. Folders only drag within the app, to be moved.
- Drag and drop on both web and phone uses one rule for where things can go: not into the folder they're already in, onto themselves, or into their own subfolders. The folder under the drag turns solid accent and a pill names the action (Upload to, Move to).

### Tests
- `devices_test.go`, `writes_test.go`: pairing, handshakes, replay, tampering, truncation, what a recording proxy sees (no paths, names, contents or methods), revocation, disabled accounts, the public port and the admin list; mkdir, moves that carry grants and links, carve-out protection, trash and its purge, resumable uploads with a crash tail, checksums, every conflict mode, read-only mounts, sync listings and hashes, an upload through the channel.
- `android/shared` (JVM, against the real server): interop, address fallback, pinning, a fake server, revocation, restart recovery; the sync engine in all three modes, conflicts, deletes, moves and a resumed upload.
- `android/app` (Robolectric, against the real server): pairing from a link and using the app at phone, folded cover screen, unfolded and tablet sizes, with screenshots; a removed phone returning to pairing; a dragged-out file's address, its name and size, and in-order and seeking reads streamed from the server.
- CI also runs the app on an Android emulator: the Keystore, a photo backup from the media library, the backed-up photo read back through its drag-out address with seeking, background sync through WorkManager, and the R8-shrunk release build paired and browsed through its own screens.

## 16a. Connected NAS (`remote.go`, `remote_ops.go`, `transfer.go`)

- A connection is added on the Burrow it should live on. On the server (this section) it becomes a library for everyone given access to it. In the phone app it will stay on that phone, so a folder someone else shared with you never reaches your own NAS unless you link it there.
- Each one is a `remoteFS`: SMB through `github.com/cloudsoda/go-smb2` (pure Go, SMB2/3, NTLM), or another Burrow through its REST API with an API token. Without a fingerprint the other Burrow's certificate must be publicly trusted; with one, only that exact certificate is. Redirects aren't followed.
- The connection's name is its top folder, so grants, favorites and the permission checks work unchanged. A name can't match a local top-level folder, and a local folder with that name can't be enabled as a library.
- Every path segment on a connected NAS has to pass `validName`: no `.burrow-*` names, no hidden names, no separators or control characters. SMB listings skip links and devices.
- The config is sealed with AES-256-GCM using `remotes.key` (0600) in the state folder, so a copy of the database alone doesn't expose a password. The settings API returns the config without its secrets, and a change that leaves a secret empty keeps the stored one.
- Reads stream with ranges (`http.ServeContent` over an SMB file or a range-requesting reader for another Burrow), and content types get the same sandboxing as local files. Thumbnails are cached under a key that includes the connection id, size and mtime.
- Uploads keep their partial file in `state/remote-parts`, checked with SHA-256 like any upload, and are written to the NAS at the end with the same conflict modes. SMB writes create the file exclusively; SMB renames check the target first because the protocol replaces by default.
- Deletes go to `.burrow-trash/<id>/` on an SMB share and appear in **Recently deleted** (the row's `stored` is `remote:<connection id>:<path>`), with restore and the 30-day purge. Another Burrow keeps its own trash.
- A move between libraries, or between two mounts of this NAS (which used to fail), is a background job: count, copy (never over anything), then trash the original. If any file was skipped because the user can't read it, the original stays put. `GET /api/jobs/{id}` reports progress; the web tray follows it.
- Not yet: indexing (search), share links, zip downloads and phone sync pairs on a connected NAS.
- Tests: `remote_test.go` runs the same checks against a real Samba server (`ci/smb-server.sh`, started in CI) and against a second Burrow over TLS with a pinned certificate: listing, range reads, thumbnails, refused paths, mkdir with parents, uploads with every conflict mode, renames that won't replace, moves both ways between libraries as jobs, trash, restore and purge, grants (no access, read-only), refused links, the wrong password, pin or token, and no plaintext secret in the database.

## 17. Roadmap

1. The iPhone app on the shared core (iOS targets with CryptoKit and URLSession).
2. Phone files alongside NAS files, and drag out of the app.
3. Browsing a photo server such as Immich inside the app.

---

## 18. Contributing

- **Run the tests:** `go test ./...`. For a manual run: `BURROW_DATA=./testdata BURROW_STATE=./state BURROW_DROP=./drop go run .`, then open `https://localhost:47823`. For the phone app, see `android/` and the README.
- **Conventions:** errors go through `httpErr`, JSON through `readJSON` and `writeJSON`, and handlers get the user with `userOf(r)`. A new library-facing endpoint needs a grant check (`resolveFor`, `canReach` or `scope`) and a test in `users_test.go`.
- **Keep it pointer-based:** links and shared folders must never copy library files or create folders before they're needed.
