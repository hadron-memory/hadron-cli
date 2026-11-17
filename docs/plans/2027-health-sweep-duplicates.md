# Implementation Plan: health sweep detects and fixes duplicate agents and tabs

> **Status: in review** — implements [#2027](https://github.com/hadron-memory/hadron-cli/issues/2027)
> (feature request from Holger, relayed by Yuri), under Xabat's CHECKED gate
> ([comment](https://github.com/hadron-memory/hadron-cli/issues/2027#issuecomment-6876087481))
> with its four binding edge limits. Related: #2017 (terminal titles, touches the
> same sweep files), #1947 (relaunch of exited agents — the dry-by-default
> posture this follows).

## Context

A manual survey of orca1 (2026-11-14) found workers with **two live agent
processes** in one worktree (one under `worker-supervise`, one a hand-launched
`codex resume` from a bare shell — both acting on the same worktree and the same
session binding) and **~25 worktrees with 2–4 terminal tabs** for one worker,
most extras bare shells. `team health sweep` (every 15 min) sees neither: it
reads the versioned terminal-status@1 contract, which has no more-than-one
category, and `health.go` contains no process detection at all.

The sweep stays read-only for **reports**; this change adds a **fix phase** that
runs after the health write and is gated to an applying run.

## What changes

### 1. Detection (the census)

Two new observations per bound-worker worktree, both fail-closed:

- **Agent processes** — `hostpoller.ScanWorktreeAgents(root)` (new, next to
  `occupied`): scans `/proc/<pid>/cwd` with the same root-or-prefix rule
  `occupied()` uses, keeps only processes that positively name an agent —
  argv[0] basename `claude`/`codex`, or an interpreter (`sh`/`bash`/…) whose
  script path names one (a #! shim), never the `worker-supervise` wrapper,
  whose own argv names the agent it watches — and records pid, the
  `processArgv` display line (the redacted, bounded #1474 form), `/proc`
  start-time ticks (the stop's identity re-check), and whether the **parent's**
  cmdline contains a `worker-supervise` word (comm is truncated to
  `worker-supervis`, so the parent is matched on cmdline). Without `/proc`
  (macOS, Windows) it reports none: detection degrades, the report does not.
- **Tabs** — `orca terminal list --json` through a new `orcaterm.SweepGuard`
  runner (allowlist: `terminal list`, `terminal read`, `terminal close`).
  `orcaterm.Terminal` gains the `tabId` field Orca already emits; a missing/
  empty tabId fails closed (no grouping ⇒ no tab fix). Terminals group by
  `(worktreePath, tabId)`.

A worktree is in scope only when it belongs to a worker registered on this host
**and** the worktree's own session binding names that worker
(`orca.BoundAt`, the `hadron-team-session.json` read the status command already
uses — exported for the sweep). An unbound worktree is never touched, whatever
it hosts.

A tab is closable only on **positive** bare-shell evidence: every terminal in
the tab classifies `bare_shell` through `orcaterm.Classify` (the classifier
requires an actual shell prompt on the last non-empty line; anything else —
busy, agent chrome, a failed read — fails closed to not-bare-shell). This one
rule carries three gate guards at once: a tab running an agent is never closed,
a mid-turn tab is never closed, and the close is re-verified by a **fresh read
immediately before the close** (read-adjacent recheck; anything not provably a
bare shell is left for the next sweep).

### 2. The decision (pure, table-tested)

`internal/healthfix.Decide(worktree, mode)` — no I/O — returns would-be fixes,
alerts and skips:

- **Agents ≥ 2**:
  - exactly one supervised → keep it, `stop-agent` for each other agent;
  - none supervised → **nothing stopped, one alert** (`unsupervised-agents`);
    the issue's "keep the oldest and report it" names the keeper for the report
    only — the acceptance line ("two unsupervised agents: nothing stopped, one
    alert") is the oracle;
  - two or more supervised → anomaly, fail closed: alert
    (`multiple-supervised`), nothing stopped;
  - 0 or 1 agent → no agent action (the only agent is never stopped).
- **Tabs**: a bare-shell tab closes only when the worktree has ≥2 tabs and at
  least one non-bare-shell tab remains (a worktree whose every tab is a bare
  shell is a `no_agent` report — relaunch is #1947's, not this change).
- **Resetting** (gate edge 3): a worker with a record in the poller state's
  `Resets` map is checked against the server's `contextReset` (the shared
  fragment gains `endedAt`); `endedAt == null` ⇒ the reset is in flight ⇒ the
  worker is skipped entirely this sweep and rechecked next sweep. A reset read
  that fails also skips (fail closed: cannot prove not-resetting ⇒ do not act).
- **Team pause** (gate edge 4): any registered worker's
  `checkStatus.pendingReason == TEAM_PAUSED` (new host-key `HealthSweepPause`
  query over `workers(appRef)`) ⇒ detect and report only — fixes are listed
  with `applied: false` and the worker carries a `team-paused` skip entry.
  Precedence: resetting > pause > normal.

Stopping a process never ends or rebinds a session (gate edge 1): the fix phase
makes no session calls at all; it signals a pid and closes a tab. The kept
agent's binding file is not read for writing, moved or rewritten.

### 3. The stop (pidfd-pinned SIGTERM, no escalation)

The sweep does not own the duplicate's process tree — its parent is someone's
shell — so it signals **the pid only, SIGTERM once, never SIGKILL and never the
process group** (a group signal would take the shell with it). Identity is
pinned the way the reset handler pins it (`hostpoller`'s `ProcessControl`):
`Observe(pid)` opens a pidfd — which by construction keeps referring to the
process the census saw, so a later reuse of the pid cannot be hit — and the
handle's kernel start-time identity is compared with the census's ticks before
the signal goes out through `SignalWithin(SIGTERM, …)` with a short
self-chosen lease; a mismatch skips, never signals. A stop that fails or
lands on nothing is reported as a failed fix, not an error exit; the next sweep
re-detects. Non-Linux: no `/proc` census ⇒ no stops (same platform limit as
`worker.reset-context`).

### 4. Posture: dry by default, applying on the timer

- `team health sweep` gains `--dry-run` (default **true**): detection and the
  full would-fix listing, no signal, no close — the #1947/#2017 posture; a
  manual run changes nothing without `--dry-run=false`.
- The scheduled entry applies: `healthsched.Spec` gains `Extra []string`
  (words between the job's own and `--config`), `team health install` sets it
  to `--dry-run=false`, and the owned-cron-line matcher accepts **extra quoted
  words after the job's Sub**, so an entry installed by an older CLI is still
  recognized as owned and replaced on reinstall (no orphaned dry sweep left
  behind). Systemd/launchd rewrites are content-compared as before.
- A clean worktree prints nothing new (text mode) and `[]` arrays (`--json`).

### 5. Report and notification

`--json` (additive keys on the existing receipt): `dryRun`, `fixes[]`
(`worker`, `action` `stop-agent|close-tab`, `subject`, `applied`), `alerts[]`
(`worker`, `code`, `count`), `skips[]` (`worker`, `code`); alert codes are
`unsupervised-agents`, `multiple-supervised`, and the per-fix failures
`stop-failed`/`close-failed`; skip codes are `resetting`, `team-paused` and
`pause-unreadable` (a pause read that cannot be proven fails closed to
report-only, marked with worker `*`), and — added with the #2037 N3 trace —
`close-gone`/`close-not-bare`/`close-recheck-failed`, one row per close the
read-adjacent recheck declined (the tab left, turned or grew a live pane, or
could not be re-verified): a planned close never evaporates silently. Subjects
are codes,
pids, kinds and tab ids — never screen text; the agent display line is the
redacted, bounded `processArgv` form the host poller already ships (#1474).
Text mode prints one line per fix/alert/skip, nothing when all
are empty. Dedupe failures never fail the sweep: the health write stays the
exit contract.

**One chat post per applying sweep that fixed something** (gate edge 2): the
sweep process reads its own worktree binding (`readBindingFile` — no
ended-session side call; the server verifies the sessionRef itself, PR #473);
a live session binding plus a resolvable ambient client ⇒ one line to the team
channel listing every applied fix (`gen.CreateTeamChatMessage` with the
sessionRef — the server attributes it to the bound worker, never the human,
cli#892). Unbound — the timer — posts nothing; a post that fails leaves output
only, one stderr note, exit unchanged. Nothing is ever pushed into a terminal (cor:agt:100:05):
the fix phase's only terminal interaction is the read and the close itself.

## Files

- `internal/healthfix/` (new) — the pure decision core and its table tests.
- `internal/hostpoller/dupes_unix.go` + windows stub — `ScanWorktreeAgents`.
- `internal/orcaterm/terminal.go` — `TabID` field; `NewSweepClient`,
  `CloseTab`; `SweepGuard` in runner.go (delivery's `Guard` untouched).
- `internal/cmd/orca/status.go` — export `BoundAt` (rename of `boundAt`).
- `internal/cmd/team/health.go`, `health_dupes.go` (new) — the fix phase,
  `--dry-run`, DTO, notification seam. Tests inject at the config level only
  (a fake `delivery.orca_bin` script, real sleeping agent scripts in a real
  git worktree, a fake GraphQL server); no new seams in production code.
- `internal/cmd/team/health_install.go`, `internal/healthsched/` — `Spec.Extra`,
  owned-line matcher relaxation, install text.
- `internal/api/queries/health_sweep.graphql` — `HealthSweepPause`;
  `hosts.graphql` — `endedAt` on `ContextResetFields`; `make generate`.
- `internal/cmd/agentic/agentic-usage.md`, `docs/help-inventory.md` — the new
  flag and receipt keys.

## Red first

The failing tests land as their own commit before any implementation: the
acceptance matrix (supervised duplicate stopped / two unsupervised → alert /
only agent never stopped / unbound worktree untouched / bare-shell tab closed,
agent tab and mid-turn tab not / resetting skipped / pause report-only / dry
default / clean worktree silent / timer entry carries `--dry-run=false` /
unbound posts nothing, bound posts once).

## Limits

- No live redemption on a real Orca/host roster: contract tests against the
  fake server and fake runners, per house style; the QA seat pins the
  destructive guards on a fresh stack.
- A tab showing stale agent chrome with no live process behind it does not
  classify bare-shell and is left alone (positive evidence only); retiring
  those is future work, noted on the issue.
- Windows/macOS hosts detect nothing (no `/proc`); the sweep remains
  report-only there, as today.
- **Unbound sweeps alert into the journal only** (review, #2037): the timer
  runs unbound, and gate edge 2 forbids an unbound sweep from posting — so
  alerts (`unsupervised-agents`, `multiple-supervised`, `stop-failed`,
  `close-failed`) and applying-sweep notices reach no chat on their own. They
  surface in the receipt the scheduler captures — the journal under systemd;
  under cron there is no journal, so the output is mailed or dropped, and
  `status` reports lastRun `unknown` — and repeat each run until the
  condition clears; that is the gated posture, stated here so nobody expects
  a push.
- **A recorded reset the server will not show keeps the worker skipped**
  (review, #2037): `workerResetting` asks EVERY recorded reset of the worker
  (reset ids are server-minted with no ordering the CLI may lean on, #2037
  N2) and treats a null read (masked or reaped — indistinguishable) as in
  flight, fail closed. The skip row repeats in every receipt until the state
  file drops the reset — which reset recovery does once the outcome is
  reported (`dropReset`), so a healthy host carries few records; a reaped,
  never-reported one is the operator's to clear, never the sweep's to guess.

## Review delta (post-seat fixes, #2037)

Findings from the BLIND (Jane) and LOCAL (Joran) seats at 4f9bcbc9, and what
became of them:

- **P1 layered chains (BLIND) / supervised descendants (LOCAL), FIXED:** the
  census now collapses process chains into one logical agent — a census
  match whose ancestry (walked through any intermediary: shims, `sg`, the
  agent's own tool shells) reaches another census match is that match, not a
  second agent. Supervision is decided by an ANY-ancestor walk up to the
  WORKTREE BOUNDARY (the walk continues only through ancestors whose cwd is
  inside the worktree): the wrapper's tree stays inside, a typed stray's
  chain leaves through the terminal emulator, and a nested one-shot of the
  supervised agent merges into it instead of dying as a "duplicate". The
  sweep also never counts its own ancestor chain (a hand-run sweep cannot
  find its own parent as a subject). A process cannot gain
  worker-supervise above it (orphan re-parenting goes to init or a
  subreaper), so a supervision read can only be stale toward keeping an
  agent — that is the stop-time answer to the LOCAL P3.
- **P2 stale tab listing before close (BLIND), FIXED:** the pre-close
  recheck now re-LISTS (not only re-reads): the tab is regrouped from the
  fresh listing, every terminal of it as it is NOW must classify bare, and
  the close handle comes from that same fresh listing. A pane split into the
  tab since the sweep's first listing is never closed unread.
- **P2 null reset read (BLIND), FIXED (fail-closed):** see Limits. Every
  recorded reset of the worker is asked (reset ids carry no ordering
  contract, #2037 N2), so a stale record costs one call and an in-flight
  reset is found wherever it sorts.
- **P2 cross-platform build (BLIND), ANSWERED:** `GOOS=darwin` and
  `GOOS=windows` builds are clean — the census stops live in
  `dupes_unix.go`/`dupes_windows.go` and `LinuxProcessControl` has a
  `procctl_other.go` variant.
- **P2 cron matcher (BLIND), ANSWERED:** `ownedCronRE` already tolerated
  extra words between the job words and the marker before this change; the
  new `TestOwnedCronLineClaimsOldAndNewEntries` locks that pre-existing
  tolerance in. No matcher change was needed, and old entries stay owned.
- **P3 batch, FIXED:** duplicate worktree path guard in `worktreesOf`; the
  receipt's skip rows key is `skips` (was `skipped`; one spelling per
  concept); `appliedActions` naming; agentic-usage wording (the sweep
  computes no alarm and installs no cadence — the install entry does; the
  upgrade note: an old timer keeps running dry until reinstalled).
- **Answered with evidence, unchanged:** `Mode.Applying` unread by `Decide`
  is the tested dry/wet parity contract; `StartTicks` is consumed by the
  pidfd identity check; an unreadable tab counts as non-bare (positive
  evidence only) and always keeps the worktree non-empty; the notification
  post is verified server-side against the app (sessionRef ↔ app, PR #473)
  and posts from the sweep's own binding as gated; supervision is anchored
  on the wrapper, the binding is not pid-attached.

## Review delta 2 (Jane's BLIND delta at 75a2a7c7, #2037)

- **N1 (P2) vacuous chain tests, FIXED:** the chain tests now synchronize on
  a ready file the chain's leaf writes once it runs, then answer ONE scan —
  the first poll can no longer pass on the top process alone — and both
  chain members carry census-matchable basenames so the raw census provably
  holds two members to merge. `collapseChains` also gained a direct,
  process-free unit test (chain + stray, oldest member survives), and the
  own-ancestry exclusion gained a re-exec test whose fake parent is a census
  match by name: a scan inside that tree never reports its own ancestor.
  Verified non-vacuous by mutation: breaking the collapse turns all three
  red.
- **N2 (P2) "newest by max id" reset read, FIXED:** see Limits above — every
  recorded reset of the worker is asked, fail-closed on any unreadable or
  invisible one; pinned by the below-max in-flight test and the all-ended
  actionable test. The snapshot note ("state read once at sweep start") is
  closed the same way: the fixing loop re-reads the state file before it
  judges resets, so a reset recorded mid-sweep — records are written before
  their claim is sent — still gates the stop (pinned by
  `TestHealth2027MidSweepResetRecordStillSkips`, red without the re-read).
- **N3 (P2) untested `tabBareNow` + silent declines, FIXED:** the recheck is
  pinned branch by branch (turned screen, added pane, failing re-list, gone
  tab, unresolvable worktree, non-tab subject, unreadable pane, live-handle
  answer) and end to end for the flip/pane/list-fail/gone worlds; a declined
  close now leaves a `close-*` skip row in the receipt instead of a silent
  continue (agentic-usage updated).
- **N4 (P2) cross-builds, ANSWERED:** `GOOS=darwin` and `GOOS=windows`
  `go build ./...` both exit 0; nothing references the deleted
  `tabHandle`/`tabStillBare`; `sort` is still used (`sortedPaths`).
- **P3 batch:** every ancestry walk is hop-bounded, `ownAncestry` included
  (a pid cycle from a reuse race costs one bounded walk, not a hang — its
  map-size bound could not fire there); `rawAgents` renamed `rawAgent`; the
  skip-key comment moved onto the field it documents; the help-inventory pin
  regenerated on a clean tree at the final head.
