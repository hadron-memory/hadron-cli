# Team grant request verbs (cli#1868), as built

Companion to hadron-server#3823 / PR #3892; refs hadron-server#3690. Leaves
hadron-cli#1846 (manager verbs), #1832 (own reads) and #1872 (compile/approve
under a batch grant) open. The issue body holds the rulings R1-R8; this is what
shipped.

## Surface

Under `hadron team grant` (next to the read-only `list`/`get`):

| Verb | Operation |
|---|---|
| `request --action --values --reason [--holder worker\|agent] [--holder-id] [--starts-at] [--expires-at] [--max-uses]` | `createGrant` |
| `request-signature <grant-id>` | `requestGrantSignature` |
| `signing-request get <request-id>` | `grantSigningRequest` |
| `signing-request withdraw <request-id>` | `withdrawGrantSigningRequest` |

`request` prints the same fourteen fields as `team grant get` (R8). The other
three print one DTO, `grantSigningRequestDTO`, with `ceremonyUrl` and the
request's own `state` as the server sent them (R5, R6).

## Decisions as built

- **No local worker gate (R1 (a), cor:api:240:01).** `grantWriteBinding` returns
  nil for an unbound worktree; `workerGrantWriteClient` then sends the one
  operation without a session and writes one stderr note. The server answers
  (`GRANT_REQUESTER_NOT_WORKER` -> 8). With a binding the cross-server and
  other-`--app` refusals stay (exit 2, nothing sent): they keep a session id
  from reaching a server or App it does not belong to. `workerGrantClient` and
  `list`/`get` are untouched.
- **Holder (R3).** Id from the binding (`WorkerID`, or `AgentID` for
  `--holder agent`); `--holder-id` overrides; with neither, exit 2 before any
  call (an input refusal, the server judges the holder).
- **Verbatim URL (R5).** Never through `SafeCell`. `unsafeForTerminal` refuses
  the text render (exit 1, nothing on stdout, names `--json`) for Cc (C0, C1,
  DEL), Cf, Zl and Zp; JSON is always byte for byte.
- **Optional input fields are omitted when unset** (`omitempty` on
  `CreateGrantInput.startsAt/expiresAt/maxUses`); an empty optional flag is
  refused, not read as unset.
- **Signing off.** `mapGrantSigningError`: a validation-only envelope naming
  only the signing names -> exit 2 "does not offer grant requests and grant
  signing" (no upgrade advice, `serverCode` kept); a mixed envelope is not
  swallowed.
- **Exit codes (R7)** in `codeForExtension`: `GRANT_REQUESTER_NOT_WORKER` and
  `GRANT_HOLDER_NOT_ALLOWED` 8; `GRANT_ACTION_UNKNOWN`, `GRANT_VALUES_INVALID`,
  `GRANT_TERMS_INVALID` 2; `GRANT_NOT_PENDING`, `GRANT_EXPIRED`,
  `GRANT_TERMS_UNREPRESENTABLE` 5. `*_NOT_FOUND` already exit 4 by suffix.
- **Withdraw is not confirmed** (it ends a request, mirroring
  `node signing-request withdraw`); the help says it withdraws the request, not
  the grant.
- **`--max-uses`** is a plain int flag; a non-integer is refused by flag parsing
  (exit 2) and the value is otherwise the server's to judge.

## Schema snapshot

The snapshot on main already carries the grant fields and their current
descriptions (refreshed since this work began), so after merging main this change
adds no schema diff: only the operations in `team_grants.graphql` and the
regenerated `generated.go`.

## Verification

- `internal/cmd/team_grant_request_1868_test.go`, red on main (commit
  `b2e4fcb8`) for the missing verbs and the GRANT_* exit-1 mapping; plus
  `TestMapError` rows.
- Mutants, each turning a pin red: omitempty dropped, expires-at not copied,
  holder kind hardcoded, URL through `SafeCell`, each of Cc/Cf/Zl/Zp removed from
  the refusal predicate, mapper -> `MapError`, only-for -> any-match, unbound
  refused locally, server guard dropped, app guard dropped, empty-flag guard
  dropped, `--values` parse error ignored, a derived status line, a GRANT_*
  mapping removed, stderr note dropped, session not attached.
- Not run here: the real binary on a signing-on and a signing-off stack (QA's
  pass, per the stamped schedule).

## Spec check

Reasoned NO. The verb names, flags and exit codes are client-only; the rules
that cross repos (pending until signed, verbatim `ceremonyUrl`, no grant state
label, worker-only create) are the server's, already in cor:acl:140:01/:03/:05/:07.
