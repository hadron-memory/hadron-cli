# `worker.reset-context`: the host handler

Issue [#1043](https://github.com/hadron-memory/hadron-cli/issues/1043), the
CLI half of hadron-server#2162 (an Agent's `resetContextAfterTask`: when a
worker's task finishes, the platform sheds its context and keeps its
binding). The contract is cor:agt:050:27; the restart rows are part 11
(rulings:0110, Decided). The Agent flag shipped separately
(`agent update --reset-context-after-task`, #1551). This doc covers the
host handler only.

Built by Jonas against hadron-server seams #3273 (restart read, merged
`01879edc`), #3276 (checkpoint writer, merged `aa4263ab`), #3309
(pre-claim `HOST_UNSUPPORTED`, merged `f5aa10e2`) and #3300 (the registered
checkpoint pointer, PR #3313, merged `22cd6369`). The schema snapshot is
exported from `22cd6369`.

## What landed

Nothing user-visible. The handler is in `team host run`'s poller, and it
is **off**:

- `EnableResets` has no production caller, so a collected
  `worker.reset-context` answers `HOST_UNSUPPORTED` before anything else.
- Registration still declares exactly the configured capabilities.
  Advertising `worker.reset-context` is a separate, authorized activation
  (issue body, scope item 3). Until then the server never issues the
  operation to this host.

Files:

- `internal/hostpoller/reset_handler.go` has the pre-claim checks and the
  relaunch.
- `reset_forward.go` has `runReset`: claim, checkpoint, stop.
- `reset_recovery.go` has restart recovery: `decideRecovery`, a pure
  per-phase table, and `recoverReset`, which carries it out.
- `procctl*.go` has the pidfd and `CLOCK_BOOTTIME` process control. It is
  Linux only; other platforms refuse `HOST_UNSUPPORTED`.
- `gittree.go` has the Git determination (ruling 0012).
- `state.go` holds `ResetRecord`, the reset kept in the host's state file
  across a restart.
- `internal/cmd/team/host_reset.go` is the GraphQL adapter: the seven
  host-key operations in `hosts.graphql`.
- `config.go` adds the per-worker `recover:` argv. A worker without one is
  never reset on this host.

## The sequence

1. **Pre-claim checks**, in this order. Every refusal comes before any
   process control or protocol step:
   - the handler is enabled;
   - the operation names a `resetId`, `sessionId` and `taskId` (otherwise
     `HOST_UNSUPPORTED` naming the missing argument);
   - a recorded reset is recovered instead;
   - the worker is configured on this host, with a recover prompt;
   - an agent this poller launched is still running;
   - Git (`DIRTY_TREE`, `UNPUSHED_HEAD`), but only inside a Git tree.
     Untracked files count as dirty whatever `status.showUntrackedFiles`
     says. The upstream must be on a configured remote: a branch tracking
     a local branch (`branch.<name>.remote = .`) has no remote upstream;
   - the old process can be pinned by pidfd;
   - it is proven idle (`NOT_IDLE`; idle is never assumed).
2. **Record, then claim.** The reset is written to the state file before
   the claim goes out, so a restart at any later point recovers it.
3. **Checkpoint.** `registerContextResetRecovery` has the server write the
   checkpoint node: ids, the worktree and its upstream, nothing from the
   environment and no credential. The host never names a node itself
   (ruling 0090 A). The answer's `checkpointNode` (#3300) is recorded on
   disk before the stop is requested. With no node, or with no disk write,
   the reset fails `CHECKPOINT_FAILED` before any process control.
4. **Stop.** `t0` is read from `CLOCK_BOOTTIME` before `authorizeStop`. One
   `SIGTERM` is sent through the pinned pidfd, only inside the granted
   lease. A clock that cannot be read, at `t0` or at the check, is an
   error and nothing is sent; so is a check reading earlier than `t0`. A
   stop sent late is reported as `lateStopMs`, rounded up. The
   stop is never escalated: an agent still alive after the wait fails
   `RELAUNCH_BLOCKED`.
5. **Relaunch.** The agent is marked `Relaunching` on disk first, so it is
   never started twice. It is launched in the same worktree and observed
   as a new process identity. The recover prompt runs with
   `HADRON_SESSION_ID`, `HADRON_WORKER_ID` and `HADRON_RECOVERY_NODE`
   (ids only). Then `reportContextResetRelaunched`, and the operation
   reports SUCCEEDED.

If any step errs, in transport or with a typed rejection, the reset goes
to recovery, which acts on the phase the server holds rather than a guess
from the failed step. A step that refused has already ended the reset;
its refusal is reported. Nothing is retried.

## Restart recovery (cor:agt:050:27 part 11)

Recovery never sends a signal. An old process the host cannot observe
counts as alive.

| phase | condition | action |
|---|---|---|
| requested | | report FAILED `PROCESS_CHANGED` |
| claimed | | `relaunchFailed CHECKPOINT_FAILED` |
| checkpointed | | `relaunchFailed STAGING_FAILED` |
| stop-granted | old alive | `relaunchFailed RELAUNCH_BLOCKED` |
| stop-granted | old gone | `agentStopped` (no lateness), then relaunch |
| agent-stopped | old alive | report FAILED `PROCESS_CHANGED` |
| agent-stopped | old gone | relaunch |
| relaunched | new alive | report SUCCEEDED |
| relaunched | new not proven | report FAILED `RELAUNCH_UNVERIFIED` |
| done / refused / failed | | report once with the server's reason, drop the record |
| unknown | | keep the record, touch nothing |

A record left without the checkpoint node, because the restart came
between the register answer and the disk write, takes it from the
server's read. A relaunch already begun before the restart fails
`RELAUNCH_UNVERIFIED` and is never started again.

## Reportable codes

Every code a FAILED report carries is checked by
`TestResetReportsOnlyReportableReasons` against a copy of the server's
`HOST_REPORTED_REASONS` (types.ts at `f5aa10e2`), for the phase it is sent
from:

- `HOST_UNSUPPORTED` only before the claim (#3309);
- `CONTEXT_RESET_ENDED` only for an ended reset, which takes any code.

## Still out of scope

- **Activation:** declaring the capability, wiring `EnableResets` and an
  idle prover into `team host run`. These need their own authorization.
- **A terminal-read idle prover.** Without one, every reset refuses
  `NOT_IDLE`.

## Minimum server

The reset operations select `ContextReset.checkpointNode`, so they need
hadron-server `22cd6369` or later; an older server rejects the document.
Only the handler sends them, and it is off; activation must not come
before that server is deployed.
