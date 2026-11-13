# hadron-cli#1961 — the intake adapter runner and `find-untracked` on the adapter

Design as built. Governing: `cor:agt:120` (Task integrations), `cor:agt:120:04`
(the intake adapter contract), `cor:agt:120:02` (the external id), `cor:agt:050:06`
(root create gate). Gate: Xabat's CHECKED stamp on #1961 and the FIT's seven rules.
Server: hadron-server#4084 (merged ab3de2e0e), the schema snapshot is pinned to it.

## What it is

`hadron team task find-untracked --adapter <exe> [--for X] [--track] [--assignee Y]
[--limit N]`. The adapter is the team's local executable; Hadron makes no tracker
call. The old `--integration` / `--assign-to-me` path is untouched and keeps working
against servers still serving `untrackedTaskItems` until #4085 retires it. The two
paths are mutually exclusive and nothing is chosen silently.

## The invocation format (the CLI owner's call, cor:agt:120:04)

- `<exe> <call>`: one argument, the call's name (`describe`, `list-untracked`,
  `mark-tracked`). Run directly, never through a shell.
- Request: one JSON object on stdin, always with `contractVersion`. Answer: one JSON
  object on stdout, exit 0. Any other exit is the adapter's refusal.
- Selection: `--adapter`, else `$HADRON_INTAKE_ADAPTER`. An explicit `--integration`
  is the server path; `--adapter` with `--integration` is a usage error.
- Environment: the CLI's own, minus `HADRON_*` credentials (token/key/secret). The
  adapter holds the team's tracker credential, never Hadron's.
- Contract version: `describe` runs first, before the worktree binding or the App is
  resolved; any version but 1 exits **15** (`exitcode.AdapterSkew`) before the adapter
  is asked anything and before Hadron is touched (LOCAL P2, Joost).
- Capabilities: `describe.calls` lists every call the adapter answers. `list-untracked`
  is required, and `mark-tracked` for `--track`; otherwise exit 2 before any Hadron
  request, so no claim is ever left unmarkable. A list-only adapter still lists.

## Untrusted output (pinned)

Stdout is capped at 1 MiB (an endless writer is killed at the cap, not at the
timeout), a call at 60 s, a page at 100 items, strings at fixed bounds, a list at 50
pages. Malformed JSON, a second document, trailing text, invalid UTF-8, an empty or
oversized `externalId`, a repeated cursor and an endless fresh-cursor list each
refuse (exit 1). Item text is printed terminal-safe and is sanitised before it
becomes a task title; nothing else from the tracker reaches Hadron.

## The flow

1. `describe` (version check).
2. `list-untracked` pages by the adapter's opaque cursor only. The CLI never builds a
   cursor or an offset (`--skip` is refused on this path, rule 5).
3. Each page is checked with one batch read, `agentTasks(externalIds:)` (any state).
   An item is dropped **only** because a task of the App holds it (rule 2).
4. Without `--track`: list up to `--limit` (1-100, default 20).
5. With `--track`: the create is the claim (rule 1). `externalId` and `source` are the
   adapter's string byte for byte (rule 3). `TASK_EXTERNAL_ID_TAKEN` means the caller
   lost: it is recorded under `lost`, never counted, never looked up, adopted or
   marked, and the walk continues; the exit stays 0. `--limit` counts tasks held
   (rule 4); the walk keeps paging until N or the list ends.
6. The winner calls `mark-tracked` with the externalId, the task (id, handle, the
   server's `portalUrl` or none — rule 6) and the worker (id, name; role and model
   only when the assignee is the bound worker, never borrowed for someone else).
7. Assignee: `--assignee`, else `--for X`, else the bound worker; none of them is a
   usage error (a create with no assignee would be queued for the matcher).

## Failure shapes

- A create refused for another reason stops the walk; what was claimed is printed
  first, then the refusal's exit code (same convention as the compat claim).
- A failed `mark-tracked` leaves the claim standing, is reported (`marked:false`,
  `markError`), stops further claims and exits 1.
- An unreachable server mid-create (exit 7) leaves that item's outcome unknown; the
  next run's Hadron check resolves it.

## STARTED versus PENDING (ruled: Ada #103270, gate addendum 2 on #1961)

`createAgentTask` accepts only BACKLOG|PENDING and `startAgentTask` is
assignee-only, so: create, then start only when the caller is the assignee (the
bound-worker default); with `--for` / `--assignee` someone else the claim stays
PENDING and the output says so. No server change. A failure between the create
and the start leaves the task PENDING; the CLI reports it (`startError`, stderr
note), still marks the claim, and a retry finds the external id held and creates
nothing. All three are pinned.

## Side effects of the schema refresh

`make schema` at ab3de2e0e also carries unrelated additions. `WorkerTerminalReportInput`
gained `attempt` and `repair`; their omitempty annotations are added to
`health_sweep.graphql` so an older server never sees them as `null`.
