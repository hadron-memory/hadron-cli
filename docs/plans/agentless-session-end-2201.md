# Prove no agent and end an agentless short-lived session (hadron-cli#2201, C10)

Design as built. Companion to hadron-server#4292 (S15), schema snapshot at
hadron-server `89cbf39a43`; spec `cor:agt:050:10` (Proposed), contract
`cor:agt:070:04` / `:08`.

## Problem

A SHORT_LIVED worker that finishes its task without ending its session leaves
the session open with no agent, indefinitely (orca1 test run 2, AT0.7687). The
server's `worker.end` came on time and the host reported FAILED "the worker was
not asked to end its session", with no proof either way about the agent.

## The one rule

**AGENT_ABSENT is said only on a complete, empty strict census.** The strict
census is the one C5 relaunches on (`relaunch_census_linux.go`, via
`probeRelaunchWorktree` and `retryCensusProbe`, the same three-probe retry). It
proves absence only when it is complete and every worktree of the worker is
empty. Each of these is *not* absence, and none leads a reason with
`AGENT_ABSENT` or calls the end: a census error, an observation not marked
proven, an agent found, no registered worktree, no session named, a live agent
this host launched, a worker the host does not mark short-lived.

`proveAgentAbsent` (`internal/hostpoller/agentless.go`) is the single place that
decides, and returns `Found` separately from "could not tell".

## Two paths, same proof

1. **`worker.end`** (`ops.go`, Ask 1 and 2). For a worker with
   `stop_on_end: always` (the host's transitional short-lived marker; the host
   config has no lifecycle field until S3 reaches `host.yaml`), no live
   launched agent and a session named: run the census over EVERY worktree the worker has here plus the tree of the
   launch this host remembers (each once; the launch record outlives its agent, so it
   never narrows the set). Proven empty: report FAILED with a reason that **leads** with
   `AGENT_ABSENT` (the server reads the leading code and ends the session), and
   skip the stop scan, since nothing runs. Anything else: report as before with
   the census result appended. The report stays FAILED: the host still ends no
   session itself.
2. **The sweep** (`relaunch_loop.go` pass, Ask 3). A row with desired STOPPED,
   reason NO_OPEN_TASK and an open session, under APPLY, is not a relaunch
   target. `sweepAgentlessEnd` takes the worktree launch reservation, checks no
   live launched agent, censuses the worker's one worktree, re-reads the desired
   state (APPLY, same App and host, same worker, same session, still
   STOPPED/NO_OPEN_TASK, and its worktree still resolving to the censused one) and only then calls `endAgentlessSession` with reason
   `AGENT_ABSENT` under the host key, with no worker session. The server checks
   the rest again (open task, short-lived, this host runs the worker, session is
   the worker's).

Leave-alone, paused, halted, resetting, retired, long-lived, no session and
OBSERVE/OFF never match the row shape, so they are not even censused.

## Run record (Ask acceptance 4)

S10 lines carry codes only: APPLIED `AGENT_ABSENT` is the end asked for on a
proven-empty census; SKIPPED `AGENT_PRESENT`, `CENSUS_UNPROVEN`,
`STATE_CHANGED`, `SESSION_ALREADY_ENDED`, or the server's closed refusal as its
own code (`WORKER_HAS_OPEN_TASK`, `WORKER_NOT_SHORT_LIVED`,
`HOST_DOES_NOT_RUN_WORKER`, `SESSION_NOT_WORKERS`, `END_UNSUPPORTED` for a server
that predates S15); FAILED only for an unexpected error (`END_FAILED`).
Refusals are skipped, not failed, because they are standing states that recur
every pass and a failed line writes an S10 record each time. The census detail
(worktree, pids, foreign-uid exclusions) goes to the local `agentless-end`
event, never to the server.

## host up

`host up` runs under the manager's credential, and both the proof (a `/proc`
census) and the call (a host-key mutation) belong to the host poller. So the
planner reports, never acts: a STOPPED/NO_OPEN_TASK worker with an open session
and no agent terminal is skipped `AGENTLESS_SESSION` (action label
`session.end`) instead of `IN_SYNC`. This departs from the issue's plan item 3
("a new item ... issuing the S15 end"): the item exists, but it does not issue.

## Limits

- The sweep path runs only where the periodic pass is wired: typed
  `launch_via: orca`, Linux. An exec-only host gets the `worker.end` path.
- `stop_on_end: always` is a proxy for SHORT_LIVED until the lifecycle reaches
  the host config; the server refuses `WORKER_NOT_SHORT_LIVED` regardless.
- The census excludes proven foreign-uid processes (#2195): an agent running
  under another uid is not seen, as for relaunch.
- Ending Cooper's current session is out of scope (a person does it).
