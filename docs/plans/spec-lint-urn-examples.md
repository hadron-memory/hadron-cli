# `spec lint`: put every URN example's decomposition on screen (#527)

Status: **shipped, with one half deliberately refused.**
Related: #239/#693 (the CLI does not hand-roll URN decomposition), #398 (where
corpus lint lives), `findings:splitnodeurn-disagrees-with-the-server-on-deep-chains`,
hadron-docs#267, `cor:urn:010:04`.

## 1. The problem, in @tove's words

> A worked example is the one part of a spec that a reader **executes**, so when
> prose and example disagree the **example wins in practice and the prose rots
> unread**.

`cor:urn:010:04` stated an App-scoped memory shape one way in prose and
decomposed a different way in its own worked example — and both disagreed with
the corpus fixtures. It survived minting because the two sat forty lines apart
in a long node, and **nobody diffs a spec against itself**.

## 2. What shipped

`spec lint` now scans each spec body for node-URN-shaped literals and prints
each one's decomposition as an **`info`** finding, beside the prose that claims
a shape. Advisory, never a gate: the lint cannot know which reading an author
intended, and failing a corpus run on one would be a guard asserting a
classification it has not computed.

## 3. The half it refuses, which is the design

**`urnlib.SplitNodeUrn` is wrong about exactly the literals this rule was filed
to examine.** It hard-codes *memory = first two segments* for both grammars, but
a v1 `::` chain uses the **last-segment** rule. Re-measured on 2026-09-20
against urn-lib-go v0.0.13 (still the latest) and the running server:

| literal | server | `SplitNodeUrn` | |
|---|---|---|---|
| `…::experiments::review:sort` | `hadronmemory.com:experiments` | same | agree |
| `…::experiments::services::db-helpers::query` | `hadronmemory.com:experiments:services:db-helpers` | `hadronmemory.com:experiments` | **disagree** |

They agree at three segments and diverge from the fourth on. `cor:urn:010:04`'s
example is a **five**-segment chain.

So decomposing it here would print a confident wrong answer about the very node
#527 exists for — the failure mode this rule is meant to catch, one level up.
A deep chain is therefore reported as **undecidable**, with the oracle named:

```
cor:urn:010:04  example hrn:node:micromentor.org::coding-app::coding-agent::app-mem::a:b
                → deep v1 "::" chain (5 segments): urn-lib reads the memory as the
                  first two segments while the server reads it as all but the last,
                  so the CLI cannot say where this one ends. Ask the platform —
                  `hadron_get_node <urn>` names the memory it tried
```

That is less than the issue asked for and it is the honest maximum. It still
puts the disagreement on screen, which was the point.

**Counting `::` segments is not decomposing.** It says how many atoms the author
wrote, never where the memory ends — a shape measurement taken to decide whether
to *ask*, which is why it does not breach #239/#693.

## 4. Two traps the corpus made concrete

**Key on SHAPE, never on "it parsed."** `SplitNodeUrn("cor:urn:010:04")` returns
memory `cor:urn`, loc `010:04` and a **nil error** — a bare citation decomposes
cleanly and silently. A parse-first scanner would have reported every citation in
a corpus made of citations. The scanner admits only a `hrn:`/`urn:` node prefix
or a `::` chain.

**A sibling citation is not an example.** Measured against the live corpus before
this filter: **400 findings, of which 396** were `hrn:node:<this-memory>:<sibling>`
cross-links — a spec citing the spec next door. The four deep chains were buried
under a hundred times their number in noise.

That is worse than not shipping. A nudge that is usually irrelevant trains a
reader to skim the rule, which is the opposite of putting a disagreement where
they cannot miss it. A literal addressing the memory being linted is a
*reference*; an example is the one pointing elsewhere. After the filter:

```
400 → 15 findings, all four deep chains kept, all in cor:urn:010:04
```

The undecidable ones are never filtered — their memory is precisely what the CLI
cannot determine, so excluding them would need the guess this rule refuses.

## 5. Not done

- **The library defect itself.** `urn-lib-go` is not this repo, and the fix needs
  deep-chain fixtures in the shared conformance corpus (all four existing
  `splitNodeUrn` fixtures are three-segment — the one arity where both rules
  coincide, which is why the drift was never asked about).
- **Exposing the server's decomposition over GraphQL.** Today only
  `hadron_get_node` reports it; `resolveUrn` and `node(ref:)` return a bare
  `null`. A GraphQL client cannot run the oracle at all, which is the reason this
  rule had to reach for a library. Reported, not filed — it is hadron-server's.
- **"Every example must satisfy the rule as stated"** is not mechanically
  checkable: a node states its shapes in prose, and extracting a template from a
  sentence is what #381 records as a failure mode. The issue never asked for it.
- **Whether the spec-review checklist gains a line** is a process call for the
  coordinator. This issue tracked the mechanism only.
