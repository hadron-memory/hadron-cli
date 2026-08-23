# HELD is not TAKEN — teaching `session start` the refusal it never learned

Design-as-built for hadron-cli#487, the half of it that ships here.

## The problem, as it stands today

`cor:agt:020:09` gives the platform two refusals where it used to have one:

| | what it means | forceable |
| --- | --- | --- |
| **HELD** | a PERSON holds this name, and only `worker release` ends that | **no** |
| **TAKEN** | a live worker session already exists | yes |

The asymmetry is the point. A hold is not forceable because *the person who
would force is not the person who would lose*, so consent cannot be given by
the party doing the taking. TAKEN stays forceable because, once holding
exists, it is only ever a question about your **own** name.

The CLI knew exactly one of these. Grepping before the change: `WORKER_HELD`
appeared in no non-test Go file — no extractor, no message, no entry in
`codeForExtension`. Three consequences, in ascending order of harm:

1. **The exit code was wrong.** `codeForExtension`'s default is
   `exitcode.Error` (1), so a hold — a state conflict exactly like its
   `WORKER_TAKEN` / `WORKER_RETIRED` / `WORKER_IN_USE` neighbours — reported
   as an unexpected failure. Note it could not ride in on the existing
   `strings.HasSuffix(code, "_TAKEN")` rule either, which is the conflation
   this issue is named after arriving as a bug.
2. **The refusal had no remedy.** The server's message carries one, but the
   CLI wrapped it as a generic error.
3. **The help actively misdirected.** `session start --help` spent a
   paragraph on `--force` as *the* takeover tool and never mentioned that a
   held name refuses it. A driver who meets a hold reads the help and reaches
   for the one flag the spec says cannot work.

(3) is the reason #487 was the hottest item on the pickup list. It is also
the live core of **#492**, whose title — *"a taken worker has one advertised
remedy — `--force`"* — describes today's code precisely, even though its
stated premise (the register holds a free name) died with #500.

## What ships

### The refusal, in one renderer, on two paths

`heldRefusal(name, holder, heldAt)` in `internal/cmd/team/session.go` is
reached from both:

- **the pre-flight**, which reads the hold off the worker row it already
  fetched, and
- **the server's own `WORKER_HELD`**, which is the authority and also covers
  the race the pre-flight cannot close — the hold can be claimed between the
  worker read and the bind.

One renderer rather than two deliberately: the TAKEN pair has a separate
message per path and they have drifted. A caller must not be able to tell
which path refused them, because it is the same fact either way.

The message names the holder, says the hold is not freed by any session
event, rules `--force` out **explicitly** rather than by omission, and gives
both real remedies: cast your own worker, or ask the holder (or an App/org
admin) to release it.

### `api.WorkerHeldDetail`, and the payload asymmetry behind it

Rendered from `extensions`, not from message wording — the same rule
`WorkerTakenDetail` follows. Verified against `hadron-server`'s
`resolvers.mutation.session.ts` rather than assumed, and the verification is
the interesting part: **two server paths raise this code and they do not
carry the same fields.**

| path | `workerId` | `heldBy` | `heldByName` | `heldAt` |
| --- | --- | --- | --- | --- |
| pre-transaction check | ✓ | ✓ | ✓ | ✓ |
| compare-and-set inside the session transaction | ✓ | ✓ | — | — |

The second is not an error case — it is the ordinary refusal of the loser of
a race, and it has no holder read to spare. So `HolderName` and `HeldAt` are
absent on a perfectly normal refusal. `HeldDetail.Holder()` falls back to the
raw user id (still actionable), and an absent `heldAt` drops the "since"
clause entirely rather than rendering an empty one.

### `classifyHold`, and why it has four values rather than two

The pre-flight has to answer *"whose name is this?"* from a row where
`heldByUserId` is **masked to null on deny**. Two unknowns fall on opposite
sides of the question, and collapsing either produces a confident wrong
sentence:

| verdict | evidence | what the CLI says |
| --- | --- | --- |
| `holdNoneVisible` | no hold on the row | nothing about holds — unheld OR invisible, and the CLI must not pick |
| `holdByMine` | hold == my user id | the ordinary TAKEN refusal, `--force` and all |
| `holdByOther` | hold != my user id | the held refusal; no takeover offered |
| `holdUnknownWhose` | held, but identity unreadable | held is a FACT; whose is the open part — say both |

`review:a-claim-must-not-outrun-its-evidence` is the rule this implements.
The two failure directions are not symmetrical and both are real: treating
an unreadable identity as "somebody else's" would refuse a legitimate
self-takeover, while treating it as "mine" would re-offer the flag that
cannot work. The hedge names the uncertainty instead.

An App-key caller resolves to `""`, never equal to a real user id, so it
lands on `holdByOther` — correct, since an App key is not a person and holds
nothing. The server agrees: the missing `ctx.userId &&` guard on that check
was a P1 in hadron-server PR #1052 precisely because it let an App key bind a
human's name.

The pre-flight is a courtesy, never a gate. It runs only on the path that was
already refusing (`active != nil && !force`), so its two extra reads never
touch the hot path, and the server remains the authority.

### Help text

- `session start --help` gains a **TWO REFUSALS** block stating both and
  which one `--force` answers, plus a closing note that binding claims a name
  while casting does not.
- `--force`'s usage says it is *never* for a name held by someone else.
- `--as`'s usage and Long gain the **URN spelling**, which was missing
  entirely though `resolveWorker` dispatches it FIRST and App-independently —
  making it the spelling that still works when App context is wrong or
  missing. (@Tove's find.)
- `worker cast --help` gains the casting-does-not-hold paragraph: a
  coordinator staffing a roster needs to know those names stay unheld, and
  that casting one does not reserve it for themselves.
- `agentic-usage.md`'s session-start section gains the same contract,
  including the exit code and the payload asymmetry.

## Scope: what is deliberately NOT here

#487's other half — **`worker list` shows neither the hold nor whether
anyone is driving** — stays open, and the issue stays open with it.

Two reasons. It is a table reshape in the same family as #486, which is
already open against the adjacent table. And more decisively: **liveness is
not on `Worker`.** The type carries `heldByUserId`/`heldAt` but nothing about
an active session, so the CLI would have to fan out over sessions and join
client-side — while the MCP `list_workers` prints "session live" today, which
means the server computes it somewhere. Building a client-side join that a
server field retires next week is the wrong order. Asked in the team chat
before building.

## Verification

Every guard was mutation-checked — removed or inverted, the test observed
red, then restored. That discipline caught one of its own: the first
mutation of the pre-flight branch was a `perl` substitution whose indentation
did not match, so it silently applied nothing and the test "passed" while
proving nothing. The mutation has to be *seen* to land.

| mutation | test that went red |
| --- | --- |
| pre-flight `holdByOther` branch removed | `…HeldByAnotherRefusesWithoutOfferingForce` |
| post-call `WorkerHeldDetail` branch removed | all three server-refusal tests |
| `WORKER_HELD` case dropped from `codeForExtension` | `TestMapError` |
| `HeldDetail.Holder()` fallback removed | `TestWorkerHeldDetail` |
| "since" clause rendered unconditionally | `…ServerHeldReducedPayloadDegrades` |
| unknown identity collapsed into `holdByOther` | `…HeldUnknownWhoseHedges` |
| remedy ref falls back to the NAME | `…HeldRemedyFallsBackToIDWhenURNIsNull` |
| nil-only URN guard (empty string slips through) | `…HeldRemedyFallsBackToIDWhenURNIsNull` |
| `--app <app>` dropped from the cast remedy | `…HeldRemedyIsRunnableWithoutAppScope` |

The fixtures carry an **explicit** hold (`heldBy("u-dara")`). The shared
`irisWorkerJSON` has no `heldByUserId` at all, so a test built on it would
exercise the unheld branch while appearing to test holding — the same shape
as the omitempty fixture that made six of the previous stint's guards unable
to fail.

## Review round: the remedy was not runnable

Both bots landed independently on the same defect, which is the signal worth
recording: **a remedy is only a remedy if the caller can run it as written.**

The refusal told the holder to run `hadron team worker release <name>` and
told the reader to `hadron team worker cast --name … --role …`. A bare name
resolves only within an App (`cor:agt:020:02`), and `cast` requires an App
context outright. So for a caller who arrived via `--as hrn:worker:…` with no
ambient App — a supported path, and the one `--as`'s own help had just started
recommending for scripts — `release` answers not-found and `cast` refuses
"no team App". **The advice failed exactly the reader most likely to be
reading it**, and the same PR that documented the App-free spelling was
handing out App-dependent remedies.

Fixed by `appIndependentRef` (URN, falling back to the id) for `release`, and
an explicit `--app <app>` in the `cast` shape, since a worker that does not
exist yet has no App-independent spelling.

The general form is worth carrying: **when a message names a command, check it
against the narrowest context that reaches the message**, not the one you had
in mind while writing it.

## Platform specs: no citation proposed

This implements `cor:agt:020:09`; it decides nothing a reader in another repo
must match. The exit-code mapping and the help wording stop at this repo's
edge.

One thing was escalated rather than decided: the **`WORKER_HELD` extensions
payload** is a contract the portal will also render, its two shapes are
undocumented, and `cor:agt:020:09` says the refusal is not forceable without
saying what it carries. That is hadron-server's surface and @Dara's thread —
raised in the team chat rather than minted here.
