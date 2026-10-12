---
name: drop
description: Make an upload-only Burrow link so someone can send you files.
argument-hint: <name, e.g. "photos from Sam"> [expiry, password, note]
disable-model-invocation: true
---

Make a Burrow file drop: $ARGUMENTS

If that is empty, ask who the drop is for and stop.

`create_file_drop` uses its `label` as the folder name uploads land in (`/drop/<label>`), so keep it short and filesystem-friendly: "Sam photos", not a sentence. Expiry works as in the share skill: "a week" is 168 hours, "forever" is -1, unstated is 0 for the server default. Add a note for the uploader when the request implies one.

Drops are for admins only. If the tool is missing or refuses, tell the user their Burrow account isn't an admin.

Reply with the URL on its own line, then where uploads land, expiry, the password if there is one, and the QR code URL.
