# `hadron decision` is now `hadron question`; `answer`, `retract` and list filters

Unreleased (cli#2465, over hadron-server#1641 slices S1-S4). **Breaking:** the server
renamed decisions to questions with no aliases, so every released CLI's
`hadron decision …` fails against it with the schema-skew message (exit 2). This
release is the client for the renamed server: it needs a server that has hadron-server
#4425-#4428 (alpha 0.22.0 or later). Older servers are no longer supported by this group.

## New

- `hadron question answer <question> --expected-revision <n> [--option <key>] [--text <t> | --text-file <path|->] [--reason <t>]`
  answers a question; `hadron question retract` takes the answer back. Only one of the
  question's deciders can, acting as themselves (a worker or agent session and an App
  key are refused, `QUESTION_NOT_DECIDER`, exit 8).
- `hadron question log list (--app <app> | --memory <memory>)` finds the logs of an App or Memory.
- `hadron question list` takes `--app` (every question in an App's Memories) and the
  filters `--answered[=false]`, `--priority`, `--assignee`, `--unassigned`, `--asked-by-me`,
  `--asked-by-worker`, `--asked-by-app`, `--answered-since` and the sorts `ASKED_AT_DESC`,
  `ASKED_AT_ASC`, `PRIORITY_DESC`, `ANSWERED_AT_DESC`. The table gains an ASSIGNEE column.

## Changed (user-visible)

- The group is `hadron question`. `hadron decision` prints a one-line notice and exits 2;
  it sends nothing.
- `--urgency` is `--priority` (LOW, NORMAL, HIGH, URGENT) and `--context` is `--background`;
  the old names are gone.
- An input file has no `parts`: one question per node, with `options` (each with
  `whyChoose`, `whyNot`, `risks`), `recommendation`, `assignee`, `neededBy`/`defaultAction`,
  `specsTouched`/`specsMustChange`. `amend` has no `parts`/`addParts`/`followUps`.
- `--json` for a question drops `parts`, `urgency`, `archival`, `context`, `followUps` and
  adds `background`, `whyNow`, `priority`, `answered`, `options[]`, `recommendation`,
  `answer` (an object or null), `answers[]`, `answeredAt`, `neededBy`, `defaultAction`,
  `specsTouched[]`, `specsMustChange[]`, `assigneeId`, `assignee`, `effectiveAssigneeId`,
  `effectiveAssignee`, `viewerMayAnswer`, `taskId`.
- Exit codes: `QUESTION_UNKNOWN_OPTION` and `QUESTION_ASSIGNEE_NOT_DECIDER` exit 2,
  `QUESTION_NOT_ANSWERED` exits 5; `QUESTION_PART_RENUMBER`, `QUESTION_SUPERSEDED` and
  `QUESTION_ANSWERED_DECIDED` no longer exist.
- `hadron secret client list` shows `clientPrefix` and a null `clientId` for a manager who is
  neither the client's issuer nor its holder (the server now returns only a prefix then).
