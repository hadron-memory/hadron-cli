# Team session restart (cli#3050)

`team session restart <worker> --reason <text>` — the dark-session restart
door, `restartWorkerSession` parity (hadron-server#4930), gated as T1 Standard
under cor:agt:070:08 S4a and cor:acl:140:02. As-built record; the issue named
the surface, the SDL the contract.

## What the door is

One transaction, no person in the loop: end the target worker's DARK open
session (endReason `ended`, NO handoff) and start the worker again. The start
carries the worker's oldest open task (taskless when none) and names the
calling worker as issuer; each call records one USE of the repeating grant.
The division of judgement is the whole design:

- **Dark is the caller's judgement.** `--reason` (required) states what was
  tried and why the session is dark; the server records it and judges nothing.
  The CLI adds no length validation of its own — ≤500 characters is the
  server's rule and arrives as its refusal.
- **The grant is the server's admission.** No client-side pre-check and no
  client-side grant read: `RESTART_GRANT_REQUIRED` (exit 8) is the server's
  answer, returned before anything about the target is read, and neither that
  answer nor this command says anything about the target (the grant's
  non-disclosure shape, same as the other grant doors).
- **The caller is this worktree's bound session.** The mutation rides
  `api.WithSession(ctx, b.SessionID)` — `X-Hadron-Session` — and there is no
  `--session` escape and no unbound spelling: a nil binding refuses exit 4
  pointing at `team session start`. `session end --session <id>` remains the
  recovery path for a binding that is gone; that escape is for ending YOUR
  session, not for acting on someone else's.
- **No destructive confirm.** S4a explicitly keeps the person out of the loop —
  the signed grant is the authorisation, `cmdutil.ConfirmDeletion` would
  reintroduce the human the door removes. (Same family as `session end`.)

Naming: help and agentic-usage say "the worker a team dedicates to manage its
agents" / "a worker holding the grant", never a team role name (Holger,
2026-11-22).

## As built

- `internal/api/queries/session_restart_3050.graphql` — `TeamSessionRestart`,
  all three input fields required, so no `omitempty` annotations (nothing is
  optional: an omitted field cannot be read as "preserve" here).
- `internal/cmd/team/session_restart_3050.go` — explicit `sessionRestartDTO`
  (never the generated struct), `endedSessionIds` initialised `[]string{}`
  then appended so empty renders `[]`, `taskId *string` so no-open-task
  renders `null` rather than a made-up `""`. Receipt lines are facts only
  (restarted/host/task/ended ids); notes never touch stdout. The human render
  first names the resolved App and which branch answered
  (`lazyAppLabel` — `--app` / App context / binding), the
  ambient-scope rule every scoped team write follows; the decorating read is
  lazy so `--json` never issues it. The worker ref in the receipt is the
  caller's own echo and renders raw (SafeCell is for server text); every id
  the server returned keeps its `output.SafeCell`.
- A null/absent result after a sent request exits 1 saying the outcome is
  unknown and names the read (`session list --as <worker>`) before any retry —
  the #2493 shape; never a success receipt, never a blind retry.
- `appRef` via `resolveTeamAppScope` (`--app` / App context / the binding's
  recorded App id), the worker ref passed exactly as typed (id, URN or name;
  the server resolves it, the CLI does not).

## Exit-code mapping (`internal/api/errors.go`)

- `RESTART_GRANT_REQUIRED` → Forbidden (8), a literal row that must precede
  the `_REQUIRED` suffix rule or the code would be miscalled a usage error
  (exit 2). Remedy is a signed grant, not a different flag — authority, not
  argument.
- `NO_OPEN_SESSION`, `NO_LIVE_HOST`, `TEAM_PAUSED`, `WORKER_STOPPED` →
  Conflict (5), joining their `WORKER_LIVE` / `WORKER_NOT_HELD` /
  `WORKER_START_PENDING` / `WORKER_RETIRED` neighbours: state refusals, the
  caller's next move changes.
- `WORKER_NOT_FOUND`, `APP_NOT_FOUND` ride the existing `_NOT_FOUND` rule (4).

## Schema provenance

No snapshot refresh: `restartWorkerSession` (mutation, input, result) was
already committed in `schema/schema.graphql`; the server contract was verified
against hadron-server `origin/main`. The `make generate` diff is purely the
new operation and its types.

## Tests (red-first)

`internal/cmd/team_session_restart_3050_test.go` on the shared harness
(`runTeamTaskFactory`/`attnServer`, `exitOf`, `gqlErrorJSON`): the bound
session id on the wire (`X-Hadron-Session`), the three required input
variables as sent (a nil catches both omission and explicit null), the receipt
text, `--json` keyset with `"endedSessionIds": []` and `"taskId": null`, the
taskless receipt line, all eleven refusal codes → their exits, missing
`--reason` and (#3058) blank/whitespace `--reason` or `<worker>` (exit 2, nothing sent, trimmed reason on the wire), an empty server `taskId` rendering `null`, the null-result "outcome unknown" branch (exit 1, one request, no receipt), the degraded scope read (receipt still prints), the cross-server binding refusal (exit 2, nothing sent), no binding (exit 4, nothing sent),
`--app` winning over the binding (canonicalized URN, as every team command
sends it), and the no-team-role-name help rule. `TestMapError` rows pin the
code→exit mapping at the `api` layer.
