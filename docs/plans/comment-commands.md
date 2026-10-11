# Comment commands (#807)

Adds `hadron comment` for governed feedback beside a target node. The commands
implement the server contract at
`hrn:node:hadronmemory.com:hadron-server:design:comment-nodes-contract` and
`cor:dmo:110:04–05`, including DAB (resolved-thread replies do not reopen).
`search --comments-only` implements the merged server #1608 search filter.

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
export). Server PR #1663 at `63513268` had the same SDL. After its merge, the snapshot
and generated client were refreshed from explicitly selected server main
`c2686749`, then `601970717c3fc1c2fc0227d427895241581ce34d` for search.
The final contract returns at most the oldest 200 replies per
thread; JSON retains the exact replyCount and human output flags truncation.
Reply pagination is a server follow-up, not implemented by this client. No snapshot or generated
client is hand-edited. GraphQL operations use shared comment/actor fragments.
Optional create quote/anchor, edit body/quote and list state use omitempty;
captured raw-variable maps pin omission separately from empty strings.

`Service` is the command/testing seam; its production implementation calls only
genqlient operations. Root registration uses the ordinary factory client.
Authored writes inherit Jonas's merged #822 command-local binding guard and
structural mutation-field policy unchanged. All six writer comment fields
(and hide/delete for parity) already belong to that policy, independent of
generated operation labels. Queries remain headerless. Destination memory gates stay
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

Author evidence is not independent QA or readiness. CLI #822 is merged; this
branch is rebased onto main with its structural
attribution policy unchanged. The PR remains draft for the sprint-2 blind
review protocol. Server #1663/#1658/#1674/#1679 have merged and the snapshot
has been refreshed from selected main `60197071`. Obtain named cross-family
review and independent QA
via Xan, then check CI. The Actions incident hold was lifted at team #7076;
failed or unexecuted CI is not green evidence. Earlier live author evidence
remains pinned to `63513268`, not the merged server revision.

No spec citation is minted: this is a client implementation of existing rules.

## Comments-only search

`hadron search <query> --comments-only` sends `NodeFilter.contentScope=COMMENTS`.
The server filters before ranking, limits and pagination in hybrid, keyword,
vector and regex modes; the CLI never fetches content then filters a page.
Memory, prefix, tags, type, object-type and JSON predicates remain AND-combined
server filters. With --comments-only, a non-comment --type is refused before
connection (exit 2), because it cannot match a comment. The default omits contentScope, preserving the server default
CONTENT (or its explicit nodeType=comment inference). `--comments-only=false`
also omits it. `--scope` retains its separate stored-memory-lens meaning.

Human output labels comments-only results as feedback, not verified target
content. JSON retains the existing search hit DTO with nodeType=comment; no
new target metadata projection is added. Use `comment get` for the complete
comment/target/author contract. Older servers refuse the explicit COMMENTS
input, while ordinary searches omit it and keep the same operation document.
All operations using the shared NodeFilter annotate the new field omitempty.

Author tests capture COMMENTS and composed filters/page arguments for every
ranking mode, JSON comment identity, the human label and absent-key behavior
with no flag, another filter, explicit --type comment and false. No new
platform spec: this client flag implements the existing server contract.

## Cycle-1 review corrections

COMMENT_MERGE_FOLDS_THREADS is mapped to Usage (2), with root-command and
API-map tests. The generic formatter previously dropped recoverable extension
values; it now forwards only code/currentRevision/threadId/class from comment
refusals (including NODE_WRITE_CONFLICT), as error.extensions in JSON and
labeled values in human errors. Rendered-output tests exercise the production
failure path and assert unrelated extensions remain absent.

Comment-addressed help names comment-ref; create/list name target-ref. Empty
quote on create is omitted; edit still forwards empty as a clear. JSON search
continues to use nodeType=comment as its existing feedback identity signal;
its scope object retains its separate memory-lens meaning. No new scope field
is invented for COMMENTS. This is documented, and empty search results carry
no per-hit identity (the caller knows its explicitly requested filter).

Remaining review follow-ups recorded in the PR cycle disposition: read-path
exit-7 wording, additional truncation/terminal-stdin/requeue tests, node-get
feedback shape/cost, and schema-refresh bundling practice. Hidden-state/API
replacement and revision behavior are assigned as CLI #836 after #823 merges
(Marco team #7838), with its merge depending on server #1727. This head keeps
the reviewed merged-server contract; no hide/retract removal is folded into it.
