---
name: share
description: Make a Burrow share link for something described in plain words.
argument-hint: <what to share> [for how long, password, download limit, who it's for]
disable-model-invocation: true
---

Share on Burrow: $ARGUMENTS

If that is empty, ask what to share and stop.

1. Resolve each thing to an exact library path with `search_files`, or `list_directory` when the user gave a folder path.
   - One clear match: use it.
   - Several plausible matches: show up to five (path, size, modified) and ask which. Don't pick by guess.
   - No match: broaden as the burrow skill describes. If there is still nothing, say what you searched for.
2. Run `file_info` on each target. If `you_can_share` is false, say so and stop. If an active link already matches what was asked, give that one instead of making another, unless the user asked for a new link.
3. Read the options from the request: expiry ("a week" is 168 hours, "forever" is -1, unstated is 0 for the server default), password (if asked for but not given, generate a four-word passphrase), download limit, a note for the recipient ("for mom"), a label.
4. Several items: make one `create_shared_folder` holding all of them, named for the request, unless the user asked for separate links.
5. A sensitive-looking target (tax, medical, identity, legal, credentials, backups) with neither a password nor an expiry given: ask once whether to add them before creating.
6. Reply with the URL on its own line, then what it points to, expiry, the password if you generated one (otherwise just whether there is one), any download limit, and the QR code URL.
