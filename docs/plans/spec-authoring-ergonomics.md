# Spec authoring ergonomics (#69 items 1 & 2)

Reduce the friction of authoring a new spec module and its first
feature/rule, hit while building `cor:brd` in `hadronmemory.com::specs`.
Two independent items, shipped as two PRs.

## Item 2 — tier-aware `spec new` templates (this PR)

**Problem.** `spec new` scaffolds the *same* generic body (the four-section
rubric: Definition / Rule & examples / Durable vs tunable / What invalidates)
and the same placeholder abstract for **every** tier. But a module root, a
feature root, and a rule have different house shapes, so the author opens a
sibling and hand-rewrites the structure each time.

**Design.** Pick the skeleton + abstract stub from the citation's tier
(`Citation.Level()` + `IsContract()`). New dispatchers in `rubric.go`:

| Tier (Level) | Body skeleton | Why it's lint-safe |
|---|---|---|
| product root (0) | `## Modules` index | lint only checks loc/name/nodeType for level < 3 |
| module root (1) | `## Features` index | same |
| product `:gen` / module `:000` contract (1–2) | general-provisions prose + `## Provisions` | same |
| feature root (2) | one-line "load-bearing point" + `## Rules` child list | same |
| feature `:00` contract (3) | general-provisions prose + **`## What invalidates this spec`** | level 3 ⇒ lint requires the invalidates statement; included |
| rule (3) / flow (4) | the existing four-section rubric (`rubricBody`) | unchanged |

`tierBody(c, title)` delegates to `rubricBody` for rules/flows and to the new
header/contract builders otherwise. `tierAbstract(c, title)` returns a
tier-worded placeholder (still carrying the `TODO(abstract):` marker so lint
keeps reminding the author to replace it at the rule tier, where the abstract
is the load-bearing RAG retrieval surface).

Only the two call sites in `new.go` change: `resolveBody`'s default
(`rubricBody` → `tierBody`) and the empty-abstract fallback
(`placeholderAbstract` → `tierAbstract`). No flag, schema, or wire changes.

**Tests.** Unit-test each tier's body/abstract (`rubric_test.go`); an
integration `spec new … --new-module --dry-run` asserts the Features index, and
a feature-`:00` `--contract --dry-run` asserts the invalidates section is
present (so the scaffold passes its own lint).

## Item 1 — fewer calls to stand up a module (next PR)

**Problem.** A fresh `module → :000 → feature → :00 → rule` tree is ~4 `spec
new` calls, and `--new-module` does **not** create the module's `:000`
general-provisions contract — yet features inherit it, so you need a separate
`--contract` call before `--new-feature` or the inheritance target is missing.

**Design (sketch).** `--new-product` / `--new-module` / `--new-feature` also
scaffold their tier's general-provisions contract (`<p>:gen` / `<m>:000` /
`<f>:00`) by default — `--no-contract` opts out. This needs `spec new` to
create more than one node per call: the result DTO grows an `also
[]newResultDTO` (omitempty) carrying the co-created contract, and the contract
is wired with a ToC edge to the new root. Each co-created node gets its
tier-aware template (item 2), so item 2 lands first.

**Deferred.** The full one-shot chain (`spec new cor:brd:010:01 --new-path`,
allocating/creating every missing ancestor in one call) is a further
convenience on top of auto-contracts; tracked in #69, not in these two PRs.
The `/add-spec` skill's "create `:000` separately" guidance will need updating
once item 1 ships (the skill lives in Hadron, not this repo).
