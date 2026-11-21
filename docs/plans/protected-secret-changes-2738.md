# Protected-secret changes (cli#2738) — design as built

Companion to hadron-server#3999 S4 (PR #4177, frozen head `71997f19b948d73f4c1f6a3d06ad5d8da4d6af9f`);
governing rule `cor:sec:070:02`. This PR merges after #4177.

## What the server does

On a **protected** App secret a change is never applied. `createSecret`, `updateSecret`,
`deleteSecret` and `setSecretProtected` refuse with `SECRET_PROTECTED_NEEDS_PASSKEY`, carrying
`extensions.requestId` and `extensions.ceremonyUrl` (a server page under `/secret-changes/`); a
person completes it there with a passkey. `secretChangeRequest(id)` is the requester's own read
(null for anyone else); `withdrawSecretChangeRequest(id)` answers `true` (withdrawn) or `false`
(not open). With signing off, `setSecretProtected`, `secretChangeRequest` and
`withdrawSecretChangeRequest` are absent from the schema and a refusal has no request id.

## What the CLI does

- **Staged refusal** (`internal/cmd/secret/staged.go`): `mapChange` wraps the error of the five doors.
  A refusal with a request id becomes `stagedChange`, an exit-5 error that prints the id and the URL
  exactly as sent (text on stderr; a URL with control or format characters is withheld from text,
  as `secret client issue` does) and carries both to the `--json` error envelope (`requestId`,
  `ceremonyUrl`, `serverCode`) through `StagedSecretChange()`, read in `renderError`.
- **No request id** is not "a request is open" (spec: the code alone never means that): exit 2,
  "this server does not offer protected-secret changes", nothing staged.
- **`secret protect <ref> --on|--off`** sends `setSecretProtected` once (`false` is sent for `--off`).
- **`secret change get <id> [--wait]`**: one read; `--wait` polls every 2s up to `--wait-timeout`
  (10m): completed 0; declined/expired/withdrawn/stale 5; timeout 13 (nothing withdrawn);
  Ctrl-C 6; an unknown state 1. Same shape as `secret client issue --wait`.
- **`secret change withdraw <id>`**: one send, never retried; an explicit `false` is 5; the result is
  read as a pointer so a missing or null one is "not confirmed" (7), not `false`; a lost answer after
  the write says the request may have been withdrawn and to run `change get`.
- **`secret protect`** treats a returned secret as unconfirmed (7): `setSecretProtected` only ever
  stages (`deliverStaged` in the resolver), so a direct success is not a state this CLI reports.
- **No usable URL**: a staged request with a missing, null or blank URL keeps its id (exit 5), says no
  person can sign it, and names `change withdraw` (it still counts against the open-request cap).
- **Never completes the ceremony**: no code path requests the ceremony URL; a test proves it.
- No value, and no staged value, reaches output or an error: the CLI selects neither, and does not
  echo the server's refusal prose, on the fallback paths too (LOCAL review): the limit refusal and a
  protected-change code inside a mixed, nested or 5xx answer print a fixed sentence (`withholdServerText`,
  `staged.go`) while the exit class, the server code and an internal fault's request id are kept.

## Choices that are the CLI owner's (not law)

Exit 5 for a staged change, 2 without signing, 13 on timeout (confirmed by the gate); `requestId`
in the envelope is the *change request's* id (never co-occurs with an internal-fault request id).
`SECRET_CHANGE_REQUEST_LIMIT` exits 5. The per-requester cap's number and code are not ruled and
are not assumed (hadron-server#4625). `SECRET_PROTECTED_TWO_PERSON_REQUIRED` (read-client issue)
is not mapped here.

## Schema snapshot

`schema/schema.graphql` carries only #4177's SDL delta: export at the PR's merge base
(`244c2660`) and at its head (`71997f19`), apply the difference to the current snapshot (a full
refresh from the PR branch would drop newer server main fields the CLI uses, e.g. `Question.context`).
The delta is the new `SecretChangeRequest` type, `secretChangeRequest`, `setSecretProtected`,
`withdrawSecretChangeRequest`, the `protected` argument on `createSecret`/`updateSecret`, two
description updates, and `issuedToOwnWorker` on the read-client types.

## Not in this PR

`secret create|update --protected` (cli#2815): the two `protected` arguments are matrix gaps.
