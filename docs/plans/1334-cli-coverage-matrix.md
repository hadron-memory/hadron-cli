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
  ones cor:api:240:02 and Holger's 2026-10-30 ruling allow: `internal`,
  `host-protocol`, `deprecated`, `not-a-capability` and `graphical`.

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

- 500 root fields: 326 reached by a command, 151 gaps, 23 excluded.
- 189 argument lines (170 gaps, 19 server-deprecated arguments excluded).
- 39 filter fields (30 flags, 9 gaps).

The gaps are filed by command group (#1335–#1347) or folded into existing
issues (#58, #60, #61, #63, #64, #65, #198, #206, #215, #230, #231, #275, #811,
#828, #878, #890, #911, #913, #933, #936, #1010, #1043, #1181, #1304,
#1333). Every exclusion is listed in the matrix with its reason, for the specs
engineer's check against cor:api:240:02.
