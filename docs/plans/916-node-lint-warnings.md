# #916 C3c: explicit node source-lint reads

Petra42032 narrows this cut to node get and node write/approval receipts.
`--lint-warnings` is opt-in on get/add/update/approve/move/clone/merge/restore.
Other worker signature, skill, bulk-authoring and approval-history surfaces stay
separate. Existing node get batch/prefix mode adds the advisory page per returned
node only when explicitly requested; this is not a new discovery pass.

The separate GetNodeLintWarnings query reads Node.lintWarnings(first:100,after).
Its shape matches merged S11a: items(kind,position,text,targetUrn,pinnedRevision,
newerRevision,overriddenNodeLoc,workerField), total,nextCursor. It never inflates
ordinary read/write documents. Query/errors/nullable field on older or denied
providers become requested-but-unavailable, not a clean page. No target lookup
or hidden-state inference fills a nullable server field.

JSON omits lintWarnings when off (including explicit=false); when on it is null
if unavailable, otherwise {items:[],total,nextCursor:null} after exhausting every
cursor page. Existing DTOs stay explicit; all nullable server values and text
are retained. Human text/kinds use SafeCell. Body-only/dry-run conflict with an
actual true opt-in before any request, because those modes cannot represent a
post-success current-warning receipt.

A successful write is never blocked, undone or retried by warning lookup.
The lookup uses the returned node ID, after mutation success, never an input
loc/URN guessed into another scope. A failed write never looks up warnings.
Warnings are current advisory state at the read, not the warning set of the
authored/read revision and not a cross-page database snapshot. Changed totals,
null items, repeated/empty cursors, stalled pages or failure after a page produce
null for the entire requested page, never partial/forged clean output. Pathological
reads stop at1000requests as unavailable. Existing response-extension warnings
from main#1142 are independent and unchanged; flag-off adds no new field/read.

Main C1 b580cb87 and C7 7d5a0e7 are merged without rebase. The C7 conflict
resolution keeps both early body-only/dry-run checks and installTarget checks;
native install writes still carry install+loc, and an install data-merge refuses
before any request, including with the new opt-in. Existing create/kind/expected
revision/presence/authored-session behavior is unchanged.

Red first7f3952c for the absent flag; additional null-items red cabde8a. Author
pins nullable/clean/current pages, explicit false/default no operation or field,
full two-page cursor collection, late failure/null/change/stall/no partial data,
exactly one successful mutation, no read after refusal, get/approval receipts,
terminal-safe text/verbatim JSON and native install selector composition.
Independent compiled-CLI/authority QA remains owed. No author production or
stack claim. Refs916, leaves umbrella open; no release/install/activation.

## Review closure

The advisory read has one five-second budget for the whole command, shared by
all pages and all nodes in batch get. It starts only after the successful main
operation, never constrains or retries a mutation, and yields requested-null on
timeout. A canceled/expired budget makes no further provider request. Red-first
unit pins require the independent deadline, shared batch budget and canceled
request refusal; the request cap remains a second pathological-page bound.

Every one of get/add/update/approve/move/clone/merge/restore now has on/off/default
command pins. Writes assert exactly one native mutation; the warning query ref
is the returned/resolved ID, including different clone/move/merge result IDs.
Batch get is tested in JSON and human mode for each node. Its existing renderer
calls renderNodeDetail, which already calls renderLintWarnings, so no additional
renderer path was missing.

Only get has body-only and only update has dry-run. Tests assert the other six
commands have no dry-run/preview/body-only mode to invent a policy for. Merge
confirmation precedes the mutation and lint read: both noninteractive refusal
and explicit interactive decline are pinned to no mutation/no lint read.
