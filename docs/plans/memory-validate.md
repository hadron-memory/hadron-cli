# Implementation Plan: `hadron memory validate`

> **Status: implemented and verified** on this branch; reflects the design as
> built. Closes [#352](https://github.com/hadron-memory/hadron-cli/issues/352)
> (option B). Wires hadron-server's `validateMemory` (#819), which had no CLI
> surface at all.

## Context

#352 opened with a measurement — 175 of 280 nodes in `hadronmemory.com::specs`
carry `STALE_ABSTRACT` — and two candidate homes for a check: **(A)** an
`abstract-stale` rule in `spec lint`, or **(B)** a `memory validate` command
wrapping the server's audit. It recommended B, with A consuming it.

Before building either, the premise was measured (the full analysis is in the
[issue comment](https://github.com/hadron-memory/hadron-cli/issues/352#issuecomment-5192102707)).
The short version, because it shaped every decision below:

**`STALE_ABSTRACT` is a hash comparison, not a staleness judgement.** It tests
`abstractOriginHash` against the current content hash, so it fires on *any* body
edit since the abstract was last written — including one that changed nothing
the abstract says.

Measuring the semantic question directly (cosine between each abstract and the
best chunk of its own body, production embedding model, all 271 spec nodes):

| cohort | n | mean cos |
|---|---:|---:|
| hash-clean | 97 | 0.9138 |
| hash-stale | 174 | 0.9099 |

Cohen's d = 0.11 overall; **d = 0.01 at the rule tier** (n=179). The same metric
separates a genuinely mismatched abstract at **d = 3.29** (pairing an abstract
against a sibling spec's body drops it 0.911 → 0.737, correct for 98.9% of
nodes), so the instrument is sharp and the flag simply does not track the thing
its name suggests. 89% of hash-stale nodes score at or above the hash-clean
cohort's own 10th percentile.

Two consequences for this change:

1. **Option A was dropped.** A `spec lint` rule firing on 63% of the corpus to
   surface an effect of d=0.01 is exactly the "trains people to ignore the whole
   report" failure #352 itself warns about.
2. **The finding is relabelled everywhere the CLI describes it.** Not "the
   abstract no longer reflects current content" (the schema's wording) but
   "body differs from the version the abstract was written against".

## What shipped

`internal/api/queries/memories.graphql` — a `ValidateMemory` query selecting the
whole result: `memoryId`, `nodesChecked`, `ok`, `totalFindings`, `truncated`,
`findings{kind,nodeId,nodeLoc,nodeUrn,detail}`, `skippedChecks{check,reason}`.

`internal/cmd/memory/validate.go` — `hadron memory validate <memoryRef>
[--check <kind>]... [--limit N] [--fail-on-findings] [--json]`.

```
hadron memory validate hadronmemory.com::specs
hadron memory validate hadronmemory.com::specs --check broken-ref --fail-on-findings
```

## Design decisions

### 1. Two counts, both in the DTO

`totalFindings` is the server's true count across every check before truncation.
`matchedFindings` counts what was actually listed, after `--limit` and `--check`.
They differ constantly — on the live specs corpus the default call reports
`totalFindings: 245` with 200 listed — and conflating them is how a truncated
audit gets read as a smaller problem. Both are in `--json`; the human output
prints the total first and the filtered count beside it.

### 2. Skipped checks lead the human output

`ok` is false whenever a check was *skipped*, regardless of findings, because
health cannot be claimed for a check that did not run. The report prints
`checks NOT run (health is unknown for these)` **before** the findings, and the
`✓ no findings` line is emitted only when the findings are empty *and* nothing
was skipped. The obvious real case is an encrypted memory, where the
stale-abstract check is skipped because the validator does not decrypt.

### 3. `--check` requests the server maximum

The server takes no per-check argument, so `--check` filters client-side. That
composes badly with truncation, and **not hypothetically**: the server caps
`findings` at 200 by default, and on `hadronmemory.com::specs` the single
`broken-ref` finding sits past that cap. A naive implementation of
`--check broken-ref` reports *zero broken references* on a corpus that has one.

So when `--check` is given and `--limit` is not, the command requests the server
maximum (1000). If the result is *still* truncated, the report says the filtered
view may be missing matches past the cap rather than letting a short list read
as complete. An explicit `--limit` always wins.

### 4. Exit 0 by default; `--fail-on-findings` is opt-in

#352's design note — do not turn 175 findings into a red build on day one —
is the whole argument. Any corpus that has never been audited lights up on first
run, and a gate that fires on two thirds of a memory trains people to ignore the
report. The gate keys on `matchedFindings`, not `totalFindings`, so
`--check broken-ref --fail-on-findings` is a useful narrow CI gate that ignores
the noisy checks. Exit code is 5 (`Conflict`), matching `spec lint`.

### 5. Detail is truncated in the table, never in `--json`

`embed-failed` details are whole provider error payloads — the SageMaker errors
run several hundred characters and embed a CloudWatch URL — which would make the
table unreadable. Truncated to 96 runes for the table; `--json` carries the
string intact, which is what you need to classify failures.

### 6. Kind names are kebab-case, wire spelling accepted

`--check stale-abstract` matches every other flag value in the tool, but
`--check STALE_ABSTRACT` (copied out of `--json` or the GraphQL schema) works
too. `kindName` falls back to lowercasing the raw enum value, so a kind added
server-side prints legibly instead of vanishing from the report.

## Deliberately not done

- **No `spec lint` rule** (#352 option A) — see Context.
- **`spec citations --stale-abstracts` is NOT re-pointed at this path**, though
  #352 asks for it "once one exists". It would be a correctness regression:
  `validateMemory`'s findings are capped (1000 max), so a memory with more
  findings than the cap would yield a silently incomplete stale set, whereas the
  existing client-side hash computation in `citations.go` is exact and runs
  inside a batch read the command already performs. Deduplicating onto a capped
  source is the wrong direction. If the server grows a per-check filter or
  pagination, revisit.
- **No remediation.** Nothing here fixes a finding; the 67 `embed-failed` nodes
  are two server-side bugs filed as
  [hadron-server#882](https://github.com/hadron-memory/hadron-server/issues/882)
  (an unbounded chunk batch that 413s past 32 chunks, and retry exhaustion with
  no self-heal), not something the CLI can repair.

## Verification

Against the live `hadronmemory.com::specs` (280 nodes, 245 findings):

- default run reports `totalFindings: 245` with the truncation note, and the
  per-kind table shows only the kinds present in the listed 200 — `broken-ref`
  is absent, which is the truncation trap made visible;
- `--check broken-ref` finds the one past-the-cap finding without a manual
  `--limit`, confirming decision 3;
- exit codes: 0 with findings, 5 with `--fail-on-findings`, 0 when the filter
  matches nothing, 4 for an unknown memory, 2 for a bad `--check`.

Unit coverage in `internal/cmd/memory_validate_cmd_test.go` against the fake
GraphQL server, including the encrypted-memory skipped-check path (no encrypted
memory was available live to exercise it).

## Gotcha found while building this

`make schema-check` gives a **false positive on any uncommitted change to
`internal/api/gen`**. Its staleness test is `git diff --quiet -- internal/api/gen`,
which compares the working tree against HEAD rather than against the
pre-regeneration snapshot it took moments earlier — so while you are developing a
new operation it reports "schema drift" that is really just your own uncommitted
generated code. Committing makes it pass. CI is unaffected (it checks out a clean
tree), so this is a local-workflow wart, not a broken gate — but it cost a
detour here, and the SDL export confirmed byte-identical schemas.
