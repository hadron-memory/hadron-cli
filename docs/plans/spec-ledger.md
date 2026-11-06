# `hadron spec ledger` (design as built, #662)

A read-only cross-check of the hadron-concept decision registry against the spec
corpus. The grammar is Vera's
([designs:decision-ledger-grammar](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-dev-team-shared:designs:decision-ledger-grammar));
this document records how the command applies it and where it goes one step further.
It is tooling, not platform law.

## Shape

- `internal/cmd/spec/ledgerlogic.go`: the pure half. `checkLedger(registry, docs, specs)`
  takes the registry text, every spec's body and abstract, and a `citation -> replaced`
  map, and returns sorted findings. No I/O, so the fixtures drive it directly.
- `internal/cmd/spec/ledger.go`: the command. Reads the registry FILE (`--registry`,
  default `design-discussions/_registry/decisions.md`; the file, not a Hadron node, is
  authoritative), lists the specs (`scanAllSpecNodes`, paged to exhaustion), reads
  replaced state (`loadReplacements`, the same convention `spec ls` uses) and every text
  (`ReadNodeBatchContent` through `CollectNodeBatch`).
- Fixtures: `internal/cmd/spec/testdata/ledger/`, vendored byte for byte from the
  grammar node's section 9.

## Report

| Category | Rules | Fails the run |
|---|---|---|
| 1 | `missing-disposition` | yes |
| 2 | `unresolved`, `superseded`, `retired-live`, `citation-unbound`, `citation-malformed` | yes |
| 3 | `unregistered-id`, `unparsed-shorthand` | yes |
| 4 | `pending` (the work queue) | no |
| 0 | `status-unknown`, `disposition-unknown`, `registry-malformed`, `duplicate-id`, `unreadable-spec` | yes |

Exit 5 when any error-level line is reported, so it can gate CI; 0 when only Pending
entries remain. Pending is re-evaluated on every run and never pinned to a count.

## Decisions beyond the grammar

- **`retired-live`.** Victor's builder note (comment 6628458382): a Retired line citing a
  live spec must be reported. The fixture's own Retired entry (`D-2026-10-01-D`) cites the
  retired spec AND its live successor and must stay clean, so the rule is per entry: report
  when the line has resolvable targets and none of them is superseded.
- **`unreadable-spec`.** A spec the listing returns but `nodeBatch` refuses leaves category
  3 short. It is reported (and fails the run) rather than dropped.
- **Empty corpus is not a finding.** Zero visible specs would make every citation
  unresolved; the command exits 4 instead.
- **Report lines for what the grammar leaves unsaid.** A malformed `## D-` heading, a
  repeated ID, a descending range, a range on a letter ID and a relative citation with no
  anchor are reported, never skipped or guessed.
- **`--json`** is an array of findings (`[]` when clean, never `null`), each with
  `locations` as `citation:field:line`.

## Boundary

Only `D-YYYY-MM-DD-<NNN|X>` IDs are read. `DA`, `DU-1` (cor:dmo:140:02), `rulings:NNNN`
and R-codes are never unregistered decisions; `TestLedgerBoundaryNegativeControl` pins it.

## Evidence

Against hadron-concept `bbc9f526` and `hrn:mem:hadronmemory.com:specs` (2026-11-04) the
command reports category 1: 0; category 2: 0 unresolved and 6 citing a superseded spec;
category 3: 0; category 4: 34, matching the grammar node's real run. That is evidence, not
a golden: no test pins today's counts.

## Out of scope

Minting, rewriting or changing any decision or spec; reading the cor:dmo:140 decision logs;
fixing the registry's six stale citations (the registry owner's).
