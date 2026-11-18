# Chat reader across chat-message search exclusion (#1516)

Gate: CHECKED6556816264, schedule6556741039 (T2, LOCAL/BLIND/QA, no §10).
Spec: cor:api:090:02, explicit nodeType wins. Companion server#2836 may
exclude chat-message from unqualified findNodes; the CLI must ship first.

## Design as built

CollectMessages reads the same memory and messagesLoc prefix twice through the
existing ChatMessages document. The first drain stays unqualified for legacy,
academy and record messages. The second explicitly asks for chat-message. The
access gate, selected fields, sort and page size stay unchanged; no schema or
operation change is needed.

Each drain has its own seenLoc map. It advances by rows served, continues past
short/clamped pages, and terminates only on empty (retaining existing nil-result
handling). A nonempty page with no new valid loc is ListNotAdvancing. Any error
from either drain returns no partial transcript. Results merge only after both
drains complete, deduplicated by loc; the unqualified version wins duplicates.
Then the existing exact container-loc exclusion, since filter, stable seq sort
and parseMessage apply. A nil-seq row found by the unqualified (legacy and
current-provider) drain keeps its existing position, after the sequenced rows
and in its stream's order. A nil-seq row found only by the explicit chat-message
drain follows those, in its own stream's order: the stable sort runs over the
two streams concatenated, unqualified first. That is the stated merge order, not
a global cross-stream ordering or snapshot guarantee. `--since` drops every
nil-seq row from either stream.
No stored cursor/read mark changes, no team-chat or memory-export change.

There are twice as many paged drains on today's provider, including one empty
terminator per drain. If each stream returns n rows at page clamp p, that is
2*(ceil(n/p)+1) reads when the streams match; there is no per-message fanout.
No new fixed history cap. Memory grows with collected unique rows per drain;
message data from both streams is retained until completeness is established.
Concurrent writes between the drains are not a new snapshot guarantee.

## Verification

Test-only red3888ce7b on fresh main cbe7dd32: a provider excluding chat-message
from the unqualified read loses the platform row. Today's provider has no second
drain. Six byte-for-byte main goldens (text/JSON; since0,1,3) were recorded before
the implementation. Tests run root chat read against clamped HTTP pages for both
provider behaviors, mixed academy/platform/record/nil-seq/container fixtures,
independent paging, failures and offset-ignoring in each drain. No partial stdout
on refusal. The existing130-row clamp test expects both drains, still130 messages.

Required mutants: remove explicit drain; remove cross-drain loc dedupe; share
one paging seen-set. Each must fail the corresponding compatibility test.

Follow-up pins (cli#1519). The goldens are a recorded baseline, not a value to
keep current: `testdata/chat-read-1516/PROVENANCE` names the revision they were
recorded from (cbe7dd32) and the SHA-256 of each, a test checks the files against
it, and `UPDATE_1516_GOLDENS` refuses unless it names that full revision AND the
code under test (`internal/cmd/chat`, `internal/api`) equals it (the original
recording commit passes; #1518 and later do not). Further pins: a same-loc row
with different content, envelope and seq in both streams emits the unqualified
row once; the exact offset sequence of each drain (today `[0 2 4 5]`, after the
exclusion `[0 2 4]`, explicit `[0 1]`); and qualified-only nil-seq rows plus the
container loc in the explicit stream (sequenced rows first, then nil-seq rows in
concatenated-stream order, the exact container absent, a near-container sibling
kept).
Provider applicability is independent QA: real compiled CLI on private stacks
at main-equivalent server and #2836 head, platform and academy/legacy fixtures,
since/JSON comparison to main, at least partly Sonnet-class/luna, cleanup.
Author fake-server tests do not establish that runtime leg.

## Delivery order and limits

Refs #1516 and hadron-server#2836, leaves both open. Merge CLI, obtain Holger's
release GO, release this reader before server#2836's merge/deploy per Xan's order.
No release/tag/install/deploy authority is implied by this PR. Release notes must
say older chat readers need an upgrade before the server exclusion is deployed.
No product/spec rule is introduced; no new status/DTO/flag/help contract.
