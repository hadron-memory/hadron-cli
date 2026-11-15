# C5 host-local relaunch (#2060)

The periodic host poller owns relaunch. It shares C3's tracked agents, pidfd
identities and worktree launch reservation. There is no new RPC, inbox or
independent trigger. Host-up and C1 report RELAUNCH_PENDING_POLLER only with a
proven live poller association; otherwise they report RELAUNCH_UNPROVEN. Their
actuator cannot launch an open-session worker.

An applying sweep reads canonical App identity through myApp and desired host
state without a session header. The source server, host, worker, open session,
absolute registered worktree and complete on-disk binding must agree. Plain
worktree bindings work without Git. Only RUNNING LONG_LIVED or HOLDS_TASK rows
under APPLY may proceed. Pauses, halts, leave-alone, unknown state, incomplete
bindings and ambiguous identities refuse. Profiles and accounts come from the
poller configuration until S1b; preparation snapshots them without disk writes.
The launch uses that same immutable snapshot, an empty grant and a fresh
session-recover prompt: no bind, force, end or resume.

Two adjacent persisted sweep generations must prove zero matching agents with
the same identity and profile. A per-poller epoch requires two fresh observations
after restart or source failure, while preserving the attempt budget. Strict
Linux proc scans include the caller and ancestors and refuse partial live reads,
PID identity changes or unknown visibility. Verified kernel threads and exited
processes do not count as live agents. Ambiguous wrapper chains refuse rather
than collapse into one process. Existing observational cleanup scans are unchanged.
There is no host-global or cross-process start fence; the reservation serializes
C3 and C5 inside this poller. Fresh proofs still surround admission and launch.

Before spending an attempt, Linux must prove exactly one running own-user
orca-daemon scope through systemd AND obtain a successful terminal list. Missing
systemd proof, zero or multiple scopes, or list failure yields RELAUNCH_UNPROVEN
and launches nothing (Wolfgang #108866). Serve readiness alone is insufficient.
A private-container measurement is assigned separately to establish actual Orca
behavior after PTY-daemon death. A less restrictive check belongs to a follow-on.

Attempts are persisted before acting, at most three per worker per rolling hour,
across sessions, profiles and poller restarts. Future timestamps remain counted
on clock rollback. The journal requires private owned directories and regular
0600 files, refuses links/FIFOs/corrupt or oversized JSON, uses a run-level flock
and fd-anchored atomic replacement with fsync. Dry runs create no directories,
locks, journal writes, repair notices or session logs.

C3 runs plainly in Orca's terminal scope, without a per-agent PID namespace.
Resource caps remain; the old claim that pkill costs only one run is withdrawn
(Holger via #108664). After this launch alone, a bounded cold-start wait allows
proven zero to become the one pinned agent. Unknown evidence or a foreign or
extra process refuses immediately. The default verifier requires the sole agent
PID to equal the returned live pidfd-pinned identity. It then rechecks desired
state and binding. Failure stops only the returned agent; any still-live returned
agent is tracked before releasing admission. There is no second launch retry
inside an attempt. Profiles that fork a separate agent instead of exec require
additional identity support; family and cwd alone never authorize adoption.

The schema is exported from merged server revision
10559e7e034e421bd23ac0fcc745ca92f7c77c26 (#4249). Typed repair notices use
RELAUNCH_REFUSED and RELAUNCH_FAILED only with a supported observed reason.
CAP_REACHED means the actual rolling cap was reached. The report timestamp is
the earlier proven absence observation, not a claim that a newly launched agent
is dark. Optional repairReason is omitted from legacy requests, never sent as
null. Repair transport and server refusals remain visible; a notice is marked
sent only after confirmed acceptance. Local deduplication is not a durable
outbox guarantee.

Known contract limit (Wolfgang #108826): setup/disk errors or an unproven
post-launch identity can be FAILED without a typed failure reason. They remain
local with noticeFailed and a bounded, credential-redacted local cause in JSON
and logs. No borrowed code or false CAP_REACHED is sent. Peter owns the generic
server failure-reason follow-on; C5 does not wait for it.

Author tests cover pure guards, journal safety and restart budgets, strict
census, session-header removal, wire omissions and reports, shared admission,
readiness failures and a private native PID/TTY cold-start fixture without Git.
That fixture is not a real Orca container or daemon-recovery acceptance. The
independent QA gate includes real-process cases and a private-container PTY
daemon death and recovery, with Sonnet-class execution. Never kill the shared
host daemon or invoke a paid provider for this work.

C3 #2088 is the launch dependency, currently incorporated through 9e715787.
C5 remains unfinished and unfrozen pending broader author checks, dependency
status and independent seats. Schedule: T2 plus security; Jerry BLIND, Jonas
LOCAL, Rodolfo security, Colin QA, subject to fresh roster/register checks.
No activation or production relaunch is claimed. No platform citation is minted.
