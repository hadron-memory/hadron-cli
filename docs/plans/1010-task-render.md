# Task render (CLI #1010)

`hadron task render <node-ref> | <loc> -m <memory> [--args <json|@file>]`
calls only the generated `RenderRunNode` Query. Shared reference composition
accepts IDs and qualified references or composes a loc within a memory; it adds
no identity lookup, runnable/type restriction or dependency read filter.
App context never changes this command into execution. Existing `task run`
behavior and DTO remain unchanged; mutual help explains its action-run usage
record versus the Query's content-read recording.

Arguments must be one JSON object, validated before client creation or any
request. Raw JSON is retained to preserve typed values and numeric precision.
The generated optional variable omits absent args and sends explicit `{}`.
Human stdout is the response string verbatim; JSON uses the explicit
`{"rendered":"…"}` DTO. Empty success is valid. Errors use `api.MapError`,
without retry/fallback. No local assembler, execution, run polling or new law.

Provider snapshot: committed SDL last refreshed in CLI `51db5f71`, against
hadron-server `39eb1dfb76a78dba0866445f94b6a00a731e00b3`. It already contains
`renderRunNode(args: JSON, nodeRef: String!): String!`; no schema refresh is
needed for this operation. Governing rules are cor:api:060:05, cor:api:140:03,
cor:dmo:060:03 and cor:dmo:080:02, per the issue's FIT and CHECKED stamp.

Evidence boundaries: fake wire pins prove document/variables, bytes, arguments
and shared refusal mapping, not provider assembly/access/usage accounting.
The server owns those semantics. No production, execution, deployment or
live-provider proof is claimed by this change.
