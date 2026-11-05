# App-defined action CLI (1669)

Provider: merged server d8ec074f5b, schema exported from that exact checkout.
`recordAppAction` and event `codePrefix` are consumed without client authority
checks. Grammar is regex plus urn-lib-go ValidateAtomShape and MaxAtomLen;
server package urn-lib-js0.0.18 MAX_ATOM_LEN=64, Go0.0.18 MaxAtomLen=64.
The server alone checks current App slug and task membership.

`app action record` validates all local inputs before configuring the client.
Payload-file reads are bounded to4097 bytes, payload is a JSON object <=4096
bytes, absent task/payload are omitted. One mutation, no retry or preliminary
lookup. A lost response may have recorded an event; inspect events before
retrying. Package DTO eventId plus shared cmdutil.ActionAnswerDTO preserves
nullable loc/URN and all server states. Text SafeCell handles server strings.
The register mapping is never an authorization decision.

`team events --code-prefix` uses a separate typed operation, preserving the
original unfiltered document and variables. Filtering/paging stay server-side,
no implicit prefix/fallback. Prefix app. includes platform events too.

Red9b568f39 fails on main for absent action command/prefix flag. Fake HTTP pins
omission, states, local no-call refusals, code/atom/64 boundary,4096-byte/file
boundary, typed code preservation, and unchanged unfiltered shape.
No server/MCP changes, production probe, instruction arm, catalog or register
writing. Interim rename/person-resolution wording remains qualified.
