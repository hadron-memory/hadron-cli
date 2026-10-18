# Chat delivery inside the host poller (C1)

Issue [#907](https://github.com/hadron-memory/hadron-cli/issues/907): slice C1
of the agent-team messaging epic (hadron-server#2016, plan r2
[`bbc9f52`](https://github.com/hadron-memory/hadron-concept/blob/bbc9f52/implementation-plans/2026-10-17-agent-team-messaging.md)).
It re-scopes #829 and #906, and supersedes #885's `--source wakes`.
Gate: CHECKED T2 + §10, dependency-ordered (Xylon, issuecomment-6246632510).
The design attach is issuecomment-6246590915; deltas 1 to 4 follow it on the
issue. Built by Joshua on 2026-10-18 from cli main `52b4968`.

**Status: the terminal half is built. Integration waits for S1/S5 on server
main.** Everything that needs no server is in, behind typed seams:
- the Orca calls and screen checks;
- send and confirm;
- locks, the expiry budget, and recovery state.

Still waiting for server main:
- the GraphQL adapter (`deliveryBatches`, `acknowledgeDeliveryBatches`,
  `holdDeliveryBatch`);
- codegen and `make unbound-ops`;
- the command wiring.

Until those land, `team host run` refuses `delivery.enabled` with exit 2.

## The split

The server decides everything that needs no screen (plan r2's thin-client
principle; S1, hadron-server#2017): recipients, filtering, rendering,
batching, timing, the interrupt flag and retries. The CLI decides only what
the terminal shows: whether it may type, and whether the text arrived.

| Package | Holds |
|---|---|
| `internal/orcaterm` | The bounded `orca` runner, the four allowed calls, the screen checks under R2. |
| `internal/delivery` | One batch's deliver-and-confirm transaction. The confirmed-record store, reconciliation, the per-worker lock. Shared with C2 (#908). |
| `internal/hostpoller` | Reads the batches each step, maps each to a registered recipient, queues it on the worker's chain. |
| `internal/cmd/team/host_run.go` | The config and the command; the S1/S5 adapter goes here. |

**The router is frozen, not moved.** `internal/teamrouter` keeps its own
`orca.go`/`screen.go` and its 140-test suite, unchanged, until phase 3 (C6)
deletes it. C1 copied that code and corrected the copy. Moving it would have
changed a command's behaviour under its suite, for a router that is retired
anyway.

## What changed from the router's code

- **#906 item 1** (hadron-server#1940):
  - each call is bounded at 90 s, with a 75 s floor, and killed with
    SIGKILL;
  - any failure other than an ordinary exit reads as code 1 with **empty**
    stdout.
- **#906 items 2 and 3:** the **final** #1968/#1976/#1983/#1985 rules, at
  server `b8384631`, with their pins ported by name onto verbatim fixtures.
  - The idle window is 16 lines deep.
  - A recap block continues only while a line indents **strictly deeper**
    than its header.
  - The notice header is completion evidence, and its body shapes count only
    contiguously below it.
  - Collapsed-output lines are weak chrome.
- **#906 item 4** (the name-keyed `reported` prune) **does not apply.** It is
  router-ledger state, and delivery has no hold ledger: S5 keeps the episodes.
- **The large-gap walk text does not apply.** Delivery types only the
  server's rendered text.
- **Reasons are codes.** The router returned the matched screen line as its
  detail. Nothing here returns, logs or reports screen, draft, or Orca-message
  text.
- **R2 (`ReadComposer`).**
  - Any draft that isn't ours holds, for **both** families. The router let a
    Claude screen that proves the box empty refute Orca's draft and typed
    over it (#6736). That inverts to a hold: a ghost draft now reaches a
    human through S5's 15-minute stuck alarm, which is R2's intent.
  - Our own prefix (`[hadron chat`) is a leftover, for the caller to recover.
  - A question, menu or approval prompt holds `question`. An unrecognized
    screen holds `screen-unknown`.
- **Guard:** only `terminal list|read|wait|send`, never `orchestration send`.

## One delivery

`Deliverer.Deliver` runs under the worker's cross-process lock. It is a copy
of the router's `lock.go`, with the #1490 stale-owner semantics, one lock per
worker, under `<state dir>/delivery/locks`. A deliverer that finds the lock
held skips the worker and never waits, so C2's post can't stall behind the
poller.

1. **Reconcile** confirmed records first (below). Anything proven delivered is
   acked without typing.
2. **Check the batch:**
   - its shape: one line, our prefix, ≤ 8,000 characters, seqs inside their
     ranges, this worker's. A malformed batch is neither typed nor acked nor
     held; S5 has no reason for it, by design;
   - `deliverAfter`;
   - the proof's remaining life against the **budget**: send, confirm,
     record and ack, about 4 minutes.
3. **Find the terminal**, fresh: in a registered worktree (cleaned,
   symlinks resolved), an agent and never a shell.
   - **Two agent terminals:** hold `two-agents`.
   - **None:** skipped, and no hold is invented.
   - **Disconnected, or the interface doesn't match:** hold `screen-unknown`.
     The interface is the worker's `tui`, or a family that names one; else the
     screen and Orca's identity must agree.
   - **Binding names someone else, or no one:** hold `binding-mismatch`.
4. **Judge the composer** (R2) and its liveness. A working agent, or one whose
   idleness is unproven, defers the batch until `busyDeadline` and no later.
   At the deadline a proven-empty composer is typed into, since both agents
   queue input during a turn.
5. **Re-check the lock and stop**, then **send**. The interrupt flag is sent
   only if the batch carries it (always false in phase 2).
6. **Confirm.** Delivered means `turn_started`, or `input_accepted` plus a
   re-read proving our text left the composer. Anything less holds
   `not-submitted` with no record, and is never re-typed: the next attempt
   finds our own leftover.
7. **Record, then ack.** After the send, every step runs under
   `context.WithoutCancel`, each one bounded: a send can't be undone. The
   record is written even if the lock was lost meanwhile, because it is the
   known outcome. A landed ack (or `unchanged`) deletes it. A failed ack keeps
   it.

Holds are reported once per (batch, reason) change, not every poll.

## Records and reconciliation

- **A record is written only after confirmation**, in state `confirmed`.
  There is no pending state on disk, so every record is submitted evidence by
  construction.
- **What it holds:** worker, `batchKey`, ranges (channel id, **incarnation**,
  exact seqs) and the time. It holds no text, no proof and no batch id.
- **How it's stored:** one 0600 file per batch, in a 0700 directory per
  worker, named by a hash of the ranges and written by atomic rename. Records
  older than 24 h are pruned.

`Reconcile`:
- **Full cover:** a `batchKey` match with the same messages, or every channel
  fully covered. The batch is acked whole, nothing typed.
- **Covered prefix:** per channel, the batch's own seqs from its first, for
  as long as each is in one record with the **same channel id and
  incarnation**.
  - Acked as `through:[{channelId, seq: X}]`, where X is always a rendered
    seq.
  - Channels without a prefix are omitted, and S1 moves nothing for them.
  - A refused entry rolls the whole ack back, so the record stays.
- **Incarnation matters.** A tombstoned channel is recreated on the same row
  with seqs restarting (server `src/lib/channels.ts`), so an earlier
  incarnation's record proves nothing.
- **Deviation from delta 2:** delta 2 also required the record to *start* at
  the batch's first seq. That only ever refused a record starting earlier
  (e.g. [4,5,6] against [5,6,7]), which proves 5 and 6 and would have
  retyped them. It was dropped (commit 73ee7de, noted on #907); every acked
  seq is still a confirmed seq of the same incarnation.
- **Not claimed: unconditional exactly-once.** A crash between a confirmed
  send and its record write retypes once, by S1's re-hand contract
  (`cor:agt:080:12`). If a deliverer that lost its lock as stale is overtaken
  before its record lands, a duplicate is possible: the crash-class window.

## The poller

- **When it runs:** after each **successful** registration and collect,
  `deliverStep` reads the batches.
  - Recipients come only from this step's registration answer.
  - A batch for a worker not registered here is skipped and logged.
- **The queue:** each batch goes on the worker's chain (`enqueue`, factored
  out of `dispatch`). One worker's operations and deliveries run one at a
  time, in order, and a batch in flight is queued once.
- **Failures:**
  - `ErrDeliveryScope` (a key without `attention:deliver`,
    `APP_KEY_SCOPE_REQUIRED`) switches delivery off for the run, and host
    operations carry on.
  - Any other read failure is logged and retried next step.
  - Neither fails the check-in.
- **`worker.deliver`** stays reported FAILED. It is a per-task-event
  operation, and chat delivery doesn't use it.

## Config

```yaml
delivery:
  enabled: true                      # opt-in; absent = host operations only
  orca_bin: /home/orca/.local/bin/orca   # absolute
workers:
  iris:
    family: glm
    tui: claude                      # optional: claude | codex
```

Delivery state (records, locks) lives beside `state_file`. That is where C2's
`--orca-host-config` finds the same locks.

## Evidence so far

- **Red first:** every behavioural commit is preceded by its pins, red against
  a stub.
- **Mutation table:** 30 one-at-a-time mutants over both packages, all
  killed. Four survivors on the first run each got a pin; one of them led to
  the delta-2 deviation above.
- `go test -race` is clean on all three packages, and `make test` and
  `make lint` are green.

## Owed before APPROVE (not in this branch yet)

- **S1/S5 on server main:**
  - `make schema` from a clean `origin/main`, `make generate`,
    `make unbound-ops`;
  - the adapter, with red pins on its refusals (`APP_KEY_SCOPE_REQUIRED`,
    `BAD_USER_INPUT`, `WAKE_PROOF_STALE`);
  - the command wiring;
  - `team host status`'s delivery block, `--help` and `agentic-usage.md`.
- **Scratch-terminal evidence** (one stack/terminal, under
  `decisions:2026-10-18-e2e-pause-partial-lift`):
  - verbatim question and menu captures, to replace the provisional
    recognizers;
  - whether an Enter-only send submits an existing composer;
  - each family's clearing keystroke.

  Until each is proven, its path holds `not-submitted`. Safe holds don't
  satisfy the plan's own-leftover acceptance.

## Out of scope

- **Router cutover:** the deploy, enabling delivery on orca1, and stopping the
  TS router.
- **Phase 3:** retiring `hadron team router`, `hadron-chat-send` (C6) and the
  attention API (S11).
- **Other slices:** C2's posting path (#908), which reuses `internal/delivery`,
  and C7's pull call (#912).
- **Docs:** hadron-docs D3 (`how-to/run-a-host-poller.md:192`) belongs to the
  coordinator.
