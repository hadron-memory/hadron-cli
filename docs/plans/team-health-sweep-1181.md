# Team health sweep (#1181)

`hadron team health sweep --config <host.yaml>` records facts from this host's
terminal-status command. It does not classify screens, type into terminals,
alarm, or install a schedule.

The host configuration and its mode-0600 key file supply the App and authority.
The sweep reads the live host registration from its persisted host id, runs the
same executable's `orca terminal status --all --json --config <absolute path>`,
and consumes only terminal-status@1 JSON. The status command owns Orca discovery
(`delivery.orca_bin`, otherwise PATH) and all classification rules. Its stderr
is discarded; stdout is bounded to 8 MiB and overflow cancels the child. Credential environment variables and
variables containing the host key are removed before launching it.

The decoder accepts only known status, prompt and reason codes and valid times.
It requires every registered worker to resolve unambiguously and have a report,
including paused workers. An unresolved registration or missing status row
refuses the whole batch before writing; other hosts' status rows are ignored.
Only worker id, status, prompt, reason, lastTurnAt and checkedAt enter the typed
mutation. No screen detail or terminal handle is retained in the output DTO.
One batch is sent; more than the server's 200-worker limit refuses before writing.
Every submitted item must be accounted for by the server as stored or refused.
Per-item WORKER_NOT_FOUND and TERMINAL_REPORT_STALE are printed without retry.

Human output is one safe name/status/reason line per worker, with a refusal
suffix when applicable. JSON is `{reports,refused}` with explicit arrays.
Exit 0 includes per-item refusals and an App without a coordinator; 1 means
invalid contract or failed write, 3 key rejection, 7 server unreachable, 8
forbidden, and 10 Orca unreachable. Argument/config usage remains 2. Server
alarm/noCoordinator extras have no influence on output or exit.

## Deployment boundary

No production sweep and no #1374 installation until server#2931 is deployed.
The schema snapshot is refreshed from deployed server 6cfeae5b88687dbc7b9ec4862ee86a95ef6ca31d
(Hugo, chat #54432); the #2930 merge e70f2fdb is independently verified as an ancestor.
That shape has only stored/refused. Unit and compiled-binary checks use synthetic
fixtures only. The #2931 production/install hold remains independent.
