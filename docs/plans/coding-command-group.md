# Implementation Plan: `hadron coding` — lint the review checklist tree and the preflight router

> **Status: proposed — not yet implemented.** This is a design-ahead doc for
> [#325](https://github.com/hadron-memory/hadron-cli/issues/325), written after
> auditing the live memories. It exists to settle the open questions *before*
> code, because the surface as filed produces a >50% false-positive rate. The
> resolutions in [Decisions](#decisions-resolved-against-live-data) are the part
> that needs review.

## Context

The `review:*` checklist trees and the `preflight` routers are executable
infrastructure. `tasks:review-changes` triages checks by reading each one's
`Applies when …` edge label back to the `review` parent; `preflight` routes
symptom → finding along its outgoing edges. When an edge label is malformed the
check is **silently skipped** — the node still exists, still looks maintained,
and never fires again. There is no mechanical detector today.

`hadron spec lint` is the prior art: the same idea for the spec corpus, with a
finding DTO, per-rule severities, and a documented exit code. This mirrors it
for the coding-workflow graph.

## What the live audit actually found

Everything below was measured against the live memories with the current
binary; the numbers drive the decisions that follow.

| Memory | Inbound edges on `review` | Genuine defects |
|---|---|---|
| `hadronmemory.com::hadron-portal` | 25 | **0** — all 25 carry a real `Applies when …` condition |
| `micromentor.org::mmdata` | 54 | **4** |

The portal's "7 of 24" from the issue **does not reproduce**; it appears to have
been fixed between the audit and filing. The mmdata four are real and confirmed:

| Node | Label | Defect |
|---|---|---|
| `review:posthog-backend-vs-app-event-routing` | `child-of` | not a condition |
| `review:role-vs-group-ident-vocabulary` | `child-of` | not a condition |
| `review:input-type-graphql-type` | *(empty)* | no label at all |
| `review:format-sources` | `Applies when Dart sources change` | foreign toolchain — lives in the TypeScript backend memory |

Two further facts shaped the design:

- **`preflight` in mmdata has 71 outgoing routes, 24 of them labelled the generic
  `routes-to`** rather than an action phrase — a third of the router fails the
  proposed convention check on day one.
- **The `review` tree contains a dangling edge.** An inbound edge points at
  `tasks:build-review-coverage`, which is absent from the node listing *and*
  unreadable (`node get` → exit 4, not found). This is the list-vs-read
  visibility gap CLAUDE.md warns about, live in the data this command must scan.

## Decisions (resolved against live data)

### 1. Membership rule — which nodes are checklist items

**This is the load-bearing decision, and the issue does not specify it.** Its
scope is "every node under the `review` parent", but the `review` parent
legitimately has non-checklist neighbours: the router, the tasks that consume the
tree, a meta backlog, pattern nodes. Implementing the issue's checks verbatim
gives **9 findings on mmdata, of which 5 are false positives** — a linter that
cries wolf on more than half its output will not be adopted.

A loc-prefix rule does not fix it either: `review:backlog` sits under `review:`
and is explicitly meta.

The node metadata separates them cleanly:

| Node | `review` tag | `meta` tag | `isRunnable` | is a check? |
|---|---|---|---|---|
| `review:thin-resolver-field` | ✅ | — | false | ✅ |
| `review:input-type-graphql-type` | ✅ | — | false | ✅ |
| `review:backlog` | ✅ | ✅ | false | ❌ meta |
| `tasks:review-changes` | — | — | **true** | ❌ task |
| `patterns:function-signatures` | — | — | false | ❌ pattern |

**Decision — a node is a checklist item iff it has the `review` tag, does *not*
have the `meta` tag, and is not `isRunnable`.**

Validated end to end on mmdata's 54 inbound edges:

```
members (checklist items): 31
excluded (meta/task/other): 22
unreadable:                  1

findings among members:
  [EMPTY LABEL]     review:input-type-graphql-type            ''
  [NOT-A-CONDITION] review:posthog-backend-vs-app-event-routing 'child-of'
  [NOT-A-CONDITION] review:role-vs-group-ident-vocabulary       'child-of'
```

**3 findings, all real, zero false positives** — versus 9/5 for the unscoped
rule. The five would-be false positives (`patterns:function-signatures`,
`preflight`, `review:backlog`, `tasks:review-changes`,
`tasks:update-review-parent-node`) are all correctly excluded.

`review:format-sources` is correctly *not* flagged by the label-shape rules — it
needs the toolchain heuristic, which stays warn-only as filed.

### 2. "Label isn't a loc" → "label is empty"

The issue's example, `review:input-type-graphql-type:rel:review`, is that edge's
**`loc`**, not its **`name`**. The name is empty:

```json
{ "name": "", "loc": "review:input-type-graphql-type:rel:review",
  "otherNodeLoc": "review:input-type-graphql-type" }
```

Edge locs are auto-derived by slugifying the name — confirmed on the healthy
ones (`name='Applies when adding or modifying a GraphQL resolver…'` →
`loc=review:thin-resolver-field:Applies-when-adding-or-modifying-a-GraphQL-resolver-f`).
So `<source>:rel:<target>` is the **derived-loc fallback when the name is
empty**: a symptom, not an independent defect class. There is exactly **one** such
edge, so the "one bad batch of 5" reading does not hold either.

**Decision — check `name` for empty/missing (error). Drop the loc-shape rule;
mention a `:rel:` loc in the finding message as corroboration only.**

### 3. New check: the edge target must resolve

Not in the issue for `review lint` (only for `preflight lint`), but the live
`review` tree has one (`tasks:build-review-coverage`, above).

**Decision — add `target-resolves` as an error, and follow CLAUDE.md: a target
that lists but cannot be read is reported as `unavailable`, never silently
dropped.**

### 4. Preflight action-phrasing is a warning, not an error

24 of 71 mmdata routes fail it immediately. As an error the command is red until
someone relabels 24 edges, which trains people to ignore it. `spec lint` already
treats convention violations as warnings and shape violations as errors.

**Decision — `route-label-phrasing` is a warning; `route-target-resolves` is an
error. `--strict` promotes warnings, as in `spec lint`.**

### 5. `--fix` uses `edge update`; server#835 is not a dependency

The issue's ⚠️ hazard is an MCP-side limitation. This repo is not exposed to it:
`hadron edge update` already exists ([`internal/cmd/edge/update.go`](../../internal/cmd/edge/update.go),
wired at `edge.go:54`) over the `UpdateEdge` mutation
([`nodes.graphql:507`](../../internal/api/queries/nodes.graphql)), whose optional
variables all carry `# @genqlient(omitempty: true)`. One edge, one field, no
whole-node write, sibling `documented-by` / `relates-to` edges untouched.

**Decision — `--fix` calls `updateEdge` per edge and must never route through
`updateNode(edges:)`. hadron-memory/hadron-server#835 is not a blocker.**

## Command surface

```
hadron coding <command>

  review lint    [-m <org::memory>] [--json] [--strict] [--fix] [--yes]
  preflight lint [-m <org::memory>] [--json] [--strict]
```

`coding` as the parent leaves room for `coding review ls|add` (the
`tasks:add-review-node` procedure) and `coding preflight ls` later. Both
subcommands take `-m/--memory` like every other group.

## Check catalogue

### `coding review lint` — over members only (Decision 1)

| Rule | Severity | Catches |
|---|---|---|
| `parent-edge-exists` | error | check invisible to `tasks:review-changes` |
| `label-present` | error | the empty label (Decision 2) |
| `label-is-condition` | error | `child-of`, `applies-when`, `related`, bare `Applies when` |
| `target-resolves` | error | dangling / unreadable target (Decision 3) |
| `description-has-trigger` | warning | second blind spot in `find_nodes` output |
| `duplicate-trigger` | warning | cloned check never re-pointed |
| `seq-unique` | warning | non-deterministic sibling ordering |
| `foreign-toolchain` | warning | the misfiled `format-sources` |

### `coding preflight lint`

| Rule | Severity |
|---|---|
| `route-target-resolves` | error |
| `route-label-phrasing` | warning (Decision 4) |
| `route-target-retired` | warning |
| `route-target-moved-memory` | warning |

### `--fix` scope

Mechanical subset only: **promote the node's description sentence into the edge
label** when the description carries a trigger and the label doesn't. That
covers the empty-label and `child-of`-with-a-good-description cases. Anything
whose description has no condition either is reported for a human. Gated like
other bulk writes — prompt on a TTY, `--yes` non-interactively.

## Package layout — `internal/cmd/coding/`

| File | Contents |
|---|---|
| `coding.go` | group root; `-m` resolution; shared DTOs |
| `review_lint.go` | `newCmdReviewLint`; the pure rule engine over an in-memory model |
| `preflight_lint.go` | `newCmdPreflightLint` + its rule engine |
| `membership.go` | the Decision-1 predicate, isolated and unit-testable |
| `fix.go` | `--fix` planner + `updateEdge` application |

Wired in [`internal/cmd/root.go`](../../internal/cmd/root.go) alongside the other
groups. Rule engines take plain structs and injected fetch functions, so they
unit-test without a server — the pattern `spec/lint.go` uses.

## GraphQL changes

**None.** Every operation needed already exists: `GetNode` (returns a node's
edges with ids, direction, name, loc — what `edge ls` uses), the node listing
(`loc`, `tags`, `isRunnable`, `seq`, `nodeType`), `NodeBatch` for bulk reads, and
`UpdateEdge` for `--fix`. No `make generate`, no schema refresh.

## Output and exit contract

- Mirror `lintFindingDTO` (`spec/lint.go`) — node loc, rule, severity, message —
  as an explicit DTO in the command package, slices initialised to `[]T{}`.
- Render via `output.Write` with an `output.NewTable` human branch.
- **Exit `5` (`exitcode.Conflict`) via `exitcode.Silent`** when any error-severity
  finding is present, matching `spec lint` (`lint.go:199`). The issue says
  "non-zero"; the existing convention is specifically 5. Warnings alone exit 0
  unless `--strict`.
- Adding these leaves means updating the embedded
  [`agentic-usage.md`](../../internal/cmd/agentic/agentic-usage.md) in the same
  PR, or `TestAgenticUsageDocumentsEveryCommand` fails the build.
- **Paginate.** Per CLAUDE.md and #23 an unbounded `nodes` query silently returns
  one page, so the memory sweep uses `scanAllNodes`/`paginateNodes`, not a single
  call.

## Tests

- **Pure-logic unit tests** (`internal/cmd/coding/*_test.go`): the membership
  predicate against the five node shapes in Decision 1; each label rule
  (`child-of`, `applies-when`, empty, bare stem, valid); the `:rel:` loc as
  corroboration but not a rule; duplicate-trigger and seq-uniqueness; the
  toolchain heuristic both directions.
- **Command/wiring tests** (`internal/cmd/coding_cmd_test.go`, via
  `testFactory` + `fakeGraphQL`/`captureGraphQL`): findings → exit 5; warnings
  only → exit 0; `--strict` promotion; `--json` shape; an unreadable target
  reported as `unavailable` rather than dropped; `--fix` asserted to issue
  `UpdateEdge` per edge and **never** `UpdateNode` (the regression that would
  destroy sibling edges); `--fix` requires `--yes` non-interactively.
- **Read-only live smoke test** against `micromentor.org::mmdata` and
  `hadronmemory.com::hadron-portal`: expect 3 errors + the `format-sources`
  warning on the former, clean on the latter.

## Out of scope (follow-ups)

- `coding review ls|add`, `coding preflight ls` — the surface leaves room.
- Coding-guidelines cross-link linting (mentioned in the issue's rationale, no
  checks specified).
- `--fix` for anything beyond description→label promotion.
- Auto-repair of dangling targets; the linter reports, a human decides whether
  the target moved or the edge is stale.
- CI workflow wiring for memory hygiene — `--json` + exit 5 make it possible;
  choosing which repos gate on it is a separate call.
