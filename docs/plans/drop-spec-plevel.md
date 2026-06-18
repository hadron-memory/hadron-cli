# Implementation Plan: drop the spec read-priority p-level

> **Status: implemented and verified** on branch `drop-spec-plevels` (not yet
> merged). This document reflects the design *as built* and is the review
> artifact for the change. Closes
> [hadron-cli#43](https://github.com/hadron-memory/hadron-cli/issues/43).

## Context

Every spec node carried a read-priority tag — `p0`..`p3` — plus a `pN:` prefix
stamped onto its table-of-contents edge label. The level was a pure function of
the citation tier (`defaultPLevel(Citation.Level())`: product/module roots `p0`,
features/rules `p1`, flows `p2`), and `spec lint` *required* exactly one such tag.

This was redundant *and* unsafe:

- **Redundant** — the value is fully determined by the loc, so storing it as a tag
  duplicated information the citation already encodes.
- **Unvalidated** — lint only checked that *one* `p[0-3]` tag was *present*, never
  that its value matched the citation. So the field was free to drift, and did. In
  the live `hadronmemory.com::specs` corpus, the 78 rule-tier nodes carried `p1`
  (×23), `p2` (×51), *and* `p3` (×4), mixed within five of eight modules — `cor:cht`
  alone had rules at all three levels. Every one passed `lint ✓`.

`defaultPLevel` itself was never wrong — it emits the documented product-agnostic
scheme (rule = `p1`) — but a stored, lint-unvalidated copy of a derived value has no
way to stay consistent across a hand-edited corpus.

**Decision (maintainer, on #43): remove read-priority from the data model.** Specs
are loaded by citation depth directly (`spec ls --prefix`, `h-find-nodes "cor:dmo"`);
there is no `pN` to keep in sync. This is the strongest of the options weighed on the
issue (the alternatives — normalize to the product-agnostic scheme, or to a
product-aware per-tier scheme — both keep a redundant field).

## What changed (as built)

All in `internal/cmd/spec/`:

- **`rubric.go`** — `specTags` no longer injects a p-level tag; its signature drops
  the `plevel int` parameter. A new spec's tags are `["spec", …extras]`.
- **`new.go`** — removed the `--plevel` flag, the `plevel` variable, the
  default/validation block, and the `tocEdgeLabel` helper. The ToC edge label is now
  just the child's title (previously `"pN: <title>"`).
- **`supersede.go`** — drops the `defaultPLevel` call; the replacement's tags and ToC
  edge label are computed without a p-level. `semanticTags` still strips a legacy
  `p[0-3]` tag when carrying tags over from a pre-migration node (defensive; harmless
  once the corpus is migrated).
- **`spec.go`** — removed `defaultPLevel`. `Citation.Level()` is **kept** — it still
  drives tier-specific rubric checks in lint (rule-vs-flow severity).
- **`lint.go`** — removed the `one-plevel` rule and its only helper, `countMatching`.
  `rePLevel` stays (used by `supersede.semanticTags`).

### Tests

- `spec_test.go` — removed `TestDefaultPLevel` and the `defaultPLevel` assertion in
  `TestCitationProduct`; updated `TestSpecTagsDedup` to the new signature.
- `lint_test.go` — `cleanSpec` no longer carries a `pN` tag, so `TestLintNodeClean`
  now also proves a spec with **no** read-priority tag is rubric-clean (previously it
  would have raised `one-plevel`).
- `spec_commands_test.go` — `TestSpecNew` asserts the ToC edge label is the title
  (not `pN:`-prefixed) and that the written tags are exactly `["spec"]`.

Read fixtures that still carry `p1`/`pN:` (e.g. `specBatchNode`) are left as-is: they
model **pre-migration** server data, which the read commands must still display
correctly.

## Follow-up: corpus migration (separate, sequenced after merge)

The 124 live nodes in `hadronmemory.com::specs` still carry `p[0-3]` tags and `pN:`
edge-label prefixes. **The migration must run only after this change ships** —
stripping the tags while the old `lint.go` still required one would fail every node.
Once merged: strip the `p[0-3]` tag from each node (preserving other tags) and remove
the `pN: ` prefix from ToC / inheritance edge labels, via a scripted
`node update` / `edge update` loop.

## Contract / behavior notes

- `spec new --json` no longer emits a `pN` in `tags`, and ToC edge labels lose the
  `pN:` prefix. This is the intended, documented change.
- `spec get` / `spec ls` are pass-throughs of server tags — their output is unchanged
  except insofar as the migrated corpus no longer contains p-levels.
- The add-spec skill node (`hadronmemory.com::core::skills:add-spec`) was updated to
  drop the "read-priority is automatic" convention in favour of "load by citation
  depth."
