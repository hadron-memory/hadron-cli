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
