# Ticker commands (#923)

This adds `hadron ticker create|update|get|list|delete|subscribe|unsubscribe`,
including the reasons set. CLI main had no ticker group; #828 covers attention,
not ticker CRUD. The accepted gate is #923 comment 6258514548 (T2+§10+UX).
The wire comes from server main b033de39798f68b02bb3ef80cb6e3dedeb994716,
including merged #2078; the complete schema refresh also contains previously
landed AppDeliverySettings declarations. No settings command is added here.

## Boundaries and behavior

`internal/cmd/ticker` owns explicit DTOs, input validation, text/JSON and
confirmation. `internal/api/queries/tickers.graphql` owns typed operations and
fragments, regenerated with genqlient. Root registers the group; unbound-ops
removes the seven operations now called and records two newly snapshotted
delivery-settings operations under their existing C5 owner (#911). The existing Factory supplies
credentials and worktree attribution; no new session or credential store.

Create/list use explicit/configured App context. Human output names the scope
and source, best-effort resolving the App identity; JSON stays object/array.
Get and mutations take IDs or fully-qualified URNs, never ticker names. The
owner is server-derived, and there is no owner flag. Delete uses the shared
irreversible confirmation gate; recreation yields a new slug/URN.

Create/update omit every unset optional input. Explicit false and clear fields
are pointers with omitempty, so they survive marshaling. Set and clear flags
are mutually exclusive. Reasons accept case/underscore normalization like the
server, reject empty/unknown entries, deduplicate and send canonical
READ_TEAM_CHANNEL then EVERY_TICK order. Omitted create uses the shipped
server default; omitted update preserves. Both reasons use any-of. Authorization,
cadence, cap, quiet-hour validity, widening consent and subscription eligibility
remain server-owned. The CLI never retries uncertain mutations or narrows a
refused edit automatically.

Ticker DTOs preserve nullable URN/owner/timestamps and reader-visible channels,
explicitly called readableChannels. Get/list page subscribers to exhaustion,
including empty pages with continuation, and reject missing/repeated cursors.
The server ticker list itself is unpaged; help makes no exhaustive-list claim.
Null ticker is one not-found result for missing/unreadable alike. Subscribers
and channels retain actionable IDs, not just display labels. Mutation responses
report the returned ticker; get/list include subscriber detail.

Subscribe/unsubscribe retain nullable announcement. Null is normal. POSTED
reports success; FAILED prints a warning that the change stands and says not
to rerun for the announcement. JSON remains a single subscription result. No
retry, secondary post or compensating unsubscribe is issued.

The server currently builds Proposed defaults, management/read audiences,
anchor/relevance/enabled/quiet-hours shape rules, and self-unsubscribe behavior.
Help and agentic usage disclose current server behavior rather than promoting
these to settled law. Vera receipts 6258404366 and 6258477948 govern the scope.
No new platform spec is needed: this exposes existing operations without
creating a cross-repository rule.

Actual DueWake/TICK transport, proof acknowledgement, terminal/poller behavior,
read/tick marks and C1's chat-range guard are outside this change. Joshua owns
existing #906 row 1. #927 was a duplicate and is closed. No S1 dependency for
this command group, and no production ticker activation.

## Validation and remaining gates

Red-first commit 8131566 fails because the ticker group is absent. Command tests
exercise captured omitted/present variables, explicit false/clear, canonical
reasons, malformed inputs, all subscription announcement outcomes, null get,
subscriber pagination/stuck cursors, delete confirmation and refusal/no retry.
Error-mapping tests cover new ticker/subscribe refusals. Existing global tests
also enforce list/ls naming, required help flags and agentic usage completeness.

`python3 scripts/check-ticker-cli.py /absolute/path/to/hadron` runs 26 offline
compiled-command cases: all seven text/JSON paths, help, validation and
confirmation refusals, FAILED announcements and a widening refusal with one
mutation. It uses an isolated temp directory, synthetic credential and loopback
fake GraphQL, not the user's configuration or worker binding. This is author
acceptance evidence, not proof of real server atomicity or independent QA.

T2 cross-family BLIND, separate LOCAL, independent compiled CLI QA, UX and §10
are required on the frozen head. Real actor/holder/manager/subscription authority,
persisted reasons and atomic widening refusal need the coordinator-authorized
one-stack disposable CLI/server QA leg, recording SHA/time/teardown. No live
workers, production mutation, router restart or release is authorized. Passing
unit tests or offline acceptance does not waive those gates.

## Author review checklist

Walked the applicable children of Hadron `hadron-cli:review` before PR opening.
This is an author check, not the required independent review/QA/UX receipts.

| Review node | Result |
| --- | --- |
| omit-unset-optional-args | Pass: generated omitempty plus captured key absence; clear/false retained. |
| regenerate-after-graphql-edit | Pass: pinned-main schema and regenerated operations. |
| stable-json-dto | Pass: package DTOs and raw empty-array checks; nil channels/subscribers mutant fails. |
| canonical-ref-handling | Skipped: no memory/node resolution or upsert; ticker/worker validation uses urn-lib through cmdutil. |
| async-wait-command-contract | Skipped: no async polling. |
| entity-fields-not-display-labels | Pass: owner/worker/channel identities retain IDs and nullable URNs, not labels alone. |
| help-text-matches-changed-behaviour | Skipped: new command group; new help and agent contract reviewed directly. |
| no-rmw-sugar-over-wholesale-writes | Skipped: reasons/channels replace explicitly; no partial set-edit sugar. |
| map-new-server-error-codes | Pass: consent/subscription permission, input and state codes deliberately mapped/tested. |
| confirm-prompt-tells-the-truth | Pass: shared irreversible gate; no restore promise or known-doomed pre-read. |
| no-hand-parsed-ref-labels | Skipped: labels come from responses; no ref-derived display names. |
| ambient-scope-must-report-its-source | Pass: human create/list header, flag/context distinction and unreadable decoration fallback; JSON unchanged. |
| needs-a-spec-when-it-decides-a-rule | Pass: no new cross-repo rule, existing wire exposed; Vera receipts cited above. |
| assert-the-query-not-the-capture | Skipped: no test infers selected response fields from request variables. |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no server rule narrowed. |
| a-claim-must-not-outrun-its-evidence | Pass: no marks/all-clear; list limitation disclosed, subscriber cursors exhausted. |
| sweep-by-identifier-not-by-prose | Pass: removed only seven now-bound entries; unrelated unbound operations remain. |
| backticks-in-flag-usage-become-the-placeholder | Pass: rendered help inspected; no backtick placeholders. |
| separate-streams-cannot-test-order | Skipped: no cross-stream ordering claim. |
| a-mutation-check-can-itself-be-a-no-op | Pass: nil-array mutant changes actual initializer and fails raw JSON assertion. |
| a-tidy-argument-is-not-a-checked-one | Skipped: no ambiguous value treated as certain. |
| mutate-with-the-original-input | Skipped: new feature, no reported defect fix. |
| a-new-meaning-for-an-existing-glyph | Skipped: no existing display glyph redefined. |
| enumerate-the-paths-before-promising-a-guarantee | Pass: seven commands enumerated; mutation/no-retry and output-failure paths tested; no live authority guarantee. |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: actual command entrypoints, multipage calls and race checks; compiled acceptance uses fake HTTP. |
| assert-the-value-the-user-gets | Pass: raw JSON, rendered warnings, production exit classifier and compiled exits. |
| an-empty-flag-is-not-an-absent-flag | Pass: Changed semantics and zero-request invalid-input checks with valid App context. |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no security exemption/quote filtering. |
| sweep-the-subject-not-the-removed-rule | Skipped: no existing rule replaced. |
| a-gate-can-outlive-its-premise | Skipped: no retired upstream proxy gate. |
| improving-an-answer-widens-a-domain | Skipped: new DTO; no existing nullable contract strengthened. |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: subscriber continuation uses hasNextPage, not array emptiness. |
| read-the-verdict-body-not-only-the-inline-comments | Skipped at author review: independent/bot verdicts not yet received. |
| gate-on-the-condition-not-the-flag | Pass: explicit boolean values forwarded; confirmation and validation reflect actual operation. |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host contract matrix edited. |
| a-pointer-must-be-followed-not-reasoned-about | Pass: documented command/help paths exercised; FAILED announcement remedy does not suggest retry. |
| a-recommended-field-must-be-seen-carrying-a-value | Pass for command mapping: optional false/clear, reasons, channels and quiet object exercised on fake wire; real persistence still QA-held. |
| classify-security-input-by-structure | Skipped: no security request classification. |
| old-server-graphql-document-compatibility | Skipped: entirely new command group; no existing command's older-server path altered. |
