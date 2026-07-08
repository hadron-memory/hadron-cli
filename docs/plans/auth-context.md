# Implementation Plan: `authContext`-backed auth commands

> **Status: implemented and verified**; this reflects the design as built.
> Follow-up to `auth token validate` ([#183](https://github.com/hadron-memory/hadron-cli/pull/183),
> issue [#182](https://github.com/hadron-memory/hadron-cli/issues/182)), enabled
> by the server capability we requested in
> [hadron-server#562](https://github.com/hadron-memory/hadron-server/issues/562)
> (shipped in hadron-server#563).

## Context

`auth token validate`, `auth status`, and `auth whoami` were all built on the
`me` query, which is lossy for auth introspection: it collapses every failure
into "invalid + a display name", never names *which* credential authenticated,
and returns `null` for a perfectly valid **App key** (so validate false-negatives
app credentials as "invalid").

The server now exposes the resolved principal for the current request:

```graphql
type Query { authContext: AuthContext }
enum PrincipalType { USER APP AGENT }
type AuthContext {
  principalType: PrincipalType!   # USER | APP | AGENT
  user: User                      # populated for USER principals
  appId: ID                       # populated for APP principals
  agentId: ID                     # reserved; always null today
  apiKey: UserApiKey              # the specific key used (USER key auth only)
}
```

`authContext` is `null` when the presented credential doesn't resolve —
**identically for revoked / never-existed / malformed** (no token oracle), so the
"invalid" branch stays deliberately non-committal.

## What changed (as built)

A single new query (`internal/api/queries/me.graphql`), reusing the existing
`UserApiKeyFields` fragment for `apiKey`. Three commands re-sourced from it:

- **`auth token validate`** — now credential-type-agnostic. A valid **user** key
  reports the owning user *and names the exact key* (`keyPreview`, `label`,
  `lastUsedAt`) as a nested `key` object. A valid **App** key now reports
  `valid:true` + `principalType:APP` + `appId` instead of "invalid". The invalid
  branch is unchanged (still the hedged message, by the no-oracle design).
- **`auth status`** — adds `principalType` and, for user keys, the `key` object;
  the human line names the principal kind for non-user credentials.
- **`auth whoami`** — adds `principalType`; an App key no longer errors with
  "token was not accepted" — it prints `App <appId>`.

### `--json` contract (additive, no breaks)

All existing fields are retained. New fields:

| Command | Added fields |
|---|---|
| `token validate` | `principalType`, `appId` (omitempty), `key` (omitempty; same shape as `token ls` rows) |
| `auth status` | `principalType` (omitempty), `key` (omitempty) |
| `auth whoami` | `principalType` (omitempty), `appId` (omitempty) |

The `key` object reuses the command package's `tokenDTO`, so it matches
`token ls`/`token create` output exactly.

## Non-goals

- Distinguishing revoked-vs-unknown for a **non-resolving** token (oracle risk;
  the server returns `null` for both by design).
- `agentId` handling beyond passthrough — no agent-credential principal exists yet.

## Snapshot note

`make schema` also caught the snapshot up on two unrelated server drifts it was
lagging on (`cloneNode` added, `moveNode` signature) — neither touches an
operation the CLI uses, which is why `schema-check` was green beforehand.
