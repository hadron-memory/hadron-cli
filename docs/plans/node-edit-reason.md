# `--reason` for CLI node edits — version-history parity with MCP (#88)

MCP `hadron_update_node` accepts a `reason` that's recorded in a node's version
history; the CLI had no equivalent, so every CLI edit landed in the version log
with no rationale — less traceable than MCP-driven edits (#67/#66 parity).

## Server dependency

The CLI writes through GraphQL, and the `upsertNode` / `updateNodeData` /
`searchReplaceInNodes` resolvers hard-coded `editedBy: clientId/userId` — there
was no `reason` on the GraphQL surface (MCP bypasses GraphQL and writes the
`NodeVersion` row directly). So this required a server change first:
**hadron-server#380** adds an optional `reason: String` to `NodeInput`,
`updateNodeData`, and `SearchReplaceInNodesInput`, and writes
`editedBy: reason ?? ctx.clientId ?? ctx.userId ?? null` — the same column and
fallback MCP uses. This CLI PR consumes that and **must merge after** it.

## CLI change

- Schema snapshot refreshed from hadron-server (picks up `reason` on the three
  inputs); `make generate` regenerated the client (`UpdateNodeData` gains a
  `reason` param; `NodeInput.reason` is `omitempty`).
- `--reason "<text>"` added to **`node update`** and **`replace text`**:
  - `node update` resolves `reasonPtr` once (nil when the flag is unset) and
    attaches it to whichever mutation runs — the `upsertNode` input (field
    updates / `--data` replace) and/or the `updateNodeData` call (`--data-merge`).
  - `replace text` sets it on `SearchReplaceInNodesInput`.
- An omitted `--reason` is left off the wire (`NodeInput.reason` omitempty; the
  merge/replace paths only set it when non-empty), so the server falls back to
  the caller identity.
- **Not** added to `node create`: only an update snapshots a prior version, so a
  reason on a pure create has nowhere to go (server-side no-op).

## Tests

`node update --reason` forwards `reason` on the upsert input; `node update
--data-merge --reason` forwards it on `updateNodeData`; an omitted `--reason` is
absent from the upsert input; `replace text --reason` forwards it on the input.
`agentic-usage` documents the flag.
