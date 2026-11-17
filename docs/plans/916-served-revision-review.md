# C3b author checklist

Author self-check; independent seats still required.

| Review node | Result |
|---|---|
| omit-unset-optional-args | Pass: page variables are required, default50/offset0 and explicit0 sent exactly; original operation documents unchanged |
| regenerate-after-graphql-edit | Pass: selected merged27455a482 export, generated query and inventories; incidental comments/new types disclosed |
| stable-json-dto | Pass: explicit record/provenance DTOs, nullable envelope and empty items[]; generated values never marshalled as output |
| canonical-ref-handling | Pass: explicit worker IDs/qualified worker refs; run ref sent directly, no ambient App/name/live-node resolution |
| async-wait-command-contract | Skipped: no polling or wait |
| entity-fields-not-display-labels | Pass: released recorded IDs/hashes only, actionable opaque ids; contract explicitly prohibits related entity expansion |
| help-text-matches-changed-behaviour | Pass: new-only commands document nullable record, recorded variables, pruned revisions and independent provenance pages; existing get unchanged |
| no-rmw-sugar-over-wholesale-writes | Skipped: no read/modify/write list |
| map-new-server-error-codes | Pass: api.MapError for provider failures; pagination inconsistency Error1, invalid arguments Usage2; null result successful concealed nullable envelope |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt |
| no-hand-parsed-ref-labels | Skipped: no parsed display labels |
| ambient-scope-must-report-its-source | Skipped: explicit ref and explicit optional memory only, no ambient App fallback |
| needs-a-spec-when-it-decides-a-rule | Skipped: existing merged server/P10 contract; no new platform rule |
| assert-the-query-not-the-capture | Pass: captured actual query excludes body/live-edge fields and requires one provenance root operation |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no narrowed counting rule |
| a-claim-must-not-outrun-its-evidence | Pass: buffered all-pages, no partial payload after error/null/change/stall; no cross-request snapshot or retained-text claim |
| sweep-by-identifier-not-by-prose | Skipped: no removed/renamed surface |
| backticks-in-flag-usage-become-the-placeholder | Pass: no backticks in new flag usage, rendered help reviewed |
| separate-streams-cannot-test-order | Skipped: no cross-stream order assertion |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no source mutants claimed by author |
| a-tidy-argument-is-not-a-checked-one | Skipped: no ambiguous state treated as certain |
| mutate-with-the-original-input | Skipped: new command, not a reported defect fix |
| a-new-meaning-for-an-existing-glyph | Skipped: no placeholder glyphs |
| enumerate-the-paths-before-promising-a-guarantee | Pass: invalid input/credentials/provider error/null/invalid page/identity/count/stall/writer errors reviewed; read-only query has no mutation path |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: each offset served separately, exhaustion fails loudly, late error/null/stall controls, focused race execution |
| assert-the-value-the-user-gets | Pass: final exit codes and JSON record revisions/hash/variables/pruned booleans, null envelope and empty list asserted |
| an-empty-flag-is-not-an-absent-flag | Skipped: only bool flag, no empty string option |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no scanning exemption |
| sweep-the-subject-not-the-removed-rule | Skipped: no removed rule |
| a-gate-can-outlive-its-premise | Skipped: no proxy lifecycle gate |
| improving-an-answer-widens-a-domain | Skipped: provider scalar-only record, no source expansion or existing nullable shape made definite |
| an-empty-result-as-a-signal-must-be-unforgeable | Pass: unavailable nullable record remains ambiguous; null/invalid provenance or stalled all-page read is error, not a forged empty success |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review bot |
| gate-on-the-condition-not-the-flag | Pass: only explicit worker-ref grammar and pagination validation; server remains owner of record authorization |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host matrix |
| a-pointer-must-be-followed-not-reasoned-about | Pass: actual command/examples and flags exercised by focused fixtures/help; no guessed portal/source link |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no fix predicated on unused field |
| classify-security-input-by-structure | Skipped: no encoded-body security scanning |
| old-server-graphql-document-compatibility | Pass: new commands require S10; old shared worker/run documents untouched, no fallback to current prompt on old providers |
| classify-by-provenance-before-wrapping | Skipped: no existing render SafeCell sweep |
