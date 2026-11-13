# cli#1962: the reconcile pass and `task release` (design as built)

Part of source-agnostic task intake (decision `hrn:node:hadronmemory.com:hadron-dev-team-shared:decisions:2026-11-12-source-agnostic-task-intake`,
items 6 and 7). Spec: `cor:agt:120` (D4 minimal write-back), `cor:agt:120:02` (release, V87 Proposed),
`cor:agt:120:04` (the adapter contract). Gate: cli#1962 CHECKED (AT0.6982, FIT AT0.6982.1 COMPLIANT).
Builds on cli#1961 (585bd9d6) and the server's `agentTaskExternalIdChanges` / `releaseAgentTaskExternalId` (hadron-server#4084, ab3de2e0e).

## `hadron team task release <task>`

Calls `releaseAgentTaskExternalId`. Cancel keeps the item (unchanged); release gives it back, so the next create naming it,
or the next `find-untracked` claim, succeeds. The item is read first (`IntakeTaskExternalId`) because the release clears it,
and the receipt prints `released: <item>`; `--json` is the task plus `releasedExternalId` (omitted when the pre-read fails,
since the mutation decides). The mutation is attributed to the bound worker like cancel (`write_attribution.go`). Who may
release is the server's (whoever may cancel; V87 Proposed): the CLI adds no check and shows the server's refusals.
`TASK_EXTERNAL_ID_NONE` is new in the exit map (Conflict, 5): a task holding no item, a second release included.

## The reconcile pass

Runs first in every `find-untracked --adapter` run, list and `--track` alike (`reconcile.go`).

| Change kind | Adapter call |
|---|---|
| REASSIGNED | `mark-tracked` again: external id, task (id, handle, server portal URL), worker (id, name) |
| RELEASED | `untrack` with the RELEASED id the feed carries (the task holds none) |
| CANCELLED | `report` {state: CANCELLED}, only when `describe.calls` lists `report` |
| ASSIGNED, HANDED_BACK, FINISHED | nothing in v1 |

A call the adapter's `describe` does not list is skipped (counted `skipped`), not failed.

**Where T lives (stated as the gate requires): the server's opaque cursor, held by the CLI** in `intake-cursors.json` beside the
CLI config (0600, written atomically under the config lock), keyed by server + App + adapter name. Never a client clock. The
adapter is in the key so two adapters in one App each see every change. The first run has no cursor, omits `after`
(the server then starts at its oldest change); a second run reads after the last answered change and tells nothing twice.
The cursor moves only past changes the adapter answered, persisted once per page and on a failure.

**Failure.** A refused adapter call stops the pass at that change, the cursor stays before it (the next run retries), the
list/claim still run (they do not depend on the write-back), and the run exits 1 with `reconcile.error` set. Limit, stated:
a change the adapter refuses forever holds the cursor until the adapter is fixed; there is no skip flag. A server without the
feed exits 2 naming #4084. A run reads at most 25 pages of 200; the rest resumes next run (`complete: false`).

**Output.** Both `--json` shapes gain `reconcile: {read, marked, untracked, reported, skipped, complete, error}` (additive);
text mode prints one `note: reconcile: ...` line on stderr when it did or said something.

**One run per feed.** A per-feed exclusive file lock (`intake-cursors.json.<hash>.lock`, the OS drops it if the process dies) is held across the cursor read, the adapter's effects and the cursor write, so a ticker run and a hand run cannot overlap and an older run cannot roll the cursor back over a newer one (LOCAL P2 on #1975). A second run waits up to 2 minutes, then exits 5 (busy) before reading or telling anything; Ctrl-C ends the wait (exit 6). The list/claim that follows the pass is outside the lock.

**At least once.** The cursor is persisted after a page is fully answered (and on a failure), so a crash mid-page replays that
page; `mark-tracked`, `untrack` and `report` are idempotent by contract. An adapter that does not list a call in `describe` is
not told, and the cursor passes that change (`skipped`, visible in the output): an adapter upgraded later does not hear the
past. An empty page that still says `hasMore` stops the pass as incomplete rather than reading as caught up.

## Not decided here (disclosed)

- After a reassignment `mark-tracked` gets the new worker's id and name only: the model at claim time does not exist for a
  worker who made no claim, and `cor:agt:120:04` lists this as an open question for Holger. Nothing is invented.
- `ASSIGNED` (including the platform's matcher assigning a queued task) calls nothing, per the D4 mapping as written. If the
  tracker should hear a first assignment, that is a spec change.
- The first run replays the App's whole history from the oldest change. That is the feed's definition of "since T" with no T;
  a baseline-at-head option would be a separate decision.

## Tests

`internal/cmd/task_release_1962_test.go` (receipt, read-before-release, attribution, refusals, JSON),
`internal/cmd/find_untracked_reconcile_1962_test.go` (the mapping, report only when declared, paging, failure, old server,
JSON), `internal/cmd/team/reconcile_test.go` (across runs: second run after the cursor, idempotence, per-adapter cursor,
retry of a refused change; a command test cannot span two runs because the cursor is keyed by server).
