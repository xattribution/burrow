# HANDOFF — ultracode improvement session (2026-11-06)

**Purpose of this file:** live state of an autonomous one-hour improvement sprint on
branch `claude/laughing-gates-u3d9ku`, so a future agent (or human) can pick up exactly
where it stopped. Updated and pushed at every milestone. Delete when the branch is done.

**Session:** https://claude.ai/code/session_0117w4v4Qbd8SpUxgB5vi1EH
**Clock:** started ~20:45 UTC, hard stop ~21:50 UTC (user's "noon").
**Mandate from user:** maximum improvements to the app, maximum parallelism, no waiting
for approval; commit + push continuously; document in parallel (this file).

## Plan

1. **Audit (running since ~20:50):** three parallel multi-agent workflows, each =
   parallel evidence-based finders → one adversarial judge that re-reads the code and
   rejects weak findings:
   - `backend-find` (6 finders): security/auth, path+file safety, concurrency,
     SQLite/perf, HTTP API robustness, product sweep. Judge caps at 28 verified.
   - `frontend-find` (5 finders): app.js correctness/XSS, app.js UX flows, public share
     page, a11y+visual, web perf. Judge caps at 24.
   - `periphery-find` (4 finders): Go test gaps, docs/deploy, Android (report-only),
     tether relay. Judge caps at 20.
2. **Implement:** group verified findings into disjoint file clusters → one agent per
   cluster in the main checkout; Go clusters run `go build ./...` + `go vet ./...`,
   web clusters run `node --check`. Android findings are report-only (no toolchain here).
3. **Verify:** full `go test ./...` + `gofmt -l`, fix fallout, commit in logical groups,
   push after each group.

## State

- [x] Baseline: `go build ./...` OK, `go vet ./...` clean (Go 1.26.9).
- [x] Baseline: full `go test ./...` GREEN (`ok burrow 57.4s`, exit 0) at 92d73ae.
      `gofmt -l` flagged only share_nested_test.go; fixed. Any later test failure is ours.
- [ ] Audit workflows: launched 20:50 UTC, results pending.
      Run IDs (transcripts under `/root/.claude/projects/-home-user-burrow/.../workflows/`):
      backend `wf_c54ec33d-ab6`, frontend `wf_54e5842f-dee`, periphery `wf_0b410a44-3f9`.
- [ ] Implementation: not started.
- [ ] Final verification/push: not started.

## Findings (filled in as judges return)

Full machine-readable findings with exact implementable fixes live in
`HANDOFF-findings/*.json` (judge-verified; each entry: file, line, title, severity,
fix, notes). Status per finding is updated below as implementation lands.

### Frontend (24 verified of 66 raw; `HANDOFF-findings/frontend.json`)

- F1 P1 app.js:532 — empty "Pick a date" silently creates a never-expiring link
- F2 P1 app.js:1119 — token Delete + 2FA-off fire on one un-confirmed click
- F3 P2 app.js:138 — boot()/login have no error path (blank page on 502)
- F4 P2 app.js:529 — admin default expiry 0 ("never") ignored, preselects 7 days
- F5 P2 app.js:643 — share-upload errors invisible / stuck "Uploading…"
- F6 P2 app.js:558 — create forms allow double-submit (duplicate live links)
- F7 P2 app.js:824 — "Rescan now" throws (currentTarget null after await)
- F8 P2 app.js:410 — Escape in a panel form destroys everything typed
- F9 P2 shares.go:595 — share password entry loses the deep link
- F10 P2 share.html:92 — broken thumbnail icons for HEIC/SVG/TIFF/RAW/PSD
- F11 P2 share.js:51 — drop-page upload errors discard the server's reason
- F12 P2 share.html:72 — shared text/markdown files render a blank preview
- F13 P2 api.go:453 — asset cache-busting (?v=) is dead code; stale app.js after upgrade
- F14 P2 app.js:285 — route() refetches everything per navigation/keystroke
- F15 P2 app.js:1340 — uploads can't be cancelled though a cancel endpoint exists
- F16 P2 app.js:621 — Links/Users/Activity rows are mouse-only (no keyboard path)
- F17 P2 app.js:497 — moving files is drag-and-drop only (dead on phones/keyboard)
- F18 P2 base.css:27 — light-mode link/breadcrumb contrast fails WCAG AA
- F19 P2 app.js:63 — info() tooltips unusable for screen readers/touch
- F20 P2 app.js:228 — Enter during search debounce acts on the previous view's rows
- F21 P3 app.js:475 — text preview renders error bodies as file content
- F22 P3 app.js:246 — stacked 15s status pollers never cleared
- F23 P3 app.js:737 — armed danger buttons never disarm
- F24 P3 app.js:548 — form errors/confirmations silent for screen readers

### Backend — judge pending (run `wf_c54ec33d-ab6`)

### Periphery (20 verified of 34 raw; `HANDOFF-findings/periphery.json`)

**Android findings are REPORT-ONLY** (no Kotlin toolchain in this container) — they are
serious and deserve their own follow-up session with `./gradlew :shared:jvmTest`:

- P1 **P0** Sync.kt:129 — two-way sync DELETES remote dotfiles/hidden trees the phone
  can't see (verified plan-table walkthrough; trash purge makes it permanent)
- P4 P1 Channel.kt:120 — any non-Burrow 401 (e.g. Authelia in front) permanently
  unpairs the phone and wipes device keys
- P5 P1 LocalTrees.kt:161 — SafTree.write treats failed renameDocument as success →
  next sync deletes the remote copy
- P6 P1 Transfers.kt:185 — retried transfers duplicate files as "name (2).ext"
- P19 P2 work/Sync.kt:85 — same sync pair can run concurrently in three workers
- P20 P2 Sync.kt:426 — move detection trusts (size,mtime), never the stored hash

Implementable here (Go/sh/yaml):
- P2 P1 shares.go:733 — folder-share download limit miscounts (one resume cookie slot)
- P3 P1 install.sh:70 — `cmd && …` swallows failures under set -e (update w/o backup,
  failed pull reports success, failed restore prints "Restored")
- P7 P1 devices.go:496 — one shared rate-limit bucket: hostile peer locks out all phones
- P8/P9/P10/P17 — missing tests: revoked grant vs in-flight upload; token Access
  injection; MCP edit tools with folder-limited token; backup/restore round-trip
- P11 P2 release.yml:169 — apk release job doesn't depend on image job
- P12 P2 devices.go:196 — certPins() strands tether-only phones on cert change
- P13 P2 tether.go:140 — transient listener error latched forever in sync.Once
- P14 P2 tether.go:266 — run() blocks forever on <-exited after scanner failure
- P15 P2 devices.go:625 — pre-auth slowloris: no read deadline on channel endpoints
- P16 P2 shares.go:347 — PATCH expires_hours:0 kills link immediately (vs create)
- P18 P2 release.yml:80 — no build caching in CI

## Implementation status

**DONE and pushed (all green: go build/vet, targeted tests pass):**
- P3 install.sh errexit fixes (e9e9a46) — verified by a 27-check stub harness
- P11 + P18 CI: apk job needs image; buildx GHA cache; Dockerfile Go cache mounts
  (e9e9a46, f148669)
- P7, P12, P13, P14, P15 devices.go/tether.go hardening (2a4b1d5) — note: cert
  rotation history is handled lazily in certPins() with new settings keys
  cert_pin / prev_cert_pin (90-day grace); hello/pair rate-limit now counted only
  for well-formed bodies
- P8, P9, P10 new boundary tests in writes_test.go / tokens_test.go (3dd23b9)
- First app.js correctness fixes snapshot (b2a5046)

**Still open for a future agent:** P2, P16, P17 (shares.go + burrow_test.go —
deliberately held back from the concurrent waves to avoid file collisions; cluster
file content preserved in HANDOFF-findings/periphery.json), all Android items
(P1 P0!, P4, P5, P6, P19, P20), and whatever the backend judge + frontend wave
report (tracked below as they land).

Frontend wave launched (~21:28 UTC), three clusters:
- app.js bugs (F1,F4,F2,F3,F5,F6,F7,F20,F22,F8) — agent 1
- share page + Go UI bits + CSS (F9,F10,F11,F12,F13,F18) — agent 2
- app.js UX/a11y/perf (F15,F16,F17,F19,F21,F23,F24, F14 safe parts) — agent 3,
  chained after agent 1 (same file)
Each agent verifies with `node --check` / `go build ./...` and reports per-finding
done/skipped. Full `go test ./...` + commit + push happen centrally after each wave.

## Decisions & conventions for whoever continues

- Branch `claude/laughing-gates-u3d9ku` only; never push elsewhere. No PR unless the
  user asks.
- Commit messages in repo style (short imperative subject, e.g. "Share links: …"), each
  ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` and
  `Claude-Session: https://claude.ai/code/session_0117w4v4Qbd8SpUxgB5vi1EH`
- No new dependencies, no frameworks in web/ (hand-rolled vanilla JS is intentional),
  no DB schema changes that older versions can't open (downgrade-safe per README).
- Android (`android/`) and Rust (`tether/`) have no toolchain run here → findings there
  are documented, not auto-fixed, unless provably safe.
- Container is ephemeral: anything worth keeping must be committed and pushed.
