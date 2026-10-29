# Team task view: `team task list --team` (#1044, slice 1)

A coordinator could not see their team's work in flight in one view. `team task list` is flat and
covers the whole App, and `team task tree` shows a single chain. Judging "open more than an hour
with no update" took a hand-written script over `--json`. This slice adds `--team`, `--tree` and
`--stale-over` to `team task list`.

**Gate.** Xan stamped it CHECKED for slice 1, T1, in issue comment
https://github.com/hadron-memory/hadron-cli/issues/1044 (01:28Z). Patrick posted a correction from
Vera's FIT at 01:37Z. Slice 2 (the creator criterion, see "Which rows") needs no server change; cross-App rows
are not built (hadron-server#2370).

## What the team is

The team is the coordinator plus every worker whose roster `reportsTo` chain reaches them, within
the one App being read. The App comes from the usual `--app` → context → binding chain.

- **Naming the coordinator.** A bare `--team` means the bound worker; `--team=worker:<id, URN or name>` names one.
  - The value must be attached with `=`. A bare `--team worker:X` would otherwise leave
    `worker:X` as a stray positional argument, so that is refused.
  - A coordinator who is not on the roster exits 4.
- **The walk.** It goes breadth-first down from the coordinator and is cycle-safe.
- **Retired workers.** The roster lists only active workers, so a retired worker leads no team
  here. Their tasks join only as a descendant, or through a current member who owns or is
  assigned them. Such a party renders as `(not on roster)`.

## Which rows

A task joins when a team member (a WORKER party) owns it, is assigned it or created it (`match: party`).
Every descendant of a joined task joins too (`match: descendant`).

- **Why descendants count.** A child task's owner is its parent's assignee
  (`TASK_CHILD_OWNER_MISMATCH`). A team member's seat task for another subtree's worker can
  therefore have children whose parties are all outside the team.
- **Creator (slice 2).** `createdBy` is already on every task the view reads, so the creator joins with no new operation. Only a WORKER in the team counts: a task a person created, or a worker outside the team created, does not join through its creator. `match` stays `party` (no new value in the `--json` contract), and the owner/assignee cells still say `outside` for parties outside the team.

## One read gate

The view reads the App's tasks through the existing `agentTasks` read and nothing else.

- **No server filters.** The read sends no owner/assignee/status filter: the recursion needs the
  ancestors whatever their status.
- **Paging.** It pages at the 200 cap with `offset` until a short page.
- **Deduplication.** Rows are deduplicated by id. A task created mid-walk shifts the
  newest-first order down by one, which produces a duplicate, never a miss.
- **A page that does not advance.** A full page that adds no new id means the server ignored the
  offset. The view refuses (exit 1) and shows nothing.
- **Bound.** It also refuses after 500 pages (100k tasks).
- **Not used: `agentTaskTree`.** It isn't used, so its 1000-row bound does not apply.
- **The join is client-side.** The roster join, the descendant walk, `--status` and
  `--stale-over` all run in the CLI over rows the server already let this caller read.
  - The view therefore never shows a task the plain `task list` would not.
  - It never shows another subtree's tasks wholesale; a test pins this.
- **Cost.** One roster read, ceil(N/200) task pages, and the alarm pages. Today's App
  (~3,000 tasks) takes 15 task pages and about 20 s against production (measured 2026-10-24).
  Slice 2's server creator/owner filters could cut this later.
- **Reporting lines.** These come from a dedicated `TeamReportingLines` read: `teamRoster`
  narrowed to `worker` + `reportsTo`. The view therefore does not inherit the full roster's newer
  fields. On 2026-10-24, production refused the full roster's `model` field, while this read worked.

`--status` is applied after the join, so an open task under a finished team task stays in view.
The plain list's server filters and paging (`--assignee --owner --parent --tag --queued
--roots-only --limit --offset`) are refused with `--team` rather than half-honoured.

## Idle time and `--stale-over`

- **Age.** A task's age is measured from `updatedAt`, else `startedAt`, else `createdAt` (the gate's
  fallback). JSON carries it as `ageSeconds`, with `ageFrom` naming the field used.
- **The filter.** `--stale-over <d>` (a Go duration, > 0) keeps PENDING/STARTED tasks whose age
  exceeds `d`.
- **The note.** A stderr note says that records lag real progress such as PR pushes and receipts.
- **Unparseable timestamps.** One exits 1 rather than silently dropping the row.

## Output

**Text.** It always starts with two scope lines: `app: …` and `team: <coordinator> (<source>) —
N workers`. The second line is there because the coordinator can be ambient, the bound worker
(#470, `review:ambient-scope-must-report-its-source`). An empty view prints `no tasks` under them.

- **Table.** Columns are ID, STATUS, IDLE, TITLE, OWNER, ASSIGNEE and ALARMS. A worker outside the
  team is marked `(outside)`, and one not on the roster `(not on roster)`.
- **`--tree`.** It draws parent/child trees in server order, with the same cells per node. A row
  whose parent is not shown becomes a root. Rows a corrupt parent cycle leaves unreached render at
  the top afterwards, so none is dropped.
- **Open alarms.** These come from `task tree`'s join (`openAlarmsByTask`, extracted and now
  shared), so the alarm facts stay the server's.

**`--json`.** It is an envelope: `{appId, coordinator, coordinatorSource, members,
staleOverSeconds, tasks}`. Each task is the plain task DTO plus:
- `ownerTeam` and `assigneeTeam`: `team`, `outside`, `unknown`, `user`, or null when there is no
  party;
- `match`;
- `ageSeconds` and `ageFrom`.

`openAlarms` appears as in `task tree`. `--tree` changes text only. `teamTaskDTO` embeds `taskDTO`
by value, so no other command's shape moves.

## Not in this slice

- The creator criterion (hadron-server#2370 item 1).
- Cross-App rows. Per Vera's FIT, a worker task's parties all belong to its App, so this case
  needs a ruling about people in two Apps, which is non-blocking.
- Owner/assignee team for parties outside the bound App.
