# C12: a context reset relaunches at once (hadron-cli#2161)

Design as built. The design note is on the issue (comment 6902973636). Wolfgang adopted it in team chat #109662, and it supersedes his first default (a detached helper). Holger chose the names (#109658): the command is `hadron team session reset-context`, and handoff nodes go under `checkpoints:`.

## The problem
Before C12, after a shed a fresh agent appeared only when C5's periodic relaunch saw the worker absent on two consecutive sweeps. That took one to two minutes. Holger: "a shed must not wait 1–2 min for the sweep."

## The flow
1. **The agent runs `hadron team session reset-context`.** It writes:
   - the checkpoint node (the shed body, under `checkpoints:<YYYY-MM-DD-HHMMSS>`), read back;
   - then the resume claim;
   - then the relaunch stamp `hadron-team-relaunch.json` beside the binding, under the binding lock, atomically. The stamp is `{version: 1, workerId, sessionId, checkpoint, requestedAt}`.
2. **It ends its own agent.**
   - The Orca launch exports `HADRON_AGENT_PID="$$"` in the launch file. The file is sourced by the shell that then `exec`s the agent, so the value is the pid the launcher pinned. The line comes last, so an inherited value never wins, and it is never part of the typed text.
   - The command sends SIGTERM to that pid through a pidfd, only while the pid is an ancestor of the command. The ancestry is checked again after the pidfd pins the process.
   - Anything less is `exit: exit-now`: nothing is signalled, and the agent is told to exit.
3. **The poller is told by the kernel.** `trackAgent` starts a watcher on the agent's `Done` (a pidfd for an Orca agent). The exit wakes the run loop, which runs an exit pass at once. The exit pass never brings the next check-in forward.
4. **The exit pass is C5.**
   - It needs the tracked agent to still be the one that exited, an Orca worker, and a stamp present. Without all three it makes no network call.
   - It reads the desired state fresh and opens the attempt journal under the same exclusive lock. The journal open (`OpenJournalHeld`) claims no generation, so the other workers' absence continuity is untouched.
   - It then runs the same `Runner` the sweep builds (`relaunchRunner`, which both passes call). The Runner has one hook, `ExitProof`. It is asked under the binding lock, after every guard and the cap, in place of the two-sweep rule and nothing else. It holds only when:
     - the old agent's `Done` has fired;
     - the old agent is still the tracked one;
     - the stamp names this worker and session and is under two minutes old.
   - The stamp is used up by being read, so one stamp gives at most one relaunch.
5. **The new agent's first prompt is C5's own** (`session recover`, with no task and no `--resume`). It finds the binding, the claim and the checkpoint.

## What the stamp can and cannot do (Rook's §10 check)
The stamp is a file the worker can write, so it proves nothing on its own.
- **It needs the exit of the agent this poller launched.** A stamp beside a running agent, or one left after an agent that a later launch replaced, does nothing.
- **It never names another session or worker.** It must match the target, which comes from desired state and has already been checked against the binding.
- **It never lifts a refusal.** RESETTING, PAUSED, HALTED, TEAM_PAUSED, RETIRED, SWEEP_OFF, LEAVE_ALONE, not RUNNING, an unknown reason, a mismatched binding, a present agent and an unproven census are all decided by the guard before the proof is asked.
- **It never lifts the cap.** The cap is checked first, so a capped exit doesn't even consume the stamp.

At most, a stamp makes a relaunch C5 would make happen a sweep or two sooner.

## Fail closed
Each of the following launches nothing early. The ordinary sweep (two absences) brings the worker back:
- no stamp, or a stale or foreign one;
- an exec worker;
- a poller restart (the agent is untracked, so there is no watcher);
- no poller at all;
- an unreadable source or journal;
- a refusal or the cap.

## Decisions (reversible, Wolfgang #109662)
- **A context reset counts toward the 3-per-hour cap.** That also stops a reset loop.
- **The CLI ends the agent by exact pid.** Claude Code runs each tool command in its own session, so the session leader is not the agent. Hence the exported pid plus the ancestor check.
- **`shed` is unchanged** until C4 rewrites the skills: it writes no stamp and ends nothing.
- **No server change.** hadron-server#4269 is closed as not needed (Peter #109657).

## Tests
- **Runner:** `exitproof_2161_test.go`. The exit proof launches on the first observation. An unproven exit pass records nothing. The proof lifts neither the cap nor any refusal.
- **Journal:** `journal_held_2161_linux_test.go`. Same lock, same cap, no generation claimed.
- **Stamp:** `relaunch_stamp_2161_test.go`. Consumed once, matches only its own worker and session, fresh, from a regular file, never through a link.
- **Poller:** `relaunch_exit_2161_test.go`. A relaunch within two seconds with no second sweep. Nothing for a missing, foreign, stale or capped stamp, a paused worker, or a worker C5 would not relaunch. A stamp alone never launches.
- **Launch:** `orcaagentpid_2161_test.go`. `HADRON_AGENT_PID` is the pinned pid and is not in the typed text.
- **CLI:** `team_session_reset_context_2161_test.go` and `reset_context_end_linux_2161_test.go`:
  - the order: checkpoint, claim, stamp;
  - a failed checkpoint gives no stamp;
  - `shed` is unchanged;
  - a real bash ancestor is ended by SIGTERM;
  - no non-ancestor is ever signalled.
