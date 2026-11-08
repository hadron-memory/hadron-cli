# Read-only usage reporting (#65)

CHECKED T1 Xavier6358377036, Patrick6358307897; Ada31774. Provider source
origin/main812b4fac: usageEvents in resolvers.query.session.ts and summary in
resolvers.query.memory.ts; committed SDL already contains both operations.

`usage events` fetches exactly one explicit page (default20, local1-200 limit,
nonnegative offset, omitted zero). Optional memory is canonicalized locally,
loc stays the exact loc field (never nodeId), type goes to the type argument.
Empty filter flags refuse locally rather than silently widen the read.
JSON is an array of the selected event metadata: id,type,createdAt,memoryId,
nodeLoc,provider,model,keyOwnerType,tokensIn,tokensOut,costMicroCents. No action
arguments or caller-identity payloads are selected. Nulls remain null; monetary
values remain strings without parsing, rounding or conversion. Human price
null is unpriced; token null is unknown. A price string of 0 is priced zero.
Units are micro-cents, 1e-8USD, not cents. No aggregate/balance is derived.

The provider returns no total or since filter. Stderr labels page offset/limit
and no total available; exactly-full pages say more may exist. No automatic
paging or --all/since. Empty JSON is []. The provider owns scoping/errors; the
CLI does no secondary tenant filtering or existence inference.

`memory usage <ref>` reports the server's per-node-loc/type event counts as an
array {nodeLoc,type,count}. It is not a client sum or money total. The server
scopes on retained memory attribution; older events without that attribution
or a loc may be absent. Memory denied/missing remain provider errors.

No deprecated nodeUsage, recordUsage/accounting write, billing activation,
model/provider invocation, new provider policy, release or install. The issue's
write/since/deprecated surfaces are not fulfilled: PR Refs65 leaves it open.

## `usage events --app-key` (cli#1203)

Design as built. Usage by App key is one pass-through flag, `--app-key <id>`,
over the server's `appKey: ID` argument on `usageEventsPage` (hadron-server#2944,
merged at main `0c46aa6a`). The schema snapshot was exported from that revision.

- The id goes to the server unchanged. The CLI never resolves a key name through
  `appKeys` or any other call: a non-manager is refused there differently, so the
  lookup would reveal which keys exist. One request, one operation.
- A key-filtered read uses its own operation, `UsageEventsPageByAppKey`. GraphQL
  validates the static document, so an `appKey` argument in the shared
  `UsageEventsPage` would make every plain `usage events` fail on a server that
  predates it, even with the variable omitted. The plain operation's document is
  pinned to carry no `appKey`; with the flag absent the request is byte-identical
  to the one sent before (also pinned with an exact-JSON test).
- The key id is required in the by-key operation, never optional.
- An empty or blank value
  is a usage error (exit 2) before any request.
- A key the caller may not filter by (another tenant's, another key of the same
  App, or any key when the caller does not manage the App) answers
  `APP_KEY_NOT_FOUND` exactly as an unknown key does: exit 4, same message, no
  rows. Every other refusal goes through `api.MapError`, never an empty result.
- The server's `UsageEvent` carries no field naming the key, so the CLI shows none
  and invents no "KEY: unattributed" cell (ruling 0244 item 5 applies only where a
  key field exists). A key-filtered result is the server's rows, never summed or
  folded.
- Help follows the gate addendum and ruling 0254 (cor:acl:090:01 rev 12): no
  resource named is your own activity; a memory shows every caller's events as
  volumes; the key flag shows that key's events in memories readable now plus its
  no-memory events, for the key's App manager or the key itself.

Not built, not pinned: K's events in a memory K can no longer read (open for
Holger); an executed key-self read before the server serves it (the QA seat runs
that against a real stack); period filtering (built in cli#1205).
