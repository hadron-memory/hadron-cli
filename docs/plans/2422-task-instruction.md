# `--instruction` on the twelve `team task` writes (#2422)

First child of the #1617 tracker. Server: hadron-server#3323, merged as
ba077810 (#3376); the schema snapshot (361c241f) already carries `instruction`
on all twelve mutations and none on `createAgentTasks`. Governing rule:
cor:dmo:050:12. Spec FIT: Victor and Vera. Gate: T1, §10 yes.

## What it does

`team task create | update | set-priority | add-blocker | remove-blocker |
assign | reassign | cancel | park | ready | dispose-finding | release` take
`--instruction <kind>:<ref>` (shared `internal/instruction.Parse`; the ref is
everything after the first colon, as written). A bad kind or a blank ref is
exit 2 before any request. Each command has a separate `...Instructed`
document (`agenttask_instruction_2422.graphql`), so a no-flag request is
byte-for-byte what it was.

- **Session.** Task writes already carry the bound worker's session through
  `AuthoredWriteContext`, which verifies the binding for every authored
  mutation. This change adds only the claim; it does not touch that rule, so
  an unusable or absent binding sends the write as the person (the middleware's
  existing notes), with the claim checked but not recorded by the server, as
  in #1592, #1594 and #1614.
- **Pre-reads.** `release` reads the outside item first and `create` finds the
  App (`TeamMemoryApp`) when the worktree has a team memory. Both stay as they
  were: the claim rides only the mutation. The other ten go straight to it.
- **Batch.** `create --batch-file --instruction` is exit 2 with nothing sent:
  `createAgentTasks` has no instruction argument.
- **Refusals.** The server decides admission. A refusal is shown once and is
  never retried without the flag.
- `update --instruction` alone is still "pass at least one field flag": the
  instruction is not a field.

Out of scope (the tracker's other families): `setAgentTaskStallSchedule`
(`team stall-schedule set`), `acknowledgeAlarm`, App and team controls, and the
five markers Eden reconciles.

## Generator note

`genqlient` generates a shared input type once. A new document that uses
`CreateAgentTaskInput` or `UpdateAgentTaskInput` without the omit-vs-null
directives the plain documents carry changes those types between runs, so the
two instructed documents repeat the plain ones' `# @genqlient(for: ...)` lines.
Regenerating twice gives identical output.

## Tests

`internal/cmd/team_task_instruction_2422_test.go`, per command (twelve):
the instructed document rides with the claim, the same variables as the plain
run and the bound session; the plain document is unchanged and carries no
instruction; a bad flag sends nothing; a refusal is shown once with no plain
retry; no binding sends the person's write with the claim; help names the flag
and the server minimum. Plus the batch refusal and a ref sent byte for byte.
