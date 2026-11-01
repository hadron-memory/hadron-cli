# Cross-entity discovery and public catalogue (#1345)

Four existing server queries gain typed CLI commands. `find` is separate from
node search and forwards the raw query, explicit fields/entity kinds, optional
org/scope/App context and one page's limit/offset. The server owns sniffing,
visibility, ranking and the bounded total. New explicit DTOs preserve nullable
identity/description fields, degraded notes and scope disclosure.

Marketplace agents/memories/orgs selects only PublicResourceRef's four fields,
returns a page envelope and never fetches nested/full resources. Public clients
attach credentials if present but do not require login; server refusals remain
MapError. Nothing interprets listedOnMarketplace locally or adds omitted hits.

No ambient App/org/scope default is consumed; every optional constraint is sent
only on flag presence. Explicit empty constraints refuse rather than silently
widening. Query shapes are not canonicalized or sniffed in the CLI. Page order
is retained; stderr hints advance by returned count, not requested size.

Red5f887543 fails because all four commands are absent. Core tests pin query
bytes, omission, enum overrides, flat DTO/nulls, pagination variables and usage
refusals; supplementary pins cover scope and count-only. The stamped T2+section10
schedule is cross-family LOCAL+BLIND, security each head and QA through the CLI.
QA must separate real server eligibility/sniffing evidence from fake wire tests.
No server/schema/MCP/portal change or production/release/install is in scope.
