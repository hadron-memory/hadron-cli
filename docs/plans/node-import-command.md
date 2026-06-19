# Implementation Plan: `hadron node import` — single-node import (md + json)

> **Status: implemented and verified** on branch `feat/node-export-import`
> (not yet merged); this reflects the design as built. GH issue
> [#33](https://github.com/hadron-memory/hadron-cli/issues/33). Paired with
> [`hadron node export`](node-export-command.md) (issue #34) — one feature: an
> export must re-import without loss. The shared `internal/nodedoc` codec this
> command parses with is defined alongside the exporter.

## Context

The ask (issue #33): `hadron node import --format md <file-path>` — parse a
node file; if a node already exists at that loc, **update** it, otherwise
**create** it. `--format md` is the default; `json` is a first-class second
format.

This maps almost exactly onto an existing primitive. `upsertNode`
([nodes.graphql:181](internal/api/queries/nodes.graphql)) is *already* a
create-or-update keyed on `(memoryId, loc)`: `node add` passes
`createOnly: true` to force create-only; `node update` resolves an existing
node first. Import is the third caller — `createOnly` unset (the default), so
the upsert's own semantics deliver issue #33's "update if exists, else create"
for free.

Crucially, `gen.NodeInput`
([generated.go:974](internal/api/gen/generated.go)) already carries **every**
round-trippable field — `alias`, `description`, `abstract`, `content`,
`nodeType`, `tags`, `seq`, `data`, `properties`, and `edges`. So importing the
node body needs **no schema or codegen change**; `node add`/`update` simply
never populated the richer fields.

The reference for *what a faithful parse looks like* is the server's own
importer, `loadFrontmatterNode`
(`hadron-server/src/integrations/localFs/loadNodeFromFs.ts`) — the same code the
markdown format was designed to round-trip through. The CLI parser
(`nodedoc.ParseMarkdown`, added by the export plan) mirrors it: the
`^---\n(...)\n---\n?(...)$` split, YAML parse, `description ?? summary`
fallback, abstract normalization, edge `id ?? target` keying.

## Command surface

```
hadron node import <file-path|-> [-m <memory>] [--loc <loc>]
                   [--format md|json] [--with-edges] [--create-only]
                   [--dry-run] [--json]
```

- Positional `<file-path>` — the file to import. `-` reads **stdin**, so
  `node export … | node import -m … -` round-trips through a pipe.
- `--format md|json` — defaults to `md`; inferred from the file extension when
  unset and the path isn't `-`; explicit `--format` wins.
- `-m/--memory` — target memory (id or URN, resolved via `resolveMemoryID`).
  **Precedence:** flag > frontmatter `memory:` key > error. Lets you retarget a
  file to a different memory while a self-describing export needs no flag.
- `--loc` — target loc. **Precedence:** flag > frontmatter `loc:` key > error
  ("loc not found; pass --loc"). Lets you re-home a node.
- `--create-only` — pass `createOnly: true`; fail if the loc already exists
  (the `node add` guard). Default is upsert.
- `--with-edges` — also wire the file's `nodes:` edges (best-effort; see below).
  **Off by default**, so import never makes surprising edge mutations and stays
  fast; when the file *has* edges and the flag is off, print a one-line hint.
- `--dry-run` — parse, resolve target, classify create-vs-update, print the
  plan; **no mutation**. Cheap safety valve for delegated/bulk work.
- `--json` — `importNodeSummaryDTO`
  `{ memory, loc, action: "created"|"updated", nodeId, edgesWired,
  unwiredEdges[] }`.

```
hadron node import flaky.md                       # self-describing file
hadron node import flaky.md -m acme.com:kb2        # retarget to another memory
hadron node import --format json flaky.json --with-edges
cat flaky.md | hadron node import -m acme.com:kb -
```

## Control flow

1. Read the source (`<file-path>` or stdin). Empty input → usage error.
2. `nodedoc.ParseMarkdown` / `ParseJSON` → `Document` (the shared struct from
   the export plan).
3. Resolve **memory** (flag → `Document.MemoryURN` → error) and **loc** (flag →
   `Document.Loc` → error). Resolve memory id via `resolveMemoryID`.
4. Build `gen.NodeInput` from the `Document` (mapping below).
5. `--dry-run`: classify by a cheap existence probe (`resolveUrn` on
   `<memory>:<loc>`, or list-by-prefix) and print "would create/update" + the
   field set; stop.
6. `gen.UpsertNode(input)` → created or updated. Classify the action by probing
   existence *before* the upsert (so the summary can say which happened).
7. If `--with-edges`: wire edges (below), collecting unresolved targets.
8. Emit summary (`importNodeSummaryDTO`) or the human line
   (`✓ created acme.com:kb:findings:flaky-ci`).

### `Document` → `NodeInput` mapping

| Document field | NodeInput | Notes |
|---|---|---|
| `Name` | `Name` (required) | |
| (resolved) | `MemoryId`, `Loc` | from flag/frontmatter precedence |
| `Content` | `Content` | body; `""` → omit vs. clear is a decision (below) |
| `Type` | `NodeType` | `"link"`→link etc.; omit `info`? (decision below) |
| `Alias`, `Description`, `Abstract` | same | |
| `Tags`, `Seq`, `Data`, `Properties` | same | `Data`/`Properties` pass through as `json.RawMessage` |
| `--create-only` | `CreateOnly` | |
| `AbstractOriginHash`, `ContentHash` | **dropped** | not settable — see limitations |
| `Edges` | via `--with-edges`, not `NodeInput.edges` | see below |

**Verify-on-implement:** the schema *exposes* `data`/`properties`/`seq`/`alias`
on `NodeInput`, but a quick live check should confirm the server resolver
actually *persists* each (schema surface ≠ resolver write). Note any field that
silently no-ops as a limitation rather than claiming a false round-trip.

## Edges — opt-in, best-effort, idempotent (`--with-edges`)

Single-node edge import is the genuinely hard part, for three structural
reasons. The plan handles each explicitly rather than pretending edges are free:

1. **`NodeInput.edges` can't carry fidelity.** `NodeEdgeInput`
   ([generated.go:959](internal/api/gen/generated.go)) is only
   `{ label, targetId }` — no `condition`, no `priority`. The format *does*
   carry both. → **Wire edges with `createEdge`** (which takes `priority` +
   `condition`,
   [nodes.graphql:202](internal/api/queries/nodes.graphql)), one call per edge,
   **after** the node upsert — not through `NodeInput.edges`.
2. **Targets may not exist yet.** A node's edge can point at a node you haven't
   imported. The server's full-graph sync does a two-pass (all nodes, then
   edges); a single-node import can't. → Resolve each target by **loc within the
   target memory** (`NodeEdgeInput.targetId` / edge resolvers accept a
   memory-scoped loc), falling back to the frontmatter `id`. A target that
   doesn't resolve is collected into `unwiredEdges[]` and reported — **never
   fatal**. (Bulk wiring is `memory import`'s job, a follow-up.)
3. **`createEdge` isn't idempotent.** Re-importing would duplicate edges. →
   Before creating, read the upserted node's existing `outgoingEdges` (one
   `nodeById`/`nodeBatch` call) and **skip any (targetId, label) that already
   exists.** Re-import converges instead of piling up duplicates.

Default (no `--with-edges`): the node is imported, edges are listed in the hint
("3 edges in file; re-run with --with-edges to wire them"), nothing is mutated.

## Round-trip contract (jointly owned with the export plan)

The invariant the two plans exist to guarantee:

```
node export X -o f          # (md or json)
node import -m <X-memory> f  # f is self-describing; -m optional
node get X                   # == the original X
```

Pinned at two levels:

- **Codec property test** (`internal/nodedoc`): `ParseMarkdown(RenderMarkdown(
  d,true))` and `ParseJSON(RenderJSON(d))` deep-equal `d` for every
  importer-consumed field. This is the byte-level guarantee and lives with the
  codec (export plan).
- **Live smoke** (this plan): export a real node, re-import to a scratch loc,
  `node get` both, diff the DTOs — expect equality modulo the documented
  recompute fields.

## Package layout

| File | Contents |
|---|---|
| `internal/cmd/node/import.go` | `newCmdImport`; read → parse → resolve → upsert → (edges) → summary; `importNodeSummaryDTO` |
| `internal/cmd/node/import_edges.go` | `wireEdges` — resolve/skip/createEdge, collect `unwiredEdges` (kept separate so the upsert path stays readable) |
| `internal/cmd/node/node.go` | wire `newCmdImport` into the group |
| `internal/nodedoc/*` | `ParseMarkdown`/`ParseJSON` (added by the export plan) |

## Tests

- `internal/cmd/node/import_test.go` (cmd-package wiring test, fake GraphQL,
  mirroring [memory_export_cmd_test.go](internal/cmd/memory_export_cmd_test.go)):
  - md create (loc absent on server → `createOnly` unset → upsert) and md
    update; the captured `NodeInput` carries `alias`/`data`/`properties`/`seq`.
  - `--format json` parses the canonical object; extension inference.
  - target precedence: `--memory`/`--loc` override frontmatter; missing both →
    usage error.
  - stdin (`-`) path; empty input → usage error.
  - `--create-only` maps to `createOnly: true`.
  - `--dry-run` mutates nothing (no `UpsertNode`/`createEdge` calls captured).
  - `--with-edges`: existing-edge skip (idempotency); unresolved target →
    `unwiredEdges`, exit 0; condition+priority forwarded to `createEdge`.
  - summary contract (`action`, `nodeId`, `edgesWired`, `unwiredEdges` is `[]`
    not `null`).
- **Round-trip integration test**: build a `Document`, render (md + json),
  parse back, assert the resulting `NodeInput` equals the original's fields —
  the executable form of the contract.

## Verification

- `make build`, `go test ./...`, `golangci-lint run`: green.
- Live smoke against `hadronmemory.com:dev`: the export→import→get round-trip
  above; an update (re-import a tweaked file) lands as `updated`; `--with-edges`
  wires a real edge and is idempotent on a second run; a file pointing at a
  missing target reports `unwiredEdges` and exits 0.

## Known limitations

- **`abstractOriginHash` & `contentHash` are recomputed by the server**, not
  set by the client (neither is settable — `contentHash` isn't on `NodeInput`,
  `abstractOriginHash` is system-managed). Lossless for unchanged content (the
  hashes are deterministic over content). The one real loss: a node exported
  while its abstract was *stale* (`abstractOriginHash != hash(content)`) loses
  that staleness marker on re-import. Documented, acceptable.
- **Edges are opt-in and best-effort** (`--with-edges`): targets must already
  exist in the memory; wiring isn't atomic with the node upsert; idempotency is
  by (targetId, label) skip. Full multi-node graph import with two-pass edge
  resolution is a `memory import` follow-up.
- **Incoming edges can't be reproduced** from a single file — they live on the
  source nodes.
- **Empty content** (`content: ""`): on a clean round-trip the body is the
  source of truth; the implementer picks one rule — omit when empty (preserve)
  vs. send `""` (clear) — and pins it with a test. Recommend **omit-when-empty**
  to match the `node add` convention (empty content already isn't sent).

## Out of scope (follow-ups)

- `hadron memory import` — directory/bundle import with two-pass edge wiring
  (the natural home for full-graph edge fidelity).
- Conflict policy beyond `--create-only` (e.g. `--if-unchanged`, merge).
- ID remapping when importing into a memory that already uses the source id.
