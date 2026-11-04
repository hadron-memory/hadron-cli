# `memory get` shows the stored sync report (#1014)

Design as built. The gate is Xan's CHECKED stamp on hadron-cli#1014 (Patrick's PM attach answers the eight deltas). The provider is hadron-server#2366 (`src/integrations/github/syncReport.ts`), and the platform rule is `cor:int:010:01`.

## What changes

`hadron memory get` reads three more Memory fields, `lastSyncedAt`, `syncError` and `lastSyncReport`, and shows them. No other command changes. `memory list` is unchanged.

## Read path

- **A dedicated operation.** The fields ride on a new `GetMemoryDetail` query, used only by `memory get`. The shared `GetMemory` is how about a dozen commands resolve a memory ref (team init, skill, spec, asset, chat post, coding…). Adding a field there would make every one of them fail with a GraphQL validation error against a server that predates `lastSyncReport`. With the split, an older server fails only `memory get`. There is still no old-server compatibility promise, and the field is requested unconditionally, as the gate says.
- **The snapshot already had the fields.** `schema/schema.graphql` carried all three, and their types match server `origin/main` typeDefs (`String`, `DateTime`, `String`). `make generate` was the only codegen step.

## Parsing (`internal/cmd/memory/syncreport.go`)

`parseSyncReport` returns a report and a status:

| Stored value | `syncReportStatus` | `syncReport` |
|---|---|---|
| null | `none` | null |
| well-formed v1 | `recorded` | parsed object |
| anything else | `unavailable` | null |

"Anything else" covers:
- invalid JSON, or a payload that is not an object;
- a `version` other than 1, or a missing one;
- an outcome outside `partial|failed|unknown`;
- a `recordedAt` that is not RFC 3339;
- an `entries` that is omitted, null or not an array (an explicit `[]` is valid);
- an entry missing `kind`, `loc` or `code`, or an entry with a field of the wrong type.

The CLI never repairs a report.

An unknown entry `kind` or `code` does not make a report unavailable, because the server may add vocabulary within v1. `--json` passes it through as-is, and the text view labels it `other "<value>"`. Entries are never dropped.

## Text view

The `sync:` block is printed only when the memory has a `source`, a `lastSyncedAt`, a stored report or a `syncError`. A plain memory's output is unchanged.

| State | `sync:` line |
|---|---|
| partial | `last sync committed with N file(s) skipped or held`, where N is the true total (never called failed) |
| failed | `last sync failed (<code>)`, with `syncError` on its own `sync error:` line |
| unknown | `last sync completed but some skips could not be listed` |
| unavailable | `report unavailable` |
| null report, `ERROR` | `last sync failed, no per-file report recorded` |
| null report, `SYNCING` | `sync in progress, no per-file report recorded` |
| null report, never synced | `never synced` (no clean claim) |
| null report, `PENDING` after a sync | `sync pending, no per-file report recorded` |
| null report, `OK` | `last sync completed, no per-file issues recorded` |

The `ERROR`, `SYNCING` and `PENDING` rows cover rows written before the provider existed, and syncs still in flight. In those cases a null report must not borrow the clean wording.

## Terminal safety (§10 surface)

- **Quoting.** Every server-supplied free-form string (`loc`, `fromLoc`, unknown kind and code values, `syncError`) prints through `strconv.Quote`, the `email.go` pattern. Control and escape sequences therefore print as escapes. `recordedAt` prints bare only because parsing has already validated it as a timestamp.
- **Caps.**
  - A loc, or an unknown kind or code value, is capped at 200 runes, with a trailing `…`.
  - `syncError` is capped at 500 runes.
  - The text view prints at most 50 entries, then `… and N more`.
  - `--json` is uncapped: it is the machine contract.
- **`syncError` redaction.** The server already replaces git command failures with a fixed text (`GitCommandError`, `execGit.ts`), but other errors' messages are stored raw. As defense in depth, the CLI masks URL userinfo (`scheme://user:pass@` → `scheme://[redacted]@`, up to the last `@` before the host, so an unencoded `@` in a password does not leak its tail) in both the text and `--json` output. It cannot know the token itself, so this is the only credential shape it can recognise. Scheme-less scp-style remotes (`user:token@host:path`) are not masked.

## Out of scope

- Portal display (portal#1266).
- Server changes.
- The `cor:int:010:01` spec amendment.
- A separate `memory sync-report` command.
- `memory list`.

## Release

Release only after the provider is deployed to the server the CLI talks to. That is recorded by deploy at release time.
