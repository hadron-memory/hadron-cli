# Team chat reads one page by default (CLI #787)

Server #1536 changes a cursorless, offsetless chat read to return the newest
page, ascending within that page. An explicit `sinceSeq` remains a forward
read from the oldest end; `beforeSeq` remains a backward page. The server keeps
the 200-message cap. This CLI change uses that contract and must deploy after
the server change.

| Command | GraphQL cursor | Calls | Result |
| --- | --- | --- | --- |
| `team chat read` | omitted | one | newest page |
| `team chat read --since 0` | `sinceSeq: 0` | one | oldest page |
| `team chat read --since N` | `sinceSeq: N` | one | next forward page |
| `team chat read --before N` | `beforeSeq: N` | one | newest page below N |
| `team chat read --all` | `sinceSeq: 0`, then each page's last seq | until short page | full forward history |
| `team chat read --all --since N` | `sinceSeq: N`, then each page's last seq | until short page | remaining forward history |

`--limit` sizes a single page. It cannot combine with `--all`, since that would
give it two incompatible meanings. `--before` also cannot combine with
`--all`, which walks forward. The JSON keys stay `messages`, `nextSince` and
`prevBefore`; `nextSince` remains the highest returned seq (or the input
`--since` when empty), and `prevBefore` remains the lowest returned seq (null
when empty).

## Read-state safety

The bound worker's local watermark and server read cursor advance only after a
successful render. An unfiltered explicit forward read can advance when it
starts at or before the existing watermark; a `--before` window cannot.

A cursorless tail can also be a window: if the worker last read through 90 and
the newest page starts at 101, then 91–100 were never delivered. It advances
only when the page begins at or before the next seq after the known watermark,
or when an uninitialized binding receives a page starting at seq 1. An empty
tail can record a read-through-0. A skipped tail stays visible to the router
until the worker explicitly walks the gap or uses `team chat mark-read`.

The Orca attention router from server #1490 polls `team attention`, using its
own token; it does not call this command. The server's monitor read uses an
explicit cursor and likewise remains forward.

## Evidence

Command tests pin the wire difference between omitted `sinceSeq` and explicit
zero, one request for a full default page, `--all` exhaustion, invalid flag
pairs, JSON cursors, and both contiguous and skipped tail watermarks. The
existing backward-page, forward-page, render-failure, scope and server-mark
tests remain in the suite.

Older context: [chat-read-walks-back.md](chat-read-walks-back.md) records the
previous exhaustive default and the `beforeSeq` rollout; its cursor and
read-state findings still apply.
