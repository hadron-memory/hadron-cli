# Node move plan from the server (#741, the rest)

**As built.** `hadron node move … --dry-run` calls the server planner `planNodeMove` (cor:api:190:03; hadron-server#3424, merged as #3566 at 560347d3) and renders its plan. It replaces the advisory read of the first slice ([741-node-move-advisory.md](741-node-move-advisory.md), #1638); it does not extend it (Patrick 6627908793, Vera 6410232737).

## Why

The first slice could only list what the caller can read, because predicting a move's outcome belongs to the server (cor:api:240:01). The planner runs the real move's own resolution, gates and checks without writing, so the CLI can now show the move's actual shape and refusals with no client-side rules.

## What the CLI does

- Validates the destination flags exactly as the real move does (`relocationDestination`), resolves the source with the same helper (`cmdutil.ResolveNodeRef`, so a bare `<loc>` with `-m` works), then sends one `PlanNodeMove` with the same selector as `MoveNode`. Optional destination variables and `$limit` are omitted when unset, so the server sees exactly one destination and applies its own default limit.
- `--limit` passes straight through (the server clamps it; the CLI copies no bound). It is refused without `--dry-run`, or below 1, before any request.
- **Refusals** are the move's: the server raises them, `api.MapError` maps them, and the CLI exits with the same code the real move would (for example `NODE_ALREADY_EXISTS` exit 5, `BAD_USER_INPUT` exit 2, `NODE_NOT_FOUND` exit 4). No plan is printed.
- **Truncation:** the plan is COMPLETE only when the server says it is not truncated and it lists every node it counts. Otherwise it is printed with `TRUNCATED: the plan lists N of M nodes` and the command exits 1. A short list is never shown as the whole subtree. In `--json`, `truncated` is that verdict (always `!complete`), not a copy of the server's flag, so the three fields always agree (cli#2320). `--limit` is refused locally above 2147483647, the GraphQL Int range; every value from 1 to 2147483647 is sent for the server to bound (cli#2320).
  - **The hint never promises a limit the server may clamp** (fix for Gabriel's BLIND P2): it suggests a larger `--limit` and says the server bounds it.
  - When the server lists fewer nodes than the `--limit` sent, that shortfall is its bound, and the error says a plan cannot list this subtree in full. The CLI does not copy the server's 1000 bound (built, not ruled).
  - A subtree beyond the bound can never plan COMPLETE; it exits 1 by design.
- **`--limit`:** refused below 1 before any request (shape, not a server rule).
- **Link warnings:** listed as the server reports them; "withheld" when `linkWarningsWithheld` (the caller can't read the destination, ruling 0211), and never "none" in that case. JSON keeps the server's distinction: `null` when withheld, `[]` when there are none.
- **Point in time:** the text says the plan reserves nothing and a change before the move can still end it with a conflict.

## Contract change

The `--json` shape of `node move --dry-run` changes (released in v0.37.0 as the advisory inventory): `{status, complete, totalCount, truncated, pointInTime, nodes[{id,currentUrn,newUrn}], linkWarnings[{nodeId,nodeLoc,raw,status,target}]|null, linkWarningsWithheld}`. `status` and `complete` keep their meaning; `advisory`, `message`, `source`, `destination` and `notPredicted` are gone, as the replacement intends. The exit code stays 1 for a plan that does not cover the subtree. Normal moves are unchanged.

## Removed

`internal/api/queries/move_preview.graphql` (four read operations) and `internal/cmd/node/move_preview.go` (the paged subtree walk). Nothing else used them.

## Tests

`internal/cmd/node_move_plan_741_test.go`, red first on main (6e7419f2): exactly one `PlanNodeMove` and no other operation besides ref resolution; `--to-urn`/`--to-memory` omit the other destination; `--limit` sent only when set; text and JSON for a complete plan, listed and withheld link warnings, a truncated plan (exit 1, counts, hint) and four refusals (exit code of the move, server message, no plan printed); `--limit` without `--dry-run` refused with zero requests; bare `<loc>` with `-m`.

## Verified against production

Before building: `planNodeMove` is in the committed schema snapshot, and production answers it. A probe with a non-existent source returned `Node not found`, not an unknown-field error. No real move plan was run against production data.
