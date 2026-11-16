# Repeating C1's scheduler acceptance

Normal Linux package tests run `systemd-analyze verify` on the three generated sweep units when that executable is available. This checks syntax and executable references, without starting services.

The runtime test needs a **disposable systemd container**, root inside it, and no installed Hadron units. It deliberately installs the emitted service, timer and failure logger under `/run/systemd/system`, then removes them. It refuses an existing unit or a host without `/run/systemd/container`. Never run it in a shared QA container or a production host.

Build a Linux test binary, copy it into your fresh container, and run:

```sh
CGO_ENABLED=0 GOOS=linux go test -c ./internal/healthsched -o /tmp/healthsched.test
# Copy into your disposable container with your container tool, then inside it:
HADRON_TEST_SYSTEMD=1 /tmp/healthsched.test -test.run '^TestSweepSystemdAcceptance2183$' -test.v -test.timeout 5m
```

Allow two to four minutes. The test runs the actual generated units with an effect-free fake CLI and logger. It verifies exit 2 and 5 stay stopped beyond the shipped 10-second restart delay, OnFailure runs, the one-minute timer retries a refusal, and SIGKILL triggers a restart. It uses systemd's recorded exit status and NRestarts as well as invocation logs. Cleanup runs even after an assertion fails; destroy your disposable container afterward. This is scheduler policy acceptance, not a live Hadron server or worker recovery test. It does not exercise the three-failures-per-minute burst cap or claim crash exit 2 restarts.

## S10 replay contract

The poller unit test covers a committed report whose response is lost, a state reload, a retry two hours later with changed worker counts, and acknowledgement surviving another reload. S10 validates each request before checking the `(app, host, runId)` conflict; a valid duplicate returns the original row and does not replace its payload. Malformed retries may still fail validation. No immutable-payload persistence is required by that contract.

To check the actual server helpers, prepare an explicit clean server revision with its dependencies and generated Prisma client, then run from that server directory:

```sh
HADRON_SERVER_DIR="$PWD" node --import tsx /path/to/hadron-cli/scripts/check-s10-replay-contract.mjs
```

The script prints the chosen server SHA. It invokes the exported validator and insert helper with an in-memory SQL transaction seam, proving acceptance of valid changed retries and rejection of malformed retries before insertion. The resolver's validate-before-insert ordering must also be reviewed in `src/api/graphql/schema/resolvers.hostSweepRuns.ts`. This probe does not test database concurrency, authorization, or summary delivery. It needs no database credentials or live stack.
