# Implementation Plan: `hadron user merge`

> **Status: implemented and verified** on this branch; reflects the design as
> built. GH issue
> [#234](https://github.com/hadron-memory/hadron-cli/issues/234). Surfaces the
> server's `mergeUsers` mutation (shipped in hadron-server
> [#656](https://github.com/hadron-memory/hadron-server/pull/656)); no server
> change needed. Sits alongside `node merge` / `memory merge` as the user-tier
> consolidation verb.

## Context

hadron-server #656 added and shipped:

```graphql
input MergeUsersInput {
  source: String!   # id, bare handle, or hrn:user:<handle> — folded into target, soft-deleted
  target: String!   # id, bare handle, or hrn:user:<handle> — the surviving user
}
mergeUsers(input: MergeUsersInput!): User!
```

It globally consolidates a duplicate **source** user into a surviving **target**:
the source is soft-deleted and loses its unique login identifiers; identities,
memberships, owned data, credentials, grants, connections, and relevant audit
references move to the target. Canonical contract:
[`cor:api:010:02`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com::specs::cor:api:010:02).

Authorization is server-enforced (platform `ADMIN`/`OWNER`, or a live org
`ADMIN`/`OWNER` when source and target are both live members of an org the caller
administers). There is **no server-side dry-run**, so the CLI confirmation is the
last local safety boundary before an irreversible global consolidation.

## CLI surface

```console
hadron user merge <source> --into <target> [--yes] [--json]
```

- Direction is unmistakable: `<source>` is consumed and soft-deleted; `--into
  <target>` survives.
- Both references pass through **verbatim** — id, bare handle, or
  `hrn:user:<handle>` all work; the CLI does no client-side resolution (the server
  resolves and authorizes).
- Destructive/global → gated: prompt on a TTY, `--yes` required non-interactively,
  and **cancellation makes no GraphQL request** (confirm runs before the client is
  even built).
- Returns the surviving target in the existing stable `userDTO` shape; human table
  or `--json`.
- GraphQL failures (forbidden, same-user, not-found, collisions) flow through
  `api.MapError` — no server logic is duplicated in the CLI.

## Implementation

- `internal/api/queries/org.graphql` — a typed `MergeUsers($source, $target)`
  mutation returning the shared `UserFields` fragment.
- `schema/schema.graphql` — the `MergeUsersInput` input + `mergeUsers` mutation
  field, added **surgically** rather than via a full `make schema` re-export (see
  Note below).
- `internal/cmd/user/user.go` — `newCmdMerge`, wired under `NewCmdUser`; reuses the
  package's `userDTO` / `userDTOFromFields` / `dash` helpers and `cmdutil.Confirm`.
- `internal/cmd/agentic/agentic-usage.md` — the `hadron user … merge` surface line,
  the destructive-command list, and a prose bullet (kept complete by
  `TestAgenticUsageDocumentsEveryCommand`).

## Tests (`internal/cmd/user_cmd_test.go`)

- `TestUserMerge` — source/target variable mapping (verbatim handle + URN) and JSON
  survivor output.
- `TestUserMergeHumanOutput` — human output names both source and survivor.
- `TestUserMergeRequiresInto` — missing/empty `--into`, and empty source, are usage
  errors before any request.
- `TestUserMergeRequiresYesNonInteractive` — the gate refuses without `--yes`
  non-interactively and `MergeUsers` is never called.
- `TestUserMergeNullResult` — a null `mergeUsers` yields an error, not a panic.
- `TestUserMergeServerErrorPropagates` — FORBIDDEN and same-user failures propagate
  through the error mapping.

## Note — surgical schema-snapshot update (pre-existing drift)

The acceptance criteria asked for a full `make schema` refresh from hadron-server
`main`. That currently fails codegen for an **unrelated** reason: CLI `main`'s
`versions` feature (`internal/api/queries/versions.graphql`) queries `nodeVersions`
/ `NodeVersion`, but hadron-server `main` has since renamed these to
`nodeRevisions` / `NodeRevision`. A whole-snapshot re-export would flip those names
and break the committed versions query — collateral outside this issue's scope.

So the snapshot update here is **surgical**: only `MergeUsersInput` + the
`mergeUsers` mutation field were added (copied verbatim from `main`'s SDL), leaving
the rest of the committed snapshot untouched. `make generate` then produced the
typed client, and codegen is idempotent. **The `nodeVersions` → `nodeRevisions`
drift is a separate, pre-existing bug** that should be reconciled on its own (it
also requires touching `internal/cmd/node/version.go` + tests) — tracked in
[#238](https://github.com/hadron-memory/hadron-cli/issues/238), which owns the full
`make schema` refresh.

## Documentation sync

The CLI reference in the sibling `hadron-docs` repo
(`docs/reference/hadron-cli.md`, guarded by `scripts/check-cli-reference.mjs`
against this CLI's `agentic-usage.md`) is updated in a companion change so the
sync check stays green.
