# Implementation Plan: `hadron auth token` — mint & manage personal API tokens

> **Status: implemented and verified** on branch `feat/auth-tokens` (not yet
> merged); this reflects the design as built. GH issue
> [#54](https://github.com/hadron-memory/hadron-cli/issues/54), Tier 1 / linchpin
> of the CLI⟷portal parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67).

## Context

We want to open-source `hadron-server` (not the web portal) and claim *"everything
works from the CLI."* Today personal access tokens (`hdr_user_*`) can only be
minted in the **portal** — so a self-hoster can't get a headless/CI credential
without it. This command closes the "mint & manage" half of #54.

The server already exposes exactly what we need (feature `025-oauth-for-mcp`,
FR-004):

| Op | Use |
|---|---|
| `createUserApiKey(label): UserApiKeyCreateResult!` | mint; returns `rawKey` **exactly once** (server stores only the SHA-256 hash) |
| `myUserApiKeys: [UserApiKey!]!` | list (active + revoked, newest first) |
| `revokeUserApiKey(id): UserApiKey!` | revoke; idempotent for already-revoked |

All three are **user-scoped**: an AppKey-resolved caller is rejected
`UNAUTHENTICATED`. So they require a user login token (what `hadron auth login`
yields), not an app/agent key.

## Command surface (as built)

```
hadron auth token create [--label <l>] [--json]
hadron auth token ls [--json]
hadron auth token revoke <id> [--yes] [--json]
```

- `create` prints the raw token **once** with a "store it now" warning; `--json`
  emits `{…, rawKey}`. `--label` is omitted when unset (server applies its
  placeholder default).
- `ls` renders a table (id, label, preview, created, last-used, status) and a
  stable `[]tokenDTO` for `--json`; never key material beyond `keyPreview`.
- `revoke` is gated by `cmdutil.Confirm` — prompts on a TTY, requires `--yes`
  non-interactively (revoking invalidates a live credential).

`tokenDTO` derives `revoked` from `revokedAt != nil` for an easy boolean check.

## Codec / API

- `internal/api/queries/auth.graphql` adds the three ops over a shared
  `UserApiKeyFields` **fragment**, so genqlient emits one embedded struct and a
  single `toTokenDTO` maps all three responses. `$label` carries
  `@genqlient(omitempty: true)` so an unset label is omitted, not sent as null.
- No `make schema` needed — the ops were already in the committed snapshot.

## Scope boundary (the part NOT in this PR)

This makes the portal a **one-time** dependency: you still need the portal (or a
browser) for the *first* interactive `auth login`, because the OAuth **consent
screen lives in the portal, not `hadron-server`**. After that one login you mint
all headless/CI tokens from the CLI.

A *fully* portal-free first login (a device-code flow, or shipping the consent
screen in the open-sourced server) is a **`hadron-server` change** and is tracked
separately under #54 — it can't be solved from the CLI alone.

## Tests / verification

- `internal/cmd/auth_token_cmd_test.go` (fake GraphQL): `create` surfaces
  `rawKey` (+ the once-only warning on the human path) and sends `--label` /
  omits it when unset; `ls` derives `revoked`; `revoke` requires `--yes`
  non-interactively and forwards the id.
- `make build`, `go test ./...`, `golangci-lint run`, `make generate` (no drift): green.
