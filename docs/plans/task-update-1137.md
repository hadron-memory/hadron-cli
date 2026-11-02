# Task update (cli#1137, server#2645)

`hadron team task update <task>` is a thin caller over `updateAgentTask(taskId, input)`
(spec cor:agt:050:09 Editing, Proposed; source held fixed and the creator refusal,
Decided; cor:agt:050:14 for the start-by re-anchor). Built against hadron-server
76792bd9 (server#2645, PR #2667); the schema snapshot was refreshed from that revision
in its own commit. Not on production when written (prod 3101666c): landing waits on a
deploy that contains it.

## Wire: a strict patch

The server applies a key that is present (null clears) and ignores one that is absent.
`UpdateAgentTaskInput` therefore cannot use plain pointers: a nil pointer cannot say
"clear", and a plain `omitempty` slice drops an empty list. Every field is `omitempty`;
description, tags, requirements and startBy are `json.RawMessage` (nil omitted, `null`
sent as null, else the value, the `UpdateApp` description idiom); title and
closeSessionWhenDone stay pointers (an empty title is sent and refused by the server;
null close-session would mean false, so a pointer to false is sent instead).

## Flags

`--title`, `--description` / `--clear-description`, repeatable `--tag` / `--clear-tags`,
`--require-role` `--family-in` `--family-not-in` repeatable `--exclude-worker` /
`--clear-requirements`, `--start-by` / `--clear-start-by`, `--close-session` /
`--no-close-session`. Presence (`Changed`), not value, decides what is sent. No flag:
usage error, nothing sent. A value flag with its clear twin, both close-session flags,
and family-in with family-not-in are refused before any request. Requirement and tag
flags given an empty value are refused rather than sent as an empty patch.

No `--source`, `--owner`, `--assignee`, `--status`, `--parent` (own commands, or fixed).
Priority and blockers are cli#1135's commands.

## Output and errors

Text is `task get`'s renderer, `--json` the task DTO. Errors go through `mapTaskError`;
`TASK_ENDED` joins the codes whose name is kept in the message (exit 5 already).
Refusals print no task.

## Help

`task create`/`assign`/`reassign` help said the close-session choice cannot be changed in
place; it can now, so each points to `task update`, and the #959 pins were updated to the
new wording.


## Finish deadline (cli#1514)

The follow-up exposes the server's existing `finishBy` field (#2648/#3088).
Create accepts `--finish-by <RFC 3339>`; update accepts that flag or
`--clear-finish-by`. Neither flag sends no key, a value is sent unchanged,
and the clear form sends explicit null. Create keeps an omitempty pointer;
update keeps an omitempty json.RawMessage. Contradictory forms or invalid
RFC 3339 syntax refuse before a call; past/order checks stay server-owned
(BAD_USER_INPUT, exit 2, no partial edit). Equal start/finish times are allowed.

The shared AgentTaskFields selection now reads finishBy, so every task-record
read and mutation receipt requires a compatible provider (#3088), not only
calls naming a new flag. DTO JSON always includes finishBy, null when unset.
Detail text shows a set deadline; plain list/mine adds FINISH BY when any row
has one, with a dash for unset rows. Existing text without deadlines is unchanged.
The clock, stall.finish-by episode, and ordinary-window suspension/restoration
are server behavior, not implemented by this CLI.

Generated against the committed schema snapshot from server cb866914
(CLI snapshot commit 2b10f8be); no sibling schema was re-exported. The
coverage generator removes the two now-reached input-gap rows per Xanthos's
acceptance correction 6587140276; it does not retain filter-style flag rows.
