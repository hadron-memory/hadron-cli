# `team chat read` walks backward

Design-as-built for **#548** — the CLI half of `beforeSeq`
(hadron-server#1116), against the snapshot refreshed in #554.

## What was missing

`beforeSeq` shipped server-side on three surfaces — `chatHistory`,
`teamChatMessages` and `hadron_team_chat_read` — and the CLI had none of it.
`--since` walks forward and, on its own, reads to the end, so **the whole
history was reachable only by asking for all of it at once.**

The motivating incident is @Ada's, at team-chat seq 383: catching up on a chat
with real history, her `sinceSeq: 0` read exceeded the MCP result ceiling and
was spilled to a file she then paged through with a script — *"it works, it is
undignified"*. I hit the same wall opening this very session.

## The shape

Two cursors, opposite directions, and **either bound makes the read ONE page**:

| flag | direction | effect |
| --- | --- | --- |
| `--since <seq>` | forward | alone: pages to exhaustion (unchanged) |
| `--before <seq>` | **backward** | the page immediately before that seq |
| `--limit <n>` | — | page size; giving it bounds the read |

`--json` gains **`prevBefore`** — the lowest seq returned — mirroring
`nextSince`. Pass it as the next `--before` to keep walking. The two cursors
compose, per the SDL, so `--since 300 --before 340` reads a bounded slice.

**Nothing about the unbounded read changed**, which is what makes this additive.

### Why `--limit` bounds rather than merely sizes

A flag whose effect depends on another flag being present is a shape this repo
has already filed twice. `--limit` earns its own meaning: an exhaustive read has
no page size a caller can observe, so a `--limit` that only tuned invisible
pages would be a flag with no effect. Bounding is the observable thing it does.

## Two traps built against rather than discovered

**`total` is CURSOR-SCOPED under `beforeSeq`, and the SDL does not say so.**
@Gil found it on hadron-portal #799 (filed as hadron-server#1121). Measured
again here against this surface rather than inherited — the dev-team chat at 435
messages:

```
no cursor      → total 435
beforeSeq: 400 → total 399
beforeSeq: 394 → total 393
```

So the obvious reading — *"how many exist"* — is wrong on every cursored page. A
client comparing it against what it has displayed concludes it has reached the
beginning while messages remain.

`chat read` therefore **neither publishes `total` nor decides anything with
it**: `prevBefore == null` on an empty page is the only end-of-history signal
offered, and an empty page cannot be scoped wrong. The field stays in the
operation because the unread-count nudge uses it correctly — `sinceSeq` only,
where it counts everything after the watermark. *(I first removed it outright
and the compiler caught the consumer: the trap is scoped to `beforeSeq`, not to
the field.)*

**A `--before` read never advances the watermark**, and the reason is not the
one the existing contiguity check tests. A backward page is the *newest*
messages before the cursor, so everything between `--since` and that page is
unread — **the hole is in the MIDDLE**, where a start-of-read check cannot see
it. This is "a window is not a prefix"
(`review:a-claim-must-not-outrun-its-evidence`, finding 8) on the first surface
that can produce a window whose *start* looks perfectly contiguous.

`--limit` alone is deliberately **not** excluded: a bounded forward read from
the watermark is a genuine prefix — seqs 1..30 with nothing skipped — so
recording 30 claims exactly what was seen. A positive control pins that, because
"never record on a bounded read" would pass the `--before` test and silently
retire the watermark for `--limit` too.

## Verified against the live server, not only against fixtures

```
--before 400 --limit 3 → 397 398 399   prevBefore 397
--before 397           → 394 395 396   prevBefore 394
--before 394           → 391 392 393   prevBefore 391
--before 1             → []            prevBefore null
```

No overlap and no gap: the caller passes the lowest seq they saw and
strictly-less-than excludes it.

## Mutations

Three, each confirmed to have applied by reading the mutated line back:

| mutation | reds |
| --- | --- |
| `prevBefore` takes the highest seq instead of the lowest | 1 |
| `--before` dropped from the bounded condition | **0 — GREEN**, then 1 |
| the watermark's `--before` exclusion removed | 1 |

**The green was the finding.** Every backward test either passed `--limit` too
(so `bounded` was still true) or returned a SHORT page (so the exhaustion loop
broke on its own) — neither can tell the bounded rule from the short-page rule.
The case that separates them is a **FULL page under `--before` alone**, which is
the ordinary case for a reader walking real history, not an edge one. Without
the bound it walks the whole chat: exactly what #548 exists to stop. Covered
now, and the mutation reds it.

## Review: a cursor that can only be empty is refused, not answered

Three findings, all valid, and the first is the design's own guarantee broken by
a typo.

**@codex P2 — `--limit 0`.** The SDL gives it a MEANING: *return only `total`*.
So it is not a nonsense value the server rejects — it **succeeds**, returns an
empty page, and is indistinguishable from the end of history. A caller walking
back with `--limit 0` would be told they had reached the beginning with the
whole chat still ahead of them. `asset ls` already refuses it, so the precedent
was in the repo.

**@copilot — the rest of the family**: a negative limit, and `--before` at or
below zero. Plus a limit above the server's 200 cap, which the server silently
caps — not wrong, but it leaves the caller believing they hold a page they do
not. All refused (exit 2) **before the query**.

**`--before 1` stays legal**, and there is a positive control for it: it means
*"nothing older"*, which is the honest end of a walk and where every backward
reader arrives naturally. A guard refusing it would break the last step of every
walk — and the over-strict mutation (`before < 2`) reds that control, so both
directions are pinned rather than only the permissive one.

**@copilot — the flag's help said "newest first"**, which reads as ordering and
contradicts the paragraph above it: the page is the *newest of those before the
cursor*, still printed oldest-first. The Long text was already right; only the
one-line usage string was wrong — **readers scan labels**, which is the same
miss `--force`'s usage string had in #552.

Three more mutations, each confirmed to have landed, each redding exactly its
own case.

## Propagation

- **No spec.** The cursor contract is the server's (#1116); this is a client
  adopting it, and the semantics that are mine — bounded-vs-exhaustive, the
  watermark rule — stop at this repo's edge.
- **hadron-docs**: reported, not filed — `team chat read` gains
  `--before`/`--limit` and `--json` gains `prevBefore`.
- **No dependent-repo issue.** `hadron-server#1121` (document `total`'s scoping
  in the SDL) already exists and is @Dara's.
