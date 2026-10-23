# Email connections inventory (#198 first slice)

`email connections --org REF [--json]` exposes only the sanitized organization
connection list selected by Xavier gate 6368859516 and Jane attach 6359115081.
The umbrella stays open (Refs #198). No messages/folders, mailbox authorization,
reconnect/consent, reply/draft/send/delete/flag/categorize/mark-read is added.

Provider source: merged server e421cc9c4506152474285952050fde35c48d53c3,
resolvers.query.session.ts exchangeConnections and helpers.ts
listOrgExchangeConnections/orgExchangeConnectionScope. The server resolves the
org ID or URN and enforces requireOrgRole ADMIN before listing nondeleted rows
in createdAt/id order. This command does not duplicate or broaden that gate.
Connection administration is separate from mailbox owner/grant authority.

The selected wire is the existing exchangeConnections(orgId: ID!) bare list,
deliberately not the newer exchangeConnectionsPage. One request returns the
provider list, preserved in server order. The CLI invents no total, continuation
or completeness guarantee. Empty means no rows returned to this caller.
No schema change or new provider dependency is needed.

Explicit --org is required and whitespace trimmed once. Missing/blank org and
extra args are usage errors before any client/request. The explicit ref is
forwarded to the server; no organization discovery, guessed default, label
parsing or hidden lookup. Human output quotes it and identifies --org as source.

The explicit stable JSON DTO is an array of rows with exactly id,
organizationId, userId, mailboxEmail, provider, displayName, syncEnabled,
syncStatus, lastSyncAt, createdAt, updatedAt. Empty arrays are []; nullable
fields remain null, distinct from empty strings. The query never selects
credentials or lastError. Human strings are quoted, including control characters.
A null list or null row is a protocol failure, not an empty inventory.

All provider errors use existing api.MapError; unknown email tool codes keep
the existing fallback. This slice adds no error map or instruction guessed
from untrusted provider prose. Fake HTTP and synthetic serialized fixtures
cover metadata/null/empty/invalid/admin-refused/unknown-error cases, explicit
scope variables and the actual generated document's field allowlist. No real
mailbox/provider spend, messages, stack, corpus edit or spec mint.
