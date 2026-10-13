---
name: links
description: Review Burrow share links, flag the exposed ones, and turn off the ones you pick.
argument-hint: [a library path, or "all" to include expired and revoked links]
disable-model-invocation: true
---

Review Burrow share links. Scope: $ARGUMENTS

Empty scope means all active links. "all" includes expired, revoked and used-up links. A path means the links for that path.

1. Call `list_share_links` with `active_only: true` unless the scope is "all", and pass `path` when one was given.
2. Show a table: number, label, path, kind (link, shared folder or drop), expires, password, downloads (with the limit), URL. Put the riskiest first.
3. Flag:
   - links that never expire and have no password
   - folder links and shared folders without a password, since they expose everything inside
   - drops with no expiry
   - links in the `missing` state, whose target is gone
   
   A short-lived link without a password is fine on its own.
4. Offer to turn off the flagged links by number. Revoke only the ones the user picks, with `revoke_share_link`, then confirm which are off.
