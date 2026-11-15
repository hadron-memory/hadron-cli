# C5 host-local relaunch (#2060)

The periodic host poller owns relaunch. It shares C3's tracked agents, pidfd
identities and worktree launch reservation. There is no new RPC, inbox or
independent trigger. The current host-up CLI has no adapter proving a live
poller association: it reports RELAUNCH_UNPROVEN and never triggers recovery.
The engine retains a PollerConnected extension point for a future in-process
caller, but RELAUNCH_PENDING_POLLER is not a current CLI result. Its actuator
cannot launch an open-session worker.

Each periodic tick may start one owned recovery job; a busy job makes later
ticks skip recovery without building a backlog. Registration, collection and
delivery continue while a launch or repair report waits. Run cancels and joins
the job on exit, including fatal check-in errors; a one-shot poll waits for it.
The first guard uses that job's discovery snapshot, so healthy workers add no
host-wide snapshot reads. Guards after admission and before/after launch still
read fresh state. A recovery sweep means an actual owned pass, not a tick that
skipped a busy job. Only completed observations contribute to continuity.
An owned live agent is tracked before admission and binding release; repair
network I/O follows release.

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
Celine's real Orca 1.4.247 private-container measurement confirmed terminal
list can succeed with zero scopes, and a stale scope can briefly remain after
daemon death. With zero terminals, the daemon respawned within seconds and
serve restoration recovered the scope/list. This does not prove live-worker
recovery; that acceptance remains independent QA work. A less restrictive or
stronger PID-aware check belongs to a follow-on (finding
`c5-pty-daemon-readiness-measurement-1.4.247`).

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
25920b11207db1230755f1fe6bf990e1565d18a2 (#4264, adopting #4259). Typed repair notices use
RELAUNCH_REFUSED and RELAUNCH_FAILED only with a supported observed reason.
CAP_REACHED means the actual rolling cap was reached. The report timestamp is
the earlier proven absence observation, not a claim that a newly launched agent
is dark. Optional repairReason is omitted from legacy requests, never sent as
null. Repair transport and server refusals remain visible; a notice is marked
sent only after confirmed acceptance. Local deduplication is not a durable
outbox guarantee.

Failure classification follows the observed phase. Local preparation/disk
failure before the launcher runs is SETUP_FAILED; a failed process proof after
launch is VERIFY_FAILED. Actual observed timeouts/refusals/exits retain their
existing codes. Cancellation, ambiguous create replies and fresh server/binding
refusals are not guessed to be either new reason. A failed cleanup never changes
the classification of the original failure or permits another launch.

The two new report documents name their enum literals statically. This is
intentional: an older provider rejects the document through schema validation,
whereas unknown enum values inside variables are redacted as generic
BAD_USER_INPUT. The normal api.MapError schema-skew path keeps the failure
local with noticeFailed and the original localCause; it never retries under a
borrowed reason. Established reason documents are unchanged. Notice acceptance
is recorded only after the server confirms the worker was stored. The refreshed
AssignHostWorkerInput's optional accountProfile and effort fields remain omitted
when unset, preserving older-provider compatibility for unrelated assignments.

Author tests cover pure guards, journal safety and restart budgets, strict
census, session-header removal, wire omissions and reports, shared admission,
readiness failures and a private native PID/TTY cold-start fixture without Git.
That fixture is not a real Orca container or daemon-recovery acceptance. The
independent QA gate includes real-process cases and a private-container PTY
daemon death and recovery, with Sonnet-class execution. Never kill the shared
host daemon or invoke a paid provider for this work.

Independent review and QA receipts belong to PR #2120 at their exact tested
heads. The fix cycle covers healthy-worker snapshot fan-out, collector progress
during blocked recovery, cancellation and joining, shared admission, and the
production pidfd verifier. The CLI remains built and off pending the coordinator's
activation gate. No platform citation is minted.

Known launch limit: #2130 tracks an ambiguous Orca create reply that can leave an
untracked agent. A moved-out orphan is invisible to this worktree's absence census.
C3's approved go-gate fix removes that reply-loss path; C5 needs no separate
blocking-record adapter. Until #2130 merges it remains an activation precondition.
A poller crash after permission to start and before tracking can still leave an
untracked agent. The census detects a recognized agent whose cwd remains in this
worktree, including subdirectories; it does not prove global absence of processes
that moved elsewhere or use an unrecognized executable. Account wrappers must
ultimately exec a recognized agent; rejecting every wrapper basename would also
reject supported account launchers.

Live QA at the initial frozen head recovered one agent in the same open session
after private daemon loss, both with Git and in a separate no-Git folder. The
async recovery fix requires a new live acceptance receipt. Live HALTED execution
remains limited by STOP_SIGNAL_UNAVAILABLE; compiled controls independently pin
refusal. #2131 retains an unconfirmed empty-journal observation: available logs
do not establish a product or fixture cause, and clean repeats do not explain it.

Binding and source server URLs must use the same spelling apart from a trailing
slash. Different aliases are not inferred to identify the same deployment.

The host-wide desired snapshot contains no prompts or transcripts. Healthy
workers share one discovery snapshot. A launched worker requires up to four
additional fresh snapshots and one repair mutation. Colin's independent report
adapter measurement used 16,311-byte snapshots and 101 reads plus 100 writes
for 100 reports; that component measurement excludes runner guards and launch
preflight, so it is not a complete recovery benchmark. Targeted host-key worker
reads are filed as nonblocking server follow-on #4260. Generic honest failure
codes from #4259 are adopted by CLI#2149.
