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

The selected GraphQL mutation is parsed structurally. Every root field must
belong to a finite authored-field list; operation labels, aliases and input
text cannot opt in. Named/inline root fragments are resolved, with unknown or
cyclic fragments failing closed. Mixed maintenance mutations stay headerless.
The list covers node CRUD/move/clone/import/merge,
node data, edge CRUD, object CRUD, governed spec/task/review node writes,
search/replace, restore-revision, asset-reference nodes and parent-node
extraction. Queries, approval/mint maintenance, arbitrary raw API requests,
session lifecycle and unrelated mutations retain their current contexts.
The eight comment fields published at server commit 95d1a9bf (#7014) are
covered too, independently of cli#807's generated operation labels. No comment
command or hand-edited schema snapshot is added by this PR.

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

## Published comment contract

Diego published createComment, replyToComment, editComment, retractComment,
resolveCommentThread, reopenCommentThread, hideComment and deleteCommentThread.
The client hook now keys on the actual selected mutation fields, so Jane's
adapter inherits attribution even if its generated operation labels differ.
HTTP policy tests cover all eight fields and aliases, selected operations,
variables, root fragments, input-text lookalikes, mixed maintenance and malformed
or cyclic documents. These are request-policy checks against the published SDL,
not live comment CRUD. Integrated comment behavior belongs to #807/#1606.
