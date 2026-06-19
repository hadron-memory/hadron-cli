# Implementation Plan: `hadron memory export` — local markdown mirror

> **Status: implemented and verified** on branch `feat/memory-export` (not yet
> merged). This document reflects the design *as built* and is the review
> artifact for the change.

## Context

Today the only way to get a memory's nodes out as files is the server's
**git sync**: `pushMemoryToGit(id)` writes the whole memory to a *configured
git repo* as frontmatter markdown. That needs a `source`/`writeBranch` set and
goes to git — not to your disk. The ask: a CLI command that exports **all
nodes** to a **local** directory, no remote required.

What hadron-server exposes through the API (no single-call "dump a memory"
endpoint exists — export is a client-side fan-out either way):

| Surface | Use |
|---|---|
| `nodes(memory, …)` | shallow, paginated id/loc/type listing |
| `nodeById(id)` | one full node (content + edges) |
| `nodeBatch(ids \| memory+locPrefix)` | **bulk** full-node read, ≤200 nodes / ≤1 MB per call (cor:api:040) |
| `pushMemoryToGit(id)` | server-side git export (the existing markdown target) |

**Decisions** (confirmed with the maintainer):

- **Target = local markdown**, mirroring the server's git-sync file format so
  the export is a faithful, re-importable mirror (not a bespoke JSON dump).
- **Fetch via `nodeBatch`** — ~200× fewer round-trips than one `nodeById` per
  node. It was added to the server the day before this work; the CLI snapshot
  was refreshed to pick it up.

## Command surface (as built)

```
hadron memory export <memory-id-or-urn> [--out <dir>] [--format markdown] [--json]
```

- Positional memory ref (id or URN), matching `memory clone|get|rm`.
- `-o/--out <dir>` defaults to `.` (the current directory).
- `--format` defaults to `markdown` and only accepts `markdown`/`md` today; it
  exists so a future JSON/NDJSON target slots in without a breaking change.
- `--json` emits a stable `exportSummaryDTO`:
  `{memory, outDir, nodeCount, skippedData, wroteManifest, unavailable[]}`.

```
hadron memory export acme.com:project-kb --out ./kb
hadron memory export acme.com:project-kb -o ./kb --json
```

## Markdown format — byte-faithful to the server

The serializer in [export.go](internal/cmd/memory/export.go) reproduces
hadron-server's `buildNodeFrontmatter` / `nodeFilePath`
(`src/integrations/github/nodeFrontmatter.ts`) **field for field** so the tree
round-trips through the server's importer (`loadNodeFromFs`):

- **Layout:** `<out>/<loc>.md` with `:` → path segments; the empty-loc root
  node is `README.md`; a node is `<seg>.md` *beside* its `<seg>/` child folder
  (stable path whether or not it has children). Empty/`.`/`..` loc segments are
  rejected (defense-in-depth around file writes), mirroring the server's guard.
- **Frontmatter** (exact key order): `name, id, alias, type, description,
  abstract, abstractOriginHash, contentHash, tags, seq, data, properties,
  nodes`. Edges are inline under `nodes:` as `{id, loc, rel, condition?,
  priority?}`. Omit-on-default matches the server (e.g. `type` omitted for the
  default `info`; `priority` omitted when `0`).
- **Round-trip invariant:** the importer upserts importer-consumed fields as
  `value ?? null`, so an omitted field is *actively nulled* on re-import — the
  omit rules here match the server's exactly so an export → import is lossless.

### `contentHash` is recomputed, not fetched

`contentHash` is load-bearing for round-trip but is a DB column the GraphQL API
**does not expose**. It's a deterministic fingerprint, so the CLI recomputes it
with the same algorithm as the server's `computeContentHash`
(`src/lib/contentHash.ts`): `sha256(content)` hex, first 8 chars; empty content
→ no hash. The emitted value is therefore identical to a server push.
(Pinned by `TestContentHashMatchesServer` against the `sha256("abc")` vector.)

## Fetch strategy — `nodeBatch` with chunking + spillover

`collectNodeBatch` (unit-testable; fetch is injected):

1. List every node id via `nodes(memory)`, paged to exhaustion (the server
   truncates an unbounded query to one default page — same #23 discipline the
   `spec` commands use). `data`-type nodes are partitioned out here (they carry
   no markdown body; the server's git export skips them too).
2. Fetch full nodes in fixed **200-id** chunks (`BATCH_READ_MAX_NODES`).
3. Honor the **1 MB response cap**: a `truncated: true` page returns the
   spillover ids in `omitted`, which are re-queued. The server always returns
   ≥1 node per call, so the backlog strictly shrinks and terminates; a
   zero-progress truncation is treated as an error rather than a hang.
4. Ids the server can't return come back in `unavailable` and are surfaced in
   the summary — never silently dropped.

## Root manifest

When no node owns the repo root (the server's "short, colon-free loc"
heuristic) and no `README.md` already exists, a `README.md` manifest
(`{urn, name, description, tags}` + a title/body) is synthesized from
`GetMemory`, mirroring step 6 of `pushMemoryToGit` so the export is
self-describing and importable.

## Schema + codegen

`make schema` re-exported the SDL from the sibling `../hadron-server` checkout.
The diff is **+32 lines, nothing removed** — exactly `nodeBatch` + its
`NodeBatchResult` type, no unrelated drift. New operation in
[nodes.graphql](internal/api/queries/nodes.graphql) selects every field the
exporter serializes (edges with `target { id loc }`, `condition`, `priority`);
`make generate` output is purely additive. `go.yaml.in/yaml/v3` (already an
indirect dep) is promoted to direct for frontmatter encoding (2-space indent to
match the server's writer).

## Package layout — `internal/cmd/memory/`

| File | Contents |
|---|---|
| `export.go` | `newCmdExport`; the `nodeBatch` fan-out, the serializer (`buildNodeFrontmatter`/`buildEdgeEntries`/`nodeFilePath`/`contentHash`/`decodeJSON`), the manifest, and `exportSummaryDTO`. |
| `memory.go` | wires `newCmdExport` into the group. |

## Tests

- `export_test.go` — golden render (full file shape + field order + framing),
  the `contentHash` vector, omit rules, edge projection, `nodeFilePath` /
  unsafe-loc rejection, `decodeJSON`, and `collectNodeBatch` (chunking,
  truncation re-queue, `unavailable` union, no-progress error, error/ nil-result
  propagation, empty input).
- `memory_export_cmd_test.go` — full command wiring against a fake GraphQL
  server: data-node skipping, correct ids batched, files at `<out>/<loc>.md`,
  edge + `type` rendering, omitted `priority: 0`, and the summary contract.

## Verification

- `make build`, `go test ./...`, `golangci-lint run`: green (0 issues).
- Live smoke against `hadronmemory.com:dev`: 85 nodes in ~0.4 s, 3 data nodes
  skipped, 6 `unavailable` reported; frontmatter, recomputed `contentHash`,
  nested `properties`, and inline `nodes:` edges all render faithfully.

## Known limitations / differences from the server push

- **Bounded by per-node read access.** A client-side export can only write what
  the read API returns. The `nodes` *listing* has looser visibility than the
  read resolvers, so a node can list but be unreadable (verified: 6 such nodes
  in `:dev`, also unreadable via `node get`). These are reported under
  `unavailable`. The server's `pushMemoryToGit` runs with full DB access and has
  no such gap.
- **Non-destructive.** Export overwrites node files but never deletes; a file
  for a node that no longer exists is left in place (the server's git path
  stages deletions via `git add -A`). A `--clean` flag could close this later.
- **Empty `{}`/`[]` `data`/`properties` are omitted** (omitempty), where a
  server push emits them. Cosmetic only — those fields aren't importer-consumed.

## Out of scope (follow-ups)

- A `--format json` (single-document / NDJSON) target — the flag is already in
  place for it.
- `--clean` (prune files for deleted nodes) and `--include-data`.
- Byte-identical YAML vs the JS `yaml` writer (long-scalar wrapping differs;
  values round-trip regardless).
