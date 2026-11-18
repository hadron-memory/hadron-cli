# #684 Slice A: explicit spec-list role filter

Refs #684. Implements only Slice A under CHECKED 6540694982 and Wolfgang’s
source-current amendment 6975717101. Authored-with/scaffold/lint work remains
outside this PR. cor:api:240:01: request provider decisions; no local role-family
matching and no new corpus law.

## Current default and explicit selection

Bare spec list retains its two paged legacy-tag/spec-role streams, deduplication,
status filters and old-role-provider fallback. The original gate’s no-role-key
line was stale; deleting that second stream would lose untagged role-only specs.

An explicit --role value is forwarded unchanged to NodeFilter.role. Whitespace
is checked only to refuse a blank supplied value before any client/memory read;
nonblank values are not trimmed or normalized. The server owns role-family
matching. The explicit path has no tag stream or compatibility fallback: a
provider refusal must not broaden the query. All pages are read before the user’s
limit/offset window. Unapproved retains NOT_APPROVED + SUPERSEDED provider scans,
with role, prefix and memory scope on both; the generic union only deduplicates
and sorts results and does not inspect role membership. Existing mutually
exclusive approval/mint flag combinations remain unchanged.

Rendering uses the existing DTO and annotations; JSON fields and null/array
shapes do not change. Role-only selection can omit legacy tag-only specs, as
help explains. No extra server/schema operation or generated code is needed.

## Evidence and limits

Red-first command tests pin every page’s wire filters, verbatim opaque roles,
memory/prefix/status combinations, paged results before a display window, JSON
keys, blank-role zero requests and unsupported-provider no-fallback behavior.
Existing bare-list/union tests remain controls. Private-GOCACHE mutants probe
selection, empty-input refusal, no fallback, filter forwarding and result union.
QA is a separately assigned compiled-CLI fake-transport seat; fixtures do not
prove provider access boundaries or live server role matching. Scoped security
reviews the read scope and absence of client fallback at every head.
