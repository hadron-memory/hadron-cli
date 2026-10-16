# Worker onboarding (#837)

`hadron team worker onboard` composes existing host operations for an already
cast, held worker. Orca-hosted workers only. Holger's A ruling and the current
issue body govern; Xylon's CHECKED T2 + §10 gate is
https://github.com/hadron-memory/hadron-cli/issues/837#issuecomment-6207823859.
The red commit is `6f6c7bf`, based on main `5e1b6b2`: the command-help pin failed
because the command did not exist.

## Boundaries and prerequisites

The server still owns authority under `cor:agt:070:05`. Only `worktree.create`
and `worker.start` are submitted. There is no new operation, free-form command,
host registration write, cast, session bind, or Channel register write. The
command never directly creates an Orca worktree or terminal. The worker binds
its own launch grant. No attention token or delivery ledger is opened by onboard.

Operators first configure the host poller's repo mapping, worker family and
foreground launch argv, register that host/worker, and add the worker's
mention-only Channel register row. Eden confirmed those prerequisites are out
of scope in team chat #13284. Starting requires App-manager authority;
`--as-person` uses the operator instead of the bound worker. Worktree root must
match the target host's configuration. The Go router loop must be upgraded to
this revision and use the supplied config; the TS loop cannot acknowledge reload.

The repo token, explicit branch, base branch and label are queue name tokens,
not shell commands or paths. A worktree name is `<repo basename>/<label>`, exactly
as the existing host poller computes it. Host ID and root are operator-selected;
this command cannot infer a target host's private filesystem configuration.

## Execution and evidence

1. Validate flags and router config. Run `orca status --json` with a 10-second
   deadline; require a successful envelope and a reachable runtime. A missing CLI,
   failed envelope or unproven runtime refuses with exit 2 before any server call.
2. Resolve the existing worker inside the explicitly supplied router config's
   App. Verify the canonical App ID, held/non-retired worker, live host/family
   registration and winning worker-level mention-only Channel row.
3. Default dry-run prints the two exact operations and human remainder. No queue
   submission, configuration write, lock creation or Orca metadata write occurs.
4. Apply holds a config-specific onboarding lock across the workflow. It scans
   every host-operation history page before submitting, reuses an identical
   operation, and refuses conflicting active operations. The newest identical
   operation wins (server ordering: issuedAt/id descending). Failed operations
   are reported instead of reissued. The workflow is serialized for this canonical
   config path; the queue has no cross-client request idempotency key. Operators
   must not provision one worker concurrently using distinct configs/clients.
5. Await create, verify its exact returned worktree token, and read Orca's
   worktree path/branch before issuing start. Await start, then require a current
   live worker session and an agent terminal in that Orca worktree. These reads
   never launch anything. A stale succeeded start with no current session refuses.
6. Perform only three host-local steps: upsert the roster entry under the existing
   router writer lock, await exact-config reload evidence, and set/read back the
   Orca display name. The config update preserves unknown top-level and row fields,
   pinned terminals and lifecycle flags, and refuses worker/worktree reassignment.
   It validates the candidate before atomically replacing the config (mode 0600).
7. The human tail asks for the worker's chat pickup and mention delivery check.

Queue waits use immediate-first reads, a reused ticker and deadline contexts,
including in-flight requests. `--timeout` is 1s–30m per operation/reload. Each
Orca invocation is separately bounded by the existing runner (300s).
After preflight, every failure prints the partial outcome before returning
nonzero: known queue IDs/statuses, completed verifications and error. An ambiguous
submission may have applied; inspect `team host ops` before retrying. A timeout
does not cancel queued work or roll back completed local steps. A crashed process
can leave the onboarding lock; an operator verifies no run remains before removal.

## Router reload

The Go loop now loads validated config between complete polls. It never interrupts
an in-flight nudge, and stops on invalid new config. A separate `config-loaded.json`
contains the exact loaded-byte SHA-256 fingerprint and UTC time. Onboard waits for
the fingerprint of its saved roster and a receipt no older than its update. The
receipt proves config loading, not a successful attention poll or mention delivery.
The adopted token is neither reset nor copied. The loop's ordinary polling may
continue updating attention state under its existing rules.

This is a host-local implementation of the existing issue/spec contract. No new
platform rule or citation is proposed; CLI flags, config receipts and retry UX
do not cross the server contract boundary.

## Validation

Command tests use an HTTP fake queue and executable Orca fixture that rejects
every verb except status, worktree show/set and terminal list. They check exact
typed arguments, two-operation apply, retry reuse, default no-submit dry-run,
pre-queue exit-2 refusal, mention-only prerequisites and partial failure JSON.
Pure queue tests cover history pagination, failed/pending states, unexpected
create result and bounded waits. Roster tests cover idempotence, conflict refusal,
unknown-field/pin preservation, reload receipt and unchanged token bytes.

`internal/cmd/team/testdata/onboard-host-contract.json` records the real argument
shapes read from server main `b2916e71fb397ca7cbfbc6ed7efa218f428942bc`.
`python3 scripts/check-onboard-host-contract.py --server-repo <checkout> --server-ref <revision>`
reads immutable git objects at the explicitly chosen revision, prints its SHA,
and refuses shape drift or an unparseable source. The existing nightly/on-demand
schema-drift workflow runs it against the fetched server checkout; normal Go
tests pin the command plan against that fixture without requiring private source.

Author validation uses full `make test`, `make lint`, `make build`, focused race
tests and the live-source fixture check. Independent real-host/fake-queue QA and
cross-provider blind/local/§10 seats are routed by Eden on the frozen green head;
author tests are not those receipts. No production onboarding was performed.

The built binary was also exercised with a missing Orca executable: exit 2,
clear nothing-queued refusal, unchanged config, and no onboarding lock. A real
router-loop subprocess against an unreachable localhost fixture acknowledged
a changed roster's exact fingerprint on its next iteration while leaving
`adopted.token` byte-for-byte unchanged. This checks reload mechanics without
claiming live attention delivery or production host provisioning.

## Author checklist review

The table below accounts for the current `hadron-cli:review` index.

| Review node | Result |
|---|---|
| `omit-unset-optional-args` | Skipped: No GraphQL document/input-field addition; uses existing typed operation. |
| `regenerate-after-graphql-edit` | Skipped: No schema or GraphQL document edit. |
| `stable-json-dto` | Pass: Explicit command DTO, initialized arrays and raw empty-array assertion. |
| `canonical-ref-handling` | Skipped: No memory/node reference composition or upsert. |
| `async-wait-command-contract` | Pass: Partial record before failure; terminal failure nonzero; immediate deadline/ticker waits. |
| `entity-fields-not-display-labels` | Pass: Explicit actionable app/worker IDs and server-returned App ref. |
| `help-text-matches-changed-behaviour` | Pass: Loop reload documented in help and agent contract. |
| `no-rmw-sugar-over-wholesale-writes` | Skipped: No server-side whole-list replacement; local config serialized separately. |
| `map-new-server-error-codes` | Skipped: Uses existing mapped host-operation errors. |
| `confirm-prompt-tells-the-truth` | Skipped: No confirmation prompt. |
| `no-hand-parsed-ref-labels` | Skipped: Labels come from worker/App reads. |
| `ambient-scope-must-report-its-source` | Skipped: Required config selects App; output names it and --config source. |
| `needs-a-spec-when-it-decides-a-rule` | Pass: Implements existing queue authority; local wrapper/receipt needs no new citation. |
| `assert-the-query-not-the-capture` | Skipped: No field-selection assertion or GraphQL document change. |
| `a-narrowed-rule-turns-expect-none-into-a-tautology` | Skipped: No existing rule is narrowed. |
| `a-claim-must-not-outrun-its-evidence` | Pass: Config fingerprint proves loading only; queue/current session/Orca reads qualify completion. |
| `sweep-by-identifier-not-by-prose` | Skipped: No removed/renamed public surface. |
| `backticks-in-flag-usage-become-the-placeholder` | Pass: No backticks in new flag usage strings; rendered help checked. |
| `separate-streams-cannot-test-order` | Skipped: No cross-stream ordering assertion. |
| `a-mutation-check-can-itself-be-a-no-op` | Skipped: No guard-mutation claim in this change. |
| `a-tidy-argument-is-not-a-checked-one` | Skipped: No uncertain value treated as proof. |
| `mutate-with-the-original-input` | Skipped: New feature, not a reported-defect mutation test. |
| `a-new-meaning-for-an-existing-glyph` | Skipped: No new placeholder glyph. |
| `enumerate-the-paths-before-promising-a-guarantee` | Pass: Error paths after preflight retain partial DTO; capability failures occur before writes. |
| `a-test-double-must-satisfy-the-real-access-pattern` | Pass: Repeat apply and paged reads; unknown fixture verbs fail; focused race tests. |
| `assert-the-value-the-user-gets` | Pass: Root tests use exitCodeFor; executable refusal checked separately. |
| `an-empty-flag-is-not-an-absent-flag` | Skipped: Required empties refuse; optional display empty explicitly uses derived default. |
| `an-exemption-you-cannot-enumerate-is-a-hiding-place` | Skipped: No parser/extractor exemption. |
| `sweep-the-subject-not-the-removed-rule` | Skipped: No rule removed or replaced. |
| `a-gate-can-outlive-its-premise` | Skipped: No retired upstream mechanism or proxy gate. |
| `improving-an-answer-widens-a-domain` | Skipped: No existing nullable JSON field made certain. |
| `an-empty-result-as-a-signal-must-be-unforgeable` | Skipped: Queue history terminates on explicit hasNextPage, not empty items. |
| `read-the-verdict-body-not-only-the-inline-comments` | Skipped: No bot review collection; sprint excludes bots. |
| `gate-on-the-condition-not-the-flag` | Skipped: Orca capability/status gates independent of host naming or --apply. |
| `a-matrix-case-states-only-what-its-contract-decides` | Skipped: No cross-host acceptance matrix edit. |
| `a-pointer-must-be-followed-not-reasoned-about` | Pass: host ops, worker onboard help and router loop help executed; fixture checker run. |
| `a-recommended-field-must-be-seen-carrying-a-value` | Skipped: Existing queue shapes and Orca status/worktree envelopes observed live. |
| `classify-security-input-by-structure` | Skipped: No new transport/security request classifier. |
| `old-server-graphql-document-compatibility` | Skipped: No GraphQL document or new server argument. |
