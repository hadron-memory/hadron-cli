# worker.end stops the worker's process (C2a of hadron-cli#2057)

Slice of the Team on Orca epic (hadron-server#4229). Design as built.

## What changed
`worker.end` used to prompt, wait `end_wait` (15 m) and report FAILED; it never
touched the process. With the per-worker host-config field `stop_on_end` it now
also stops the worker's agent.

- `stop_on_end: never` (default, today's behaviour) | `manager` (only when the
  operation's issuer is a USER: a person who manages the App, not the
  PLATFORM's own and not a bound reports-to WORKER) | `always` (a short-lived worker). `WorkerConfig.stopsOnEnd`
  is the only lookup: **transitional**, removed with `host.yaml` (C4); S3's
  `Agent.lifecycle` replaces it. Decision recorded by Wolfgang (#107617).
- Wait: after a *delivered* end prompt, `stop_wait` (default 60 s) instead of
  `end_wait`, cut short if the launched agent exits. No end prompt, or one that
  failed: nobody was asked, so the stop is immediate. The host cannot read
  "session ended", so the bound is all it has.
- Outcome code unchanged: still FAILED, the server records an ended session as
  success (cor:agt:020:10: the host ends no session). The stop result (pid,
  SIGTERM/SIGKILL, or "not touched" and why) is in the report's reason.
  A session still open when its process is stopped stays open: S2
  (hadron-server#4223) ends it server-side.

## Which process
1. The agent this poller launched: `Agent.Terminate()` (its own launch group;
   records dropped only on a confirmed stop), after the same binding check.
2. Otherwise (a `worker-supervise` terminal agent, or a launch from before a
   restart): `ScanWorktreeAgents` (#2027's /proc cwd census, not a second
   implementation) over the worker's registered worktrees. A tree is skipped
   when its session binding names another worker, another session than the one
   being ended, or cannot be read. No binding (the worker ended its session) is
   the worker's own tree by this host's config.

## How it stops
`ExecHost.StopAgents` (Linux): every signal through a pidfd, with the start time
re-proved, so a reused pid is never signalled. The agent's process group is
signalled as a group only when every member works inside the worktree and none
is the caller or its ancestor; otherwise only the agent process, and the detail
says so. SIGTERM, `TerminateGrace` (5 s), SIGKILL, 2 s. Other platforms: no
`/proc`/pidfd, nothing is touched and the report says so.

## Not in this slice
C2b (workspace reset inside `worker.start`), C2c (wire `worker.reset-context`),
the server ending a session whose process was stopped (S2), a spec change
(client-only host config; the rule it serves, cor:agt:050:10, is unchanged).

## Review round 1 (at 3a46c23a)
- A launched agent is held to the same binding rules as a scanned one.
- The binding is asked again immediately before the first signal (`still`
  guard into `StopAgents`). A separate process writes the binding, so the
  window cannot be closed, only made as short as the signal.
- "Gone" is a definitive absence only (ESRCH / no /proc entry); a failed pin or
  probe is "not touched" / unconfirmed, never "stopped".
- The lead process's current directory is re-checked against the worktree.
- `manager` admits USER only.

## Follow-ups #2068 and #2070
- **Lock (#2068).** `delivery.LockBinding` takes the same `hadron-team-session.lock`
  `session start` writes under (flock / LockFileEx, a bounded try-lock, 2 s). Every
  signal is preceded by `Authorize`: take the lock, re-read the binding under it,
  signal, release at once. Never held across the SIGTERM grace or any wait. For a
  launched agent, `execAgent.TerminateWith` asks before SIGTERM and again before
  SIGKILL. A lock that cannot be taken declines the stop and the report says so.
  A plain directory with no binding cache has nothing to lock.
- **Session identity (#2070).** The reset relaunch records the session the reset names;
  a launch the server had already finished has none recorded, so only a binding that
  names this worker AND the session being ended identifies it (an empty binding declines).

## Security review (§10) on #2073
- The stop creates and locks inside a tree the worker can write, so `LockBinding`
  follows no link: `Mkdir` (not `MkdirAll`) for a plain cache, `Lstat` the lock
  location and the lock file (a link or non-regular file is refused), `O_NOFOLLOW`
  on unix, and an `fstat` regular-file check after the open.
- **Residual, stated plainly:** the stop is fail-safe, not containment. A hostile
  agent can still decline its own stop (hold the lock for the 2 s bound, rewrite
  its binding, move its working directory); each of those makes the host signal
  nothing and say so. The host also trusts the worktree's `.git` pointer, as every
  binding reader here already does; on orca1 the poller and the agents share one uid.
- **Open without a check-then-open gap (blind delta).** On unix the lock is opened
  relative to a descriptor for its directory (`O_NOFOLLOW|O_DIRECTORY`, then
  `openat(..., O_NOFOLLOW)`), so a directory swapped for a link after the check is
  refused. On Windows each open uses `FILE_FLAG_OPEN_REPARSE_POINT` and the handle's
  attributes are checked, and the directory handle stays open (no delete-sharing) until
  the file is open and checked, so the directory cannot be renamed or replaced between
  the two opens. Not run on a Windows host (none available): it builds and vets clean,
  and the reparse-attribute rule is pinned as a platform-neutral unit.

## #2082: authority is proved again after the lock wait
`signalAuthorized` re-checks `stopAuthorityGone()` once the authorization (the binding lock, which can wait up
to 2 s) is held and immediately before the signal. On a launch without the zombie hold the leader's exit frees
the group's number unserialized with a stop, so a leader that exits during the wait could otherwise leave the
numeric group signal to reach a reused group. A lost authority signals nothing and reports the stop unconfirmed,
records kept.
Residual, by design and already documented in `team host run`'s help: on a launch that could not take the zombie
hold (a kernel or policy that refuses `waitid`, and the non-Linux unixes), the instant between the final
authority check and the `kill(2)` itself is not serialized with the leader's exit; the 2 s lock-wait window
that #2068 added is closed by the check above, and a hold-capable launch has no such gap.

## #2099: Windows checks exit after authorization too
The Windows launch stop checks `Done` and the waiter's recorded exit again after
authorization returns, before `taskkill /PID /T /F`. A launch that exited during
the binding-lock wait is reported as already exited, with no process touched;
the authorization is released and taskkill is not run. The portable decision
is shared with Windows production code so its interleavings run on Linux tests;
the Windows adapter has a separate test, cross-compiled but not run here.
This does not retain a process handle or establish identity atomically with
taskkill: the remaining final-check-to-taskkill PID-reuse window and descendant
tracking after the leader exits still need a handle/Job Object design. The
binding lock continues to span synchronous taskkill; its duration is the
separate follow-up #2087. No Windows runtime or PID-reuse reproduction is claimed.
