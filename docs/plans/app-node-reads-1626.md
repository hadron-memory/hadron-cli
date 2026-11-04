# App node reads and installed-Agent filtering (cli#1626)

Built against the existing committed SDL snapshot, from CLI main 870d5d98.
Xabat's CHECKED gate 6627906072, refusal clarification 6628251021 and matrix
accounting confirmation in team chat 74280 bound this slice. Parent #63 stays open.

## Behavior

`app nodes <app-ref>` sends one `appNodesPage` read with a required canonical App
reference. It does not reconstruct the server's memory admission. The default
window is 20; values above 200 clamp to 200, negatives to zero; zero is count-only.
Offset is explicit and nonnegative. A continuation advances the requested raw
window, even when post-read authorization pruning leaves fewer displayed rows.
Count-only continuation suggests limit 20 at the same offset. Rows retain server
loc/id order; there is no client sorting or body fetch.

The stable JSON DTO is `{items:[{id,memoryId,loc,name,nodeType}],total,hasMore}`.
`memoryId` is the actionable server ID, not a display label, and disambiguates
same-loc nodes in different memories. Empty items is `[]`. The human table shows
loc/name/type/memory ID; only server-derived cells use SafeCell. Nil pages/items
or incomplete row identities fail without partial success output.

Ten structured node-list flags are forwarded, preserving explicit false booleans.
The App owns memory scope: memory/memory-class are local usage errors. children-of
and where are also local errors, including explicitly empty arguments, with a
node-list remedy and no request. `where` is silently ignored server-side today;
QA Charles independently reproduced it and filed hadron-server#3427. This CLI
change does not implement or repair that server filter. Four remaining filter
capabilities stay gap #63; proposed exclusions retain the #1334/ruling0061 caveat.

`app list --agent <id-or-URN>` forwards AppFilter.agentId directly to the installed
AppAgent join. It composes with either org or owned-by-me, and may stand alone
across readable Apps. The existing bare-array DTO is retained; the exhaustive
listing uses at most 20 rows per request on the Agent path. Unset Agent input is
omitted, not null, including the legacy owner-only path. The static Apps operation
is unchanged; only the input field's serialization directive changes.

## Cost and evidence

App nodes: one GraphQL request per invocation, default at most 20 five-scalar rows,
with neither content nor a scope pre-resolution query. A compiled-binary local
HTTP fixture containing 1,000 logical nodes returned a default page in 1 request,
1,911 response bytes, about 18ms; count-only took 1 request / 73 bytes / 16ms.
Agent-filtered App list: ceil(N/20) reads when pages are full; a 45-App fixture took
3 requests, 1,597 total response-body bytes, 15ms. These timings are local synthetic
author measurements, not production latency promises or independent QA.

A separate read-only live API probe returned total 69,632, hasMore true and an
actual row with all five selected fields. The four new app-nodes help examples
were executed using the compiled binary against the local HTTP fixture. Existing
node-list help confirms the narrowing remedies; app-memories help confirms scope.

Red-only commits 5d100f47 and 20e1ace5 precede production code. Mutation probes
verify each source replacement occurred exactly once and preserve original files
in finally. The intended assertion, not a build error, must fail. An equivalent
default rewrite (10+10) is the surviving control. Detailed local logs are kept in
the author's private scratch; the PR records the final check results and limits.

Platform specs: no new rule is minted. This exposes existing server reads and
implements the gate owner's local-refusal rulings. Independent LOCAL/BLIND and
compiled CLI QA still evaluate the frozen PR head; author probes do not replace them.

## Self-review checklist

Author triage of every review-index child follows. This is not an independent
help-audit assessment; the generated help inventory retains unread for the new page.

| Review node (`review:` prefix) | Result and evidence |
|---|---|
| a-claim-must-not-outrun-its-evidence | N/A — Scope does not match: a command persists a coverage marker (watermark, cursor, sync point) or prints an all-clear derived from a query. |
| a-gate-can-outlive-its-premise | N/A — Scope does not match: an upstream change retires a mechanism a client-side gate reads a PROXY for — an expiry, a reap, a cleanup, a backstop. |
| a-matrix-case-states-only-what-its-contract-decides | N/A — Scope does not match: a diff adds or edits a case in the cross-host acceptance matrix or any contract-cited fixture. |
| ambient-scope-must-report-its-source | N/A — Scope does not match: a command resolves its scope (App, memory, org) through a fallback chain rather than a required argument. |
| a-mutation-check-can-itself-be-a-no-op | PASS — Exact replacement/readback, assertion failures and surviving equivalent control. |
| a-narrowed-rule-turns-expect-none-into-a-tautology | N/A — Scope does not match: a change NARROWS what a rule counts (only enabled, live, owned, in-scope) and tests assert that nothing is found. |
| an-empty-flag-is-not-an-absent-flag | PASS — Changed guards and no-request empty Agent/unsupported-selector pins. |
| an-empty-result-as-a-signal-must-be-unforgeable | N/A — App node termination uses explicit hasMore; App list retains existing total-primary collector. |
| a-new-meaning-for-an-existing-glyph | N/A — Scope does not match: a change renders an UNKNOWN value using a placeholder a neighbouring column already uses for a definite answer. |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | N/A — Scope does not match: a check EXEMPTS something — a self-reference guard, an allowlist, a skip-quoted filter — and going silent would be dangerous. |
| a-pointer-must-be-followed-not-reasoned-about | PASS — Compiled examples and node-list/app-memories help checked. |
| a-recommended-field-must-be-seen-carrying-a-value | PASS — Live page total/hasMore/memoryId observed; no invented count. |
| assert-the-query-not-the-capture | PASS — Actual HTTP request query decoded and root field checked. |
| assert-the-value-the-user-gets | PASS — JSON decoded after output.Write; exitCodeFor classifies command errors. |
| async-wait-command-contract | N/A — Scope does not match: a command polls or waits on server-side async state (--wait/--follow/status polling). |
| a-test-double-must-satisfy-the-real-access-pattern | PASS — Two-page Agent listing drives both offsets; race run required in final checks. |
| a-tidy-argument-is-not-a-checked-one | N/A — Scope does not match: the diff argues in a comment or plan doc that an ambiguous value is nonetheless safe to treat as certain. |
| backticks-in-flag-usage-become-the-placeholder | PASS — Rendered help inspected; no backtick placeholders in new flag usages. |
| canonical-ref-handling | N/A — Scope does not match: a command resolves/composes a memory or node reference, or does an upsert-style write. |
| classify-by-provenance-before-wrapping | PASS — Only server node fields wrapped; fixed hints stay raw. |
| classify-security-input-by-structure | N/A — Scope does not match: an API or transport guard classifies a JSON or GraphQL request by scanning the encoded body for a key or token. |
| confirm-prompt-tells-the-truth | N/A — Scope does not match: adding or changing a command's confirmation prompt (ConfirmDeletion / Confirm). |
| entity-fields-not-display-labels | PASS — memoryId carries a nonempty actionable ID rather than a nullable name. |
| enumerate-the-paths-before-promising-a-guarantee | N/A — New reads, no custody/recovery guarantee. |
| fetch-only-what-the-command-needs | PASS — All five selected node scalars used; one bounded read; request/byte statement above. |
| gate-on-the-condition-not-the-flag | N/A — Each new flag has one filtering/selector meaning. |
| help-text-matches-changed-behaviour | PASS — list help, examples, README and agentic usage updated together. |
| improving-an-answer-widens-a-domain | N/A — Scope does not match: a change makes a --json value MORE certain — retiring a null, a hedge, or an 'unknown' state a client used to have to handle. |
| map-new-server-error-codes | N/A — App reads use existing NOT_FOUND/FORBIDDEN/BAD_USER_INPUT mappings. |
| mutate-with-the-original-input | N/A — New feature, no existing CLI defect input; unsupported selector argv pinned literally. |
| needs-a-spec-when-it-decides-a-rule | N/A — Existing server contract; local CLI interface only, gate rulings recorded. |
| no-hand-parsed-ref-labels | N/A — Scope does not match: a command derives a display label by parsing a user-supplied ref instead of reading it from the server. |
| no-rmw-sugar-over-wholesale-writes | N/A — Scope does not match: a command edits part of a list/set the server replaces whole. |
| old-server-graphql-document-compatibility | N/A — No new argument in existing Apps document; new page operation is new-command-only. |
| omit-unset-optional-args | PASS — Unset filter/agent keys absent; explicit false preserved; omission mutant checked. |
| read-the-verdict-body-not-only-the-inline-comments | N/A — Scope does not match: collecting review feedback from a bot before deciding a PR is clean. |
| regenerate-after-graphql-edit | PASS — make generate; generated output committed and freshness checked before publication. |
| separate-streams-cannot-test-order | N/A — Scope does not match: a test asserts that one line of output comes before or after another in a command that writes to both stdout and stderr. |
| stable-json-dto | PASS — Explicit package DTO, empty [] pin, existing list array unchanged. |
| sweep-by-identifier-not-by-prose | N/A — Scope does not match: a change removes or renames a surface — a flag, a subcommand, a --json key, a GraphQL field. |
| sweep-the-subject-not-the-removed-rule | PASS — App list selector prose swept in help, query comment, agentic usage and README. |

## Final author checks

`make test`, `make lint`, build, focused race and coverage-matrix check pass.
The first full run found a noncanonical `ls` in the new agentic example; corrected
to `list`, with the complete suite rerun green. No unrelated failures waived.

| Mutant | Intended pin | Outcome |
|---|---|---|
| default | TestAppNodes1626DefaultAndCountOnlyAreOneRequest | KILLED |
| zero | TestAppNodes1626DefaultAndCountOnlyAreOneRequest | KILLED |
| cap | TestAppNodes1626DefaultAndCountOnlyAreOneRequest | KILLED |
| raw-offset | TestAppNodes1626EmptyDisplayedPageAdvancesRawWindow | KILLED |
| false | TestAppNodes1626FiltersAndFalseAreForwarded | KILLED |
| memory | TestAppNodes1626MemoryFiltersAreLocalUsageErrors | KILLED |
| where-children | TestAppNodes1626UnsupportedNarrowingRefusesBeforeAnyRequest | KILLED |
| nil-array | TestAppNodes1626DefaultAndCountOnlyAreOneRequest | KILLED |
| agent | TestAppLs1626AgentComposesAndDoesNotResolveTheAgent | KILLED |
| owned | TestAppLs1626AgentComposesAndDoesNotResolveTheAgent | KILLED |
| bounded-list | TestAppLs1626AgentWalksEveryBoundedPage | KILLED |
| equivalent | TestAppNodes1626DefaultAndCountOnlyAreOneRequest | SURVIVED |
| AppFilter.agentId omitempty removed at GraphQL source and regenerated | TestAppLs1626UnsetAgentDoesNotReachTheWire | KILLED (owner-only sends agentId:null) |

## Main merge before freeze

After the first hosted CI passed 4/4, fresh main d217b257 introduced app-user
creation (#1633). Main was merged without rebasing. Conflicts were the generated
help inventory header (regenerated) and the shared app synopsis (retains both
`list --agent` and incoming `user create`). The generated client was regenerated;
coverage preserves both capabilities. app.go keeps both command registrations.
No authored app-nodes/list implementation or test changed in this merge.
