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
