# `team task list|mine --json` is now `{tasks, total, hasMore}`

Unreleased (cli#2734, over server#4622 / spec cor:api:120:04). **Breaking for
parsers:** `hadron team task list --json` and `hadron team task mine --json`
used to print a bare array of tasks; they now print one object:

```json
{"tasks": [ ...same task objects, server order... ], "total": 42, "hasMore": false}
```

Read tasks from `.tasks[]`. `total` is the server's count over the same
snapshot as the page; `hasMore` is its page end. Text output is unchanged
except for the continuation line: it stays on stderr with the other paging
lines, prints only when the server says more rows remain (`more tasks remain of
42 total: continue with --offset 20`), and names `--offset <offset + rows
served>` — a short page with `hasMore` skips nothing (cor:api:120:04). A page
that happens to be full is no longer reported as "more" — the old full-page
guess is gone, `--limit` or not. A page that cannot be trusted now refuses with
exit 1 instead of rendering an empty success: no or null page object, null
`items`, a null item (previously skipped silently), or an empty page that
claims more remain — the last can never advance a walk, so it is a server
failure, not a loop to the page bound.

## Whole-App reads stop guessing too

- `team task list --team` pages to the server's `hasMore` instead of stopping
  when a page comes back short, advancing by the rows served; the loud
  refusals (a repeated page, a walk that will not end) are unchanged.
- The intake adapter's held check (`team task find-untracked`) and reconcile's
  holder lookup now page their task reads to exhaustion: server#4622 does not
  widen an `externalIds` batch past one 20-row page (the legacy field widened
  to 200), so a held item or an active holder past the first page was silently
  invisible. An ended task keeps its external id, so holder history can trail
  the active holder; both reads now walk all of it, stepping by the rows
  served, and both refuse an empty page that claims more remain instead of
  paging to the bound.

## Server floor

Both commands need a server with `agentTasksPage` (hadron-server#4622). An
older server refuses the query outright (unknown field); the CLI does not fall
back to the legacy `agentTasks` list. The intake reads keep a friendly
old-server refusal, but the message changed with the document: a server
without the paged read is told `this server cannot page its task reads (it
predates hadron-server#4622)` — the pre-CLI-change message named
hadron-server#4084, which understates the floor this read needs. The #4084
message now appears only for a server that knows the paged read but rejects
the `externalIds` filter argument itself.
