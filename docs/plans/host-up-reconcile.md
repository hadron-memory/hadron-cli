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

## Server adapter (second PR: closes #1389 and #2069)

The default source is now the server's `hostDesiredState(appRef, hostId)` (S3, hadron-server#4242, snapshot pinned at e34194de until it merges). `--desired-state-file` stays for tests and offline dry runs. The DTO gains `source` (`server` or `file`).

Mapping, server row to engine worker:
- `openSessionId` becomes the session.
- `family` becomes the setup family.
- A null `desired` stays empty, so the engine never acts. Its `reason` (`LEAVE_ALONE`/`SWEEP_OFF`) is one of the never-act reasons, and any other reason with a null `desired` reads `UNKNOWN_DESIRED`.
- `worktrees` must hold exactly one host name token, which becomes the setup worktree. Zero, several or a malformed value sets `Setup.Problem`, and the engine reports `SETUP_INVALID` before reading the host for that worker.
- There is no repo until S1a (#4222), so a missing worktree is `NO_WORKTREE_SOURCE`.
- An answer for another host is exit 1, because it is the server's fault, not usage.

Decisions taken without Holger: exactly one registered worktree, else `SETUP_INVALID`; no worktree creation from the server source before S1a.

#2069: the fake queue scripts per-operation transitions and stalls. The tests cover:
- a stalled operation that times out, reported with its id and left running;
- ISSUED → COLLECTED → SUCCEEDED;
- an adopted operation that ends FAILED, with no new mutation;
- the polled operation followed over a newer identical one (Rook's G1).

## Fix head after review (cli#2064)

- The onboard queue's active-only filter keeps the operation it is polling, so an operation issued or adopted while ISSUED is followed to SUCCEEDED/FAILED (Jake, local P1).
- The desired-state file must end after its JSON document; a stray `]`, `}` or anything else exits 2 (Julius, blind P2).
- `--worker` resolves an exact id first, else a name only one worker has; a shared name refuses (Julius, blind P2).
- An agent terminal counts whether or not Orca reports it connected (Julius, blind P3: uncertain counts as present).
- The fake queue can complete operations asynchronously; tests cover adoption of an in-flight operation and a conflicting one (Julius, blind P3).
- Every operation is issued as the person running the command, never as a worker bound to the worktree, and `--as-person` is gone: a worker actor cannot start a worker, so a worker-attributed run could create a worktree and then be refused the start. No request, read or write, carries `X-Hadron-Session` (Rook, §10 P2; pinned by an apply from a bound worktree).
- The exit-3 abort arm is pinned beside the exit-8 one (Rook, §10 P3).
- A refused registration read stops the run with its own code, read once, before anything is issued; only plain engine errors become exit 2, a coded server error keeps its code (Rook, §10 note).

## Never

Cast, bind, rebind, `session start`/`end`, `--force`, remove a worktree, call
an Orca create or launch verb, or send `X-Hadron-Session` (reads use the plain
command context and the issuing mutation goes out as the person; pinned by the
dry-run and apply tests, both from a bound worktree).

## Tests

`internal/hostup/hostup_test.go` covers the decision table with fakes;
`internal/cmd/team_host_up_1389_test.go` runs the command against a stateful
fake queue, a fake Orca executable that refuses every non-read verb, and a
desired-state file: the issue's seven acceptance lines plus the addendum's
additions (never-act reasons, sweep mode, lock, `--worker`, no session header).
