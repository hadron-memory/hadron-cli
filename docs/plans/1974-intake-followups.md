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

The Skipped rows state what the #2250 diff actually lacks — it touches only the
intake adapter runner (`internal/intake/`), the find-untracked adapter path and
its help, the new native fixture, tests, and these plans; no `.graphql` file,
schema snapshot, flag registration, confirmation prompt or scope resolution
changes. Row reasons were rewritten for concreteness and two rows corrected in
#2260 (F4/F9): the `stoppedReason` compatibility claim and the prose-pointer
row, whose pointers were each followed to their definitions that time.

| Review node | Result |
|---|---|
| a-claim-must-not-outrun-its-evidence | Pass: Limit stops cannot claim exhaustion; errors retain partial results and explicit stages. |
| a-gate-can-outlive-its-premise | Skipped: no gate in this diff reads a proxy for an upstream mechanism an expiry, reap or cleanup could retire — the server contract is untouched (no `.graphql` or schema change). |
| a-matrix-case-states-only-what-its-contract-decides | Skipped: no acceptance-matrix case or contract-cited fixture is added or edited — the new `testdata/adapter` is a runner test double, not a contract artifact. |
| ambient-scope-must-report-its-source | Skipped: scope resolution (App, memory, org) is unchanged; the adapter path's App resolution predates this diff. |
| a-mutation-check-can-itself-be-a-no-op | Skipped: no guard's correctness is argued from a mutation run; the pins' redness is the red-first commit against fresh main (mutate-with-the-original-input row). |
| a-narrowed-rule-turns-expect-none-into-a-tautology | Skipped: nothing in this diff narrows what a rule counts (only enabled, live, owned, in-scope). |
| an-empty-flag-is-not-an-absent-flag | Pass: Changed integration with empty/whitespace value refuses before adapter/server calls. |
| an-empty-result-as-a-signal-must-be-unforgeable | Skipped: no empty result is read as a termination signal; the walk ends on the adapter's cursor end, and a limit stop is now explicitly NOT exhaustion. |
| a-new-meaning-for-an-existing-glyph | Skipped: no unknown value is rendered with a placeholder a neighbouring column already uses for a definite answer. |
| an-exemption-you-cannot-enumerate-is-a-hiding-place | Skipped: the only exemption-shaped check — the adapter env scrub — moved to the closed, enumerable `HADRON_` prefix; nothing unenumerable is exempted. |
| a-pointer-must-be-followed-not-reasoned-about | Pass: the prose this diff edits names commands, calls, a file and an exit code — `task release` (internal/cmd/team/task_release.go), the `untrack`/`report` adapter calls (internal/intake/intake.go), `intake-cursors.json` (internal/intake/cursor.go), the second-run exit 5 (internal/cmd/team/reconcile.go) — each followed to its definition, not assumed (#2260 F9). |
| a-recommended-field-must-be-seen-carrying-a-value | Skipped: no fix rests on a schema field or option nothing here has observed carrying a real value — no GraphQL selection changes. |
| assert-the-query-not-the-capture | Skipped: no test keys on which GraphQL FIELDS an operation requests; the operation-level pin (no `IntakeStartTask` is sent) is covered by mutate-with-the-original-input. |
| async-wait-command-contract | Skipped: no command polls or waits on server-side async state — the reconcile lock's 2-minute wait is on a local file lock, not server state. |
| a-test-double-must-satisfy-the-real-access-pattern | Pass: Native executable receives real JSON stdin and call argv; multiple runner calls, caps and refusals covered. |
| a-tidy-argument-is-not-a-checked-one | Skipped: no comment or plan argues an ambiguous value is safe to treat as certain — the `MaxExternalIDLen` rewrite states a bound, not a tidied ambiguity. |
| backticks-in-flag-usage-become-the-placeholder | Skipped: no cobra flag usage string is added or edited — the flag set predates this diff; only the Long help changed. |
| bind-terminal-evidence-to-target-process | Skipped: no terminal idle or prompt evidence authorizes stopping, resetting or replacing a process. |
| canonical-ref-handling | Skipped: no command resolves or composes a memory or node reference, and no upsert-style write is added. |
| classify-by-provenance-before-wrapping | Skipped: no new render-site string is wrapped in `output.SafeCell` and no detail/table site is swept into guarded cells; the existing SafeCell sites are unchanged. |
| classify-security-input-by-structure | Skipped: no API or transport guard classifies a request by scanning an encoded body — the env scrub matches variable NAMES by prefix, not bodies. |
| confirm-prompt-tells-the-truth | Skipped: no confirmation prompt is added or changed; the adapter path prompts for nothing. |
| entity-fields-not-display-labels | Skipped: `--json` gains no field naming another entity — the one new key (list `stoppedReason`) carries the walk's own state. |
| enumerate-the-paths-before-promising-a-guarantee | Pass: One scrubbed Runner.call covers every adapter call; one create reports server state with no follow-up start. |
| fetch-only-what-the-command-needs | Skipped: no GraphQL operation or selection changes, and no entity is newly resolved by name or ref or read as a list. |
| gate-on-the-condition-not-the-flag | Skipped: the empty-value refusals key on flag-presence-plus-empty-value deliberately (an-empty-flag-is-not-an-absent-flag row), never on presence standing in for a fact. |
| help-text-matches-changed-behaviour | Pass: Long help, agentic usage, inventory and design-as-built updated together. |
| improving-an-answer-widens-a-domain | Skipped: no `--json` value becomes MORE certain — no retired null, hedge or unknown state; `complete` at a limit becomes honestly false. |
| map-new-server-error-codes | Skipped: no server operation with new typed error codes is wired — no operation changes. |
| mutate-with-the-original-input | Pass: Separate first test commit fails for scrub, empty integration, limit completion, redundant start and null name. |
| needs-a-spec-when-it-decides-a-rule | Skipped: the diff stays inside the CLI's own edge — `stoppedReason` vocabulary is CLI output contract (agentic-usage.md), and claim starting implements cor:agt:050:02 rather than deciding a new rule (see the header). |
| no-hand-parsed-ref-labels | Skipped: no display label is parsed from a user-supplied ref — the name fallback reads a server field, falling back to the server's own id. |
| no-rmw-sugar-over-wholesale-writes | Skipped: no command edits part of a list or set the server replaces whole. |
| old-server-graphql-document-compatibility | Skipped: no GraphQL operation adds a server-new argument — no `.graphql` or schema change. |
| omit-unset-optional-args | Skipped: no optional GraphQL variable or input field is added — no operation changes. |
| read-the-verdict-body-not-only-the-inline-comments | Skipped: no review-bot verdict is summarized from inline comments alone. |
| recovery-must-not-block-the-host-loop | Skipped: no recurring registration, collection, delivery or heartbeat loop gains waits, RPCs or recovery jobs. |
| regenerate-after-graphql-edit | Skipped: no `.graphql` query file or schema snapshot changed. |
| separate-streams-cannot-test-order | Skipped: no test asserts that one line of output comes before or after another across stdout and stderr. |
| stable-json-dto | Pass, with the breakage named: explicit DTOs; keys retained (`startError` stays, always null). `stoppedReason` is additive ON LIST ONLY — track already carried it. Track's `stoppedReason` vocabulary CHANGED (`"stopped"` → the failing stage, or `limit_reached`), and on BOTH paths a limit stop now reports `complete:false` + `stoppedReason:"limit_reached"` where it reported `complete:true`: a consumer matching `"stopped"` or `complete:true`-at-limit breaks (#2260 F4/F9). |
| sweep-by-identifier-not-by-prose | Skipped: no flag, subcommand, `--json` key or GraphQL field is removed or renamed. |
| sweep-the-subject-not-the-removed-rule | Pass: Caller-own start explanations swept across source, tests, help, usage and plan. |
