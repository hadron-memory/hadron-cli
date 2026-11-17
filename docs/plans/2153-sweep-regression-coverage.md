# S10 sweep regression coverage (#2153)

Test-only follow-up built from main `ff687c87`. Production guards and host units are unchanged. Tests use temporary files, loopback HTTP, injected schedulers and synthetic poller dependencies. No real Orca, systemd, cron, Docker or Incus operations are required.

## Acceptance map

| Acceptance | Coverage |
| --- | --- |
| Wrong host / absent host, no record mutation | Existing `TestSweepIdentityProvesHostWithScopedRead2137` cases `wrong-host` and `missing` |
| Config/reconcile alias identity | Existing valid/alias/error controls; new `second-app-wrong-host`, exact App-ref assertions, normalized-server positive control |
| Server mismatch, zero identity reads/writes | Existing `server-mismatch` case |
| Zero / two host registrations | Superseded by #2137's scoped `HostSweepIdentity -> hostDesiredState(hostId)` read. The test explicitly forbids `hostRegistrations` and checks the selected host. An absent identity remains a negative control; unrelated registrations are not the recorder's input. |
| Sweep command's own concurrent lock and explicit App/server conflicts | `TestSweepCommandScopeAndLockRefuseBeforeNetwork2153` |
| Dry run records OBSERVE; OFF/unknown modes write nothing | `TestSweepRecorderModes2153` |
| Run-id length / invalid characters | `TestSweepRunIDBoundariesBeforeReads2153` (command refusals plus valid bounded-token controls through `host up`, reaching the isolated fake-Orca refusal) |
| Cron / launchd refusal | `TestSweepRefusesCronAndLaunchdWithoutWrites2153`, no scheduler mutation or files |
| Failure unit and crash policy | Existing `TestSweepUnitHasCrashLimitAndOneMinuteTimer2056`, `TestSweepUnitInstallUninstallRetainsHealth2056`, `TestSweepFailureLoggerUsesInjectedPath2137` |
| Sweep status human / JSON, correct job, read-only | `TestSweepStatusUsesItsOwnJob2153` |
| History human output / limit boundaries | `TestSweepHistoryLimitsAndHumanOutput2153`; existing empty/malformed test retained |
| Episode identity renews across App or host; same-scope persisted acknowledgement | `TestSweepOutageScopeRenewsToken2153` |
| Token creation / acknowledgement / clear save failures | `TestSweepOutageSaveFailuresRollBack2153` |
| S10_UNAVAILABLE keeps episode pending, same-token retries, stops after acknowledgement | `TestSweepS10UnavailableKeepsTokenPendingAndRetries2153`; existing recorder compatibility case retained |

Only `host up` accepts `--run-id`; `host sweep` generates its token internally and exposes no such flag. The qualified-config same-App/server command control reaches a synthetic successful record, while the negative cases require their specific scope/lock refusal messages.

The first four guard families already have pins on main. This change strengthens the alias proof and fills the folded backlog; it does not recreate the retired host-registrations protocol.

## Spec boundary

The gate's FIT is test-only. Mode and token pins follow Proposed `cor:agt:070:09`; the server-side host-key rule is `cor:agt:070:04`. No new corpus rule is authored.

A token-grammar ambiguity was resolved before pinning it: Vera verified that the server RUN_KEY and CLI both require a letter or digit first, and clarified Proposed `cor:agt:070:09` accordingly (team #112616). The tests now pin `.run`, `_run`, `:run` and `-run` as invalid as well as the length/alphabet boundaries. This records existing behavior; no production or new-law change is part of this PR.

## Causal evidence

The guard-mutation matrix is retained with the PR's author evidence. Each entry verifies the edited source, records its hash and patch, and requires the named test to fail after successful compilation. Production source is restored after every run. A commuted scope-equality expression is the equivalent surviving control. Existing pins count as already-built evidence; new tests need not make correct production code fail unmodified.

The live two-minute real-Orca acceptance belongs to the separately assigned QA seat, not these author fixtures. Host APPLY-pilot units remain pinned.
