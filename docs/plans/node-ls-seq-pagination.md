# `node ls` — seq flags must page the whole collection (#319)

> **Status: implemented and verified** on this branch. Fixes
> [#319](https://github.com/hadron-memory/hadron-cli/issues/319).

## Bug

`node ls --seq-gt N` and `--sort-seq [asc|desc]` post-process **client-side**
(`internal/cmd/node/ls.go`), but they ran over a single server page that the
server had **already truncated** to its default size (~50). Once a collection
exceeded one page, the newest nodes fell off that page, so:

- `--sort-seq desc` topped out at seq 50 and missed 51/52;
- `--seq-gt 50` returned **empty** — read as "no new messages" when there were
  two. Silent-wrong-empty is the worst failure mode (a polling agent confidently
  reported "nothing new").

## Why not push it to the server

The server can't express either flag on the `seq` column: `NodeFilter` has no seq
bound, and `NodeWhereColumn` is only `data`/`properties` (JSONB) — not `seq`.
`NodeSort` has a `seq` value but no direction arg. So `--seq-gt`/`--sort-seq`
must stay client-side — which means they must see the **whole collection**, not
one page.

## Fix

This is the same #23 discipline the spec commands already use: a listing whose
contract is "the whole collection" must page to exhaustion.

- When `--seq-gt` or `--sort-seq` is set **on a browse** (not a ranked
  `--search`), `node ls` now pages `findNodes` to exhaustion via
  `paginateAllNodes` (fixed 500-node pages until a short page), then applies the
  seq filter and sort over the full set.
- `--limit`/`--offset` are then applied **client-side, after** the filter/sort —
  so `--sort-seq desc --limit 3` means "the top 3 by seq", not an arbitrary first
  page.
- The plain browse (no seq flag) is unchanged: server-side `loc` sort + `--limit`
  page.

No silent wrong-empty is possible now — `--seq-gt` returning empty genuinely
means no higher-seq nodes exist.

## Tests

- `internal/cmd/node/ls_test.go` — `paginateAllNodes` pages past the first page
  (500 + 500 + 3 → 3 calls) and stops on a short page.
- `internal/cmd/commands_test.go` — `--seq-gt` returns only seq>N **and** requests
  a large page (proving it doesn't rely on the default page); `--sort-seq desc`
  orders correctly; `--sort-seq desc --limit 2` yields the top 2.
