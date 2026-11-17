# Channel memory projections (#1090)

The readable Channel and its host memory metadata have separate visibility.
The four ChannelMemory consumers are Channels, GetChannel, CreateChannel and
UpdateChannel. Each keeps its successful parent result when memory is null.
The package DTO emits memoryUrn and memoryName as explicit null, and text uses
`(not readable)`. MemoryID stays the Channel's scalar; no other object supplies
withheld metadata. Readable strings, including empty strings, retain their values.

The committed schema already made ChannelMemory.Memory nullable. Re-exporting
from hadron-server 31d638284 produces no SDL drift; regeneration changes only
comments that described the older whole-parent-null assumption. There are no
selection, optional-input, authorization or server-policy changes.

Request paths remain: one mutation for create/update, one Channels request per
page for list, GetChannel plus the pre-existing best-effort NodePortal read for
get. That enrichment supplies only portalUrl and never memory metadata. No new
memory lookup or fallback is added. Existing GraphQL/transport error mapping
and whole-Channel-null not-found behavior remain; empty lists still emit [].

Inventory: MemoryMembers/MemoryShares and member/share mutations in
memory_access.graphql select roles and user/grantee fields, not nested memory.
They need no nullable-memory projection change. No other nullable surface is
fixed here.

Evidence: red commit 94f8f8c5 fails on JSON empty strings and blank memory cells
for all four consumers. Mixed rows, readable empty values, empty lists and
field refusals are pinned at the command output. Compiled fake-transport probes
cover null success and FORBIDDEN exit 8 across all four operations and exact
request counts. Existing SafeCell tests exercise server-controlled readable
cells. Synthetic evidence does not establish live access policy or deployment.
