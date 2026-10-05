# Comment commands (#807): implementation checkpoint

This branch implements the command layer against the pre-code machine contract
`hrn:node:hadronmemory.com:hadron-server:design:comment-nodes-contract` revision 3,
and `cor:dmo:110:04–05`. It is not a shipped command: root registration and the
mandatory genqlient adapter wait for server #1606's authoritative SDL export.
No provisional schema or hand-written GraphQL transport is introduced.

## Command contract

- `comment list <target> [-m memory] [--state OPEN,RESOLVED] [--limit 50] [--offset 0]`
  reads one explicit page of threads, retaining server total and reply counts.
- `comment get <comment> [-m memory]` reads a comment or visible stub.
- `comment create|add <target> --body text|--body-file path [--quote text]
  [--anchor-revision N]` starts a thread; omitted anchor uses server current revision.
- `comment reply <comment> --body text|--body-file path` replies in its thread.
  DAB (#6989) permits replies to resolved threads without reopening; there is no
  client-side state refusal or implicit reopen.
- `comment edit <comment> --expected-revision N [--body text|--body-file path]
  [--quote text]` preserves omitted fields; an explicit empty quote clears it.
- `comment retract <comment> --expected-revision N` retains a stub/history.
- `comment resolve <root> --expected-revision N [--reopen]` changes root state.

Every reference accepts a node id, qualified URN or bare loc with `-m`, using the
existing shared grammar. `--body -` is piped stdin, refused on interactive input.
Validation runs before connection/auth. Explicit expected revisions are comment
revisions, never target or anchor revisions. Conflict/permission refusals are
not retried or replaced with an automatic read followed by an overwrite.

The stable command DTOs preserve null fields (including stub bodies), public
scalar identities, target/anchor metadata and state. Page/reply slices serialize
as arrays. Human output labels state, author and an older target anchor.

## Integration still required

1. Obtain an exact server revision/SDL from Diego; export the schema using an
   explicitly selected worktree, then write operations and run genqlient.
2. Implement the Service adapter exclusively with generated operations. Optional
   edit body/quote and create quote/anchor must use omitempty pointers, preserving
   absent versus explicit empty values. Map typed COMMENT_* refusals to existing
   exit categories with actionable remedies. No auth or server gate duplicated.
3. Register the command in root; update agentic usage and unbound-ops baseline.
4. Add the target commentSummary cue to node get from the server export. Generic
   comment-node refusals should direct callers to the governed command group.
5. Coordinate with Jonas (#821) on session attribution for authored writes;
   do not independently invent a second binding/header policy.
6. Run command-level fake GraphQL cases plus integrated standard-stack cases at
   the server head. Finish the author review checklist, open the PR as draft if
   server dependencies are still unmerged, then route through Xan.

Search is not dispatched in this slice and waits on server #1608. No rule or
citation is minted: these commands implement existing cross-client contracts.

## Validation at this checkpoint

Focused tests cover pre-client refusals, omission versus quote clearing, piped
bodies, explicit anchors, conflict/no-retry discipline, page request semantics,
null stubs, empty arrays, missing-comment exit 4 and human stub rendering. The
package tests, race run and vet pass. This is command-layer evidence only, not
transport compatibility, integrated QA or readiness evidence.
