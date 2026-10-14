# Burrow

Burrow indexes a NAS for instant search and turns any file or folder into a share link. Links can have an expiry, a password, a download limit and a QR code. It also keeps a log of every file change and every link visit. One Go binary, one SQLite file, an MCP endpoint for LLM clients, and an Android app that browses, shares, backs up and syncs, at home and away, end-to-end encrypted.

## Quick start

**TrueNAS:** Apps → Discover Apps → ⋮ → **Install via YAML**, paste [`deploy/truenas-app.yaml`](deploy/truenas-app.yaml), set `user:` to the output of `id <username>` and the host paths to your own, and install. The app name must be lowercase. Then open `https://<truenas-ip>:47823`. The template puts the drop folder inside the library so uploads are searchable, which also means anyone with access to its parent folder can see them. Give those users **No access** on the drop folder if that's not what you want.

**Any Docker host:**

```bash
curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh | sudo bash -s -- install /path/to/your/files
```

Burrow only speaks HTTPS. Until you give it a certificate it makes its own, so the browser asks you to accept it once. On first launch the web UI asks for the setup key printed in the container log, an admin username and password, the public address (for example `https://share.example.com`) and which top-level folders to index. That's all the setup there is. Add more people later under **Users**.

| Task | Command |
|---|---|
| Update | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- update` (see [Updating](#updating)) |
| Uninstall | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- uninstall` |
| Back up | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- backup` |
| Restore | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- restore ~/burrow/backups/<file>.db` |

On TrueNAS, back up with:

```bash
sudo docker exec $(sudo docker ps -qf name=burrow) burrow backup /state/backups/burrow-$(date +%F).db
```

The backup holds settings, users, links, link history, API tokens and the change log. The file index rebuilds itself from disk if it is ever lost.

## Updating

Every update is in place. Only the container is replaced. Everything Burrow keeps stays in the state folder: the database (settings, users, links, link history, API tokens, the change log), the TLS certificate and the thumbnails. Your files are never touched. When the new version starts, it upgrades the database by itself. Browser sessions, paired phones, share links and API tokens all carry on, with nothing to set up again.

Each build is also published under its own tag (`sha-<commit>`, and `1.2.3` for releases) at `ghcr.io/xattribution/burrow`, so you can always go back to the version you had. Older versions ignore what newer ones add to the database, so going back keeps your data too. To check which version runs, look at the bottom of **Settings**.

### TrueNAS

1. Back up (the command above).
2. **Apps → Installed → burrow.** If TrueNAS offers an update for it, choose ⋮ → **Update**.
3. If it doesn't (TrueNAS doesn't always notice a new image behind `:latest`), pull the image in **System → Shell**:
   ```bash
   sudo docker pull ghcr.io/xattribution/burrow:latest
   ```
   Then on the Apps page, **Stop** the app and **Start** it again, so it starts from the new image. If **Settings** still shows the old version, open **Edit** and **Save** without changes, which redeploys the app.

To go back: **Edit** the app and change the image line to the version you want, for example `ghcr.io/xattribution/burrow:sha-27c3c4b` or `:1.0.0`, then **Save**. Set it back to `:latest` to follow new versions again.

### Any Docker host (install.sh)

```bash
curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh | sudo bash -s -- update
```

It backs up the database to `~/burrow/backups/` first, takes the new `compose.yaml`, pulls the new image, restarts Burrow and waits until it answers. Then it prints the old and new versions and the command to go back. Put your own changes to the compose file (a writable folder, other ports) in `compose.override.yaml` next to it. Docker Compose merges that file in, and updates never touch it. If you edited `compose.yaml` itself, the update keeps your file and leaves the new one beside it as `compose.yaml.new`.

To go back or stay on one version, name it: `... | sudo bash -s -- update sha-27c3c4b`. `update latest` follows new versions again. To roll the data back too, restore the backup the update made: `... | sudo bash -s -- restore ~/burrow/backups/<file>-before-update.db`.

### Your own Compose file

In the folder with your compose file:

```bash
docker exec burrow burrow backup /state/backups/burrow-$(date +%F).db
docker compose pull && docker compose up -d
```

To go back, set `image:` to a version tag and run `docker compose up -d` again.

### docker run

Back up, `docker pull ghcr.io/xattribution/burrow:latest`, then `docker stop burrow && docker rm burrow`, and start it again with the same `docker run` command. The state folder must be mounted at `/state` as before, so nothing is lost.

### From source

Stop Burrow, run `git pull && go build -o burrow .`, and start it again with the same `BURROW_STATE`.

### Phones

A server update needs nothing on the phones: paired phones keep working. A new APK, when there is one, is on the [Releases](https://github.com/xattribution/burrow/releases/latest) page and installs over the old one.

### Updating to token settings

The first version with per-token MCP settings keeps every existing API token working as it was, plus one change: every tool now asks before each call in Claude Code. Tokens keep REST API access too. Narrow them under **Settings → MCP and API tokens → Edit**. If you go back to an older version, it ignores token settings, so tokens act without limits there until you update again.

## What it does

- **Search** matches every word as a substring of the full path, across every enabled library, as you type. `-word` excludes, `ext:pdf` and `type:video` filter. You can narrow by type, by library, or sort by newest or largest.
- **Browse** reads folders straight from disk, so listings are never stale. Images get cached thumbnails, with phone photos rotated correctly. PDFs, video, audio and text preview in place.
- **Share links** work for files or folders. Each link can have an expiry (1 hour to never, or a date), a password, a download limit, a note to the recipient, an optional custom name (`/s/taxes-for-mom`) and a QR code you can show or download as a PNG. Folder links let recipients browse subfolders and download everything as a .zip. Links can be turned off, turned back on or deleted.
- **Shared folders** are one link whose contents you keep changing. Add any file or folder from the library to it, and admins can also upload new files straight into it. Library items are linked live, not copied. The link, password and history stay the same, so the recipient just reopens it to see what's new. Admins can optionally let recipients add files too. Turn it off or let it expire when you're done, and deleting it can also delete its uploads.
- **File drops** (admins only) are upload-only links, like Nextcloud's file drop. Someone can send you files without seeing anything on the NAS. Uploads land in `/drop/<name>`, and duplicate names are renamed rather than overwritten.
- **Nothing is copied.** A link or shared folder is a database row that points at library paths. A drop's or shared folder's upload folder is only created when the first file arrives, so making links never adds folders to your NAS.
- **Users** each get their own sign-in, two-factor, favorites, links, API tokens and phones. Admins see everything. Everyone else sees only the folders an admin grants them: **View**, **View and share**, **View and edit**, **View, edit and share**, or **No access** to carve a subfolder out of a folder they can see. Search, previews, zips, the change log, the overview, links and the phone app all follow those grants. A link stops working if its owner loses share access or the account is turned off.
- **Writable folders** take uploads, new folders, renames, moves and deletes, from the web UI and the phone app, where a user has edit access and the folder is [mounted writable](#writable-folders). In the web UI you can drag files and whole folders from your computer onto any folder to upload them, drag a row onto a folder or the path bar to move it, and drag a file out to your desktop to download it (Chrome and Edge). Moving a folder takes its permissions and links along. Deleting moves things to a trash that keeps them 30 days (**Recently deleted**).
- **Connected NAS** (admins, in **Settings**) adds another Burrow server or a plain SMB share (Windows, Synology, TrueNAS, Unraid) as an extra library. People reach it through the same folder access as any library. You can browse it, preview and stream files, upload, make folders, rename and delete it into a trash on that NAS, and drag files between it and your own libraries. Moving between libraries copies first and only then puts the original in **Recently deleted**. The passwords and tokens are stored encrypted in `remotes.key` in the state folder, apart from the database, and are never shown again. Search, share links and zip downloads cover this NAS's own libraries for now. To connect another Burrow, make an API token there (with API access on) and paste it with that Burrow's address, plus its certificate fingerprint if it uses the self-signed certificate.
- **Phone app** for Android: browse and search, preview, share links, save to the phone, and back up or sync folders both ways in the background. See [Phone app](#phone-app).
- **Activity** has two logs. One records files added, modified and deleted, kept for 180 days. The other records every link open, download, upload and wrong password, with the visitor's IP. Each user sees changes in their folders and activity on their own links.
- **Overview** charts size by library, size by file type and changes per day, counting only what you can see.
- **Favorites** pin files and folders to your sidebar.
- **MCP** endpoint at `/mcp` with tools for search, listing folders, file info, reading text files, recent changes, share links, shared folders and file drops, new folders, writing text files, moving, deleting to the trash, and index status. Tools act as the user who made the token, and each token sets which tools it offers, which folders it reaches, and whether Claude Code must ask before every call.

## Measured performance

These were measured on a test library of 250,000 files in 3,425 folders.

| Operation | Time |
|---|---|
| First full index | 26.6 s (about 9,400 files/s) |
| Rescan with nothing changed | 1.4 s |
| Search with a specific match (45 hits) | 10 ms |
| Search matching 6,300 files | 47 ms |
| Search matching 47,000 files (first 300 shown) | 76 ms |
| A file changed on disk → visible in search | about 2 s (live inotify) |
| Database size | 90 MB |
| Container image | 34 MB |
| Memory at idle / after indexing 250k files | 3.4 MB / 147 MB |

## Opening share links to the internet

Burrow listens on two ports, both HTTPS only:

| Port | Serves | Expose it? |
|---|---|---|
| 47823 | the whole app: web UI, API, MCP, share links | No. Keep it on your LAN |
| 47824 | share links (plus the few files their pages load) and the phone app | Yes, through your reverse proxy |

Everything else answers 404 on 47824: the web UI, the API, sign-in, setup, MCP and the health check. The phone app's requests all go to one path, `/api/d`, sealed end to end (see [How the phone connects](#how-the-phone-connects)). **Settings → Public port** can add the web UI or MCP to it, and both are off by default.

Nothing travels in plain text on any hop. Burrow terminates TLS 1.3 itself, preferring the hybrid post-quantum key exchange (X25519MLKEM768), so the hop from the proxy to Burrow is encrypted too. Plain HTTP on either port only ever gets a redirect to HTTPS (or, from a proxy, an error), never content. Pick one of these two Nginx Proxy Manager setups.

### Option A: proxy host (recommended)

Nginx Proxy Manager holds the public certificate and re-encrypts to Burrow. Visitors' real addresses reach Burrow, so rate limits and logs are per visitor.

1. **Hosts → Proxy Hosts → Add:** your domain, **Scheme `https`**, your NAS address, **port 47824**. Turn on **Block Common Exploits**.
2. **SSL tab:** request a Let's Encrypt certificate, and turn on **Force SSL**, **HTTP/2** and **HSTS**.
3. Copy Burrow's certificate, `state/tls/burrow.crt`, into Nginx Proxy Manager's data folder as `burrow.crt`, so the proxy can check it's really talking to Burrow.
4. **Advanced tab:**
   ```nginx
   client_max_body_size 0;
   proxy_request_buffering off;
   proxy_read_timeout 3600s;
   proxy_ssl_verify on;
   proxy_ssl_trusted_certificate /data/burrow.crt;
   proxy_ssl_name burrow;
   proxy_ssl_server_name on;
   proxy_ssl_protocols TLSv1.3;
   ```
5. Tell Burrow which address the proxy connects from, so it trusts the proxy's `X-Forwarded-For`: `BURROW_TRUSTED_PROXIES=<proxy address>` (loopback is trusted by default, nothing else). If you skip this, Burrow logs a reminder the first time the proxy connects.

The proxy decrypts and re-encrypts, so the proxy machine handles plaintext in memory. Post-quantum key exchange at the proxy needs an Nginx built with OpenSSL 3.5 or newer; with older builds the proxy's hops use X25519, which is strong but classical.

### Option B: stream (TCP pass-through)

Nginx Proxy Manager forwards raw TCP and never sees plaintext. TLS runs end to end between the visitor and Burrow, with post-quantum key exchange for any current browser.

1. Give Burrow a certificate for your domain: `BURROW_TLS_CERT=/path/fullchain.pem` and `BURROW_TLS_KEY=/path/privkey.pem`, from any ACME client (for example a DNS-challenge certificate from TrueNAS or acme.sh). Burrow reloads the files when they change, so renewals need no restart.
2. **Streams → Add:** an incoming port (for example 8443; 443 is taken by proxy hosts), forwarding to your NAS address, port 47824, TCP.
3. Set the public address in Burrow to `https://share.example.com:8443`.

With a stream Burrow sees every visitor as the proxy's address, so its guessing limits apply to everyone together: someone hammering random links can block link pages for everyone for 15 minutes.

### Check it from the outside

```bash
docker run --rm ghcr.io/xattribution/burrow verify-public https://share.example.com https://share.example.com/s/<a link id>
```

It reports the certificate, the TLS version and key exchange, whether plain HTTP serves anything, whether the web UI, API or MCP can be reached, a set of path-traversal attempts, and a share page's security headers. It exits non-zero on any failure.

For the strictest setup, `BURROW_TLS_PQ_ONLY=true` refuses clients without post-quantum key exchange. Older browsers and phones then can't open links at all, so it's off by default.

## Phone app

The Android app reaches Burrow at home and away. Download the APK from [Releases](https://github.com/xattribution/burrow/releases/latest) on the phone and open it to install. Updates install over it.

**Pair it:** in Burrow's web UI open **Settings → Phones → Pair a phone**, then in the app tap **Scan the code**. The code works once and expires after 10 minutes. It carries two addresses: the public address (**Settings → Public address**) and the home address (**Settings → Home address for phones**, or the address your browser used). On Wi-Fi the app tries the home address first, and the public one everywhere else.

What it does:

- **Files:** browse and search everything your account can see. Photos zoom, video and audio stream (with seeking), text and PDFs preview. Save anything to the phone's Downloads or open it in another app. Where you may edit: upload, new folder, rename, delete.
- **Links:** make a link with an expiry, password and download limit, copy or share it, show its QR code, turn it off.
- **Sync and backup**, per folder pair:

  | Mode | What happens |
  |---|---|
  | Back up to Burrow | New and changed files go to Burrow. Deleting on the phone deletes nothing on Burrow. |
  | Keep in sync both ways | Additions, changes and deletes go both ways. |
  | Keep a copy on this phone | The Burrow folder is copied to the phone and kept up to date. |

  **Photos and videos** backs up the whole camera roll, with photo locations kept, a minute or two after each new photo. Every pair also runs hourly, only on Wi-Fi or only while charging if you like. When a file changed on both sides, both versions are kept. Files deleted from Burrow wait 30 days in its trash. Large uploads resume where they stopped, and each one is checked end to end with SHA-256.
- **Save to Burrow** from any app's share menu, and drag files in from another app on a foldable, in split screen or in DeX. Drop them on the folder you're in or on any folder row.
- **Drag files out** of Burrow into another app beside it. The other app reads the file straight from the NAS, with no download first. Hold any file or folder and drag it onto a folder or the path bar to move it.
- **Other connections, on the phone only:** pair with another Burrow (someone else's, with the account they made for you) or add an SMB share, in Settings, Connections, or from the place menu at the top of Files. Their keys and passwords are sealed in the phone's Keystore and never reach your own Burrow. Copy to and Move to work between any two places, and on an unfolded phone or a tablet, **Show another place beside** puts two places side by side to drag files between them. Dragging between places copies, and the original stays where it was.
- **Link to my NAS** (admins) hands a phone connection to your Burrow, so it becomes a library there for everyone you give access to. For another Burrow, the phone makes a REST-only token on it for your account, which you can delete there like any token.
- **Built for phones and foldables:** one pane at a time on a phone or a folded foldable; the list and the open file side by side when it's unfolded (kept clear of the hinge) or on a tablet. Half-folded on a table, a photo or video takes the top half and the details the bottom.

### How the phone connects

- **End to end:** every request the app makes is encrypted a second time, inside HTTPS, with keys only the phone and Burrow hold. A reverse proxy (Nginx Proxy Manager included) sees one URL and scrambled bytes: no file names, paths, contents or credentials. This works with either setup above; nothing extra to configure.
- **Post-quantum:** keys come from ML-KEM-768 and X25519 together, mixed with a secret that only ever travels inside the QR code, and are renewed every hour. Requests are sealed with AES-256-GCM and each one is accepted once.
- **Nothing to steal:** no password or reusable token ever crosses the network. A server that isn't your Burrow can't complete the handshake, so the app won't talk to it, even on a Wi-Fi network that copies your home address.
- **Removal:** paired phones are listed under **Settings → Phones** (and per user under **Users**). Removing one cuts it off at once; resetting a user's password removes their phones too.
- **On the phone:** the key is sealed by the Android Keystore and excluded from backups and phone-to-phone transfers. A new phone is paired again.

### Checking a download

Each release lists the APK's SHA-256 and the SHA-256 of the certificate it is signed with; every release uses the same certificate. To check by hand: `apksigner verify --print-certs burrow-v1.0.0.apk`.

### Writable folders

Burrow, the web UI and the phone app write only where both are true: the user has **View and edit** on the folder (**Users → Folder access**), and the folder is mounted writable into the container. Keep the library read-only and mount the folders you want writable on top of it:

```yaml
volumes:
  - /mnt/pool/files:/data:ro                  # the library, read-only
  - /mnt/pool/files/Photos:/data/Photos       # writable: phone backups land here
```

The container's user needs write permission on that folder. Deleted files move to a hidden `.burrow-trash` folder on the same mount and are removed after 30 days.

## Connecting an LLM

Create a token in **Settings → MCP and API tokens → New token**. Burrow shows the token once, together with a ready-made client config:

```json
{
  "mcpServers": {
    "burrow": {
      "type": "http",
      "url": "https://share.example.com/mcp",
      "headers": { "Authorization": "Bearer brw_..." }
    }
  }
}
```

A token never reaches further than the account that made it, and you can narrow it further when you create it or later with **Edit**:

| Setting | Choices |
|---|---|
| Tools | Each tool **Off**, **Ask** or **On**, grouped as read, share, edit and admin, with presets: Read only, Read and share, Everything, Nothing. New tokens start at Read and share, all on Ask. |
| Folders | Every folder the account can see, or only the folders you list. Search, previews, links, the change log and edits all stay inside them, and the token sees only links that point there. |
| REST API | Off by default: the token works only at `/mcp`. On: scripts can use it too, with the same folders, and sharing or editing only if it offers a share or edit tool. |

**Ask** marks the tool so Claude Code asks you before every call, in every permission mode (auto and bypass included), with no "don't ask again". Other MCP clients may treat it like **On**, which leaves the decision to the client's own permission settings. A token without the admin tools has no admin powers, even when an admin made it. Tokens made before these settings existed offer every tool on **Ask** and keep REST API access.

Delete the token in Settings to cut access. MCP is reachable on the LAN port, and on the public port only if you turn that on under **Settings → Public port**.

### Claude Code plugin

For Claude Code, install the plugin instead of pasting the config:

```
/plugin install burrow --marketplace xattribution/burrow
```

It asks for the MCP endpoint and a token, and keeps the token in your system's credential store rather than a settings file. It also adds a hook that asks you before every Burrow call, on top of the token's **Ask** settings. Besides the tools, it adds five commands:

| Command | What it does |
|---|---|
| `/burrow:find <description>` | Finds files from a plain description |
| `/burrow:share <what> [options]` | Makes a link: "the 2024 tax folder for mom, one week, with a password" |
| `/burrow:links` | Lists your links, flags the exposed ones, turns off the ones you pick |
| `/burrow:changes [span] [folder]` | Summarizes what changed |
| `/burrow:drop <name>` | Makes an upload-only link (admins only) |

Or just ask: "find the bracket STL I changed last week and give me a link to it". See [`claude-plugin`](claude-plugin/README.md).

## Configuration

Everything you'd normally change is set in the app. Environment variables cover only the plumbing:

| Variable | Default | Purpose |
|---|---|---|
| `BURROW_ADDR` | `:47823` | LAN port: the whole app |
| `BURROW_PUBLIC_ADDR` | `:47824` | Public port: share links only (`off` to disable) |
| `BURROW_TLS_CERT`, `BURROW_TLS_KEY` | unset | Certificate and key files; without them Burrow makes a self-signed pair in `state/tls` |
| `BURROW_TLS_PQ_ONLY` | `false` | Refuse clients without hybrid post-quantum key exchange |
| `BURROW_DATA` | `/data` | Library root; mount it read-only |
| `BURROW_DROP` | `/drop` | Writable folder for file-drop uploads |
| `BURROW_STATE` | `/state` | Database and thumbnail cache |
| `BURROW_SCAN_HOURS` | `6` | Full rescan interval; live updates cover the time between |
| `BURROW_WATCH` | `true` | Live updates through inotify |
| `BURROW_TRUSTED_PROXIES` | loopback | Proxies allowed to set `X-Forwarded-For`; add your reverse proxy's address |

Folders named `.zfs`, `@eaDir`, `#recycle`, `.Trash-*`, `$RECYCLE.BIN` and a few others are always hidden. You can edit that list under **Settings → Hidden names**.

## Security model

- **Transport:** HTTPS only, TLS 1.3 only, hybrid post-quantum key exchange preferred, HSTS. Plain HTTP is answered with a redirect or an error and nothing else.
- **Exposure:** the public port serves share links and nothing else unless you add the web UI or MCP to it.
- **Files:** the library is mounted read-only, except folders you mount writable on purpose. Writing also needs an edit grant, and a user can't move or delete a folder holding something they may not change.
- **Phones:** end-to-end encrypted inside HTTPS with post-quantum hybrid keys (ML-KEM-768 + X25519 + the QR code's secret), AES-256-GCM, one-time message numbers; no reusable credential is ever sent. See [How the phone connects](#how-the-phone-connects).
- **Paths:** every path is checked against the enabled libraries and hidden names, must be spelled exactly as on disk (so case tricks don't work on case-insensitive datasets), and is opened without following symlinks, even one swapped in after the check.
- **Permissions:** every request runs as a user and is checked against that user's folder grants. Share links run with their owner's share permission, so a recipient never sees more than the owner could share.
- **Link ids:** 12 random characters (about 70 bits). Custom names are easier to guess, so add a password when you use one. More than 30 unknown link ids from one address in 15 minutes blocks that address for 15 minutes.
- **Guessing limits:** every attempt is counted before it's checked, so parallel guesses can't get past the limit.
  - Sign-in: 8 per 15 minutes per address and 30 per hour per account.
  - 2FA codes: 10 per 15 minutes per account.
  - Share passwords: 10 per 15 minutes per address and 50 per hour per link, from all addresses together.
- **Download limits:** each browser counts once per file, whether it previews or downloads, and can then resume or seek for 6 hours. Every other request counts, so no Range trick gets a file for free.
- **Caching:** share content is sent with `Cache-Control: private, no-store`, so neither the proxy nor the browser keeps a copy.
- **Uploads:**
  - Each file goes to a hidden temporary name, is flushed to disk, then renamed into place.
  - Uploads are refused once free space falls below a reserve (1% of the volume, between 1 and 50 GB).
  - Leftovers from a crash are cleaned up after a day.
- **Served files:** HTML and SVG are served with a sandbox policy so they can't run scripts on Burrow's address.
- **Cross-site requests:** sign-in, sign-out, setup and every change need a header a page on another site can't send.
- **First launch:** setup needs the key printed in the container log, unless the browser runs on the server itself.
- **Accounts:**
  - Two-factor sign-in with authenticator-app codes is available to every user. Turning it on needs the password, each code works once, and you get 10 single-use recovery codes.
  - API tokens can't change passwords, 2FA, tokens, settings or users.
  - Each token is narrowed to its own tools and folders on every request, MCP and REST alike: the narrowing is applied to the account it acts as, so every permission check that guards the web UI guards the token too.
  - The database (password hashes, 2FA secrets) is readable by Burrow's user only.
- **Recovery:** an admin can set a new password under **Users**, which also revokes that user's API tokens. If you lose the only admin password, run `docker exec <container> burrow reset-password <username> --admin`. It prints a temporary password, turns off that user's 2FA, revokes their tokens and makes them an admin. Links and settings are kept.

## Development

```bash
go test ./...
BURROW_DATA=./testdata BURROW_STATE=./state BURROW_DROP=./drop go run .   # then https://localhost:47823
```

The phone app is in `android/`: a Kotlin Multiplatform core (`shared`: the encrypted channel, API client and sync engine) and the Jetpack Compose app (`app`). Its tests start the Go server and talk to it for real.

```bash
cd android
./gradlew :shared:jvmTest :app:testDebugUnitTest   # protocol, sync and UI tests, with screenshots
./gradlew :app:assembleDebug                       # app/build/outputs/apk/debug/app-debug.apk
```

Pushing a `v*` tag builds the release APK, signed with the key stored in the repository's `release` environment, and attaches it to a GitHub release.
