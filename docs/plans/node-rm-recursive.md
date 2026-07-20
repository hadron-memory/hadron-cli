# `node rm --recursive` — delete a node's whole subtree (#239)

hadron-server#661 added `recursive: Boolean` to the `deleteNode` mutation and,
with it, a **behavior change**: a plain (non-recursive) delete of a node that has
descendants now **refuses** with a typed `NODE_HAS_DESCENDANTS` error (carrying
the count) instead of silently orphaning the subtree — parity with the
`hadron_delete_node` MCP tool. The CLI's `node rm` couldn't reach either half.

## GraphQL op

`deleteNode` in the committed schema snapshot already carried `recursive` (a prior
`make schema`), so only the `DeleteNode` **operation** needed the `$recursive`
variable, `@genqlient(omitempty)` so an unset/false value is omitted (server
default = non-recursive), then regenerate. `gen.DeleteNode` gains a trailing
`recursive *bool` (its only caller is `node rm`).

## Flag + prompt

`--recursive` / `-r` on `node rm`, threaded to `gen.DeleteNode` with the same
tri-state omit pattern as `--hard` (only sent when true). The confirmation target
names the blast radius — a recursive delete adds "and its entire subtree (all
descendant nodes)", and a recursive **hard** delete (the most destructive form)
spells out "HARD: permanently removes every node in the subtree, their edges, and
version history". The success line and `--json` gain an "and its subtree" /
`recursive: true` marker.

## Refusal → actionable CLI error

`NODE_HAS_DESCENDANTS` isn't a code `api.MapError` knows (it'd fall through to a
generic error with the server's raw *"pass recursive: true"* GraphQL wording,
against the node's opaque PK). `node rm` intercepts it instead:
`api.HasErrorCode` detects it, a new `api.DescendantCount` pulls `extensions.count`
(JSON numbers arrive as `float64`), and the CLI emits a usage error (exit 2)
`"<loc>" has N descendant(s); pass --recursive to delete the whole subtree` — with
the node's **loc**, not the PK, and `--recursive --hard` when the refused delete
was itself `--hard`, preserving intent.

## Tests

`--recursive` sends `recursive:true` and omits it when unset (tri-state);
`NODE_HAS_DESCENDANTS` surfaces the count + loc + `--recursive` (never the raw
`recursive: true` wording) as a usage error, and keeps `--hard` in the hint;
the recursive+hard confirmation target names the subtree + HARD; and a
`DescendantCount` unit test covers the `float64`/missing/wrong-code cases.
