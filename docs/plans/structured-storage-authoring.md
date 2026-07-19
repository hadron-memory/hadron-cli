# `memory set --schema` + `node --object-type` / `--properties` — author structured storage from the CLI (#268)

The write/authoring half of structured-storage CLI parity (#134). The read/query
half shipped in #265 (`--where` / `--object-type` / `--sort-property` on `search`
/ `node list`); together they make the server's #719/#725 structured-storage flow
doable CLI-only (it unblocks the hadron-docs market-research-agent tutorial,
hadron-docs#162, which had to drop to raw GraphQL). Three server surfaces back it,
all already merged:

- **`Memory.schema`** (hadron-server #725 / #735) — an opt-in per-memory property
  schema (declared collections + typed fields). Settable via `updateMemory(schema:)`
  (createMemory has none). NULL = unstructured.
- **`Node.objectType`** (hadron-server #725 / #734) — the collection discriminator
  (e.g. `competitor`), orthogonal to `nodeType`. On `createNode`/`updateNode` input.
- **`Node.properties`** (pre-existing column, schema-governed by #725) — the typed
  JSONB bag `where`/`sortProperty` target by default, distinct from `Node.data`.
  On `createNode`/`updateNode` input.

## `memory set`

- `--schema '<json>'` / `--schema-file <path>` set `Memory.schema`. `""` or `"null"`
  clears it (JSON null on the wire); an unset flag is omitted (preserve). Malformed
  JSON is a client-side usage error (exit 2); the server validates the schema *shape*
  and returns `BAD_USER_INPUT`, surfaced verbatim through `api.MapError`.
- **createMemory has no schema arg**, so on create the schema is applied in a
  follow-up `updateMemory` against the just-created memory — reusing the existing
  post-create `--slug` rename path (both now flow through one follow-up call). A
  follow-up failure is reported as an honest partial write (exit non-zero), naming
  which of slug/schema didn't apply.

## `node create` / `node update`

- `--object-type '<name>'` — the collection discriminator. On `update`, `--object-type ""`
  sends an explicit empty string (server normalizes → null, clearing it → ordinary
  node); omitting the flag preserves (tri-state, like `--runnable`).
- `--properties '<json>'` / `--properties-file <path>` — **REPLACE** the typed
  properties bag (server preserves an omitted field, overwrites a supplied one);
  `null` clears. Exactly mirrors the `--data` / `--data-file` flag set. The
  `resolveData` helper was generalized to `resolveJSONObject(flag, inline, file)`
  and now backs both `--data` and `--properties` (data and properties stay separate
  columns).

### `--properties-merge` deferred (no server mutation)

The issue asked for `--properties-merge` mirroring `--data-merge`, but `--data-merge`
is backed by the dedicated server-side `updateNodeData` mutation built specifically
to avoid a read-modify-write race (see `node-data-merge.md`). There is **no
`updateNodeProperties` mutation**, so a client-side merge would reintroduce exactly
that race. Per an explicit product decision, this PR ships REPLACE only and defers
merge to a future server `updateNodeProperties` mutation (filed as a hadron-server
issue). The `node update` help and `agentic-usage` call this out.

## Read-back

`node get` now selects + surfaces `objectType` and `properties`, and `memory get`
surfaces `schema` (text + `--json`), so what you author is verifiable CLI-only.
`node ls` already carries `--object-type` from #265. Export/import file-format
round-tripping of `objectType` (nodedoc) is a separate concern, out of scope here.

## Schema / codegen

Hand-extended the committed snapshot (avoiding a full `make schema` drift) with
`objectType` on the `Node` type + `CreateNodeInput`/`UpdateNodeInput`, `schema` on
the `Memory` type + `updateMemory`, plus the `@genqlient(omitempty)` directives and
the `$schema` variable on `UpdateMemory`; `node get`/`memory get` selections gained
`objectType`/`properties`/`schema`. Regenerated deterministically. The `api`/`gen`
`UpdateMemory` signature gained a trailing `schema *json.RawMessage` (the two other
callers — `spec describe`, the post-create follow-up — pass `nil`/the resolved arg).
The node-import upsert-emulation mapper (`updateNodeInputFrom`) maps the new
`ObjectType` field (enforced by `TestUpdateNodeInputFromMapsAllFields`).

## Tests

`internal/cmd/structured_storage_cmd_test.go`: node create/update send
`objectType` + `properties` (distinct from `data`); `--object-type ""` clears via an
explicit empty string; malformed/`--properties`+`--properties-file` are usage errors
before any round-trip; a schema-conformance `BAD_USER_INPUT` surfaces verbatim.
`memory set --schema` threads the JSON into `updateMemory`; `"null"`/`""` send an
explicit `schema:null`; malformed is a usage error; a create with `--schema` applies
it in the follow-up; the server's schema rejection surfaces verbatim.
