# Implementation Plan: `hadron spec link` — convention-aware cross-ref

> **Status: implemented and verified** on branch `feat/spec-link` (not yet
> merged). Design *as built* and the review artifact for the change. Closes
> item #4 of
> [hadron-cli#41](https://github.com/hadron-memory/hadron-cli/issues/41)
> ("Cross-reference edges are manual and the convention is implicit"). Ships as
> its own PR; item #1 (`spec edit`) is tracked separately under the same theme.
> Items #2 (`spec extract`) and #3 (`--abstract-file`) already merged.

## Context

A cross-reference edge between two specs — a field spec pointing at the entity
it documents, a flow at its rule — is, today, a raw `hadron edge add` whose
**direction and label have to be reverse-engineered from neighbors**. Both
endpoints must be spelled as fully-qualified node URNs (`<org>:<memory>:<loc>`),
even though every other `spec` command addresses nodes by their bare citation
under `-m`. It is easy to get the direction backwards or to drift from the
corpus' label convention.

`spec extract` already wires this edge automatically when it splits a rule out
of a parent (`new → source`, labeled in the field→entity convention). `spec
link` is the **standalone** counterpart for the case where both specs already
exist and you just need the cross-ref — the same edge, the same direction, the
same default label, addressed by citations.

The motivating shape (from the issue) is the live
`cor:dmo:020:04 → cor:dmo:060:02` edge labeled
"documents the nodeType field of Node".

## Command surface

```
hadron spec link <from-citation> <to-citation> -m <memory> [--label "…"] [--dry-run]
```

- `<from>` is the **specific/citing** spec, `<to>` the **general/cited** one —
  the field→entity / flow→rule direction `spec extract` uses. Both are bare
  citations resolved under `-m`.
- `--label` defaults to the corpus convention synthesized from the two titles
  (`documents <from-title> on the <to-title> entity`) via the same
  `defaultRefLabel` helper `spec extract` uses; pass `--label` to override, and
  `edge update` to refine later.
- `--dry-run` prints the planned edge without writing.
- `--json` emits the stable `{from,to,label,memoryId,edgeId,dryRun}` DTO.

## Design decisions

- **Citations, not URNs.** The whole point over `edge add` is to address specs
  the way the rest of the `spec` group does. `ParseCitation` validates shape;
  `fetchSpecNode` resolves each under `-m`.
- **Both endpoints must be specs in the same corpus.** Same-corpus is
  structural — both resolve under one `-m`, so there is no cross-memory check to
  make. "Is a spec" is enforced by requiring the `"spec"` tag on each fetched
  node (`requireSpecTag`); a non-spec endpoint is a `Usage` error pointing at
  `edge add` for arbitrary nodes. `edge add` remains the escape hatch for
  arbitrary or cross-memory edges.
- **Self-link guard** fires on the citations before any network round-trip.
- **No `--priority`/`--condition`/`--data`.** A spec cross-ref is a plain
  labeled edge; the rare gated/weighted edge stays in `edge add`. Keeping the
  surface minimal is the feature.
- **Reuse over re-implementation.** The command is `memoryURNFromFlag` +
  `ParseCitation` ×2 + `fetchSpecNode` ×2 + `defaultRefLabel` + `gen.CreateEdge`
  — all existing. The only new code is the two-endpoint validation and the DTO.
  The title-extraction logic that `defaultRefLabel` already contained is
  factored into a shared `titleFromName` helper (behavior-preserving; covered by
  the existing `TestDefaultRefLabel`).

## What it deliberately does not do

- It does not create the *target* if it is missing — that is `spec new` /
  `spec extract`. `link` only connects two existing specs.
- It does not enforce that `<from>` is structurally more specific than `<to>`
  (e.g. deeper citation) — direction is a semantic convention documented in
  help, not a parse rule. Linting direction is out of scope.

## Testing

- Unit (`internal/cmd/spec`): `TestTitleFromName` covers the extracted helper;
  `TestDefaultRefLabel` (unchanged) proves the refactor is behavior-preserving.
- Command-level (`internal/cmd`, fake GraphQL): happy path (default label
  synthesized, `CreateEdge` called with it, DTO carries the two citations +
  `edgeId`), explicit `--label` pass-through, `--dry-run` makes no `CreateEdge`
  call, a non-spec endpoint is `Usage`, a self-link is `Usage` (pre-network),
  and a missing endpoint is `NotFound`.

## Follow-ups (out of scope for this PR)

- The `add-spec` skill still says "add cross-refs by hand"; once `spec link`
  lands that guidance is stale. The skill is generated from a Hadron node
  (`hadronmemory.com::core::skills:add-spec`), so that is a corpus edit, not a
  CLI change.
- `spec edit` (#41 item 1, the `$EDITOR` affordance) is the remaining write-side
  gap, tracked in its own PR.
