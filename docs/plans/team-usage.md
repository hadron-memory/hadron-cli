# `hadron team usage` — design as built (cli#1472)

Tracks each model subscription account's rolling 5-hour and 7-day allowance as
Hadron objects so "which account runs out first, and when" has data. Scope is
the issue body plus Patrick's attach amendment 1 (the amendment wins).

## Shape

- `internal/accountusage` — pure logic: config, snapshot and ccusage readers,
  record shape, burn-rate view. No subprocess, no network, no `os/exec` (pinned
  by a test that reads the package source and runs `collect` with an empty PATH).
- `internal/cmd/team/usage.go` — `collect | show | install | uninstall | status`.
- `internal/healthsched` — generalized from one hard-coded job to a `Job`
  (unit name, launchd label, argv words, uninstall hint). The zero `Job` is the
  health sweep, so every existing entry, golden and caller is unchanged; the
  usage collector is a second job with its own entry per user.

## What it reads, writes, never does

- Reads exactly `<configDir>/hadron-usage/rate-limits.json` per account and the
  optional operator-produced ccusage file, plus the `--config` YAML. Regular
  files only (a symlink is refused), size-capped, decoded into typed structs:
  fields the record does not define (an email, a token) are dropped at decode.
  Errors name the path and a reason class, never contents.
- Writes only account-usage objects (server) and, for `install`, the scheduler
  entry. Never a harness `settings.json`; never runs `npx` or ccusage.
- `collect` validates every account before the first write; any problem is exit
  2 with nothing recorded for any account. A snapshot that does not exist yet is
  "no data": that account is recorded with its windows absent.

## Decisions worth knowing

- **observedAt** is the snapshot's own time when there is one, else collect
  time. A stale snapshot collected twice yields two records with the same
  observedAt; `show` orders by time and takes the newest, and a rate needs two
  points at different times in the same window.
- **tokensSinceLast / estCostUsdSinceLast** are the growth of the ccusage
  blocks since this account's previous record on this host and family (one
  `FindObjects`, only when a ccusage file is configured). Each record carries
  `ccusageBlocks`: the blocks that started within 6h (`ActiveWindow`) of its
  observation, the only ones that can still grow. Against the previous record, a
  block that started within 6h of its observation counts by its increase (a block
  not in the checkpoint counts in full if it started after that observation; one that started before it was backfilled, so the interval is unknown); older blocks are final and add nothing.
  So usage inside a block that was already running counts, and blocks evicted
  from a rolling ccusage file cannot skew the result. No previous checkpoint, no
  ccusage file, a known block whose totals went down, or a checkpointed block
  that is gone from the file (its final usage was never seen): absent, never 0. The
  ccusage `blocks --json` shape (`startTime`, `isGap`, `totalTokens`, `costUSD`)
  is read tolerantly but was not verified against a live ccusage run.
- **Account identity** in `show` is host + family + label: labels are
  host-local, so the same label on two hosts is two accounts. `RUNS OUT` shows
  the earliest qualifying exhaustion across both windows. Config must be one YAML
  document, and a refused label is never echoed.
- **Burn rate** uses points sharing the current `resetsAt` (a reset starts a new
  window); `exhaustsAt` projects 100% at that rate and says whether it falls
  before the reset. An expired window is shown as `expired`, not as current.
- **Record shape** is declared in the CLI and pinned by a golden test, not in
  `team init` (that converges the server's own collections: a server change).

## Out of scope (named follow-ups)

A Z.ai usage source (glm is recorded as "no local source" until one exists),
a server-declared collection, the portal view, the
Lifecycle Agent procedure step, moving workers.

## All providers (cli#1659)

- `family` is `claude` (default), `codex` or `glm`. A codex `configDir` is the
  Codex home and must be a clean absolute path.
- **Codex** reads the newest `sessions/YYYY/MM/DD/rollout-*.jsonl` (lexical
  order: the newest day directory that has one, then the newest name) and takes
  the latest `token_count` event's `rate_limits` (`primary`, `secondary`). Only
  `used_percent`, `window_minutes` and `resets_at` are decoded and stored;
  `credits.balance`, `plan_type`, token counts and conversation text never are.
  The Codex home must itself be a real directory and nothing under it is
  followed through a link (components above the configured home are the
  operator's own path and may be linked); the file opened must be the regular
  file that was checked. Directory listings, line length (1 MiB) and the bytes
  actually read (256 MiB, enforced on the reader) are bounded, and a bad line
  is skipped unread. A missing source is `no-reading-yet`; an unreadable
  directory or a read failure is an error with no path or content in it, never
  a partial reading. The newest file only: a fresh session that has not
  yet taken a turn reads as `no-reading-yet` until its first `token_count`.
- **Record**: Codex windows are `primary*` and `secondary*` with their own
  minutes, not relabelled as the 5h/7d pair; `sourceState` is `ok`,
  `no-reading-yet` (codex) or `no-local-source` (glm). A Claude record is
  byte-identical to before (every new field is omitted).
- **glm** is a finding: an accepted family whose record says `no-local-source`.
- **show** ranks every window of every family on one axis (the earliest
  `exhaustsAt` before its reset); minutes label a window and are part of its
  identity for burn rate (a reading of another length, or none against one that
  has it, never combines), never rank it. Extra `PRIMARY`/`SECONDARY`/`SOURCE` columns appear only
  when an account has them.
