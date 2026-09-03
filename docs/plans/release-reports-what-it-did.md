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
- `status: "no-visible-hold"` → **`"nothing-released"`**
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

Three, all in one PR deliberately — the adoption is where a break belongs, and
dribbling them across releases is worse:

1. `wasHeld`: `true | null` → `true | false`
2. `forced`: `true | false | null` → `true | false`
3. `status`: `"no-visible-hold"` → `"nothing-released"`

For (1) and (2) the `null` a consumer handled **can no longer occur** — that
branch detected an ambiguity that no longer exists. (3) is the weakest of the
three and the one I would revert on request: the old value was not false, it
hedged about VISIBILITY when the answer is now about the ACT, and leaving a
label that names the retired criterion is the stale-label failure
`review:sweep-the-subject-not-the-removed-rule` is about.

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
withheld. The receipt falls back to `@handle`; `--json` passes the null
through, so a consumer can tell "not permitted" from a name that happens to
equal the handle.

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

Five, each confirmed to have applied by reading the mutated lines back before
the result:

| mutation | reds |
| --- | --- |
| human receipt classifies from the client prediction | 1 — the unreadable-identity retry |
| `--json` `forced` from the client prediction | 3 |
| `releasedFrom` from the pre-read | 8 |
| `notified` collapsed to "posted" | 1 — precisely the failed-notice test |
| **handle fallback deleted** | **0 — GREEN** |

The last one **landed and passed**, which is a finding rather than a formality:
every other fixture has a visible name, so the human receipt's fallback had no
coverage at all and would have shipped *"force-released Iris from "* — a
sentence with a hole where the person goes, on the path this command exists for.
Coverage added; the mutation now reds exactly that test.

Worth noting the first row too: reverting the core of the adoption reds only ONE
test, because in every ordinary fixture the client's prediction and the server's
answer **agree**. The tests that can tell the two apart are the ones where the
client is blind — which is also the only place the old code was wrong.

## Propagation

- **No spec.** #1073's contract is the server's and `cor:agt:020:09` already
  governs holding. @Ada has asked @Dara to draft the projection rule
  (*a surface names a principal by projection, not by returning the entity
  type*) — that is a server-side enforcement rule, not a CLI one.
- **hadron-docs**: reported, not filed — `worker release --json` gains
  `releasedFrom`/`notified` and loses two nullables.
- **`make schema` truncates its own snapshot on a failed export** — hit while
  refreshing for this change, filed separately.
