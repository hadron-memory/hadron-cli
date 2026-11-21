# CLI coverage matrix

Design as built for #1334 (the full coverage sweep). It also builds the mapping
layer #66's gate asks for (CHECKED T1, 2026-10-23). Snapshot:
`schema/schema.graphql` is hadron-server main 94d33f45. Re-exporting the SDL at
that commit leaves the committed snapshot unchanged.

## What it is

`internal/api/coverage-matrix.txt` replaces `internal/api/unbound-ops.txt`. It
maps every Query and Mutation field to one of:

- `command <path>`: a command reaches it;
- `gap #<issue>`: nothing reaches it yet, and the issue tracks it;
- `excluded <class>: <reason>`: deliberately left out. The classes are the
  scanner's five accepted labels: `internal`, `host-protocol`, `deprecated`,
  `not-a-capability` and `graphical`. Graphical exceptions excuse only the view.
  The [Open Questions in cor:api:240:02](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:api:240:02)
  (read at revision 14) leave acceptance of the non-graphical classes to Holger;
  a syntactically accepted label is not a ruling.

Two more sections go below the operation level:

- **Arguments**: each argument or input field that a reached operation never
  passes, listed at the shallowest unreached path;
- **Filters**: every direct field of a reached list query's `*Filter` input,
  with `flag <path> --<flag>` naming the flags that set it (or a positional
  argument such as `[query]`).

There is no parity claim. While any line says `gap`, the CLI does not cover the
platform.

## Reached is derived, not claimed

`scripts/unboundops` (the existing scanner, extended rather than duplicated)
derives what the CLI reaches. It then writes the matrix, carrying the
hand-written dispositions across a regeneration:

- **Root field**: an operation document selects it, AND production code calls
  that operation's generated function. Production code means `./cmd/...` and
  `./internal/...`, type-checked with `golang.org/x/tools/go/packages`, without
  tests and without `internal/api/gen`. A document that nobody calls binds a
  field but doesn't reach it. `query Me` is the live example: it has no caller,
  so `me` is not reached.
- **Argument path**: a reached operation passes it. An object literal in the
  document passes exactly the fields it spells out. Below a variable, Go
  decides: a field is reached when production code names the genqlient struct
  field (a keyed literal, an assignment or a read). Decoding JSON straight into
  the struct also reaches it, which covers the bound `NodeWhereInput`. Input
  types bound in `genqlient.yaml` map back to their GraphQL names.
- An alias selects the same field again. The field's own name is reached, with
  the union of the arguments.
- A recursive input is walked once per type per path, so the walk ends.
- A field the server marks `@deprecated`, and the CLI doesn't reach, gets the
  line `excluded deprecated: @deprecated: <the server's reason>` unless a
  hand-written line already exists.

## What the check refuses

`make coverage-matrix-check` runs in CI (`make unbound-ops-check` is kept as an
alias). It fails when any of these is true:

- the file is stale: the schema or the code changed what is reached;
- a line is `UNCLASSIFIED`. That covers a new operation, a new argument and a
  new filter field on a query the CLI already reaches;
- a disposition contradicts the facts: a `gap` or exclusion on a field a
  command now reaches, a `command` on a field nothing calls, a `flag` on a
  filter field the code never sets, or a `command` or `flag` in the wrong
  section;
- a line is a duplicate or an orphan (its field left the schema). The
  regenerated file drops orphans;
- a line is malformed. The grammar is strict, so a typo can't pass as a
  disposition, and an exclusion class outside the five is refused.

`TestCoverageMatrixNamesRealCommands` (in `internal/cmd`, which has the
command tree) fails on a dangling `command` or `flag`: a path that isn't a
command, a flag the command doesn't have, or a positional argument its `Use`
line doesn't spell. A positive control shows that each kind fails.

## Known limits

- The `command` named for a reached field is checked to exist, not to call that
  operation. The first mapping was drafted from the static call graph (each
  command's RunE closure, followed through direct calls to the generated
  functions), then corrected by hand. Reviewers should spot-check it.
- A filter field that the code sets for its own use, with no flag (for example
  `findNodes(filter.role)`, set by the spec commands), may stay a `gap`. Only a
  `flag` claim is checked against the facts in Filters.
- Field reach is per input type, not per call site. A field set for one
  operation counts for every operation that shares the input type.
- Returned fields that no command selects (for example `AppKey.scopes`) are out
  of the maintained matrix. #1334 asked for them as analysis, and the child
  issues name the ones that matter. Tracking them is follow-up work if wanted.
- `hadron api` sends any GraphQL and is not counted as coverage.

## Result at 94d33f45

- 500 root fields: 328 reached by a command, 149 gaps, 23 excluded (326 and 151
  before #1348 wired `app key revoke` and `app key rotate`).
- 189 argument lines (170 gaps, 19 server-deprecated arguments excluded).
- 39 filter fields (30 flags, 9 gaps).

The gaps are filed by command group (#1335–#1347) or folded into existing
issues (#58, #60, #61, #63, #64, #65, #198, #206, #215, #230, #231, #275, #811,
#828, #878, #890, #911, #913, #933, #936, #1010, #1043, #1181, #1304,
#1333). Every exclusion is listed in the matrix with its reason, for the specs
engineer's check against cor:api:240:02.

## Residual reconciliation (2026-11-21, #1334)

Two independent source baselines were used: CLI main
`acb0bb95f593a69a1afce130cccf9a3d14089a56` and its committed
`schema/schema.graphql` for the matrix counts below; server main
`ef26d5856de6c33f99e4b29a5e9af2bee6875f75` only for the MCP/HTTP-to-GraphQL
source audit. The committed schema is not a complete inventory of that server
revision: for example, `answerQuestion`, `retractQuestionAnswer` and
`deleteAgentRevision` are absent from the matrix. Counts and reconciled gap
pointers therefore apply only to the committed schema, not to all current
server GraphQL capabilities. This is a dated audit, not a promise that issue
states or counts stay current. The matrix's reachability
scanner and offline guard are unchanged. No schema refresh or command build is
part of this reconciliation.

| Section | Total | Command / flag | Gap | Excluded |
|---|---:|---:|---:|---:|
| Query | 237 | 203 | 8 | 26 |
| Mutation | 359 | 328 | 19 | 12 |
| Arguments | 37 | 0 | 16 | 21 |
| Filters | 76 | 68 | 3 | 5 |

The 46 gap rows name 16 open issues at this audit. Pointers use the most specific
open owning child: App-agent sessions #2510, git push #2513, ticker instruction
#2773, team/host/wake instruction #2774, and snapshot-grant filters #2775. The
parents remain trackers. Closed duplicate #1880's four rows now name #1846.
Closed-stale #890's unbuilt residuals now have fresh tracking children #2788
(org grants and paged subscription reads) and #2789 (personal subscription
revoke, including its memory-deletion side effect). Closed #684's still-unreached
`importNode(input.authoredWith)` now names #2790; this does not reopen its
declined scaffolding. Filing those children is not a gate or a build dispatch.

Compared MCP registrations and Express routes against server `94d33f45`. The
22 added MCP tools all call existing GraphQL resolvers (Agent boot prompt,
automation read/snapshot grants, grant create, Questions, secret metadata,
content validation and web polls). The 71 removed team tools are disabled in
code, not inferred from a principal's tool list.

Twenty new HTTP registration expressions include constant/template paths, not
just literal strings. Eighteen are the node-signing, grant-signing and person
secret-reveal browser protocols; GraphQL exposes their request/read/withdraw
entry points and the server-built person reveal URL. Assertion completion stays
on the browser ceremony, as the SDL requires, just as OAuth follow-on protocol
was excluded from GraphQL by the original audit. One is mailbox browser-bound
confirmation after the existing GraphQL connection start. One is operational
`GET /ready`. No added MCP/HTTP capability lacking a GraphQL surface was found in this
bounded comparison. This does not establish CLI reachability for those
GraphQL operations or completeness of the committed schema. No
assertion-bearing GraphQL endpoint is proposed. These are static source checks,
not live-deployment or user-journey validation. The issue thread records the complete tool-to-resolver map, route-family
inventory and issue-state table. [Vera's row check](https://github.com/hadron-memory/hadron-cli/issues/1334#issuecomment-7056961507)
and [confirmation](https://github.com/hadron-memory/hadron-cli/issues/1334#issuecomment-7057026765)
cover the 33-row exclusion delta and the checkpoint/retirement corrections.

The optional network issue-liveness guard was not added: offline CI continues
to check scanner/file consistency without calling GitHub. No new guard means
no red-first behavioral test is needed for this bookkeeping change. There is
still no global parity claim while any row is a gap.

### Scope of the Proposed annotations

`Proposed class unruled under #1334 (33-row review)` annotates the 32
non-graphical rows in the 33-row exclusion delta from `0a9024df` reviewed at
`acb0bb95`. The graphical `dashboardWidgetData` row is the remaining member of
that delta and carries no unruled marker. The annotations record this audit's
scope; unmarked historical rows are not a ruling or a verdict that their class
is decided. All non-graphical classes still have the open question cited above.

The five retired attention surfaces keep the scanner's existing `deprecated`
label, with reasons explicitly saying retired API and no replacement. Vera's
confirmation accepts that format: the label is not a claim of a replacement or
a new exception ruling. Likewise the distinct `checkpointNode` input is marked
as a Proposed host-plane exclusion, not withdrawn or equivalent to `checkpoint`.
