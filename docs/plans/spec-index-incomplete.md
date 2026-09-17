# Implementation Plan: `index-incomplete` lint for spec index nodes

> **Status: implemented and verified** — reflects the design as built. Closes the
> open half of [#605](https://github.com/hadron-memory/hadron-cli/issues/605),
> whose primary fix (the tier-aware `abstract-length` remedy) shipped separately
> in [#609](https://github.com/hadron-memory/hadron-cli/pull/609) (`4e5b93e8`).
>
> The surface this checks is **not** the one the issue started on. Both readings
> originally proposed were about the ABSTRACT; the specs engineer measured the
> corpus and ruled them out. The reasoning is reproduced below because the rule
> is only defensible with it.

## Context

#605 began as a remedy defect: `abstract-length` told a *feature* — whose
abstract is an index of its children — to perform a supersede-level split, which
at that tier would mean superseding every child into a tombstone. The fix was to
tier the remedy, and it shipped.

It left a second question open. If an index abstract's job is to ROUTE to its
children, can the linter check that it routes to **all** of them? Two readings
were proposed: the child's citation appears in the parent's abstract, or the
child's subject is named there.

Both were implemented and run against `hrn:mem:hadronmemory.com:specs` before
either shipped. That run reported **72 findings across 15 index nodes, every one
N/N** — including `cor:dmo:060`, the node nominated as the model of a *correct*
routing index. Shipping it would have put 72 warnings on a corpus that had been
cleared to zero hours earlier: "a rule enforced by advice nobody should take".

The uniformity was the tell. A real defect distribution varies.

## Why the abstract is the wrong surface

One argument, not three, and it is structural rather than a matter of degree.

An abstract is the **RAG retrieval surface** (`cor:dmo:020:03`) — it exists to be
embedded and matched semantically. A citation string is an opaque token: it
carries no meaning to an embedding, and it spends characters against the
**2000-character hard cap that `abstract-length` errors on**. `cor:agt:020` has
twelve rules; twelve citations is ~290 characters of ceremony on a node that had
just been rewritten from 1940 to 1427 characters to clear that very error.

**One signal would push abstracts toward the cap while its neighbour errors on
them for being near it.** That is not adoptable at any compliance rate, which is
why the measured rate (13 of 86 index nodes already comply) does not enter the
argument.

The second reading failed a different test: the probe for "does the abstract name
the child's subject" scored `cor:dmo:060` as naming only some of its 14 children,
while by eye it plainly routes to all of them. A predicate that disagrees with
its author's judgment on the node put forward as correct cannot be a linter. And
a fuzzy completeness check fails in the **expensive** direction — it certifies an
index as complete. No check beats a check that says "complete" when it is not.

## The convention nobody had written down

The corpus's real convention is **two-layer**, and this rule is the first place
it is stated:

| field | job |
| --- | --- |
| **abstract** | routes by *describing* subjects — for retrieval |
| **body** | indexes by *citing* children — one entry per child |

The second is where the drift is, it is far better attested, and it is **exactly
decidable**: a loc string is present or it is not.

### What settled it

`cor:agt:020` was **abstract-complete (12/12) and body-incomplete (7/12)** — its
`## Rules` list stopped at `:06`, omitting `:07`–`:11`, on the same day its
abstract was rewritten to route all twelve and reported as fixed. It was the only
such node in the corpus. **Neither abstract-side reading would have caught it.**
Only the body check does.

## What shipped

`index-incomplete`, **warning** tier, in `lintCorpus` (`internal/cmd/spec/lintindex.go`).
One finding per PARENT, naming the uncited children — a single unwritten index
list is one defect, not a dozen.

**Scope** is decided by the CHILD's level, not the parent's. That indirection is
load-bearing: `ParseCitation` reads a lone atom as a flat module, so a bare
product root (`cor`) and a real module (`cor:agt`) both arrive at level 1 and
cannot be told apart from the parse alone — the ambiguity `@codex` found in
`indexRemedy` on #609. A child's level has no such collision.

In scope: module tier (feature children) and feature tier (rule children).
Out: the **product root**, which cites 0 of its 17 modules and is a different
shape; the **rule tier**, where the corpus has one parent with one child and a
one-item list is not a convention; and **general-provisions contracts**, which
are inherited by their siblings rather than indexing anything (the #609
exclusion, extended here — zero contracts have children today, so it removes no
findings; it is there so the first one that does is not told to route).

**Drift classes.** The message says whether each uncited child was edited *after*
the index (the list fell behind) or already existed when the index was last
written (the list was touched and the child left out). Timestamps ride the
existing `nodeBatch` projection, so this costs nothing on the wire, and they are
compared as parsed instants rather than lexically — the live corpus writes UTC,
where a string compare happens to agree, but it agrees by accident and an offset
spelling would invert the classification silently.

`Node.updatedAt` was **seen carrying a value through the path this rule uses**,
not assumed from the schema (`review:a-recommended-field-must-be-seen-carrying-a-value`,
whose occurrence was a `total` that is null on the live server while every fake
fills it in). The drift clause renders only when both timestamps parse, and the
21:07 live run against `hrn:mem:hadronmemory.com:specs` printed it — *"edited
since this index was last written"* on `cor:acl:100` — off a real `nodeBatch`
response, which no fixture was involved in.

## The matcher is the whole rule

A too-narrow matcher does not merely miss findings; it reports **compliant
indexes as empty**. That failure occurred three times during this work, twice
before any code shipped:

Measured over the 293 module- and feature-tier child-edges of a single snapshot
(`hrn:mem:hadronmemory.com:specs`, 2026-09-17 21:07, before the repairs below):

| matcher | cited | gaps | parents warned |
| --- | --- | --- | --- |
| full citation, word-boundary-guarded, + relative | 208/293 | 85 | 18 — **the false zero** |
| + relaxed lead guard on the full form | 272/293 | 21 | 10 |
| + the colon-leaf form (**shipped**) | 280/293 | 13 | **8** |

**Form 1 — the full citation** (`cor:agt:020:09`), matched after any
non-identifier character, **including `:` and `/`**. That looseness is the
corpus's own convention: the module tier links by URN —
`[**010 Memory access**](hrn:node:hadronmemory.com:specs:cor:acl:010)` — so the
citation's only appearance is inside the link target, preceded by a colon.
Requiring a word boundary in front took module-tier coverage from 76/81 to
**12/81**.

**Form 2 — the last two atoms** (`020:09`), strictly boundary-guarded. The strict
guard is what stops a cross-reference to `cor:oth:020:09` from satisfying an
index for `cor:agt:020:09`.

**Form 3 — the colon-leaf** (`:09`). `cor:acl:100` writes its entire index this
way. With only the first two forms the rule warned **three times on an index that
routes to all four of its children** — found by reading the single residual
finding a run produced rather than trusting it.

**The bare leaf** (`020`, `09`) is deliberately **not** accepted. Measured: it
adds zero coverage over the three forms, because every node writing a bare number
also links the full citation. At the rule tier it is two digits, which prose
produces by accident, so accepting it would buy nothing and cost precision.

**Not checked: that the citation sits in an index LIST.** A child named once in
passing prose counts. That is the price of exact decidability, and it is the
price the ruling chose — a predicate judging whether a mention "is an index
entry" is the fuzzy check that was rejected.

## Effect on the corpus

Run against a snapshot of `hrn:mem:hadronmemory.com:specs` taken at 21:07 on
2026-09-17, the shipped rule reported **8 parents**: `cor:agt`, `cor:agt:010`,
`cor:api:040`, `cor:api:050`, `cor:cht`, `cor:int:030`, `cor:int:040`,
`cor:sec` — between 1 and 3 uncited children each, both drift classes present.

Over the following minutes the specs engineer, working independently from her own
repair list, edited **every one of those eight nodes**. Re-run against the
repaired corpus, the rule reports **zero**. That agreement — a finding list and an
expert's repair list matching node for node, neither derived from the other — is
the strongest validation available here, and it is worth more than the unit
tests, which passed in the version that was wrong.

For contrast, the word-boundary-guarded matcher reports **71 gaps across 11
parents** on that same repaired corpus, where the shipped one reports 0. That is
the #605 failure exactly: dozens of warnings on a corpus an expert has just
cleared.

That 71 is worth naming precisely, because it is also somebody's published
number. The strict matcher scores the repaired corpus at **222/293** — the figure
the specs engineer measured and reported, alongside a standing backlog of "34
parents / ~95 missing body citations". Both are artifacts of the same
word-boundary guard. Under a matcher that accepts the form the corpus actually
links with, that backlog is **zero**.

The rule therefore ships **green** against the live corpus. Like `scaffold-body`
(#545), it earns its place by catching the next one.

## Verification

Unit tests in `internal/cmd/spec/lintindex_test.go`, with fixtures copied from
real corpus node bodies rather than written from a model of them — the first
attempt's tests all passed while the check was reporting 72/72, because fixtures
and matcher shared one mistaken model.

Every load-bearing decision is **mutation-verified**: reverting it makes a named
test fail.

| mutation | caught by (subtest of `TestIndexIncompleteCitationForms` unless named) |
| --- | --- |
| drop the colon-leaf form | `colon-leaf_form` |
| require a strict lead guard on the full form | `full_citation_inside_a_URN_link_target` |
| loosen the lead guard on the relative forms | `other_module's_rule_does_not_credit_this_one` |
| accept the bare leaf atom | `bare_leaf_atom_is_not_a_citation` |
| drop the trailing whole-token guard | `grandchild_does_not_credit_child` |
| compare timestamps lexically | `TestIndexIncompleteComparesInstantsNotStrings` |
| widen the scope to every tier | `TestIndexIncompleteScope` — both subtests |
| drop the contract-parent exclusion | `TestIndexIncompleteSkipsContractParents` |

Two of these needed a second pass, which is the check on the check
(`review:a-mutation-check-can-itself-be-a-no-op`): the first attempts at the
timestamp and contract mutations left an unused import and an unused variable,
so the package did not build and "the tests failed" would have been a false
green. The harness reports a build failure as *mutation invalid*, not as caught.

One existing test changed: `TestLintCorpusCleanReturnsEmptySlice`'s "clean
corpus" was three nodes whose header fixtures carried **no body at all**, so its
indexes routed to nothing. That is #545's lesson one tier up — there, the fixture
for a clean SPEC was a spec nobody had written. The fixture now indexes its
children (`specHeader`), which is what makes it clean.

## Deliberately not done

- **No abstract-side signal.** Not deferred — ruled out, for the reason in *Why
  the abstract is the wrong surface*.
- **No bulk repair of the corpus.** Index lists are content, and repairing them
  is the specs engineer's call, not a sweep this CLI takes unasked.
- **The product root is not warned on.** It cites 0 of its 17 modules and is a
  different shape; that is a corpus decision, upstream of this rule.
- **No `## Rules` / `## Entities` heading requirement.** Requiring the heading
  would be a second, separate rule about presentation; this one checks that the
  child is reachable from its parent at all.
