# Intake follow-ups (#1974)

Scope: the issue's CHECKED gate, comments 6847793031 and 6847871250.
Server dependency #4115 was merged on 2026-11-13; no server/schema change here.
The item-by-item decisions live in [the runner plan](1961-intake-adapter-runner.md#follow-up-decisions-cli1974).

No new platform spec: claim starting implements cor:agt:050:02 and the FIT;
environment/JSON/flags are the CLI-owned invocation surface under cor:agt:120:04.
No citation is minted. Item 13's root-wide cancellation migration is declined;
item 7's wholesale legacy shell-fixture rewrite is declined in favor of focused
byte-sensitive fixes and additional native portable runner coverage. These are
explicit scope limits for independent review, not claims those migrations ran.

## Author verification

- Red-first commit b4af2e75: the new regressions fail on fresh main for the reported reasons.
- Focused intake, adapter, reconcile and real compiled-binary interrupt tests pass.
- Race run covers new command cases and native runner; build and golangci-lint pass.
- Native fixture tests execute on Linux; Windows execution is delegated to CI.
- Windows intake test binary cross-compiles; no claim of local Windows execution.
- No production adapter or tracker calls; QA must run the compiled CLI with a fake adapter.

## Repository checklist

This is the author's pass, not either independent review seat. Existing scope
resolution, GraphQL selections, confirmations and pagination are unchanged.

| Review node | Result |
|---|---|
| a-claim-must-not-outrun-its-evidence | Pass: Limit stops cannot claim exhaustion; errors retain partial results and explicit stages. |
| a-gate-can-outlive-its-premise | Skipped: no an upstream change retires a mechanism a client-side gate reads a PROXY for — an expiry, a reap, a cleanup, a backstop added or changed in this diff. |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no a diff adds or edits a case in the cross-host acceptance matrix or any contract-cited fixture added or changed in this diff. |
| ambient-scope-must-report-its-source | Skipped: no a command resolves its scope (App, memory, org added or changed in this diff. |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no a guard's correctness is demonstrated by mutating the code and watching a test go red added or changed in this diff. |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: no a change NARROWS what a rule counts (only enabled, live, owned, in-scope added or changed in this diff. |
| an-empty-flag-is-not-an-absent-flag | Pass: Changed integration with empty/whitespace value refuses before adapter/server calls. |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: no a command treats an EMPTY result as a termination signal — end of history, nothing left, caught up added or changed in this diff. |
| a-new-meaning-for-an-existing-glyph | Skipped: no a change renders an UNKNOWN value using a placeholder a neighbouring column already uses for a definite answer added or changed in this diff. |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: no a check EXEMPTS something — a self-reference guard, an allowlist, a skip-quoted filter — and going silent would be dangerous added or changed in this diff. |
| a-pointer-must-be-followed-not-reasoned-about | Skipped: no a diff adds or edits prose that DIRECTS a reader somewhere — help text, an error message, a doc, a PR description, a comment — naming a command to run, a field to read, or a page to open added or changed in this diff. |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no a fix is built on a schema field, flag or option that nothing in the change has observed carrying a real value added or changed in this diff. |
| assert-the-query-not-the-capture | Skipped: no a test asserts that an operation does or does not request a particular field on the wire added or changed in this diff. |
| assert-the-value-the-user-gets | Pass: Assertions inspect rendered JSON and exitCodeFor; native runner asserts decoded bytes. |
| async-wait-command-contract | Skipped: no a command polls or waits on server-side async state (--wait/--follow/status polling added or changed in this diff. |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: Native executable receives real JSON stdin and call argv; multiple runner calls, caps and refusals covered. |
| a-tidy-argument-is-not-a-checked-one | Skipped: no the diff argues in a comment or plan doc that an ambiguous value is nonetheless safe to treat as certain added or changed in this diff. |
| backticks-in-flag-usage-become-the-placeholder | Skipped: no a cobra flag's usage string is added or edited added or changed in this diff. |
| bind-terminal-evidence-to-target-process | Skipped: no terminal idle or prompt evidence authorizes stopping, resetting or replacing a process added or changed in this diff. |
| canonical-ref-handling | Skipped: no a command resolves/composes a memory or node reference, or does an upsert-style write added or changed in this diff. |
| classify-by-provenance-before-wrapping | Skipped: no a change wraps a render-site string in output.SafeCell, or sweeps detail/table sites into guarded cells added or changed in this diff. |
| classify-security-input-by-structure | Skipped: no an API or transport guard classifies a JSON or GraphQL request by scanning the encoded body for a key or token added or changed in this diff. |
| confirm-prompt-tells-the-truth | Skipped: no adding or changing a command's confirmation prompt (ConfirmDeletion / Confirm added or changed in this diff. |
| entity-fields-not-display-labels | Skipped: no a command's --json gains a field naming another entity (a user, org, app, memory added or changed in this diff. |
| enumerate-the-paths-before-promising-a-guarantee | Pass: One scrubbed Runner.call covers every adapter call; one create reports server state with no follow-up start. |
| fetch-only-what-the-command-needs | Skipped: no a command adds or changes a GraphQL operation or selection, or resolves an entity by name or ref, or reads a list added or changed in this diff. |
| gate-on-the-condition-not-the-flag | Skipped: no a guard, refusal, or forwarded override keys on a FLAG's presence rather than on the fact the flag usually accompanies added or changed in this diff. |
| help-text-matches-changed-behaviour | Pass: Long help, agentic usage, inventory and design-as-built updated together. |
| improving-an-answer-widens-a-domain | Skipped: no a change makes a --json value MORE certain — retiring a null, a hedge, or an 'unknown' state a client used to have to handle added or changed in this diff. |
| map-new-server-error-codes | Skipped: no wiring a server operation whose typed error codes are not already in codeForExtension added or changed in this diff. |
| mutate-with-the-original-input | Pass: Separate first test commit fails for scrub, empty integration, limit completion, redundant start and null name. |
| needs-a-spec-when-it-decides-a-rule | Skipped: no a change decides behaviour a reader in another repo must match, rather than implementing an existing rule or staying inside this repo's edge added or changed in this diff. |
| no-hand-parsed-ref-labels | Skipped: no a command derives a display label by parsing a user-supplied ref instead of reading it from the server added or changed in this diff. |
| no-rmw-sugar-over-wholesale-writes | Skipped: no a command edits part of a list/set the server replaces whole added or changed in this diff. |
| old-server-graphql-document-compatibility | Skipped: no a GraphQL operation adds a server-new argument while an existing command path must still work against older servers added or changed in this diff. |
| omit-unset-optional-args | Skipped: no adding an optional GraphQL variable/flag or refreshing an input with a new optional field added or changed in this diff. |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no collecting review feedback from a bot before deciding a PR is clean added or changed in this diff. |
| recovery-must-not-block-the-host-loop | Skipped: no a recurring registration, collection, delivery or heartbeat loop gains waits, RPCs or recovery jobs added or changed in this diff. |
| regenerate-after-graphql-edit | Skipped: no a .graphql query file or the schema snapshot changed added or changed in this diff. |
| separate-streams-cannot-test-order | Skipped: no a test asserts that one line of output comes before or after another in a command that writes to both stdout and stderr added or changed in this diff. |
| stable-json-dto | Pass: Explicit DTOs, existing keys retained, additive stoppedReason, startError remains null. |
| sweep-by-identifier-not-by-prose | Skipped: no a change removes or renames a surface — a flag, a subcommand, a --json key, a GraphQL field added or changed in this diff. |
| sweep-the-subject-not-the-removed-rule | Pass: Caller-own start explanations swept across source, tests, help, usage and plan. |
