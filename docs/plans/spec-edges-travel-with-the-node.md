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

## Slice 2: edges added to an EXISTING node

hadron-server#1300 Q1 was ruled yes (Holger, team chat #1306). server#1307 drops
the platform gate, so `createEdge` is memory-gated like everything else and
additive again. On that basis:

- **`spec link` needs no change.** It is a plain `createEdge`. It works for a
  default-role user once #1307 is deployed, and it is how the two existing
  orphans get repaired. No customer node is edited by us.
- **`spec supersede`'s structural edges travel inline** on the replacement's
  `createSpecNode`, resolved first. The replacement can no longer be left
  orphaned from the tree, and the `skipped` edge status, which only that state
  produced, is gone.
- **The `superseded-by` edge stays a separate `createEdge`**, because it leaves
  the OLD node. If it errors, supersede **re-reads the old spec before
  prescribing anything** (Codex on #691): a lost response looks exactly like a
  refusal, and `spec link` over an edge that landed would fail, while a blind
  rerun over one that didn't would mint a second replacement. An edge that
  landed finishes the run. One confirmed absent gets
  `hadron spec link <old> <new> -m <mem> --label superseded-by`, after which
  rerunning `spec supersede` takes its existing finish-the-retirement path. An
  unverifiable one reports status `unknown` (Copilot: `failed` would be a claim
  the run cannot make) and says to check with `spec get` first.
- **The re-read runs after EVERY superseded-by write, not only a failed one**
  (Codex P1, Copilot on #691). Two supersedes that pick different replacements
  both create successfully, since edge identity includes the target, and both
  used to retire the old spec. Now a run retires only when its replacement is
  the **sole** successor; otherwise it exits 5 and prescribes no write. This
  narrows a race that predates this change without closing it. Both runs can
  no longer retire, because each re-reads after its own write, so the later
  writer always sees the earlier one's edge. What remains: the earlier run may
  retire before the later one writes, and the later run's edge then stays
  behind next to it, reported as a conflict for a human to remove. **Refusing
  that second write needs a server-side conditional retirement**, reported to
  the coordinator as a hadron-server item.
- **`spec lint`'s inheritance-edge remedy was a command that could not run.**
  It said `hadron edge add … --label`, and the flag is `--name`, so it exited
  `unknown flag: --label`. It now names `spec link` when both ends carry the
  `spec` tag (`spec link` refuses any that doesn't), else `edge add … --name`.

Both remedies are tested by RUNNING them: the test takes the command from the
message and executes it through the CLI. A string assertion on the message
could not see `--label`, and it was not seeing it.

**Rejected: `updateSpecNode` with the full outgoing edge set** (the CLI-only
route before Q1). `UpdateNodeInput` has no revision precondition, so
concurrent links lose edges. The replace also runs `pendingEdge.deleteMany`,
so it destroys pending edges, and edges to targets the caller cannot read,
neither of which the client can see to resend.

## Specs

No platform-spec change. This is client behaviour over an existing server
capability (inline edges on the kind's create door, which `coding review add`
already uses through `createReviewNode`). No concept crosses a repo boundary.
The open server questions are routed to #1300, with Vera and Holger for the
contract call.
