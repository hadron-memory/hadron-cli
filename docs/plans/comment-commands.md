# Comment commands (#807)

Adds `hadron comment` for governed feedback beside a target node. The commands
implement the server contract at
`hrn:node:hadronmemory.com:hadron-server:design:comment-nodes-contract` and
`cor:dmo:110:04–05`, including DAB (resolved-thread replies do not reopen).
Search remains a separate slice waiting on server #1608.

## Commands and write semantics

- `comment list <target> [-m memory] [--state OPEN,RESOLVED] [--limit N] [--offset N]`
  reads one page of threads (default 50, range 1–200), preserving total and reply counts.
- `comment get <comment> [-m memory]` reads one comment or visible stub.
- `comment create|add <target> --body text|--body-file path [--quote text]
  [--anchor-revision N]` starts a thread; omitted anchor uses current target revision.
- `comment reply <comment> --body text|--body-file path` replies in its thread,
  including a resolved thread, without a client-side state refusal or reopen.
- `comment edit <comment> --expected-revision N [--body text|--body-file path]
  [--quote text]` preserves omitted fields; explicit empty quote clears it.
- `comment retract <comment> --expected-revision N` leaves a stub/history.
- `comment resolve <root> --expected-revision N [--reopen]` changes thread state.

References use the shared node grammar: node id, qualified URN, or bare loc with
`-m`. Piped `--body -` is allowed; interactive stdin is refused. Validation runs
before connection/auth. Expected revisions are the comment's observed revision,
not its target or anchor. No read-then-overwrite, blind write retry or inferred
authorization is introduced. Generic comment-node refusals point to the governed
group. The assigned writer operations are covered; admin hide/delete remain
explicitly annotated gaps in unbound-ops for later parity work.

## Typed transport and output

The schema snapshot was exported from Diego's explicitly selected
`95d1a9bf` worktree via the repo exporter, using its installed tsx directly
(the host's pnpm 11 attempted dependency auto-install instead of running the
export). Server PR #1663 at `63513268` has the same SDL. No snapshot or generated
client is hand-edited. GraphQL operations use shared comment/actor fragments.
Optional create quote/anchor, edit body/quote and list state use omitempty;
captured raw-variable maps pin omission separately from empty strings.

`Service` is the command/testing seam; its production implementation calls only
genqlient operations. Root registration uses the ordinary factory client.
Authored writes inherit Jonas's #822 command-local binding guard; this branch
adds the six generated comment mutation labels to its finite policy. Queries
remain headerless. The pending structural policy update from Jonas will replace
that label extension before final readiness. Destination memory gates stay
server-owned. The CLI does not select the portal-only viewer-author/open-thread
fields because its reads do not carry the worker actor.

JSON is explicit command-local DTOs, not generated structs. It retains comment
and target identities, public author refs, binding-user provenance, anchor/hash,
state, revision, nullable body/quote and timestamps. Empty thread/reply slices
are arrays. Human output labels author, stubs and an older target revision.
Every published COMMENT_* refusal is assigned a deliberate exit category and
root-command tested. A missing mutation result exits 7 with an unknown-outcome
message, rather than reporting success or retrying.

`node get` adds `commentSummary` via a separate optional, capped nodeBatch
projection so older servers still read the existing body. Unknown counts remain
null/unavailable; they never become zero. Spillover is requeued with a progress
guard. Feedback changes without advancing the target revision, so the cue is
an advisory snapshot outside the revision-consistent body bracket.

## Validation

- Full `go test ./...`, relevant `-race`, lint, build, codegen and unbound-ops checks.
- Generated-request tests cover all writer operations, actual session headers,
  omitted versus cleared fields, the server thread fixture, null result and every
  typed refusal without retries. Older-server cue compatibility is tested.
- Three verified compiling mutants are killed by their intended tests: omitted
  edit-body directive removed, create-comment attribution allowlist entry removed,
  and COMMENT_NOT_AUTHOR exit mapping removed. Equivalent revision `+0` survives.
- Author live checks used the standard per-worker Incus stack `jane-807` built
  from server `63513268`: create, one-open refusal, quote clear, stale edit refusal,
  resolve, resolved reply/no-reopen, retract/stub, filtered page, reopen, unchanged
  target body/revision, feedback counts and generic-create refusal. A separate
  disposable worker binding proved worker author and binding-user provenance.
  Stack, forwarder and captured credentials were removed afterward.

Author evidence is not independent QA or readiness. The PR remains draft until
server #1663/#1658 and CLI #822 settle; then revalidate the exported snapshot and
attribution policy, obtain named cross-family review and independent QA via Xan,
and check CI. GitHub Actions is degraded as of the author run (#7041); failed or
unexecuted CI cannot be treated as product failure or green evidence.

No spec citation is minted: this is a client implementation of existing rules.
