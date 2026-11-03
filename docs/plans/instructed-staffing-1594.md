# Instructed staffing and member writes (#1594)

Five commands gain an optional `--instruction <kind>:<ref>`: `team worker cast`
(including `--dry-run`, which calls `castWorkerPreview`), `team worker retire`,
`team worker rm` (alias `delete`), `app member add` and `app member remove`.
This is the CLI side of hadron-server#3308 (S2b of #2483, PR #3342, merged
as 517eb1db; rulings 0096 and 0097, cor:dmo:050:12, cor:agt:080:24). The
schema snapshot is refreshed at 517eb1db. `leaveApp` also gained the argument,
but no command calls it (gap #63), so it stays out of scope.

## Documents

Each door gets a separate generated operation (`CastWorkerInstructed`,
`CastWorkerPreviewInstructed`, `RetireWorkerInstructed`,
`DeleteWorkerInstructed`, `EnsureAppMemberInstructed`,
`RemoveAppMemberInstructed`) with a required `$instruction`. Without the flag
the old document goes out byte for byte (pinned against the generated
`*_Operation` constants), with no `instruction` key, so older servers keep
working. The CLI never retries, and never downgrades a refused instructed call
to the plain one. Parsing reuses `internal/instruction.Parse` from #1593/#1598:
it runs first in each `RunE`, so a bad kind or blank ref is exit 2 before any
prompt or request.

## Attribution (the #1592 matrix, per command)

None of the five is on the authored-write allowlist (`write_attribution.go`).
Today each is the signed-in person's write and carries no session, even from a
bound worktree. That is `worker change-agent`'s row of the #1592 matrix, so
the flag follows that row, not `worker update`'s fail-closed row:

- **Without the flag:** unchanged, headerless as the person.
- **With the flag:** the binding is snapshotted before the command's first
  lookup (`team.BeginInstructedWrite`). At send time the write carries the
  snapshot's worker session only when the binding is complete, for this
  server and for the **target App**, and unchanged (the existing
  `instructedSession` re-read; a binding that appears in a worktree that was
  unbound counts as a change). Otherwise the write goes out headerless as the
  person, with the existing stderr note. The server then checks the person's
  reference and records no claim.
- **Target App:** for `retire`/`rm` it is the resolved worker's own `appId`
  (an ambient `--app` elsewhere does not matter). For `cast` and the member
  doors it is the App ref the write names (`--app` or the active App). When
  that ref is not literally the binding's App id, it is resolved headerlessly
  with `app(ref:)` (`TeamAppIdentity`), only after the server, completeness and
  unchanged checks pass, so an unusable binding costs no lookup. An App the
  caller can't see resolves to "", so the write goes out as the person. A
  failed lookup exits with its own code and sends nothing; it is never one of
  the member doors' "outcome unknown" errors.
- **Lookups never carry the session.**

`app` cannot import `team`, so the member doors reach the same function
through a new `cmdutil.Factory.BeginInstructedWrite` hook, which the root
installs, as `InstructedContext` was for #1595. The two failure messages that
said "the worker" now name "the target", since the target can be an App.

## Refusals and output

FORBIDDEN is exit 8 with `serverCode` in `--json`. To meet acceptance 4, the
five doors' write errors now go through `MapErrorKeepingCode`: `castChoiceRemedy`,
retire, rm, and the member doors' definite-refusal branch. On the no-flag path
this adds `serverCode` to the error envelope. That is additive; exit codes and
messages are unchanged.

`cast --dry-run` with the flag is not write-free: an admitted instructed
preview records one claim event. The text receipt says the instruction went
out through the worker session exactly when it did (the finisher reports
that), hedged: the server records a claim it admits, never a host-launched
session's, which the CLI cannot tell from the binding. DTOs are unchanged.

`app(ref:)` is readable only to the App's org members (and platform admins),
so a holder who is an App member outside the org gets a null lookup. The note
then says the target App can't be read as them, not "a different App", and
the write goes out as the person.

## Help

The doors share `instruction.StaffingHelp(grant)` (`workers.staff` or
`app.members`), which covers the following: the person's call without the
flag; the attribution rule; that the holder answers for the claim and still
needs the right; that the server checks the reference, never that an
instruction happened; that previews and no-ops record a claim; that a
host-launched session's instruction is ignored and it acts on its own grant
only (refused without one, admitted with one, per ruling 0097); that a worker
session without a claim needs the grant, which nobody holds by default; that
the MCP tools take no instruction; and the minimum server 517eb1db. The cast
help no longer says a preview "writes nothing".

## Tests and mutants

`internal/cmd/staffing_instruction_1594_test.go` covers:

- the wire for each of the six requests, across the three kinds and the
  first-colon split;
- the plain documents, byte for byte;
- shape refusals with zero requests, with and without `--yes`;
- the attribution matrix per command: same App by id and by alias, ambient
  `--app`, another App, a foreign server, an incomplete binding, unbound, and
  outside a worktree;
- a rebind during the target lookup, and a binding appearing during the
  worker lookup. Cast and the member doors make no lookup when unbound, so
  the send-time re-read is the only window there;
- unchanged attribution without the flag;
- the preview receipt;
- refusal pass-through (FORBIDDEN 8, BAD_USER_INPUT 2, `serverCode`);
- a lookup failure sending nothing;
- the help.

17 mutants, one per guard, all killed. Each was applied by exact-string
replacement that had to match once (a non-match is reported, never run),
compiled as part of `go test`, and reverted with `git checkout`:

| Mutant | Killed by |
|---|---|
| flag ignored (`staffingInstruction` always nil) | Wire, ShapeRefused, Matrix, Rebind, Appears, Refusals, Receipt, LookupFailure |
| no target-App check in `instructedSession` | Matrix |
| alias never resolved | Matrix, Rebind, LookupFailure |
| no send-time re-read | Rebind |
| lookup carries the session | Matrix, Rebind |
| cast / member / retire / rm drop `serverCode` (4) | Refusals |
| unbound reported as the worker's | Receipt |
| member add / remove report a lookup error as a write outcome (2) | LookupFailure |
| retire parses after its lookup | ShapeRefused, Rebind, Appears |
| receipt always printed | Receipt |
| rm sends the plain document with a claim | Wire, Matrix, Rebind, Appears, Refusals |
| unreadable target App called "a different App" | Matrix (target App unreadable) |

## Spec reach

The CLI decides no rule here: the server contract is cor:dmo:050:12 and
cor:agt:080:24. The attribution rule is client-only (the #1592 matrix), so no
new citation is proposed.
