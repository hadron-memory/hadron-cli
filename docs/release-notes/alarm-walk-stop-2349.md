# Alarm-walk stop markers: `lastEventId` is always an event id; `resumeCursor` continues

Unreleased (cli#2349, over server#4135). **For parsers of the incomplete
marker** in `hadron team alarms --json` (`incomplete`) and
`hadron team task list --team --json` (`alarmCounts`), and for readers of the
INCOMPLETE text lines:

- `lastEventId` is now **always an event id** — the last one the walk read,
  empty when it read none. At a `page-limit` stop it used to hold the page
  **cursor**, so a value fed back as `--before`/`--after` to continue worked
  by accident; it no longer does.
- To continue a `page-limit`ed walk, use the new **`resumeCursor`** — the
  cursor the walk stopped at, one it has not paged from.
- A `cursor-stalled` stop (null, repeated, or cycling cursor) carries **no**
  `resumeCursor`: its cursor is one the walk just proved only re-reads pages,
  so following it loops. Retrying such a walk needs a fresh read (usually a
  newer `--since`), not the same cursor.
- A cursor the walk has already used — repeated immediately or cycling
  A→B→A — now stops as `cursor-stalled` at the revisit instead of paging to
  the walk's own page bound.
- The INCOMPLETE text lines and stderr notes name the last event read (or say
  "before reading any event") and, on a page-limit stop, the resume cursor.

The rest of both commands' `--json` shapes is unchanged.
