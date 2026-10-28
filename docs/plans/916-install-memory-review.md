# C7 author checklist

Author self-check; independent seats still required.

| Review node | Result |
|---|---|
| omit-unset-optional-args | Pass: install omitted on legacy paths; native install create omits memoryId; full input regressions pass |
| regenerate-after-graphql-edit | Pass: generated InstallMemory operation and memoryId omitempty; ordinary operation documents unchanged |
| stable-json-dto | Pass: explicit memory DTO or null, existing node output unchanged |
| canonical-ref-handling | Pass: qualified App refs use CanonicalAppRef, agent parser validates full refs, node paths reuse ResolveNodeRef on returned memory URN; no hand-composed node URN or upsert |
| async-wait-command-contract | Skipped: no polling or wait |
| entity-fields-not-display-labels | Pass: nested memory record carries actionable id/URN/name, not a label |
| help-text-matches-changed-behaviour | Pass: four node helps, customize help, agentic surface and inventory updated |
| no-rmw-sugar-over-wholesale-writes | Skipped: no read/modify/write list |
| map-new-server-error-codes | Pass: INSTALL_TARGET_CHANGED mapped and API-tested; native write refusal command pins |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt |
| no-hand-parsed-ref-labels | Skipped: no parsed display labels |
| ambient-scope-must-report-its-source | Skipped: both install references are required explicit arguments, no ambient context |
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
| enumerate-the-paths-before-promising-a-guarantee | Pass: validation, metadata lookup, null short circuit, node lookup/write and transport paths separated; only server transaction creates memory |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: all read/write calls recorded, unknown operations fail; success and null/refusal paths pinned |
| assert-the-value-the-user-gets | Pass: command tests use final exitCodeFor and serialized native inputs |
| an-empty-flag-is-not-an-absent-flag | Pass: Changed gates install, explicit empty and explicit empty --memory combination refuse before requests |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no scanning exemption |
| sweep-the-subject-not-the-removed-rule | Skipped: no removed rule |
| a-gate-can-outlive-its-premise | Skipped: no proxy lifecycle gate |
| improving-an-answer-widens-a-domain | Skipped: no existing nullable output made definite |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: no paginated empty-result termination |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review bot |
| gate-on-the-condition-not-the-flag | Skipped: flag changes one pointer variable, no authority gate |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host matrix |
| a-pointer-must-be-followed-not-reasoned-about | Pass: customize/help and add/get/list/update fixture paths exercised |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no fix predicated on unused field |
| classify-security-input-by-structure | Skipped: no encoded-body security scanning |
| old-server-graphql-document-compatibility | Pass: ordinary create/update documents unchanged; new query only explicit install path; nonempty memory selector preserved |
| classify-by-provenance-before-wrapping | Skipped: no existing render SafeCell sweep |
