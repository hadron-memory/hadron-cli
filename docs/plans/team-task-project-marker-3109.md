# Task project marker (cli#3109)

Implements the CLI half of hadron-server#4997 (merged as #5053 at 9c081e0f,
2026-11-24): `AgentTask.isProject` (`Boolean!`, set at creation only) and the
`isProject` input on `createAgentTask`/`cloneAgentTask`. Specs: cor:agt:150
"A project", cor:agt:050:01/:02. Gate: Xan's CHECKED re-gate, T1 Standard
(LOCAL + BLIND, light CLI QA through the real binary; UX waived, no security).
The server owns every refusal; the CLI never re-implements one — most notably
`project: true` with `isProject: false` is the server's BAD_USER_INPUT (exit 2),
sent as given and shown verbatim.

## Wire semantics

Tri-state by presence, per the repo's omit-unset rule: `--is-project` is wired
`if cmd.Flags().Changed("is-project") { input.IsProject = &isProject }` on
create and clone, so an omitted flag omits the key entirely (the
`@genqlient(for: "…Input.isProject", omitempty: true)` hints already existed in
the snapshot — this PR wires them, it does not add them) and `--is-project=false`
is sent as `false`. On create the marker applies whatever the parent (a nested
task can carry it; `--project` still makes a top-level task and marks it). On
clone the flag overrides the copied marker outright — false unmarks, omitted
keeps the source's — unlike `--project`, whose `=false` is refused client-side.

## Read and render

`isProject` joins the shared `AgentTaskFields` fragment, so the explicit
`taskDTO.IsProject bool` rides every task `--json` (additive key, always
present; `Boolean!` is never null). `task get` prints a `project marker: yes`
line only when marked, distinct from the parent/"project: none" line that names
where the task sits. `task list` adds a conditional IS PROJECT column (`yes`/`—`)
when any row on the page is marked — the same any-row pattern as MIN TIER. The
column is headed IS PROJECT, never the bare PROJECT: beside a HANDLE like
AT3.405 the noun reads as "belongs to a project", while the cell answers
whether the task IS one itself
(`mine` shares that renderer; `list --team` keeps its own ops table, no MIN TIER
or FINISH BY there either, so it deliberately shows no marker column — `--json`
carries `isProject` either way). The
`—` is a definite answer (the server returned `false`), consistent with
OWNER/ASSIGNEE's `—` = none; no unknown is ever rendered with it. The cell is a
client constant and is deliberately not wrapped in `output.SafeCell`
(provenance, not proximity).

## Server floor

The fragment field raises every task command's floor to 2026-11-24 (#5053's
merge date), stated on the group page and per-command; `task dependencies`
keeps its own narrower query and `team task findings` its own narrow read
(11-02), both untouched. The one-date invariant is now enforced per sentence by
`TestTaskRecordFloorIsOneDateOnOrAfter`: any date in a sentence naming the
task-record read must be the current floor. That test's old form (banning two
hardcoded old dates) is exactly how this PR's review miss survived: create's
model-group bullet kept 2026-11-17 while the group page, update and clone all
said 2026-11-24; subsumed arm floors (finish-by overdue arm, `--unresolved`,
`--match-failed`, clone links, min-tier, findings-finish) were bumped or
dropped in the same sweep.

## Batch

`--batch-file` items decode strictly into the generated `CreateAgentTaskInput`
by API name, so an item's `"isProject"` reaches `createAgentTasks` unchanged
with no batch-specific code; pinned both ways (an item without the key must not
grow one). `--is-project` stays refused beside `--batch-file` by the existing
single-create allowlist. The three `#3109` gap lines are removed from
`internal/api/coverage-matrix.txt` (`make coverage-matrix-check` green).

## Alternatives rejected

Client-side contradiction refusal (the server owns it — one verbatim-passthrough
test instead); a separate narrow fragment for get/list (the shared task-record
read is the established pattern — run settings, finish-by and model groups all
raised the floor the same way, and the raise is uniform and documented);
SafeCell on the yes/— cell (client constant).

## Tests

Red first: the test-only commit is 16 subtests red on main c5c90db45 for the
right reasons (unknown flag, marker unrendered, floor/hash stale), verified on
a throwaway worktree at that SHA; absence pins pass on both by design. Wire
assertions decode captured variables into `map[string]json.RawMessage` and
assert key presence, not value (the omitempty trap). The refusal test pins the
request going out as given and the refusal returning verbatim with its code.
Help wording is grep-pinned domain-neutral ("task"/"project", never "epic",
"PR", "review seat" — the #4997 ruling). Parity hashes and the safe-cells JSON
snapshots are re-pinned for the additive key (human text unchanged). The floor
test's mutation check (reintroduce 2026-11-17 → red) is part of the suite run.

## Schema and limits

Snapshot `schema/SOURCE` `server_sha=251162799bee245250bab14bac44f4e43b553e12`
on cli main already carried `isProject` (confirmed at issue amendment; no
refresh, no sibling-server read). Live check through the real binary:
`team task list --json` returns `isProject: false` on every row of the dev App
and the IS PROJECT column correctly stays absent when no row is marked. No
`isProject: true` row exists on that App yet (every root is an AT0 child, and
the marker is two days old server-side), so the marked-rendering path is pinned
by unit tests and delegated to the light QA seat through the real binary.
