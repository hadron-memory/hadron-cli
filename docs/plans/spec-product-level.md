# Implementation Plan: product citation level + tiered general-provisions contracts

> **Status: in progress** on branch `feat/spec-product-level`. Resolves
> [#17](https://github.com/hadron-memory/hadron-cli/issues/17) (add a `<product>`
> top level to the citation scheme) and
> [#18](https://github.com/hadron-memory/hadron-cli/issues/18) (generalize the
> `00` general-provisions contract to the module and product tiers). This doc is
> the review artifact and is bundled in the PR.

## Context

`hadron spec` (shipped in [#12](https://github.com/hadron-memory/hadron-cli/issues/12))
runs product specs like a legal code: a spec's `loc` **is** its citation,
`<module>:<feature>:<rule>[:<flow>]` (e.g. `msg:010:02:03`), each colon level a
real parent/child node, a `register` node governing the code table, numbers
never renumbered. The memory (`<org>::<memory>`) is the de-facto product
boundary — one `::specs` memory per product.

That breaks when **one memory holds specs for multiple products** — exactly
Hadron dogfooding its own multi-product corpus (`cli`, `srv`, `por`, …) in
`hadronmemory.com::platform-specs`. `module` is overloaded: is `cha` the CLI's
`chat` command group or the server's chat subsystem?

Two coupled changes fix it:

- **#17** adds **product** as a real top level: `<product>:<module>:<feature>:<rule>[:<flow>]`.
- **#18** gives every tier a general-provisions contract, not just the feature.

### Ground truth (verified against the live server)

- `micromentor.org::platform-specs` — a populated **flat** 4-level corpus
  (`mat`/`msg`/`acc`, feature contract `msg:010:00`). Must keep working
  **unchanged** → flat stays the default and is fully backward compatible.
- `hadronmemory.com::platform-specs` — **empty/greenfield**. The product scheme
  debuts here, so there is **no data migration anywhere**.

## Decisions (settled with the user)

1. **Per-memory mode, flat = legacy default, product opt-in.** A memory is
   either flat or product-rooted; never mix the two arities in one memory (a
   lint warning enforces the hygiene). Matches issue #17's suggested rollout.
2. **The grammar is self-describing for any real (≥2-segment) citation** — the
   character class of segment 2 decides it: `cli:cha:…` (2nd seg alpha) is
   product-rooted; `msg:010:…` (2nd seg numeric) is flat. The only ambiguous
   input is a *lone* 3-letter code (`cli` — product or module?), which creation
   commands disambiguate with explicit `--product`/`--new-product` intent and
   which is benign for read commands (top nodes have no parent, and ToC matching
   is by loc string). So **no stored mode is required for the tool to function.**
3. **Scheme is *declared* in `Memory.data`** (a `JSON` field on `Memory`, added
   to hadron-server alongside this work) under a `spec` namespace:
   `{"spec":{"scheme":"product"}}`. The new **`spec describe`** command reads it
   and reports it as authoritative; `spec describe --declare flat|product`
   writes it; and the scheme is always cross-checked against what the live nodes
   look like (derive-from-live), with any drift flagged. An empty memory can
   declare its arity before it has nodes. See *Scheme storage*.
4. **Product-level contract = a reserved alpha module code, `gen`.** Keeps the
   grammar self-describing (segment 2 stays alpha ⇒ product-rooted). The feature
   and module contracts keep their numeric spellings (`:00`, `:000`) — they are
   in live use and the numeric tiers can't hold letters. The three contracts
   **mirror in role** (each is the reserved "zero of the tier"), and a single
   `--contract` shorthand addresses/creates the contract at any tier.

## The citation grammar (as designed)

```
flat (legacy):   <module>:<feature>:<rule>[:<flow>]            msg:010:02:03
product:        <product>:<module>:<feature>:<rule>[:<flow>]   cli:cha:010:01:02
```

Segment regexes are unchanged: product/module `[a-z]{3}`, feature `[0-9]{3}`,
rule/flow `[0-9]{2}`.

`ParseCitation(s)`:
1. Split on `:`; reject empty.
2. **Decide rooting from segment 2's character class.** `len≥2` and
   `parts[1]` is `[a-z]{3}` ⇒ product-rooted (`parts[0]`=product, `parts[1]`=module).
   `len≥2` and `parts[1]` is `[0-9]{3}` ⇒ flat (`parts[0]`=module, `parts[1]`=feature).
   `len==1` ⇒ lone code, parsed as a flat module (Module set); ambiguous, callers
   that need a product root build the `Citation` directly.
3. Validate each remaining segment with its regex; enforce max depth (flat ≤ 4
   segments, product ≤ 5).

`Citation` gains a `Product string` field. **Level is unchanged for tiers ≥ 1**
(module 1, feature 2, rule 3, flow 4) so all existing `Level()`-based logic keeps
working; a **product root is Level 0**. The `Product` field — not the level —
distinguishes a flat module root (`msg`, no parent) from a product's module root
(`cli:cha`, parent = product root `cli`).

| method | flat behavior (unchanged) | product additions |
|---|---|---|
| `Level()` | module 1 … flow 4 | product root → 0 |
| `Format()` | `msg:010:02` | prepends product: `cli:cha:010:02` |
| `Parent()` | flow→rule→feature→module→∅ | module → product root (when `Product` set) |
| `IsContract()` | rule `00` | feature `000` (module contract); module `gen` (product contract) |
| `InheritedContractLoc()` | rule → feature's `:00` | feature → module's `:000`; module → product's `:gen` |

`InheritedContractLoc` replaces the feature-only `ContractLoc`: every
non-contract node inherits the reserved "zero" sibling **at its own tier**
(rule→`00`, feature→`000`, product-rooted module→`gen`). Contracts are
inheritance *sources*, not sinks — they carry no inheritance edge themselves
(matching today's `:00`).

### Contracts, summarized

| tier | inheritors | contract loc | reserved value |
|---|---|---|---|
| feature | its rules | `msg:010:00` / `cli:cha:010:00` | rule `00` |
| module | its features | `msg:000` / `cli:cha:000` | feature `000` |
| product | its modules | `cli:gen` | module `gen` |

`000` is already unreachable by feature allocation (features start at `010`,
step 10); `gen` is alpha so it is never auto-allocated (module codes are
author-chosen, never minted). Both are reserved exactly the way rule `00` is.

## Command surface changes

```
spec new   ... [--product <ppp> | --new-product] [--new-module] [--contract]
spec ls    ... [--prefix <loc>]                 # already product-capable (server prefix match)
spec lint  ... [--product <ppp>]                # product-scoped corpus lint
spec describe -m <org::memory> [--json]         # NEW — report the corpus's spec scheme
spec register / find / get / supersede          # product-aware via the shared grammar
```

- **`spec new`** target resolution (deepest wins):
  - `--new-product cli` → product root `cli` (Level 0).
  - `--product cli --new-module --module cha` → module `cli:cha` under the product.
  - `--contract` → the reserved contract at the deepest specified tier:
    `--product cli --contract`→`cli:gen`; `--module msg --contract`→`msg:000`;
    `--feature 010 --contract`→`msg:010:00` (same as the old `--rule 00`).
  - All existing flat paths (`--new-feature`, `--feature`, `--rule`, `--flow`)
    are unchanged; `--product` simply qualifies the module they hang under.
  - Inheritance edges generalize: a new feature → module `:000`, a new
    product-rooted module → product `:gen`, a new rule → feature `:00` (as today),
    each wired only when the target contract exists in the corpus.
- **`spec describe`** — one `Nodes` scan, parse every loc, then report:
  scheme (`flat` | `product` | `mixed` | `empty`), the tier names + contract
  codes, per-tier counts, and a `mixed-arity` warning if both shapes appear.
  `--json` emits a stable DTO. When `Memory.data` is available it also prints the
  **declared** scheme and flags any drift vs. the derived one.
- **`spec lint`** — `parent-exists` now follows the product chain; the
  `inheritance-edge` check covers all three contract tiers; a new `mixed-arity`
  warning fires when a memory mixes flat and product roots; `--product <ppp>`
  scopes a corpus lint to one product.
- **`spec register`** — the derived ledger groups by product → module → feature
  → rule for product corpora and stays flat for flat corpora.

## Scheme storage (`Memory.data`)

`Memory` gained a `data: JSON` "client-defined bag" field in hadron-server,
exposed on `memory` / `createMemory` / `updateMemory`. CLI convention
(namespaced so `data` stays general):

```json
{ "spec": { "scheme": "flat" | "product" } }
```

Wiring:
1. the vendored `schema/schema.graphql` is refreshed from the server SDL;
2. `data` is selected in `GetMemory` and accepted by `UpdateMemory`
   (`internal/api/queries/memories.graphql`, regenerated);
3. `spec describe` reads the declared scheme (resolving the memory id by
   normalizing the `::` separator and matching `myMemories`), reports it as
   authoritative, and flags drift vs. the derived view;
   `spec describe --declare flat|product` merges the scheme into `Memory.data`,
   preserving any other keys.

Derivation still covers every non-empty memory, so the declaration is optional;
it exists so an *empty* memory can announce its arity before it has nodes
(`new --new-product`/`--new-module` also work from empty and self-bootstrap the
shape).

## Code changes by file (`internal/cmd/spec/`)

| file | change |
|---|---|
| `spec.go` | `Product` field; product-aware `ParseCitation`; `Level` 0 for product root; `Format`/`Parent`/`IsContract`; `InheritedContractLoc` (replaces `ContractLoc`); `productContractCode = "gen"`, `moduleContractFeature = "000"`. |
| `allocate.go` | `childNumbersAt` matches `Product` too; numeric tiers unchanged; reservation of `000`/`gen` documented (already enforced by the floor / alpha). |
| `new.go` + `rubric.go` | `--product`/`--new-product`/`--contract` flags; `planTarget` handles product root, product-qualified module, and tiered contracts; generalized inheritance edges; contract rubric. |
| `lint.go` | product-chain `parent-exists`; generalized `inheritance-edge`; `mixed-arity` warning; `--product` scope. |
| `register.go` | product → module → feature → rule grouping; product-aware drift. |
| `describe.go` (new) | `spec describe`; derive-from-live scheme report + DTO. |
| `find.go` / `ls.go` / `get.go` / `supersede.go` | inherit the grammar; minor product-awareness (e.g. supersede inheritance via `InheritedContractLoc`). |

## Tests

- **Pure logic** (`spec_test.go`, `allocate_test.go`, `lint_test.go`): product
  parse/format/level/parent; `cli:gen`/`msg:000` contract recognition;
  `InheritedContractLoc` at all three tiers; allocation still skips `000`/`gen`;
  lint `mixed-arity`, product-chain parent, tiered inheritance edges.
- **Command/wiring** (`spec_commands_test.go`): `new --new-product`,
  `new --product … --new-module`, `new … --contract` (asserts `cli:gen` /
  `msg:000` / `:00` loc + edges); `describe` (derived scheme JSON); product-aware
  `register`; a flat-corpus regression for every command.

## Verification

- `go build ./...`, `go vet`, `gofmt`, `go test ./...`, `make lint` green.
- Read-only live smoke: `spec describe` on both corpora (micromentor → `flat`,
  Hadron → `empty`); `new --dry-run` product scaffolding against the Hadron
  corpus (product root, module, `cli:gen`, `cli:cha:000`); flat-corpus
  regression (`register`, `lint --all`, `new --dry-run`) on micromentor — no
  writes.

## Out of scope / follow-ups

- `docs/reference/hadron-cli.md` (#product-specs) and
  `docs/how-to/maintain-product-specs.md` — referenced by #17; started here,
  fleshed out as the corpus grows.
- Spec Kit / source import (still stubs).
