# Task priority and blockers

Targets the merged S1 server contract at cf9bcfece3e6fc1d12fd6caf82a50c941035680a
(server #2726, parent #2644). Refs CLI #1135.

Create optionally sends priority and repeated blockedBy refs. Omission is
load-bearing: the default flag value is 1 for help, but the input pointer is
set only if the caller supplied the flag. Otherwise the server default applies
without turning an ordinary create into an assigner-authorized operation.
The existing project omission annotation remains intact during regeneration.

Three dedicated generated mutations set priority, add a blocker or remove a
blocker. Refs are trimmed ids or handles, not parsed locally. Existing bound
worker attribution applies to these mutations. Range, assigner authority,
same-App visibility, cycles, allowed states and atomicity remain server decisions.
Known task refusals retain their code and mapped message/exit status. No mutation
is retried and an error prints no partial task receipt.

The shared task fragment and explicit command DTO expose priority and blocked.
Both dependency directions are bounded pages: first 20, explicit total/hasMore,
and summary items containing only id/handle/title/status. A separate dependencies
read takes limit/offset for both directions so callers can inspect later pages.
An empty items slice serializes as [], while an unavailable page remains null.
Neither a short page nor a false blocked flag is used to infer hidden relations.
Ordinary task list preserves server order; matcher priority is not list sorting.

Author checks use generated-wire fixtures for omission, bound actor propagation,
create and read round trips, refusal codes/exits/no stdout, single mutations,
server ordering and dependency paging. Independent QA must exercise a compiled
CLI against a private S1 server, including authority and atomic refusals. No
production mutation or rollout is part of this change.

## Fix-cycle constraints

Release/install prerequisite: every target server must carry #2726 (cf9bcfece)
or later. Shared task fragments require these fields even on existing task
commands; a merged server SHA alone is not a deployment receipt. Self-hosted
operators must update the server before this CLI. No release is authorized here.

The task mapper also prefixes known refusal codes on existing task commands
(create, assign, reassign, simple transitions and finish). It wraps the mapped
error so classification and GraphQL extension readers retain the original chain.
Task-ref operations do not resolve --app: server-side caller context resolves
bare handles, and callers with ambiguous handles use ids or qualified handles.

List cost is explicit and accepted for this slice (Ada, team chat #44472): each
row resolves two bounded 20-item dependency pages. The selected server performs
readability checks and findMany/count separately per direction; there is no
request-local dataloader. A 200-row list page therefore invokes 400 relation
sub-resolutions, and list --team scans every task page. Retaining these summaries
meets the requested list detail, not a claim of batching or measured throughput.
Server follow-up: hadron-server#2764. Non-blocking CLI review follow-ups: #1182.
