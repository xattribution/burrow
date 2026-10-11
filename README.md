# Burrow

Burrow indexes a NAS for instant search and turns any file or folder into a share link. Links can have an expiry, a password, a download limit and a QR code. It also keeps a log of every file change and every link visit. One Go binary, one SQLite file, and an MCP endpoint for LLM clients.

## Quick start

**TrueNAS:** Apps → Discover Apps → ⋮ → **Install via YAML**, paste [`deploy/truenas-app.yaml`](deploy/truenas-app.yaml), set `user:` to the output of `id <username>` and the host paths to your own, and install. The app name must be lowercase. Then open `http://<truenas-ip>:47823`. The template puts the drop folder inside the library so uploads are searchable, which also means anyone with access to its parent folder can see them. Give those users **No access** on the drop folder if that's not what you want.

**Any Docker host:**

```bash
curl -fsSL https://raw.githubusercontent.com/xattribution/burrow/main/install.sh | sudo bash -s -- install /path/to/your/files
```

On first launch the web UI asks for an admin username and password, the public address (for example `https://share.example.com`) and which top-level folders to index. That's all the setup there is. Add more people later under **Users**.

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

## Reverse proxy (Nginx Proxy Manager)

Point a proxy host at `http://<nas-ip>:47823`. For big uploads to file drops, add this under **Advanced**:

```nginx
client_max_body_size 0;
proxy_request_buffering off;
proxy_read_timeout 3600s;
```

Burrow trusts `X-Forwarded-For` and `X-Forwarded-Proto` only from loopback, Docker networks (172.16.0.0/12) and Tailscale (100.64.0.0/10) by default. If your proxy sits on your LAN instead, add its IP to `BURROW_TRUSTED_PROXIES`. Cookies get the `Secure` flag only when the request really arrived over HTTPS. That's why signing in at `http://<nas-ip>:47823` on your LAN works, unlike Nextcloud's.

To keep the admin UI reachable only from home or Tailscale, set **Settings → Admin access** to something like `192.168.1.0/24, 100.64.0.0/10`. Share links still work from anywhere.

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

A token can see and share exactly what the account that made it can, and nothing more. Make a separate user with narrower folder access if you want an LLM to see less than you do. Delete the token in Settings to cut access.

## Configuration

Everything you'd normally change is set in the app. Environment variables cover only the plumbing:

| Variable | Default | Purpose |
|---|---|---|
| `BURROW_ADDR` | `:47823` | Listen address |
| `BURROW_DATA` | `/data` | Library root; mount it read-only |
| `BURROW_DROP` | `/drop` | Writable folder for file-drop uploads |
| `BURROW_STATE` | `/state` | Database and thumbnail cache |
| `BURROW_SCAN_HOURS` | `6` | Full rescan interval; live updates cover the time between |
| `BURROW_WATCH` | `true` | Live updates through inotify |
| `BURROW_TRUSTED_PROXIES` | loopback, Docker, Tailscale | Peers allowed to set forwarded headers |

Folders named `.zfs`, `@eaDir`, `#recycle`, `.Trash-*`, `$RECYCLE.BIN` and a few others are always hidden. You can edit that list under **Settings → Hidden names**.

## Security model

- The library is mounted read-only. Only `/drop` and `/state` are writable.
- Every path is checked against the enabled libraries and hidden names, and symlinks are not followed.
- Every request runs as a user and is checked against that user's folder grants. Share links run with their owner's share permission, so a recipient never sees more than the owner could share.
- Uploads are written to a hidden temporary file, flushed to disk and renamed into place, so a half-sent file is never visible. Leftovers from a crash are cleaned up after a day.
- Share ids are 12 random characters (about 70 bits). Custom link names are optional and easier to guess, so add a password when you use one.
- Shared HTML and SVG files are served with a sandbox policy so they can't run scripts on Burrow's address.
- Password guesses are rate-limited: 8 per 15 minutes for sign-in and 10 per 15 minutes for share passwords, per IP.
- First-launch setup from outside your LAN needs the setup key printed in the container log. Even so, finish setup before you publish Burrow through the proxy.
- Two-factor sign-in with authenticator-app codes is available to every user under **Settings**. Each code works only once, and you get 10 single-use recovery codes. API tokens can't change the password, 2FA, tokens, settings or users, so a leaked MCP token can't lock you out.
- A browser that started a counted download can resume it for 6 hours, even after the link reaches its download limit. Other browsers can't.
- If someone loses their password, an admin can set a new one under **Users**. If you lose the only admin password, run `docker exec <container> burrow reset-password <username>`. It prints a temporary password and turns off that user's 2FA. Links and settings are kept.

## Development

```bash
go test ./...
BURROW_DATA=./testdata BURROW_STATE=./state BURROW_DROP=./drop go run .
```
