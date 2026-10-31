# Node, edge, asset and comment coverage (cli#1341)

Design as built for [hadron-cli#1341](https://github.com/hadron-memory/hadron-cli/issues/1341),
a child of the CLI coverage sweep (#1334, cor:api:240:02). Gate: Xabat CHECKED
(issuecomment-6540743958) with the addendum (issuecomment-6540762632): the
`allocateSpecNode` row belongs to cli#398, so this PR covers the other 47
`gap #1341` rows. Every rule about who may do what stays in the server
(cor:api:240:01); the CLI carries values and maps refusals.

## Surfaces

| Server field | CLI |
|---|---|
| `updateNodeProperties` | `node set-properties <ref> (--patch <json> \| --patch-file <path>) [--reason <text>] [-m <memory>]` |
| `edge` | `edge get <edge-id-or-urn>` |
| `edges` (+ every `EdgeFilter` field, `orgId`, paging) | `edge list` with **no node argument**: `[-m <memory>] [--source <node>] [--target <node>] [--runnable[=false]] [--include-deleted] [--org <org>] [--limit N] [--offset N]` |
| `assets` (+ every `AssetFilter` field, `orgId`, paging) | `asset list --all [-m <memory>]... [--org <org>] [--mine] [--mime <type>] [--scan-status <s>] [--include-deleted] [--limit N] [--offset N]` |
| `deleteCommentThread` | `comment delete <comment-ref> --thread --reason <text> [--yes]` |
| `createNode`/`create{Spec,Review,Task}Node` `input.edges.{loc,description,isRunnable}` | `node add --edge <name>=<target>` or `--edge '<json>'` (keys `target`, `name`, `loc`, `description`, `runnable`), repeatable |
| `update{,Spec,Review,Task}Node` `input.edges.{…}` | `node update --edge … --replace-edges` |
| `create*Node(input.id)` | `node add --id <id>` |
| `deleteNode(cascadeCrossMemoryEdges)` | `node rm --hard --cascade-cross-memory-edges` |
| `findNodes(expand, fields, granularity, minScore)` | `search --expand N --field <f>... --granularity node\|chunk --min-score <x>` |
| `findNodes(filter.createdBy, includeDeleted, memoryClasses, role, updatedAfter, updatedBefore)` | `node list --created-by --include-deleted --memory-class... --role --updated-after --updated-before` |
| `nodeExport(sections)` | `node export --section <s>...` (presentation render, `full: false`) |
| `beginAssetUploadV2(input.uploadRequestId)` | `asset upload --agent-request <id>` (intent `AGENT_REQUEST`) |
| `node(memoryRef)` | proposed **excluded `not-a-capability`** (below) |

### Decisions

- **The doors share one edge builder.** `node add`/`node update` already route
  through the kind's door (`api.CreateNodeByKind` / `UpdateNodeByKind`), so one
  `--edge` parser that fills `NodeEdgeInput` covers all eight node-write doors.
  `--edge` takes the shorthand `<name>=<target>` or a JSON object; the target is
  any form `NodeEdgeInput.targetId` accepts (id, node URN, loc in the node's
  memory). An empty `--edge`, an unknown JSON key, or a JSON edge without
  `target` is exit 2, before anything is sent.
- **`node update --edge` replaces.** `updateNode(edges:)` replaces the node's
  whole outgoing edge set (findings:createnode-edges-is-atomic). So `--edge` on
  update is refused without `--replace-edges`, and `--replace-edges` needs at
  least one `--edge`; `edge add` / `edge rm` stay the way to change one edge.
- **`edge list` without a node lists edges.** With a node it is unchanged (both
  directions of one node, same `--json`). With no node it pages the uniform
  `edges` list to exhaustion (#23) unless `--limit`/`--offset` asks for one
  page, and prints `{total, edges}`; the node-only filters (`--direction`,
  `--name`, `--to`, `--from`) need a node, and the list-only flags need none.
- **`asset list --all` is the cross-memory list.** Without `--all`, `-m` is still
  required and the per-memory list is unchanged. With `--all`, `-m` becomes an
  optional, repeatable narrowing (`filter.memoryRefs`); `--org` narrows to an
  organization (cor:api:100:01, a non-member gets an empty list).
- **`comment delete --thread` needs `--reason`.** The server requires it; the
  CLI asks for confirmation on a terminal and `--yes` otherwise, as for a single
  comment. Who may delete a thread (a memory admin) is the server's rule.
- **`node rm --cascade-cross-memory-edges` needs `--hard`.** The server reads it
  only on a hard delete, so without `--hard` it would be silently ignored.
- **`search --granularity chunk` prints passages.** Chunk granularity returns
  `passages` (vector mode only, a server rule); text and `--json` show them
  alongside the hits.
- **`node export --section` is a presentation render.** It sends `full: false`
  with the sections; the output is not round-trippable through `node import`,
  and `--format pdf` is refused with it.
- **`uploadRequestId` is not a retry key.** The issue called it "a retry-safe
  upload id"; on hadron-server main it is required only for upload intent
  `AGENT_REQUEST` and is kept with the upload ticket. `asset upload
  --agent-request <id>` sends that intent and id; help says no more than that.

### Proposed exclusion: `Query.node(memoryRef)`

`node(ref: <bare loc>, memoryRef: <memory>)` reads the same node as
`node(ref: <full node URN>)`, and the SDL documents the two as equivalent
(including the system-memory read-through). The CLI's `-m <memory> <loc>`
composes the URN (`cmdutil.ResolveNodeRef`) and reaches every node `memoryRef`
reaches, so no capability is missing. Needs the specs engineer's acceptance.

## Tests

`internal/cmd/node_edge_asset_1341_test.go`, committed red first: one fake-GraphQL
test per surface asserting the operation is sent with the variables (and, for
each refusal, that nothing is sent).
