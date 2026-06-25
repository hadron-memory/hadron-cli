# `isRunnable` read/write parity for `node` (#89)

`Node.isRunnable` is the predicate `hadron task run` gates on — but the CLI
could neither **read** it (`node get`/`node ls` omitted the field) nor **write**
it (no flag on `node update`/`node create`). You could author a `task`-typed,
`runnable`-tagged node yet have no CLI way to see or set the one field that
actually decides runnability, forcing a drop to MCP `hadron_update_node`. A
concrete parity hole under the #67 epic.

## Read

`isRunnable` is now selected on the `Nodes` and `GetNodeById` operations (and on
the `UpsertNode`/`UpdateNodeData` mutation projections, so the write commands'
output reflects the resulting state). It's surfaced as `nodeDTO.IsRunnable`
(`json:"isRunnable"`):

- `node get` prints a `runnable: <bool>` line in the text view and the field in
  `--json`.
- `node ls` adds a compact `RUN` column (✓ for runnable) and the `isRunnable`
  field in `--json`.

The server's `isRunnable` is a nullable `Boolean`; a null is coerced to `false`
via the `boolVal` helper (the server treats a null as "not runnable", matching
the existing edge-runnable handling in `edgeRefOf`).

## Write

A tri-state `--runnable` bool flag on `node update` and `node create`, wired to
`NodeInput.isRunnable`:

- **`node update`** — `--runnable` sets true, `--runnable=false` clears it,
  **omitting it preserves** the current value. The tri-state is gated on
  `cmd.Flags().Changed("runnable")`, and `NodeInput.isRunnable` carries the
  `# @genqlient(... omitempty: true)` annotation so a nil pointer is omitted
  from the wire (server reads omitted as "preserve", not "clear") — the same
  preserve/clear discipline the other optional `NodeInput` fields follow.
- **`node create`** — `--runnable` sets it on the new node (omitted → server
  default).

## Tests / docs

Command tests assert: `node get`/`node ls` surface `isRunnable` in text and
`--json`; `node update --runnable` sends `true`, `--runnable=false` sends
`false`, and an omitted flag keeps `isRunnable` off the wire; `node create
--runnable` forwards it. `agentic-usage` documents the read fields and the
tri-state write flag.

## Follow-up — `node ls --runnable` filter

The original change surfaced `isRunnable` on the listing but couldn't filter on
it. The server's `nodes(isRunnable:)` arg (added after the first schema refresh)
lets us push the predicate down: `node ls --runnable` returns only runnable
nodes, `--runnable=false` only the explicitly non-runnable, and omitting it
constrains nothing. Same tri-state plumbing as the write flag — gated on
`Changed("runnable")` with an `omitempty` query variable, so a default-false
bool never silently hides the many NULL-`isRunnable` nodes. This is the listing
counterpart to `hadron task run`'s gate (find the tasks you can run in one
query). Required a `make schema` refresh to pull in the new `nodes(isRunnable:)`
argument; a passthrough test locks the omitted/true/false wire behavior.
