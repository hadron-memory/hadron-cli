# `worker release` states the hold it classified against

Design-as-built for **#522**, adopting **hadron-server#1084** (`7ad305f`) — the
preventive half of hadron-server#1073, which was itself the P1 raised from PR
#504's review. Third in a line: #511 fixed the refusal, #523 fixed the roster,
this one fixes the write.

## What was wrong

`worker release` performs one of two acts, and which one it is depends on a
fact it reads *before* it writes: is this name held by me, by somebody else, or
by nobody? Releasing my own owes nobody notice. Releasing somebody else's is an
admin force-release that **announces itself in the team chat**.

Between the read that classified and the call that performed, the hold could
move. PR #504 added a re-read to narrow that window and refuse on a change, and
said plainly in its own comment that this **does not close the race**: it
shrinks it from human thinking time to one round trip.

The dangerous case was never the prompted one. It was the *unprompted* one: a
nil hold meant no prompt, and an unconditional release. So a hold taken in the
interval — **or one merely masked from this caller** — was force-released in
silence, and the server announced it in the team chat for an act nobody had been
asked about.

## What shipped

Exactly one assertion travels with every release:

| classified as | asserts |
| --- | --- |
| held by someone (me or another) | `expectedHolderUserId: <that holder>` |
| no hold visible | `expectUnheld: true` |

Never both — the server refuses `BAD_USER_INPUT`. Never neither — that is the
old unconditional call, and it is the thing this removes.

**Enforced by the guarded write**, which takes the asserted value, so there is
no check-then-write window at all. The client-side re-read is gone, and with it
`sameHolder`.

## The refusal is turned into an informed offer, once

Refusing outright is the safe-looking answer and it **breaks a legitimate
operation**. A caller who cannot READ the hold classifies it as nil and asserts
`expectUnheld`; re-running re-derives the same wrong assertion, so they would be
refused forever. The correct admin force-release becomes impossible.

So `WORKER_HOLD_STALE` is caught and re-offered against the truth. The refusal
carries the holder found *now* — information the caller could not otherwise have,
and the server says why it can disclose it: reaching that resolver already
required the read gate the masking rule applies to.

- **the holder is me** — proceed silently. A self-release owes no confirmation,
  exactly as it would not have if the hold had been readable in the first place.
- **the holder is somebody else** — prompt with the truth, then assert *that*
  holder. `--yes` proceeds; no TTY and no `--yes` refuses, exit 5.
- **unheld now** — refuse. The outcome the caller wanted is true, but a
  "released" receipt would claim this command did it, and a force-release
  receipt would claim a chat notice that never happened.
- **stale a second time** — stop. One retry, never a loop; a client racing a
  human is not a fix.

**The receipt gets more honest as a side effect.** On the previously-silent path
`wasHeld` and `forced` were nil and the prior holder unknown, because the CLI
had classified against a hold it could not see. After a retry all three are
*known* — from the refusal payload. That is most of hadron-server#1073's ask (2)
obtained without the breaking payload type it needs.

## What did NOT change, and why

**The retirement re-read stays.** The precondition asserts who holds the name,
not whether the worker is still working, and the confirmation's transfer clause
branches on retirement — "to whoever takes the name next" versus "stays with the
name".

An earlier draft narrowed it to the prompted path, reasoning that only a shown
confirmation can be invalidated. **The repo's own test refused that**: it pins
the refusal under `--yes`, where no prompt is shown at all. Waiving the QUESTION
is not waiving the ACT's meaning — and #522 was asked to remove the hold
mitigation, not to quietly weaken a neighbouring guard while nobody was looking
at it. Reverted.

**`release` still does not use `hasLiveSession`.** #523 established that it is a
sound visibility signal and could disambiguate a nil hold. It is not used here,
and the reason is the better answer: the precondition makes the ambiguity
**harmless** rather than resolving it. A resolved ambiguity is a client-side
inference that can be wrong; an asserted one is refused by the server when it
is. Both designs PR #504 retracted were attempts at *resolving* it.

## What the mutation run caught

Ten mutations, nine red immediately. The survivor was the interesting one:

**`WORKER_HOLD_STALE`'s exit-code mapping is unreachable from this command.**
Deleting the `codeForExtension` case left every command-level test green,
because `worker release` intercepts that code before `MapError` ever sees it.
A line of setup wearing a guard's clothes — **the third instance of that shape
in two PRs**, after `selfKnown` and the `sessionStartWorker` nilling.

Kept rather than deleted, and pinned where it *is* reachable: a row in
`TestMapError` and a `TestWorkerHoldStaleDetail` covering the null-holder
distinction. Exit codes are a documented contract, and a future caller that does
not intercept must still get 5 rather than 1.

**A gap the mutations did not find, caught by reading my own assertions.**
`expectUnheld: null` and an omitted `expectUnheld` both decode to a nil `*bool`,
so a typed test cannot tell them apart — while on the wire they are *different
requests*, which is precisely `findings:optional-arg-meets-presence-semantics`
and the reason the server took two arguments instead of one nullable id. The
test asserted values where the hazard is PRESENCE. It now captures the raw
variables and asserts key presence; dropping either `omitempty` reds it with
`raw: null`.

## Reasoned NOs

- **No spec citation.** This implements `cor:agt:020:09` and hadron-server#1084's
  contract; nothing here decides a rule another repo must match. The retry
  policy — one attempt, then stop — is a client ergonomics choice, and no other
  client has to match it to be correct.
- **hadron-docs follows.** The CLI reference documents `team worker release`;
  its guarantee is now stronger than the re-read's. @Tove's call whether that
  rides with the #523 pass.
- **hadron-server#1073 stays open** for ask (2) — the prior holder in the
  payload — which still needs `ReleaseWorkerPayload` and a coordinated
  three-repo change. This PR gets most of its VALUE from the refusal payload
  without the schema break, which is worth knowing when that change is
  sequenced: the remaining gap is the no-op path, where nothing is refused and
  so nothing is reported.
