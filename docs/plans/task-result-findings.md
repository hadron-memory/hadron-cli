# Immutable task result findings

cli#1550 adds an opt-in finish input and a separate paged read over the existing
server#3248/#2662 contract. The provider chosen for stack QA is
8652fe902fdb5bf1a0ae47b475dda158cd2de96b (contains merge bcbac9e4).
The committed schema already includes the fields; this change adds generated
operations, not a hand-edited schema or server behavior.

## Write boundary

Without --findings-file the existing finish branch, operation, variables,
selection and DTO remain unchanged. With it, the input is read once through a
4 MiB+1 bounded reader before client creation. A lexical UTF-8/escape pass
rejects NUL and lone surrogate escapes before encoding/json can replace them.
A token decoder then checks duplicate and unknown keys, array/object shape,
field types, exact severity, code-point length and all-or-none unique ids.
Errors name categories without quoting supplied data.

The dedicated generated mutation uses a non-optional RawMessage variable.
Genqlient otherwise marks a non-null list variable omitempty and silently omits
an explicit empty slice. The message comes only from marshaling the validated
non-nil typed slice, so [] is present and the server assigns missing ids.

## Read and presentation boundary

The finish operation retains AgentTaskFields and aliases a second result
selection for findings(limit:20,offset:0). Shared task selections/DTOs are not
expanded. The new read selects only task id and its result findings page. Each
invocation makes one call; neither reads nor finish receipts drain pages.
Nullable generated page/item fields distinguish an omitted/null response from
legal zero/false/empty values. Missing/malformed receipts fail without retry;
finish uncertainty explicitly warns that state may have changed.

The opted-in DTO shadows only result to add findings and preserves warnings.
The read DTO distinguishes no result from an empty finished result. Human
rendering neutralizes terminal controls while JSON keeps original text. Page
continuation uses actual item count; count-only and zero-progress pages do not
invent a next offset. Severity never changes ordering, task outcome or exit.

## Verification boundaries

Author tests cover strict parsing, wire presence/value preservation, page/null
shapes, safe human versus raw JSON, source limits and output failure propagation.
Independent seats must additionally verify provider-assigned ids, immutable
repeat-finish refusals, reader/assignee/state boundaries and counts-only event
privacy on the one stamped fresh private stack. Synthetic tests are not claims
of live authorization, event privacy, deployment or release availability.
