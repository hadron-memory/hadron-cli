# `team events --json` is now `{events, hasMore, nextCursor}`

Unreleased (cli#1919, over server#3933 / spec cor:api:120:04). **Breaking for
parsers:** `hadron team events --json` used to print a bare array of events; it
now prints one object:

```json
{"events": [ ...same event objects, server order... ], "hasMore": false, "nextCursor": null}
```

Read events from `.events[]`. `hasMore` is the server's page end and
`nextCursor` is the cursor to continue with (`--after` oldest-first, or
`--newest --before`); it is null on the last page. Text output is unchanged
except for the continuation line: it is printed only when the server says more
events remain, and names the server's cursor. A page that happens to be full is
no longer reported as "more".

## New

- `team events --newest` reads from the newest end (newest events first,
  newest alarms with `--alarms`) without walking the stream from its start;
  `--before <id>` continues it. `--after` with `--newest`, and `--before`
  without `--newest`, exit 2 before any request. The default is unchanged:
  oldest-first from the start of the App, 20 events.

## Open-alarm counts say when they are incomplete

`team task tree` and `team task list --team` count open alarms by walking the
App's alarm events oldest-first to the server's page end. If that walk stops
early (the 100-page bound, or the server reports more events without advancing
its cursor), the counts are lower bounds, and the output now says so (exit 0,
as before):

- text: one `alarms: open-alarm counts are INCOMPLETE (...)` line, counts as
  `≥N`, and `?` (not `—`) for a task with no count in the `list --team` table;
- `--json`: every emitted task carries `openAlarmsIncomplete: true` (then a
  missing `openAlarms` means unknown, not none), and `list --team` also
  carries `alarmCounts: {complete:false, reason, lastEventId, pages}`.

Both markers are absent on a complete walk, so complete output is unchanged.

## Role-memory action before release

The `hadron-handle-a-stall-alarm` role task reads `team events --json` for
`chain`, `wakes` and `subjectWorker`; those are now under `.events[]`. Its
role-memory owner must update that step before this release ships; the repo
edit does not do it.
