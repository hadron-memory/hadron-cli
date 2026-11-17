# Retired literal prompt fields (#1173)

The CLI stops reading or writing Agent/App `systemPrompt` and Worker
`promptOverride` before hadron-server#2712 removes those fields. This CLI change
must be released before the server field-removal merge. It does not change the
serving gate or migrate stored values.

Agent create/update and App update lose the system-prompt flags and file/stdin
paths. Worker cast/preview/update lose prompt-override flags. Removed flags fail
locally with usage exit 2 before credentials, file/stdin access or requests.
Agent and worker JSON omit the retired keys. Worker `prompt`, boot pointers,
served-prompt records, role metadata and `systemMemoryId` stay available.

Worker update now only edits signature/trailer templates. Their omission/empty
semantics and mandatory verified bound-session attribution remain unchanged.
The obsolete override-only operation is removed; signing keeps its operation.

An Agent starts without a prompt. Write `boot:source` with role `boot-prompt` in
its system memory, then compile and approve. A worker source is
`boot:workers:<id>:source` with role `boot-prompt` in the App install memory.
Use `hadron team worker compile <worker>` and `hadron node approve <compiled-node-ref>`
for that source. These are separate operations; this PR introduces no create-time source or
implicit compile/approval.

The schema snapshot stays at the selected published provider contract while
#2712 is pending. A red-first test removes retired fields and arguments from
that parsed schema and validates the entire CLI operation set against it.
This proves the client query set is compatible with both the present schema
and the removal shape; it does not claim a live server removal was tested.
Generated API and operation/help inventories are rebuilt from source.

Coverage includes removed flags refusing with zero requests, no retired output
key even if a provider returns one, ordinary Agent/App updates, cast briefing,
worker signing presence and bound-identity controls. Existing prompt-writing
tests are retired along with their deleted feature, while AI-config stdin
controls remain.
