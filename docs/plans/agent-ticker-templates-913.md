# Agent ticker-template CLI (#913, MCP cut C1)

Four typed operations in agent_ticker_templates.graphql expose per-template
CRUD under `agent ticker-templates` with singular alias `ticker-template`.
The existing committed SDL already contains the wire; no snapshot refresh.

List takes an Agent ref unchanged; create requires name/cadence and omits
absent optional fields. Update/delete take only opaque template id and required
expected revision. No fetch/rewrite, inferred revision, retry or runtime worker
mutation. Delete uses the normal explicit confirmation and requires --yes off TTY.
The shared ticker parser functions preserve the existing reasons, relevance and
quiet-hours vocabulary; server validation and authorization remain authoritative.

DTOs are package-local, with explicit agent id/name, revision, shapeVersion,
tickCode, requested cadence, JSON quietHours, lower-kebab reasons, nullable
updatedAt. Lists and reasons are arrays including the empty case. Human cells
are guarded. Creation/update return one object, delete {id,deleted:true}.
Template limits and revision mismatches map to conflict exit5. Writes never join
the authored worker-session mutation allowlist.

Help follows the server contract: Agent managers write; App managers gain no
implied Agent edit rights; changes affect future casts only; requested cadence
is raised to the fresh App floor when casting. Existing workers/tickers remain
untouched. Separate `ticker apply-templates --worker` adds the template tickers a worker
lacks without changing existing tickers.

Gate: #913 comment6539354001, T1 cross-family BLIND+LOCAL, UX, compiled CLI QA on
one disposable stack; scoped help-wording spec fit (Victor per Ada #54739) on the wording before ready. Refs
#913 and hadron-server#2937, neither closed by the PR. Release before MCP cutover
is a later gate, not inferred by this build.
