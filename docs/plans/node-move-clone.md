# Implementation Plan: `hadron node move` / `hadron node clone`

> **Status: implemented and verified** on this branch; reflects the design as
> built. GH issue
> [#188](https://github.com/hadron-memory/hadron-cli/issues/188), following
> server change hadron-server#564 (merged as #565). Part of the CLI⟷portal
> parity epic [#67](https://github.com/hadron-memory/hadron-cli/issues/67);
> portal follow-up is hadron-portal#501.

## Context

hadron-server#564 reshaped `moveNode` onto the `<object>Ref` convention (#542)
and added a sibling `cloneNode`. The old `moveNode(from, to, memoryRef,
targetMemoryRef)` — a bare node.update in disguise — was never surfaced by the
CLI, so there was no `hadron node move` to migrate; this adds both commands
fresh against the new signature.

New server shape (both mutations):

```graphql
moveNode(sourceRef: ID!, targetUrn: String, targetMemoryRef: ID): Node!
cloneNode(sourceRef: ID!, targetUrn: String, targetMemoryRef: ID): Node!
```

`sourceRef` is the node's id or full URN; the destination is **exactly one** of
`targetUrn` (a full destination node URN) or `targetMemoryRef` (a destination
memory, same loc). Both/neither → `BAD_USER_INPUT`. `move` keeps the node's id
(edges stay valid); `clone` returns a **new** node (fresh id) and copies only
the outgoing edges that naturally resolve at the destination.

## Command surface (as built)

```
hadron node move  <node-urn> | <loc> -m <memory> (--to-urn <urn> | --to-memory <memory>)
hadron node clone <node-urn> | <loc> -m <memory> (--to-urn <urn> | --to-memory <memory>)
```

- **Source** — a fully-qualified `<org>::<memory>::<loc>` URN, or a bare `<loc>`
  with `-m/--memory`. Resolved to a node id via `cmdutil.ResolveNodeRef` (same
  path as `node get`/`update`), so a bad form is a usage error (exit 2) and a
  genuinely-absent node is not-found (exit 4).
- **Destination** — exactly one of:
  - `--to-urn <org>::<memory>::<loc>` — normalized to canonical `hrn:node:` form
    via `cmdutil.CanonicalNodeURN` (the no-network half of `ResolveNodeURN`; the
    destination need not exist yet, so it is *not* resolved).
  - `--to-memory <org::memory>` — normalized via `cmdutil.CanonicalMemoryRef`.
  - Neither / both → usage error (exit 2) before any network call. The server
    enforces the same rule; the client check is the friendlier error.
- **No overwrite gate.** Unlike `node import` (an upsert), move/clone never
  clobber: the server fails loudly with `NODE_ALREADY_EXISTS` when a live node
  occupies the destination loc. So no `--yes` gate — the failure is the guard.

## `--json` shape

Both marshal the package-local `nodeDTO` (id, memoryId, loc, name, nodeType,
tags, seq, isRunnable, updatedAt) via `output.Write`, identical to
`node add`/`update` — decoupled from the genqlient types so a regeneration can't
reshape the contract. `clone`'s DTO carries the **new** node's fresh id.

## GraphQL / codegen

- Added `MoveNode` / `CloneNode` mutations in
  `internal/api/queries/nodes.graphql`; `$targetUrn` / `$targetMemoryRef` are
  `# @genqlient(omitempty: true)` so an unset destination is omitted, never sent
  as `null` (the server reads `null` as a value, breaking exactly-one).
- `schema/schema.graphql` re-exported from the server via `make schema` (also
  picked up the previously-lagging `authContext` additions from #562/#563; no
  generated-code impact — no CLI operation selects them).

## Tests

`internal/cmd/node_move_clone_cmd_test.go`: move to-urn / to-memory, clone
to-urn, the exactly-one-destination guard (neither/both), bare-loc source
resolution via `-m`, and assertions that the unset destination var is *absent*
from the wire (omitempty) and that bare refs are canonicalized before send.
