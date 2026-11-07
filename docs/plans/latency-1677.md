# Latency: worker get, node get, chat read (cli#1677)

Design as built. Follow-up to hadron-server#2970; the bar is each call under 2 s at normal team load, with no output or `--json` change.

## Where the time went

Each call made 5 or 6 requests one after another, at ~230-260 ms each from orca1, before any server work.

`worker get` got slower after 2026-10-31 because four independent lookups were added, each in its own operation so an older server that rejects one field still serves the rest:

| Commit | Date | Lookup added |
|---|---|---|
| `cf251d74` | 10-30 | `GetWorkerProfile` (#1355) |
| `f6ee1721` | 10-31 | `GetWorkerTerminal` (#1375) |
| `e684edac` | 11-01 | `GetWorkerModelGroups` (#1485) |
| `4124196d` | 11-01 | `GetWorkerRendered` (#1525) |
| `5fba4886` | 11-06 | `GetWorkerBootPromptServe`, a fifth |

None of them can come from `GetWorkerInApp`'s selection: each is its own operation by design (old-server field rejection is document-wide), and folding them in would make the shared `WorkerFields` fragment fail on an older server for every command that uses it.

## What changed

The per-operation documents are untouched. Independent requests now go out together.

- **worker get:** after the worker row, the profile, terminal, model-group, rendered and boot-preview reads run concurrently (`readWorkerGetExtras`). Errors are taken in the old order, so the first failure a caller sees is unchanged.
- **node get:** the `/capabilities` read starts before the URN resolve; the comment-summary, signature and lint advisories run together, each buffering its stderr note and emitting in the old order (comments, then lint). The capability probe runs on a cancellable context that every exit path cancels and joins, so a failed resolve or read leaves no request behind. For a full `hrn:node:` URN the bracket's first revision probe is asked in the same breath as the resolve.
- **chat read:** the allocator-head and read-cursor reads overlap the head and page reads. Their stderr notes are held and printed in the old order.

## What did not change, and why

- **The two revision probes stay.** `node get` reads revisions before and after the content read so the printed revision provably is the printed content (@codex on #724). The issue calls the second probe a duplicate; it is the bracket. Only the first probe moved earlier, and it still completes before the content read starts.
- **The head probe still precedes the page.** Its staleness claim is about messages observed before the page was requested.
- **`GetWorkerInApp` (~1 s) is server-side.** See hadron-server#3674.

## Pins

`internal/cmd/latency_1677_test.go` holds each request at a barrier until its peers have arrived, so a command that went back to one-at-a-time goes red after 3 s. The request sets are also pinned (each read exactly once). Production mutants run: sequential `worker get`, a duplicated `GetWorkerProfile`, the early node probe disabled, and the chat metadata read moved before the page; each is red. Blind-review fix (Gizelle, P2/P3): also pinned are probe cancellation on a failed resolve and a failed content read, capability/resolve overlap, advisory note order under simultaneous failures, and the first revision probe completing before the content read; mutants for the missing cancel and the swapped note order are red.
