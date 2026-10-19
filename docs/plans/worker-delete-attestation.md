# Worker deletion external-attribution attestation (#899)

Server #1162 / PR #2000 replaced the ever-bound proxy with persisted-reference checks and requires a separate external-history attestation. The CLI now exposes that input through `--confirm-no-external-attribution`; `--yes` alone only confirms deletion. The optional Boolean is true only when the flag value is true, otherwise omitted. The server decides eligibility. Known or uncertain external history means retire; this flag neither crawls external hosts nor proves absence.

Missing attestation is Usage 2, in-use is Conflict 5, concealed/not-found remains 4. Existing authentication mappings and the three-field deletion JSON remain unchanged. The static operation names the new argument even when the variable is omitted: an older server can refuse it as schema skew; there is deliberately no fallback deletion with the argument removed. The gate explicitly accepts that refusal.

Only the worker-rm caller, typed mutation, exact error mapping and embedded help/usage change. No production worker deletion, session mutation or router action is involved. The T2 + §10 review and compiled-CLI independent QA are separate from author verification.

## Verification

Red commit `2bcd94b` failed for the unknown flag, wrong missing-attestation exit and stale help. Tests now pin true/omitted transport, --yes not attesting, stable JSON, error classification and one mutation without fallback on in-use/auth/forbidden/not-found/schema refusals. Full test suite, lint and build pass; focused race run is recorded in the PR.

## Author checklist

The general correctness pass followed worker resolution → deletion consent → optional attestation → one mutation → error mapping → explicit DTO. No client pre-read makes eligibility claims. Registry checklist nodes considered below (the JSON shape and selections are unchanged).

| Review node | Result |
| --- | --- |
| `omit-unset-optional-args` | Pass: Pointer Boolean plus omitempty; absent key and explicit true pinned. |
| `regenerate-after-graphql-edit` | Pass: Generated from committed SDL; no snapshot change needed. |
| `stable-json-dto` | Skipped: output shape unchanged; existing DTO and exact three-field JSON additionally pinned. |
| `canonical-ref-handling` | Skipped: no reference resolution change in this diff. |
| `async-wait-command-contract` | Skipped: no polling in this diff. |
| `entity-fields-not-display-labels` | Skipped: no entity output field change in this diff. |
| `help-text-matches-changed-behaviour` | Pass: Help, prompt, example, query comment and embedded usage updated together. |
| `no-rmw-sugar-over-wholesale-writes` | Skipped: no list replacement in this diff. |
| `map-new-server-error-codes` | Pass: Exact missing-attestation Usage 2; map and command tests. |
| `confirm-prompt-tells-the-truth` | Pass: Hard delete; requester attestation is separate from deletion consent. Server remains authority; absence is forwarded for its refusal as gated. |
| `no-hand-parsed-ref-labels` | Skipped: no reference parsing in this diff. |
| `ambient-scope-must-report-its-source` | Skipped: no scope fallback change in this diff. |
| `needs-a-spec-when-it-decides-a-rule` | Pass: No new law: client flag exposes merged server#1162; Victor FIT 6259025551. |
| `assert-the-query-not-the-capture` | Skipped: no selection/literal claim; tests inspect request variables, not fields selected. |
| `a-narrowed-rule-turns-expect-none-into-a-tautology` | Skipped: no client reference census in this diff. |
| `a-claim-must-not-outrun-its-evidence` | Skipped: no coverage/watermark assertion in this diff. |
| `sweep-by-identifier-not-by-prose` | Skipped: no removed/renamed surface in this diff. |
| `backticks-in-flag-usage-become-the-placeholder` | Pass: Plain usage string; rendered Boolean help has no placeholder. |
| `separate-streams-cannot-test-order` | Skipped: no cross-stream ordering assertion in this diff. |
| `a-mutation-check-can-itself-be-a-no-op` | Skipped: no source-mutant verification claim in this diff. |
| `a-tidy-argument-is-not-a-checked-one` | Skipped: no ambiguous input accepted as certain in this diff. |
| `mutate-with-the-original-input` | Skipped: no reported original-input regression beyond the red flag/refusal cases in this diff. |
| `a-new-meaning-for-an-existing-glyph` | Skipped: no placeholder change in this diff. |
| `enumerate-the-paths-before-promising-a-guarantee` | Skipped: no new broad guarantee; one existing mutation call only in this diff. |
| `a-test-double-must-satisfy-the-real-access-pattern` | Pass: Existing responder driven through real command; one mutation after each refusal, response completion orders captures. |
| `assert-the-value-the-user-gets` | Pass: Command assertions use exitCodeFor and emitted JSON. |
| `an-empty-flag-is-not-an-absent-flag` | Skipped: no empty string-valued flag in this diff. |
| `an-exemption-you-cannot-enumerate-is-a-hiding-place` | Skipped: no new exemption in this diff. |
| `sweep-the-subject-not-the-removed-rule` | Pass: Reviewed worker group/rm/retire help, agentic usage, query comment and migration-plan description. |
| `a-gate-can-outlive-its-premise` | Skipped: no client proxy gate removal in this diff. |
| `improving-an-answer-widens-a-domain` | Skipped: no JSON domain change in this diff. |
| `an-empty-result-as-a-signal-must-be-unforgeable` | Skipped: no empty-result termination in this diff. |
| `read-the-verdict-body-not-only-the-inline-comments` | Skipped: no bot verdict collected yet in this diff. |
| `gate-on-the-condition-not-the-flag` | Skipped: no conditional override beyond the explicit attestation value in this diff. |
| `a-matrix-case-states-only-what-its-contract-decides` | Skipped: no contract matrix edit in this diff. |
| `a-pointer-must-be-followed-not-reasoned-about` | Skipped: no new destination beyond existing worker retire/help in this diff. |
| `a-recommended-field-must-be-seen-carrying-a-value` | Skipped: no unobserved server field dependency in this diff. |
| `classify-security-input-by-structure` | Skipped: no raw security request scanner in this diff. |
| `old-server-graphql-document-compatibility` | Skipped: gate explicitly requires schema refusal, not legacy success or retry. |
