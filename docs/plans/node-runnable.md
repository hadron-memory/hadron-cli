# `isRunnable` read/write parity for node commands (#89)

`Node.isRunnable` is the predicate `hadron task` / `h-run-task` gates on (it
checks the field, **not** `nodeType`), yet the CLI could neither read nor write
it: `node get`/`node ls --json` omitted the field, and `node update`/`node
create` had no flag for it. Authors had to drop to MCP `hadron_update_node {
isRunnable }` or raw GraphQL — a real parity hole vs MCP (#67 epic). This closes
it on both the read and write side.

## Design — surface the existing server field

The server already models all three surfaces (no server change needed; the
committed schema snapshot was just stale and is refreshed via `make schema`):

- `Node.isRunnable: Boolean` — read it
- `NodeInput.isRunnable: Boolean` — write it through the existing `upsertNode`
  path the CLI already uses for `node create`/`update`
- `Query.nodes(isRunnable: Boolean)` — filter a listing by it

`isRunnable` is **tri-state**: most nodes leave it NULL ("unset"), distinct from
an explicit `false`. So it stays a `*bool` end to end — in the `nodeDTO`/
`nodeDetailDTO` `--json` shape (renders `true`/`false`/`null`) and on the wire.

### Read

- `GetNodeById` and `Nodes` queries select `isRunnable`; both DTOs carry it.
- `node get --json` always includes `isRunnable`; the text view prints a
  `runnable: <bool>` line **only when set** (matches how empty description/tags
  are omitted — absence reads as "unset/NULL").
- `node ls --json` includes the field. The human table is left unchanged (a
  mostly-NULL column would be noise on every listing).

### Write

- `node update` / `node create` gain `--runnable` (a bool flag with
  `NoOptDefVal = "true"`, so `--runnable` ≡ `--runnable=true`). Tri-state:
  `--runnable[=true]` sets true, `--runnable=false` sets false, **omit
  preserves**. The flag reaches the wire only when `cmd.Flags().Changed`, and
  `NodeInput.isRunnable` carries `# @genqlient(omitempty: true)` so a nil
  pointer is omitted (server reads omitted = preserve), per the existing
  wire-semantics contract in `nodes.graphql`.
- `node ls --runnable[=false]` passes the value through to the
  `nodes(isRunnable:)` filter; omitting it constrains nothing.

```sh
hadron node create -m acme:mm --loc tasks:post-comment --name "Post comment" --runnable
hadron node update tasks:post-comment -m acme:mm --runnable=false
hadron node get    tasks:post-comment -m acme:mm --json | jq .isRunnable   # true|false|null
hadron node ls     -m acme:mm --runnable --json
```

## Tests / docs

Command tests cover the tri-state on the wire for `update` (set true / set
false / omitted-is-absent), `create --runnable`, the `ls --runnable` filter
passthrough, and that `node get` surfaces the field in both `--json` and the
text view. `agentic-usage` documents the flag and the read surfaces. Schema
snapshot + generated client refreshed with `make schema` (genqlient adds the
`nodes(isRunnable:)` arg and `NodeInput.isRunnable`).
