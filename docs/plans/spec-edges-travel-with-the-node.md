# `spec new` / `spec extract`: a spec node's edges travel with it (#687)

## The defect

A customer PM (hadron-server#1300) ran `hadron spec new` and ended up holding
two **orphaned** spec nodes that no CLI command could attach:

1. `spec new` created the node with `createSpecNode`, then wired its
   table-of-contents and inheritance edges with **separate** `createEdge`
   calls.
2. `createEdge` refused each one with a bare `Forbidden`.
3. The command exited 1 and told her to wire the edges with `hadron edge add`.
   That is the same `createEdge`, so it failed the same way, and so did
   `spec link`.

Holger's first read was that the recent spec-node protection (#1201) was to
blame. The server source at `main` `f98539c` points somewhere else. GraphQL
`createEdge` / `updateEdge` / `deleteEdge` open with
`requireRole(ctx, 'CONTRIBUTOR')`. `ctx.roles` is the user's **platform** role
(`User.roles`, default `[READER]`), not their role on the memory. No node
mutation carries that gate, and MCP `hadron_create_edge` gates on memory access
instead. See `hrn:node:hadronmemory.com:hadron-cli:findings:graphql-create-edge-gates-on-platform-role`.
Whether that gate is right is the server's call, in #1300.

The CLI half is independent of that call. **A command that creates a node and
then wires it in a second request can always leave an orphan**, whatever makes
the second request fail.

## The fix

`CreateNodeInput.edges` is already accepted by `createSpecNode`. The server
writes the node and its OUTGOING edges in **one transaction**. That path checks
source-memory write access and target readability; it has no platform-role
gate. Every edge the create commands make is outgoing from a node they are
creating:

| command | node | its inline edges |
|---|---|---|
| `spec new` | the new spec | ToC → parent, inherits → tier contract |
| `spec new` (root) | the co-created contract | ToC → the root, by the id just returned |
| `spec new --new-path` | each node in the chain | ToC / inherits → an earlier node (by its returned id) or an existing one |
| `spec extract` | the new spec | ToC, inherits, cross-ref → the source (by the id already read) |

So all of those now send `edges` on `createSpecNode`, and never call `createEdge`.

**Every target is resolved before anything is written** (`resolveSpecEdges`).
Previously a target that didn't resolve was skipped after the node existed,
which orphaned it. Now it refuses the command, **nothing is created**, and the
target's own exit code (4) comes through. Targets travel by **id**, never by
loc: the server never re-resolves them, and a node created moments earlier (by
the same command) is wired by the id it returned, since `resolveUrn` can lag.

## What remains partial, and says so

A command that creates **several** nodes still writes each one separately:
a root and its co-created contract, or a `--new-path` chain. A failure part-way
names the nodes already created, and each of them is complete with its edges.
The how-to shows how to create only the missing contract. Making several nodes
one transaction would need a server batch-create door, and that is not proposed
here.

## Not in this change: waiting on hadron-server#1300

These add an edge to an **existing** node, so they cannot travel inline on a
create:

- `spec link` (between two existing specs)
- `spec supersede`'s `superseded-by` edge (old → new)
- repairing orphans already stranded

`updateSpecNode.edges` **replaces the whole outgoing set**. Adding one edge
through it would be read-modify-write over a wholesale replace, which this
client does not build. The next slice follows whichever contract #1300 settles:
a memory-scoped `createEdge` gate, a spec edge door, or both. The dead-end
`hadron edge add` advice on those paths is replaced then, against a remedy that
actually works.

## Specs

No platform-spec change. This is client behaviour over an existing server
capability (inline edges on the kind's create door, which `coding review add`
already uses through `createReviewNode`). No concept crosses a repo boundary.
The open server questions are routed to #1300, with Vera and Holger for the
contract call.
