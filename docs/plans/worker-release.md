# Design as built: `team worker release` (#495)

Wraps `releaseWorker(workerRef: ID!)` from hadron-server#1050 (holding half,
PR #1052). The contract is `cor:agt:020:09` — *"a name is a person's until
released"*.

## Why this is not an ordinary missing verb

The spec's first clause is the whole argument:

> A worker **name is held by a PERSON** … and **only an explicit release ends
> it** — not a session ending, not an idle window, not an expiry, not a reap,
> not a closed conversation.

`releaseWorker` is the only thing that frees a hold. With no wrapper the CLI
was not merely missing a convenience: **there was no way to perform a
mandatory-by-spec operation** from the surfaces this team uses. MCP has no
release tool either, so the only non-portal path was the raw `hadron api`
escape hatch.

That also left the platform's escape hatch unreachable: an admin force-release
exists so a departed colleague's names are not held forever, and it could not
be run from a terminal.

## The CLI does not decide which act it is

The server gates both paths — the holder path on the holder, the force path on
org ADMIN / user-owned-App owner. The CLI must not reimplement that; a
client-side authority check would be a second, drifting copy of an access rule.

What the CLI owes is **telling you which act you are about to perform**, which
is a rendering question, not an authorization one. It answers it by comparing
the worker's `heldByUserId` against `authContext.user.id`:

| pre-read | act | prompt? |
|---|---|---|
| `heldByUserId` is null | nothing to release | no |
| equals my user id | self-release — nobody is losing anything | no |
| a different user | **admin force-release** — notifies the team | **yes** |

`authContext` rather than `me` on purpose: it is credential-agnostic, so an
App-key caller resolves to a null user. That is the correct answer rather than
an error — per the spec *an App key holds nothing*, so an App-key caller is
never the holder and always lands in the force branch's framing.

## Three things the force prompt must say, not one

The issue asked for "who holds it". The spec adds two more, and both are
invisible from the verb:

1. **Whose name it is.** Resolved to a human name via `GetUser`, falling back
   to the raw id when that read is denied — the `describeApp` pattern, since a
   decoration must never gate the operation.
2. **This posts to the team chat**, naming you and them. Per the spec the
   notification is a *requirement*, not a courtesy: *"the incident this whole
   rule answers is one where notice was owed and there was no way to give it,
   and a silent override rebuilds exactly that."* A caller who discovers the
   post afterwards has been made to do a public act privately.
3. **The worker's working memory and handoff history go with the NAME.**
   Straight from the spec, and the least guessable: "release" sounds like
   giving something up, not like handing someone your notes. It applies to a
   self-release too, so the receipt says it there as well.

## The race the CLI cannot close (hadron-server#1073)

`releaseWorker` takes no precondition and returns the worker **post**-release,
so `heldByUserId` is null by construction. The act therefore has to be
classified from a **pre-read**, and the hold can change in between:

- an admin approves a prompt naming Dara and releases whoever holds it now;
- worse, a pre-read showing *unheld* or *me* skips the prompt **entirely**, so a
  hold taken in the interval is force-released **silently** — the team chat
  records a public act the caller was never asked about, and the receipt calls
  it routine.

The act performed differs from the act described, which is the failure this
whole command exists to avoid.

**Mitigation, not a fix.** The hold is re-read immediately before the mutation
and a change refuses (exit 5). That narrows the window from human thinking time
at a prompt to one round trip, and turns the silent force-release into a
visible refusal. It cannot close the race — nothing outside the server can make
the check and the write atomic.

Filed as hadron-server#1073, asking for either an `expectedHolderUserId`
precondition (the `expectedNames` shape, and what
`review:no-rmw-sugar-over-wholesale-writes` prescribes) or the prior holder in
the payload. The first prevents; the second at least stops the client asserting
a falsehood.

### The receipt does not claim the announcement landed

The notification is **best-effort** server-side — *"an unreachable chat never
blocks the release"* — and the payload carries no delivery signal. So the
receipt says the server *posts* a notice, not that one *appeared*.

The PROMPT keeps the strong wording (`POSTS TO THE TEAM CHAT`). The two have
different jobs: a warning about what an act **is**, before you consent, errs
toward caution; a report of what **happened** errs toward accuracy.

This was the third instance of the same rule on one PR — after the nil-holder
claim and the `forced` classification. hadron-server#1073 also asks the server
to report whether it posted, which would let the CLI stop hedging and say.

## Decisions worth the words

### The no-op is reported, but not as "not held"

The mutation is idempotent, so releasing an unheld worker succeeds. Printing
`✓ released` there would be a false receipt for a no-op — the same rule the
retired `role names add` sugar followed when its edit turned out to be a no-op.

It does not say "was not held" either; see the ambiguity section below. The
receipt is *"no hold on X was visible to you"*, `status: "no-visible-hold"`.

### The nil-holder ambiguity does not collapse, and cannot be probed either

`heldByUserId` is **masked to null on deny**, so a nil pre-read means "unheld"
or "held, and not visible to you".

Two attempts to resolve that, both wrong, both caught in review:

1. **An argument.** The receipt prints after a *successful* release, and a
   success means the caller was the holder or an admin — both of whom can read
   the field. False: the mask exists so a **former App member** cannot read
   staffing, and a former member can still BE the holder. They pass the release
   gate and fail the read gate.
2. **A probe.** `heldByUserId` is masked alongside
   `prompt`/`promptOverride`/`memoryId`, so any of those being readable proves
   the gate is open. Also false: all three are legitimately nullable — an agent
   with no template, no per-worker override, and a best-effort memory provision
   that failed. The repo's own `retiredWorkerJSON` fixture has exactly that
   shape, so a real, visible, unheld worker read as "cannot see" and got hedged
   output plus a spurious prompt.

There is **no explicit visibility signal on `Worker`**, so the ambiguity is
irreducible from here. The command therefore stops trying to resolve it and
reports what it knows:

- human: *"no hold on Iris was visible to you — nothing was released that you
  could see"*
- `--json`: `status: "no-visible-hold"`, with `wasHeld` and `forced` both
  **null** rather than a guessed `false`
- **no prompt** — prompting on every idempotent no-op would spend the case #495
  asked to keep quiet, in exchange for a guess

The distinction earns its extra words because of what a reader *does* with it:
"was not held" reads as *"this name is free to bind"*, and a caller acting on
that meets `WORKER_HELD` at the next `session start`.

hadron-server#1073 asks for the prior holder in the release payload, which
resolves this outright — the receipt could then report what happened instead of
predicting it.

### "Cannot tell" is a third answer, not a `false`

`currentUserID` returns three states: the id, "" for a caller that
**definitively** has no user (an App key — which per the spec holds nothing),
and *unknown* when the AuthContext read itself failed.

Collapsing the last two was the first version, and it is the "unknown is not
none" mistake again: a failed identity read is not evidence that the caller is
somebody else. It reclassified a legitimate self-release as a force-release —
refusing it non-interactively, and then reporting `forced: true` plus a
team-chat announcement the server never made.

So `forced` is `*bool`. Null propagates to `--json`, and the human receipt says
in words that the act could not be classified. The prompt still fires (we
cannot rule out a public act) but explains *why* it is asking, because a prompt
that cannot justify itself is one people learn to `--yes` past.

### No `--yes` on a self-release

`--yes` gates the force branch only. A self-release loses nothing and notifies
nobody, and a prompt on the ordinary end-of-work step is the kind of friction
people learn to `--yes` past reflexively — which then also skips the force
prompt that matters. Non-interactive callers are unaffected on the self path.

### `heldByUserId`/`heldAt` join the shared fragment, and only `get` renders them

They are added to `WorkerFields` and to `workerDTO` — **additive** `--json`
keys, not a break — because `release` needs the pre-read and the data is what
#487 wants.

`worker get` renders the holder — only when it is KNOWN, since a `held by: —`
line would answer "nobody" to a caller who merely cannot see. **`worker list` is
deliberately untouched.**
The table has column pressure, and "which worker surface shows who is driving"
is #487's design question. Adding a column here would pre-empt it with a choice
made in passing.

## Also in this change: one word covering two states

`agentic-usage.md` and `session.go` both said:

> Closing a chat session does NOT release the worker; only `session end` does.

Accurate about **taken** when written. Since #1050 made *release* a term of art
for clearing the **hold**, it now reads as though `session end` clears a hold —
the one thing the spec says no session-lifecycle event may do. Reworded to name
the state each sentence is about.

This is the same defect #487 tracks: one word for two states with different
remedies. Fixing the sentence is not fixing #487, and it is called out as such
so nobody reads it as closed.

## Verified live, and what was not

Against `srv.hadronmemory.com` on the real team App:

- `worker get --json` carries `heldByUserId`/`heldAt` — Jonas reads as held
  since 2026-08-19.
- The **no-op** branch end to end, on two genuinely unheld workers:
  `· no hold on Mira was visible to you — nothing was released that you could
  see`, `--json` `status: "no-visible-hold"`, and Mira unchanged afterwards.
- `make unbound-ops` drops `releaseWorker` from the unwrapped list (87 → 86),
  which is the gate that surfaced this gap recording it closed.

**The self-release and force branches were NOT exercised live, deliberately.**
Every worker on that App is held by the same person, so from any credential
here a release takes the self path and would really drop a hold on shared team
state — someone else could then bind Jonas or Ada. That is an outward-facing
change to other people's work, not a test fixture. The force branch is
unreachable live for the same reason: there is no second holder to force
against.

Both are covered by tests, and every guard is mutation-checked. Saying which
paths the live run did and did not cover matters more than the count: "verified
against the live server" would otherwise imply the mutation's interesting
branches were exercised, and they were not.

### `session start` does not report the hold at all

`startSession` **claims** the hold for a person, but `session start --json`
builds its worker object from the PRE-mutation read — so carrying the hold
there reports `heldByUserId: null` immediately after the bind that set it.

Omitted rather than re-read: `session start` reports the session it just
created, not current staffing, and a round trip on the hot path to decorate a
field nobody asked for is the wrong trade. Omitted rather than **nulled**, too
— a null asserts "unheld", which is the claim this command spent a review
learning not to make. `worker get` is the staffing read.

The same reasoning gives `heldByUserId`/`heldAt` `omitempty` on `workerDTO`:
absence uniformly means *not asserted*.

## Not done here

- ~~**`session start --help`** presents `--force` as *the* takeover tool while
  `WORKER_HELD` is not forceable, and nothing says casting does not hold.
  That is #487's, and wants its design rather than a patch.~~
  **Done** — [held-is-not-taken.md](held-is-not-taken.md) (#487). It did want
  its design: the refusal itself was unwired end to end, not just undescribed.
  #487 stays open for its other half, the `worker list` columns.
- **An MCP `release` tool.** Server-side, and the team-features rule says the
  logic belongs there so MCP and the portal get it too — the CLI wrapping a
  mutation does not give MCP a verb.
