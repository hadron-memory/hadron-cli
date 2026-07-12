# Implementation Plan: `hadron node revision …` — node revision history

> **Status: implemented and verified**; this reflects the design as built. GH
> issues [#216](https://github.com/hadron-memory/hadron-cli/issues/216) (initial
> surface) + [#221](https://github.com/hadron-memory/hadron-cli/issues/221)
> (NodeVersion→NodeRevision adoption), against hadron-server #617/#619/#620/#623.

## Context

Every node edit snapshots the previous state server-side. #216 first exposed this
as `hadron node version …` against the #617 `NodeVersion` surface; **days later**
hadron-server renamed it `NodeVersion → NodeRevision` (breaking, #623) and added
the editor split + `revLabel`/`changes` (#619/#620). #221 adopts that: the
command group is now `node revision`, targeting the `*Revision` GraphQL surface.
("version" is deliberately dropped, not aliased — the server reserves it for a
future deliberate blessed state distinct from automatic per-edit revisions.)

## Server surface consumed (current `main`)

| Op | Kind | Notes |
|---|---|---|
| `nodeRevisions(nodeRef: ID!, limit: Int)` | query | history, most-recent-first; per-snapshot memory-gated. `nodeRef` = PK or URN. |
| `nodeRevision(revisionId: ID!)` | query | single-snapshot read (adds `content`); **null** (never error) when unreadable/soft-deleted. |
| `restoreNodeRevision(revisionId: ID!, truncate: Boolean = false)` | mutation | default snapshots-then-restores (undoable); `truncate:true` also deletes newer revisions, atomically. |
| `updateNodeRevision(revisionId: ID!, revLabel: String)` | mutation | set a revision's label (#620, 500-char cap); returns the snapshot. |
| `deleteNodeRevision(revisionId: ID!)` | mutation | NOT_FOUND for an unknown id. |
| `clearNodeHistory(nodeRef: ID!)` | mutation | returns the count; reachable on soft-deleted nodes. |
| `NodeRevision.editedBy / editedByInfo / editedByUser` | fields | `editedBy` = User id; `editedByInfo` = identity strings (`github:`/`email:`/`app:`) when no User id; `editedByUser` resolves to public identifiers only (`handle`+`urn`). |
| `NodeRevision.revLabel / changes` | fields | user label; which fields the edit changed. |

## Command surface (as built)

A dedicated `node revision` subgroup (mirrors the `memory member` grouping):

```
hadron node revision list <node-ref> [-m <memory>] [--limit N]   # nodeRevisions       (alias: ls)
hadron node revision get <revision-id>                           # nodeRevision        (alias: show)
hadron node revision restore <revision-id> [--truncate [--yes]]  # restoreNodeRevision
hadron node revision label <revision-id> --label <text>          # updateNodeRevision
hadron node revision delete <revision-id> [--yes]                # deleteNodeRevision  (alias: rm)
hadron node revision clear <node-ref> [-m <memory>] [--yes]      # clearNodeHistory
```

Group aliases `revisions`/`rev`. Verb naming mirrors the sibling node commands:
verbose primary (`list`/`get`/`delete`) with the short form an alias
(`ls`/`show`/`rm`).

## Design decisions

- **`nodeRef` resolution (`revisionNodeRef`).** A fully-qualified URN, or a
  `-m/--memory` + bare loc, resolves client-side to the node PK via the shared
  `cmdutil.ResolveNodeRef`. A **colon-free bare token** is passed through as a
  node PK — the only way to reach a **soft-deleted** node's history for `clear`,
  whose URN no longer resolves. A **namespaced** bare loc without `-m` is rejected
  with the usual usage error.
- **`revisionId` is opaque** — `get`/`restore`/`label`/`delete` pass it straight
  through.
- **Editor identity.** `editedByUser` renders `@handle` → `urn`, falling back to
  the raw `editedByInfo` identity string, then `editedBy`, then `—`. A single
  shared genqlient **fragment** (`RevisionFields`) backs every revision-returning
  op, so one nil-guarded converter (`editorFrom`) serves all — no per-op editor
  type.
- **Confirmation guards.** `delete`/`clear` are deletions → `ConfirmDeletion`
  (+`--yes`). `restore --truncate` discards intervening history → `cmdutil.Confirm`
  **only when `--truncate` is set**; a plain restore is undoable, unguarded.
  `label` mutates a label field only, no guard.
- **`label` semantics.** `--label` is required (an explicit `""` clears); the
  value is always sent.
- **`truncate` wire semantics.** `# @genqlient(omitempty: true)` so an unset flag
  is omitted and the server applies its `= false` default — never sent as `null`.
- **`--limit`** rejects negatives with a Usage error (matching `hadron search`).
- **`--json` DTOs.** Explicit `revisionDTO` / `revisionDetailDTO` /
  `restoredNodeDTO` structs (never genqlient types); `tags`/`changes` normalized
  to `[]`. `delete` uses a boolean `deleted`; `clear` uses an int `deletedCount`
  (distinct keys, distinct types).

## Files

- `internal/api/queries/revisions.graphql` — the six typed operations + the
  shared `RevisionFields` fragment.
- `internal/cmd/node/revision.go` — the subgroup, its six leaves, DTOs, and the
  `revisionNodeRef` / `editorDisplay` / `revisionDTOFrom` helpers. Wired into
  `NewCmdNode`.
- `schema/schema.graphql` — refreshed snapshot (the `NodeRevision` surface).
- `internal/cmd/agentic/agentic-usage.md` — `node` surface line + examples.
- `internal/cmd/node_revision_test.go` — command tests against the fake server.

## Testing

`internal/cmd/node_revision_test.go` covers: bare-id passthrough vs URN
resolution, `--limit` forwarding + negative rejection + bare-loc rejection, `get`
rendering (incl. `revLabel`) + null→NotFound, `label` (required flag + revLabel
forwarded), restore default (truncate omitted) vs `--truncate` (sent, gated by
`--yes`), the `--truncate`/`delete` refusals without `--yes`, the
`delete:false`→NotFound guard, and `clear`'s `deletedCount`. The
`editedByInfo`-fallback editor display is exercised by the list test.
`TestAgenticUsageDocumentsEveryCommand` gates the doc surface line.
