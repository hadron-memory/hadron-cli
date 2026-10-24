# Roster model and tier (cli#993)

Design-as-built for `team roster` / `team worker list` showing the model a
worker self-reported and the server's coarse tier (hadron-server#2252 slices A
and C). Gated CHECKED T1 at cli#993 (Xavier), folded rulings R-A/B/C.

- **Source.** One read, unchanged: `TeamRoster` gains `model`, `modelReportedAt`
  and `tier` on `TeamRosterEntry` (server companion #2278, closed); `worker list`
  gains the same three on `WorkerRosterFields`. No second read or join. The schema
  snapshot was refreshed from a clean worktree of server `origin/main`
  (`make schema` with `HADRON_SERVER_DIR` set deliberately, #503).
- **Display.** Roster: MODEL and TIER after FAMILY. Worker list: MODEL and TIER
  after LAST RECORDED ACTIVITY, no FAMILY. A null renders `-`. Both are advisory
  (model self-reported, tier server-derived) and never authorization; the help
  text says so.
- **`--json`.** Additive only: the three keys, nulls preserved, an unrecognised
  tier string passed through untouched. Every earlier key is pinned.
- **Capacity line (table only).** `free for work by tier: high N · mid N · low N
  · unknown N` counts the rows the server marked `freeForWork` — not `state ==
  AVAILABLE`, since a worker can be AVAILABLE and still not free. A null or any
  string other than exactly `high`/`mid`/`low` is `unknown`; nothing is inferred.
  An empty roster prints zeros.
- **Ordering.** A server without the fields rejects the static query
  (GRAPHQL_VALIDATION_FAILED), which would fail the whole command, so there is no
  old-server fallback claim. The CLI release carrying this must not ship before
  the server with #2278 and slice A is deployed to production (Hugo owns the
  deploy); the PR body and release note say so.
