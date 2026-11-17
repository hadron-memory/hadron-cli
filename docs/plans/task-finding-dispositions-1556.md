# Task finding dispositions

This slice exposes server#2663 through `disposeAgentTaskFinding`, the paged `AgentTask.findingDispositions` field and the `agentTasks(unresolvedFindings)` argument. It follows cli#1555 and leaves immutable findings input/read to cli#1550. It adds no MCP surface, authorization rule or result mutation.

## Commands and output

- `hadron team task dispose-finding <task> <finding-id> --disposition fixed|accepted|deferred|rejected [--ref kind=target[|label]]` sends one mutation. Its bare JSON receipt is `{taskId,findingId,disposition,reference}`. The receipt comes from the server-returned latest disposition; a missing readback is an error with an unknown-write-outcome hint, never a fabricated receipt or retry. The response selects all possible dispositions (write cap 100 findings, read limit 200) to find the named finding in server order.
- `hadron team task finding-dispositions <task> [--limit N] [--offset N]` returns one page: `{items:[{findingId,disposition,reference}],total,hasMore}`. Default 20, cap 200, zero count-only; follow hasMore. Empty items is `[]`; absent reference is `null`, and a reference has `{kind,target,label}` with nullable label. This does not fetch the referenced text or immutable findings.
- `hadron team task list --unresolved[=false]` selects a separate typed document. True narrows to finished tasks with at least one undisposed finding; false does not narrow. All four disposition codes resolve a finding. The filter is server-side before paging and combines with the existing filters. Both named values conflict with the coordinator `--team` view before any request. Omission retains the existing plain and filtered list documents and output bytes.

References reuse the existing task-reference parser: kind plus stored target and optional label, no fetching or client authorization inference. Omitting reference on replacement deliberately removes the earlier reference from the new disposition value. Human cells neutralize controls and bound text; JSON retains complete originals.

## Server source and compatibility

The named isolated snapshot refresh is server `4d1d71dcd66d76575b90debe091c821b484a4ca7` (deployed build 0.0.1636 per Hugo's receipt; contains the gated minimum d5111f74e6). The refresh was run with explicit HADRON_SERVER_DIR against a detached checkout and a private exporter install. The inherited snapshot was already byte-identical to this export after cli#1555; the new operations regenerate the client. The earlier accepted drift from cli#1555 is accounted for by that inherited snapshot, rather than relabeling the older d4 export as current. Schema-check is run against this exact revision.

New commands and the named unresolved flag require server#3258. Existing task DTOs/shared fragments and documents are unchanged. A separate `AgentTaskListUnresolved` document preserves older document validation when the flag is absent. No release is authorized here; compatible provider deployment is a prerequisite.

The matrix indexes root operations and unpassed inputs, not arbitrary nested output fields. Per the gate-holder clarification, its `agentTask` row names `team task finding-dispositions` (selecting the nested disposition page), `disposeAgentTaskFinding` names `team task dispose-finding`, and the now-reached unresolved argument gap disappears. Other rows are preserved; no generator expansion.

## Refusals and verification

The server owns assigner/status/finding checks: `TASK_NOT_OWNER` remains Forbidden (8), `TASK_NOT_FINISHED` is Conflict (5), `TASK_FINDING_NOT_FOUND` is NotFound (4). New surfaces preserve serverCode in the JSON envelope and the code in safe human messages. No local authority inference or retry. Result bytes are not sent to the mutation.

Red tests are committed separately at f1aeeea8 on fresh main135cda93: missing command/flags fail before network. Unit/fake-server pins cover all four values, exact variables, reference/label omission, raw hostile text vs safe rendering, server-derived receipts, null/missing readback, paged/count-only reads, filter true/false/omitted and combination with other filters, no request on bad arguments, output failure without retry, and the actual error renderer's exit/serverCode.

Independent compiled-binary QA runs on one fresh private compatible server stack. Its required legs include latest replacement, owner vs assignee-alone, unfinished/unknown finding refusals, all codes, reference replacement/omission, paging, unresolved filtering and hostile references. No production writes are part of this procedure. LOCAL and BLIND are distinct cross-family engineers; QA is separate. Findings and limits are recorded at the frozen head, not inferred from unit tests.

## Fetch budget and follow-up

Each read or write sends exactly one GraphQL request; bad local syntax sends none. A normal disposition page selects only finding id/code/reference plus total/hasMore, not immutable finding text or the task detail. A write must find one returned item in a bounded set of at most 100. The server exposes no targeted single-disposition read; server#3325 records that low API-efficiency improvement. This is the smallest available projection for a server-derived receipt, with no extra lookups. Expect a few KB for a typical page and tens of KB for 100 short references; hostile escaped maximum-length strings can be larger. QA records actual GraphQL body bytes, request count and process wall time at its chosen corpus size rather than treating these estimates as measurements.
