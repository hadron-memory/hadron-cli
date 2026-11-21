# Paged task list reads (cli#2734)

Design as built for hadron-cli#2734, adopting server#4622 (`agentTasksPage`).
Companion to the paged-events change (cli#1919) and the paged web-poll /
signing-request reads (cli#2032).

## What changed

| Call site | Before | After |
|---|---|---|
| `team task list` (5 filter shapes: plain, text, filtered, unresolved, creator) | `agentTasks(...)`, one page, full page guessed as "more" on stderr | `agentTasksPage(...) { items total hasMore }`; one page per invocation |
| `team task mine` | same, forced to my party | same switch |
| `team task list --team` (`readAllTasks`) | pages at the 200 cap, ends when `count < limit` | ends on `!hasMore`; the no-advance and page-bound refusals stay |
| intake adapter (`IntakeTasksByExternalIds`, cli#1961) | one unbounded `agentTasks(externalIds)` call | pages at 20 to exhaustion (`hasMore`); the batch is no longer trusted to fit one page |
| reconcile (`IntakeItemHolders`, cli#2265) | one unbounded `agentTasks(externalId)` call | pages to exhaustion: ended tasks keep the id, so history can trail the active holder past one page |

The five list queries stay five separate documents (an omitted argument must
stay off the wire), each switched at the root field only.

## Decisions

- **Offset paging, not cursors.** The server's contract is `limit` (default 20;
  0 serves only `total`; over 200 served as 200) and `offset` — the same flags
  `team task list` already has. `--limit` stays validated 1..200 client-side
  (`checkTaskPage`); continuation is `--offset <offset + len(items)>`.
- **`--json` is now a page object.** Breaking, per the cli#1919 precedent:
  `team task list --json` and `team task mine --json` print
  `{"tasks": [...], "total": <int>, "hasMore": <bool>}` instead of a bare
  array. `tasks` matches the wrapper family (`events`, `items`); `total` is
  selected because `AgentTaskPage.total` is non-null and counted over the same
  snapshot as `items` (server#4622). Release note added.
- **Text output.** The table is unchanged. The old stderr guess ("Showing a
  full page … there may be more") is **removed**; cli#967's rule survives in
  its honest form: a continuation line is printed only when the server says
  `hasMore`, naming `total` and the exact `--offset` to continue with.
- **Untrustworthy page metadata is an error** (exit 1, `dependencyPage`
  precedent, cli#1135): a missing page object or missing/nil `items` refuses
  rather than rendering an empty success.
- **No fallback, no capability gate.** A server without `agentTasksPage`
  rejects the document at GraphQL validation; `api.MapError` surfaces that
  refusal. The CLI does not probe, gate or fall back to the legacy array —
  same decision as cli#2032. The legacy `agentTasks` row moves to
  `excluded not-a-capability` in the coverage matrix beside `agentTeamEvents`.
- **Whole-corpus reads page to exhaustion.** `readAllTasks` ends on `!hasMore`
  (the `count < limit` guess goes); the no-advance refusal and the 500-page
  bound stay — a server that ignores offsets is still refused loudly. The two
  intake reads loop the same way with their own loud bound: the adapter's
  drop-if-held decision and reconcile's complete holder map are wrong if
  truncated, and server#4622 explicitly does **not** widen an `externalIds`
  batch past a 20-row page (the legacy field did widen to 200 — history past
  that was silently dropped).
- **`--team`'s alarm-count walk is untouched** (already paged on
  `agentTeamEventsPage`, cli#1919).

## Sequencing (the schema-refresh blocker)

`agentTasksPage` merged to hadron-server as cfe90cff (2026-11-20), one day
**after** the Question overhaul #4426 (b14b3b304, 2026-11-19) — every server
revision with `agentTasksPage` already carries the new `Question` shape, so no
snapshot pin window exists, and refreshing `schema/schema.graphql` today breaks
`decision.graphql` generation until cli#2465 (PR #2801) lands. This branch
therefore authors the plan doc, the red-first tests and the `.graphql` edits
now; `make schema` (with `HADRON_SERVER_DIR` set deliberately, sibling at
origin/main) + `make generate` + the Go implementation land rebased behind
#2801's merge. The snapshot is never hand-patched.

## Out of scope

Server changes; auto-pagination (continuing is a second invocation); other
`agentTasks` callers' behavior beyond the two intake reads; `limit: 0`
total-only mode; and a CLI release (the release note rides the next one).
