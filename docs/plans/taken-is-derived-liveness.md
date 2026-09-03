# `session start`'s TAKEN pre-flight reads derived liveness, not an open row

Design-as-built for **#550**, against hadron-server `main` after **#1114/#1120**.

The third in a sequence. [`held-is-not-taken.md`](held-is-not-taken.md) split
the two refusals; [`held-is-not-driven.md`](held-is-not-driven.md) gave the
roster a column for each; #549 corrected the prose after the server stopped
reaping. This one fixes the last surface still deciding from the retired fact —
and it is the only one of the four where the wrong fact was load-bearing rather
than merely narrated.

## What was wrong

`session start` refused a TAKEN worker from a session row:

```go
if s.EndedAt == nil { active = &cp; return false }   // workerActivity
if active != nil && !force { … exit 5 … }            // the pre-flight
```

**hadron-server#1114 retired the inactivity reaper.** From `reapSessions.ts`:

> `startSession` still leaves `expiresAt` null for DEVELOPER sessions, which
> since #1114 means nothing ends them but an explicit `endSession` — the
> intended outcome, not an oversight.

So for an abandoned worker session `endedAt == nil` is **permanent**. The
refusal could never clear. **Waiting could never help.** And the server's own
derived gate — which would have admitted the bind, because the name is no longer
live — was never reached, because the client returned first.

That is the shape this repo treats as a real bug: *a refusal that cannot be
satisfied by doing the thing the refusal implies*. Its remedy was `--force`, for
a takeover that takes nothing from anybody, on a session nobody is driving.

Found by @codex on PR #549 (P1) and split out, because the fix is a behaviour
change to logic #487/#522/#524 spent fifteen review rounds on and did not belong
in a prose PR.

## The fix

`Worker.hasLiveSession` — not ended AND driven inside the window, computed
server-side by the same predicate the bind gate applies (`cor:agt:020:11`). It
already rides on `WorkerFields`, so the decision costs no round trip.

The session row stays, demoted to **narration**: it is what names the driver,
the tool, the host and the session id, which `cor:agt:020:03` requires a
takeover to show. The decision and the narration are now two separate reads of
two separate things, which is why `drivenBy` tolerates a nil row — they can
disagree by permission or by a race, and a missing sentence is not a reason to
admit a bind.

## Liveness has three answers, so `liveness` has three values

```go
liveUnknown  // masked: the working-state group was not readable
liveNo       // provably not being driven (an OPEN row says nothing)
liveYes      // provably being driven, in the derived sense
```

Deliberately not `(bool, bool)`. That pair is the shape that invites `if live`
and silently collapses masked into false — refusing a caller the server would
have admitted, on a fact they were never shown.

## The design question, and the answer

**What should a masked liveness do?** `hasLiveSession: null` means the caller
did not pass the worker read gate. It is the answer being WITHHELD, not the
answer being "no".

**Decision (Holger, 2026-09-03): defer to the server.** The bind proceeds; the
server applies the same predicate atomically and refuses `WORKER_TAKEN` or
`WORKER_HELD`, whose extensions payloads this command already renders. The cost
is one round trip on the refusal path and no safety at all.

The alternative was fail-closed, and the suite measured its price rather than
arguing it: making masked refuse **reds eleven of this package's session-start
tests**, nearly all of them ordinary binds — because a fixture that does not
mention `hasLiveSession` IS a masked row. In the field the population differs,
but the direction does not: fail-closed makes `--force` the routine path for
everyone outside the read gate, and an override everybody passes routinely has
stopped being one.

## What else moved, and why it had to

**`tookOver` is now nullable.** It was `active != nil`, the same wrong fact in
the `--json` contract: binding over an abandoned open session displaced nobody
and reported `tookOver: true` anyway. It is now derived liveness — `true` when
the name was live and `--force` took it, `false` when it provably was not,
**`null` when liveness was masked**, where `false` would be a claim with no
evidence behind it. That is the shape `releaseResultDTO`'s `wasHeld` and
`forced` already have, for the same reason.

`null` degrades where `false` did: falsy in jq and in JS, and `encoding/json`
drops it into a plain `bool` without erroring. It is a deliberate change to a
documented key, taken because the old value was not merely imprecise but false.

**The refusal's own sentence.** It explained itself with *"its worker session is
still open, which a closed chat session does not end"* — true, and no longer the
reason. Left alone it would have taught, from the one surface a driver meets at
the moment they are trying to act, exactly the conflation #549 spent seven
review rounds removing from this repo's prose. A test pins both retired
spellings out.

**The narration split three ways** to match the decision: a takeover when live,
*"not live; an earlier session is still open and this bind does not end it"*
when provably not, and a statement that liveness was withheld when masked —
which claims nothing, because "not live" there would be a fact the server
declined to state.

## What did NOT move: the HELD classification

`classifyHold` still runs inside the liveness gate. #550 asked about this,
because HELD and TAKEN are independent (`cor:agt:020:09`) and the server checks
HELD first, so gating one behind the other looks like a coupling.

It is not one, and hoisting it would cost more than it buys:

- The **server** is the authority on a hold, and the `WORKER_HELD` branch
  renders the SAME refusal from its extensions. A hold on a name that is not
  live already reaches the identical sentence, one round trip later.
- Hoisting it spends **two extra reads** (holder + own identity) on every
  ordinary bind — every one of which holds its own name.

So the pre-flight buys message quality on a path that was going to refuse
anyway. It is not the gate, and the comment at the site says so, because the
next reader will ask this question again.

## Tests

Three cases, because liveness has three answers, plus the two seams:

| case | expected |
| --- | --- |
| live | refuse (5), naming LIVE — never "still open"; server not reached |
| not live + OPEN row | **binds, no `--force`**, `tookOver: false`, note says the open session is not ended |
| masked | binds; server reached; `tookOver: null`; note asserts nothing |
| masked + server says TAKEN | the server's refusal renders |
| live + unreadable session list | still refuses, driver renders as "an unknown driver" |

**Six mutations, each verified to have applied before its result was read, each
red on exactly the intended tests**: the gate back to `endedAt` (reds the
regression and both masked cases); masked failing closed (reds eleven);
`tookOver` collapsing masked into false (reds one, precisely); `drivenBy`'s nil
guard (panics the seam test); the dropped "does not end it" clause; and the
retired refusal wording restored.

### The fixtures were the work

Seven existing tests broke, all for one reason: **the shared `irisWorkerJSON`
carries no `hasLiveSession` at all**, so every test built on it was exercising
the MASKED branch while appearing to test TAKEN — the trap
`team_worker_activity_test.go` documents one file over. They broke in the safe
direction, loudly, rather than passing on the wrong branch.

`heldBy` was worse: a **row the server cannot emit**, carrying a visible hold
with no liveness answer, when the working-state group masks together and a
readable `heldByUserId` proves the gate was passed. Nothing read the field, so
the impossible row went unnoticed — which is how a fixture stops describing the
server and starts describing the test.

`withLiveness(worker, "true"|"false"|"null")` now says it explicitly at every
site where liveness decides the outcome, in the same three-valued spelling
`workerWith` already uses for the same field.

## Review, and the two findings that were this bug again

Four findings, all valid. **Two of them were this PR committing the error it
exists to remove**, which is worth recording rather than quietly fixing.

**1. The refusal claimed waiting could not help (@codex, P2).** It said *"nothing
frees this name by waiting"* — the pre-#1114 conclusion about an OPEN session
row, attached to LIVENESS, where **the opposite holds**: derived liveness is
recency of driving, so it lapses when the driver stops. Waiting is a real remedy
here, and the sentence sent readers at an unnecessary `--force`. The general
shape is the one #549 named — *a conclusion outliving the premise that made it
true* — and the carry-over went in the direction nobody checks, from the fact
being retired to the fact replacing it.

**2. The SERVER's TAKEN refusal still explained itself by openness (@codex, P2).**
One explanation in two places, edited in one: #549's round 6, inside the PR that
cites it. Sharper here than there, because **#550 made that path newly
reachable** — a masked liveness now defers to the server, so the one wording
left teaching open-equals-taken was the one a caller outside the read gate gets
*every time*.

**3. `withLiveness` could return a row it had failed to change (@copilot).** The
replace path was unchecked, so `"hasLiveSession": false` — the key present, the
regex not matching — would enter that branch and hand back the original while
the test believed it had set liveness. A fixture helper that silently declines
to act, in the PR whose subject is fixtures testing a branch they do not name.
Now whitespace-tolerant, and it panics rather than passing through.

**4. An absence assertion over an ignored decode error (@copilot).** `_ =
json.Unmarshal` is safe for a POSITIVE assertion — a nil map fails it — and
unsafe for *"force is not present"*, which a nil map satisfies vacuously.
Checked, plus an explicit `Input != nil`.

**The mutation run on fix 3 found a weakness in its own new test**, which is the
reason to run them: with the whitespace tolerance dropped, the case still passed
— the helper fell through to the INSERT path and produced a **duplicate**
`hasLiveSession` key, which `strings.Contains` was happy with while Go's decoder
takes the last. The test now asserts the old value is GONE, not merely that the
new one is present, and reds under the exact reported defect.

Honest exception: **fix 4's guard has no constructible failing input** through
the current harness — the capture cannot hold invalid JSON. It is the
landed-mutation-uncovered-branch case, reported rather than counted as proven.

## Propagation

- **No spec.** `cor:agt:020:03`/`:09`/`:11` already state that liveness is
  derived and that TAKEN is the forceable refusal. This client was disagreeing
  with the corpus, not extending it — the rule was right and the code was wrong,
  which is the corpus working.
- **hadron-docs**: reported, not filed — if the CLI reference documents
  `session start --json`, `tookOver` is now nullable with three meanings.
- **No dependent-repo issue.** Nothing server-side moves; the portal has no
  equivalent pre-flight.
