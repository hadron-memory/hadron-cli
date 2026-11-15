# C1 team sweep and poller-owned recovery reports

C1 consumes S10 from server fd9442d67 and the merged C5 (#2120) recovery owner.
Implementation is integrated; independent daemon-recovery acceptance is still
pending. No production host is activated by this PR.

Every completed `team host up` engine pass can record its start/end, effective
OBSERVE/APPLY mode, planned/applied/skipped/failed counts, and at most 50 worker
outcome/reason-code lines. `--config` supplies a separate host-key client from
0600 `key_file`. It never substitutes that key for reconciliation's existing
manager credential and never adds a worker session. The host-key gate proves
both App scope and the host identity before the record. A different configured
server is not used to record another deployment's reconciliation. `--run-id`
reuses a host token; S10 makes a replay the same row. A fresh UUID is the default.

No config records nothing and reports NO_HOST_KEY without changing the reconcile
exit code. OFF records nothing. A server without S10 skips the separate record
selection with S10_UNAVAILABLE; it cannot invalidate the older reconcile query.
Other record failures are printed as codes and do not change reconciliation's
exit (Wolfgang #109123). S10's stale-sweep alarm surfaces five minutes without
records. The CLI makes no team-chat post. The server owns summaries and alarms.
`team host sweep-runs --host <id>` reads bounded newest-first history with the
normal read credential and explicit DTOs; empty history is `[]`.

`team host sweep --config <host.yaml>` takes its host id from saved poller state
and invokes the same host-up path. It uses the config's App/server, not an ambient
worktree binding. The applying run lock is shared with host up, nonblocking and
released by the OS on death. The App's mode still gates worker actions. C1 does
not call a host-local relaunch runner; C5 owns recovery inside Poller.Step/Run.

The systemd user install writes a one-minute timer and a oneshot service with
Restart=on-failure, RestartSec=10s, StartLimitBurst=3 in a 60-second window. Normal
one-minute starts do not exhaust that window; a crash loop does. OnFailure invokes
a separate fixed-code journal logger; no key or command output is passed to it.
There is no schedule hidden in a script. Cron/launchd are refused for this job.
Installing does not change App sweepMode. Existing 15-minute team-health and
5-minute report-stops timers remain: they still own their health and pending-stop
report duties. Reinstall is idempotent; uninstall removes only sweep units.

## Recovery and outage integration

C5 remains the only owner, through host run's periodic recovery job. C1's S10
observer records only passes with applied or failed outcomes, including the
first pending ORCA_DOWN announcement; it creates no IPC, actuator or Runner.
Routine empty, OBSERVE and skip-only C5 passes write no S10 row, so they cannot
mask a stopped one-minute timer. Counts and code-only lines are recorded with
the host key after launch admission and the journal lock are released. Report failures log codes but do not change the recovery result.
The C1 command records OBSERVE metadata; OFF records nothing. A malformed or missing
source identity never becomes an empty proof of safety.

For typed Orca-launch hosts, the sweep and C5 share the managed-daemon
preflight: exactly one running
own-user daemon scope plus a successful terminal list. A missing managed scope
or an unreachable terminal list is ORCA_DOWN. Unreadable/ambiguous scope proof
stays RELAUNCH_UNPROVEN, never asserted as a confirmed outage. The queue-only
sweep takes the shared nonblocking host-up lock before this check; an unavailable
pass reads desired state and records skipped metadata but queues no operation.
Exec-launch hosts retain the ordinary host-up readiness check without requiring
C5's systemd daemon scope. Both readiness and reconciliation select the same
configured Orca binary. Maintenance calls strip worker-session attribution.
An expected ORCA_DOWN/RELAUNCH_UNPROVEN observation exits zero after writing its
result; the next timer tick retries it. Lock contention still exits5 immediately,
but RestartPreventExitStatus=2 5 prevents service restarts for that result.
Exec-launch preflight failures retain the ordinary Usage exit2 before a report;
they also wait for the next timer tick instead of restarting. Missing records
remain visible to the server stale-sweep alarm. Actual crashes retain bounded
Restart=on-failure behavior. The failure logger
is resolved through the install environment and its absolute path is saved.

C5's first applying ORCA_DOWN observation for an eligible should-run worker
records failed outcome lines, so S10 can announce that outage once. The report
token is persisted in existing poller state before sending. A lost or refused
answer retries the same row; the episode is marked reported only after explicit
S10 confirmation. Subsequent C5 outage passes send nothing; the one-minute
C1 command records quiet skipped lines without another summary. A successful readiness observation
ends the daemon episode. Paused/halted/leave-alone/resetting rows do not create
an outage announcement or a launch. The dark-worker repair notices and episode
identity remain C5/server-owned; no third alarm is added.

Recovery tests pin two observations then one launch, a fresh recovery prompt,
tracking of the existing session, and no second launch while the agent lives.
The real daemon-kill leg belongs to Celine, alongside Colin's independent QA.
The target uses the one-minute C1 timer and C5's default 30-second polling (two
absence observations), plus bounded launcher/reaping time and the three-attempts
per rolling hour cap. A custom slow poll interval or unavailable prerequisites
can miss the approximately two-minute target; no hard OS latency guarantee is
claimed. Daemon restart itself remains the service manager's job.
