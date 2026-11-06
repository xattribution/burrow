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

### Periphery (tests/docs/Android/tether) — judge pending (run `wf_0b410a44-3f9`)

## Implementation status

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
