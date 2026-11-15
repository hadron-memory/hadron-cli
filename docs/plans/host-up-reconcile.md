# `hadron team host up`: the host reconcile engine (design as built)

hadron-cli#1389, epic hadron-server#4229 (Team on Orca), CLI lane step 1:
"#1389 engine against a fake source". The issue's 2026-11-15 review addendum
wins where it differs from the issue body. This PR does not close #1389: the
server adapter for the desired state is a later slice.

## What it does

`hadron team host up --host <id> --worktrees-root <path> --desired-state-file <file> [--dry-run | --apply] [--worker <ref>]`

For each worker the host should run, it checks four things (host
registration row, worktree, Orca agent terminal, worker session), plans only
what is missing, and with `--apply` carries the plan out through the typed
host-operation queue (cor:agt:070:05). Dry run is the default.

## Shape

The engine is `internal/hostup`, pure over three seams:

| Seam | Interface | This PR's implementation | Later |
|---|---|---|---|
| Desired state | `Source.DesiredState(host)` | `--desired-state-file` (strict JSON in the shape drafted for `hostDesiredState`, hadron-server#4224) | server adapter over `hostDesiredState` after S1a (#4222) and S3 (#4224); the flag goes away |
| What runs | `Observer.Observe(worker)` | `hostRegistrations` (read once per run), `os.Stat` of the worktree, `orca terminal list --worktree path:<dir>` | unchanged |
| How a step is done | `Actuator.Supports/Apply` | `worker onboard`'s `onboardQueue` (adopt an identical in-flight op, else issue, then wait for its verified outcome) | host-local relaunch, cli#2060 (C5) |

The command (`internal/cmd/team/host_up.go`) only wires the seams, takes the
lock and renders.

## Decision table (per worker)

Checked in this order; the first match decides.

1. Reason is `LEAVE_ALONE`, `HALTED`, `PAUSED`, `TEAM_PAUSED`, `RESETTING`, `RETIRED` or `SWEEP_OFF`: skipped with that reason, the host is not even read.
2. Desired is neither RUNNING nor STOPPED: `UNKNOWN_DESIRED`. RUNNING for a reason other than `LONG_LIVED`/`HOLDS_TASK`: `UNKNOWN_REASON` (a new server reason must be taught to the engine before it starts processes on it).
3. Observation fails: failed `OBSERVE_FAILED`, nothing acted on for that worker.
4. STOPPED: `STOP_PENDING` if an agent runs (stopping is `worker.end`, cli#2057), else `IN_SYNC`.
5. RUNNING: two or more agents `DUPLICATE_AGENTS`; session plus one agent `IN_SYNC`; not in the registration `NOT_REGISTERED`; agent with no session `TERMINAL_WITHOUT_SESSION`; session without worktree `SESSION_WITHOUT_WORKTREE`; session without agent: `worker.relaunch` if the actuator supports it, else `RELAUNCH_UNAVAILABLE`; otherwise `worktree.create` (if missing) + `worker.start`.
6. Any planned step the actuator cannot do: `ACTION_UNAVAILABLE`.
7. Sweep mode not `APPLY`: `SWEEP_NOT_APPLY` (the App's off-by-default switch).

On apply, a failed step marks that worker's later steps `NOT_ATTEMPTED`; a
credential refusal (exit 3/8) is an `AbortError`: the step is `REFUSED`,
every later step `NOT_ATTEMPTED`, and the run exits with the mapped code.

## Decisions taken without Holger (recorded in the decision queue)

- **The interim source is a file flag**, not `host.yaml`. The ruling is "no host.yaml", and the engine must not grow host.yaml-specific code; the file uses the server's drafted shape so the adapter swap is mechanical. Reversible: delete the flag when the adapter lands.
- **A live session with no terminal is reported, not restarted** (`RELAUNCH_UNAVAILABLE`). The addendum allows a host-local relaunch, but that is C5 (cli#2060); the engine already plans it when an actuator supports it.
- **A finished identical operation is not reused** (`onboardQueue.activeOnly`). Onboard reuses one on retry; a reconcile that reused last week's `worker.start` would never restart a worker that died since.
- **The worker's repo** comes from the desired state's `setup.repo` (the addendum's default, `TeamRole.repos` on the server). A worker with no repo and no worktree is `NO_WORKTREE_SOURCE`, never created.
- **The lock** is an OS file lock at `<config dir>/host-up.<sha256(host)[:8]>.lock`, taken without waiting and only by `--apply`. A dry run never needs it.

## Never

Cast, bind, rebind, `session start`/`end`, `--force`, remove a worktree, call
an Orca create or launch verb, or send `X-Hadron-Session` (all reads use the
plain command context; pinned by the dry-run test).

## Tests

`internal/hostup/hostup_test.go` covers the decision table with fakes;
`internal/cmd/team_host_up_1389_test.go` runs the command against a stateful
fake queue, a fake Orca executable that refuses every non-read verb, and a
desired-state file: the issue's seven acceptance lines plus the addendum's
additions (never-act reasons, sweep mode, lock, `--worker`, no session header).
