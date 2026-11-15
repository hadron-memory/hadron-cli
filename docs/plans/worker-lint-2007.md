# Worker and served-record lint (cli#2007) — as built

Shows the three lint pages the server already serves (cor:agt:040:13, :04) where the spec says they are shown. Client-only: no server, schema-snapshot, portal or MCP change, no new root API. Contract: the signed fold on the issue (Patrick, issuecomment-6867769007), gate CHECKED by Xylon (issuecomment-6867823452). Where it differs from the issue body's draft plan (R1–R6, `first: 100`, `hasLiveSession`, first-page-only history), the contract wins.

## Surfaces

| Surface | Lint | Mode | Operation(s) |
|---|---|---|---|
| `team worker get` | `signatureLint`, `promptLintWarnings` | always on | `GetWorkerSignatureLint`, `GetWorkerPromptLint` |
| `team worker update --signature/--trailer` receipt | `signatureLint` | always on, read after the write | `GetWorkerSignatureLint` |
| `team worker served-prompt` | `currentLint` | always on | `WorkerServedPromptLint` |
| `run served-prompt` | `currentLint` | `--lint-warnings` | `RunServedPromptLint` |
| `team session served-prompts` | `currentLint` per record | `--lint-warnings` | `SessionServedPromptsLint`, `…LintAt` |
| `chat served-prompts` | `currentLint` per record | `--lint-warnings` | `ChatServedPromptsLint`, `…LintAt` |

Every op lives in `internal/api/queries/worker_lint.graphql`, is its own operation (an older server rejects only the lint), and sends `first: 20` explicitly (cor:api:120:04), following `nextCursor` to the end. The node `--lint-warnings` op keeps its `first: 100`; it is not precedent.

## Shape

- `internal/cmd/lintwarn` holds the shared core: `Page`/`Warning` DTOs, the fixed stderr class, `Drain` (fail-closed cursor loop: null page, nil items/element, negative or drifting total, empty or repeated cursor, short final page, spent budget all give `nil`), the per-command `Budget` (5 s, 1000 requests, atomic, started after the main operation), the per-surface readers and the text renderer. `node` keeps its flag, help and renderer and now calls `Drain`; its tests ran unmodified (byte-identical output).
- **States** (`worker get`): *masked* (the row's `signature` is null) sends no lint read, prints nothing, JSON `null`; *nothing served* (`servedPrompt == null` in the same op) prints one neutral line, JSON `null`; *unavailable* is JSON `null` plus one fixed stderr line; *holds none* is `items: []`, `total: 0`. `hasLiveSession` is never consulted: an idle but open session has a record and shows its warnings.
- **Identity**: the signature op's worker id must be the worker shown; prompt lint pins `servedPrompt.id` and `provenanceRevSeq` across pages; worker/run lint pins the shown record id; history lint uses the inline first page only when item *i* has the shown id and the history total is unchanged, and drains a record with more pages one at a time by offset (`limit: 1`) requiring the same id and totals. A record is whole or `null`, never partial, never another record's.
- **Principals**: `worker get` lint ops carry no session header; the `worker update` lint read carries the write's own context, including the session, on the plain and instructed paths.
- **Output**: `workerGetDTO` gains two always-present keys; `worker update` uses an update-only `workerUpdateReceiptDTO` so cast, list and roster JSON do not move; `recordDTO.CurrentLint` is `**Page, omitempty` (absent unless requested) and ignored by `identity()`.
- **Text**: `<KIND> <locator>: <server sentence>`; locator is `workerField` for `WORKER_LINK_MISSING` (both fields carry position 0), `occurrence #n` for compiled lint (an ordinal, not an offset); the subject names the provenance revision; no "up to date" or "no warnings" on null.

## Verified

`internal/cmd/team_worker_lint_2007_test.go`, `served_prompt_lint_2007_test.go`, `lintwarn/lintwarn_test.go`, `servedprompt/identity_test.go`: 46+ controls, red on the pre-feature code, one mutant per guard killed (nil slice, null-as-none, stop after page 1, `first` above 20, each id/total check, `hasLiveSession` gate, session header on get lint, sequential reads, update lint failing the exit, update lint without the write's session, masked still reads, worker served-prompt opt-in only, `currentLint` in identity). `make lint`, `make test`, `make coverage-matrix-check`, `make help-inventory-check`, codegen fresh.

## Not covered by the author's suite

- The request budget's *start point* (after the main operation) is by construction, not by a timing test; the deadline and the 1000-request cap are unit-tested.
- No real stack was run by the author: the QA seat owns the private-stack legs (masked caller, manager, idle-open worker, no-record worker, session-principal separation).
