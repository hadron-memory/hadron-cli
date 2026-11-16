# Repeating the host sweep scheduler acceptance

Normal Linux package tests run `systemd-analyze verify` on the three generated sweep units when that executable is available. This fails on warnings as well as a nonzero exit, checking syntax and executable references without starting services.

The runtime test needs a **disposable systemd container**, root inside it, and no installed Hadron units. It deliberately installs the emitted service, timer and failure logger under `/run/systemd/system`, then removes them. It refuses an existing unit or a host without `/run/systemd/container`. Never run it in a shared QA container or a production host.

Wait until your fresh container’s system manager is running (`systemctl is-system-running --wait`). Build a Linux test binary, copy it into the container, and run:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/healthsched -o /tmp/healthsched.test
# Copy into your disposable container with your container tool, then inside it:
HADRON_TEST_SYSTEMD=1 /tmp/healthsched.test -test.run '^TestSweepSystemdAcceptance2183$' -test.v -test.timeout 10m
```

Match GOARCH to your container. Allow two to six minutes; timer waits include systemd’s actual accuracy and randomized-delay windows. The test runs the actual generated units with an effect-free fake CLI and logger. It verifies exit 2 and 5 stay stopped beyond the shipped 10-second restart delay, OnFailure runs, the one-minute timer retries a refusal, and SIGKILL triggers a restart. It uses systemd's recorded exit status and NRestarts as well as invocation logs. Cleanup is registered before writing units and runs on ordinary assertion failures. It is best-effort and does not run after a process kill or test timeout: always destroy your disposable container afterward. This is scheduler policy acceptance, not a live Hadron server or worker recovery test. It does not exhaust the three-starts-per-minute burst cap. Go panic/fatal exits use status 2 too: the service does not immediately restart those, and recovery waits for the next timer tick.

## Host sweep run replay contract (S10)

The poller unit test covers a committed report whose response is lost, a state reload, a retry two hours later with changed worker counts, and acknowledgement surviving another reload. S10 validates each request before checking the `(app, host, runId)` conflict; a valid duplicate returns the original row and does not replace its payload. Malformed retries may still fail validation. No immutable-payload persistence is required by that contract.

To check the actual server resolver, prepare an explicit server revision with no tracked source changes, its dependencies and generated Prisma client, then run from that server directory (Node 22 with module-mock support):

```sh
HADRON_SERVER_DIR="$PWD" node --experimental-test-module-mocks --import tsx /path/to/hadron-cli/scripts/check-s10-replay-contract.mjs
```

The script prints the chosen server SHA and invokes the actual `recordHostSweepRun` resolver. It stubs authorization, audit and notification plumbing, with an in-memory SQL transaction seam underneath the actual insert helper. It checks that a valid changed retry returns the original record and requests one summary, malformed retries fail before SQL, and a distinct run token creates a second record. No validation ordering is reimplemented by the probe. This is not a live database, authorization, concurrency or delivered-summary test; it needs no database credentials.

S10 is the existing server workstream for sweep records. A run token identifies the first accepted snapshot, not an accumulating inventory of every worker seen on later retries. The poller test checks persisted token/acknowledgement and current retry payload only; the resolver probe checks first-row-wins behavior. Replayed newer counts are not added to the original row. No audit of downstream consumers is claimed here.
