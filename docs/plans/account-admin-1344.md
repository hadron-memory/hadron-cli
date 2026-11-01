# Account deletion, hidden API keys, impersonation audit reads (#1344)

Design as built. Gate: CHECKED T2 + §10 every head (issue comment 6540754687).
Server: every operation is on hadron-server main; no server change.

## Surface

| Command | Operation | Notes |
|---|---|---|
| `user delete [--user <ref>] [--reason <text>] --yes` | `deleteUser` | No `--user` deletes the caller's own account. Destructive: `ConfirmDeletion`, `--yes` non-interactively. |
| `auth token hide <id>` / `unhide <id>` | `setUserApiKeyHidden` | One mutation, both directions. |
| `auth token list --include-hidden` | `myUserApiKeys(includeHidden)` | Sent only when set; the server default is false. |
| `admin impersonation list [--org] [--active] [--limit] [--offset]` | `impersonationSessions` | Drains every page unless `--limit`/`--offset` is given. |
| `admin impersonation get <id>` | `impersonationSession` | A null is one NotFound; denied and missing are not told apart. |

`admin` is a new top-level group (the gate's proposed surface). It is audited
under child #1052 with `auth` and `user`.

## Decisions

- **No local authorization (cor:api:240:01).** `user delete` sends the ref and the
  reason and maps what the server answers. It does not check who the caller is,
  and it never words an error to say whether a named user exists; the server's
  one FORBIDDEN (exit 8) comes through unchanged. `SOLE_ORG_OWNER` keeps its
  server code and message (`MapErrorKeepingCode`).
- **Wire semantics.** `userRef`, `reason`, `includeHidden`, `orgId`, `filter`,
  `limit`, `offset` carry `omitempty`: an unset flag is absent from the request,
  never `null`. A deliberate `--user ""` or `--reason ""` is a usage error.
- **`deleted:false` is not success.** The server's boolean is checked.
- **Output.** `user delete --json` is `{"deleted":true,"user":<ref|null>}`.
  Every token JSON gains `hiddenAt` (null while visible), additive. Impersonation
  records are explicit DTOs with `endedAt`/`endedBy`/`reason` as explicit nulls and
  `[]` for an empty list. No key material appears in any new output.
- **After deleting your own account** the stored login is dead; the message says to
  run `auth logout`. The command does not touch the credential store.
- **Hiding.** Only a revoked key can be hidden; the server refuses an active one
  (BAD_USER_INPUT, KEY_ACTIVE, exit 2, nothing changes). Hiding is display state.
- **Matrix.** The five `gap #1344` rows are now `command` / `flag` lines;
  `unhide` shares `setUserApiKeyHidden` with `hide`.

## Tests

`internal/cmd/account_admin_1344_test.go`, fake GraphQL with request capture:
variable presence/absence, confirmation before any request, refusal codes,
`deleted:false`, hiddenAt in text and JSON, list paging, empty list, null session.
Red on main (every test fails: the commands do not exist), green here. Mutants
killed: ref dropped, false-as-success, confirmation removed, hide direction fixed,
includeHidden always sent, activeOnly always sent, NotFound code changed.

## Limits

Fake transport only: no live server, no real account deleted, no real
impersonation. Authorization outcomes are the server's and are not re-tested here.
