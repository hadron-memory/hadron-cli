# Implementation Plan: `hadron ai-config` — AI service config picker

> **Status: implemented and verified** on branch `feat/ai-config-ls` (not yet
> merged). This document reflects the design *as built* and is the review
> artifact for the change. Closes
> [hadron-cli#13](https://github.com/hadron-memory/hadron-cli/issues/13).

## Context

hadron-server shipped a new GraphQL query in
[hadron-server#272](https://github.com/hadron-memory/hadron-server/pull/272)
(spec 036 — AI Service Config registry):

```graphql
resolveAiServiceConfigs(appId: ID, agentId: ID): [AiServiceConfig!]!
```

It returns the **masked** set of AI service configs *resolvable* in a given chat
context — every distinct config name an agent-in-an-app chat could pick from,
deduped with the innermost owner winning (App → Agent → Org → HadronServer),
enabled-only. Each entry is the row `resolveAIConfig` would pick for that name.
It is masked: it never carries key material beyond a short `apiKeyPreview`.

This surfaces it in the CLI so a user (or a UI shelling out to the CLI) can
present a config picker. It is the **first verb of a new `ai-config` group** —
the server has a whole AI-config surface (CRUD, decrypted resolve) to grow into
later. Placement (`ai-config` group vs `app ai-configs`) was the issue's one open
question; resolved with the maintainer in favour of the dedicated group.

## Command surface (as built)

```
hadron ai-config <command>                              (alias: ai-configs)

  ls [--app <id|urn>] [--agent <id|urn>] [--json]       (alias: list)
```

- `--app` is the **existing persistent root flag** (`--app` / `hadron app use`),
  consumed via `cmdutil.Factory.App()`. No local `--app` is declared — that would
  collide with the persistent one and lose the configured-context default for free.
- `--agent` is a new local flag, optional.
- Both `appId`/`agentId` accept an **ID or a URN** and are passed to the server
  **verbatim** — unlike node refs, no client-side URN→ID resolution is needed
  (the server resolves both).
- Table columns: `NAME  OWNER  PROVIDER  MODEL  ENABLED  KEY`, where `OWNER` is
  `ownerType` (which tier won the dedup) and `KEY` is `apiKeyPreview` or `—`.
- `--json` emits a stable `aiConfigDTO` array of all masked fields.

Examples:
```
hadron ai-config ls --app acme.com:juno-app
hadron ai-config ls --app acme.com:juno-app --agent acme.com:juno --json
```

## Auth (server-enforced, surfaced via `api.MapError`)

Scoped to one App's chat context. A non-admin caller must pass `--app`, be a
member of that App, and — when `--agent` is given — the agent must be installed in
that App. Platform ADMIN/OWNER bypass and may omit `--app`. The CLI authenticates
as a user, so the App-membership gate applies normally; `Forbidden`/not-a-member
errors flow through the existing `api.MapError` exit-code path.

## Schema + codegen — `schema/schema.graphql`, `internal/api/gen/generated.go`

The `AiServiceConfig` type and `AiConfigOwnerType` enum already existed in the
snapshot; only the `resolveAiServiceConfigs` field on `Query` was missing.
Refreshed the snapshot with `make schema` (re-export from the sibling
`../hadron-server` checkout via `scripts/export-graphql-sdl.mjs`) — the diff is
**exactly** the one field + its doc block (24 added lines, no unrelated drift).
The export script alphabetizes args, so the canonical form is
`resolveAiServiceConfigs(agentId: ID, appId: ID)`.

New operation [internal/api/queries/aiconfigs.graphql](internal/api/queries/aiconfigs.graphql)
selects the masked fields; `$appId` is declared first so codegen yields
`gen.ResolveAiServiceConfigs(ctx, client, appId, agentId *string)`. Both vars carry
`# @genqlient(omitempty: true)` so an unset flag is **omitted**, not sent as an
explicit `null` (lets the platform-admin "may omit appId" path work). `make
generate` output is purely additive.

## Package layout — `internal/cmd/aiconfig/`

Mirrors `internal/cmd/app/`; calls the generated `gen.*` function directly.

| File | Contents |
|---|---|
| `aiconfig.go` | group root (`ai-config`, alias `ai-configs`); `NewCmdAiConfig`. |
| `ls.go` | `newCmdLs` + `aiConfigDTO` (stable `--json` shape, all masked fields) + `optional(string) *string` helper (nil-when-empty). |

Reuses `cmdutil.Factory.App()` for context, `api.MapError` for errors, and
`output.Write`/`output.NewTable` for rendering — same shape as
[internal/cmd/app/ls.go](internal/cmd/app/ls.go). Wired in
[internal/cmd/root.go](internal/cmd/root.go).

## Tests — `internal/cmd/commands_test.go`

Via the existing `testFactory` + `captureGraphQL` harness (mirrors
`TestAppLs`/`TestAppInstall`):

- `TestAiConfigLs` — table mode with `--app` + `--agent`: asserts the rendered
  name/owner/provider/model + key preview, the `—` fallback for a keyless config,
  and that both flags map to the `appId`/`agentId` variables.
- `TestAiConfigLsJSONOmitsUnsetAgent` — `--json` with only `--app`: asserts the
  masked DTO shape, that **no raw `apiKey` field** leaks, and that an unset
  `--agent` is **omitted** from variables (not `null`).

## Verification

- `make build`, `go test ./...`, `golangci-lint run`: green (0 issues).
- `hadron ai-config --help` / `ai-config ls --help` render the group, alias,
  flags, and examples.
- Masking contract is asserted in the JSON test; no key material beyond
  `hasApiKey` + `apiKeyPreview` in either output mode.

## Out of scope (follow-ups)

- The rest of the `ai-config` surface: management list (`aiServiceConfigs`),
  create/update/delete, and the decrypted `resolveAIConfig` resolve.
