# #684 Slice A: explicit spec-list role filter

Refs #684. Implements only Slice A under the [CHECKED gate](https://github.com/hadron-memory/hadron-cli/issues/684#issuecomment-6540694982) and Wolfgang's [source-current amendment](https://github.com/hadron-memory/hadron-cli/issues/684#issuecomment-6975717101). Authored-with/scaffold/lint work remains
outside this PR. `cor:api:240:01`: request provider decisions; no local role-family
matching and no new corpus law.

## Current default and explicit selection

Bare spec list retains its two paged legacy-tag/spec-role streams, deduplication,
status filters and old-role-provider fallback. The original gate's no-role-key
line was stale; deleting that second stream would lose untagged role-only specs.

An explicit --role value is forwarded unchanged to NodeFilter.role. Whitespace
is checked only to refuse a blank supplied value before any client/memory read;
nonblank values are not trimmed or normalized. The server owns role-family
matching. The explicit path has no tag stream or compatibility fallback: a
provider refusal must not broaden the query. All pages are read before the user's
limit/offset window. Unapproved retains NOT_APPROVED + SUPERSEDED provider scans,
with role, prefix and memory scope on both; the generic union only deduplicates
and sorts results and does not inspect role membership. Existing mutually
exclusive approval/mint flag combinations remain unchanged.

Rendering uses the existing DTO and annotations; JSON fields and null/array
shapes do not change. Role-only selection can omit legacy tag-only specs, as
help explains. No extra server/schema operation or generated code is needed.

## Evidence and limits

Red-first command tests pin every page's wire filters, verbatim opaque roles,
memory/prefix/status combinations, paged results before a display window, JSON
keys, blank-role zero requests and unsupported-provider no-fallback behavior.
Existing bare-list/union tests remain controls. Private-GOCACHE mutants probe
selection, empty-input refusal, no fallback, filter forwarding and result union.
QA is a separately assigned compiled-CLI fake-transport seat; fixtures do not
prove provider access boundaries or live server role matching. Scoped security
reviews the read scope and absence of client fallback at every head.

## Follow-ups from #2472's reviews (#2474)

- `--unapproved` reads two partitions (NOT_APPROVED, then SUPERSEDED). The second runs only after the first succeeded; a failure in either ends the command with no rows. Both are pinned.
- An older server whose node filter has no `role` field gets a refusal that says so (`this server does not support --role`), at the exit code the raw error had, with nothing listed and no fallback. The same validation error about any other field is not mistaken for it.
- The help says the value need not be a spec role: nodes that carry it are listed under the spec columns, spec or not (the provider owns matching; no local validation).
- With no `-m` and no `--prefix` the wire filter is the role alone.
- The pager's page count is not pinned by the role tests any more; which status streams ran still is.
