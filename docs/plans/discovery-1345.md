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

The red-first test commit `5f887543` fails because all four commands are absent.
Core tests pin query
bytes, omission, enum overrides, flat DTO/nulls, pagination variables and usage
refusals; supplementary pins cover scope and count-only. Gate 6540755109 requires distinct LOCAL and BLIND engineers from a different
model family, a security review at every head, and QA through the compiled CLI.
QA must separate real server eligibility/sniffing evidence from fake wire tests.
No server/schema/MCP/portal change or production/release/install is in scope.

Review fix: human scope disclosure names the server source without attributing it
to a flag. The server resolves an explicit org id or fully qualified org URN;
the CLI adds no lookup. Tests pin non-empty degraded notes in JSON and stderr,
raw whitespace, a short page hint, and absence of ambient configured defaults.
