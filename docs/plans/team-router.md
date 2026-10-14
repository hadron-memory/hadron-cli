# Implementation Plan: `team router poll|loop|status` — the Orca attention router in Go

> **Status: as built — PR 1 (v1 parity, hadron-cli#883) and PR 2 (the v2
> `dueWakes` source, below).** Ada ruled the two-PR split at team chat
> #11614 item 3. Implements
> [#829](https://github.com/hadron-memory/hadron-cli/issues/829) (T7 of
> [hadron-server#1704](https://github.com/hadron-memory/hadron-server/issues/1704)):
> a native port of hadron-server's `scripts/orca-attention-router/router.ts`
> (TS, run via tsx). The acceptance spec is the TS suite at server main
> `f0ab31a0`, ported 1:1 into `internal/teamrouter/*_test.go` (Joost), then
> carried forward to hadron-server#1924's merged behaviour (`fea74669`).
> Review schedule per Xylon's CHECKED: **T2 Standard + §10 on every head**.
>
> PR 2 adds the v2 delivery source (`dueWakes` + `acknowledgeWakes` on an
> App key with `attention:deliver`, plan `hadron-server:design:named-heartbeats`
> §7), as `--source wakes` on the same commands and gates.

## Context

The router nudges Orca-hosted workers when they are mentioned: it polls the
team App's attention and types one short line into the worker's Orca
terminal, saying where to start reading. It never reads, relays or posts a
message, never marks anything read, and never hands its token to a worker.

Every rule in it came from an incident, and the TS suite names each one. The
port keeps them all, by name:

- **Two signals before a send.** Orca's `tui-idle` proves the agent idle, and
  the rendered screen proves the prompt empty (#5473: tui-idle reads idle while
  a human types). A `tui-idle` timeout means *unknown*, not *busy*, whether or
  not Orca still tracks the terminal (#7398, then hadron-server#1924 for
  tracked terminals stuck at a completed turn). The screen then decides: an
  empty prompt with positive idle chrome above it proves idle, and a live
  running-turn line vetoes (#1700).
- **Draft refutation.** An Orca draft holds a Codex worker (Codex renders its
  composer empty while text is pending). A Claude screen that proves the
  composer empty refutes a stale draft (#6736).
- **Send-time recompute.** Counts are re-read tokenless right before the send.
  A worker that read in the meantime is dropped, and its frontier is recorded
  as covered (#1616). Both gates are proved again afterwards, with the idle
  recheck fail-closed at 500 ms.
- **Token and ledger.** The token is adopted only when every due worker was
  nudged. The ledger, keyed on a hash of the token, stops a retry re-nudging
  the others. It is persisted immediately after each send, and a nudge whose
  frontier is beyond the adopted token's horizon is carried across adoption.
- **Resolution never guesses.** A pinned terminal (#1845) is tried first, then
  identity, then the #1823 screen fallback. A worktree bound to another worker
  holds.
- **The liveness rule** (#1826) for coordinator/lifecycle rows, on its own
  durable clock.

## Package layout (`internal/teamrouter`)

| File | Port of |
|---|---|
| `screen.go` | `promptLineIsEmpty`, `idleProofAbovePrompt`, `screenAgentFamily`, `promptIsEmptyFromTerm`. The regexes are 1:1, with JS `\s` spelled out (Go's `\s` is ASCII-only, and the fixtures carry NBSP). |
| `nudge.go` | `nudgeText` (the #1805 watermark anchor, the #1811 large-gap mentions walk frozen at the named head per #1827) and `livenessNudgeText`. |
| `orca.go` | `orca terminal list/wait/read/send`, with fail-closed envelope parsing and the durable-request retry. `Terminal` decodes with router.ts's defaults: connected/writable unless `false`, and any non-null `lastOutputAt` counts as tracked. |
| `state.go` | `State`: `adopted.token`, `ledger.json`, `liveness.json`, `liveness-notes.json`, byte-compatible with the TS state dir. Liveness is sanitized on read. |
| `lock.go`, `proc_*.go` | The `.writer-lock` mkdir lock: owner record `{pid, boot, startedAt}`, the #1490 stale proofs (reboot, ESRCH, pid reuse), and the tomb-swap break. Unix uses `kill(0)` and `ps -o lstart`. Windows uses `OpenProcess`/`GetProcessTimes`, with the boot proof skipped. |
| `config.go` | `LoadConfig` over router.ts's JSON keys, validated strictly. |
| `router.go`, `liveness.go` | `PollOnce`: the mention pass, the liveness pass, hold reporting and adoption. |
| `server.go` | `GraphQLServer`: the typed reads in `internal/api/queries/teamrouter.graphql`. |
| `status.go` | `Status`: local state only. |

**Why mkdir and not flock.** The CLI's own binding lock is flock (no stale
lock exists). This lock has to stay visible to the TS router it replaces, so
both exclude each other on a shared state dir during migration. Its
stale-owner proofs are the reviewed #1490 semantics.

## The seams

- **`Server`**: four reads (`AttentionPage`, `RegisterRow`, `ReadCursor`,
  `ChannelHead`). There is no mutation: the router changes no server state.
  Joost's original stub had `MarkRead`, which is dropped. The mentions walk's
  mark is the worker's own step, through its own session.
- **`OrcaRunner`**: `func(ctx, argv) RunResult`, with argv starting at the
  verb. `ExecOrca(bin)` bounds each exec at 300 s (router.ts's spawnSync
  timeout), so a wedged Orca costs one call, not the loop (Hugo #11532).

## GraphQL (`internal/api/queries/teamrouter.graphql`)

These are the router's own operations, not shared ones, so §10 review reads
in one file exactly what the router asks for:

- `RouterAttentionPage(appRef, since?, page?)`: `teamAttentionPage`, including
  `lastRelevantSeq`, the frontier the ledger keys on.
- `RouterRegister(workerRef, appRef)`: `attendeeRegister`. The winning row for
  the team Channel must be `WORKER`-named and mention-only. There's no
  `ownerRef`, so organization-owned rows count (@codex P2, #1490).
- `RouterReadCursor` and `RouterChannelHead`: the #1826 liveness sources. Any
  GraphQL error, including one that arrives with data, reads as unknown.

**Principal.** These run on the operator's own login, as the TS router's
`hadron api` calls did. On server main, `teamAttentionPage` serves only an
ordinary user and refuses App keys
(`findings:router-port-two-delivery-principals`).

## Commands (`internal/cmd/team/router.go`)

- `team router poll [--config]`: one poll. Human mode prints one
  `DescribeOutcome` line. With `--json`, it prints `{at, ok, error, nudged,
  dropped, covered, held{name:reason}, adopted, liveness, livenessDropped}`,
  sending router log lines to stderr. A `RouterError` exits 1 with
  "(token unchanged)"; a transport error goes through `api.MapError`.
- `team router loop [--config] [--interval 60s]`: runs in the foreground (the
  default runtime, by Holger's visibility rule). It prints only polls that
  nudged, held or failed. SIGINT/SIGTERM stop it **after** the poll in flight,
  which runs on `context.WithoutCancel`, so a send is never cut between Orca's
  receipt and the ledger write. A second signal exits at once.
- `team router status [--config]`: local state only, with no network and never
  the token.

**Config lookup:** `--config`, `$HADRON_ROUTER_CONFIG`, `$ORCA_ROUTER_CONFIG`,
then `~/.config/hadron/team-router/config.json`, then the TS router's
`~/.config/hadron-orca-attention-router/config.json`. **Migration is
therefore zero-copy:** the existing config, state dir and lock are used as
they are. Verified read-only against orca1's live install (65 workers, lock
held by the running TS loop).

The JSON keys stay router.ts's: `terminal` (not `pin`) and
`coordinator`/`lifecycle` (not `watch`). `hadronCli` is accepted and ignored.
The `orcaCli` default is the macOS app bundle on darwin and `orca` elsewhere.

## Deviations from router.ts, deliberate

- **#1924's `reported` prune is not ported.** router.ts deletes
  `reported[<worker id>]` when a worker leaves the held set, but `reported`
  is keyed by worker **name** there, so the delete is a no-op. The port keeps
  the observable behaviour: it prunes `heldSince` and `heldLoggedAt` only.
  Reported to QA as a suspected TS defect.

- **Stricter config.** A non-string `worker` (TS: any truthy value), or a
  non-integer `busyWaitMs`/`submitWaitS` (TS: `Number(...)`), is refused.
  This follows the CLI's prefer-a-loud-refusal rule.
- **`lastRelevantSeq` null is modelled as 0.** Seqs start at 1, and every
  null check maps to `== 0`.
- **The held log line lists workers in roster order.** TS uses `held`'s
  insertion order: the mention pass inserts in roster order too, and only a
  liveness-pass hold can land later. Go maps are unordered, and log lines
  should not flap.
- **A ledger or state write failure fails the poll** (`RouterError`). TS threw
  from `writeFileSync`, so the effect is the same; the error is explicit here.

## Tests

- **The ported suite** (`internal/teamrouter`). Blocks 1–12 are green at
  `7a1956c`. Blocks 13–15 (liveness, loadConfig, terminal pin) and the
  failures/locks block are Joost's to port. The engine already implements
  them.
  - Port defects fixed against router.test.ts (commit `5fed206`): `screenSpec.ok`
    was inverted, `sawWait` was never reset by non-attention calls, `headOk`
    defaulted false, NBSP was missing from the prompt fixtures, and four flap
    tests had no due worker.
- **The command** (`internal/cmd/team_router_test.go`): config lookup order,
  the `status --json` shape (`[]`/`{}`, never `null`), a usage exit for a
  missing or invalid config, and one **end-to-end poll** through the real
  genqlient client and a real exec of a fake `orca` shell script. That poll
  checks the operation order, that no `since` is sent before adoption, the
  send argv, that the token never appears in argv or output, and that the
  token is adopted and passed back next poll. Also: a failed poll exits 1
  with nothing adopted.

## PR 2: the v2 source (`--source wakes`)

**Contract.** The plan's §4.3 and §7, plus cor:agt:080:16 (acknowledging is
not reading), :17 (only the receiving caller, within 15 min), :22 (an App key
with `attention:deliver` and no session), and the server's reservation rule
(`src/lib/tickers/dueWakes.ts`): **an unacknowledged wake is re-handed
IDENTICALLY, with a fresh proof and no new budget slot, until it is
acknowledged or lapses.** A worker that read in the meantime is simply not due.

**One poll** (`PollWakes`, `internal/teamrouter/wakes.go`):

1. `dueWakes(app, workerRefs: <roster>)`, drained page by page. `workerRefs`
   is always the roster: an empty roster sends nothing, because an empty list
   would read every worker of the App.
2. For each roster worker with a wake, in roster order:
   - **Already delivered?** If the sent-record holds this wake's identity
     (worker, each channel's range, each reason), the earlier send's
     acknowledgement never landed. Acknowledge it with this scan's proof and
     type nothing.
   - Otherwise run the v1 gates unchanged: resolution (pin, identity, screen),
     the idle proof (busy-wait, or the untracked screen proof), the empty
     prompt with draft refutation, and the lost-lock check.
   - **Fresh re-read** of that one worker. Not due any more → **dropped**.
     Otherwise this wake's counts are what get typed, and its proof's
     15-minute window starts here, after the gates, which can busy-wait a
     minute per worker.
   - Re-prove idle (500 ms, re-listed tracking) and empty, then check the lock
     again.
   - Send `WakeNudgeText`, **persist the sent-record**, then acknowledge at
     once. A refused or failed acknowledgement is reported (`ackFailed`) and
     the record stays, so the next poll does the "already delivered" step
     above.
3. Holds and acknowledgement failures are logged once per reason. The budget
   (`WAKE_BUDGET_EXHAUSTED`, withheld) and dormant workers are logged once per
   state (`wake-notes.json`).

**Why acknowledge per send, not batched at the end (plan §7.2 step 3).** A
batch acknowledged after a long poll can hold proofs past 15 minutes. A
refused ack leaves the wake due, and v2 without a sent-record would type it
again.

**What v2 drops from v1** (plan §7.2): token adoption, `ledger.json`, the
mention-only prerequisite, and the #1826 liveness pass. A coordinator's watch
becomes a ticker subscription. v1's `coordinator`/`lifecycle` flags are
ignored under `--source wakes`.

**The text** (`WakeNudgeText`): `Hadron team attention (<reasons>): N relevant
unread (M mentions); team #A–#B, latest mention #C; <other> #a–#b.` The
reasons are mentions, `<ticker> tick`, or `task <event>: <title> (<ref>)`.
After that:
- the team chat's v1 read anchor, including the large-gap mentions walk;
- for any other channel, `hadron channel read <id> --since` and its mark-read;
- for a task, `hadron team task get <id>`.

It never carries the proof.

**Custody (§10).**
- **The App key.** It comes from `appKeyFile` only: never argv, never env.
  The file must be owner-only (unix) and hold exactly one `hdr_app_` key, and
  no refusal quotes it.
- **The client.** v2 builds its own client with `api.NewClient(server, key)`,
  outside the factory's session-attributing decorator. The command test
  asserts that every request's bearer is the App key and that no
  `X-Hadron-Session` is ever sent. It runs inside a bound worktree, so a
  factory client *would* have attached a session.
- **Proofs.** They never reach the outcome, logs, argv or state. The
  sent-record stores the wake's identity, not its proof.

**Shadow mode** (`--shadow`, wakes only) reads `dueWakes` only. It touches no
terminal, sends and acknowledges nothing, and writes no state, so it can run
beside the live v1 loop (plan §8 step 3). **Cost to know:** the server
reserves each wake it hands out, so shadow reads take daily-budget slots
until those wakes lapse unacknowledged.

**State.** `wakes-sent.json` holds the sent-records (24 h TTL, sanitized on
read) and `wake-notes.json` the logged-once notes. Both live in the same
state dir, behind the same writer lock, so v1 and v2 exclude each other.
`status` lists `pendingAcks`.

**Tests.** 23 red-first in `wakes_test.go` (`96800fd`), green at the PR 2
head, plus `wakes_config_test.go`. The command tests cover the custody checks
above, shadow touching nothing, and the usage refusals: a world-readable key
file, `--shadow` on v1, an unknown `--source`.

## What this slice does not do

- **The cutover itself.** That's Hugo's: shadow beside v1 for a day, then
  `--source wakes`.
- **Per-channel wakes under v1** (Hugo's 2026-10-07 addendum: DMs,
  per-(worker, channel) frontiers). v1's ledger stays single-channel, as in
  TS. v2 delivers every channel a wake names, and the server owns that
  bookkeeping.
- **Orca's hook-based agent state** (`orca worktree ps --json`, plan §8 of
  `design:agent-tasks`). This is an extra signal to add when it lands. It
  would not replace the screen.
