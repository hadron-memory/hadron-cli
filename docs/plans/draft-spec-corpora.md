# Design as built: draft spec corpora and minting (cli#777, slice 1)

> **Status:** cli#779 and cli#789 have merged. This plan records those slices
> and the cli#777 completion slice on top of current CLI main. Server #1459's
> draft-only spec bulk door has merged. The schema snapshot for this slice was
> exported from server `origin/main` at `c2a3ec13` through an explicit
> `HADRON_SERVER_DIR`; `make schema-check` passed against that revision.
>
> Contract: hadron-server#1447 ([contract comment](https://github.com/hadron-memory/hadron-server/issues/1447#issuecomment-5895948862),
> [mint rulings](https://github.com/hadron-memory/hadron-server/issues/1447#issuecomment-5898878597)).

## 1. Scope

In (slice 1):
- `memory set --draft-corpus` — create a memory as a DRAFT spec corpus.
- `spec describe` reports `corpusState` (and `mintedAt`).
- `spec reserve`, `spec renumber`, `spec backlinks`, `spec unresolved`,
  `spec mint`.
- The three new server refusals mapped to exits.

Slice 2 shipped through cli#789; see §6. The cli#777 completion slice adds
draft `spec replace` through merged server#1459 and placeholder inventory to
`spec describe`; see §7.

Originally planned for slice 2:
- **Placeholder awareness in `spec lint` / `spec list` / `spec get`.** Needs
  `Node.isPlaceholder` in the shared node projections, which widens the blast
  radius of an old-server mismatch to every `spec` read; kept out of slice 1 on
  purpose. Until then `spec mint --dry-run` lists every placeholder (kind
  PLACEHOLDER); `spec unresolved` lists only *references to* placeholders, so a
  placeholder nothing links to shows up in the mint report alone.
- **State-aware lint advice.** Three lint messages say "a citation is never
  renumbered"; in a draft it can be. They need lint to read the corpus state.
- **`spec replace` in a draft** — delivered by the completion slice (§7).
- **A Go port check against the server's `specMint.fixtures.json`** — the four
  mint-blocking lint rules exist in `spec lint` already; a parity test loading
  a vendored copy of the fixture is a follow-up once the fixture is on `main`.

## 2. Commands

| Command | Server op | Draft only | Notes |
|---|---|---|---|
| `memory set --draft-corpus` | `createMemory(draftCorpus: true)` | — | create-only, free-standing only |
| `spec describe` | `memory { corpusState corpusMintedAt }` | no | state omitted on an older server |
| `spec reserve <citation> [--name]` | `reserveSpecCitation` | yes | `--name` omitted when unset |
| `spec renumber <from> <to> [--dry-run]` | `renumberSpec` | yes | text citations reported, never rewritten |
| `spec backlinks <citation>` | `specBacklinks` | yes | exact citation only |
| `spec unresolved` | `specUnresolvedReferences` | yes | exit 0 either way |
| `spec mint [--dry-run] [--yes]` | `mintSpecCorpus` | yes | check first; confirm; one-way |
| `spec replace [--dry-run] [--yes]` | `searchReplaceInSpecNodes` | yes | governed specs, exact preview plan |

## 3. Decisions

- **`--draft-corpus` lives on `memory set`, not in the `spec` group.** Draft
  is a property of the memory, chosen at `createMemory`, the only mutation
  that takes it. Refused offline (exit 2, nothing written) on an update — the
  server can never switch a memory into draft — and with `--app/--agent`,
  since `createMemoryInApp` has no such argument. Gated on `Changed`, so an
  explicit `--draft-corpus=false` on an update is refused too, not read as
  "nothing to do". An ordinary create uses `CreateMemory`, whose document
  contains no `draftCorpus` argument and selects no `corpusState`, so a
  pre-#1447 server can still validate it. `CreateMemoryDraft` is a separate
  operation with `draftCorpus: true`. The draft path reads the state back
  with `SpecCorpusState` and echoes what the server stored; a follow-up slug
  or schema update preserves that state in the output.
- **`spec mint` always checks first.** It sends `dryRun: true`, prints the
  report, and only then — with no blockers — asks. A mint already known to be
  blocked is never offered (review:confirm-prompt-tells-the-truth), and the
  real `dryRun: false` call is never sent: the tests count the calls. The
  prompt says what is true of a mint: the citations become permanent and the
  corpus can never return to draft. Non-interactively `--yes` is required.
- **Blocked exits 5, whoever notices.** A blocked check exits 5 after writing
  the report (like `spec lint`); the server's own `SPEC_CORPUS_MINT_BLOCKED`
  (a blocker appearing between check and mint) maps to 5 as well, so the exit
  contract doesn't depend on who refused.
- **Exit codes.** `SPEC_CORPUS_NOT_DRAFT`, `SPEC_CORPUS_MINT_BLOCKED`,
  `SPEC_CORPUS_BUSY` and `SPEC_CORPUS_ENCRYPTED_UNSUPPORTED` (the scans don't
  read an encrypted memory yet) all map to 5 (Conflict): the corpus's state refuses.
  BUSY is retryable, but the server *refused* — it is not 7, which means the
  answer never arrived.
- **A FAILED renumber rewrite exits 1 after the full report.** The move is
  atomic and stands; each rewrite is a separate guarded edit. A failed one
  leaves a node naming the old citation — a partial write, which must not read
  as a clean success (the `node import --with-edges` precedent).
- **`spec unresolved` exits 0** with references listed: listing them is not a
  failure, and `spec mint --dry-run` is the gate.
- **`spec mint` refuses an explicitly blank `-m`.** An unset `"$M"` would
  otherwise fall back to the `spec use` / active memory; for an irreversible
  write that fallback is refused. With `--json` on a terminal, the report is
  written to stderr before the prompt, so stdout stays one document.
- **`spec describe` degrades on an older server.** A `GRAPHQL_VALIDATION_FAILED`
  naming `corpusState` reports `"corpusState": null` (the server can't say —
  distinct from an older CLI, which omits the key) instead of failing the
  inventory. The
  test fake answers `SpecCorpusState` that way by default (the
  `NodeLiveRevisions` precedent), so every existing describe test exercises the
  degrade path.

## 4. `--json` shapes (additive)

- `memory set` create: `corpusState` (omitempty; `--draft-corpus` creates only).
- `spec describe`: `corpusState` (always present; `null` = unknown), `mintedAt`
  (omitempty); `placeholderCount` and sorted `placeholders[]` when known.
- New DTOs, all slices initialized to `[]`: `reserveDTO`, `backlinksDTO`,
  `unresolvedDTO` (shared `referenceDTO`), `renumberDTO`, `mintDTO`. See
  `agentic-usage.md` for the field lists.

## 5. Evidence

- Command tests in `internal/cmd/spec_corpus_cmd_test.go`, against fake
  servers, plus exit-code table rows in `internal/api/errors_test.go`: operation
  choice for old-server compatibility, variables sent (`name`, `dryRun`), exit codes
  per path, `[]` not `null`, the mint call sequence (check-only when blocked,
  declined or non-interactive; check then mint with `--yes`).
- The merged slices received independent source review and live QA on their
  exact PR heads; those receipts are linked from cli#779 and cli#789.

## 6. Slice 2 — the read commands know about drafts

- **One state read, and draft-only scans.** `spec lint`, a scoped `spec list`
  and `spec get` read `corpusState` once (`loadDraftInfo`). Only for a DRAFT
  do they scan for placeholders — a separate minimal `SpecPlaceholderScan`
  (`loc`, `isPlaceholder`), NOT a field added to the shared node projections,
  which would make an older server reject every `spec` read — and, in lint,
  read `specUnresolvedReferences`. A minted corpus or an older server costs
  one read and is otherwise untouched; an unscoped `spec list` (many
  memories) reads nothing.
- **A placeholder is not a malformed spec.** Lint reports it once as
  `placeholder` (warning) and does not run the per-node rules an empty,
  possibly untagged body would trip; `spec get` gives it that single finding
  and `"placeholder": true`; `spec list` marks it the same way.
- **`unresolved-reference` warnings, at the citing spec, in scope only.** A
  warning, not an error: in a draft an open reference is work in progress,
  and `spec mint` is the gate. `--strict` escalates it like any warning.
- **State-aware advice.** The index and split remedies no longer say a split
  is impossible / supersede-level in a draft: `spec renumber` / `spec reserve`
  make it a plain edit. The contract remedy is unchanged (its reason — one
  reserved atom per tier — holds in any state).
- **Parity with the server's mint check.** `testdata/specMint.fixtures.json`
  is the server's shared fixture, verified verbatim against merged server
  `main` at `8a864c52`;
  `TestSpecLintSatisfiesTheServerMintFixture` runs `spec lint` over all 14
  cases. It found a real drift on its first run: `serialization-leak` read a
  CRLF-authored closing fence as not closing (the `\r` made the remainder
  non-blank), so a documented marker inside a CRLF fence was reported as an
  ERROR — pre-existing on `main`, affecting every corpus. Fixed in
  `withoutCode`: fence decisions use the line without its trailing `\r`, the
  kept text is unchanged.

## 7. Completion — draft replace and placeholder inventory

- `spec describe` reuses the state and placeholder scan that lint/list/get
  already use. It reports sorted `placeholders[]` and `placeholderCount`
  separately from the total `specs`. A minted corpus reports an empty list;
  when an older server lacks either the state or placeholder marker, those
  optional fields are absent rather than falsely reporting zero.
- `spec replace` reads the corpus state before selecting its explicit spec
  node IDs. A draft uses `searchReplaceInSpecNodes`, passing raw literal text
  and the server's `wordBoundary` flag. A real run previews, confirms, then
  supplies that exact opaque `plan` as `expectedPlan`; the server refuses a
  changed plan with zero writes. `--max-specs` remains a CLI preview gate and
  is sent as `maxNodesChanged` on both calls, because the limit contributes
  to the plan fingerprint. The draft result includes the
  server's searched/skipped counts and node-addressed skip reasons.
- Minted corpora and servers without `corpusState` retain the generic
  `searchReplaceInNodes` path and its governed-skip explanation. A draft
  server without the spec door fails loudly; silently using the generic door
  there would report governed specs as unsearched and would not fulfill the
  draft edit contract.
