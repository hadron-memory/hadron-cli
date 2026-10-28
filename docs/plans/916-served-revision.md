# #916 C3b: read the recorded served revision

Separate `team worker served-prompt <worker-ref>` and `run served-prompt <run-ref>`
commands keep ordinary get queries/DTOs unchanged. Worker refs are explicit IDs
or fully-qualified worker URNs; no ambient App/name or current-prompt lookup.
This is the merged S10 Worker.servedPrompt/AppRun.servedPrompt surface, from
selected clean server main27455a482. The SDL is the same source export used for
C1b, not a proposal. Governing rule cor:agt:040:03.

The envelope is `{servedPrompt: record|null}`. Null includes concealed owner or
record absence, serving-off and unrecorded states; it is not diagnosed as one
of those by a secondary lookup. The record contains id, kind, ordinal,
pointerLevel, compiledNodeId, compiledContentHash, servedRevSeq,
provenanceRevSeq, servedAt, recorded variables, pruned and provenance. Revision
numbers are revSeq. No body, loc, title, source entity or live edge is requested.
Variables are recorded values, not current worker settings. JSON retains them;
human text uses SafeCell, as for every server string. This is a record read,
not a promise that the recorded text is still retained or readable.

Provenance uses the actual `{items,total}` offset page (default50/cap200,
explicit0counts), with nodeId/revSeq/hash/pruned entries. Served-provenance
pruned is a provider Boolean, unlike C1b source-status nullable Boolean: no
cross-surface inference. `--all` buffers pages and checks record identity,
recorded fields and total before output; changing/null/error/stalled pages
produce no partial result. It cannot promise one database snapshot over pages.
Explicit --offset excludes --all, including explicit offset0. Duplicate source
occurrences are legal, so no source-ID deduplication is performed.

Null returns success with the nullable envelope; typed provider refusals use
api.MapError. Invalid pagination/worker-name arguments fail before a request.
No new mutation, session binding, activation, prompt reconstruction, source
read or local authorization rule. Other #916 cuts remain separate.

Author tests: absent commands red firsta0addb2, captured single-operation
nullable record, recorded revisions/variables/hash/pruning, explicit count0,
two-page all, later error/null/change/stall/no partial result, and bad arguments
with no requests. Independent CLI/authority QA remains required; author runs
are fake transport, never production or private-stack evidence.


## Review closure

The shared task Project omission defect from the schema refresh is fixed at
AgentTaskCreate's generator annotation source, with the same actual task-create
wire and explicit false/true controls as C1b. No manual generated edits.

Tests now assert populated/null and complete --all human output, control safety,
changed totals, over-limit and beyond-remaining pages, and null items. A bounded
fake-provider request budget makes a no-advance regression fail instead of hang.
An offset at/past total must have an empty page; the old code accepted a nonempty
one (red pin), now refused by the same remaining-count guard. JSON page limit
continues to mean requested per-page size under --all, not aggregate item count.
