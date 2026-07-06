# Implementation Plan: headless-run surface — `run` / `schedule` / `webhook` / `ticket`

> **Status: implemented and verified** (design-as-built). Closes
> [#134](https://github.com/hadron-memory/hadron-cli/issues/134), part of the
> CLI⟷portal parity epic. Backed by hadron-server's spec-040 headless-run
> kernel ([hadron-server#493](https://github.com/hadron-memory/hadron-server/pull/493)
> — kernel + surfaces, [#494](https://github.com/hadron-memory/hadron-server/pull/494)
> — webhooks + tickets). Specs `cor:agt:010` (+ `:00`/`:01`/`:02`),
> `cor:acl:050:04`.

## Context

The CLI is the open-source counterpart to the closed-source portal: everything
the platform can do must be drivable from `hadron` so a self-hosted deployment is
fully operable without the portal. Spec-040 added a large new surface — headless
App runs (schedules, webhooks, the run audit/control surface, and the action
ticket ledger) — and the CLI had none of it. The schema snapshot was already
refreshed (all spec-040 ops present); the gap was the genqlient operations + the
command layer.

A *run* executes an entry (prompt) node under an App's identity, off any
interactive session. `run`, `schedule`, and `webhook` are three triggers into the
one kernel; `ticket` is the action-budget ledger a run consumes from.

## Command surface (as built)

```
hadron run trigger --app <ref> --entry <node-urn> [--as-self] [--arg k=v]... [--ai-config <n>] [--wait [--wait-timeout <d>]]
hadron run ls [--app <ref> | --org <ref>] [--status <s>]
hadron run get <id>
hadron run cancel <id> --yes
hadron schedule create --app <ref> --name <n> --cron '<expr>' [--tz <zone>] --entry <node-urn> [--as-self] [--policy <json>] [--ai-config <n>] [--arg k=v]... [--agent <ref>] [--disabled]
hadron schedule ls --app <ref>
hadron schedule update <id> [--name|--cron|--tz|--entry|--ai-config|--policy|--arg|--enabled[=false]|--as-self[=false]]
hadron schedule rm <id> --yes
hadron webhook create --app <ref> --name <n> --entry <node-urn> [--as-self] [--policy <json>] [--args-schema <json>] [--ai-config <n>] [--agent <ref>] [--disabled]
hadron webhook rotate <id> --yes
hadron webhook ls --app <ref>
hadron webhook rm <id> --yes
hadron ticket mint --org <ref> [--app <id>] --action comm.outbound --count <n> [--note <why>] [--expires <iso>]
hadron ticket ls --org <ref>
```

Every command supports `--json` over a stable, hand-written DTO (never a
genqlient struct), per the repo's output contract.

## Design decisions

- **One genqlient operations file** (`internal/api/queries/runs.graphql`) holds
  all 14 ops (5 queries, 9 mutations) plus five reusable fragments
  (`AppRunFields`, `AgentScheduleFields`, `AgentWebhookFields`,
  `AgentWebhookCredentialFields`, `ActionTicketFields`). The list/detail/mutation
  results all reuse the same fragment, so the DTO mappers are single-sourced.

- **Input objects + omitempty directives.** The spec-040 mutations take input
  objects (`TriggerAppRunInput!`, `UpdateAgentScheduleInput!`, …), unlike the
  older per-variable mutations. genqlient only adds `,omitempty` to an input
  field's JSON tag when a `# @genqlient(for: "Type.field", omitempty: true)`
  directive precedes the operation (the `NodeInput` pattern). These are
  **load-bearing** on the `Update*` inputs: the server reads an omitted field as
  "preserve" and an explicit `null` as "clear", so an unset flag must be omitted,
  not serialized as `null`. A gotcha found on the way: a directive block must
  precede the `mutation` keyword on its **own line** — a single-line
  `mutation X($input: T!) {` attaches the comment to the `$input` variable
  instead of the operation, and genqlient rejects `for:` on a variable. All
  input-taking ops therefore declare their args on separate lines.

- **Shared helpers in `cmdutil`** (`headless.go`), since four command groups need
  them: `ResolveAppRef` (`--app` flag → App context fallback → usage error),
  `CanonicalNodeURN` (the no-network half of `ResolveNodeURN` — validates the
  `<org>::<memory>::<loc>` grammar and normalizes to `hrn:node:`; the entry node
  is a stored URN resolved at run time, not a node id resolved now),
  `KeyValsToJSON` (`--arg k=v` → eventData object, value parsed as JSON or
  string), and `ParseJSONArg` (`--policy` / `--args-schema` JSON, "" ⇒ omit).

- **`run trigger --wait`** polls `appRun` to a terminal status
  (`COMPLETED`/`FAILED`/`CANCELLED`/`TIMED_OUT`) under a deadline context, so
  `--wait-timeout` bounds the whole wait including a single hung poll (a timeout
  returns the last-seen run plus a `Cancelled`-coded error, exit 6). Because the
  outcome is known, a `--wait` that ends in a **non-`COMPLETED`** terminal status
  exits non-zero (the run, with its failure payload, is still printed) so a
  script can branch on the run's result. `pollInterval` is a package var
  (default 2s).

- **Webhook secrets are shown once.** `createAgentWebhook`/`rotateAgentWebhook`
  return `{ path, token, webhook }`; the URL path and platform token are never
  queryable again. The human view frames them with a "Shown once" warning and a
  ready-to-use `POST <path>?hpt=<token>` line; `--json` carries them under
  `path`/`token`. `webhook ls` never returns the secret, and a test asserts the
  listing's `--json` carries no `token`/`path` field.

- **Gating.** `run cancel` and `webhook rotate` are significant state changes, so
  they use `cmdutil.Confirm` (prompt on a TTY, `--yes` required non-interactively).
  `schedule rm` and `webhook rm` use `cmdutil.ConfirmDeletion` like the other
  destructive commands.

- **`--as-self`** maps to `runAsSelf` on trigger/schedule/webhook. It reaches the
  caller's personal memories and is only usable by an authenticated user — an
  App-key caller gets `UNAUTHENTICATED`. v1 never delegates a third party
  (`cor:agt:010:01`). Surfaced in help; the server enforces the identity rule.

## Deliberately out of scope (recorded in the README)

The completeness audit (issue #134 "100%" criterion) swept every `Query`/
`Mutation` field against the CLI. Uncovered ops fall into three buckets, now
documented in the README's "CLI coverage and deliberate exclusions":

- **Interactive / portal-only** — chat + session lifecycle, editor lock,
  revision/version machinery, multipart asset upload, invitation/onboarding.
- **Git-sync internals** — `replaceSubtree`, `mergeMemories`/`mergeNodes`,
  `pushMemoryToGit`/`syncMemory`, `setMemorySourceToken`, `encryptMemory` (the
  end-user round-trip is `memory export` / `node import`).
- **Not-yet-built follow-ups** — the whole Agent command group (`agent` CRUD, AI
  config wiring, subscriptions, imports); one-time schedules
  (`schedule create --at`, blocked on hadron-server#510); the admin
  grants/policy/quota surface (blocked on hadron-server#510/#501).

All remain reachable through the `hadron api` raw-GraphQL escape hatch.

`ai-config` (spec-036 era) already satisfies the audit's AI-config check: it
covers all three owner levels (`--app`/`--agent`/`--org`), the bedrock provider
(the JSON-triple credential rides the opaque `--api-key` value), and the
"effective config" view (`ai-config ls` wraps `resolveAiServiceConfigs`).

## Testing

Command-level tests (`internal/cmd/{run,schedule,webhook,ticket}_cmd_test.go`)
run against the fake GraphQL server keyed by operation name, asserting request
variables (input-field mapping, entry-URN canonicalization, omitempty on unset
optionals and the `Update*` preserve-vs-clear contract, `--status` enum casing,
`--app`/`--org` mutual exclusion) and output (run id surfaced, shown-once secret
printed, `webhook ls` hides the secret, tri-state `schedule update`). The
no-network usage-error paths (bare-loc entry, non-positive `--count`,
`--yes`-required deletions) are covered too.
