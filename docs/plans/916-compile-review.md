# C1 author checklist

Author self-check; independent seats still required.

| Review node | Result |
|---|---|
| omit-unset-optional-args | Pass: wire presence pins nil omission and false |
| regenerate-after-graphql-edit | Pass: generated client refreshed; snapshot already contains merged operations |
| stable-json-dto | Pass: five-field command DTO, no generated output or slices |
| canonical-ref-handling | Skipped: no memory/node input resolution; target App uses existing canonicalizer |
| async-wait-command-contract | Skipped: no polling or wait |
| entity-fields-not-display-labels | Pass: compiledNodeRef is the actionable returned URN, not a display label |
| help-text-matches-changed-behaviour | Skipped: new commands; no existing command semantics changed; new help reviewed |
| no-rmw-sugar-over-wholesale-writes | Skipped: no read/modify/write list |
| map-new-server-error-codes | Pass: literal central mapper cases and API plus command tests |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt |
| no-hand-parsed-ref-labels | Skipped: no parsed display labels |
| ambient-scope-must-report-its-source | Pass: worker human output identifies returned worker and App source; URNs are explicit |
| needs-a-spec-when-it-decides-a-rule | Skipped: existing merged server/P10 contract; no new platform rule |
| assert-the-query-not-the-capture | Skipped: tests pin actual operation/variables, no field-exclusion assertion |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no narrowed counting rule |
| a-claim-must-not-outrun-its-evidence | Skipped: no persisted coverage or all-clear |
| sweep-by-identifier-not-by-prose | Skipped: no removed/renamed surface |
| backticks-in-flag-usage-become-the-placeholder | Pass: rendered bool flag has no placeholder |
| separate-streams-cannot-test-order | Skipped: no cross-stream order assertion |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no source mutants claimed by author |
| a-tidy-argument-is-not-a-checked-one | Skipped: no ambiguous state treated as certain |
| mutate-with-the-original-input | Skipped: new command, not a reported defect fix |
| a-new-meaning-for-an-existing-glyph | Skipped: no placeholder glyphs |
| enumerate-the-paths-before-promising-a-guarantee | Pass: pre-submit, mutation error, missing result and output failure paths enumerated |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: one response per request, captures all calls, unexpected operation fails |
| assert-the-value-the-user-gets | Pass: command tests use exitCodeFor and decoded JSON |
| an-empty-flag-is-not-an-absent-flag | Skipped: only bool flag, no empty string option |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no scanning exemption |
| sweep-the-subject-not-the-removed-rule | Skipped: no removed rule |
| a-gate-can-outlive-its-premise | Skipped: no proxy lifecycle gate |
| improving-an-answer-widens-a-domain | Skipped: no existing nullable output made definite |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: no paginated empty-result termination |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review bot |
| gate-on-the-condition-not-the-flag | Skipped: flag changes one pointer variable, no authority gate |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host matrix |
| a-pointer-must-be-followed-not-reasoned-about | Pass: node get approvalStatus/current revision read verified in existing command and tests |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no fix predicated on unused field |
| classify-security-input-by-structure | Skipped: no encoded-body security scanning |
| old-server-graphql-document-compatibility | Skipped: new commands only, existing operations untouched |
| classify-by-provenance-before-wrapping | Skipped: no existing render SafeCell sweep |
