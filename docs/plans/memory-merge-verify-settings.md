# Memory merge, verify and settings (cli#1340): design as built

Child of #1334 (CLI coverage sweep), epic #67. Gate: CHECKED T1 (Xabat,
issuecomment-6540742081). Specs: cor:api:240:02 (the CLI covers the platform),
cor:api:240:01 (business rules live in the server).

## What was missing

`mergeMemories` and `verifyMemoryNodes` had no command. `memory create|set`
dropped most of what `createMemory` / `createMemoryInApp` / `updateMemory` take.
`memory list` passed neither `orgId` nor `filter.visibility`, `scope list` never
passed `filter.appRef`, and `memory encrypt` could not send a passphrase. The
coverage matrix recorded 39 `gap #1340` rows.

## Surface

| Server field | CLI |
|---|---|
| `mergeMemories` | `memory merge <source> --into <target> [--field F]... [--delete-source] --yes` |
| `verifyMemoryNodes` | `memory verify <memory>` |
| create/update text settings | `memory set --category0/1/2 --license --icon-url --hero-url --home-url --git-source --read-branch --write-branch` |
| `data` | `memory set --data <json> \| --data-file <path>` |
| `embeddingSource`, `vectorIndexEnabled` | `--embedding-source`, `--vector-index` / `--no-vector-index` |
| `chunkTokens`, `chunkOverlap`, `forceFixedSize`, `acknowledgeVectorInversionRisk` | change-only flags of the same names (`--force-fixed-size` / `--no-force-fixed-size`) |
| `memories(orgId)`, `filter.visibility` | `memory list --org`, `--visibility` |
| `scopes(filter.appRef)` | `scope list --app-context` |
| `encryptMemory(passphrase)` | `memory encrypt --passphrase-file <file\|->` |
| `updateMemory(organizationId)` | **excluded, not-a-capability**: the resolver accepts only the current organization and refuses any other with `USE_TRANSFER_MEMORY_OWNERSHIP`; `memory transfer` is the move. Awaits the specs engineer's acceptance (acceptance 2). |

## Wire semantics

- `updateMemory` reads an omitted argument as "keep" and an explicit `null` as
  "clear" for the text settings and `data` (verified in
  `resolvers.mutation.memory.ts` on hadron-server main). The text settings are
  bound to `json.RawMessage` with `omitempty`: nil is omitted, `null` clears, a
  JSON string sets. A flag not passed never reaches the wire; `--license ""`
  sends `null`. Every gate is on `Changed`, so an empty value is a value
  (review node `an-empty-flag-is-not-an-absent-flag`).
- On create there is nothing to clear: an empty setting is exit 2 before any
  request. The change-only dials are exit 2 on create. With `--app --agent`
  (createMemoryInApp) only `--data`, `--embedding-source` and the vector-index
  pair apply; `--app` alone (createAppMemory) takes none. Each refusal names the
  flag, rather than dropping it.
- Boolean pairs send `true` / `false` / omitted; both at once is exit 2.
- `mergeMemories` always sends `deleteSourceMem` explicitly; `include` is
  omitted (every field) without `--field`. The `--field` parser moved to
  `cmdutil.ParseMergeFields`, shared with `node merge`.
- `encryptMemory` sends exactly one of `dataKey` / `passphrase`. The passphrase
  is read after the confirmation (stdin is shared with the prompt), only the
  line ending is dropped, and it is never printed.

## Output and exit codes

- `memory merge --json`: the target memory (`memoryDTO`). Refusals, including
  `MERGE_TARGET_LOC_TOMBSTONED` and a partial merge, map through
  `api.MapError`; re-running the same merge finishes it.
- `memory verify --json`: `{memory,checked,intact,notApproved,superseded,tampered,problems[]}`,
  `problems` always an array. Exit 5 when `tampered > 0`, after printing.
  Unlike `node verify`, NOT_APPROVED and SUPERSEDED alone exit 0: most nodes of
  a memory are never approved, so "any non-INTACT" would make the tamper check
  fail on almost every memory.

## Authorization

None in the CLI. Who may merge, encrypt, list an organization's view or set a
setting is the server's rule (cor:api:240:01).

## Tests

`internal/cmd/memory_settings_1340_test.go`, committed red first: each command
or flag fails on main because the operation or variable is not sent.
