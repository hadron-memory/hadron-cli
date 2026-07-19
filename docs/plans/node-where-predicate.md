# `--where` / `--object-type` / `--sort-property` — structured property queries on `node ls` + `search` (#265)

The server's unified `findNodes` grew a structured, faceted retrieval surface
that the CLI couldn't reach — so property/attribute queries like "insights from
substack in the last two weeks" had no CLI path. This closes that parity gap
(#134) across both node-retrieval front doors. Three merged server stacks back it:

- **`NodeFilter.where: NodeWhereInput`** (hadron-server #719 / #727 #729 #730 #731)
  — a recursive predicate over a node's `properties`/`data` JSONB. A **leaf** is a
  `path` plus exactly one operator (`eq|ne|in|lt|lte|gt|gte|between|exists|contains`),
  with `field` (`properties`|`data`, default `properties`) and `as`
  (`text`|`number`|`datetime`|`boolean`, default `text`). A **branch** is `and`/`or`
  (lists) or `not`. Bounded (depth ≤ 4, ≤ 32 leaves, path ≤ 8); a malformed/oversized
  tree is `BAD_USER_INPUT`. Composes with every mode (keyword/regex/vector/hybrid)
  and the no-query browse — #731 dropped the earlier "not composable with
  vector/hybrid" restriction, so there is **no** client-side rejection of that combo.
- **`NodeFilter.objectType: String`** (hadron-server #725 / #734) — a collection
  discriminator (e.g. `competitor`, `insight`), orthogonal to `nodeType`.
- **`findNodes(sortProperty: NodePropertySort)`** (hadron-server #739) — orders by
  the value at a `properties`/`data` JSON path; reuses the `field`/`as` enums plus
  `direction` (`asc`|`desc`). Missing/unparseable values sort last; overrides the
  `sort` enum when present.

## Design — raw-JSON, grammar parity

`--where` and `--sort-property` take **raw JSON** whose keys are the GraphQL input
field names verbatim, so the v1 surface is exact grammar parity with the server
(ergonomic sugar like `--prop k=v` or `--since` can layer on later without breaking
it). `--object-type` is a plain string flag. All three land on both `hadron search`
(ranked) and `hadron node ls` (browse) — `where`/`objectType` via `NodeFilter`,
`sortProperty` as a top-level `findNodes` arg.

The JSON parses straight into the input structs (`cmdutil.ParseNodeWhere` /
`cmdutil.ParseNodePropertySort`), which is why the wire semantics matter:

> **Omit-vs-null is load-bearing here.** The server's leaf validation counts any
> operator key that is `!== undefined` — an explicit `null` included — so a nil
> operator serialized as `null` would trip "a leaf must carry exactly one
> operator". Every optional field must therefore be **omitted** when unset, never
> sent as null. Same contract the `NodeInput` mutations follow.

`NodeWhereInput` is **recursive** (`and`/`or`/`not` reference it), and genqlient's
per-field `omitempty` resolution for a self-referential input type is
**non-deterministic** — the `,omitempty` tag flips between codegen runs, which
both breaks the contract above and reds CI's codegen-freshness gate. So
`NodeWhereInput` / `NodePropertySort` (+ the `NodeWhereColumn` / `NodeWhereCast` /
`SortDirection` enums) are **bound** (genqlient.yaml `bindings`) to hand-authored
structs in `internal/api/gqltypes`, which pin the json tags. `NodeFilter.objectType`
/ `where` stay generated but need their `omitempty` `for:` directive repeated
across **all three** ops that reference `NodeFilter` (nodes/search/chat) — genqlient
resolves a shared input type's field-level omitempty with last-writer-wins, so a
partial set on one op strips it non-deterministically (the convention already
documented in `chat.graphql`).

Client-side validation is deliberately thin: **well-formed JSON + unknown-field
rejection** (`json.Decoder.DisallowUnknownFields`, so a typo like `"equals"` fails
loudly with exit 2 instead of silently dropping). Deep validation (depth, leaf
arity, path shape, the where+mode composition rules) is the server's job and
surfaces as `BAD_USER_INPUT` verbatim through `api.MapError`.

```sh
hadron node ls -m acme.com::kb --object-type insight \
  --where '{"and":[{"path":["source"],"eq":"substack"},{"path":["capturedAt"],"as":"datetime","gte":"2026-07-04"}]}' \
  --sort-property '{"path":["capturedAt"],"as":"datetime","direction":"desc"}' --json
hadron search "pricing" --object-type competitor --where '{"path":["tier"],"eq":"enterprise"}'
```

## Schema / codegen

The committed snapshot was hand-extended (rather than a full `make schema`, which
would drag in unrelated server SDL drift) with `NodeWhereInput` + `NodeWhereColumn`
/ `NodeWhereCast` / `SortDirection` enums + `NodePropertySort`, the `where` /
`objectType` fields on `NodeFilter`, and the `sortProperty` arg on `findNodes`;
`make generate` regenerated the client. The `FindNodes`/`SearchNodes` operations
gained the `$sortProperty` variable; the `api.FindNodes`/`api.SearchNodes` wrappers
gained a `sortProperty *gqltypes.NodePropertySort` param (existing non-target
callers pass `nil`). See the "Schema / codegen" note above on the bound
`gqltypes` package and the three-way `NodeFilter` omitempty consistency.

## Tests / docs

Command tests assert: the predicate reaches `filter.where` verbatim, `--object-type`
lands on `filter.objectType`, `sortProperty` is a top-level arg, unset leaf
operators are **omitted** from the raw wire body (the omit-vs-null contract), and a
malformed/unknown-field predicate is a client-side usage error before any
round-trip. Help text on both commands and the `agentic-usage` synopsis + example
document the leaf/branch grammar and the enums.
