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
  file's cumulative totals (`tokensTotal`, `estCostUsdTotal`, recorded on every
  record as the checkpoint) against this account's previous record on this host
  (one `FindObjects`, only when a ccusage file is configured). Differencing
  totals, not filtering blocks by start time, counts usage inside a block that
  was already running. No previous checkpoint, no ccusage file, or totals that
  went down (blocks aged out of the file): absent, never 0. The ccusage
  `blocks --json` shape (`startTime`, `isGap`, `totalTokens`, `costUSD`) is read
  tolerantly but was not verified against a live ccusage run.
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

Codex and GLM sources, a server-declared collection, the portal view, the
Lifecycle Agent procedure step, moving workers.
