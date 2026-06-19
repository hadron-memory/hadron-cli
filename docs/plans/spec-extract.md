# Implementation Plan: `hadron spec extract` — first-class split

> **Status: implemented and verified** on branch `feat/spec-extract` (not yet
> merged). Design *as built* and the review artifact for the change. Closes
> item #2 of
> [hadron-cli#41](https://github.com/hadron-memory/hadron-cli/issues/41)
> ("No first-class extract / split"), the highest-value remaining write-side
> gap. Ships as its own PR; items #1 (`spec edit`) and #4 (`spec link`) are
> tracked separately under the same theme. `--strip-source` (true "move") is
> included per the issue author's call.

## Context

Extracting a durable rule out of a fat parent spec is the *core* restructuring
activity the add-spec skill describes, yet today it is ~5 manual steps — read
the source raw, pick the chunk, `spec new` the chunk into a fresh citation,
hand-edit the source to drop the chunk, and hand-wire the cross-reference edge
back to the parent — **each a full-body replace** with no diff and no
convention guard. A transcription slip silently corrupts a node, and the
field→entity edge direction/label has to be reverse-engineered from neighbors.

The motivating walkthrough (the issue) extracted `cor:dmo:020:04 — Node type`
out of the `cor:dmo:060:02 — Node` entity spec in `hadronmemory.com::specs`.
That extraction is already live (done by hand), so its shape is the spec for
this command:

- New field-spec `cor:dmo:020:04` lives under feature `020` (the *fields*
  feature) — a **different feature** from the source entity's `060`.
- It carries an outgoing cross-ref edge `cor:dmo:020:04 → cor:dmo:060:02`
  labeled **"documents the nodeType field of Node"**, mirroring its siblings
  `cor:dmo:020:02 → cor:dmo:060:02` ("documents the abstract field on the Node
  entity") and `:020:03 → :060:02`.

So an extract is, mechanically, **`spec new` under `--to-feature` with the moved
chunk as the body, plus one cross-ref edge `new → source`, plus reminders** —
it composes machinery that already exists.

## Command surface

```
hadron spec extract <source-citation> --to-feature <fff> --title "…" [flags]
```

| flag | meaning |
|------|---------|
| `<source-citation>` (positional, required) | the parent we extract *out of* (e.g. `cor:dmo:060:02`). Its product+module set the destination root; must already exist. |
| `--to-feature <fff>` (required) | existing feature under the same product:module where the new rule lands (e.g. `020`). Must exist. (v1: existing feature only — `--new-feature` is a later add.) |
| `--rule <rr>` | exact rule number under `--to-feature`; omit to allocate the next free rule (reuses `allocateChild`). |
| `--title "…"` (required) | the new spec's title. |
| `--content -` / `--content-file <path>` / `-c <inline>` | the **moved chunk** = the new spec's body. Omitted ⇒ rubric template (with a reminder), but the normal path pipes the chunk in. |
| `--abstract …` / `--abstract-file <path>` | new spec's abstract; default the lint-flagged placeholder (same as `spec new`). |
| `--ref-label "…"` | the cross-ref edge label (`new → source`). Default: a synthesized template; the result prints the edge so the author can refine it with `edge update`. |
| `--strip-source` | also trim the moved chunk out of the source body (true "move"). Verbatim-match only — see below. Requires the chunk to be supplied (`--content`/`--content-file`/stdin). |
| `--tag`, `--no-edges`, `--dry-run`, `--json`, `-m/--memory` | identical semantics to `spec new`. |

Examples:

```
# Pipe the moved chunk in; allocate the next rule under 020.
hadron spec get cor:dmo:060:02 -m hadronmemory.com::specs --body-only \
  | sed -n '/## Node type/,/## /p' \
  | hadron spec extract cor:dmo:060:02 -m hadronmemory.com::specs \
      --to-feature 020 --title "Node type" --content - \
      --ref-label "documents the nodeType field of Node"

# Preview the allocation + edges without writing.
hadron spec extract cor:dmo:060:02 -m hadronmemory.com::specs \
  --to-feature 020 --rule 04 --title "Node type" --dry-run
```

## Behavior — pure-additive (never mutates the source)

1. Parse `<source-citation>`; derive product+module; confirm the source node
   exists (a real `fetchSpecNode`, so a typo fails fast and `--ref-label`'s
   default can use the source name).
2. Scan the product/module subtree paged to exhaustion (issue #23), exactly as
   `spec new` does for allocation.
3. `planTarget` with `{product: source.Product, module: source.Module,
   feature: to-feature, rule: rule}` → new citation + ToC parent
   (`<product>:<module>:<to-feature>`) + inheritance loc (the to-feature's `:00`
   contract, if present). This reuses *all* of `new`'s frozen-code / parent-
   exists / allocation rules unchanged.
4. Resolve the moved-chunk body (`resolveBody`) and abstract
   (`cmdutil.ResolveTextInput`), same guards as `new`.
5. Create the new node via `gen.UpsertNode` (`createOnly`, `nodeType: info`,
   `data.version`) — byte-for-byte the `new` create path.
6. Wire edges (unless `--no-edges`): ToC (`new → to-feature`, label = title),
   inheritance (`new → to-feature:00` if present), **and the cross-ref
   `new → source` with `--ref-label`** (the one new edge).
7. **If `--strip-source`:** trim the moved chunk out of the source body (see the
   strip contract below) and `node update` the source — the one place `extract`
   touches an existing spec, and only behind the opt-in flag.
8. Print reminders on the human path: refresh the abstract on the new spec **and
   on the source** (the source's abstract may now describe content that left).
   Without `--strip-source` (or on a strip miss), also remind to trim the source —
   `hadron spec get <source> --body-only | hadron node update <source-urn> --content -`
   (or `spec edit <source>` once item #1 lands).
9. `--dry-run` previews the planned citation + edges + body **and the strip
   outcome** (would-trim N chars / chunk not found verbatim), and writes nothing.

**`--strip-source` contract (safe "move"):**
- The moved chunk is already in memory (it is the new spec's resolved body), so
  the match needs no stdin re-read.
- Match is **verbatim**: `strings.TrimSpace(chunk)` must occur **exactly once** as
  a substring of the source body. Found once ⇒ remove that span and tidy the seam
  (collapse a 3+ newline run to a blank line, normalize the trailing newline).
  Not found, or found more than once (ambiguous) ⇒ **leave the source untouched,
  warn, and keep going** — the additive create already succeeded.
- Ordering: create the new node + edges **first**, then strip — so a strip miss or
  a failed source update never costs the extraction; the trim is best-effort and
  the reminder covers the miss.
- `--strip-source` with no chunk supplied (body would fall back to the rubric) is a
  usage error.
- This keeps the destructive edit deterministic and reversible-by-eye: a
  reformatted chunk simply doesn't match and the source is left for the author.

## Reuse / factoring

`extract` is mostly orchestration over existing helpers: `planTarget`,
`allocateChild`, `scanAllNodes`, `resolveBody`, `cmdutil.ResolveTextInput`,
`specName`/`specTags`/`specDataRaw`/`placeholderAbstract`, `gen.UpsertNode`,
`resolveSpecNode` + `gen.CreateEdge`, `output.Write`. The only genuinely new
logic is deriving the destination citation from `<source>` + `--to-feature` and
the cross-ref edge.

The only genuinely new helpers are `planExtract` (citation derivation) and
`stripChunk` (the verbatim trim); the cross-ref edge itself is just one more
entry in the existing plan-then-create edge loop `spec new` already runs
(`resolveSpecNode` + `gen.CreateEdge`), so no new wiring was needed. A future
`spec link` (issue item #4) shares the *convention* — direction (field→entity),
sentence-style label, and the resolve+create pattern — and adds its own
endpoint validation (both ends are specs in one corpus); `extract` skips that
check because it already holds both nodes.

## Files

- `internal/cmd/spec/extract.go` (new) — the command + two pure helpers:
  `planExtract(source, toFeature, rule)` (citation derivation) and
  `stripChunk(sourceBody, chunk) (trimmed string, ok bool)` (verbatim-once
  removal + seam tidy) — both unit-testable without GraphQL.
- `internal/cmd/spec/spec.go` — register `newCmdExtract`. (The stable
  `extractResultDTO` — `new`'s shape + a `source` field + the strip outcome —
  lives next to its renderer in `extract.go`, mirroring `newResultDTO` in
  `new.go`.)
- `internal/cmd/spec/extract_test.go` (new, package `spec`) — unit tests for
  `planExtract` (derivation, allocate-next, bad inputs).
- `internal/cmd/spec_commands_test.go` — command-level tests against the fake
  GraphQL (UpsertNode/CreateEdge/Nodes/GetNodeById/ResolveUrn by op name).
- `internal/cmd/agentic/agentic-usage.md` — add `extract` to the `spec` synopsis
  line + a prose sentence (the `--json` contract doc).
- `docs/plans/spec-extract.md` — this doc, updated to "as built" on completion.

## Tests

Command-level (fake GraphQL keyed by operation name, `captureGraphQL` for vars):

- **Happy path** — `extract cor:dmo:060:02 --to-feature 020 --rule 04 --title
  "Node type" --content -` (chunk on stdin): assert `UpsertNode` loc =
  `cor:dmo:020:04`, name = `cor:dmo:020:04 — Node type`, body = the piped chunk;
  the `CreateEdge` calls include `new → cor:dmo:060:02` with the `--ref-label`;
  reminders printed to stderr.
- **Allocate-next** — no `--rule`, subtree scan returns `020:01..03` ⇒ new loc
  `cor:dmo:020:04`.
- **Source not found** ⇒ exit NotFound (the source fetch fails).
- **`--to-feature` not found** ⇒ exit NotFound (from `planTarget`).
- **`--dry-run`** ⇒ no `UpsertNode`/`CreateEdge`/source-`UpsertNode` calls; strip
  preview rendered.
- **`--ref-label` default vs explicit** — default synthesizes a template; explicit
  is passed through to the edge.
- **`--strip-source` hit** — chunk occurs once in the source ⇒ a second
  `UpsertNode` updates the source with the trimmed body; reminder drops the
  trim line.
- **`--strip-source` miss** — chunk absent / ambiguous ⇒ **no** source
  `UpsertNode`, a warning, and the new node + edges still created.
- **`--strip-source` without a chunk** ⇒ usage error.
- **Guards** — `--content - --abstract -` rejected; `--abstract` + `--abstract-file`
  rejected (mirrors `new`).
- **`--json`** — `extractResultDTO` shape is stable.

Unit (`extract_test.go`): `planExtract` derives the right destination citation
from product-rooted and flat sources, allocates the next rule, and rejects a
malformed `--to-feature`. `stripChunk` covers: found-once (trim + seam tidy),
not-found (ok=false), found-twice/ambiguous (ok=false), and whitespace-only
chunk (ok=false).

## Contract / behavior notes

- **Additive by default.** `spec extract` creates one node and up to three edges;
  it touches an existing node only under `--strip-source`, and only to remove a
  verbatim-matched chunk from the source body (best-effort, never on a miss). No
  new clearing semantics.
- **New `--json` shape** `extractResultDTO` lives in the command package (stable
  contract), initialized with `[]` slices, marshaled via `output.Write`.
- **Exit codes** route through `api.MapError` / `exitcode.Newf` — Usage (bad
  flags), NotFound (source/feature missing), Conflict (an explicit `--rule` that
  already exists fails the `createOnly` upsert).
</content>
</invoke>
