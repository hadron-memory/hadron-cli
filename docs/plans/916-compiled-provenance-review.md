# C1b author checklist

Author self-check; independent seats still required.

| Review node | Result |
|---|---|
| omit-unset-optional-args | Skipped: read pagination variables deliberately required; default20/offset0 and explicit limit0 always sent; no write omission change |
| regenerate-after-graphql-edit | Pass: selected merged27455a482 export, generated query and inventories; incidental comments/new types disclosed |
| stable-json-dto | Pass: explicit nested DTOs, items initialized[], nullable source status tested in raw JSON |
| canonical-ref-handling | Pass: BatchNodeRef normalizes without live resolution, preserving exact-revision provider gate |
| async-wait-command-contract | Skipped: no polling or wait |
| entity-fields-not-display-labels | Pass: released recorded IDs/hashes only, actionable opaque ids; contract explicitly prohibits related entity expansion |
| help-text-matches-changed-behaviour | Skipped: new command, existing behavior untouched; new help/inventory and agent contract updated |
| no-rmw-sugar-over-wholesale-writes | Skipped: no read/modify/write list |
| map-new-server-error-codes | Pass: null result explicitly4, provider errors MapError, local pagination inconsistency1/5 documented/tested; no invented provider code |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt |
| no-hand-parsed-ref-labels | Skipped: no parsed display labels |
| ambient-scope-must-report-its-source | Skipped: explicit ref and explicit optional memory only, no ambient App fallback |
| needs-a-spec-when-it-decides-a-rule | Skipped: existing merged server/P10 contract; no new platform rule |
| assert-the-query-not-the-capture | Pass: captured actual query excludes body/live-edge fields and requires one provenance root operation |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no narrowed counting rule |
| a-claim-must-not-outrun-its-evidence | Pass: all-page output buffered, late errors/change/nonprogress fail without partial record; pages are not claimed one snapshot |
| sweep-by-identifier-not-by-prose | Skipped: no removed/renamed surface |
| backticks-in-flag-usage-become-the-placeholder | Pass: no backticks in new flag usage, rendered help reviewed |
| separate-streams-cannot-test-order | Skipped: no cross-stream order assertion |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no source mutants claimed by author |
| a-tidy-argument-is-not-a-checked-one | Skipped: no ambiguous state treated as certain |
| mutate-with-the-original-input | Skipped: new command, not a reported defect fix |
| a-new-meaning-for-an-existing-glyph | Skipped: no placeholder glyphs |
| enumerate-the-paths-before-promising-a-guarantee | Pass: invalid input, ref parse, credentials, provider errors/null, invalid occurrence, page identity/order/count and writer failure paths reviewed; no mutation exists |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: each offset served separately, exhaustion fails loudly, late error/null/stall controls, focused race execution |
| assert-the-value-the-user-gets | Pass: final exitCodeFor and serialized DTO, including three source-pruned states and raw empty array |
| an-empty-flag-is-not-an-absent-flag | Skipped: only bool flag, no empty string option |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no scanning exemption |
| sweep-the-subject-not-the-removed-rule | Skipped: no removed rule |
| a-gate-can-outlive-its-premise | Skipped: no proxy lifecycle gate |
| improving-an-answer-widens-a-domain | Skipped: no existing nullable output made definite |
| an-empty-result-as-a-signal-must-be-unforgeable | Pass: nil provider/items refuse; all-page empty progress/incorrect total and duplicate order fail |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review bot |
| gate-on-the-condition-not-the-flag | Skipped: flag changes one pointer variable, no authority gate |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host matrix |
| a-pointer-must-be-followed-not-reasoned-about | Pass: actual command/examples and flags exercised by focused fixtures/help; no guessed portal/source link |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no fix predicated on unused field |
| classify-security-input-by-structure | Skipped: no encoded-body security scanning |
| old-server-graphql-document-compatibility | Skipped: new-only query, old operation documents unchanged; no old-provider fallback claimed |
| classify-by-provenance-before-wrapping | Skipped: no existing render SafeCell sweep |
