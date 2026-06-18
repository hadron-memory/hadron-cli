# Implementation Plan: spec write-side DX + lint hardening

> **Status: implemented and verified** on branch `spec-dx-release` (not yet
> merged). Design *as built* and the review artifact for the change. Closes
> [hadron-cli#42](https://github.com/hadron-memory/hadron-cli/issues/42) and
> the [#35](https://github.com/hadron-memory/hadron-cli/issues/35) message
> remedy; advances
> [#41](https://github.com/hadron-memory/hadron-cli/issues/41) (abstract files,
> `spec get --body-only`) and
> [#38](https://github.com/hadron-memory/hadron-cli/issues/38) (`--abstract-file`).

## Context

A pass over the `spec` workflow surfaced three independent, small gaps — all on
the read/write ergonomics and lint surface, none touching the citation model.
Bundled here as one "spec DX + lint" theme; each is severable into its own PR.

1. **Findability hole is invisible (#42).** `spec find` silently degrades to
   keyword search on a memory with no vector index — and the abstract, the
   documented RAG retrieval surface every spec must carry, isn't actually
   embedded. Nothing flagged this; a spec that plainly existed returned nothing
   from `find`.
2. **The inheritance-edge lint names the gap, not the fix (#35).** When a
   general-provisions contract is added to a tier that already has siblings,
   every sibling is instantly non-compliant. The remedy is mechanical and fully
   determined (`edge add`, fixed direction + label), but the finding didn't
   spell it out — an author had to already know to reach for `edge add`.
3. **Paragraph fields fight the shell, and there's no clean edit round-trip
   (#41/#38).** `--abstract` was inline-string-only, so an abstract with
   backticks/newlines is hostile to quoting; and `spec get` had no way to emit
   just the raw body for a `… | node update --content -` round-trip.

## What changed (as built)

### #42 — `spec lint` warns when the memory has no vector index

- **`internal/cmd/spec/lint.go`** — new `vectorIndexWarning` helper resolves the
  memory (reusing `resolveSpecMemoryID` + `gen.GetMemory`, the same path
  `spec describe` already uses) and, if `vectorIndexEnabled` is false, returns a
  memory-level finding (citation `(memory)`, rule `vector-index`, severity
  `warning`). `RunE` appends it after the per-node/corpus findings, so it covers
  every scope — a single `<citation>`, `--product`/`--module`, and `--all`. It
  is **best-effort**: the node scan already proved the memory reachable, so a
  failed lookup skips the check rather than failing the lint. `--strict`
  promotes it to an error (exit 5) via the existing promotion loop.

### #35 — actionable inheritance-edge remedy

- **`internal/cmd/spec/lint.go`** — `lintCorpus` gains a `memURN` parameter so
  the `inheritance-edge` finding can name a copy-pasteable fix with
  fully-qualified node refs:
  `… — add it: hadron edge add --from <org::mem::child> --to <org::mem::contract> --label "inherits the shared contract (general provisions)"`.
  The label reuses the existing `inheritEdgeLabel` constant, so it can't drift
  from what `spec new` auto-wires.

### #41/#38 — abstract files/stdin + `spec get --body-only`

- **`internal/cmdutil/textinput.go`** (new) — `ResolveTextInput(flag, value,
  file, stdin)` factors out the `--x` / `--x-file` / `--x -` (stdin) convention
  that `--content` already used, so every text flag behaves identically and the
  error messages name the right flag.
- **`internal/cmd/node/update.go`** — adds `--abstract-file`; `--abstract`/`-`
  and `--abstract-file` now route through `ResolveTextInput`. A guard rejects
  `--content -` together with `--abstract -` (stdin is consumable once).
- **`internal/cmd/spec/new.go`** — same `--abstract-file` / `--abstract -`
  support for the scaffolded abstract (falls back to the placeholder when
  empty), with the same dual-stdin guard.
- **`internal/cmd/spec/get.go`** — adds `--body-only`: for a single citation it
  prints only the raw markdown body (no metadata/edges/lint). Mutually exclusive
  with `--abstract-only` and with `--prefix` (single-citation only). New stable
  `--json` shape `specBodyDTO` (`{citation, content}`) in `spec.go`.

### Tests

- **`internal/cmdutil/textinput_test.go`** (new) — inline / file / stdin / the
  mutual-exclusion and missing-file errors / empty.
- **`internal/cmd/spec/lint_test.go`** — `lintCorpus` calls carry the new
  `memURN`; `TestLintCorpusInheritanceAndParent` asserts the finding message
  contains the exact `hadron edge add …` command with qualified refs and the
  shared label.
- **`internal/cmd/spec_commands_test.go`** — `vector-index` warn / `--strict`
  promotes / indexed-memory stays silent; `spec get --body-only` (raw body, no
  metadata) + `--json` shape + the mutual-exclusion guards. The two existing
  lint tests that now reach the vector-index probe mock an indexed memory so
  their assertions are unchanged.
- **`internal/cmd/commands_test.go`** — `node update --abstract-file` reads the
  file; `--content - --abstract -` is rejected.

## Contract / behavior notes

- **New lint finding.** `spec lint [--json]` can now emit a finding with rule
  `vector-index` at citation `(memory)`. The `(memory)`-citation pattern is
  already established (`mixed-arity`), so this is an additive value, not a shape
  change. It is a *warning* (non-fatal) unless `--strict`.
- **`spec get --body-only`** is a new output mode: raw markdown on the human
  path, `{citation, content}` on `--json`. The existing single-citation and
  `--prefix` shapes are unchanged.
- **No new clearing semantics.** `--abstract-file`/`--abstract -` reuse the
  omitted-vs-explicit rules already in `node update`; an empty result clears
  only when the flag was explicitly passed.
