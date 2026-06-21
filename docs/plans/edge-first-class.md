# Adapt the CLI to first-class edges (spec 037, #74)

hadron-server made `Edge` a first-class, addressable entity (spec 037, server
#306, merged to `main`): `label` → optional `name`, plus a required `loc` (the
edge's identity and the suffix of `hrn:edge:<org>::<memory>::<loc>`), and new
`description`/`isRunnable`. The GraphQL/MCP contract the CLI codes against moved
in lockstep, so the CLI adapts.

## ⚠️ Deploy sequencing

This is a **wire-breaking** change — the CLI now sends `name` (not `label`) to
`createEdge`/`updateEdge` and selects `name loc isRunnable` on edges. **It must
land after the server deploys #306.** Against an old server it would fail. The
server change is on `main`; coordinate the merge with the deploy.

## What changed

- **Schema snapshot** (`schema/schema.graphql`): `Edge`, `createEdge`,
  `updateEdge`, `NodeEdgeInput`, `EdgeInput` updated to the spec-037 shape,
  copied from the server's `origin/main` typedefs (the edge-only delta — the
  rest of the snapshot is untouched to keep the diff focused).
- **Operations** (`nodes.graphql`): `$label`→`$name` (+ optional
  `loc`/`description`/`isRunnable`, all `omitempty` so unset args are omitted,
  not sent as `null` — #263 redux); edge selections `label` → `name loc
  isRunnable`. Regenerated.
- **`edge add` / `edge update`**: `--label` → `--name`, plus `--loc` /
  `--description` / `--runnable`.
- **Display** (`edge ls`/`add`/`update`, `node get`, `spec get`): the
  relationship is `name ?? loc` (a nameless edge shows its loc) —
  `cmdutil.EdgeDisplay`.
- **`spec link` / `spec new` / `spec extract` / `spec supersede`** and node
  `import --with-edges`: set the edge **name** (was label); idempotent re-import
  keys on `(target, name)`.
- **nodedoc**: `Edge.Label` → `Name` (the markdown `rel` key is unchanged — it
  already meant the relationship; the `--format json` document key moves
  `label`→`name`).
- **`--json` DTOs**: edge shapes carry `name`/`loc`/`isRunnable` (was `label`).

## Not in scope

The `hrn:edge:` URN parser + golden set the issue mentions is a server/portal/
docs concern — the CLI has no edge-URN parser (edges are addressed by id), and
`cmdutil.ResolveNodeURN` already accepts any `hrn:`/`urn:` prefix generically
(#239). The public reference doc (hadron-docs) is updated separately.
