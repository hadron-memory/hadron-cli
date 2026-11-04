# Usage activity read slice (#65)

Implements CHECKED6540693917/amendment6540641301, cor:acl:090:01.
`usage activity` reads the server's date/read/write buckets. Optional from/to,
bucket and time-zone pointers stay nil until the corresponding flag is passed.
Repeated memory references are passed verbatim via StringArray (no comma splitting
or normalization). An omitted memory list remains nil/omitted. There is one read
request and no client scope resolution, reference-count cap, retry, fallback,
zero-fill, sums or computed window. Empty resource results remain empty.

JSON is an explicit array DTO `{date,reads,writes}`, preserving order and values;
empty is `[]`. Nil bucket rows fail before any success output. Text names the
requested scope, shows provided flags or “server default”, then the buckets.
Dates are server labels, SafeCell'd in text only; locally supplied flags are
quoted, not treated as server cells. Refusals use MapError.

`usage events --node` takes a server entity PK or qualified node URN (legacy
qualified forms accepted through the existing canonicalizer), never a bare loc.
It conflicts with --loc before a client/request is built. The existing loc
variable remains distinct in the generated operation; no extra resolving read
is introduced. The ordinary events DTO and paging/money behavior are unchanged.

The code-derived coverage matrix marks usageActivity as a command. Its
Arguments section lists only *unreached* arguments, so the now-reached nodeId
line disappears rather than accepting an invalid manual `flag` disposition.
The command flag itself is in Cobra/help inventory. recordUsage remains gap#65;
nodeUsage, billing and ledger writes are outside this slice.

Tests use synthetic HTTP replies and inspect omitted variables, exact resource
lists, one request on refused21-ref scope, sparse/out-of-order buckets and
refusals. The gate schedules independent cross-family LOCAL/BLIND, scoped
security on scope selection/no fallback, and compiled fake-HTTP QA. No production
read is needed or authorized. Refs-only; this read slice does not close umbrella65.
