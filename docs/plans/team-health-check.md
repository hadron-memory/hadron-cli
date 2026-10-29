# `hadron team health check` — design as built (cli#1181)

An independent, mechanical dark-team alarm (Holger's order 2026-10-29, AT0.2920 item A). Every other alarm runs
inside the system it watches: the wake deliverer on orca1 (#2746 held silently) and the server's alarms (#2755). This
command shares no code path with delivery. It reads the roster and the tickers and judges client-side. A non-zero
exit is the alarm, and a scheduler's failure email carries it. Where it runs, its cadence and the email recipients
are ops decisions (Holger via Bo), outside this repo.

## Reads (all existing server fields; no write of any kind)

| Read | Why |
|------|-----|
| `TeamHealthRoster` (`teamRoster`) | role, `lastActiveAt`, `pausedUntil`, `checkStatus`, and `asOf` (used as "now") |
| `TeamHealthApp` (`app.teamPausedAt`) | the team pause, which skips workers with or without a session |
| `ListTickers` + `TickerSubscribers` | the only read that names a worker's subscriptions; every page, non-advancing → exit 7 |

## The rule

- **Key workers:** roster `role` ∈ `--role` (case-insensitive; default `coordinator`, `program-manager`,
  `project-manager`; no live worker has `program-manager` today, and it was kept as the issue asks). Retired workers
  are not on the roster.
- **Counted ticker** (Marco's ruling, team chat #44783, a deviation from "every enabled ticker counts"): enabled, a
  live subscription, and sure to wake the worker:
  - `EVERY_TICK` counts as is;
  - `LIVE_CHECK` counts with one cadence of grace, because it skips a tick when the worker checked within its
    cadence;
  - `READ_TEAM_CHANNEL` alone does not count, because it fires only on unread and its silence proves nothing.
- **Due tick** per counted ticker: the latest tick strictly before `asOf − slack − grace` and outside quiet hours.
  This is the server's `currentTick` arithmetic (`ticks.ts`) evaluated at that cutoff. A tick only counts if it fell
  after the worker subscribed and after its own pause ended.
  - It is not `Ticker.lastTickAt`. With a 5-minute ticker and 5 minutes of slack, the current tick is never older
    than the slack, so a check built on it could never fire.
- **Verdicts**, in this order:
  1. SKIPPED, for any of: team paused; the server's PENDING reason is `TEAM_PAUSED`, `STOPPED`, `PAUSED`, `ISOLATED`
     or `STANDBY` (`NO_TASK` is not a skip); its own `pausedUntil` is in the future; no counted ticker.
  2. DARK "never driven": `lastActiveAt` is null.
  3. DARK: `lastActiveAt` is earlier than the newest due tick.
  4. OK otherwise.
- **Exit:** 0 when every key worker is OK or SKIPPED. **9** (`exitcode.HealthDark`, command-specific like `chat
  check`'s 9) when a key worker is DARK, or when there are none to watch. 3, 7 and 8 per the contract. A failed
  read prints nothing.

## Known limits, all erring toward DARK

- The server reports a stop signal (`STOPPED`) only for a worker with a live session. A stopped worker with no
  session reads DARK. Reading coverage directly (`teamStopSignals`) is refused for App keys and worker sessions, so
  it is not used.
- `lastActiveAt` under-reports on a reaped session, and a turn without a Hadron call doesn't move it.
- When a ticker's time zone can't be loaded on the runner (no tzdata), its quiet hours are ignored.

## Not built

- `--post` (dropped in the issue). The team channel is what goes dark, and posting would need a write key.
