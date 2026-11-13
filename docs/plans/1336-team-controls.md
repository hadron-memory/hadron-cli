# Team App controls and filters (#1336)

Built on main b66c373f, targeting committed server schema; no schema refresh.
Nine remaining coverage rows are reached. Instruction arguments on revokeAppGrant
and setAppTickerSettings remain gap #1617 (Marco6849341566); grantApp is person-only.
No platform contract or spec amendment: this client exposes existing server rules.

Surfaces: team grant add/rm <worker-ref> <name> (rm confirms); team ticker settings set;
team worker held --user; experimental team register pick/candidates; role update
--repo / --clear-repos; task list --creator; team register attendee --agent/--worker.
The existing team grant action-grant reader/request workflow stays separate from
named App grants, identified in help. All output contracts are explicit DTOs.

Writes use ordinary authenticated caller. No manager/role authorization is computed
in the client. No instruction is sent. Refusals map through api.MapError. New
commands expose no credential values. Ticker flags parse GraphQL number shape only;
server owns limits and relationships. Each omitted setting is omitted on the wire;
clearWakeDailyBudget is a distinct Boolean with explicit false preserved.

Legacy task-list documents remain unchanged without --creator. Creator has a new
document that forwards all other supported filters. --team rejects --creator,
because filtering before the roster recursion would change the meaning of that view.
Role repos have a separate write document: --clear-repos sends [], omission preserves.
Repo affinity is advisory and never inferred. Register read projections preserve
server row order and nullable values; neither picker assigns or delivers.
heldWorkers empty output gives no cause, preserving missing/no-holds/unreadable equivalence.

Red first:9bfd1b9d fake GraphQL coverage pins all nine surfaces on unmodified main.
Checks and exact-head independent LOCAL/BLIND/security/compiled-CLI QA receipts are
recorded on the PR; fixtures are not proof of a live server deployment.

Expected request counts: held/pick/candidates/attendee each use one narrow data
operation, plus one best-effort App-label read for App-scoped human output (none
for JSON, none for cross-App held). Grants and ticker settings send one mutation,
plus the same human-only label decoration. Grant rm confirmation may read that
label before the write. No prompt/body/transcript fields are fetched. Default
list pages20 (held/candidates); profile/route/capability scalars only, normally
kilobytes rather than whole worker briefings. Role repo update reuses the existing
role-definition resolver (teamRoles pages200 until exhaustion) and then one write;
there is no narrower teamRole query in this schema. This does not add another
lookup or alter the existing role resolution path. Task creator stays one filtered
page request (default20), plus existing human App-label decoration.
