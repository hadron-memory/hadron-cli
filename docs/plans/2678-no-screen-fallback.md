# Host poller no-screen fallback (cli#2678, slice c of #2675)

Built as ruled (Peter #122249, Hugo #122255, 2026-11-20): when Orca gives no
screen for a worker's terminal, the poller **surfaces** it and **never types**.

## What changed

- `internal/delivery/noscreen.go`: `NoScreenTracker` watches the `delivery.Event`
  stream the deliverer already emits. Per worker, the first `held screen-unknown`
  starts an episode; after `NoScreenAfter` (15 minutes) of continuous holds it
  fires once. A delivery, reconcile, deferral or a hold of any other reason ends
  the episode (a screen was read: today's rules apply again). A worker with no
  screen-unknown hold for `NoScreenGap` (5 minutes) also ends it, so a wake that
  was consumed elsewhere never fires a stale episode. If recording fails, the next
  hold retries; a recorded episode is never recorded again.
- `internal/cmd/team/host_delivery.go`: `wireDelivery` feeds the tracker from the
  same `Emit` seam. On fire it (a) sends **one terminal report** through
  `setHostWorkerTerminals` with status `locked` and reason `no_screen` (the SDL
  says that is how the CLI reports it, hadron-server#4566). It is stored as the
  worker's terminal state, which the roster shows; the server documents the
  report as advisory (it feeds no check status or alarm of its own); and (b) writes **one host event**
  `delivery-no-screen` with the terminal handle, held-since and pending count, so a
  person can open that exact terminal in Orca; and (c) records the episode on the
  host status (`team host status`: a `NO SCREEN since ...: N pending, open terminal
  ...` line, and `noScreen` in `--json`, null when none). A delivery, reconcile,
  deferral or another hold reason clears it.

## Decisions to know

- **Never types.** The tracker holds no Orca handle (it sees events only), and the
  deliverer's `screen-unknown` hold is unchanged. The incident test runs 66
  one-minute cycles against a terminal whose screen is unavailable and asserts no
  send at any cycle; a mutant that falls through to typing after the hold fails
  it (and two older guard tests).
- **No re-render step.** Ruled step 2 allowed a re-render only through an Orca call
  documented read-only. Orca's `terminal` commands are list, show, read, send,
  wait, create, switch, close, rename, set-pane-title and split; the poller already
  calls `read --screen` every cycle, `switch` changes the UI, and the rest write.
  There is no such call, so the step is dropped, as the ruling says to do.
- **The report cannot carry the terminal id, held-since or pending count.**
  `WorkerTerminalReportInput` has no field for them ("no free-text field") and the
  server has no hold report yet (`Hold` answers `ErrHoldUnavailable`, #940), so the
  three live on the host event line and the host status (Petra #133247: no server
  field now; a roster field would be a separate server issue).
- **Pending count** is the number of distinct held wakes/batches in the episode
  (distinct spans), not the number of holds.
- The event line and the failure line carry codes, ids and counts only: never
  screen text, never an upstream error message.

## Pins

Red commit first (`ac00c26b`): tracker threshold, once-per-episode, distinct
pending, episode end on each of delivered/reconciled/deferred/other hold, separate
workers, silent gap, failed-report retry, and the end-to-end incident through the
real `Deliverer`. Wiring tests in `internal/cmd/team` assert exactly one
`HealthSweepSetTerminals` (locked, no_screen, the worker id) and one event naming
the terminal, and that a failed report retries with no upstream text in any line.
Hand-applied mutants, each killed: guard types on screen-unknown; threshold
dropped; fired flag ignored; a returned screen does not end the episode; a silent
gap never ends one.

## Review fold (Juniper's BLIND at 962ea26f)

- The report runs inside the delivery event path, so it is bounded by a 10 s
  timeout (`noScreenReportTimeout`), and a hung server is pinned by a test. One
  episode never has two reports in flight (an `inFlight` guard, pinned with
  concurrent `Observe` under `-race`).
- A failed report waits `NoScreenRetry` (5 min) before the next attempt, and its
  host line is stamped now, is the documented kind `delivery-error` with outcome
  `no-screen-report`, and carries a code (`TIMEOUT`, `REFUSED`, `UNCONFIRMED`,
  `ERROR`), never the server's message. A refusal `TERMINAL_REPORT_STALE` means a
  newer stored report (the next health sweep's) superseded ours: it is recorded,
  not retried.
- One rule ends an episode for the tracker and the status book (`endsNoScreen`);
  skipped and failed events do not end one. The status line also stops showing an
  episode after `NoScreenGap` without a hold, and a recorded episode's pending
  count is refreshed on every later hold.
- **Nothing sends a clearing report.** A clearing report would have to assert a
  state the poller does not know (idle? busy?). The stored report is replaced by
  the next sweep report (an older report than the stored one is skipped by the
  server), and the host status stops naming the episode when the screen returns or
  the gap passes.
- Tracker state is in memory: a restart inside an episode records it again after
  another 15 minutes.
