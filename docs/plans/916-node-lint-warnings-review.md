# C3c author checklist

Author self-check; independent seats still required.

| Review node | Result |
|---|---|
| omit-unset-optional-args | Pass: standalone read cursor only; native mutation variables unchanged. Flag off/explicit=false adds no operation or DTO field |
| regenerate-after-graphql-edit | Pass: standalone node-lint document generated; C1/C7 merged and generated union refreshed, existing documents not inflated |
| stable-json-dto | Pass: explicit page/item DTOs; nil outer pointer omits unrequested field, requested nil page is null, complete items initialized[] |
| canonical-ref-handling | Pass: existing selector resolution unchanged; current lint uses resolved/returned node ID, not a guessed input loc/URN |
| async-wait-command-contract | Skipped: bounded cursor pagination, no wait/poll command |
| entity-fields-not-display-labels | Pass: server nullable warning scalars retained without entity/target enrichment; human kind/text SafeCell |
| help-text-matches-changed-behaviour | Pass: explicit opt-in, current advisory/non-snapshot semantics and actual incompatible modes documented; no default extra read |
| no-rmw-sugar-over-wholesale-writes | Skipped: no read/modify/write list |
| map-new-server-error-codes | Pass: failed native write preserves api.MapError before lint; failed advisory query becomes null and does not retry/change successful mutation |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt |
| no-hand-parsed-ref-labels | Skipped: no parsed display labels |
| ambient-scope-must-report-its-source | Pass: existing command target/source behavior unchanged; new read uses the resulting explicit node identity |
| needs-a-spec-when-it-decides-a-rule | Skipped: consumes merged node-lint contract; no new warning policy or law |
| assert-the-query-not-the-capture | Pass: exact returned node ref and second-page cursor captured; ordinary mutation/read documents remain free of new fields |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no narrowed counting rule |
| a-claim-must-not-outrun-its-evidence | Pass: incomplete/failed/changed/stalled warning collection is unavailable, not clean/partial; no authored-revision snapshot claim |
| sweep-by-identifier-not-by-prose | Skipped: no removed/renamed surface |
| backticks-in-flag-usage-become-the-placeholder | Pass: no backticks in new flag usage, rendered help reviewed |
| separate-streams-cannot-test-order | Skipped: no cross-stream order assertion |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no source mutants claimed by author |
| a-tidy-argument-is-not-a-checked-one | Skipped: no ambiguous state treated as certain |
| mutate-with-the-original-input | Skipped: new command, not a reported defect fix |
| a-new-meaning-for-an-existing-glyph | Skipped: no placeholder glyphs |
| enumerate-the-paths-before-promising-a-guarantee | Pass: flagoff/false, invalid format, success/refusal, null/error/invalid page, cursor/total/nonprogress/cap, writer errors and each rendering mode reviewed |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: separate first/second pages and late errors, one mutation plus all reads counted, actual request cursors recorded; focused race run |
| assert-the-value-the-user-gets | Pass: JSON nullable/clean pages, exact server text/nullables, counts/cursors, approval revision and safe human text asserted |
| an-empty-flag-is-not-an-absent-flag | Pass: bool value controls the read, including explicit --lint-warnings=false; no value/presence confusion |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no scanning exemption |
| sweep-the-subject-not-the-removed-rule | Skipped: no removed rule |
| a-gate-can-outlive-its-premise | Skipped: no proxy lifecycle gate |
| improving-an-answer-widens-a-domain | Pass: nullable source-warning fields stay nullable, no existence/current-target diagnosis added |
| an-empty-result-as-a-signal-must-be-unforgeable | Pass: null field/items, failures, changed totals and repeated/empty cursors never become a clean page; empty[] requires valid complete total0 |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review bot |
| gate-on-the-condition-not-the-flag | Pass: body-only/dry-run incompatibility checks actual mode values; opt-in controlled by bool value; server owns admission |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host matrix |
| a-pointer-must-be-followed-not-reasoned-about | Pass: source lint queried directly from provider; no target lookup or guessed warnings from current text |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no fix predicated on unused field |
| classify-security-input-by-structure | Skipped: no encoded-body security scanning |
| old-server-graphql-document-compatibility | Pass: old documents unchanged; new field queried only on opt-in and older-provider refusal becomes unavailable, never empty |
| classify-by-provenance-before-wrapping | Skipped: no existing render SafeCell sweep |
