# Implementation Plan: `hadron spec grep` + `hadron spec replace`

> **Status: implemented and verified** on this branch; reflects the design as
> built. Resolves parts **1 & 2** of
> [#240](https://github.com/hadron-memory/hadron-cli/issues/240) (spec
> corpus-maintenance friction). Part **3** (a tool-name lint anchored to the real
> MCP registry) is a fast follow-up — see the note at the end.

## Context

A corpus-wide rename of stale MCP-tool shorthand across the platform specs
(`h-read-node` → `hadron_get_node`, `h-chat-*` → `hadron_chatbot_*`, … — 125
replacements across 23 specs) had to be hand-scripted because `hadron spec`
couldn't (a) search spec **bodies** across the corpus, or (b) bulk find/replace.
Discovery — a per-spec `spec get` loop over ~230 specs — timed out at 2 minutes.

Both gaps map onto server primitives the CLI already reaches:

- **grep** — spec bodies are readable in bulk via `nodeBatch` (200 nodes / 1 MB
  per call), the same path `spec get --prefix` already uses.
- **replace** — `searchReplaceInNodes` (the server mutation behind `hadron
  replace text`) already does regex + `--dry-run` over `content`/`abstract`,
  scoped by memory + loc prefix.

So this is CLI wiring, not new server work.

## `hadron spec grep <pattern> [-m <memory>]`

Body-level, line-oriented, **exhaustive** search across the corpus.

- Lists every citation node in scope (`scanAllNodes`, paged to exhaustion — #23),
  then **bulk-reads** their `content`+`abstract` via `CollectNodeBatch` (one/two
  calls for the whole corpus, replacing the per-spec loop that timed out).
- Matches **client-side**, printing each hit as `citation:line: text` (abstract
  hits tagged `citation:abstract:line:`). RE2: literal by default, `--regex`,
  `-i`, `--field content|abstract` (default both).
- Deliberately **broad — no word boundary**: grep is for discovering everywhere a
  token appears (including inside longer tokens) before rewriting precisely.
- A node that lists but is unreadable by `nodeBatch` is surfaced as a note (the
  whole-corpus-read visibility rule), never silently dropped.
- `--json` emits `[{citation, field, line, text}]`.

Why bulk-read-then-grep rather than `findNodes(mode:regex)`: regex mode *does*
match bodies (`fields` is a weight-mask, `cor:api:090:03`), but returns
ranked/limited nodes **without match locations**, so it can't produce
`citation:line` output exhaustively. Reading the matched text and scanning it
locally is simpler and truly complete.

## `hadron spec replace <pattern> <replacement> [-m <memory>]`

The citation-aware, spec-scoped analogue of `hadron replace text`.

- **Word-boundary-aware by default**: a literal pattern is quoted and wrapped in
  `\b…\b`, sent as a regex, so renaming `h-read-node` never touches
  `h-read-nodes` or `h-read-next-node` (the exact cross-contamination that forced
  hand-written longest-match logic). `--word-boundary=false` for a raw substring,
  `--regex` for a full pattern with `$1`/`$&` backrefs (boundaries yours to set),
  `-i` folds case.
- Rewrites **content + abstract** by default (`--field` narrows).
- Gated like every bulk write: `--dry-run` previews affected specs + per-citation
  counts; a real run previews first, then prompts (or `--yes`, required
  non-interactively); `--max-specs N` caps blast radius; every change is versioned
  (undoable).
- **Re-lints the changed specs afterward** and folds findings into the report — a
  bulk body rewrite can leave an abstract out of sync with its content
  (abstract-stale), and the re-lint surfaces exactly that. Best-effort: a lint
  read error is a note, never an undo.
- `--json` emits the citation-keyed result + a `lint` array.

Both reuse the server's `searchReplaceInNodes` (regex built by JS `RegExp`, so
`\b` works) — the spec memory URN goes straight into `memoryIds` (URN-or-ID
accepted), no separate ID lookup.

## Tests

- Unit (`grep_test.go`): `parseGrepFields`, `compileMatcher` (literal-quoting,
  regex, `-i`, bad-regex→usage), `grepField` (line numbers, CRLF trim),
  `buildReplacePattern` (word-boundary wrapping + escaping, regex passthrough,
  bad-regex), `parseReplaceFields`.
- Command-level (`spec_commands_test.go`): grep over `FindNodes`+`nodeBatch`
  (content and abstract hits), grep bad-args; replace `--dry-run` (asserts the
  `\bh-read-node\b` regex + content/abstract fields on the wire), a real
  `--yes` run that re-lints, and the non-interactive refusal (no write).

## Follow-up — part 3 (tool-name lint)

A `spec lint` rule that flags any `hadron_*` token in a spec that isn't a real
registered MCP tool. Deferred to its own PR because the registry lives in
hadron-server (`server.tool('hadron_*')` in `src/mcp/server.ts`); the agreed
approach is a **checked-in generated manifest** (e.g. `schema/mcp-tools.txt`)
produced by a `make tools-manifest` target and guarded by a CI drift-check,
mirroring the schema-snapshot pattern.
