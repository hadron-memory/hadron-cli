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
5. **The new agent's first prompt is C5's own** (`session recover`, with no task and no `--resume`), plus the checkpoint to read. `session recover` reads the state note, tasks and mentions, but not checkpoints, so the exit pass carries the stamp's checkpoint into the prompt. A stamp may name only a `checkpoints:` node URN (`delivery.ValidCheckpointURN`), because the value reaches the prompt. The resume claim is not needed: the relaunch keeps the binding file, and recovery reads from it. The claim only matters for a `session start` with the binding gone.

## Fix head after review (Jibril P2, Julius P2, Jibril P2)
- **A lost exit wake.**
  - The bug: the job's two cleanup defers ran in reverse, so the re-wake check saw the job's context already cancelled. An exit that came in while another job held the lock waited for the next check-in.
  - The fix: one ordered cleanup that checks the run's own context. If no wake is taken, the next check-in runs pending exits first, and the two-absence sweep stays the backstop.
- **A false "signalled".** The receipt now reports `endOwnAgent`'s real result. It says "signalled" only after the signal was delivered to the pinned ancestor; any failure is `exit-now`.
- **The checkpoint** (found while answering Jibril's claim P3): see step 5.
- **Rook §10 P2:** the poller scrubs its own `HADRON_AGENT_PID` from what it passes on (`scrubbedEnv`). Otherwise an exec agent launched by a poller that itself runs inside an Orca agent's tree could end that agent, which really is its ancestor. Rook's two P3s are pinned too: a stamp that cannot be removed proves nothing, and a FIFO with a live writer is refused within a bound.
- **Jibril delta P2 (signal before receipt):** everything that can refuse (parse, ancestry, pidfd pin, ancestry again) happens before the receipt. The receipt is written while the agent still reads it, and only then is the pinned agent signalled (`finishResetContext`). A signal that still fails says "exit it now" on stderr.
- **Tests:** the negative exit tests now wait for the exit pass to finish and assert the reason the Runner logged. A test pins that an exit wake keeps the check-in deadline.

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
