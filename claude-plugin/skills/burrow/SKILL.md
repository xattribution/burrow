---
name: burrow
description: Search, browse, read, share and organize files on the user's Burrow NAS through the burrow MCP tools. Use when the user mentions Burrow, their NAS or home server files, finding a file they own, share links, shared folders, file drops, saving a note to the NAS, or what changed on their storage.
---

# Burrow

Burrow indexes the user's NAS. Every tool acts as the user who made the API token, narrowed by that token's settings in Burrow (which tools, which folders). A "not allowed" error or a missing tool is a boundary the user set, not a bug: say so and stop. Changing it is done in Burrow under **Settings → MCP and API tokens**.

The user approves every Burrow call in a permission prompt. Make each call count: plan the searches you need, batch independent ones, and don't call tools to double-check what a previous result already told you.

## Paths

Library paths look like `DOCUMENTS/Taxes/2024.pdf`. The first segment is a library (an enabled top-level folder), there is no leading slash, and the spelling must match the disk exactly, case included. `list_directory` with an empty path lists the libraries.

Never guess a path for `file_info`, `read_text_file` or a share tool. Get it from `search_files` or `list_directory` first.

## Finding files

`search_files` matches every word as a case-insensitive substring of the full path. It searches names, not file contents.

- Use short distinctive fragments, not sentences: `taxes 2024`, not `my 2024 tax return`.
- `-word` excludes, `ext:pdf` filters by extension, `type:video` filters by kind (image, video, audio, doc, archive, code, 3d, folder, other). Kind is also the `type` argument.
- Narrow with `folder`, `modified_after` (YYYY-MM-DD) and `min_size_mb`. Order with `sort`: relevance, newest, oldest, largest, name.
- Nothing found: drop the least distinctive word, try synonyms and abbreviations (`invoice`/`inv`, `photo`/`IMG`, `2024`/`24`), or list the likely library. If results look stale, `index_status` says whether a scan is running.
- `total_matches` larger than `returned` means more exist. Tighten the query before raising `limit`.

`read_text_file` returns the first 64 KB by default, 512 KB at most. Check `truncated`. Binary files are refused; use `file_info` for those.

## Sharing

Pick the kind of link by what the user wants:

| The user wants | Tool |
|---|---|
| To send one file or folder | `create_share_link` |
| Several items under one link, or a link whose contents keep changing | `create_shared_folder`, then `add_to_shared_folder` / `remove_from_shared_folder` |
| Someone to send files to them | `create_file_drop` (admins only; it is missing from the tool list for other users) |

- Links are public URLs on the internet. Create one only when the user asked for it.
- Before making a link, run `file_info` on the target. It lists the links that already point there and whether the user may share it (`you_can_share`). Offer an active link that already fits instead of making a duplicate.
- Map plain words to arguments: "for a week" is `expires_hours: 168`, "forever" or "no expiry" is `-1`, unstated is `0` (the server's default). "One download" is `max_downloads: 1`. "For mom" is a `note`.
- "With a password" and no password given: generate a four-word passphrase and include it in your reply.
- If the target looks sensitive (tax, medical, identity, legal, credentials, backups) and the user gave neither a password nor an expiry, ask once whether to add them.
- Report the URL on its own line, then what it points to, expiry, whether it has a password, any download limit, and the `qr_png` URL.
- `revoke_share_link` takes the id, the part of the URL after `/s/`. It takes effect at once. Removing an item from a shared folder deletes nothing on disk.

## Changes

`recent_changes` lists files added, modified and deleted, newest first (last 24 hours and 100 rows by default). Summarize by folder and kind rather than dumping rows.

## Changing files

When the token offers them, and only when the user asked for the change:

- `create_folder` makes a folder (`parents: true` makes missing ones on the way).
- `write_text_file` creates a text file. Replacing one needs `overwrite: true`, and the old version goes to Recently deleted.
- `move_item` moves or renames. It never replaces what's at the destination, and links and folder access follow the item.
- `delete_item` moves a file or folder to Recently deleted, where it stays 30 days and can be restored in the web UI.

Editing also needs edit access on the folder and the folder mounted writable. A "can't change this folder" or "read-only" error means one of those is missing; say which.

## Not available through MCP

Content search, uploading binary files, custom link names, restoring from Recently deleted, and editing an existing link's expiry or password (revoke and recreate instead). These live in Burrow's web UI and phone app; point the user there.
