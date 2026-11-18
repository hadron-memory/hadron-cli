# `hadron team alarms` (cli#1829) — design as built

## What it answers
"Which alarms are in effect on my team's tasks, and is the assignee reachable?" in one command, replacing the
`task list --team` → per-task `events` → `task get` → `roster` walk.

## Composition (client-side, no server change — decided in the issue)
1. `TeamAlarmRoster` (new, slim): reporting lines for the team set plus the server's `state`, `bound`, `liveSession`
   and `checkStatus` per worker. Deliberately not `TeamRoster`: no newer-field schema skew, no modelGroups advisory.
2. `readAllTasks` (existing): the App's tasks. With `--team`, `selectTeamTasks` (extracted from `listTeamTasks`,
   behaviour unchanged) picks the same set `task list --team` shows, so the two views cannot disagree.
3. `walkAlarms`: `AgentTeamEventsPage` with `alarmsOnly`, `newestFirst`, and `alarmsInEffect` **unless `--all`**,
   paged to the server's end (`hasMore:false`), the cursor being the server's `nextCursor`. A walk that cannot finish
   (page bound, null or repeated cursor) is reported as incomplete — a stderr note, a text line, and `incomplete` in
   `--json` — never silently truncated.
4. Join by task id; alarms with no task, or whose task is outside the scope/status filter, are not listed.

## Decisions
- "In effect" is the server's (`cor:agt:050:20`); the CLI cites it and never re-derives it from `acknowledgedAt` or
  `escalationEndedAt`. `--all` only stops asking for the filter.
- Roster state is shown as returned (`TeamRosterState`), not relabelled; "no session" is `checkStatus` / `liveSession`.
- Default tasks are PENDING and STARTED (a finished task's alarm is not actionable); `--status` overrides.
- `--all` adds per-task `taskAcknowledgedAlarms` and `taskActivityAgeSeconds` (task record age, as `task list --stale-over`
  reads it): an acknowledgement stops the climb, not the stall.
- No `--owner`/`--delegated`: one ownership vocabulary, the existing `--team`.

## Pins
`internal/cmd/team_alarms_1829_test.go`: request shape (in effect / `--all`), task and roster join incl. NOT_LAUNCHABLE
and NO_SESSION, scope and status, page boundary, incomplete walk, `--type`/`--since` as server filters, empty list,
exit 2 before any read, exit 4 unknown coordinator, stable JSON field set, bare `--team`, sanitised text.

## Follow-up
hadron-docs: add the command to the team CLI page (one line plus synopsis) — issue by the implementer.
