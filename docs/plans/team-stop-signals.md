# Team stop signals (CLI #911 slice B1)

Dispatched by Ada (task 01a16960, #32626). Gate: Xan's re-gate CHECKED
T2 + §10 + UX (issue comment 6358452428), scope per Joshua's re-attach
6358431984, wire per Joost's exact-wire delta 6305150650 with Victor's FIT
6305264102 (and its correction 6305431396). Refs #911: the umbrella stays open.
Slice B2 (worker isolation) is a separate PR after its own fit.

## Wire

Bound to the stop SDL in the committed snapshot, which is identical to server
main a2463194 (#2166, `typeDefs.ts`): `teamStopSignals(appRef, worker,
includeEnded, first, after): StopSignalPage!`,
`setTeamStopSignal(input: SetTeamStopSignalInput!): StopSignal!` and
`liftTeamStopSignal(appRef, id): LiftStopSignalResult!`. The operations live in
`internal/api/queries/team.graphql`; there is no schema refresh in this change.

## Commands

- `team stop list [--worker <ref>] [--include-ended]` (alias `ls`). Sends
  `first: 200` (the server maximum) and pages to the end. A next page with a
  null, empty or repeated cursor refuses (exit 1) and prints nothing, since a
  partial list would read as complete. `--worker` and `--include-ended` are
  omitted unless passed. `--worker` means the signals covering that worker now
  (Victor's FIT point 1), never "targeting".
- `team stop set --scope worker|team|host --level pause-dispatch|pause|halt
  [--worker <ref> | --host <id>] [--until <RFC3339>]`. Worker and team scopes
  need `--worker`; host scope takes `--host`, which may be omitted so that a
  host key's own host is resolved by the server. Wrong-scope target flags,
  empty targets, unknown levels or scopes, and an `--until` without an offset
  refuse locally (exit 2) before any request. `--until` is parsed with
  RFC3339Nano and sent as UTC without rounding. Omitted, it is not sent (until
  lifted), and an explicit null is never sent: genqlient `omitempty` on
  `worker`, `host` and `until`. Future-time checks stay server-side.
- `team stop lift <signal-id>`: one id. `changed: false` prints "had already
  ended; nothing changed", never "Lifted". Every lift receipt says other
  signals may still cover the same workers.

## Credential

All three use the person's credential. Neither mutation is in the
authored-write list (`write_attribution.go`), so a bound worktree's worker
session is never attached. This matches `setModelFamilyAvailability`: the
server never counts a worker as managing the App (R113). There is no host-key
discovery and no fallback; a host key works when it is the configured
credential.

## Output

JSON uses explicit DTOs mapped from `StopSignalFields`: id, level, scope,
targetWorkerId, targetHostId, setAt, until, setByUserId, setByHostKey, live,
endedAt, endKind, endedByUserId, endedByHostKey. Nulls are preserved, with no
nested principal or synthesized URN. The list is an array (`[]` when empty);
set returns the signal, and lift returns `{changed, signal}`.

Text names the App and its source and prints the stored values. The set
receipt says "Stop signal stored. Its notice is recorded but not delivered."
No receipt claims that workers stopped, were interrupted or resumed, or that
the stop is in effect on any host.

## Refusals

Exit classes in `api/errors.go`:
- `STOP_SIGNAL_UNAVAILABLE` (#2173): 5, the platform's state.
- `HOST_SCOPE_REQUIRED` and `STOP_SIGNAL_SCOPE_FORBIDDEN`: 8. Both are matched
  ahead of the `_REQUIRED` usage rule.
- `STOP_SIGNAL_NOT_FOUND`: 4, through the existing suffix rule.

`stop.go` prints `CODE: <fixed sentence>` for each known code (the stop codes,
plus BAD_USER_INPUT, APP_NOT_FOUND, FORBIDDEN, UNAUTHENTICATED and
GRAPHQL_VALIDATION_FAILED); NOT_FOUND and BAD_USER_INPUT have a sentence per
command. The server's free text is never printed, and an unknown code prints a
generic line with MapError's exit class. A lost answer on set or lift keeps
the CLI's own transport sentence (exit 7), adds "check with hadron team stop
list --include-ended before retrying", and is never retried.

## Holds in the help

The group help, and the set and lift help, carry:
- the no-use hold: no set or lift before hadron-server#2169 is live;
- the fact that every set is currently refused with STOP_SIGNAL_UNAVAILABLE;
- the dark notice lane: no worker is told and nothing is interrupted.

The open Holger questions H21, H23 and H24 are not mentioned. The cor:agt:090
drafts are cited by loc only.

## Tests

`internal/cmd/team_stop_911_test.go` (red first in b94f11f) covers:
- exact wire inputs and their omission;
- every local refusal, with zero mutations sent;
- scalar and null DTOs;
- lift changed true and false;
- page draining, filters and empty `[]`;
- stalled, empty and repeated cursors;
- fixed sentences with no echo of an upstream sentinel;
- one send on a 502, for both set and lift;
- the help-copy holds.

`internal/api/errors_test.go` pins the new exit classes. Independent
compiled-CLI QA against one real server stack is a review seat, not part of
this change.
