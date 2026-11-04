# Drive read and stdout export (#275 first cut)

Gate 6368861611 accepts Jane6359116344's list/get/export-to-stdout cut.
Refs #275, which stays open for separately gated remote document creation.
No output-file option, exportNodeToGoogleDoc, background upload, consent or auth
flow is added. Static provider source inspected at merged server
726f8d6ad4c0e9aef71254c322f585608e8550fd: query.session driveFiles/driveFile/
driveFileExport use runOwnedEmailOperation and owner/drive.read ConnectionGrant
semantics. Org-admin connection metadata does not confer mailbox/Drive access.
The tool, not independent server validation, supplies export format/binary/size
guarantees; typed too-large refusal is preserved, never silently truncated.

List returns exactly one bounded DriveFilePage with files and nextPageToken.
No automatic --all loop, retry, total or completeness claim. Explicit top defaults
to 50 for browse, 25 when any query/nameContains/mimeType/modifiedAfter criterion
is present; folder or page token alone remains browse. Top range 1..100 is checked
before a request. Next token/null is preserved. Human mode only suggests another
page when a non-null token is returned; callers retain all filters for that page.
Opaque page tokens are forwarded unchanged, including punctuation and whitespace;
blank supplied tokens/filters are refused. Modified-after is an RFC3339 instant
with an offset, never a guessed local zone or all-day midnight.

Connection and file IDs are explicit, nonblank and trimmed. No discovery or
org-admin fallback. Absent optional variables are omitted, not null. Export format
is optional (omitted delegates the tool default); explicit MARKDOWN/TEXT/HTML is
case-normalized to the GraphQL enum, unknown or empty values fail locally.

Explicit file DTO has id/name/kind/mimeType/parents[]/size/createdAt/modifiedAt/
webLink. Size remains nullable Float BYTES, including large/fractional numbers;
no integer/zero substitution or exact-integer precision guarantee. Null native
document/folder size remains null. Null required response/list/row fails before
output. Page files and file parents remain [] when empty.

Get/list human mode labels quoted JSON metadata as external data and identifies
the connection source. Text export alone writes the original content string's
bytes to stdout, with no banner, formatting, newline, truncation or evaluation.
JSON export is {file,format,mimeType,content}. External content is never executed,
interpreted as instructions, rendered as HTML by the CLI, or copied to stderr.
No default file output or implicit remote mutation exists.

Existing api.MapError and provider refusal sentences/unknown-code fallback stay
unchanged; no guessed reconnect or retry. No schema/shared error policy/spec law
or mint changes. Synthetic serialized pins and independent compiled fake-HTTP QA
cover the cut; no real Drive/provider spend or stack.
