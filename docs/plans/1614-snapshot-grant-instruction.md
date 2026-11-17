# Snapshot-grant list/revoke instructions (#1614)

Schema snapshot is exported from server
7907d93de89fd02ef14b914613485e75b86519f5 (#3379 / #3361). The refresh adds the
four automation grant instruction arguments and provider descriptions; only
team snapshot list/revoke are wired by this CLI change. Set remains person-only.
Automation read-grant coverage and #3381 are separate work.

The shared parser handles explicit --instruction before confirmation/network.
The binding is snapshotted then; the #1613 shared callback rechecks it before
each page and before the revoke. Only the server decides target App admission.
An unusable local binding uses the existing person fallback; a server refusal
is never downgraded/retried. Separate instructed documents preserve the legacy
no-flag documents, requests and DTOs, including null revocation fields.

List still reads all pages by default, advancing by rows actually returned;
--limit bounds it to one page. Each admitted instructed page, even empty,
records one team event on the server. Later-page refusal suppresses all output.

Revoke without an instruction retains its existing history-lookup then
returned-App/trigger confirmation order. With an instruction it pre-confirms
the grant ID, target App and one-event-per-page consequence before any request
(Xanthos ruling #72769). After consent it reads the complete App history
instructed, then sends one instructed revoke if the selected row is live,
without a second prompt. Missing row means NotFound/no revoke; ended row means
readback/no repeat revoke. This history walk has no --limit option; its event
count grows with history pages. The gate accepts this (#72758/6620052939).

Expected request count: list = pages (one when --limit is supplied); revoke =
history pages plus one mutation for a found live row. No App identity lookup.
Each page selects only eight grant metadata fields plus total/hasMore; cost
scales with grant history, never worker roster/task contents. No atomic snapshot,
runtime concurrency, old-server instruction support or production proof claimed.

Author tests cover legacy and instructed documents/variables/session headers,
paging, early usage/confirmation, one pre-confirm, missing/ended rows, refusal
without partial output/fallback/retry, and binding changes between pages.
Independent T1 seats: cross-family LOCAL and diff-only BLIND, plus a compiled
CLI on one fresh private provider stack; no security seat per the gate.
