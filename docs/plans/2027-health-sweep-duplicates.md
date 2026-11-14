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
(`worker`, `code`, `count`), `skipped[]` (`worker`, `code`); alert codes are
`unsupervised-agents`, `multiple-supervised`, and the per-fix failures
`stop-failed`/`close-failed`; skip codes are `resetting`, `team-paused` and
`pause-unreadable` (a pause read that cannot be proven fails closed to
report-only, marked with worker `*`). Subjects are codes,
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
