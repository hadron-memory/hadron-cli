# Dark-worker alarm CLI companion (#1208)

Build on fresh CLI main 8f8b93d after Hugo's deployment confirmation (chat 47446): server #2755 / PR #2789 merge 5faecd4f first deployed0.0.1405; current 0.0.1410 built 8889e61c1 includes it. Schema exporter and schema-check use detached server 8889e61c1, not a shared branch. The refresh also carries deployed #2821's recoveryEdges(edgeIds) schema addition; no new recovery operation is authored here.

Delivery settings use the existing string integer flag pattern. Explicit whole signed32-bit input goes to AppDeliverySettingsInput.darkAlarmWindowSeconds; server owns bounds300–86400 and default 1800. Omission preserves. The DTO and get/table add the field; ordinary caller/auth/session behavior is unchanged. The new flag participates in empty-patch/updated messages. Duplicate existing genqlient annotation removed while editing that input.

Task-less event rows keep the existing table columns and put worker <name> (reason) in the TASK cell. Known reasons overdue and wake-unacked are fixed vocabulary; arbitrary metadata is not printed. Task-bound rows and acknowledgement output are pinned byte-for-byte. Task-less ack names the worker; JSON event shape is unchanged, including evidence only when returned. No lifecycle inference, status calculation or client-side dark detector.

Standalone red commit 327dc35 proves missing worker/reason and unknown flag on base; task-alarm baseline passes. Added controls cover get text/JSON, omitted input, integer parse errors, server range refusal/no retry, known/unknown reason and JSON metadata preservation. T1 seats: cross-family LOCAL+BLIND, independent real compiled binary/fake API QA. No stack required by stamp 6505866240.

Help/agentic usage and handwritten docs companion #533 propagate the surface. No platform contract or mint is authored. Stop at ready; coordinator/CM owns merge, release/install/activation separate.

Fix after the initial review: restrict worker/reason rendering to the exact
alarm.dark-worker type in both event rows and acknowledgements. Subject-bearing
non-alarm rows retain the prior dash. The regression test failed on 2e0c948.
The new setting stays first, beside the non-deprecated checking settings; JSON
key order is not a semantic promise. No older-provider compatibility is claimed.
