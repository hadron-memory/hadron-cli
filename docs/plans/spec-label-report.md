# Spec parent-edge label inventory (#462)

`spec label-report [-m MEMORY] [--prefix CITATION] [--json]` inventories visible
citation-parent labels for editorial review. It never judges whether different
wording is stale, reads history, rewrites edges, or adds lint findings. Lint is
unchanged. Unequal editorial labels are legitimate.

The existing selected-spec-memory resolution applies. Prefix selects the parent
branch (boundary-aware), and both legacy-tag and spec-role listing streams page
to empty before deduplication. Only syntactically valid citations enter the
report. Full reads use CollectNodeBatch's cap and byte spillover; only IDs from
the visible listing are requested. Protocol errors, missing listing envelopes,
foreign/moved batch nodes and transport failures fail before report output.

An incoming source proves a direct relationship only if its citation's Parent()
is the visible parent's loc in the same memory. Other readable edges are excluded.
No source is derived from edge loc, name or URN.

The DTO is `{memoryRef,prefix,items:[]}`. Every item has exactly
`{parentId,parentLoc,edgeId,label,childId,childLoc,childName,status}`:

- available: visible, independently batch-readable child; current name and label
  are side by side. No lexical quality judgment or lint exit follows.
- unavailable with edgeId present and childId null: **unclassified candidate:
  source unavailable**. This visible incoming edge cannot be counted as a verified
  direct relationship. Child loc/name are also null.
- unavailable with edgeId null: **unavailable-parent** marker, once per listed
  parent that the batch explicitly marks unavailable. Label and child fields are
  null; it does not invent an edge. Explicit unavailable child reads may also
  produce this marker independently of an edge row on a readable parent.
- unavailable with visible childId/loc: the current child name is unavailable;
  no concealed endpoint recovery query is made.

Null remains distinct from an empty string. Text quotes values and uses these
same facts and uncertainty labels. Items sort by parent citation then edge ID.
Only available rows count as verified available relationships. Visibility is not
proof of whole hidden-corpus coverage. Report completion exits zero even when
unavailable rows or unequal labels exist; ordinary failures keep their error exit.

Gate: 6368862092, clarification 6369356721. No schema/provider/law change, live
corpus edit, stack or mint. Independent compiled fake-HTTP QA is the stamped mode.
