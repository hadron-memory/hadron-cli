# List pagination completeness (cli#967, slice A)

Companion of hadron-server#2211/#2214 (cor:api:120:04: a consumer that needs a
complete list follows pages to the end; hidden defaults are 20 or less).

## Rules as built
- **Complete-list commands drain** when neither `--limit` nor `--offset` is set: `agent list`, `node list` (also the `--seq-gt`/`--sort-seq` paths), `team session list` (and `--active`). An explicit `--limit`/`--offset` is one deliberate page, as before.
- **Drain stop rule:** an empty page, or a known total reached. Advance by rows actually received; a later-page error fails the whole listing. `api.CollectPages` (nullable total) and `api.CollectAll` implement it; the sessions scan also stops on a page of already-seen ids (a server ignoring the offset) and the chat `--all` walk on a cursor that does not advance.
- **No total, no drain:** `node revision list` (no offset) and `team task list|mine` send an explicit page of 20 and print "more may exist" / "full page ... --offset N" on **stderr** when a default page comes back full. Bare-array JSON shapes are unchanged.
- **Defaults dropped from 50 or 200 to 20:** `team task list|mine`, `team events`, `team host operations`, `comment list`/CommentThreads, bounded `team chat read`. `team chat read --all` still walks in 200-row chunks (internal drain chunk, exempt).
- Internal drain chunk sizes (200/500/100) are unchanged.

## Not in this slice
Short-page drains in export/spec/skill/coding/import/chat helper (slice B); the host poller `collectHostOperations` (cli#968, own gate); `memory validate` (`--fail-on-findings` gates on the listed count; no offset to page with); `channel read|register list`, `search`, `node sign` still say "server default"; #2212 continuation.

## Tests
`pagedGraphQL` (internal/cmd/paged_fake_test.go) serves a list in windows clamped to 20 rows; each command is pinned for >20 rows, order, explicit-flag single request, later-page error, and the stderr cue. Production mutants (restore the short-page stop, drop the guards, restore the old defaults) fail.
