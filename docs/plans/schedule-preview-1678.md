# Schedule preview (#1678)

`hadron schedule preview` performs one `previewAgentSchedule` query. It never
creates a schedule, adoption, run or throwaway trigger, and never retries with a
write. `schedule create` is unchanged. The command accepts create's App/entry,
Agent, principal, timing, budget and argument inputs; name, scope and policy are
not preview inputs. Invalid flags fail before opening the client.

The explicit output DTO is `{outcome,hash,reason,summary}`. ADOPTABLE exits 0;
CANNOT_BE_ADOPTED exits 5 with reason-only text and null hash/summary in JSON.
Its only reasons are MEMORY_UNREADABLE and OTHER. The server's schedule line is
printed directly. Human cells use SafeLine, while JSON keeps values unchanged.
Nil, malformed and older-provider replies fail nonzero without a fallback.

The result is a snapshot, not a reservation or authority to create. Changes to
the automation may change its hash. Preview does not validate policy or scope;
create may refuse either after ADOPTABLE. Worker scheduling requires human
authorization or an applicable grant, with admission decided by the server.
This change implements none of the evolving worker-permission work in #3483.

## Source and provider evidence

The provider floor used as evidence is server#3463's merge
b7cf14b0ccbe242128219d6d7193fd08861e2e5e, not an asserted earliest or deployed
version. The schema was deliberately re-exported from merged server
d0b374cd8accbc9c47cb0dbd3f6162fabc7a71c2 (including #3446). That export is
byte-identical to the existing snapshot; the new query adds generated types.
An initial export from the older provider merge would have removed unrelated
newer contracts and was discarded before implementation.

Per Xylon's gate clarification 6651740508, provider execution is reused from
server#3463 with a blob/section applicability map. At reviewed b36d3ab5 and merged
b7cf14b0, preview resolver, pins, summary builder and preview test files match.
Charles QA (6640222890) used real Apollo/private DB: preview/create hash equality,
missing entry, admin refusal parity and unchanged row counts. It was not HTTP or
CLI execution and did not alone prove unreadable entries. Rodolfo's scoped
security receipt (6640023797) independently covers unreadable caller/run-principal
states, reason collapse and no-write mutants. Andi LOCAL (6640375965) adds deleted
entry, byte bound, admin order and omitted-Agent/run-at parity. All three selected
schema contracts match; selected-type SHA256 is
3b70d31d37595deda3fde39092724fc1bbe6d4505df994f58762652935ad5923.

At schema source d0b374cd, preview/create/authority sections and pins/summary/test
blobs remain identical, but #3443 introduced two relevant transitive changes:

- Explicit Agent selection uses `isAgentInstalledForAppOperation`. The other
  changed resolver site is manual-run Agent selection, outside preview/create;
  omitted Agent input for preview/create stays null.
- `canReadMemory` / `buildAppMemoryMap` in `src/mcp/access-control.ts` now use
  `loadAppInstallationPage`. This is also used by captureSetupOutcome for caller
  and run-principal reads, so the original map's single-seam claim was incomplete.

The old runtime receipts do not establish execution of either new seam. The
helpers came from #3443; #3446 is the schema-source merge, not their origin.
[Cooper's bounded real-resolver/private-Postgres completion](https://github.com/hadron-memory/hadron-cli/pull/1683#issuecomment-6652313090)
covers explicit ROW/DERIVED, omitted Agent, preview/create hash equality, no
preview writes, and collapsed unlicensed/uninstalled/unknown refusal at d0b.
It is not an HTTP/CLI run. Independent memory-read coverage is tracked separately
under AT0.4734.7: a positive control must actually reach the changed read fold;
Riana's all-unreadable probes do not prove that. The final PR receipt must name
that completion or the remaining limit before ready. Exact map/correction:
[saved and read back](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-dev-team-jane:work:cli1678:provider-seam-correction).

Schema compatibility is distinct from runtime proof. No new worker-rule,
production, concurrency or deployed-floor claim is made. Only uncovered bounded
legs are repeated, per [Xylon's provider ruling](https://github.com/hadron-memory/hadron-cli/issues/1678#issuecomment-6651740508).

## Verification

The first commit contains command-level tests that fail on main because the
command is absent. Loopback tests capture all requests and pin defaults,
omission, optional inputs, timing exclusion, typed outcomes, exact JSON lists
and nulls, refusal mapping, transport failures, old-provider failure, malformed
identity suppression and terminal safety. A valid missing and unreadable result
has identical output. Targeted mutants check behavior rather than compilation.
Compiled stateful-fake QA, independent LOCAL and supported isolated BLIND, and
scoped security review are required at the frozen head. Help inventory and the
coverage matrix are regenerated; the query is recorded as implemented.

Real-provider evidence above belongs to the named server seats. Author loopback
checks and the compiled CLI QA leg cannot prove live authorization or no-write
behavior on an untested provider. docs#653 follows the merged command. Refs #1678
and #2435; both remain open for reconciliation.

The LOCAL fix adds single-violation malformed-result cases, unknown-reason exit7
and identity suppression, every summary/flow text cell, raw JSON preservation,
and refusal message/serverCode pins. The original reason/list mutants survived;
the added pins kill those and the related guard/sanitizer mutants on assertions.
Production code is unchanged. Original independent verdicts remain tied to their
heads and require same-holder delta/applicability decisions at the fix head.
