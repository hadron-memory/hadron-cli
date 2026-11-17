# The host poller's `orca` launches stop scaling with the roster (hadron-cli#2181)

## Problem
Every `orca` call is a process. The poller's delivery made one `terminal list` per due batch or wake and one `terminal read --screen` per terminal in that worker's worktrees, every cycle. A batch held behind a draft stays due, so it was probed again each cycle: cost followed the number of due, held workers (measured on orca1: about 40 launches/min at a 30 s interval, 27 at 60 s). Part of the per-call cost was configuration (the AppImage bootstrap instead of Orca's node CLI, Hugo #110163); this is the code half.

## What changed
- **One list per cycle** (`delivery.SharedList`, wired in `wireDelivery`): one `terminal list` answers every delivery within a short window (5 s, far under the poll interval). A failed list is never shared; a `Send` ends the shared list. A shared list may keep a line held; it never sends one: a delivery that used a list another delivery made (or a remembered screen) and would now type, or touch our own leftover, runs again with a fresh list (`ListFresh`) and fresh screens, so a second agent terminal that appeared inside the window still holds it (review F1).
- **A truncated list is never a roster** (`orcaterm.Client.List`): when Orca says `truncated`, or declares a `totalCount` it did not return, the list is read once more with `--limit <totalCount>`; if it is still short the call fails closed (`ErrOrca`), so a terminal missing from a short list is never "gone".
- **No re-read of a quiet held terminal** (`Deliverer.screens`): the screen read for a terminal is remembered with the list's `lastOutputAt`. While the stamp is unchanged (new output moves it) a delivery may be kept held or deferred from the remembered screen without a process. A row with no stamp is always read, and a remembered screen is at most `ScreenMemoLife` (2 min) old, since a stamp is milliseconds and Orca does not promise it moves on every composer edit (review P2).
- **Remembered evidence never allows a send.** If a pass on a remembered screen would type, or would touch our own leftover in the composer, it stops and reads the screen again first (`typeLine` → `typeLineOn`, `needFresh`). A send forgets the terminal's remembered screen. A false "hold" only delays; a false "safe" is the failure, so remembered evidence can only hold.

## Decisions unchanged
Outcomes (delivered, held, deferred and their reasons) are the same for the same screens: the existing delivery, wake and poller-step suites pass unmodified. The composer-empty proof still reads a full screen: `terminal list`'s `preview` was not shown to be as good as one, so it is not used.

## Measured (counting fake `orca`, `TestOrcaLaunchesPerCycleDoNotGrowWithTheRoster2181`)
Poller as `team host run` wires it, every worker holding a due wake behind a draft, two quiet cycles after the first:

| workers | before: list + read launches | after: list + read launches |
|---|---|---|
| 5  | 10 + 10 | 1 + 0 |
| 40 | 80 + 80 | 1 + 0 |

The first cycle still reads each held worker's screen once (a decision that needs a screen gets one). The interval does not enter the count.

## Not here
The reset driver (off until C3), the relaunch readiness list (one per check) and the title rename (one list) keep their own single calls. `health_dupes` and `orca status` read per terminal but run outside the poller (the 15-minute sweep service and a user command). Switching the service's `orca_bin` from the AppImage to the node CLI is configuration (done by Hugo). Live before/after on orca1 is for QA after deploy.
