# cli#916 C1b: compiled revision provenance

Petra dispatch42861, final provider server#2685 merged as27455a482. Read-only
`node provenance <ref> --revision N` wraps compiledPromptProvenance with the
provider's offset pages, not the earlier proposed cursor envelope. Refs #916;
umbrella stays open. No spec minted: this passes the existing cor:agt:040:12
contract, including Holger's final nullable source-pruning visibility rule.
No compiler/source selection, approval, serving or activation change.

## Wire and privacy

The ref is passed through BatchNodeRef (id/qualified URN/bare loc with memory)
without a live-node prelookup: the provider applies its exact compiled
revision/history gate. Null is one unavailable answer, exit4, not proof of
absence and not an empty occurrence list. Genuine provider errors map normally;
there is no substitution of the newest live compiled-from edge.

Select only the released page and occurrence scalar fields plus recorded
position/selection values. No source bodies, memory objects, users or source
nodes are expanded. Recorded node ids/revisions/hashes/settings remain visible
under the compiled gate. Source pruned is copied as nullable Boolean; null
is not shown, never inferred as retained or pruned. Compiled pruned describes
that compiled revision's text and is a different, nonnullable field.

## Output and pagination

Explicit command DTO: nodeId, revNo, contentHash, version, pruned, items, total,
hasMore, limit, offset. Occurrences keep order, depth, parent, kind, nodeId,
revNo, hash, replacesNodeId, nullable position, selection and nullable pruned.
An empty items collection emits[] rather than null. Human cells are terminal
safe; JSON preserves the provider's scalar values. Source status is shown as
retained/pruned/not shown without a hidden-target lookup.

CLI defaults one page20, cap100. Zero means counts only and is actually sent
as zero. Positive 32-bit revision is required; negative/out-of-range offsets
or limits refuse before requests. --all starts at0, excludes explicit offset
and counts-only, and collects before printing. Each response must retain the
same node/revision/hash/version/total/compiled-pruned state. Offset advances by
returned count; zero progress, overflow, duplicate/nonincreasing occurrence
orders, a late unavailable page, failure or incomplete final count produces
no partial record. Separate requests are not one database snapshot; source
availability may differ across pages, which the per-occurrence nulls preserve.
No whole-read or mutation retry is added.

## Schema selection and compatibility

Exported the full SDL from our explicitly selected detached server-main
checkout27455a482. Used the installed tsx exporter directly (same graphql/tsx
constraints) after pnpm's wrapper failed; write-if-produced retained the old
snapshot on failure. Schema/generated incidental updates are comments and
new unused provider types/root fields, not existing command input/output
changes. Wrapped-operation inventory also records newly visible server2484
App-memory write grants and server2517 edge-removal audit as separate CLI
parity; their ownership was reported to Petra. They are outside C1b.

This is a new query/command; older servers may reject it. Existing commands
and documents gain no new argument or field. No compatibility fallback
converts an unsupported read to empty provenance.

## Author evidence and remaining seats

Red-first3111cd5 fails on main for the missing command. Focused/race pins cover
exact revision/ref, nullable source state, zero counts limit, absent result,
invalid zero-call inputs, every offset, late errors, changing metadata and
nonprogress with no partial output. Full Go/lint/build, generation, inventories
and independent compiled-CLI QA/review are required before readiness. Author
synthetic transport is not new live authorization/history-gate evidence.
