# `worker list` answers who holds a name and whether anyone is driving it

Design-as-built for **#487's remaining half**, against **hadron-server#1086**
(`427ab04`). The refusal half shipped in #511 —
[`held-is-not-taken.md`](held-is-not-taken.md) — and this is its bookend: that
one fixed what happens when you *hit* a hold, this one fixes the surface you
consult *before* you do.

## What was wrong

Ada filed #487 after reading a roster and concluding a worker was active. She
was reading a `✓` that meant "this name is allocated", and inferring the
meaning she had met first: "someone is driving this". Same word, two facts,
opposite time horizons.

Checking the misreading turned up the worse half. Dara had two sessions ever,
lasting 2m26s and 41s, both ended — and ten issues dispatched to her, all
untouched. **A coordinator can dispatch into a channel nobody reads and get no
signal at all**, because a casting nobody has ever picked up renders identically
to one worked yesterday on every surface we have.

`worker list`'s columns were `WORKER / ROLE / RETIRED / URN / ID`. It answered
neither question.

## Why it waited

The hold (`heldByUserId`/`heldAt`) was already on `Worker` and easy. **Liveness
was not.** Nothing on the type said whether a session was open, so the CLI would
have had to fan out over sessions and join client-side — while MCP's roster
rendered it today, which meant the server computed it somewhere. Building a
client-side join that a server field retires next week is the wrong order, so
this half was parked on a question to hadron-server rather than on effort.

Dara's answer was a `Worker`-level field, and it is the whole reason this
change is small: `hasLiveSession` and `lastActiveAt`, additive, no fan-out.

## The three semantics that decided every render

None of these are inferred here; all three are the server engineer's, stated on
#487 and verified against `resolvers.worker.ts` and `lib/sessionActivity.ts`
before being relied on.

1. **`hasLiveSession` is not availability.** Availability is the HOLD
   (`cor:agt:020:09`). Liveness says only that a session is open, and a worker
   session outlives the chat session that started it — so `live` is never a
   claim that a person is at the keyboard. A column that lets liveness read as
   availability re-files the issue it closes.
2. **`lastActiveAt: null` on a permitted read means never driven.** That is the
   state the issue exists for.
3. **The instant deliberately under-reports on a reaped session.** The reaper
   ends an idle session with an `updateMany` and Prisma's `@updatedAt` moves to
   the reaping instant, so the last real heartbeat is overwritten and
   unrecoverable; the derivation drops it, falls back to `startedAt`, and caps
   everything at `endedAt`. An age that looks older than expected is this, and
   it is the safe direction — abandoned reads as MORE idle, never less.

> **(3) is read from the resolver, not from the field's SDL description.** That
> description omits both guards and closes with a clause that is wrong on
> exactly the reaped case (*"agrees with the idle window the reaper acts on"*).
> @Tove found it from hadron-docs (team chat seq 273) and it is hadron-server's
> to fix; `schema/schema.graphql` here now carries the stale text too, because
> the snapshot is generated. **Do not write help text from it.**

## What shipped

`WORKER / ROLE / HELD BY / LAST DRIVEN / RETIRED / URN / ID`

```
WORKER  ROLE              HELD BY            LAST DRIVEN  RETIRED  URN     ID
Iris    backend-engineer  you                live         —        …       wkr1
Dara    backend-engineer  Dara Holt (@dara)  3d ago       —        …       wkr2
Mira    backend-engineer  nobody             never        —        …       wkr3
Pia     backend-engineer  ?                  ?            —        …       wkr4
```

Four decisions worth recording:

**One column, not two.** `LAST DRIVEN` carries liveness as a *value* (`live`)
rather than getting its own `ACTIVE` column. A separate column reads as
availability, which is the HOLD, which is the conflation being removed. And
there is **no age beside `live`**: an age there invites "driven 4h ago, so is
someone there?" when the honest answer is that a session is open and nobody may
be at the keyboard. Both are how MCP's roster renders it; two rosters answering
one question in two vocabularies is itself a #487.

**The holder is named, not printed as a uuid.** `worker get` resolves the same
id to a name, and one entity answering two surfaces two ways is what an agent
has to special-case (the asymmetry #520 was reviewed for, one command over).
Resolved once per DISTINCT holder, never for the caller's own hold, and only on
the human path — `--json` pays for none of it. Decoration, never a gate: a
failed lookup falls back to the raw id, and that answer is memoized too so a
broken lookup is not re-paid per row.

**`?`, not `—`, for a masked read.** See below.

**`--json` always carries `hasLiveSession` and `lastActiveAt`,** never omitted,
unlike the `omitempty` on `heldByUserId`/`heldAt` beside them. An absent key
cannot carry a discriminator: absent is indistinguishable from a client that
never asked.

## The discriminator, and the claim it rests on

The repo has documented for two PRs that the hold's null is **irreducibly**
ambiguous — "unheld OR not visible to you" — because *there is no visibility
signal on `Worker`*. #1086 introduced one, without meaning to.

`hasLiveSession`'s resolver masks to null on deny and otherwise coalesces to
`false` (a worker with no sessions answers `false`, never `null`), so **the only
null path is the read gate** — and it is the same `maySeeWorkerInternals(appId)`
predicate that masks `heldByUserId`, `heldAt`, `memoryId` and `promptOverride`,
memoized per request per App. So on a row where `hasLiveSession != null`, every
other null is a genuine absence.

That is what makes `nobody` and `never` sayable at all. Without it this change
could not have shipped its main feature: `never` would have been
indistinguishable from `unknown`, which is the ambiguity the issue is about.

**Stated as an inference, because that is what it is.** Both fields document
their own masking; *"these two mask together"* is read from the implementation
(`hadron-server` main @ `e5c6ff2`) and is not stated as a contract anywhere. It
is asked of hadron-server rather than assumed permanent, and
`workingStateVisible` is the single place that has to change if it stops being
true.

**Deliberately NOT widened into `worker release`,** which still hedges ("no hold
was visible to you") and could now classify precisely. That is a separate
change: #504 retracted two designs in that area, and #1073's ask (2) is already
open across three repos. Reported rather than fixed in passing.

## `?` rather than `—`, and how that was found

The masked cells first rendered as `—`, on the reasoning that the table already
uses it for "no value" and a second glyph asks a reader to learn a vocabulary to
be told nothing.

Looking at the rendered table killed that argument. The adjacent `RETIRED`
column renders `—` for a **definite** answer, so a masked row was three dashes
in a row, reading as *unheld, never driven, not retired* — two settled facts the
server had refused to state, spelled in the vocabulary the table uses for facts.

**That is this issue's own defect, one level in, in the change that fixes it.**
And it was invisible to every assertion in the suite: the tests named the cells,
and each cell was individually correct. It showed up in one glance at the
output. That is @Gil's `restructure-leaves-its-prose-behind` lesson in its
general form — *structured assertions confirm the thing you thought to name and
say nothing about what else is on the page* — and the pin against it now is an
assertion that `unknownCell != dash(nil)`, which reds if the em-dash comes back.

## What the mutation run caught

Twelve mutations. Ten red as intended. **Two did not, and both were real.**

1. **`renderHeldBy` took a `selfKnown` flag with no constructible failing
   input.** `currentUserID` returns `("", false)` on a failed lookup, so
   `selfID != ""` already implies it. The three-state result is load-bearing
   where an act is CLASSIFIED — `worker release` must not reclassify a
   self-release as a force-release on a failed self-read — and collapses safely
   on a display cell. It was a line of setup wearing a guard's clothes.
   Removed; then `selfID != ""` turned out to be unreachable for the same reason
   (an empty holder has already returned "nobody"), and went too, with the
   coupling written down.
2. **Column ORDER was not pinned.** Reordering the header to put `LAST DRIVEN`
   last passed everything. `HELD BY appears somewhere` is satisfied by it
   sitting behind the URN and the id, which is most of the defect still present
   for a coordinator scanning the table. Now pinned as a full header sequence
   by position — the same loose-assertion class that #521's review found twice
   in my own tests.

**Both directions are pinned**, per `a-tidy-argument-is-not-a-checked-one`'s
last item: the lazy correction to an over-confident claim is to go maximally
uncertain, and *"`workingStateVisible` always returns false"* reds five tests
rather than passing quietly as caution.

## What the review flow added

`review:entity-fields-not-display-labels` asked for a row this change had not
covered: **a holder whose display fields are all null.** `describeHolder`
already falls through to the id, so the behaviour was correct — but nothing
pinned it, and the consequence is worse in this column than in the ones that
rule was written for. A label helper returning `""` leaves a BLANK cell, and
blank in `HELD BY` does not read as "no label": it reads as the value directly
above it in the same column, **nobody**. An unreadable name would render as a
free name. Now tested, and the mutation shows exactly that empty cell.

The `--json` side passes as-is: `heldByUserId` carries the actionable id rather
than a rendered label, matching the spelling `worker get` has emitted since
#504 — and widening it to a user DTO now would break that surface's contract
and split the two apart.

## What the PR review found

**@codex, P1, and it was mine.** `session start --json` embeds the worker built
from the **pre-mutation** read. Adding the activity pair to `workerDTO` meant a
successful bind reported `hasLiveSession: false` — **the negation of the
operation that just succeeded, inside the document reporting its success** — and
a `lastActiveAt` predating the stint.

`sessionStartWorkerDTO` already stripped the hold pair for exactly this reason
(#504), and its doc comment says so at length. **I added two fields with the
same hazard directly beneath a comment explaining the hazard.** The other four
`workerDTO` call sites are fine — `cast`, `retire` and `release` all report
post-mutation rows, and `get` is a fresh read — so the finding is precisely one
site wide.

Fixed by omitting the pair, not nulling it: `null` on these two is load-bearing
on `worker list` (it is the gated-read signal), so publishing null here would
give a **false account of why the value is missing**. The keys are dropped via a
`sessionStartWorker` struct that shadows them with `omitempty`.

**And the fix's own mutation run found a dead line.** Reverting the reported bug
— removing `dto.HasLiveSession = nil` — reds *nothing*, because the shadowed
outer fields decide the JSON by themselves. The nilling is invisible to every
wire assertion.

It is kept rather than deleted, because the two mechanisms answer different
questions — the **shadow** decides omitted-vs-null, the **nilling** decides
null-vs-a-stale-value that a Go caller reading the embedded struct would see —
but keeping it required making it falsifiable, so there is now a package-level
test asserting the embedded fields directly. All three mutations red:
bug-reverted, shadow-removed, hold-nilling-removed.

That is the second instance in this PR of the same shape: **a guard whose
failing input cannot be constructed is a line of setup.** The first was
`selfKnown`. Neither was found by reading the code.

### Round two: three findings, two real

**@codex P2 — the agent contract contradicted itself,** and it was my own
expired-caveat trap firing on me. `agentic-usage.md` still said *"there is no
visibility signal on `Worker`"* in the `worker release` section while the
section I added said `hasLiveSession` is exactly that. Two mutually exclusive
instructions in one document that agents act on. The fix is the honest
distinction: `release` **chooses** not to disambiguate; the ambiguity is no
longer intrinsic.

Reading that paragraph to fix it turned up a **second** false sentence I had
not been told about: *"`releaseWorker` has no precondition (hadron-server#1073)"*
stopped being true when #1084 merged. The server has one; this CLI has not
adopted it (#522). Corrected in the same pass rather than left because nobody
flagged it.

**@copilot — the fixture emitted a row the server cannot.** `workerWith` always
stamped a non-null `heldAt`, including on the unheld and masked rows, against a
documented invariant (*"`heldAt` is null exactly when `heldByUserId` is"*, and
the whole working-state group masks together). Nothing depended on it today,
which is precisely why it was worth fixing: it looked like coverage.

The helper now **refuses** an impossible combination rather than silently
correcting it — a silent correction would leave a future author believing they
had covered "held but masked" while the fixture tested something else, which is
the same hiding the finding is about. Proven by asking for the impossible shape
and watching it fail.

**@copilot on `time.RFC3339` — declined, in writing.** The claim was that the
layout's missing fractional part makes `...:05.123Z` fall through to the raw
string. Go's parser accepts a fractional second even when the layout omits one;
measured on go1.26.4 across `Z`, an offset, and 1–6 digit fractions, with
`RFC3339Nano` returning identical instants. The suggested swap changes nothing.

But the finding was **right about the stake and nothing pinned it**: the
fallback path is real, and if parsing ever narrowed, every age would quietly
become a raw timestamp with no test to notice. So it bought a test rather than
the edit — and the mutation that simulates its premise reds it.

## Prose swept

Three caveats expired in this commit, each written as *"not yet — tracked as
#487"*, which is the shape that is a promise to come back and delete it:

- `team.graphql` — *"NOT added to WorkerRosterFields: which surface shows who is
  driving is #487's design question."*
- `team.go`'s package doc — *"'Taken right now' derives from an active Session"*,
  the exact word the issue is about.
- `team_worker_portal_url_test.go` — *"the table #487's remaining half still
  wants HELD and LAST DRIVEN columns in."*
- `agentic-usage.md` — *"`worker list` is unchanged; which surface shows who is
  driving is hadron-cli#487"*, plus the `worker get` hold paragraph, which
  became half-true once that read learned to say "nobody".

## Reasoned NOs

- **No spec citation.** The columns and glyphs are this client's rendering of
  existing contracts (`cor:agt:020:09` on held-vs-live, `cor:agt:020:03` on
  liveness from the session lifecycle). Nothing here decides a rule a reader in
  another repo needs. **But one thing nearby might**, and it is hadron-server's
  to mint, not mine: *"a reader must be able to tell a masked working-state null
  from an absent one"* — the discriminator above — is currently true by
  implementation and stated nowhere.
- **No hadron-docs change from me.** The CLI reference's `worker ls` section
  follows this merge; @Tove deliberately held it back rather than documenting
  columns nobody could see (`conventions:specs-may-precede-code-instructions-may-not`).
- **No dependent-repo issue.** hadron-server's half is merged; the SDL
  description defect above is reported, not filed by me, since @Tove found it
  first and it is already with @Dara.
