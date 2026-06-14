# Implementation Plan: `hadron spec` — product specification management

> **Status: implemented and verified** on branch work (not yet committed). This
> document reflects the design *as built* — including the deviations from the
> original plan and the lint recalibration discovered while testing against the
> live corpus. It is the review artifact for the change.

## Context

`hadron-cli` is today a thin, typed wrapper over the Hadron GraphQL API
(`auth`, `memory`, `node`, `edge`, `app`, `config`, `api`). This adds the
**first CLI-only feature set**: an opinionated `hadron spec` command group for
maintaining "product spec" nodes in a Hadron memory.

The conventions come from the live memory `hrn:memory:micromentor.org::platform-specs`
and its authoring procedure node `micromentor.org::mmdata::tasks:add-platform-spec`.
Specs are run like a **legal code**: a spec's `loc` **is** its citation number
(`<module>:<feature>:<rule>:<flow>`, e.g. `msg:010:02:03`), each colon level is a
real parent/child node, a `register` node governs the code table + number ledger,
every spec follows a fixed rubric (mandatory abstract + a "what invalidates"
statement), and numbers are **never renumbered** — to replace a spec you
*supersede* it (new number, old retired, `superseded-by` edge).

These rules can't be expressed by the generic `node`/`edge` commands. `hadron spec`
encodes them. It is **general** — it works on any memory following the
convention, addressed by `-m/--memory`, not hardcoded to micromentor.

## Decisions (resolved with the user)

1. **Register = advisory/read-only.** Next-free numbers are **derived by scanning
   live spec nodes**; the tool never edits the register node. `spec register`
   renders the computed ledger; `--check` flags drift vs the hand-written ledger.
2. **Semantic find = the default.** `spec find <query>` searches by meaning
   (`nodeSearch`, **hybrid** keyword+vector — degrades to keyword with a note on
   memories without a vector index). `--match-exactly` forces literal keyword.
3. **General tooling**, not hardcoded to one memory.
4. **Scaffold = rubric template by default** (non-interactive, agent-friendly),
   with `--abstract`/`--content-file`/`--content -` overrides. No `$EDITOR`.
5. **Import (Spec Kit / source) = stubs** for now (exist, validate args, exit 2).

### Terminology: `module`, not `service`

The top loc segment (the frozen 3-letter code) is a **module** in the CLI — a
general term for any memory. The platform-specs memory labels its modules
"services" in prose (`mat — Matching Service`); that's just that memory's name
for the same thing — the CLI only operates on the 3-letter code.

## Addressing model

- Memory URN form is `<org>::<memory>` (e.g. `micromentor.org::platform-specs`).
  The loc uses single colons (`msg:010:02`). A fully-qualified node URN is
  `hrn:node:<org>::<memory>::<loc>`.
- `cmdutil.ResolveNodeURN` prepends `hrn:node:` to any bare ref with ≥2 colons,
  so the spec package builds a node ref as `<org>::<memory>` + `::` + `<citation>`
  and lets the resolver/server do the rest. `-m/--memory` accepts the bare or the
  `hrn:memory:`-prefixed form (a helper strips the scheme prefix).
- There is no "active memory" in the CLI (that is MCP session state); every
  subcommand takes `-m/--memory`. **Verified against the live server.**

## Command surface (as built)

```
hadron spec <command>                                  (alias: specs)

  ls   [-m <org::memory>] [--prefix <loc>] [--limit N] [--offset N]   (alias: list)
  get  <citation> -m <org::memory> [--abstract-only]
  register -m <org::memory> [--check]                                 (alias: reg)
  find <query> -m <org::memory> [--match-exactly] [--limit N] [--tag <t>]
  new  -m <org::memory> --module <mmm> --title <t>
       [--feature <fff> | --new-feature] [--rule <rr> | --rule-after <rr>] [--flow <uu>]
       [--plevel <N>] [--tag <t>]... [--abstract <text>]
       [--content-file <path> | --content -] [--inherit <citation>]
       [--new-module] [--no-edges] [--dry-run]                        (alias: scaffold)
  lint [<citation>] -m <org::memory> [--module <mmm>] [--all] [--strict]   (aliases: check, validate)
  supersede <old-citation> -m <org::memory> --title <t>
       [--feature <fff>] [--rule-after <rr>] [--reason <text>] [--copy-body] [--yes] [--dry-run]
  import spec-kit <path> -m <org::memory>     # STUB (exit 2)
  import code     <path> -m <org::memory>     # STUB (exit 2)
```

## Package layout — `internal/cmd/spec/`

Mirrors `internal/cmd/memory/`; calls the generated `gen.*` functions directly.

| File | Contents |
|---|---|
| `spec.go` | group root (alias `specs`); DTOs; `Citation` parser/validator; `memoryURNFromFlag`/`specNodeRef`/`resolveSpecNode`/`fetchSpecNode`/`fetchRegister`; `specNode` model + `nodeFromGQL`; `registerLedger`/`parseLedger`. |
| `allocate.go` | pure numbering: `levelSpec`, `childNumbersAt`, `nextNumber`, `allocateChild`. |
| `rubric.go` | scaffold template + heading consts + `specName`/`specTags`/`specDataRaw`/`placeholderAbstract`. |
| `lint.go` | `newCmdLint` + pure `lintNode`/`lintCorpus` rule engine + corpus scans. |
| `ls.go` `get.go` `register.go` `find.go` | read/navigate commands. |
| `new.go` | scaffold command + `planTarget`. |
| `supersede.go` | supersede workflow + `planReplacement`. |
| `importcmd.go` | `import` group + two stubs. |
| `spec_test.go` `allocate_test.go` `lint_test.go` | pure-logic unit tests. |

Wired in [internal/cmd/root.go](internal/cmd/root.go); command/wiring tests added to
`internal/cmd/spec_commands_test.go`.

## GraphQL changes — [internal/api/queries/nodes.graphql](internal/api/queries/nodes.graphql) (+ `make generate`)

1. Added `data` and `abstractOriginHash` to the `GetNodeById` selection set.
2. Added a `NodeSearch` query (`query!, mode, memoryUrn, limit → degraded, reason, nodes`)
   — the default `find` path. `SearchMode` is `{hybrid, keyword, vector}`.

All additive; existing commands ignore the new fields. A generic
`cmdutil.Confirm` (same `--yes`/non-interactive gate as `ConfirmDeletion`, correct
"retire" wording) was added for `supersede`.

## Key mechanics

- **Citation grammar** (`spec.go`): module `[a-z]{3}`, feature `[0-9]{3}`, rule/flow
  `[0-9]{2}`; deeper level requires shallower. `Level/Parent/IsContract/ContractLoc/Format`.
- **Numbering policy** (`allocate.go`): **features are numbered in tens**
  (`010, 020, 030, …`); **rules and flows increment by one**; **rule `00`** is the
  feature's general-provisions contract and is never auto-allocated. Allocation is
  **monotonic** — strictly above the current max, so retired numbers are never
  recycled (gaps are fine). `--rule-after` raises the floor for the rare
  retired-above-max case. *(Verified: `spec register` derives `msg` → next feature
  `020`, `msg:010` → next rule `04`, matching the hand-written ledger.)*
- **Scaffold** (`new.go`/`rubric.go`): one `gen.Nodes(prefix=<module>)` scan does
  both parent-existence checks and allocation; `gen.UpsertNode(createOnly)` writes
  `nodeType=info`, `name="<loc> — <title>"`, `tags=[spec,p<N>,…]`, `data={"version":"0.0.1"}`,
  abstract (or a lint-flagged placeholder), and the rubric body; then ToC + inheritance
  edges via `gen.CreateEdge`. `--dry-run` mutates nothing.
- **Find** (`find.go`): default `gen.NodeSearch(mode:hybrid)` + post-filter to spec
  nodes, surfacing `degraded`/`reason`; `--match-exactly` → `gen.Nodes(search, tags:[spec])`.
- **Lint** (`lint.go`): pure engine over the `specNode` model. **Errors:** loc-shape,
  name `"<loc> — "` prefix, `nodeType=info`, `spec` tag, exactly one `p<N>`,
  abstract present, a "what invalidates" statement, duplicate-loc, parent-exists.
  **Warnings:** `data.version`, ToC edge, inheritance edge, register drift.
- **Supersede** (`supersede.go`): validate old (numbered, not already superseded) →
  allocate the next number (same level; `--feature` relocates) → `cmdutil.Confirm`
  gate (`--yes` for non-interactive) → create replacement (`--copy-body` or template)
  → `superseded-by` edge old→new → retire old by adding the `superseded` tag (same
  loc, never renumbered) + a body note → print a register-update reminder. `--dry-run`.

## Deviations from the original plan

1. **Lint recalibrated against the live corpus.** The original section-heading
   checks (require `Definition`/`Rule & examples`/`Durable vs tunable` headings)
   were dropped — the real corpus uses varied wording, so they were pure noise. The
   "what invalidates" check now matches a **heading or inline `**bold**`** form.
   The abstract + invalidation rules are **errors for top-level specs (rules) and
   advisory warnings for pull-on-demand flow nodes**. Result on the real corpus: a
   small, meaningful finding set instead of a wall of false positives.
2. **`new` does not consult the register's retired overlay** during allocation —
   it relies on monotonic-above-max + `--rule-after`. The retired overlay is used by
   `spec register`/`--check`. Keeps `new` to one read plus the writes.
3. **`spec supersede` drops `--new-feature`** (relocate via `--feature <existing>`
   only). To move a replacement under a brand-new feature, create it first with
   `spec new --new-feature`, then supersede with `--feature`.
4. **Abstract-staleness lint deferred** — `abstractOriginHash` is selected (so a
   future pass / `get` can use it), but reliable staleness needs a server-provided
   signal; not enforced in v1.

## Tests

- **Pure-logic unit tests** (`internal/cmd/spec/*_test.go`): citation parser;
  allocation (tens-features, +1 rules, `00` never auto, `--rule-after` floor,
  retired overlay, exhaustion); rubric template; `parseLedger`; the
  `lintNode`/`lintCorpus` rule engine over in-memory node structs.
- **Command/wiring tests** (`internal/cmd/spec_commands_test.go`, via
  `testFactory`+`captureGraphQL`): ls, get (asserts the `hrn:node:…::` URN), find
  (default `mode:hybrid`; `--match-exactly` tags), register `--check` drift→Conflict,
  new (asserts `UpsertNode` input + edges), new `--dry-run` (no mutations), new
  missing-parent→NotFound, lint error→Conflict, supersede requires `--yes`,
  supersede (`superseded-by` edge + `superseded` retire tag), import stub exit 2.

## Verification

- `go build ./...`, `go vet`, `gofmt`, `go test ./...`: green. `make lint`: 0 issues.
- **Read-only live smoke test** against `micromentor.org::platform-specs`: `ls`,
  `get`, `register --check` (✓ matches), `find` (semantic), `lint --all`, and
  `new --dry-run` (allocated `msg:010:04` with correct edges). No writes.
- Genuine finding surfaced by `lint --all`: **`msg:010:00` (the W-series contract)
  has no abstract** (error), and several `msg:010:03:*` flows lack abstracts
  (advisory) — real corpus gaps, not code issues.

## Out of scope (follow-ups)

- Spec Kit / source-code import implementations (stubs only now).
- Register auto-maintenance / structured-data ledger.
- Precise abstract-staleness lint (needs a server staleness signal).
- `// Spec: <loc>` code-pointer insertion and the cross-memory implementer
  back-reference edge in `new`/`supersede`.
- `supersede --new-feature`.
