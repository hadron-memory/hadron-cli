# Authored write session attribution (#821)

Design posted on #821 before implementation. Depends on server#1658 merged and
deployed before landing. DAC's MCP connection binding is a separate server
follow-on. Header attribution never changes the authenticated user's access.

The root installs one command-local context hook at the authenticated generated
client seam. The first request snapshots the worktree binding, before any
lookup can race a rebind. Each eligible mutation re-reads worker ID, session ID,
App ID and recorded deployment. Missing provenance, another deployment/App,
changed binding or no binding leaves the write headerless. Unreadable binding
or ambiguous App identity emits one diagnostic per command. A canonical App
lookup is headerless and the binding is rechecked after its network round trip.

The finite generated-operation list covers node CRUD/move/clone/import/merge,
node data, edge CRUD, object CRUD, governed spec/task/review node writes,
search/replace, restore-revision, asset-reference nodes and parent-node
extraction. Queries, approval/mint maintenance, arbitrary raw API requests,
session lifecycle and unrelated mutations retain their current contexts.
Future comment generated operations are added when cli#807's authoritative
SDL exists. This is an explicit extension seam, not guessed comment support.

Destination memory is not treated as an App-ownership check. Server#1658's
bindSession validates session ownership, endedAt and expiresAt and publishes the
actor independently of target-memory access. A worker may author a write to an
authorized org knowledge memory. Ambient App context, when present, still must
resolve to the binding's App. The server stays authoritative for an ended,
expired or foreign-principal session; such a header cannot invent an actor.
The CLI does not add a liveness query to every write.

No retries: the header is already recognized by old servers and replaying a
mutation could duplicate work. The wrapper sits above the unchanged transport;
bearer/session redirect and insecure-URL protections still apply. No new schema
or JSON output. Server authorship fields/read projections remain #1658's scope.

Author tests observe real command/generated-client HTTP envelopes for bound,
unbound, same-App aliases, other App/deployment, absent provenance/worker,
a lookup-time rebind/end, per-write App/deployment rewrites, a second-write
rebind, and a rebind during canonical App lookup. Query/maintenance/raw API
controls stay headerless; an authored edge is attributed; a business refusal
is never retried. Integrated worker+agent/user and revision evidence remains
an independent QA dependency against the deployed server, not a mocked claim.
