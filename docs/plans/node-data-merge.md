# `node update --data-merge` — shallow-merge a JSON patch into `data` (#92)

`node update --data`/`--data-file` **replaces** the whole `data` bag (omit to
preserve, `--data null` to clear). There was no way to set/overwrite a few
top-level keys while keeping the rest — you had to `node get`, merge
client-side, and `node update --data` the full object back, a read-modify-write
race. hadron-server shipped a server-side merge for exactly this
(hadron-server#366, `HADRON_SERVER_VERSION` 0.7.0):

- **GraphQL:** `updateNodeData(nodeId: ID!, data: JSON!): Node!`
- Merge contract: shallow merge, **patch wins** on top-level key collisions;
  unmentioned keys preserved; no prior data → patch taken verbatim; nested
  object values **replaced** (not deep-merged); a non-object patch (array /
  scalar / `null`) is rejected with `BAD_USER_INPUT`.

## Design — option 1: flags on `node update`

Per the issue, merge lives next to `--data` for discoverability rather than as a
separate subcommand:

- `--data-merge '<json>'` (`-` reads stdin) and `--data-merge-file <path>`.
- **Mutually exclusive with `--data`/`--data-file`** — replace and merge are
  different operations backed by different mutations, so combining them is a
  usage error.
- Composes with the other field flags (`--name`, `--content`, …): those still
  go through the `upsertNode` write; the merge is a second mutation that runs
  **last**, so its returned node is the rendered result. A merge-only update
  skips the upsert entirely.

The new `UpdateNodeData` query selects the same node projection `UpsertNode`
does, so `mergeDTO` maps to the same stable `--json` shape as `upsertDTO`.

Client-side validation is kept to **valid JSON** (mirroring `resolveData`);
object-only enforcement leans on the server's `BAD_USER_INPUT`, routed through
`api.MapError`. The node is resolved once via `fetchNode` — its id feeds the
merge, its memoryId/loc/name feed any upsert.

```sh
hadron node update acme.com:kb:findings:flaky-ci --data-merge '{"status":"closed"}'
cat patch.json | hadron node update findings:flaky-ci -m acme.com:kb --data-merge -
```

## Schema / codegen

`make schema` refreshed the committed snapshot from the sibling hadron-server
checkout (it was behind 0.7.0); `UpdateNodeData` was added to
`internal/api/queries/nodes.graphql` and `make generate` regenerated the client.

## Tests / docs

Command tests assert: `--data-merge` calls `updateNodeData` (never `upsertNode`)
with the resolved id + parsed patch; `--data-merge-file` reads from a file;
`--data` + `--data-merge` is a mutual-exclusion error before any round-trip; an
invalid patch is rejected. The `--data`/`--data-merge` help text and
`agentic-usage` spell out merge-vs-replace, mirroring the server's tool
descriptions.
