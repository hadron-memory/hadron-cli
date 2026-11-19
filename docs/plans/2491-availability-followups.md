# cli#2491 — availability follow-ups: deferred review pins and clarifications

As-built plan (test/doc/refactor only; no production behavior change). Gate: the
CHECKED stamp at <https://github.com/hadron-memory/hadron-cli/issues/2491#issuecomment-6979468235>
(T1 Standard). Premise re-verified on main `36f2a1ce` (2026-11-19): the mutant
below still survives `go test ./internal/cmd/ -run 'TestTeamAvailability|TestAvailability'`,
and nothing in #2536/#2543/#2549/#2551 touched the availability surface.

## F1 — pin the instructed cross-App fallback (Rook's surviving mutant)

`availability set --worker … --instruction` calls `ctx = withAsPerson(ctx)`
(`internal/cmd/team/availability.go`) before the instructed flow, so the
authored-write middleware cannot attach the worktree binding's session to a
person fallback. No test failed when the call was removed (verified by running
the mutant before and after this change, private GOCACHE): every existing
fallback mode (`none`, `wrong-server`, `incomplete`, `unreadable`, `changed`)
leaves the binding in a state the middleware independently declines.

**Finding during the build:** the mutant is *observation-equivalent* through
today's CLI. The middleware skips queries outright (`authoredWriteRequest`
gate), so no lookup can carry a session, and on the mutation its own
App-identity check declines the one topology where the binding is otherwise
valid (App-A binding + `--app <App-B id>`). No black-box test can distinguish
the mutant from the real code; a line-level kill would need a production
change (out of scope per the gate). What the new pin does instead — Rook F1's
exact topology — asserts:

1. the fallback note names the different-App reason;
2. the mutation carries the instruction claim, targets App B, and **no**
   session;
3. **no request in the whole flow carries a session header**.

That pins the security property end-to-end: it kills the equivalent
middleware-side mutants (removing the identity check, or any future change
that makes the middleware attach in this fallback — verified by running the
identity-check mutant against the pin), so the `withAsPerson` line remains as
defense-in-depth rather than as the last line of defense. The gate left the
line-kill question to the ready call ("that call is theirs"); this is the
honest position to review.

## F2 — agentic synopsis: `--instruction` is set-only (Jimmy F2)

The combined synopsis line in `internal/cmd/agentic/agentic-usage.md` reads
`availability get | set (…flags… [--instruction …])`, implying `get` takes the
flag; `get` has no `--instruction`. Split the flags so the set-only ones sit on
the `set` arm (minimal edit; no contract change — the documented behavior
sentences already describe `set`).

## F3 — explain the instructed call site's empty target and discarded bool

Same treatment as #2537's f35c59d8 gave the other two call sites: a comment at
`instructed(ctx, "", *appRef)` explaining the empty `targetAppID` (the command
holds the App as `scope.Ref`, not an id; the write resolves it) and the
discarded bool (worker-session-attached — unused because admission is the
server's either way and a fallback note was already printed if one applied).

## F4 — dedupe the instructed/plain DTO conversion (Jimmy F3/F4)

Three hand-rolled copies of the worker-record mapping (plain set, instructed
set, `get` loop) and three of the family mapping. The generated projections
carry getters but their party-typed getters return distinct concrete types, so
a shared interface is not available; the dedupe is one primitives-level builder
per kind, with the generic `partyDTO` at the edges:

- `workerAvailabilityDTOFrom(worker, setByParty *taskPartyDTO, state, reason,
  until, setAt, setBy, inEffect)` — used by all three sites (the nil-party
  deref dance lives in one place);
- `familyAvailabilityDTOFrom(appID, family, state, reason, until, setAt,
  setBy, inEffect)` — same for the three family literals.

Wire/JSON contracts are already pinned byte-exact by
`TestAvailabilityInstruction2450Wire` (plain vs instructed input and `--json`
output identical) and the attribution tests; those must stay green unchanged.

## Non-goals (per the gate)

- Plain mutation null-result handling — separate baseline QA intake (the null
  paths here already answer via `availabilityResultMissing`/the instructed
  twin, pinned in `team_availability_null_2493_test.go`).
- Rook F2 (direct-worker/no-App session-attach branch) and the extra
  instruction-kind/available-state coverage (Jimmy F6) only where they beat
  shared-helper coverage: kind parsing is `internal/instruction`'s own tested
  surface; the no-binding fallback-note contract (no note when no binding
  existed before and after — `instruction.go`) is already pinned by the
  `none` mode of `TestAvailabilityInstruction2450FallbackAndRefusal`. Nothing
  added beyond the F1 pin.

## Verification

Named mutants only, private `GOCACHE` in a trap, files restored from backups
(nothing cached shared, nothing else reverted). The measured matrix:

| mutant | plain cross-App pin | instructed fallback pin |
|---|---|---|
| clean code | pass | pass |
| middleware identity check removed | **FAIL** (mutation carries `s-new`) | pass (`withAsPerson` covers) |
| availability's `withAsPerson` removed | pass (identity check covers) | pass (equivalent mutant) |
| both removed | — | **FAIL** (mutation carries `s-new`) |

So every guard on the fallback is singly pinned except availability's own
`withAsPerson` line, whose removal is provably redundant with the middleware's
identity check and dies only as the double mutant — stated plainly here and in
the test comments for the ready call to weigh (the gate left that call to the
coordinator).

- `make lint`, `go test ./...`, help-inventory check untouched (no help text
  changes; the agentic synopsis is not the help inventory's surface).
