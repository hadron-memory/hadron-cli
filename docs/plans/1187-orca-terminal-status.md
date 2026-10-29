# `hadron orca terminal status` (cli#1187): design as built

An **Orca integration command**: it classifies a worker's Orca terminal as
`busy | idle | locked | dark` and prints the versioned `terminal-status@1`
contract. Reference for the contract itself: the "Orca terminal status"
section of `internal/cmd/agentic/agentic-usage.md`.

## Layers (nothing Orca-specific crosses the contract)

| package | job |
|---|---|
| `internal/terminalstatus` | the contract (`Result`, closed `Status`/`Prompt`/`Reason` lists) and the dark rule (`IsDark`). Pure; knows nothing of Orca or the server. |
| `internal/orcaterm` (`classify.go`) | the ONE screen classifier, `Classify(terminal, screen, now)`, built on the rules delivery and guardedpush already run (`ReadComposer`, `IdleProof`, `ScreenAgentFamily`), plus the per-harness `LastTurnAt` adapters. |
| `internal/cmd/orca` | the command, the three server reads (`Reads`), terminal-to-worker resolution. Everything outside it is injected (`Env`), so tests use fakes. |

The team health sweep (#1181) consumes the JSON only and never imports the classifier.

## Decisions

- **Canonical classifier: `internal/orcaterm`.** The router's own copy
  (`internal/teamrouter/screen.go`, which still demands the `· ← for agents`
  suffix) is left alone: the #1178 host-poller cutover retires it, so adopting
  the canonical rules there is not this change's business and would be an
  unrelated behavior change for a path being retired.
- **Terminal resolution.** A worker's terminal is an Orca terminal whose
  worktree's session binding (`hadron-team-session.json` in the git dir, the
  same file `team push` trusts) names the worker. A terminal id is taken
  directly; its worker is whatever its binding says, or null. Agent terminals
  outrank shells in the same worktree; two agent terminals fail closed.
- **Dark rule.** Inputs, all reads: `tickers(appRef)` and each enabled
  ticker's `subscribers` (the worker's active subscriptions and their cadence;
  `TickerSubscribers` is the only subscription read there is, so it is one call
  per ticker per run, not per worker), and `dueWakes(appRef, workerRefs)` (new
  operation `DueTickWakes`, never acknowledged). A worker is dark when an
  unanswered tick, aged by `anchorAt` for a tick-only wake or by the ticker's own
  tick time for a merged wake, is at least one
  **fastest subscribed ticker** interval old. `Ticker.lastTickAt` is
  deliberately not an input (Jonas's #1181 lesson: with a 5-minute ticker and
  5 minutes of slack it is never old enough). `lastTurnAt` is deliberately not
  an input either, so a harness without an adapter classifies the same.
- **Precedence** locked > busy > dark > idle: the dark rule runs only on a
  terminal that otherwise reads idle.
- **Fail closed.** Unrecognized screens, unreadable screens, disagreeing
  identity/screen, and an idle worker whose due-wake evidence could not be
  read or was refused for this caller are all `locked/unrecognized`. A busy or
  locked worker keeps its status. A coordinator asking about another worker is
  gated by the server; no server change here.
- **`--all`** is one array, one entry per active App worker, in one pass: one
  `orca terminal list`, one screen read per terminal, one subscription read,
  one paged `dueWakes` read.
- **Exit 10** is `exitcode.OrcaUnreachable`: `orca terminal list` did not
  answer. Distinct from a worker with no terminal (`dark/no_terminal`, exit 0).
  Command-specific like `team push`'s 9-12; 9 is #1181's.
- **`detail` is local only** (never marshalled).

## Known limits, stated rather than papered over

- Reason codes without a captured screen are deferred to `locked/unrecognized`
  per the gate (`model_at_capacity`, `rate_limited`, `token_budget_exhausted`,
  `guardrail_triggered`, `shed_staged_not_relaunched`). The repo's captured
  Codex content-suppression notice screens classify as `idle`, as delivery
  already treats a suppressed turn's completion.
- A fresh Codex launch (banner scrolled out of `IdleProof`'s window by the
  launch art) is `locked/unrecognized`, as delivery holds it.
- `lastTurnAt` is a clock time of day on screen; the date is the most recent
  such time not in the future of now, in the host's zone.
- Follow-up outside this issue: the on-base-tick task node uses this command
  once it ships; `send --if-idle` is a separate slice.
- **Merged wakes cannot prove a dark.** `anchorAt` is the earliest waiting
  reason of any kind (wakeRule.ts takes the min over mentions, ticks and tasks),
  so a worker whose tick is merged with a mention/task is aged by the ticker's
  latest tick time, the youngest bound: no false dark, but also no dark until a
  per-tick oldest-unacknowledged field exists server-side (ruled by
  Marco, #45577/#45645: accepted as a v1 limit; closure is server#2790
  `WakeReason.tickDueAt` plus cli#1216). Several tick reasons in one wake share
  that anchor too, so each ticker is aged by its own `lastTickAt`; a merged tick
  without one fails closed (an error, `locked/unrecognized`).
- A bare shell needs a positive shell prompt on the last line; both families'
  chrome at once is ambiguity and holds, whatever Orca's identity says.
- Foreign text (worker names, handles, `detail`) goes through
  `output.SafeCell` at the human sink.
