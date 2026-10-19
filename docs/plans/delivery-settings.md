# Delivery settings (CLI #911 slice A)

Dispatched by Ada #22886; CHECKED T2+§10+UX6259474601, Victor FIT6259424645.
Refs #911: the umbrella stays open. Stop signals, isolation, S7 standby behavior
and Agent templates are held/outside this change.

## Contract

`hadron team delivery-settings get|set` uses the existing explicit App/config/
worktree fallback. Human output names that source and server-derived App label;
JSON is an explicit five-field DTO: batchWindowSeconds, busyDelaySeconds,
stuckWindowSeconds, standbyEnabled, standbyEffective. No generated structs are
marshalled directly. No arrays, secrets, nested actor or raw request input.

Set exposes --batch-window-seconds, --busy-delay-seconds,
--stuck-window-seconds and --standby=true|false. Whole seconds fit GraphQL Int;
passed empty/null/default/fractional/overflow values refuse before requests.
Cobra Changed plus generated pointers/omitempty preserve omission and false.
No reset/null path or standbyEffective input. Empty patch sends {} and prints
“nothing changed (unchanged readback),” never a claimed modification.

Batch window IS ticker cadence floor, one dial; help states this coupling next
to the flag: existing tickers keep their cadence and a raise above a live
ticker is refused. Decided minimum 60 and defaults120/600/900 are stated. Busy/stuck
ranges, joint relation and manager write rights are described as current server
behavior under Proposed corpus text, not new law. Server owns joint/live-ticker/
self-cadence validation. Standby read fields are returned values; no activation
claim. Explicit true currently returns DELIVERY_STANDBY_UNAVAILABLE.

Ordinary authenticated caller, like existing App settings commands: no bound
worker-session header, automatic binding, alternate credential or person fallback
after denial. A server refusal is final. Do not infer permission from App scope.

## Wire and refusal map

Typed TeamDeliverySettings / TeamDeliverySettingsSet operations use a shared
AppDeliverySettingsFields fragment and optional generated input. Exported SDL
from detached server main c80826e76345f3e3183cc798767290e1350b6dde;
SDL is byte-identical to the committed snapshot, so no schema diff is required.
Only these two operations disappear from the unbound baseline.

- DELIVERY_SETTINGS_OUT_OF_RANGE, DELIVERY_SETTINGS_CONFLICT and
  TICKER_SETTINGS_OUT_OF_RANGE: Usage2 (fix the candidate input).
- TICKER_SETTINGS_CONFLICT and DELIVERY_STANDBY_UNAVAILABLE: Conflict5
  (live ticker/state or unavailable behavior must change).
- FORBIDDEN8, APP_NOT_FOUND4, BAD_USER_INPUT2: existing mappings.
- Existing SESSION_NOT_LIVE behavior remains generic1; no unrelated mapping change.
- Transport uncertain/unavailable7: existing mapping. No retry, success DTO or
  follow-up mutation. Text error includes the normal `hadron:` prefix; the server
  message itself is preserved. JSON uses the existing error envelope.

## Evidence and limits

Red-first commit1453791 pins absence of the group on fresh CLI main6ba05b8.
Command tests capture actual JSON variables (presence, false, {}), output DTOs,
help/coupling, no-mutation integer refusals and exact refusal/no-retry behavior.
`python3 scripts/check-delivery-settings-cli.py bin/hadron` exercises the compiled
entry point: 59 cases, both text/JSON, exact message and exit, get/set and empty
patch, authority/concealment/session/range/relation/standby synthetic refusals,
transport503, missing result and help. Isolated config, synthetic bearer and
loopback server only. This is NOT real-server authority/persistence or independent
QA; Ada allocates the one independent compiled-CLI stack/seat. No author stack,
production settings control, release, activation, stop or host action.

## Author checklist

| Review node | Result |
|---|---|
| omit-unset-optional-args | Pass: pointer tags and captured map presence, false and empty patch |
| regenerate-after-graphql-edit | Pass: typed generation; final freshness check |
| stable-json-dto | Pass: five explicit scalar fields, output.Write; JSON compiled checks |
| canonical-ref-handling | Skipped: no memory/node ref or upsert; existing App scope helper |
| async-wait-command-contract | Skipped: no wait/poll |
| entity-fields-not-display-labels | Skipped: no entity-valued JSON field |
| help-text-matches-changed-behaviour | Skipped: pure new surface; new help independently checked |
| no-rmw-sugar-over-wholesale-writes | Skipped: server atomic scalar patch |
| map-new-server-error-codes | Pass: five explicit mappings, API and command/compiled tests |
| confirm-prompt-tells-the-truth | Skipped: no deletion/confirmation |
| no-hand-parsed-ref-labels | Pass: existing server-read lazyAppLabel |
| ambient-scope-must-report-its-source | Pass: unconditional scope line, JSON skips identity decoration |
| needs-a-spec-when-it-decides-a-rule | Skipped: existing server contract + Victor FIT; no new law |
| assert-the-query-not-the-capture | Pass: compiled server checks output fields in actual query |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no narrowed eligibility rule |
| a-claim-must-not-outrun-its-evidence | Skipped: no watermark or all-clear |
| sweep-by-identifier-not-by-prose | Skipped: no removed/renamed surface |
| backticks-in-flag-usage-become-the-placeholder | Pass: no backticks; rendered help checked |
| separate-streams-cannot-test-order | Skipped: no cross-stream ordering assertion |
| a-mutation-check-can-itself-be-a-no-op | Pass: asserted production substitution applied; JSON and text tests fail; restored source passes |
| a-tidy-argument-is-not-a-checked-one | Skipped: no ambiguity promoted to certainty |
| mutate-with-the-original-input | Skipped: addition, not incident-specific fix |
| a-new-meaning-for-an-existing-glyph | Skipped: no unknown glyph |
| enumerate-the-paths-before-promising-a-guarantee | Skipped: no widened universal behavior claim |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: captured requests, loud unexpected operation, repeat-count checks and focused race |
| assert-the-value-the-user-gets | Pass: compiled exit/stdout/stderr/JSON checks |
| an-empty-flag-is-not-an-absent-flag | Pass: Changed before request, explicit empty refuses |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no exemptions |
| sweep-the-subject-not-the-removed-rule | Skipped: no replaced rule |
| a-gate-can-outlive-its-premise | Skipped: no retired-mechanism gate |
| improving-an-answer-widens-a-domain | Skipped: no existing answer made more certain |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: null result errors, not completion |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review bot (sprint2 override) |
| gate-on-the-condition-not-the-flag | Skipped: flag presence IS the patch contract |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no cross-host contract matrix edit |
| a-pointer-must-be-followed-not-reasoned-about | Pass: new command/help runs; no invented endpoint |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no proposed workaround built on an unobserved field |
| classify-security-input-by-structure | Skipped: no security body scanner |
| old-server-graphql-document-compatibility | Skipped: new S9 commands require landed S9; no older-server fallback promised |

## Review delta (Uma/Joran)

Red ad36573 reproduces the false-retiming help claim and adds a distinct
stored-on/effective-off projection fixture. The DTO-substitution mutant
(effective := stored) then fails both get/set JSON and exact text rows; restored
production passes. The fixture is a synthetic projection, not evidence that
this CLI enabled standby on the current server. Help now states unchanged
existing ticker cadence and the named-ticker refusal on floor raise. Text-only
updates add explicit updated/unchanged receipts and readable standby labels;
JSON keys, input/transport/auth and error mappings are unchanged. User help
removes S7/GraphQL terminology. P3 string flag placeholders are deferred; empty
set remains the gate's explicitly chosen manager-write readback semantics.
