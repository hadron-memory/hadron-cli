# Resend sender and platform invitation coverage (#1342)

The CLI exposes the committed server operations without local authorization
rules. `org resend-sender` has create/get/list/update/rm; `user invite` creates
a platform invitation. Existing org member commands now send orgRef/userRef.
The six operations and six reference-argument gaps in the coverage matrix are
covered; deprecated ID arguments become explicit exclusions on regeneration.

## Input and output

Sender create uses `--org` (ref), `--from` and a key read from file/stdin or a
masked terminal prompt. Update uses presence-aware optional variables, including
false for enabled; rotating a key requires `--api-key-file`. There is no inline
key flag. Sender IDs are raw IDs. List's optional `--org-id` is a raw org ID,
with default exhaustive pagination and explicit one-page limit/offset mode.
The stable DTO contains only sanitized metadata and hasApiKey. Errors on
key-bearing writes retain mapped exit/serverCode but discard backend text and
its raw error chain. Reflected key text in returned metadata is redacted.
Deletion confirms before any network request, and false/null is never success.

Invite exposes all five optional fields. Omission preserves server defaults,
including one activation and no expiry; explicit zero values pass through.
The slug is an acceptance token and is intentionally returned to its creator;
no email is sent and identity hints do not restrict who may accept. The server
owns admission and membership rules. Accept uses the existing invite command.

## Verification and limits

Fake GraphQL command tests cover operations, variables, omitted fields, false
and zero values, pagination, empty arrays, not-found, false deletion, local
refusals, classified secret-safe errors, and member-ref forwarding. The original
red test commit precedes implementation. QA's stamped CLI/private-stack run
covers sender CRUD, key rotation and member email/handle refs. Real Resend mail
delivery is outside this registry surface; no real provider call is claimed.
No server, portal, MCP, schema snapshot or platform law changes are required.

Expected cost: each targeted get/create/update/delete/invite/member mutation
makes one GraphQL request, with a few hundred response bytes for ordinary
metadata. An exhaustive sender list makes ceil(N/200) requests (one on an empty
scope), roughly 300–600 response bytes per ordinary row; no roster is fetched
to resolve an org or sender. A compiled fake-server probe of 211 sender rows
made two requests, 49,161 response-body bytes, and took about 25 ms on this host.
This is a synthetic measurement, not a production latency claim.
