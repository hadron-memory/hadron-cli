# `team host` and `app key scope` — host operations, reader and manager side

Issue [#871](https://github.com/hadron-memory/hadron-cli/issues/871), CLI
parity for [hadron-server#1876](https://github.com/hadron-memory/hadron-server/pull/1876)
(#1709 T6 slice 1: the host-operation queue and the `host:operate` App-key
scope). Schedule **T1 Standard** per Xylon's CHECKED stamp. As built by Jonas,
2026-10-14.

## What landed

```
hadron app key scope grant <key-id> <scope> [--host-id <host>]
hadron app key scope revoke <key-id> <scope>
hadron team host list
hadron team host ops [--host <id>] [--status …] [--worker <ref>] [--limit N] [--after <cursor>]
hadron team host issue <type> --host <id> --worker <ref> [--arg k=v]… [--as-person]
```

Thin callers over `grantAppKeyScope` / `revokeAppKeyScope`,
`hostRegistrations`, `hostOperations` and `issueHostOperation`, as typed
operations in `internal/api/queries/hosts.graphql`. The schema snapshot was
re-exported from a throwaway clone of hadron-server at `fa96070` (==
origin/main, which carries #1876 and #1884). The host's own surfaces (`registerHost`,
`collectHostOperations`, `reportHostOperation`, `setHostWorkerAvailability`)
stay in `unbound-ops.txt`, annotated as the poller's (#831).

## Who acts

- **`app key scope grant|revoke`** is the App manager's, as a person: the
  server refuses a worker session, an App key and an impersonation
  (`actor.kind !== 'user'` → FORBIDDEN). These mutations are not in the
  authored-write registry, so they never carry a bound worktree's session;
  a test pins that from a bound worktree.
- **`team host issue`** is the one write here where the server accepts
  *either* party under different rules (cor:agt:070:05): a worker actor
  qualifies only as the target's reports-to; a person qualifies as the
  target's holder or the App's manager. The CLI cannot tell which the caller
  means, so:
  - `issueHostOperation` joins the authored-write registry: in a bound
    worktree the operation is the worker's (issuedBy WORKER), consistent with
    the agent-task surface and honest provenance for a coordinator issuing
    for its report.
  - `--as-person` sends no session (issuedBy USER), for the holder or the
    manager. It is a command-local context marker (`withAsPerson`) the
    authored-write hook checks, not a transport default.
  This is the #868 lesson applied up front: a manager-only write must not
  carry the session (`stall-schedule set`, `availability set --family`), but
  this write is not manager-only, so neither fixed choice is right for
  everyone, and the flag makes the choice explicit.
- The reads are plain.

## Arguments, and the credential rule

`--arg key=value` is repeatable and builds the operation's argument object
(string values, keys sorted on the wire, a repeated key refused locally). The
type and argument validation is the server's (closed type set, per-type
shape); the CLI forwards and maps. The worker is named with `--worker`, never
inside `--arg` (the server refuses `workerId` in args as
BAD_HOST_OPERATION_ARGS). Per Holger's ruling (#10777), no argument, flag,
example or output carries a credential: operation arguments are ids and name
tokens, and the key-scope output selects the key's id, label and scopes only,
never `keyPreview`.

## Exit codes

| Code | Exit |
|---|---|
| HOST_OPERATION_NOT_PERMITTED | 8 |
| HOST_OPERATION_TYPE_UNKNOWN, HOST_OPERATION_NOT_ISSUABLE, BAD_HOST_OPERATION_ARGS | 2 |
| APP_KEY_SCOPE_HOST_CONFLICT | 5 (revoke host:operate first) |
| HOST_NOT_FOUND, WORKER_NOT_FOUND, APP_KEY_NOT_FOUND | 4 (the `_NOT_FOUND` suffix rule) |

## `--json` shapes

- `team host list`: an array of `{hostId, label, pollIntervalSeconds,
  registeredAt, refreshedAt, live, workers: [{workerId, workerName, family,
  worktrees}]}`.
- `team host ops`: `{items: [op], hasNextPage, endCursor}`. The page object
  is kept, unlike `team events`' bare array, because the server's cursor is
  the only continuation.
- `team host issue`: one op: `{id, appId, hostId, type, args, workerId,
  workerName, status, issuedBy, issuedByUserId, issuedByWorkerId, issuedAt,
  collectedAt, finishedAt, result, failureReason}`; `args` and `result` are
  the server's JSON, `null` when absent.
- `app key scope grant|revoke`: `{id, appId, label, revokedAt, scopes:
  [{scope, hostId, grantedAt, grantedBy}]}`.

## Follow-ups

- The read audience: #1884 (in this snapshot, server fa96070) narrows `hostOperations` for a `host:operate`
  key to its own host, and server#1881 tracks the participant audience
  (#10778). Neither changes the SDL this client uses; regenerate from main if
  either does.
