# hadron-cli#1846: the person-manager grant verbs (design as built)

Companion to hadron-server#3711 / #3876 (cor:acl:140:05). Binding brief: the
issue body (Petra's restatement, sections (a)-(e), acceptance 1-19) and the
CHECKED comment (T1).

## What was built

`hadron team grant` gains four verbs, all called as the signed-in person (never
a worker session):

| Verb | Operation | Notes |
| --- | --- | --- |
| `create` | `TeamCreateGrant` (existing) with `input.appRef` | `--app` and `--holder-id` required; unset optionals omitted; prints the grant, next step on stderr; never signs |
| `app-list <app>` | `TeamAppGrants` -> `appGrants(appRef, limit, offset)` | `--limit 0` counts only; an empty page says the caller may not be authorised, never "no grants" |
| `revoke <grant-id>` | `TeamRevokeGrant`, or `TeamRevokeGrantInstructed` with `--instruction` | the shared `--instruction` helper; the instructed document is a separate operation, so an unset flag never sends an explicit null |
| `withdraw <grant-id>` | `TeamWithdrawGrant` | withdraws the GRANT, not a signing request |

One fragment, `GrantManagerFields` (`team_grants_manage.graphql`), is the
selection of all four (22 fields, incl. `revokedByUserId`, `holderDepartedAt`,
`allows`, `state`); `TeamCreateGrant` selects it too, so `team grant request`
reads the grant the way the manager does. `team grant list` and `get` gain
`state` (15 fields) so the worker's reads and the manager's agree.

## Decisions

- `state` is printed exactly as the server sent it, beside `allows`; neither is
  derived or corrected (cor:acl:140:03, hadron-server#4173: seven states). An
  ACTIVE row can show `allows` false. `HOLDER_GONE` and `holderDepartedAt` are
  printed as returned, with no reason words.
- No local authorization gate in any verb (acceptance 19): the server answers
  (`GRANT_NOT_MANAGER` and `GRANT_NOT_REQUESTER` exit 8; `GRANT_NOT_REQUESTER`
  is newly mapped).
- An already-ended grant comes back unchanged from the server and is printed
  as returned; no line claims a new revocation or withdrawal.
- No confirmation prompt on `revoke` or `withdraw`: the acceptance sends the
  call without `--yes`, the server judges every caller, and an ended grant keeps
  its first end. Recorded as an open point in the PR.
- The `--instruction` App for the at-send-time check is `--app`, else the
  worktree binding's App (a revoke names a grant, not an App).

## Compatibility

`state` is a server field from 2026-11-17 (hadron-server#4207); `list`, `get`
and `request` now select it, so an older server refuses those reads. `request`
(through the shared `TeamCreateGrant` selection) also reads the manager's fields,
which shipped earlier (hadron-server#3876, 2026-11-10); `list` and `get` select a
subset of what `request` does. So 2026-11-17 is the floor for all three, and the
help says so. `secret client revoke` already selected `state` on the same field.

## Tests

`internal/cmd/team_grant_manage_1846_test.go` (red before the verbs existed):
one pin per verb and refusal class, the exact field selection of the four
operations, and the stale-help sentence. Existing 1832 and 1868 pins changed
for `state` (15 fields; "no derived state word" now pins the server's word).
