# Author checklist for #1173

No platform law change: implements the existing no-literal-fallback ruling and dispatched provider removal. No schema snapshot guessed ahead of #2712.

| Review node | Result |
|---|---|
| omit-unset-optional-args | Skipped: no new optional variable or input field; existing signing/description presence controls retained. |
| regenerate-after-graphql-edit | Pass: Queries regenerated; full freshness check passes. |
| stable-json-dto | Pass: Explicit DTOs retained; retired key removals called out in plan/usage and tests. |
| canonical-ref-handling | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| async-wait-command-contract | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| entity-fields-not-display-labels | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| help-text-matches-changed-behaviour | Pass: Agent/App/team help and agentic contract updated; inventory rebuilt. |
| no-rmw-sugar-over-wholesale-writes | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| map-new-server-error-codes | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| confirm-prompt-tells-the-truth | Skipped: no new confirmation path; existing App field-name confirmation retained and tested. |
| no-hand-parsed-ref-labels | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| ambient-scope-must-report-its-source | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| needs-a-spec-when-it-decides-a-rule | Skipped: no new cross-repo rule; existing Holger ruling and specs govern removal. |
| assert-the-query-not-the-capture | Pass: Red9063dbc validates operation documents against schema without fields and args, now green. |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| a-claim-must-not-outrun-its-evidence | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| sweep-by-identifier-not-by-prose | Pass: Removed identifiers swept across source/docs; old plan sections bannered. |
| backticks-in-flag-usage-become-the-placeholder | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| separate-streams-cannot-test-order | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| a-tidy-argument-is-not-a-checked-one | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| mutate-with-the-original-input | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| a-new-meaning-for-an-existing-glyph | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| enumerate-the-paths-before-promising-a-guarantee | Pass: Flag matrix covers create/update/app/cast/worker-update; unknown flags refuse at parse, operation set validates without retired fields. |
| a-test-double-must-satisfy-the-real-access-pattern | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| assert-the-value-the-user-gets | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| an-empty-flag-is-not-an-absent-flag | Skipped: no new empty-sensitive flag; deleted prompt flags refuse, signing empties unchanged. |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| sweep-the-subject-not-the-removed-rule | Pass: Read create/update/cast/role/signing/bootstrap explanations and contract together. |
| a-gate-can-outlive-its-premise | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| improving-an-answer-widens-a-domain | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| gate-on-the-condition-not-the-flag | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| a-pointer-must-be-followed-not-reasoned-about | Pass: Built agent compile, worker compile and node approve help verified; fixed nonexistent agent approve advice. |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| classify-security-input-by-structure | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| old-server-graphql-document-compatibility | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |
| classify-by-provenance-before-wrapping | Skipped: no new ref resolution, polling, auth policy, pagination, coverage marker, translated errors or derived state in this removal. |

Full tests/lint/build/generate/unbound/help freshness PASS in /tmp/joost1173-full3.log. JSON snapshot changes remove only promptOverride; human hashes unchanged. Approval guidance checked with built commands. No live server-removal/serving claim.
