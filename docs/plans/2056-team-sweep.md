# C1 team sweep — implementation in progress

C1 consumes S10 from server fd9442d67. The repair integration remains gated on
C5 (#2120); this document does not claim daemon-recovery acceptance has passed.

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

Remaining after C5 merges: merge main (no rebase), report its relaunch ownership
state, preserve no-parking/leave-alone/pause/halt/resetting/unbound/only-agent
barriers, and independently test daemon-kill recovery with real Orca in a private
container. No production host or installed unit is changed by this development.
