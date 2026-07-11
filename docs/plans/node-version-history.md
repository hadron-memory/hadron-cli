# Implementation Plan: `hadron node version …` — node version history

> **Status: implemented and verified**; this reflects the design as built. GH
> issue [#216](https://github.com/hadron-memory/hadron-cli/issues/216), against
> hadron-server [#617](https://github.com/hadron-memory/hadron-server/pull/618).

## Context

Every node edit snapshots the previous state server-side, but the CLI exposed
**none** of that history — `internal/cmd/node/` had ls/get/add/update/move/
clone/merge/rm/export/import and nothing for versions or restore. hadron-server
#617 expanded the version-history GraphQL surface (a single-snapshot read, a
delete-one, a clear-all, an editor-identity field, and a `truncate` option on
restore), so this change makes the whole surface reachable from `hadron`.

## Server surface consumed (#617)

| Op | Kind | Notes |
|---|---|---|
| `nodeVersions(nodeRef: ID!, limit: Int)` | query | history, most-recent-first; per-snapshot memory-gated. `nodeRef` = PK or URN. |
| `nodeVersion(versionId: ID!)` | query | **new** single-snapshot read; **null** (never error) when the node is unreadable/soft-deleted. |
| `restoreNodeVersion(versionId: ID!, truncate: Boolean = false)` | mutation | **extended**. Default snapshots-then-restores (undoable); `truncate:true` also deletes every snapshot newer than the selected one, atomically. |
| `deleteNodeVersion(versionId: ID!)` | mutation | **new**. NOT_FOUND for an unknown id. |
| `clearNodeHistory(nodeRef: ID!)` | mutation | **new**. Returns the count; reachable on soft-deleted nodes. |
| `NodeVersion.editedByUser: NodeVersionEditor` | field | **new**. Resolves `editedBy` to public identifiers only (`handle` + `urn`); null for app/client principals, deleted, or handle-less users. |

## Command surface (as built)

A dedicated `node version` subgroup (mirrors the `memory member` / `org invite`
grouping pattern) rather than five more flat verbs on `node`:

```
hadron node version list <node-ref> [-m <memory>] [--limit N]   # nodeVersions      (alias: ls)
hadron node version get <version-id>                            # nodeVersion       (alias: show)
hadron node version restore <version-id> [--truncate] [--yes]   # restoreNodeVersion
hadron node version delete <version-id> [--yes]                 # deleteNodeVersion (alias: rm)
hadron node version clear <node-ref> [-m <memory>] [--yes]      # clearNodeHistory
```

Verb naming mirrors the sibling node commands: the verbose form is primary
(`list`/`get`/`delete`) with the short form an alias (`ls`/`show`/`rm`).

## Design decisions

- **`nodeRef` resolution (`versionNodeRef`).** A fully-qualified URN, or a
  `-m/--memory` + bare loc, is resolved client-side to the node PK via the shared
  `cmdutil.ResolveNodeRef` — consistent with the other node commands and
  validating the URN grammar. A **bare token** (no `::`, no `hrn:`/`urn:` scheme,
  no `-m`) is passed through verbatim as a node PK. This last path is deliberate:
  `clearNodeHistory` is reachable on a **soft-deleted** node, whose URN no longer
  resolves through `resolveUrn`, so a raw id is the only way in. The server's
  `nodeRef` accepts a PK either way.
- **`versionId` is opaque** — `show`/`restore`/`rm` pass it straight through; no
  client-side resolution.
- **Editor identity.** `editedByUser` renders as `@handle`, falling back to its
  `urn`, then the raw `editedBy` id, then `—`. The two genqlient selections
  produce distinct `NodeVersionEditor` types, so each is aliased
  (`listEditor`/`showEditor`) with a small nil-guarded converter rather than a
  reflective/generic one — a typed-nil must not masquerade as a present editor.
- **Confirmation guards.** `rm` and `clear` are deletions → `ConfirmDeletion`
  (+`--yes`). `restore --truncate` discards intervening history and isn't
  undoable → guarded by `cmdutil.Confirm` **only when `--truncate` is set**; a
  plain restore is undoable and needs no prompt.
- **`truncate` wire semantics.** The operation variable carries
  `# @genqlient(omitempty: true)`, so an unset flag is omitted and the server
  applies its `= false` default — a nil pointer is never sent as `null`.
- **`--json` DTOs.** Explicit `versionDTO` / `versionDetailDTO` / `restoredNodeDTO`
  structs (never genqlient types), with `tags` normalized to `[]` so empties
  render as `[]`, not `null`.

## Files

- `internal/api/queries/versions.graphql` — the five typed operations.
- `internal/cmd/node/version.go` — the subgroup, its five leaves, DTOs, and the
  `versionNodeRef` / `editorDisplay` helpers. Wired into `NewCmdNode`.
- `schema/schema.graphql` — refreshed snapshot (adds the #617 surface).
- `internal/cmd/agentic/agentic-usage.md` — `node` surface line + examples.
- `internal/cmd/node_version_test.go` — command tests against the fake server.

## Testing

`internal/cmd/node_version_test.go` covers: bare-id passthrough vs URN
resolution, `--limit` forwarding, `show` rendering + null→NotFound, restore
default (truncate omitted) vs `--truncate` (sent, gated by `--yes`), the
`--truncate`/`rm` refusals without `--yes` (no mutation sent), and `clear`'s
count output. `TestAgenticUsageDocumentsEveryCommand` gates the doc surface line.
