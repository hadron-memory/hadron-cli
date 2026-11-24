# `team utilization`: open family, `lastMatchFailure`, the server's order

Unreleased (cli#2896 and cli#2964, companions to hadron-server#3819 slice 1; needs server
2026-11-21 or later, because the query now selects `lastMatchFailure` and an older server rejects the
document). The `--json` contract of `hadron team utilization` changes in three ways a caller can hit.

| Field or behaviour | Before | Now |
|---|---|---|
| `combinations[].family` | a string | a string, or `null` for an OPEN family (the failed task named no family, or some excluded); the table prints `—`. `--family` cannot select such a row: use `--all`. |
| `combinations[].lastMatchFailure` | absent | `{at, reason}` (the latest no-worker match inside the window; `reason` null on a row recorded without one) or `null`; the table gains a LAST MATCH FAILURE column (a null reason prints the time alone, an empty reason the time and `(empty)` (cli#2986), `—` for none). |
| Row order | sorted by the CLI | the server's order, never re-sorted: utilization, then the most recent failed match, then fewest eligible workers, then names. |

`saturated` is also true when the combination's most recent match inside the window answered no worker
(a later match clears it). Exit codes are unchanged: `--fail-on-saturated` exits 5 on a shown saturated
row, otherwise 0.
