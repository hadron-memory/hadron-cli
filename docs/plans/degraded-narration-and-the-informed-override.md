# A demoted read must not be able to fail the command — except where it is the decision

Design-as-built for **#553**, the follow-up
[#552](taken-is-derived-liveness.md) split out rather than folded in.

The fourth in the sequence. #552 re-pointed `session start`'s TAKEN gate at
derived liveness and **demoted the session scan to narration**. The error
handling did not move with it.

## What was left

```go
last, active, err := workerActivity(ctx, client, w.Id)
if err != nil {
    return err          // aborts the bind
}
```

That was structural before #552 — the decision needed those rows. Afterwards it
is a read the command no longer depends on, still able to fail the command. The
two reads are **gated separately**: `hasLiveSession` rides on the worker row,
`sessions(workerRef:)` is its own query. A caller who can read the worker but
not its session list could not bind at all — refused by a dependency that had
been removed, which is #550 in a different costume.

## The one real question, and why it went to the coordinator

`cor:agt:020:03` says an informed override is **never silent**. Does that mean
`--force` must keep FAILING when it cannot name the driver it takes over from?

That is a reading of platform law, not a wording choice — a "refuse when you
cannot name the holder" reading and a "narrate the gap and proceed" reading
produce different commands. Raised at team-chat seq 417 and 440, and left
**blocked for a day** rather than guessed.

**Ruling (@Ada, coordinator, on the issue):**

1. **`--force` fails closed on a transport error; everything else degrades.**
   *"Never silently"* constrains the **override**, not the refusal. And #552
   sharpens it rather than weakening it: `--force` is now reached only when
   liveness is genuinely true, so the population it serves is *"somebody is
   actually driving this right now"* — naming them stops being narration and
   becomes the entire content of the decision.
2. **An empty list does NOT refuse.** `sessions()` treats `workerRef` as a
   filter, not a lookup: an unresolvable ref returns `[]` deliberately, so it
   cannot be used to probe which workers exist outside the caller's scope, and
   `sessionScopeFilter` never throws for authorization. **Empty is the normal
   answer for a caller who may not read sessions**, and refusing on it would
   refuse precisely the masked caller.
3. **Do not render masking as an ordinary unknown.**

Not ruled, deliberately: whether "driver not visible to you" is one string or a
separate exit path. Wording and control flow were left to this repo.

### The process half is worth recording

The ruling had been given a day earlier **in the team chat**, and #553 carried
zero comments. A new session went to the issue, found nothing, and was about to
re-raise it a fourth time. The coordinator named this herself as the failure she
had already ruled on three times that week. **A ruling that lives only in a
conversation is not one an implementer finds** — the correction cost one comment
and the omission cost a day.

## The design: the carve-out pays for the inference

The three points are not three independent changes. Carving the transport error
out **under `--force` only** is what makes point 3 decidable at all.

`hasLiveSession` is *not ended AND driven inside the window*, so when it is true
an unended row **exists**. If the read answered and returned nothing anyway, the
caller cannot see that row. **Masked is not inferred from a guess — it is what
is left once the failed read has been removed from the candidates.**

### The argument as first written was wrong, and the review checklist caught it

Kept here rather than quietly corrected, because
`review:a-tidy-argument-is-not-a-checked-one` exists for exactly this and its
instruction is to keep the argument that felt airtight.

The paragraph above was originally the whole justification, and it shipped a
**two-condition** version of the test: *answered, empty, live ⟹ masked*. Running
the checklist asks **who is excluded** by an argument of the form "anyone who can
do A can do B" — and there is a population: the two reads are separate, so the
open session can END between them. That caller sees the worker's **ended** rows
and no open one. They are fully permitted, and the two-condition version tells
them they are *not permitted to see* the driver — a false statement about the
reader, manufactured out of a timing accident.

That is the same defect PR #504 shipped when it argued a nil holder meant unheld,
in the same file, about the same class of masked field.

The fix is the third condition, `last == nil`, and it is a **probe rather than an
argument**: a masked caller is scope-filtered out of the whole list, so seeing
*any* row is evidence the gate is open. Cheap, local, and it degrades to
"an unknown driver" instead of to a falsehood — which is what the checklist means
by preferring a probe to a proof.

`review:an-empty-result-as-a-signal-must-be-unforgeable` reaches the same finding
from the other side: giving emptiness a second job means every *other* way of
being empty now produces a confident wrong answer, and the test is not *can this
be empty* but **can this be empty for a reason other than the one the signal
means**. The race is that reason. Its other arm passes — nothing user-supplied
reaches this query, so there is no input that can forge the empty.

So `active == nil` has **three** origins, not two:

| origin | reachable | what the caller is told |
| --- | --- | --- |
| the read FAILED | `!force` only | *the session list could not be read* |
| answered, empty, liveness **yes** | either | **a driver you are not permitted to see** |
| answered, empty, liveness no/masked | either | *an unknown driver* — unchanged |

Collapsing the first two is the mistake this file already has a name for, a few
hundred lines down: `currentUserID` carries **three** states rather than two, on
PR #504's review, citing `review:a-claim-must-not-outrun-its-evidence` — *a
failed read is not evidence of absence*. `workerActivity` returned bare nils and
threw the reason away, so a dropped connection rendered as a driver who could
not be identified: **a claim about the worker made from a fact about the
transport.**

Hence `sessionNarration{last, active, answered}`. The `(*T, *T, error)` triple
is the shape that invites the collapse, because the error is easy to drop on the
floor once it has been logged.

**The ORDER of the two nil-explaining arms is load-bearing and nothing about
reading them says so.** `live` comes off the worker row, which succeeded — so a
failed sessions read against a live worker satisfies *both* conditions. Test the
liveness arm first and every dropped connection during a takeover is reported as
a driver the caller is not permitted to see: a confident, specific, entirely
invented claim about permissions, from a network failure. Pinned by its own
test, because a reader reordering a `switch` for tidiness would not otherwise
find out.

## The exit code survives the sentence

The refusal wraps with `exitcode.New(exitcode.FromError(aerr), …)`, not a fresh
code. A lost answer stays **7** (`Unavailable`, #394) rather than collapsing to a
generic 1 because we added an explanation — and 1 vs 7 is exactly the
distinction a script branches on: *"the server refused this"* vs *"the answer
never arrived"*, which is what happened.

## Tests, and the mutation that indicted a fixture

Six mutations, each verified applied before its result was read, each red on
exactly the intended tests: the `--force` carve-out removed; each of `drivenBy`'s
two nil-explaining arms dropped; the two arms **swapped**; the degradation
reverted to `return aerr`; and the `last == nil` race guard dropped.

**MUT1's first
pattern matched nothing** — caught because the script asserts the file changed
and aborts otherwise, per `review:a-mutation-check-can-itself-be-a-no-op`, whose
whole subject is that a no-op probe and a sound guard produce identical output.
Redone as an exact-string replacement. Each surviving mutation reds exactly the
tests it should, and they are all tests this change owns.

**The transport failure is simulated by hanging up mid-request** — hijack the
connection and close it — not by returning a GraphQL error body. A fixture
returning `UNAUTHENTICATED` would exercise a branch @Dara measured the server
cannot produce, and the exit-code assertion needs a code that is not 1, or
preserving and collapsing look identical.

**And hanging up made the recorder race.** `review:a-test-double-must-satisfy-the-
real-access-pattern` asks what the *real* consumer does to the double. The
existing `captureGraphQL` writes its capture map from the handler goroutine and
reads it from the test, which is safe **because completing a response orders the
two** — an abort does not, so the client can observe EOF and the test march on
while the handler is still writing. `go test -race` fails on the map version.
Measured rather than argued, and the recorder is now a mutex-guarded accessor.
The pre-existing helper is not affected; its ordering is real.

**One mutation found a defective fixture rather than a defective guard.** The
force test originally omitted `StartTeamSession`, which looks stricter: reaching
the bind would trip the fake server's "unexpected operation". It is weaker.
Removing the guard then failed the command anyway, so `err == nil` — the
assertion carrying the test's actual claim — passed **vacuously**, and the test
reddened only through its exit-code and wording checks, for a reason unrelated
to the guard. With the bind stocked and answering, removing the guard produces a
clean successful takeover, which is the real regression. The test also now
asserts the mutation was not reached at all, since the bind is the irreversible
half.

That is the fourth PR running where **a green (or near-green) mutation was the
finding**: a red confirms a guard you already believed in; a green tells you
which case your fixtures cannot express.

## One existing test INVERTED, deliberately

`TestTeamSessionStartLiveWithNoReadableSessionStillRefuses` (#552) pinned exactly
the fixture this change re-classifies — live + empty list — and required *"an
unknown driver"*. Its stated reason was to match the server's wording so the two
refusals would not describe one situation in two vocabularies.

**That reasoning was right and its premise has moved.** These are two
situations: the server says "an unknown driver" when *it* cannot name the driver;
this fixture is a read that answered against a name the server calls live, which
means the row exists and this caller was not shown it. Two vocabularies for two
situations is the point; the defect was one word for both.

The assertion is inverted rather than deleted, and the forbidden direction is the
load-bearing half — collapsing back to "an unknown driver" is the regression, and
it is the comfortable direction to drift in, because the shorter sentence reads
fine.

## Propagation

- **No spec.** This is a client deciding what to say about a read it no longer
  depends on; `cor:agt:020:03` already governs the override, which is precisely
  why the question went to the coordinator instead of becoming law. Stated as a
  reasoned no rather than left silent.
- **`agentic-usage.md`** gains the new refusal: `--force` fails closed when it
  cannot name whom it displaces, with the read's own exit code, and an empty
  answer is not a failure.
- **#552's plan doc** has one superseded table row, annotated in place rather
  than rewritten — that document records what #552 shipped, and the row is an
  accurate account of it.
- **hadron-docs**: reported, not filed — a new `session start --force` refusal
  and a changed stderr line.
