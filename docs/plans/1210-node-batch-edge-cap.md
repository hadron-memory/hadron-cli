# nodeBatch edge-cap recovery (#1210)

Gate 6540788008, inventory amendment 6540720913 and post-write correction
6554436256 govern this change. The server's selected-edge budget is operational;
this PR changes no provider policy, eligibility, matching or access rules.

## Read paths at base 3f8698f4

| Caller | Strategy |
| --- | --- |
| node/getbatch explicit refs | A: same-query bisection |
| node/getbatch memory+prefix | C: explicit actionable refusal |
| memory/export | A |
| spec/get prefix details | A |
| spec/lint collectSpecDetails | A |
| spec/replace relintChanged | A, post-write exception below |
| spec/citations | A |
| spec/label_report | A |
| spec/replaced loadReplacements (added since original gate) | A |
| spec/lint rawSpecBody | B: NodeBatchContent, no edges |
| skill/skill fetchNodes | B |
| spec/checktools | B |
| spec/grep | B |
| coding/coding fetchNodes | B |

All production `gen.NodeBatch` calls now live in the API adapter. A retries
ONLY a bare GraphQL error list in which every entry has code BAD_USER_INPUT
and limit edges. Mixed errors, HTTP/transport/decode failures and prose matches
retain ordinary mapping. Refusal data is discarded, never rendered.

For a k-ref chunk, the binary split tree has at most 2k-1 requests (399 for
200 refs). Requests select the unchanged NodeBatch document, retaining both
directions and the provider's inherited/null-endpoint projection. No ordinary
edges() query or endpoint lookup reconstructs a view. Resolved-id duplicates
across halves are removed, as on the provider. Existing byte truncation,
omitted-ref requeues and unavailable refs remain the collector's responsibility.
Each call is still bounded by the provider's existing response limit.

An indivisible ref or prefix refuses with exit 2, sanitized identity and
numeric cap, and points to `hadron edge ls --source` / `--target`. Pre-write
callers emit no partial report/files. A prefix may instead be narrowed or read
by explicit refs. B documents select the same stored content/scalars without
outgoingEdges/incomingEdges; a typed adapter retains the consumer projection.

## Post-write re-lint exception

A successful `spec replace` is never retried or relabelled refused because its
subsequent read fails. `relintStatus` is complete or incomplete after an applied replacement;
`relintError` is present for incomplete re-lint, and incomplete findings are
discarded. Text never says clean for an incomplete read. Terminal edge refusals
carry the adapter's safe actionable message; other read failures get a fixed
message rather than raw server/transport text. Preview-only output is unchanged.

## Evidence and limits

Red-first command pins (corrected fixture ca884f66) fail on the cap/refusal,
not revision probes. A separate red pin 0d407ea4 proves false-clean post-write
behavior for cap and ordinary failures. Command matrices compare split/control
human+JSON and exported file bytes, empty/denied endpoints, per-caller terminal
refusals, and edge-free documents. API tests check the 399-request bound,
structured/mixed/transport controls, alias deduplication, byte spillover and
privacy-safe refusal text. Synthetic tests do not establish live provider
projection/applicability; the stamped independent private-stack QA does that.

No deployment/release claim or production probe. The local host's load rule
limits local test concurrency; hosted CI supplies the full suite where required.
