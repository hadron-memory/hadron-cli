# Implementation Plan: `hadron memory extract`

> **Status: implemented and verified** on this branch; reflects the design as
> built. GH issue
> [#228](https://github.com/hadron-memory/hadron-cli/issues/228), following
> server change hadron-server#637. Part of the CLI⟷portal parity epic
> [#67](https://github.com/hadron-memory/hadron-cli/issues/67) / OSS
> feature-completeness [#134](https://github.com/hadron-memory/hadron-cli/issues/134).

## Context

hadron-server#637 added a mutation that turns a node subtree into its own
memory, rebasing the parent to the new memory's root:

```graphql
extractParentNodeToMemory(parentRef: ID!, targetUrn: String!, move: Boolean = false): Memory!
```

The subtree is loc-prefix defined (the parent's loc plus every live descendant);
locs are rebased so the parent becomes the root (`findings:auth` → the memory
slug, `findings:auth:oauth` → `<slug>:oauth`). `move = false` (default) COPIES
the subtree, leaving the source intact; `move = true` relocates it,
soft-deleting the source subtree. The new memory preserves the source's class.
This is a sibling of `cloneMemory` — a whole-memory copy — so the CLI surface
mirrors `hadron memory clone`.

## Command surface (as built)

```
hadron memory extract <parentRef> <targetUrn> [--move] [-m <org::memory>]
```

- **`<parentRef>`** — the parent node's id or a fully-qualified
  `<org>::<memory>::<loc>` URN, or a bare `<loc>` with `-m/--memory`. Resolved
  to a node id via `cmdutil.ResolveNodeRef` (same path as `node get`/`move`), so
  a malformed ref is a usage error (exit 2) and a genuinely-absent node is
  not-found (exit 4) before the mutation runs. Resolving client-side also hands
  the server a plain id it always accepts.
- **`<targetUrn>`** — a fully-qualified `<org>::<slug>` memory URN naming the new
  memory; its org may differ from the source's. A value without `::` is rejected
  client-side (mirrors `memory clone`'s `--target-urn` gate); the server does the
  full validation.
- **`--move`** — relocate (soft-delete the source subtree) instead of copying.
  Default is copy. The server default is `false`, applied server-side.

## `--json` shape

Marshals the package-local `memoryDTO` (id, urn, name, shortDescription, class,
visibility, organizationId, isEncrypted, maxRevCount, updatedAt) via
`output.Write` — the same DTO `memory clone`/`get` emit, decoupled from the
genqlient types so a regeneration can't reshape the contract. The human branch
prints `✓ extracted (copied|moved) <parentRef> → <new-urn>`.

## GraphQL / codegen

- Added the `ExtractParentNodeToMemory` mutation in
  `internal/api/queries/memories.graphql` (beside `CloneMemory`); `$move` is
  `# @genqlient(omitempty: true)` and bound `*bool`, so an unset flag is omitted
  (never sent as `null`/`false`), preserving the server-side default.
- `schema/schema.graphql` re-exported from the server via `make schema` (added
  only the `extractParentNodeToMemory` field + doc block — no unrelated drift).

## Tests

`internal/cmd/commands_test.go` (beside the clone tests): copy path (parentRef
resolved to a node id, `move` omitted from the wire, `--json` carries the new
memory URN), the `--move` + bare-loc-via-`-m` path (asserts the canonicalized
`ResolveUrn` input, `move: true` on the wire, and "moved" in the human output),
and the relative-`targetUrn` rejection. Agentic-usage contract updated (surface
line + prose).

## Not covered / follow-up

The optional `hadron_extract_parent_node` MCP tool (issue #228's follow-up) is
left to hadron-server's MCP surface, matching how the other node-write
delegations live server-side.
