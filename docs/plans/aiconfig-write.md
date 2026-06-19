# Implementation Plan: `hadron ai-config` write — create / update / delete

> **Status: implemented and verified** on branch `feat/aiconfig-write` (not yet
> merged); this reflects the design as built. GH issue
> [#55](https://github.com/hadron-memory/hadron-cli/issues/55), Tier 1 of the
> CLI⟷portal parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67).

## Context

`ai-config` was read-only (`ls` — the masked resolvable-config picker). You
couldn't configure an LLM provider + key + model from the CLI, so the AI half of
a self-hosted platform couldn't be set up without the portal. This adds the
write side over the server's `036-ai-service-config` mutations:

| Op | Use |
|---|---|
| `createAiServiceConfig(name!, provider!, model!, ownerId!, ownerType!, apiKey, enabled, params)` | create; returns the masked config |
| `updateAiServiceConfig(id!, …all optional)` | update by id |
| `deleteAiServiceConfig(id!): Boolean!` | delete |

No `make schema` — the ops were already in the committed snapshot.

## Command surface (as built)

```
hadron ai-config create (--app | --agent | --org <id-or-urn>) --name <n> --provider <p> --model <m> [--api-key -] [--param k=v ...] [--disabled]
hadron ai-config update <id> [--name] [--provider] [--model] [--api-key -|""] [--param k=v ...] [--enabled=false]
hadron ai-config rm <id> --yes
```

- **Owner** — `create` takes exactly one of `--app`/`--agent`/`--org` (ID or
  URN), mapped to `ownerType` + `ownerId` (HADRON_SERVER is platform-admin only;
  reach it via `hadron api`). Mutually exclusive; missing → usage error.
- **Secret** — `--api-key -` reads the key from **stdin** (trimmed), keeping it
  out of argv / shell history; inline is allowed too. The key is never echoed —
  output is the masked DTO (`hasApiKey` + `apiKeyPreview`), reusing the shape
  `ai-config ls` already emits.
- **apiKey semantics on update** — the heart of the design: `--api-key <v>`
  replaces, `--api-key ""` **clears**, omitting it **keeps** the stored key. The
  GraphQL `$apiKey` carries `@genqlient(omitempty: true)`, which is *nil-based*:
  a nil `*string` is omitted (keep), a non-nil `&""` is sent as `""` (clear).
  `update` gates every field on `cmd.Flags().Changed`, so only what you pass
  reaches the wire.
- **params** — `--param k=v` (repeatable) assembles a JSON object; each value is
  sent as JSON when it parses (numbers/booleans/arrays/objects), else as a
  string. On `update`, `--param` replaces the whole object.
- `rm` is gated by `cmdutil.ConfirmDeletion` (prompt on a TTY, `--yes`
  non-interactive).

## Codec / API

`internal/api/queries/aiconfigs.graphql` adds the three mutations over a shared
`AiServiceConfigFields` fragment (one embedded struct → one `dtoFromFields`
mapper). The owner enum maps to `gen.AiConfigOwnerType{App,Agent,Organization}`.

## Discovery of ids

`update`/`rm` take a config id: `create` returns it, and existing configs' ids
come from `ai-config ls --json`. An owner-scoped raw list
(`aiServiceConfigs(ownerId, ownerType)`) — to surface configs the resolved
picker dedupes away — is a possible follow-up, not in this PR.

## Tests / verification

`internal/cmd/aiconfig_cmd_test.go` (fake GraphQL): owner mapping + mutual
exclusion + required-owner; `--api-key -` reads/trims stdin and is never echoed;
`--param` builds the object; `update` omits unset fields (preserve) and sends
`""` to clear the key; `rm` requires `--yes`. `make build` / `go test ./...` /
`golangci-lint run` / `make generate` (no drift) all green.
