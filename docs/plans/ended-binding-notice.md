# The ended-binding notice (hadron-cli#914)

**Status:** design as built. **Ruling:** Holger R1/R2 (`decisions:2026-10-17-holger-rulings-to-vera`). **Server:**
hadron-server#2036 (4d795ffc) adds `Session.endReason` (cor:dmo:070:05). **Gate:** Xavier, CHECKED T2 on #914.

## Behaviour

A call through a worktree binding whose session has **ended** warns once on stderr. The warning names the
session and worker, the end time, the cause as the server recorded it (`ended`, `superseded`, `expired` or
`unknown`), and the supported rebind: `hadron team session start --as <worker id>`. A plain start replaces an
ended binding, so no `--force` is needed. Everything else about the call is unchanged: stdout, `--json`, the
exit status, the binding file. Nothing ends, rebinds or releases.

An open session, whether live or lapsed, gets no notice. A check that cannot complete says `note: could not
check …` and never claims an end. That covers a transport error, a session you can't read, a binding for
another server, and a legacy binding that recorded no server.

## Where it runs

**Once per command,** at the first point the command consumes the binding:

- **`readBinding`.** It is the funnel for every team command that reads the binding, so the notice lands
  before the call's own work and before any refusal.
- **`AuthoredWriteContext`.** It now reads the file raw for its per-request look, and checks only when it
  actually attaches the session to a write. A plain read like `node get` in a bound worktree sends no session,
  so it pays no extra request. The notice is a round trip, so the write re-reads the binding raw after it and
  attaches the session only if the binding is unchanged; a rebind during the notice attaches nothing (Xavier's
  local P2 on #915).

The check state is installed per root command, like `AuthoredWriteContext`, so each command checks at most
once.

**Exempt:** `session start` (it judges an existing binding itself and *is* the rebind), `session end` (it
addresses the bound session and the server answers for an ended one) and `whoami --check` (it reports
openness itself).

## The cause

The cause is read through a separate operation, `GetBoundSessionEnd`. An older server rejects the unknown
`endReason` field at validation, and the notice then falls back to `GetTeamSession`'s `endedAt`, with the
cause `unknown`. It is never inferred from `autoExpiredAt`, the summary or binder metadata, and no actor is
shown.

## Test harness

The shared fakes (`unstubbedDefault`, plus three strict file-local fakes) answer `GetBoundSessionEnd` as an
open session. The request-order recorders don't record it. The notice's own behaviour is pinned in
`team_ended_binding_test.go`.
