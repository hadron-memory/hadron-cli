# Blank node roles (#903)

`node add/update --role ""` used to refuse because old servers stored empty roles verbatim. Server #1219 / #1994 now normalizes blanks, but sending empty strings would remain unsafe on older servers. The selected compatibility route is to omit a blank create role and send literal `role: null` on update. Omitted updates preserve; nonblank strings are not trimmed. Empty import-role metadata keeps its existing no-op behavior; this PR corrects that comment only.

## Transport and governance

Four dedicated typed operations cover generic/spec/review/task updates. A literal null is inside each document's input; every other UpdateNodeInput field is carried as an omitempty variable in the SAME mutation. No global/shared input tag changes. The helper copies the input only for current ∪ resulting kind routing; the local empty routing marker never reaches the server. Unknown governed kinds continue to receive authoritative server refusal; no new door, fallback or retry is invented.

A clear requires a successful current-kind read (normal field edits retain their prior best-effort routing). Concurrent authority/kind changes are still enforced by the server at its mutation boundary. The helper forwards an existing expectedRevision when supplied; the ordinary node-update command remains last-write-wins and gains no unadvertised revision pre-read. Server conflict/schema/governance refusals produce one failed write without a second save or retry. Dry-run stays read-only and previews the changed role field without claiming server acceptance.

Other field replacements, body and reason are in the clear mutation. Existing --data-merge normally uses a second mutation, so a blank-role clear refuses that combination BEFORE requests; use --data to replace the bag atomically instead. Nonblank role updates keep their prior merge behavior.

## Verification

Red commit `0b5ff0e` pins main refusing blank updates before current-kind writes. Command tests now cover blank/whitespace create/update, omitted roles, governed current doors, dry-run, unreadable current-kind abort, merge guard, conflict/schema refusal with one write and no fallback. API tests enumerate all input fields on each door and preserve false/zero/JSON-null alongside omission. Cross-kind spec→task clearing refuses before a mutation. Generated-operation assertions fail when literal null is removed and codegen re-run; restored documents pass. Full test suite/lint/build and focused race evidence are recorded in the PR.

Platform specs: no new rule; exposes existing cor:dmo:060:02 and established null-clear. Victor FIT 6259025737; Vera owns post-merge Built-note reconciliation. No production or real-node mutation by the author. T2 + §10 and compiled-CLI independent QA are separate gates.

## Author review

General correctness: flag intent → pre-request incompatible-merge guard → node read → current/resulting kind → one typed mutation → mapped refusal/explicit DTO. All dedicated operations preserve unrelated omission; all server refusals stop. Registry nodes considered:

| Review node | Result |
| --- | --- |
| `omit-unset-optional-args` | Pass: Dedicated clear operations use omitempty variables; all 22 other fields and absence are tested on every door. Shared input tags unchanged. |
| `regenerate-after-graphql-edit` | Pass: Generated client rebuilt from committed SDL; no schema refresh needed. |
| `stable-json-dto` | Skipped: result and preview DTOs unchanged; no JSON fields added. |
| `canonical-ref-handling` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `async-wait-command-contract` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `entity-fields-not-display-labels` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `help-text-matches-changed-behaviour` | Pass: Updated add/update help and embedded usage; stale get/import/test comments corrected without changing import wire semantics. |
| `no-rmw-sugar-over-wholesale-writes` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `map-new-server-error-codes` | Skipped: no new server codes; existing governance/conflict/schema mapping pinned at command boundary. |
| `confirm-prompt-tells-the-truth` | Skipped: no confirmation prompt changes. |
| `no-hand-parsed-ref-labels` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `ambient-scope-must-report-its-source` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `needs-a-spec-when-it-decides-a-rule` | Pass: No new law: existing null-clear and server#1219; Victor FIT 6259025737. Vera owns Built-note refresh. |
| `assert-the-query-not-the-capture` | Pass: Actual four generated operation constants assert field-exact literal null. Removing literals and regenerating fails this test; restored. |
| `a-narrowed-rule-turns-expect-none-into-a-tautology` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `a-claim-must-not-outrun-its-evidence` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `sweep-by-identifier-not-by-prose` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `backticks-in-flag-usage-become-the-placeholder` | Pass: No markup backticks; compiled add/update help inspected. |
| `separate-streams-cannot-test-order` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `a-mutation-check-can-itself-be-a-no-op` | Pass: Removed all four role:null document literals, regenerated, saw generated-document test fail; restored and regenerated. |
| `a-tidy-argument-is-not-a-checked-one` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `mutate-with-the-original-input` | Pass: Blank and whitespace CLI updates remain failing cases on red commit; literal-removal mutant proves the new wire assertion. |
| `a-new-meaning-for-an-existing-glyph` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `enumerate-the-paths-before-promising-a-guarantee` | Pass: Single-mutation boundary: clear helper dispatches once, all fields carried, data-merge combination refused before requests, no retry after failure. |
| `a-test-double-must-satisfy-the-real-access-pattern` | Pass: Real typed requests to httptest server; one write after each refusal; focused race verification. |
| `assert-the-value-the-user-gets` | Pass: Command tests use exitCodeFor and emitted preview JSON. |
| `an-empty-flag-is-not-an-absent-flag` | Pass: Changed(role) AND blank selects clear; omission preserves, nonblank stays verbatim. Blank acceptance is the explicit ruled exception to generic empty-ref refusal. |
| `an-exemption-you-cannot-enumerate-is-a-hiding-place` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `sweep-the-subject-not-the-removed-rule` | Pass: Read add/update/get/import, tests, query docs and embedded usage; import no-op intentionally preserved. |
| `a-gate-can-outlive-its-premise` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `improving-an-answer-widens-a-domain` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `an-empty-result-as-a-signal-must-be-unforgeable` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `read-the-verdict-body-not-only-the-inline-comments` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `gate-on-the-condition-not-the-flag` | Pass: Guard checks explicit blank role intent; nonblank role plus data merge keeps existing behavior. |
| `a-matrix-case-states-only-what-its-contract-decides` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `a-pointer-must-be-followed-not-reasoned-about` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `a-recommended-field-must-be-seen-carrying-a-value` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `classify-security-input-by-structure` | Skipped: no matching scope in this role-intent/typed-transport diff. |
| `old-server-graphql-document-compatibility` | Pass: No new server input/field: clear uses existing role:null and existing kind doors; schema refusal never falls back or retries. |
