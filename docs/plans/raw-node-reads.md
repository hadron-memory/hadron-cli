# Design as built: raw node reads (#736)

> **Status: built.** Written 2026-09-26 by Jonas, from Vera's report in team
> chat #1987 and the finding
> `hrn:node:hadronmemory.com:hadron-cli:findings:node-get-renders-mustache-so-cli-round-trips-strip-placeholders`.

## What was verified before changing anything

The single-node GraphQL read, `node(ref:, raw:)`, **compiles Mustache** unless
`raw: true` (hadron-server `resolvers.query.node.ts`: `if (!raw &&
node.content?.includes('{{'))`). `nextNode` does the same. `nodeBatch` and
`nodeExport` never render. So `node export`, `memory export` and every batch
read already return stored text. Jane measured both reads on production, read
only (team chat #2130): `node(ref:)` returned 0 `{{`, and `node(ref:, raw:
true)` returned 2.

Every CLI caller of `GetNode` (the rendered read) was inventoried:

| Caller | Uses the content? | Writes it back? | Verdict |
|---|---|---|---|
| `coding preflight create`, `coding preflight route` (via `appendRoutingLine`) | yes: splices a routing line into the router body | **yes** | **data loss: fixed** |
| `node update`, `node import` | kind only (role, runnable) | no | safe |
| `coding review create`, `edge ls`, `node import --with-edges`, `chat post --reply-to`, `coding` lint/get | ids, edges, seq, or display | no | safe |
| `spec supersede`, `spec extract --strip-source` | yes | yes | Jane's cli#742 |
| `spec edit` | yes | yes | already raw since cli#740 (`GetSpecNodeRaw`) |
| `node get` (single ref) | display | no, but its output is what an editor copies | `--raw` added |

## Design

1. **`GetNodeRaw` shares `GetNodeNode`.** It has the same selection with `raw:
   true`, and both operations carry `@genqlient(typename: "GetNodeNode")`.
   Every existing caller and signature is unchanged, so the spec package, where
   Jane's #742 is live, needed no edit. genqlient **refuses to generate** if the
   two selections ever diverge (measured: dropping one field fails with
   "conflicting definition for GetNodeNode"), so a field added to one and
   forgotten in the other is a build error, not a silent gap.
2. **Every CLI read-modify-write reads raw.** `appendRoutingLine` and both
   planning reads switch to `GetNodeRaw`. The plan is validated against the same
   stored text the write splices into.
3. **`node get --raw`, and a `rendered` field.** The default single-ref read
   stays rendered, following the issue's instruction not to silently break
   deliberate rendering users. `--raw` selects the stored body. `--json` gains
   `rendered` on every node object: `true` only for a compiled single-ref read,
   `false` for `--raw`, batch and `--prefix` reads. It is always present,
   additive, and an old reader ignores it. The human render marks a stored body
   ("content: stored body ({{…}} placeholders not compiled)").
4. **Guidance.** `node get --help`, `node update --help` and `agentic-usage.md`
   say: to edit content, read it with `--raw`.
5. **The single-ref not-found message** now reads "not found, or not readable
   by you", per `cor:api:140:03` (a node in a memory you can't open answers as
   a missing one).

## Not in this change

- **`spec get` / `spec lint` read rendered** through `fetchSpecNode`. Neither
  writes back, but a `spec get` body copied into `spec edit --content-file`
  would carry the loss. The `spec` group is its own surface, and Jane holds
  #742 there, so this is raised for routing rather than changed here.
- **`coding` reads that display content** (review and preflight bodies shown to
  an agent) stay rendered. That is what the platform executes, and nothing
  writes it back.

## Verification

- `TestCodingPreflightCreateKeepsRouterPlaceholders`: the router update sends
  `{{name}}`, `{{role}}` and an absent-variable placeholder literally, asserted
  on the mutation's variables, not a re-read.
- `TestNodeRawReadEditWriteRoundTripKeepsPlaceholders`: `node get --raw --json`,
  then edit, then `node update --content-file`. The update carries every
  placeholder.
- `node get` tests for `--raw` (it reads `GetNodeRaw`, `rendered: false`), the
  default (rendered, `rendered: true`), a batch (`rendered: false` per node),
  and the human marker.
- `TestGetNodeRawAsksForTheStoredBody` asserts the operation document itself
  carries `raw: true`, since the command fakes answer by operation name and
  cannot see it.
- **Mutation-checked:** each of these fails a test, and each mutant compiles:
  - reverting any of the three preflight reads to `GetNode`;
  - dropping `raw: true` (regenerated);
  - ignoring `--raw`;
  - claiming `rendered: true`;
  - inverting the human marker;
  - marking batch nodes as rendered.
- No production writes. Every write path is exercised against fakes.
