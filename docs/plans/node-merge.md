# Implementation Plan: `hadron node merge`

> **Status: implemented and verified** on this branch; reflects the design as
> built. GH issue
> [#186](https://github.com/hadron-memory/hadron-cli/issues/186). Part of the
> CLI⟷portal parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67);
> surfaces the server's existing `mergeNodes` mutation (no server change needed).

## Context

The server has long had `mergeNodes(input: MergeNodesInput!): Node!` — it folds
a **source** node into a **target** (the survivor) and returns the target. It was
portal-/API-only; this adds the CLI surface. Sits alongside the freshly-added
`node move`/`node clone` as the third node-relocation/consolidation verb.

Server contract (unchanged):

```graphql
input MergeNodesInput {
  source: String!            # id or full URN — folded into target
  target: String!            # id or full URN — the survivor
  deleteSource: Boolean = false
  include: [NodeMergeField!] # omit/null = every mergeable field
}
enum NodeMergeField { ABSTRACT CONTENT DATA DESCRIPTION EDGES PROPERTIES TAGS }
```

Fold strategy (server-defined): CONTENT/ABSTRACT/DESCRIPTION concatenate
target-first; TAGS unions; DATA/PROPERTIES shallow-merge with the target winning
on key collisions; EDGES re-points the source's incoming/outgoing edges onto the
target. Typed errors: `NODE_NOT_FOUND`, `MERGE_SOURCE_EQUALS_TARGET`; a
cross-memory merge needs write access to both memories (and to any third memory
whose edges get re-pointed).

## Command surface (as built)

```
hadron node merge <source-urn> | <loc> -m <memory> --into <target> \
  [--field <f>]... [--delete-source] [--yes] [--json]
```

- **Source** is the positional; **target** is `--into`. Each is a full URN or a
  bare `<loc>` resolved via `-m/--memory`, which scopes **both** endpoints
  (mirrors `edge add -m`). Both are resolved to node ids via
  `cmdutil.ResolveNodeRef` before the call.
- **`--field`** (repeatable, case-insensitive) selects which fields fold in;
  parsed by `parseMergeFields` into `[]gen.NodeMergeField`, de-duplicated and
  sorted for a deterministic wire order. Omitting it sends no `include` (server
  folds every field). An unknown value is a usage error (exit 2) naming the set.
- **`--delete-source`** sets `deleteSource: true` (hard-removes the source after
  a successful merge); omitted → `deleteSource` is omitted on the wire (server
  default `false`).
- **Confirmation.** Merge mutates the target (and, with EDGES, re-points
  relationships) and can delete the source, so it is gated like the other
  destructive/bulk-write commands: `cmdutil.Confirm` prompts on a TTY and refuses
  non-interactively without `--yes`. The prompt escalates when `--delete-source`
  is set.

## GraphQL / codegen

- Added the `MergeNodes` mutation in `internal/api/queries/nodes.graphql` with
  `# @genqlient(for: "MergeNodesInput.include"/"deleteSource", omitempty: true)`
  so an unset field is omitted (not sent as `null`), letting the server apply its
  defaults (all fields / keep source). `MergeNodesInput` + `NodeMergeField` were
  already in the committed schema snapshot; only `generated.go` changed —
  `make schema` was not needed.

## `--json`

Marshals the package-local `nodeDTO` (the surviving target) via `output.Write`,
identical to the sibling node commands.

## Tests

`internal/cmd/node_merge_cmd_test.go`: all-fields (include + deleteSource
omitted), selected fields (case-insensitive, de-duped, sorted `include` on the
wire), `--delete-source`, missing `--into`, an invalid `--field`, and the
non-interactive `--yes` gate.
