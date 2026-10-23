# Organization Agent grant list (#890 first slice)

CHECKED T1 Xavier6368509877. `agent subscription list --org <id|urn>` (`ls`)
reads Organization.agentOrgGrants once, using the unchanged explicit ref as
existing `org get` does. No prelookup/classification or ambient org fallback.
Provider source a2463194, resolvers.types.ts Organization.agentOrgGrants:
person org-admin gate; run/worker/foreign/nonadmin may receive empty projection.
An empty visible array is not a global no-grants assertion.

Select only agentId/orgId, server isActive, createdAt and nullable activatedAt,
expiresAt, revokedAt. These IDs form the grant identity; the SDL has no grant id.
No Agent, organization, key/config/customer traversal and no client license
eligibility calculation. Every row, including revoked, is preserved. Malformed
null row (SDL says nonnull) errors instead of silently dropping one.

Explicit DTO array preserves null timestamps and empty []. Text prints server
active flag/lifecycle columns; empty says no rows visible to this caller.
Missing organization exits4, provider/transport failures use existing MapError.
No pagination params are invented for this unpaginated provider field, and no
completion claim is made. Existing subscription/grant terminology is qualified:
organization licenses, not principal action grants or per-user subscriptions.

No write/subscribe/revoke/install, user-memory deletion, mine/agent expansion,
real provider/account/stack operation, release or install. Refs890 only: full
issue stays open for separately gated mutation/customer/per-user surfaces.
