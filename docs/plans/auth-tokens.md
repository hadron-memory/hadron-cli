# Implementation Plan: `hadron auth token` — mint & manage personal API tokens

> **Status: implemented and verified**, merged in
> [#68](https://github.com/hadron-memory/hadron-cli/pull/68); this reflects the
> design as built. GH issue
> [#54](https://github.com/hadron-memory/hadron-cli/issues/54), Tier 1 of the
> CLI⟷portal parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67).

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

## Where this fits (correction)

> An earlier draft of this plan claimed the OAuth consent screen lived in the
> portal, so the *first* login still needed it. **That was wrong.** Spec
> `025-oauth-for-mcp` put the entire OAuth server — consent screen, login, DCR,
> `hdr_user_*` minting — **server-side**, and the CLI already drives it.

Authentication is already portal-free; this command adds the token-management
layer on top of paths that exist:

1. `hadron auth login` — full server-side OAuth (discovery → DCR → `127.0.0.1`
   loopback → PKCE → token exchange) against the configured server, no portal
   (`internal/auth/`, exercised by `browser_test.go`).
2. `pnpm admin:mint-token` on the server host — a no-browser bootstrap for the
   first credential
   ([hadron-server#303](https://github.com/hadron-memory/hadron-server/pull/303)),
   consumed via `auth login --with-token`.
3. **this PR** — `auth token create` mints further PATs once you have one.

The only residual is server-side (de-hardcode the `/oauth/authorize` IdP bounce
for arbitrary self-host configs), tracked in
[hadron-server#300](https://github.com/hadron-memory/hadron-server/issues/300).
The operator-facing version of this is
[docs/how-to/authentication.md](../how-to/authentication.md).

## Tests / verification

- `internal/cmd/auth_token_cmd_test.go` (fake GraphQL): `create` surfaces
  `rawKey` (+ the once-only warning on the human path) and sends `--label` /
  omits it when unset; `ls` derives `revoked`; `revoke` requires `--yes`
  non-interactively and forwards the id.
- `make build`, `go test ./...`, `golangci-lint run`, `make generate` (no drift): green.
