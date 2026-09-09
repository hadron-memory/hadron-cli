# Implementation Plan: `abstract-length` lint for spec abstracts

> **Status: implemented and verified** — merged 2026-08-05 (`19c7429`); reflects the design as
> built. Closes [#347](https://github.com/hadron-memory/hadron-cli/issues/347),
> which followed from hadron-server
> [#880](https://github.com/hadron-memory/hadron-server/issues/880).
>
> The threshold shipped here (**1600 characters**) is *not* the one the issue
> proposed (1000). The issue's number came from an inference that this plan's
> measurements do not reproduce; the experiment and its data are below, because
> the number is only defensible with them.

## Context

hadron-server #880 opened as "abstracts aren't embedded" and closed as
not-a-bug: the abstract vectors existed all along. But the investigation left
behind a secondary conclusion — that **long spec abstracts dilute into weak
vectors** — and #347 proposed acting on it: warn in `spec lint` above 1000
characters, aim for 500–800.

That secondary conclusion rested on a comparison between two *different* nodes
answering two *different* queries: `cor:acl:100:03` (~1300-char abstract)
scoring ~0.59 on a conversational question, versus `cor:acl:070:01` (~700-char
abstract) scoring 0.9+ on its own. Length was the salient difference, so length
got the blame. Nothing controlled for topic, query phrasing, or the corpus's own
discrimination.

Encoding a threshold in the linter makes it corpus law for every spec author
from then on, so it was worth measuring first.

## The experiment

Everything ran against the **production embedding model**, offline: ollama
serving `nomic-embed-text` (v1.5, 137M, 768-dim), the same model
`hadron-server/src/lib/embedding/client.ts` reaches over HTTP in dev and via
SageMaker in prod, with the same mandatory task-instruction prefixes
(`search_document: ` for documents, `search_query: ` for queries). Body chunking
was ported faithfully from `hadron-server/src/lib/chunking.ts` (structure-aware
on `#`/`##`/`###`, 512-token sections, 64-token overlap, 4 chars/token).

- **Index** — all 271 real abstracts from `hadronmemory.com::specs`.
- **Gold set** — the 69 rule-tier specs with an abstract ≥1000 chars and ≥4
  sentences (room to manipulate).
- **Queries** — 476 conversational questions, one generated per abstract
  sentence by a local LLM instructed to use everyday wording and avoid the
  sentence's distinctive noun phrases. Plus 138 first-sentence questions.
- **Metrics** — cosine similarity, and the operational ones: the gold node's
  rank against the real corpus and its top-1 rate.

### Positive control: the harness detects real dilution

Before trusting a null result, check the instrument can see the effect. Padding
a spec's core sentence with **off-topic** spec text:

| padding | length | mean cos | Δ | top-1 |
|---|---:|---:|---:|---:|
| none | 179 | 0.6912 | — | 37.7% |
| off-topic | 700 | 0.6482 | −0.043 | 13.0% |
| off-topic | 1000 | 0.6419 | −0.049 | 10.1% |
| off-topic | 2000 | 0.6430 | −0.048 | 6.5% |

Dilution is real, large, and detectable — at **700 characters**.

### Length itself: five tests, one answer

**1. Truncation.** Prefix-truncate each gold abstract at sentence boundaries.
Balanced panel of the 25 golds with ≥8 sentences, so every length step contains
the same items:

| sentences | mean len | mean cos | mean rank |
|---:|---:|---:|---:|
| 1 | 166 | 0.6815 | 11.48 |
| 3 | 579 | 0.6886 | 5.64 |
| 5 | 1000 | 0.6947 | 3.92 |
| 8 | 1524 | 0.6950 | 3.80 |

Longer is *better*, monotonically, out to 1500 characters.

**2. Extension.** Grow each real abstract past its natural length with on-topic
prose lifted from its own body — precisely the "comprehensively restates the
body" pattern #347 names — holding the queries fixed:

| length | Δ score vs real | 95% CI | top-1 |
|---:|---:|---:|---:|
| 1377 (real) | — | — | 42.1% |
| 1728 | +0.0004 | ±0.002 | 35.9% |
| 2109 | −0.0015 | ±0.0015 | 36.5% |
| 2497 | −0.0045 | ±0.0017 | 35.7% |
| 3099 | −0.0082 | ±0.0017 | 31.0% |

Nothing measurable until ~2000, and the server's own cap is 2000.

**3. Paired dilution.** Same query, same node, only extra on-topic sentences
added, baselined on the shortest variant that already contains the answering
sentence:

| extra chars | Δ score | mean rank |
|---:|---:|---:|
| +1..200 | +0.0007 | 9.56 |
| +400..700 | −0.0046 | 6.50 |
| +700..1100 | −0.0127 | 5.90 |
| +1100..2000 | −0.0125 | 4.83 |

There *is* a small dilution cost to length — about −0.013 for +1000 on-topic
characters, against a corpus whose median rank1−rank2 margin is 0.015. But rank,
the thing retrieval is actually decided on, improves anyway, because the added
text also wins the node queries it would otherwise have missed.

**4. Compression — the decisive test.** Truncation loses content; distillation
does not. LLM-compressed variants of 35 gold abstracts, instructed to preserve
every distinct claim, evaluated on the full per-sentence query set so coverage is
held constant:

| variant | mean len | mean cos | Δ vs real | top-1 |
|---|---:|---:|---:|---:|
| compressed | 722 | 0.6959 | −0.0010 ± 0.0041 | 45.7% |
| compressed | 918 | 0.6997 | +0.0029 ± 0.0037 | 48.6% |
| real | 1396 | 0.6969 | — | 43.3% |

Both confidence intervals straddle zero. **Distilling a 1400-char abstract to
~750 changes retrieval by nothing measurable.** It is not wrong advice; it is
simply not a retrieval intervention.

**5. Observational, on the corpus as it stands.** Across the 69 golds on their
own queries, abstract length vs mean rank is r = **−0.12** — longer ranks
*slightly better*. And the abstract-to-body length ratio versus the abstract's
value-add over the body's own chunks is r = **+0.33**: relatively longer
abstracts add *more* over chunk indexing, not less, which is the opposite of the
redundancy argument in #347.

### Why #880 saw what it saw

Re-running its query ("can an org admin see another member's private chats or
mailbox") reproduces the symptom exactly — and shows the cause is not length.
The entire corpus bands between 0.68 and 0.70 on that query; the median
rank1−rank2 margin corpus-wide is 0.0152. Nothing in the corpus scores 0.9+ on a
natural-language question; a 0.9 requires a near-verbatim query. So the 0.59 vs
0.9+ contrast compared different queries, not different lengths.

The real failure mode is **topical drift**, and length is a poor proxy for it:
abstract length versus worst-pair inter-sentence similarity is r = −0.29.

## What shipped

**`internal/cmd/spec/lint.go`**

- `abstractSoftMax = 1600` — the top of the measured plateau, documented at the
  constant as a ceiling rather than an optimum.
- New per-node rule `abstract-length`, in the `else` branch of the existing
  abstract-presence check (a node with no abstract gets one finding, not two):
  `warning` at the rule tier, `info` at the flow tier, matching how the rest of
  the rubric tiers down. `--strict` promotes it through the existing mechanism.
- `abstractLength` counts **runes**, not bytes. Spec prose is full of em-dashes
  and arrows; byte-counting would flag abstracts the server — which counts
  characters — considers well inside its own cap.
- The `abstract` presence message and the command's `Long` now carry the
  authoring guidance, framed on topical focus.

**`internal/cmd/spec/rubric.go`** — `abstractStyleHint`, appended to every
scaffolded placeholder abstract at all three tiers that carry one.

**`internal/cmd/spec/new.go`**, **`internal/cmd/agentic/agentic-usage.md`** —
the same guidance where the abstract's role is explained.

### Wording

Every user-visible string states the bound as a ceiling and points at topic, not
brevity, because that is what the data supports:

> abstract is 1739 chars — past ~1600 added length stops paying for itself;
> distill it, and check every sentence is still about this spec (off-topic
> sentences dilute the vector far more than length does)

## Effect on the corpus

`hadron spec lint --all -m hadronmemory.com::specs` → 19 `abstract-length`
warnings out of 180 rule- and flow-tier specs (10.6%).

| threshold | flagged | verdict |
|---:|---:|---|
| 800 | 115 (63.9%) | inside the plateau; no measured basis |
| 1000 (as filed) | 71 (39.4%) | inside the plateau; no measured basis |
| 1200 | 43 (23.9%) | inside the plateau |
| **1600** | **19 (10.6%)** | **top of the plateau** |
| 2000 | 0 | the server's cap; would never fire |

The five `cor:acl:100*` abstracts that motivated #347 (1076–1296 chars) are
**not** flagged. That is the intended outcome: the measurements clear them, and
their poor showing on that one query was corpus-wide discrimination, not their
length.

## Deliberately not done

- **No hard enforcement** (#347 ask 3). The evidence for any bound at all is
  weak; enforcing one would be unjustified. `--strict` remains the escalation
  path.
- **No header-tier check.** `lintNode` returns before the rubric for levels < 3,
  and the issue scoped the rule to the rule tier. Module and feature abstracts
  are embedded too, but nothing measured here argues for policing them.
- **No topical-drift rule**, even though drift is the effect that actually
  matters. A per-node coherence check needs embeddings the CLI does not compute,
  and inter-sentence similarity was only a weak discriminator (r = −0.29
  against length) — worth a separate investigation, not a rule bolted on here.

## Reproducing

The harness is not vendored (it needs a GPU and a model pull), but it is small:

```bash
ollama pull nomic-embed-text
hadron spec get --prefix cor -m hadronmemory.com::specs --json > specs.json
```

Embed with `search_document: ` / `search_query: ` prefixes, L2-normalise, dot.
The one methodological rule that matters: **compare the same query against the
same node**, varying only the thing under test. Every misleading number in #880
came from changing two things at once.

---

## Follow-up: the lint knew the soft bound and not the wall (#539)

Design-as-built for the reporting half. **Nothing above changes** — the 1600
bound, the plateau it sits on, and the "off-topic dilutes more than length"
framing are all unaltered. What changed is that the finding stopped being the
same sentence at 1601 and at 1990.

### What it cost

@Ada, amending `cor:agt:020:03`: the abstract sat at 1922, the lint said what it
says at 1650, and replacing one 39-character sentence failed the write **twice**
(2090, then 2048) before fitting at 1990.

> The information that would have let me write it once — "you have 78
> characters" — exists at lint time and is never printed.

The lint knew `abstractSoftMax` and had the hard cap only in prose: a comment on
the constant, and another on `abstractLength` saying it counts runes "matching
how the server measures its own 2000-char cap". **Teaching it the number was
most of the fix.**

### Three changes

1. **`abstractHardMax = 2000`**, mirrored from the server (spec 031) — not this
   repo's number to choose, carried only so the finding can report distance.
2. **Every message reports headroom**: *"1700 chars, 300 from the 2000-char hard
   cap"*.
3. **With fewer than `abstractTightHeadroom = 150` characters left, the finding escalates to an
   error and the advice inverts.** These are different findings wearing one rule
   name: past the soft bound, "distill it" is right; a sentence from the cap it
   is *wrong*, because on a spec whose sentences are all on-subject, cutting one
   drops a contract. The remedy is a supersede-level split.

The escalation does **not** tier down for flows. The severity ladder is about
how much a long abstract matters for retrieval; the cap is about whether the
node can be edited at all, and a flow's write fails at 2000 exactly as a rule's
does.

### The split hint, and why it is only ever a clause

@Ada's observation, and it holds up: three specs flagged for length, three titles
containing a conjunction — *"allocation **and** permanence"*, *"sessions,
liveness **and** provenance"*. A title naming two subjects is the node saying
where the split goes.

It is appended to the near-cap finding and is **never a finding of its own**.
Plenty of single-subject titles contain "and" ("create and update"), so alone it
would be noise; paired with an abstract a sentence from the cap it is a lead
worth printing. The citation half of the title is stripped first, so a loc like
`cor:and:010` cannot match.

### Measured before shipping

**Both corpora are clean** — zero `abstract-length` findings in
`hadronmemory.com:specs` and `micromentor.org:platform-specs` — so the new error
tier fires on nothing today and cannot break CI on landing.
`cor:agt:020:03` is now 1564 characters; someone distilled it after the issue was
filed, which is also how the measurement harness got proved before its clean
result was believed.

### Two green mutations, both real gaps

Blanking the headroom numbers left the suite green: the headroom test used 1922,
which takes the **escalated** message, so the ordinary warning's numbers — what
most authors see — had no assertion. And hard-coding the conjunction clause on
changed nothing, because the only assertion was on the helper in isolation,
never on the pairing. Both now covered.

### Review: the escalated finding overclaimed what fails

@codex (P2) and @copilot, independently, and they were right. The message said
*"the next edit fails at write time"*. **The server rejects a write whose
abstract EXCEEDS the cap — not the next edit.** At 1922 an equal-length
replacement is fine, and so is adding up to 78 characters.

That matters more here than a wording nit usually would, because the sentence
goes on to recommend a **supersede-level split**: the stronger claim would have
justified a costly restructure on a node that did not need one yet. A claim
outrunning its evidence, in the one sentence written to make the reader act.

It now names the condition — *"any edit that grows it past that is rejected"*.

@copilot also caught the arithmetic: **at or past the cap, `abstractHardMax - l`
is zero or negative**, so the message read *"only -48 from the 2000-char hard
cap"*. Over-cap is reachable from data written before the cap existed, so it is
a real state rather than a defensive branch, and the sentence changes there:
nothing about "headroom" is true, and what the author needs is that any update
which does not shorten the abstract is refused.

Both fixes swept all three surfaces that carry the claim — the finding, the
`spec lint --help` text, and `agentic-usage.md` — since one wording living in
three places is how the retired version survives in two of them.

### And the fix reproduced the overclaim at the boundary

Round 2 is worth recording because it is the same mistake as round 1, one branch
over. Correcting *"the next edit fails"* to name the condition, I added an
`at or past the cap` branch that said *"any update that does not shorten it is
rejected"* — **which is false at exactly 2000**, where the server still accepts
an equal-length rewrite, because the cap rejects values LONGER than it.

So the finding would have pushed a node at the boundary toward a supersede-level
split it does not need — the identical consequence round 1 removed. **I fixed
the instance and rebuilt the class one case over**, which is precisely @Dara's
line: answering the example is how you get shown the next example.

Three states now, because the boundary is one:

| length | what is true |
| --- | --- |
| `< 2000` | any edit that grows it past the remaining headroom is rejected |
| `== 2000` | any edit that LENGTHENS it is rejected; an equal-length rewrite still works |
| `> 2000` (legacy data) | any update that REPLACES the abstract is rejected unless it brings it to 2000 or fewer; an update leaving the abstract alone still succeeds |

None of the three is unconditional — the round below corrected the last row a
second time, and this table with it.

### The overclaim had a third form, and the wall binds every tier

Two more from @codex, both after the boundary fix above.

**The over-cap claim was still too broad.** It said *"any update that does not
shorten it below the cap is rejected"* — but `UpdateNodeInput` preserves omitted
fields, and `spec supersede` retires a node by sending only tags and content
(`supersede.go`). So a body-only edit succeeds with an over-cap abstract
untouched, **and the message was telling the reader that the very remedy it
recommends would be rejected.** Scoped to abstract *replacements* now, with the
supersede path named explicitly.

That is three rounds of the same overclaim, each correction re-stating it one
case over. The message construction is now a single function, `nearCapMessage`,
rather than an expression at two call sites — the shape that let it drift.

**And the hard cap binds module and feature headers too.** `lintNode` returns at
`c.Level() < 3` before the rubric, which is right for an advisory length bound —
a long header abstract costs retrieval little. It is wrong for the WALL: a header
abstract a sentence from the cap is exactly as unwritable as a rule's, and
reporting nothing there leaves the author to discover it at write time, which is
the whole defect this issue is about.

So the near-cap check moved ABOVE the early return and the soft check stayed
below it. Only the advisory bound tiers down. A rule-tier node at the wall must
therefore collect exactly one finding, not two, and there is a test for that.
