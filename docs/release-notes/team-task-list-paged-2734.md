# `team task list|mine --json` is now `{tasks, total, hasMore}`

Unreleased (cli#2734, over server#4622 / spec cor:api:120:04). **Breaking for
parsers:** `hadron team task list --json` and `hadron team task mine --json`
used to print a bare array of tasks; they now print one object:

```json
{"tasks": [ ...same task objects, server order... ], "total": 42, "hasMore": false}
```

Read tasks from `.tasks[]`. `total` is the server's count over the same
snapshot as the page; `hasMore` is its page end. Text output is unchanged
except for the continuation line: it is printed only when the server says more
rows remain (`more tasks remain of 42 total: continue with --offset 20`), and a
page that happens to be full is no longer reported as "more" — the old
full-page guess is gone, `--limit` or not.

## Whole-App reads stop guessing too

- `team task list --team` pages to the server's `hasMore` instead of stopping
  when a page comes back short; the loud refusals (a repeated page, a walk that
  will not end) are unchanged.
- The intake adapter's held check (`team task find-untracked`) and reconcile's
  holder lookup now page their task reads to exhaustion: server#4622 does not
  widen an `externalIds` batch past one 20-row page (the legacy field widened
  to 200), so a held item or an active holder past the first page was silently
  invisible. An ended task keeps its external id, so holder history can trail
  the active holder; both reads now walk all of it.

## Server floor

Both commands need a server with `agentTasksPage` (hadron-server#4622). An
older server refuses the query outright (unknown field); the CLI does not fall
back to the legacy `agentTasks` list.
