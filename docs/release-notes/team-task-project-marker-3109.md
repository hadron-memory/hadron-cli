# `task create` / `task clone`: `--is-project` marks a task a project

Unreleased (cli#3109, over hadron-server#4997 merged as #5053, spec
cor:agt:150 "A project" and cor:agt:050:01 "Project marker" / :02 "What a
clone does"). The server gained an explicit project marker (`AgentTask.isProject`,
`Boolean!`, set at creation only, never edited); a project used to be
"whatever landed at the top level". The CLI sends and shows it.

| Command | Behaviour |
|---|---|
| `task create <title> --is-project` | sends `isProject: true`; the task is a project at any depth — nested and under AT0 included (`--project` still makes a top-level task and marks it) |
| `task create … --is-project=false` | sends `isProject: false` as given |
| `task create …` (flag omitted) | the key is omitted entirely (never sent as null); the server creates the task unmarked |
| `task clone <task> --is-project[=false]` | overrides the copied marker outright: true marks the clone, false unmarks it; omitted keeps the source's marker |
| `task create --batch-file` | an item's `"isProject"` passes through unchanged (items decode into the generated `CreateAgentTaskInput`); `--is-project` stays refused beside `--batch-file` |
| `task get` / `task list` | text shows the marker — `get` prints a `project marker: yes` line (only when marked; distinct from the "project:" line that names where the task sits), `list` and `mine` add an IS PROJECT column (yes/—) when any row on the page is marked (`list --team` keeps its own ops table, no marker column); `--json` carries `isProject` on every task, all task commands |

`project: true` with `isProject: false` is refused by the server
(`BAD_USER_INPUT`, exit 2, nothing created); the CLI passes it through
verbatim and never re-implements the refusal client-side. The same holds for a
clone. The wording about the flag and the marker is domain-neutral — "task"
and "project", never "epic", "PR" or "review seat" (the #4997 ruling).

## Server floor

The marker rides the shared task-record read (the `AgentTaskFields` fragment),
so **every task command now needs a server released on or after 2026-11-24**
(hadron-server#5053's merge); an older one rejects each task query as schema
skew, exit 2, even with no `--is-project` flag. `task dependencies` still runs
its own narrower query and answers on an older server. The help floors are
updated to that date; the `--json` `isProject` key is additive.
