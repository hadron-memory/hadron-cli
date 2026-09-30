# Design as built: draft spec corpora and minting (cli#777, slice 1)

> **Status: reviewable on `Jane/777-draft-corpora`.** The hadron-server #1447
> stack — #1451 (draft state), #1452 (placeholders), #1453 (reference scan,
> renumber), #1462 (mint) — has merged. The schema snapshot is exported from
> merged server `main` at `8a864c52`, using an explicit `HADRON_SERVER_DIR`
> (#503). Live behavior still needs independent QA confirmation before this
> CLI slice is ready for a human merge decision.
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

Out, for slice 2 (tracked on cli#777):
- **Placeholder awareness in `spec lint` / `spec list` / `spec get`.** Needs
  `Node.isPlaceholder` in the shared node projections, which widens the blast
  radius of an old-server mismatch to every `spec` read; kept out of slice 1 on
  purpose. Until then `spec mint --dry-run` lists every placeholder (kind
  PLACEHOLDER); `spec unresolved` lists only *references to* placeholders, so a
  placeholder nothing links to shows up in the mint report alone.
- **State-aware lint advice.** Three lint messages say "a citation is never
  renumbered"; in a draft it can be. They need lint to read the corpus state.
- **`spec replace` in a draft** — needs hadron-server#1459 (Dara's governed
  bulk replace, scoped to draft), a separate stack.
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

## 3. Decisions

- **`--draft-corpus` lives on `memory set`, not in the `spec` group.** Draft
  is a property of the memory, chosen at `createMemory`, the only mutation
  that takes it. Refused offline (exit 2, nothing written) on an update — the
  server can never switch a memory into draft — and with `--app/--agent`,
  since `createMemoryInApp` has no such argument. Gated on `Changed`, so an
  explicit `--draft-corpus=false` on an update is refused too, not read as
  "nothing to do". When unset, `draftCorpus` is omitted (omitempty), so an
  older server never sees an argument it doesn't know — and `CreateMemory`
  does **not select** `corpusState`, or an older server would reject every
  create. The draft path reads the state back with a separate
  `SpecCorpusState` call and echoes what the server stored.
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
  (omitempty).
- New DTOs, all slices initialized to `[]`: `reserveDTO`, `backlinksDTO`,
  `unresolvedDTO` (shared `referenceDTO`), `renumberDTO`, `mintDTO`. See
  `agentic-usage.md` for the field lists.

## 5. Evidence

- 24 command tests in `internal/cmd/spec_corpus_cmd_test.go`, against fake
  servers, plus exit-code table rows in `internal/api/errors_test.go`: variables sent (omitted `name`/`draftCorpus`, `dryRun`), exit codes
  per path, `[]` not `null`, the mint call sequence (check-only when blocked,
  declined or non-interactive; check then mint with `--yes`).
- No live run: the fields don't exist in production until the stack merges.
