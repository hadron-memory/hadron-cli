# `hadron question` — question logs and questions (design as built, cli#2465)

Replaces the `hadron decision` group of cli#1335 ([the old plan](decision-command-group.md)).
Gate: Xan's CHECKED on hadron-cli#2465 (T2) and Victor's FIT. Server: hadron-server#1641,
slices S1 #4425 (rename), S2 #4426 (shape), S3 #4427 (answer, retract), S4 #4428 (lists
and filters), all merged. Built against the schema exported from hadron-server main
`ef26d5856` (see "Schema" below).

## Surface

| Command | Operation |
|---|---|
| `question log create <memory> --loc --decider… [--eligible…] [--name] [--prefix]` | `createQuestionLog` |
| `question log list (--app \| --memory)` | `questionLogs` (new) |
| `question log get <log>` | `questionLog` |
| `question log set-deciders <log> …` | `setQuestionLogDeciders` |
| `question ask <log> --input-file [--title --background --priority --tier --assignee]` | `askQuestion` |
| `question get <question>` / `get <code> --log <log>` | `question` |
| `question list (--app \| --log \| --awaiting-me) [filters]` (alias `ls`) | `questions` |
| `question amend <question> --expected-revision --reason [--input-file] [field flags]` | `amendQuestion` |
| `question answer <question> --expected-revision [--option] [--text \| --text-file] [--reason]` | `answerQuestion` (new) |
| `question retract <question> --expected-revision [--reason]` | `retractQuestionAnswer` (new) |
| `question withdraw <question> --expected-revision --reason [--yes]` | `withdrawQuestion` |
| `question verify <question>` | `verifyQuestion` |
| `question purge <question> --reason [--yes]` | `purgeUnansweredQuestion` |

`hadron decision` (any arguments, any old flag) prints
`hadron decision was renamed to hadron question (hadron-server#1641). Nothing was sent.`,
exits 2, and reads no config and makes no request (`internal/cmd/question/retired.go`;
flag parsing is off so an old flag reaches the message). It is not an alias.

## Decisions made at build time

- **One shape, no probe.** The old group carried a dual-shape path (cli#2639: a one-time
  server probe, hand-written S2 operations in `s2.go`). The server has deployed S2, so
  that path is gone (cli#2640) and every verb is one typed genqlient operation.
- **`answer` and `retract` send `via: CLI` themselves, never a flag**: where an answer
  was given is the caller's claim, and the server records it (cor:dmo:140:03).
- **`--expected-revision` is required and there is no hidden read**: it is the `revision`
  that `question get` prints, so an answer covers what the decider read. A stale one is
  `NODE_WRITE_CONFLICT` (exit 5).
- **Who can answer is the server's rule** (`QUESTION_NOT_DECIDER`, exit 8: a worker or
  agent session and an App key are refused). The help says so, and that a worktree bound
  to a worker must use the portal or a shell bound to no worker.
- **Tri-state amend fields** (`background`, `whyNow`, `assignee`, `neededBy`,
  `defaultAction`) are bound as `json.RawMessage`, so a file that says `"assignee": null`
  clears it and a file without the key leaves it; the four reference lists are
  deliberately not omitempty (an empty list clears). `AskQuestionInput.deciders` is not
  omitempty either (null means the log's defaults).
- **Every list filter is sent only when its flag was given.** `--answered` alone is
  `true`; `--answered=false` is pending. A list needs a scope: `--app`,
  `--log` or `--awaiting-me` (`--awaiting-me=false` is none; `--app` with `--log` is refused;
  `--awaiting-me` may stand alone or sit beside `--app`/`--log`, the server judging the
  combination), checked before any request.
- **Exit codes** (`internal/api/errors.go`): the `QUESTION_*` rows keep their classes;
  `QUESTION_PART_RENUMBER`, `QUESTION_SUPERSEDED` and `QUESTION_ANSWERED_DECIDED` are
  gone with the old shape; `QUESTION_UNKNOWN_OPTION` and `QUESTION_ASSIGNEE_NOT_DECIDER`
  are usage (2); `QUESTION_NOT_ANSWERED` is a conflict (5).

## Schema

`schema/schema.graphql` is now exported from a real hadron-server revision (`ef26d5856`,
on main, with S1-S4), not the cli#2043 merge pin. See the PR for what that retires.
