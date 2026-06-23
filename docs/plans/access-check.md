# Implementation Plan: `hadron access check` — effective access for (user, resource)

> **Status: implemented and verified** (design-as-built). Part of the CLI⟷portal
> parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67).
> Backed by hadron-server's `effectiveAccess` resolver
> ([hadron-server#323](https://github.com/hadron-memory/hadron-server/issues/323),
> PR [#328](https://github.com/hadron-memory/hadron-server/pull/328)).

## Context

There was no way to ask "what access does user X have to resource Y?" from the
CLI. The grant model is spread across many tables (`memory.members`,
`memory.shares`, the owning org's membership, `app.members`, `agentSubscriptions`,
the AI-config owner walk, public visibility), so composing the answer in the CLI
would re-implement — and inevitably drift from — the server's real authorization
decision.

Instead we filed for, and now consume, an **authoritative** server resolver:

```graphql
effectiveAccess(user: ID!, resource: ID!): EffectiveAccess!
```

The resolver reuses the server's enforced authz paths and returns the
capabilities **plus the grants that confer them**. The CLI side is a thin typed
wrapper + a stable `--json` DTO + the two pieces of client-side reference
resolution the server doesn't do.

## Command surface (as built)

```
hadron access check <user> <resource> [--json]
```

- `<user>` — id, email, or handle. Resolved to a **User ID** (the only form
  `effectiveAccess(user:)` accepts; Users have no URN yet —
  [hadron-server#325](https://github.com/hadron-memory/hadron-server/issues/325))
  via `searchUsers`, which is itself access-scoped to the caller. An
  `hrn:user:` / `urn:user:` wrapper is unwrapped to its inner id/handle for
  forward-compatibility. Exact match on id/email/handle/githubUsername wins;
  a sole fuzzy hit is accepted; multiple non-exact matches are a usage error
  (no arbitrary pick); a colon-free token that matches nothing is passed through
  verbatim as a literal id, but an unmatched email is not-found.
- `<resource>` — a fully-qualified `hrn:<type>:` URN (`memory`/`node`/`app`/
  `agent`) passed through for the server to dispatch, **or** a bare,
  colon-free `AiServiceConfig` id (the one URN-less kind). An unprefixed ref
  containing `:` is an under-qualified shorthand the server would misread as a
  config id, so the CLI rejects it locally with guidance (validated *before*
  the user lookup, so a bad resource fails fast with no network round-trip).

## Output / `--json` contract

DTO defined in the command package (never a genqlient struct), field names
mirror the resolver:

```json
{
  "user":     { "id": "...", "name": "...", "email": "...", "handle": "..." },
  "resource": { "urn": "hrn:memory:acme.com::kb", "kind": "memory" },
  "canRead": true, "canWrite": true, "canManage": false, "canDelete": false,
  "role": "writer",
  "grants": [ { "source": "MEMORY_SHARE", "role": "writer", "via": null } ]
}
```

`grants` is initialized to `[]` (renders `[]`, not `null`). An empty `grants`
array is the first-class **"no access"** answer — the human view prints a
"No access — … has no grants on this resource." line. The human view also shows
a capability table (READ/WRITE/MANAGE/DELETE as ✓/✗) and the role.

## Codec / API

`internal/api/queries/access.graphql` — one `EffectiveAccess` operation. User
resolution reuses the existing `SearchUsers` op (`org.graphql`); no new user
query. Schema snapshot refreshed via `make schema` (+111 lines, the new
`EffectiveAccess`/`AccessGrant`/`AccessSource` SDL only; no unrelated drift).

`internal/cmd/access/` — `NewCmdAccess` group (wired in `root.go`) + `check`
subcommand; `resolveUserID` / `normalizeResourceRef` helpers.

## Exit codes

Routed through `api.MapError`: server `FORBIDDEN` (caller lacks audit rights)
→ exit 1; unresolvable resource (`NOT_FOUND`) → exit 4; local under-qualified
resource / ambiguous user → usage, exit 2. A *subject* with no access is a
**success** (exit 0, empty grants), distinct from the caller being forbidden.

## Out of scope (possible follow-ups)

- A list form (`access who-can <resource>` — every user's access to one
  resource) — the group is named to leave room for it.
- User-URN input once hadron-server#325 lands (`effectiveAccess(user:)` accepts
  a `hrn:user:` URN); today the CLI unwraps it client-side.
- The resolver mirrors the *enforced* memory write gate; once hadron-server#324
  tightens `requireMemoryWriteAccess`, the answer tightens server-side with no
  CLI change.

## Tests / verification

`internal/cmd/access_cmd_test.go` (fake GraphQL): JSON shape + email→id
resolution forwarded to `effectiveAccess`; empty-grants "No access" table;
under-qualified resource rejected locally (exit 2, no network); ambiguous user
(exit 2). `make build` / `go test ./...` / `golangci-lint run ./internal/cmd/access/...`
/ `make generate` (no drift) all green.
