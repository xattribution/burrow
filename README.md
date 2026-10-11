# Burrow

Burrow indexes a NAS for instant search and turns any file or folder into a share link. Links can have an expiry, a password, a download limit and a QR code. It also keeps a log of every file change and every link visit. One Go binary, one SQLite file, and an MCP endpoint for LLM clients.

## Quick start

**TrueNAS:** Apps → Discover Apps → ⋮ → **Install via YAML**, paste [`deploy/truenas-app.yaml`](deploy/truenas-app.yaml), set `user:` to the output of `id <username>` and the host paths to your own, and install. The app name must be lowercase. Then open `https://<truenas-ip>:47823`. The template puts the drop folder inside the library so uploads are searchable, which also means anyone with access to its parent folder can see them. Give those users **No access** on the drop folder if that's not what you want.

**Any Docker host:**

```bash
curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh | sudo bash -s -- install /path/to/your/files
```

Burrow only speaks HTTPS. Until you give it a certificate it makes its own, so the browser asks you to accept it once. On first launch the web UI asks for the setup key printed in the container log, an admin username and password, the public address (for example `https://share.example.com`) and which top-level folders to index. That's all the setup there is. Add more people later under **Users**.

| Task | Command |
|---|---|
| Update | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- update` |
| Uninstall | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- uninstall` |
| Back up | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- backup` |
| Restore | `curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh \| sudo bash -s -- restore ~/burrow/backups/<file>.db` |

On TrueNAS, update from the Apps page. Back up with:

```bash
sudo docker exec $(sudo docker ps -qf name=burrow) burrow backup /state/backups/burrow-$(date +%F).db
```

The backup holds settings, users, links, link history, API tokens and the change log. The file index rebuilds itself from disk if it is ever lost.

## What it does

- **Search** matches every word as a substring of the full path, across every enabled library, as you type. `-word` excludes, `ext:pdf` and `type:video` filter. You can narrow by type, by library, or sort by newest or largest.
- **Browse** reads folders straight from disk, so listings are never stale. Images get cached thumbnails, with phone photos rotated correctly. PDFs, video, audio and text preview in place.
- **Share links** work for files or folders. Each link can have an expiry (1 hour to never, or a date), a password, a download limit, a note to the recipient, an optional custom name (`/s/taxes-for-mom`) and a QR code you can show or download as a PNG. Folder links let recipients browse subfolders and download everything as a .zip. Links can be turned off, turned back on or deleted.
- **Shared folders** are one link whose contents you keep changing. Add any file or folder from the library to it, and admins can also upload new files straight into it. Library items are linked live, not copied. The link, password and history stay the same, so the recipient just reopens it to see what's new. Admins can optionally let recipients add files too. Turn it off or let it expire when you're done, and deleting it can also delete its uploads.
- **File drops** (admins only) are upload-only links, like Nextcloud's file drop. Someone can send you files without seeing anything on the NAS. Uploads land in `/drop/<name>`, and duplicate names are renamed rather than overwritten.
- **Nothing is copied.** A link or shared folder is a database row that points at library paths. A drop's or shared folder's upload folder is only created when the first file arrives, so making links never adds folders to your NAS.
- **Users** each get their own sign-in, two-factor, favorites, links and API tokens. Admins see everything. Everyone else sees only the folders an admin grants them: **View**, **View and share**, or **No access** to carve a subfolder out of a folder they can see. Search, previews, zips, the change log, the overview and links all follow those grants. A link stops working if its owner loses share access or the account is turned off.
- **Activity** has two logs. One records files added, modified and deleted, kept for 180 days. The other records every link open, download, upload and wrong password, with the visitor's IP. Each user sees changes in their folders and activity on their own links.
- **Overview** charts size by library, size by file type and changes per day, counting only what you can see.
- **Favorites** pin files and folders to your sidebar.
- **MCP** endpoint at `/mcp` with tools for search, listing folders, file info, reading text files, recent changes, creating share links and file drops, listing and revoking links, and index status. Tools act as the user who made the token.

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
| 47824 | share links only, plus the few files their pages load | Yes, through your reverse proxy |

Everything else answers 404 on 47824: the web UI, the API, sign-in, setup, MCP and the health check. **Settings → Public port** can add the web UI or MCP to it, and both are off by default.

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

## Connecting an LLM

Create a token in **Settings → MCP and API tokens**. Burrow shows the token once, together with a ready-made client config:

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

A token can see and share exactly what the account that made it can, and nothing more. Make a separate user with narrower folder access if you want an LLM to see less than you do. Delete the token in Settings to cut access. MCP is reachable on the LAN port, and on the public port only if you turn that on under **Settings → Public port**.

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
- **Files:** the library is mounted read-only. Only `/drop` and `/state` are writable.
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
  - The database (password hashes, 2FA secrets) is readable by Burrow's user only.
- **Recovery:** an admin can set a new password under **Users**, which also revokes that user's API tokens. If you lose the only admin password, run `docker exec <container> burrow reset-password <username> --admin`. It prints a temporary password, turns off that user's 2FA, revokes their tokens and makes them an admin. Links and settings are kept.

## Development

```bash
go test ./...
BURROW_DATA=./testdata BURROW_STATE=./state BURROW_DROP=./drop go run .
```
