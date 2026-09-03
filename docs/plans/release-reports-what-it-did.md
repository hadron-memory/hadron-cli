# `worker release` reports what the server DID, not what the client predicted

Design-as-built for the CLI's adoption of **`ReleaseWorkerPayload`**
(hadron-server#1073, shipped in #1124 / `465c0c6`).

The bookend to [`the-act-performed-is-the-act-described.md`](the-act-performed-is-the-act-described.md).
That one made the *act* honest — the release asserts the hold it was classified
against, and the server refuses if that is not the hold it writes. This one
makes the *report* honest, and it is mostly a **deletion**: two client-side
classifications go away, and with them the two hedges this command published.

## What the command used to do

Everything the receipt said was derived from a **pre-read** the client made
before the call:

| reported | derived from | failed when |
| --- | --- | --- |
| `wasHeld` | `heldByUserId != nil` on the pre-read | the hold was masked — so it was TRUE-or-NULL and *never false* |
| `releasedFromUserId` | the same pre-read | ditto: unknowable when masked |
| `forced` | `*priorHolder != me`, against its own identity read | that read failed — so it was nullable |

Both hedges were correct answers to real ambiguities. `heldByUserId` masks to
null on deny, so a nil pre-read means *"unheld OR held and invisible to you"*,
and publishing `wasHeld: false` there would have told a caller the name was free
when the next `session start` answers `WORKER_HELD`. Publishing
`forced: false` on a failed identity read would have claimed a private act;
`true` would have claimed an announcement.

**And they were worst on the path that matters most.** After a
`WORKER_HOLD_STALE` refusal the pre-read is, by construction, the thing the
server had just rejected — so the retry carried its own `holder` and `forced`
back through an `informedRelease` struct, precisely because the ordinary source
was known to be wrong there.

## What the payload changed

```graphql
releaseWorker(...) {
  worker { ...WorkerFields }
  releasedFrom { id handle urn name }
  forced
  notified
}
```

- **`releasedFrom`** — the holder the WRITE ended, from inside the guarded
  write. Non-null exactly when something was released.
- **`forced`** — computed server-side against the authenticated principal.
- **`notified`** — three-valued: no notice owed / posted / **owed and failed**.

So the two hedges are **retired because the ambiguity is**, not because we
decided to live with it. The adoption is:

- `wasHeld` → a definite `bool` (`releasedFrom != nil`)
- `forced` → a definite `bool` (the server's)
- `releasedFromUserId` → same key, same meaning, new **provenance**
- `+ releasedFrom`, `+ notified`
- `status` — **unchanged**, and see below
- `informedRelease` → **deleted**; the retry returns only the response

### The client still predicts — for one job only

`forced` survives locally, and it is worth being explicit about why, because the
two used to share one nullable value:

- **Predicting** the act decides whether to **ASK**. It must stay conservative
  when identity is unreadable: the holder may be somebody else, so it prompts.
- **Reporting** the act is the server's. The path where this client could never
  classify is exactly the path where the server always can.

The prompt keeps its hedge. The receipt no longer has one.

## Breaking `--json` changes, stated rather than buried

**TWO, and only the two that could not be avoided:**

1. `wasHeld`: `true | null` → `true | false`
2. `forced`: `true | false | null` → `true | false`

For both, the `null` a consumer handled **can no longer occur** — that branch
detected an ambiguity that no longer exists.

**A third was proposed and withdrawn**: renaming `status` from
`"no-visible-hold"` to `"nothing-released"`, because the old name hedges about
VISIBILITY while the answer is now about the ACT. @codex filed it P1 and was
right. A status is a **branch key**, so a rename is the one change that fails
without erroring anywhere — an agent matching the documented literal silently
takes no branch. And the improved evidence was already exposed through
`wasHeld`, `releasedFrom` and `notified`, which are *new* keys nobody is
matching yet, so the rename bought a consumer nothing it could not already read.
The literal stays; its meaning got stronger and its spelling did not.

That is the counterweight to `review:sweep-the-subject-not-the-removed-rule`,
which says a stale LABEL misleads. It does — and for a label that is also a
machine contract, the cost of correcting it lands on a different party than the
cost of leaving it. Prose gets corrected; identifiers get versioned or kept.

`releasedFromUserId` was deliberately **not** folded into `releasedFrom`: agents
parse it, its meaning did not change, and it mirrors `releasedFrom.id`.

## `releasedFrom` is a projection, and the CLI must not widen it

`ReleasedFromUser` is a separate type from `User` on the
`cor:acl:080:02` / `PublicOrganization` precedent: a field returning another
entity's object hands over that entity's field resolvers, and `User`'s
default-resolved tail (`policy`, `identityProvider`, `externalId`, `githubId`,
`roles`, `maxReferrals`) carries no gate of its own.

The operation selects **four fields and no more**, and the DTO mirrors exactly
those. Selecting further would ask for the door to be reopened from this side.

**`name` is gated and its absence is the ORDINARY case here**, not an edge:
`leaveApp` deletes the AppMember row while the hold survives, so the departed
colleague this admin path exists for is precisely the person whose name is
withheld. The receipt falls back to `@handle` — and then to the **id**, because
`handle` is nullable too and a bare `@` reads as a rendering bug rather than as
missing data. `--json` passes every null through, so a consumer can tell "not
permitted" from a name that happens to equal the handle.

## Fewer round trips, as a consequence rather than a goal

The receipt used to call `describeHolder` — a `GetUser` — to name the person.
It no longer does: carrying the identity is what the projection is **for**, and
on this command's own path the caller may have no other route to that user. A
test pins that `GetUser` is not called.

## Tests

The fixtures carry most of the diff, and the theme is the one #550 just paid
for: **a fixture must describe a server that can exist.**

- `releasePayload(worker, releasedFrom, forced, notified)` builds the payload;
  `notified` is raw JSON because the field is three-valued and a Go `bool`
  parameter could not express the state that matters most.
- `releaseStubs` **derives** the payload from the worker it is stubbed beside —
  whoever the fixture says holds the name is whom the release ends, and `forced`
  follows. A stub that named a different holder would let the receipt disagree
  with the story the test tells.
- The stale-path sequences build a payload per scenario for the same reason.
- `nameWithheld` produces the departed-colleague shape.

New coverage: a **failed** notice; the handle fallback in both `--json` and the
human receipt; and the sharpest one —
`TestWorkerReleaseReportsAHolderTheCallerCouldNotSee`, where the caller cannot
read the hold and the receipt names the person anyway. That case was
*unreachable* before this change.

### Mutations, and the green that was a finding

Seven, each confirmed to have applied by reading the mutated lines back before
the result (the last two added in review):

| mutation | reds |
| --- | --- |
| human receipt classifies from the client prediction | 1 — the unreadable-identity retry |
| `--json` `forced` from the client prediction | 3 |
| `releasedFrom` from the pre-read | 8 |
| `notified` collapsed to "posted" | 1 — precisely the failed-notice test |
| **handle fallback deleted** | **0 — GREEN** |
| the re-hold branch deleted | 1 |
| the id rung deleted | 1 |

The last one **landed and passed**, which is a finding rather than a formality:
every other fixture has a visible name, so the human receipt's fallback had no
coverage at all and would have shipped *"force-released Iris from "* — a
sentence with a hole where the person goes, on the path this command exists for.
Coverage added; the mutation now reds exactly that test.

Worth noting the first row too: reverting the core of the adoption reds only ONE
test, because in every ordinary fixture the client's prediction and the server's
answer **agree**. The tests that can tell the two apart are the ones where the
client is blind — which is also the only place the old code was wrong.

## Review

Three findings, all valid, and two of them corrections to claims I had written
into the diff.

**@codex P1 — the `status` rename.** Withdrawn; see above. I had flagged it in
the PR body as the weakest of the three breaks and said I would revert it on
request. This was the request, better argued than my own hedge.

**@codex P2 — the receipt promised availability after a re-bind.** The payload's
worker is re-read *after* the write and *before* the notice, so its hold is
current state: a non-null holder there is a LATER hold, never a failed release.
**My own query comment says exactly that**, four lines from the code that then
printed *"anyone may bind it now"* regardless. The response in hand contradicted
the sentence.

That is the **third** correction to this one clause — it over-promised for a
retired worker (PR #504) and before that claimed a chat post it could not
observe. Every unverifiable claim this command has shipped has been in the part
that tells the reader what to do NEXT, which is worth knowing as a place to look
rather than as three separate fixes.

Handled as two independent tests rather than a precedence order, because
retirement and a re-hold are independent facts: ordering them would have made
the retired branch assert *"the name is simply no longer held"* while the
payload said otherwise — one false promise swapped for another.

**@copilot — `handle` is nullable and I claimed it was not.** The comment said
*"the one identifier always present for a real user"*; the schema says `String`,
and `urn` documents its own null as *"a handle-less user"*. What `handle`
actually survives is the **#384 name gate**, not existence — so the fallback has
**three** rungs, and the id is the one that cannot be absent. A bare `@` would
have read as a rendering bug rather than as missing data. Corrected in all three
copies plus the agent contract.

**Second round: both bots, independently, on the fix itself.** The re-hold fix
handled only the NON-NULL direction, on the reasoning that a nil hold still
means *"unheld OR masked from you"* so a hedge would cost the ordinary case.

That reasoning is **pre-#487**. `hasLiveSession` has been the visibility signal
for this whole field group since then — masked to null on deny, coalesced to
false otherwise — so masked and unheld *are* distinguishable, and the hedge is
paid only where it is owed. I wrote a constraint from the world before the field
that removes it: `review:a-gate-can-outlive-its-premise` applied to a claim
rather than to a gate, and the second time in two PRs that a sentence of mine
carried a retired premise forward.

The clause now composes three independent facts rather than ordering them:
**retirement** decides whether anyone CAN bind, the **hold** whether anyone HAS,
and **visibility** which of those two we are entitled to say at all. Ordering
them is what made the previous round's fix assert *"the name is simply no longer
held"* while the payload said otherwise.

Three more mutations, each confirmed to have landed, each redding exactly its
own test: the re-hold branch, the id rung, and the visibility gate.

## Propagation

- **No spec.** #1073's contract is the server's and `cor:agt:020:09` already
  governs holding. @Ada has asked @Dara to draft the projection rule
  (*a surface names a principal by projection, not by returning the entity
  type*) — that is a server-side enforcement rule, not a CLI one.
- **hadron-docs**: reported, not filed — `worker release --json` gains
  `releasedFrom`/`notified` and loses two nullables.
- **`make schema` truncates its own snapshot on a failed export** — hit while
  refreshing for this change, filed separately.
