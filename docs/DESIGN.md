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
burrow_test.go   integration tests through real HTTP (httptest)
users_test.go    tests for every permission rule, link ownership, the upgrade, lazy upload folders, thumbnails, MCP
web/             embedded UI: index.html, app.js, app.css, base.css, share.html, share.js, share.css, fonts, favicon
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
| **Library mounted read-only into the container** | The kernel guarantees Burrow can't modify or delete library files, whatever bugs it has. Only `/drop` (uploads) and `/state` (database, thumbnails) are writable. |
| **Browsing reads the disk directly; search reads the index** | Folder listings are never stale. Search is instant. Browsing a folder also queues a quick rescan of it, so the index heals itself. |
| **Vanilla JS UI, no framework or build step** | Small, fast and dependency-free. All app state is kept in the URL query (`?view=&path=&q=&sel=&share=&tab=…`), so the back button and links work. |
| **Share links over HTTPS, not FTP** | FTP sends passwords in clear text, needs extra ports that reverse proxies can't pass, and needs client apps. An HTTPS link works in any browser. |

### Configuration
Environment variables cover plumbing only. Everything a user changes lives in the `settings` table and is edited in the app.

| Env | Default | Meaning |
|---|---|---|
| `BURROW_ADDR` | `:47823` | listen address |
| `BURROW_DATA` | `/data` | library root (read-only mount) |
| `BURROW_DROP` | `/drop` | writable upload folder |
| `BURROW_STATE` | `/state` | database and thumbnail cache |
| `BURROW_SCAN_HOURS` | `6` | full rescan interval |
| `BURROW_WATCH` | `true` | inotify live updates |
| `BURROW_TRUSTED_PROXIES` | `127.0.0.1/32,::1/128,172.16.0.0/12,100.64.0.0/10` | peers allowed to set X-Forwarded-* (loopback, Docker, Tailscale) |

Settings keys: `public_url`, `roots` (JSON list of enabled top-level folders), `excludes` (newline globs), `default_expiry_hours` (default 168), `max_upload_mb` (default 20480), `admin_cidrs`, `secret` (HMAC key, generated), `setup_done`, `initial_scan_done`. Versions before multi-user kept the one admin account in `admin_user`, `admin_hash` and the `totp_*` keys. The upgrade moves that account into the `users` table as user 1 and deletes those keys.

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

- **First-launch setup** happens in the UI. It asks for an admin username and password (10+ characters), the public URL, and which top-level folders to enable. Setup from a non-private IP requires a **setup key** printed in the container log. Setup takes a mutex so two requests can't race. Before setup, `/api/roots/available` (the folder names) only answers the LAN or holders of the setup key; after setup, only admins.
- **Client IP:** `X-Forwarded-For` is trusted only when the direct peer is in `BURROW_TRUSTED_PROXIES`. It's walked right-to-left past trusted hops.
- **The Secure cookie flag is decided per request.** It's set only if the request is TLS or arrived from a trusted proxy with `X-Forwarded-Proto: https`. So signing in over plain HTTP on the LAN works, while the public HTTPS address still gets Secure cookies.
- **Session cookie:** HttpOnly, SameSite=Lax, 30 days, stored as sha256.
- **CSRF:** every non-GET request authenticated by cookie must carry `X-Burrow: 1`. A cross-site form can't set that header.
- **Rate limits** (in memory): sign-in 8 failures per 15 min per IP; share passwords 10 per 15 min per IP; turning off 2FA 8 per 15 min per IP; password change 8 per 15 min per user. Empty keys are deleted and the map is swept at 50k keys.
- **Two-factor sign-in** is per user and follows RFC 6238: SHA-1, 6 digits, 30 s steps, ±1 step. Each step can be used once (`users.totp_last`, updated with a conditional UPDATE under the write lock, so two racing requests can't both use one code). There are 10 single-use recovery codes, stored hashed. Enrollment works by: start (pending secret plus a QR code of the `otpauth://` URI), then confirm with a code, then recovery codes are shown once.
- **Sign-in** is per user. Wrong usernames take as long as wrong passwords (a dummy bcrypt compare), and both get the same message. Changing your password needs the current one (8 tries per 15 min per user), then signs out your other sessions.
- **API tokens** (prefix `brw_`) belong to the user who made them and act as that user, with the same folder grants. They **cannot** change the password, 2FA, tokens, settings or users; those need a browser session.
- **Route guards** (`auth.go`): `requireUser` (session or token, the normal API), `requireAdmin` (admin by session or token, e.g. rescan), `requireSession` (account changes), `requireAdminSession` (server settings, user management). Every guard attaches the user to the request; handlers read it with `userOf(r)`.
- **Admin access CIDRs** (`admin_cidrs`) optionally restrict the admin UI and API to listed networks. Share links (`/s/…`) always work from anywhere. Saving a list that would lock out the current client is refused.
- **Headers:** nosniff, `X-Frame-Options: SAMEORIGIN`, a CSP of `script-src 'self'` on the UI and share pages, and `Referrer-Policy`.
- **Serving library files:** HTML and JS are served as `text/plain`. SVG, HTML and XML get `Content-Security-Policy: sandbox`. Everything gets nosniff. `Content-Disposition` is built with `mime.FormatMediaType`, which prevents header injection through file names.
- **CLI:** `burrow backup <file>` makes a safe online copy (`VACUUM INTO`). `burrow reset-password <username>` sets and prints a temporary password, turns off that user's 2FA, turns the account back on and signs it out everywhere.
- **Setup is done** only when `setup_done=1` **and** at least one user exists, so a database with no accounts goes back to first-launch setup instead of locking everyone out.

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
- **Download limit:** counted atomically (`UPDATE … WHERE dl < max_dl`). **Range requests that don't start at byte 0 don't count.** The browser whose download was counted gets a signed **6-hour resume cookie**, so it can finish the download even after the limit is reached; other browsers get 410.
- **States:** active, expired, revoked ("turned off"), used-up, missing (target gone), unavailable (the owner is gone, turned off, or can no longer share the target; a drop whose owner is no longer an admin). The UI also shows "expiring" when less than 24 h remains. Non-active links render a clear page explaining why.
- **Note to the recipient** is shown on the page and HTML-escaped by `html/template`.
- **QR code** as PNG: public at `/s/<id>/qr.png`, and from the admin side at `/api/shares/<id>/qr.png`, which can be downloaded at 1024 px.
- **Zip** downloads are streamed with no compression (`zip.Store`), so they run at disk speed, use no memory, and skip hidden names and symlinks.
- **Events** are logged with IP and user agent: view, download, zip, upload, password-fail.
- **Public pages** are server-rendered (`web/share.html`). They work without JavaScript; `share.js` only adds drag-and-drop and per-file upload progress. It sends one request per file, so one failed file doesn't fail the rest. Pages send `Referrer-Policy: no-referrer`, `X-Robots-Tag: noindex` and a CSP with `script-src 'self'` and `form-action 'self'`.
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
- **Integration tests through real HTTP** (`burrow_test.go`, `users_test.go`, 22 tests). They cover path escapes, symlinks, hidden names and disabled libraries; CSRF; 2FA replay and recovery codes; inert serving of HTML/SVG and header injection through file names; download limits and range resumes; every grant rule on every surface in section 12; link ownership and owner changes; admin-only endpoints; token scope; the last-admin guard; the upgrade; lazy upload folders and hidden partial uploads; thumbnail revalidation; and MCP acting as the token's user.
- **Property test:** `TestScopeMatchesCan` (SQL filter against the Go check, 400 random grant sets).
- **Mutation checks:** three protections were removed one at a time to confirm the tests fail: the zip filter, the owner check on link sub-paths, and the SQL filter in search. All three are caught.
- **Browser run:** the real binary on a sample library, driven with Playwright as an admin and as a restricted user. 21 checks covered setup, user creation, carve-outs, view-only files, sharing, search, settings, a link stopping when its owner loses access, and the phone layout. 7 more covered public drop uploads, admin uploads into a shared folder, folders appearing only on first upload, and no partial files left. No page or console errors.

### Known limits and remaining risks
1. **Admin access list** (`admin_cidrs`) blocks the whole API from outside the list, tokens included. The phone app needs device tokens to pass (section 16).
2. **Rate limits** are in memory and per IP, and reset on restart. Sign-in isn't limited per account across many IPs, on purpose: a per-account lock would let anyone lock a user out.
3. **Custom link names** are guessable. The UI suggests a password with them.
4. **Drop folder inside the library** is visible to anyone with access to its parent (section 12).
5. **Very broad searches** that match most of the index are slower (see below), because results are ranked and the total is counted up to 10,000. Specific searches stay around 15 ms.
6. **Thumbnail memory:** up to 3 decodes at once, each capped at 60 MP (about 240 MB), so roughly 720 MB at the worst moment.
7. **Token "last used"** can lag by up to a minute (written at most once a minute on purpose).

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

## 16. Phone app (planned) and the server work it needs

### Decisions
- **Native, with Kotlin Multiplatform.** A shared module holds the API client, the sync engine and address switching. The Android screens use Jetpack Compose and come first. A separate SwiftUI iPhone app comes later and reuses the shared module.
- **Each folder pair** can be set to **one-way backup** or **two-way sync**.
- **Access follows the signed-in user**: the per-folder grants above.
- **Local vs. public address:** on Wi-Fi networks marked as home, the app uses the LAN address, and the public address everywhere else. Android needs location permission to read the Wi-Fi name.
- **The app should:** browse the NAS, open other people's share links, drag and drop between phone and NAS, back up and sync, and show phone files alongside NAS files.
- **Open question:** browsing a photo server such as Immich inside the app, either directly or passed through Burrow.

### Server design (planned, not built)
1. **Writable folders.** The library stays mounted read-only, and specific folders are mounted writable on top. Writing needs both a write grant and a writable mount (`mountWritable` asks the kernel). Then add write to the grants UI, plus move, new folder and delete-to-trash.
2. **Pairing.** A short-lived, single-use code shown as a QR in web Settings. The app trades it for a **device token** tied to the user, and each device can be revoked. Schema to add: `pair_codes(code_hash, user_id, expires)`, plus `tokens.kind` (`api` | `device`) and `tokens.platform`.
3. **Server identity check.** Before sending its token over the LAN, the app sends a random challenge, and the server answers with an HMAC keyed by the token's hash. That way the token is never sent to a fake server on a Wi-Fi network with a lookalike name.
4. **Uploads.** Resumable chunked uploads to a hidden partial file (the same `.burrow-part-` pattern), renamed into place when complete. Name conflicts: keep both, overwrite or skip. Schema: `uploads(id, user_id, dir, name, size, off, sha256, mtime, conflict, created)`.
5. **Deletes** go to a hidden trash folder and are kept 30 days.
6. **Sync API:** a folder listing, a change feed with a cursor (`changes.id` already increases monotonically), and cached SHA-256 hashes, `file_hashes(path, size, mtime, sha256)`.
7. **Admin access list:** let **device** tokens through from anywhere (not API tokens), or the app won't work away from home.

These tables and columns are not in the schema yet; it only holds what is built.

---

## 17. Roadmap

1. Phone pairing, the server identity check, and device tokens past the admin network list.
2. Writable folders, resumable uploads, move, delete-to-trash and new folder (write grants in the UI).
3. The sync API.
4. The Android app, then the iPhone app.

---

## 18. Contributing

- **Run the tests:** `go test ./...`. For a manual run: `BURROW_DATA=./testdata BURROW_STATE=./state BURROW_DROP=./drop go run .`, then open `http://localhost:47823`.
- **Conventions:** errors go through `httpErr`, JSON through `readJSON` and `writeJSON`, and handlers get the user with `userOf(r)`. A new library-facing endpoint needs a grant check (`resolveFor`, `canReach` or `scope`) and a test in `users_test.go`.
- **Keep it pointer-based:** links and shared folders must never copy library files or create folders before they're needed.
