# Implementation Plan: `hadron node export` — single-node export (md + json)

> **Status: implemented and verified** on branch `feat/node-export-import`
> (not yet merged); this reflects the design as built. GH issue
> [#34](https://github.com/hadron-memory/hadron-cli/issues/34). Paired with
> [`hadron node import`](node-import-command.md) (issue #33) — the two are one
> feature: an export must re-import without loss. The shared codec it extracts
> lives in `internal/nodedoc`.

## Context

`hadron memory export` already writes every node in a memory to local
frontmatter markdown, byte-faithful to hadron-server's git-sync format (see
[memory-export-command.md](memory-export-command.md) and the serializer in
[export.go](internal/cmd/memory/export.go)). What's missing is the **single
node** case: grab one node as a portable file, edit/review/move it, and import
it back — into the same memory, a different memory, or a fresh server.

This command is the *producer* half. The format it emits is the contract the
importer parses, so the round-trip invariant (`import(export(n)) == n`) is owned
jointly by the two plans and pinned by golden tests in the shared codec.

What the API gives us (no single-node-specific endpoint needed):

| Surface | Use |
|---|---|
| `resolveUrn(urn)` | node URN → id (already used by `node get`) |
| `nodeById(id)` | full node, but **missing `alias` + `properties`** |
| `nodeBatch(ids)` | full node projection — `alias`, `properties`, edges, everything the serializer needs |

**Decision — read via `nodeBatch([id])`, not `nodeById`.** `GetNodeById`
([nodes.graphql:97](internal/api/queries/nodes.graphql)) omits `alias` and
`properties`; `NodeBatch` selects the complete projection the markdown
serializer already consumes. Reusing the proven `api.CollectNodeBatch` path
means **zero schema/codegen change** for export and one read shape shared with
`memory export` / `spec get --prefix`.

## Foundation: extract `internal/nodedoc` (shared codec)

The serializer is currently package-private to `memory`
([export.go:207–376](internal/cmd/memory/export.go)). Both `node export` and
`node import` need it, and import needs its inverse. Extract a small codec
package so the encode/decode pair lives in one place and round-trip is true *by
construction*:

```
internal/nodedoc/
  document.go   // the neutral Document struct (decoupled from gen.* types)
  markdown.go   // RenderMarkdown(doc, standalone) / ParseMarkdown([]byte)
  json.go       // RenderJSON(doc) / ParseJSON([]byte)
  hash.go       // contentHash, decodeJSON helpers
  path.go       // nodeFilePath, locToSegments   (moved verbatim)
  *_test.go     // golden render + the Parse∘Render round-trip property
```

- `Document` is the in-memory node representation both directions share:
  `{ ID, MemoryURN, Loc, Name, Type, Alias, Description, Abstract,
  AbstractOriginHash, ContentHash, Tags, Seq, Data, Properties, Content,
  Edges[] }`, where `Edges` is `{ TargetID, TargetLoc, Label, Condition,
  Priority }`. It is independent of genqlient's deeply-nested generated names,
  so a schema reshuffle can't ripple into the file format.
- Move `buildNodeFrontmatter`, `buildEdgeEntries`, `contentHash`, `decodeJSON`,
  `nodeFilePath`, `locToSegments`, `marshalYAML`, and the `nodeFrontmatter` /
  `edgeEntry` types **verbatim**. `memory/export.go` is rewritten to call
  `nodedoc.RenderMarkdown(doc, false)` — its existing golden tests
  ([memory_export_cmd_test.go](internal/cmd/memory_export_cmd_test.go),
  `export_test.go`) prove the refactor is byte-for-byte non-regressing.
- Add the inverse, `ParseMarkdown`, mirroring the server's `loadFrontmatterNode`
  (`hadron-server/src/integrations/localFs/loadNodeFromFs.ts`): the
  `^---\n(...)\n---\n?(...)$` frontmatter split, YAML parse, body trim.

This extraction is a self-contained first PR (pure refactor, no behavior
change) or the opening commit of the export PR — implementer's call.

## On-disk format

### Markdown — server format + two self-describing keys

The frontmatter is **identical** to what `memory export` writes (so a file
pulled from a tree export and a file from `node export` are interchangeable),
with two additions that make a *standalone* file self-describing:

- `loc:` — the node's own loc. In a tree export this is encoded in the file
  *path* (`<out>/<loc>.md`); a single file has no tree, so it must carry its
  loc inline. The importer reads it (overridable by `--loc`).
- `memory:` — the source memory URN (`<org>:<memory>`), so a lone file knows
  where it came from and `node import` needs no `--memory` flag to round-trip.

Both keys are **ignored by the server's tree importer** (it derives loc from the
path and memory from the sync target), so emitting them is purely additive —
the file still round-trips through hadron-server's git sync unchanged.

Implementation: add `Loc` and `Memory` fields to the shared `nodeFrontmatter`
struct, `omitempty`, positioned right after `id`. `memory export` never sets
them → omitted → its golden output is byte-identical. `node export` sets them
→ they render after `id`. (The `standalone` bool on `RenderMarkdown` gates
populating them; no second struct.)

```yaml
---
name: Flaky CI finding
id: ckk9...
loc: findings:flaky-ci
memory: acme.com:project-kb
type: task
description: One-liner
abstract: Paragraph summary…
contentHash: a1b2c3d4
tags: [ci, flaky]
nodes:
  - id: ckk0...
    loc: start-here
    rel: routes-to
    priority: 10
---

The node content as the markdown body.
```

`contentHash` is recomputed from content (the DB column the API doesn't
expose), same `sha256[:8]` as the server — already implemented and pinned by
`TestContentHashMatchesServer`. `abstractOriginHash` comes straight from the
`nodeBatch` read.

### JSON — one canonical object

`--format json` emits the `Document` as a single pretty-printed JSON object —
the same shape `ParseJSON` reads, so JSON↔JSON is trivially lossless and
md↔json share the in-memory `Document`:

```json
{
  "id": "ckk9...",
  "memory": "acme.com:project-kb",
  "loc": "findings:flaky-ci",
  "name": "Flaky CI finding",
  "type": "task",
  "alias": null,
  "description": "One-liner",
  "abstract": "Paragraph summary…",
  "abstractOriginHash": "a1b2c3d4",
  "contentHash": "a1b2c3d4",
  "tags": ["ci", "flaky"],
  "seq": null,
  "data": null,
  "properties": null,
  "content": "The node content…",
  "edges": [
    { "targetId": "ckk0...", "targetLoc": "start-here", "label": "routes-to", "condition": null, "priority": 10 }
  ]
}
```

NDJSON / multi-node bundles are out of scope (that's `memory export`'s job).

## Command surface

```
hadron node export <node-urn> [-o <file>] [--format md|json] [--json]
```

- Positional `<node-urn>`: fully-qualified, same grammar/resolver as `node get`
  (`cmdutil.ResolveNodeURN`). Bare locs rejected.
- `-o/--out <file>`: target file. **Default: stdout.** `-o -` is also stdout,
  so the command composes in pipes:
  `hadron node export acme.com:kb:x | hadron node import -m acme.com:kb2 -`.
- `--format md|json`: defaults to `md`. When `--out` ends in `.json`/`.md` and
  `--format` is unset, infer from the extension; explicit `--format` wins.
- `--json`: the global structured-output flag. When writing to a **file**,
  prints an `exportNodeSummaryDTO` `{ node, loc, memory, outFile, format,
  bytes }`. When streaming to **stdout**, the document *is* the output (no
  summary wrapper — don't corrupt a piped md/json stream).

```
hadron node export acme.com:kb:findings:flaky-ci -o flaky.md
hadron node export acme.com:kb:findings:flaky-ci --format json -o flaky.json
hadron node export acme.com:kb:findings:flaky-ci          # markdown to stdout
```

## Control flow

1. `ResolveNodeURN(ref)` → node id (usage error on bare loc / wrong kind).
2. `api.CollectNodeBatch([id], …)` → the one full node. An id that lists but
   can't be read (the nodes-list-vs-read visibility gap noted in
   [memory-export-command.md](memory-export-command.md)) comes back in
   `unavailable` → a clean `NotFound`, never a silent empty file.
3. Map the `batchNode` → `nodedoc.Document` (populate `Loc` + `MemoryURN` for
   the self-describing keys; `memory` URN from the batch node's `memoryId`
   resolved via `myMemories`, or carried if the node projection exposes the
   URN — implementer verifies which is cheapest).
4. `nodedoc.RenderMarkdown(doc, true)` or `RenderJSON(doc)`.
5. Write to the file (creating parent dirs) or stream to stdout; emit the
   summary when `--json` + file.

## Package layout

| File | Contents |
|---|---|
| `internal/nodedoc/*` | shared codec (above) — new |
| `internal/cmd/node/export.go` | `newCmdExport`; resolve → batch-read → map → render → write; `exportNodeSummaryDTO` |
| `internal/cmd/node/node.go` | wire `newCmdExport` into the group |
| `internal/cmd/memory/export.go` | rewired to call `nodedoc` (refactor) |

## Tests

- `internal/nodedoc/*_test.go` — golden markdown render (field order, framing,
  omit rules, self-describing `loc`/`memory` keys present); golden JSON render;
  the **round-trip property** `ParseMarkdown(RenderMarkdown(d,true))` and
  `ParseJSON(RenderJSON(d))` deep-equal `d`; `contentHash` vector; unsafe-loc
  rejection. (Most assertions move from `memory/export_test.go`.)
- `internal/cmd/node/export_test.go` (or a `cmd`-package wiring test mirroring
  [memory_export_cmd_test.go](internal/cmd/memory_export_cmd_test.go)) — drive
  the command against a fake GraphQL server: resolve → `NodeBatch` → file at
  `-o`; `type` omitted for `info`; `priority: 0` omitted; stdout path emits the
  raw document (no summary); `--format json` shape; `unavailable` → NotFound.
- `memory export`'s existing tests must stay green unchanged (the refactor's
  proof).

## Verification

- `make build`, `go test ./...`, `golangci-lint run`: green.
- Live smoke against `hadronmemory.com:dev`: export a known node to md and to
  json; eyeball frontmatter (recomputed `contentHash`, `abstract`, inline
  `nodes:` edges); confirm `node export X | node import -m <X-mem> -` recreates
  X (the joint round-trip — co-developed with the import PR).

## Known limitations

- **Single node only.** Incoming edges aren't part of a node's own file (they
  belong to the source nodes); only `outgoingEdges` are serialized. A full-graph
  dump is `memory export`.
- **`abstractOriginHash`/`contentHash` are read-only on the wire** — export
  faithfully *writes* them, but on re-import the server recomputes them (see the
  import plan's limitations). Lossless for unchanged content.

## Out of scope (follow-ups)

- `--with-edges` fidelity lives in the *import* plan (export always writes
  edges; import decides whether to wire them).
- Multi-node selection (`node export --prefix …`) — overlaps `memory export`.
- Byte-identical YAML vs the JS `yaml` writer (values round-trip regardless).
