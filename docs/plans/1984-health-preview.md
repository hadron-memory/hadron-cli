# Monitoring wake reads are previews

Issue #1984; CHECKED 6851769160, FIT 6851534446. Task AT0.7231 was initially
misnumbered #1833; Eden corrected it in team chat104968. This fixes only the
DueTickWakes read used by orca terminal status and its team health sweep caller.

## Change

The generated operation sends literal preview:true on every page. No flag,
variable, selection, DTO, acknowledgement, caller scope or auth path changes.
The host delivery DueWakes query still spends; TeamDueWakes and its prospective
public --preview flag remain #1833. The non-OK status guard remains fail-closed.

The server's preview clauses are Proposed in cor:agt:080:15/:22, implemented
since server#3755. The CLI follows that existing contract rather than deciding
a new one. Old servers that reject preview fail closed; retrying without it
would restore the defect, so there is no spending fallback.

## Evidence and limits

Separate red test commit2aedb274 fails on main1d27c47b with
"monitoring request 1 omitted literal preview:true". The recording HTTP fixture
parses the actual operation, exercises two pages/two workers against a one-slot
budget model, and asserts zero reservations, withheld wakes, notices and
acknowledgements. This is a synthetic model of the documented server semantics,
not proof of server-side persistence. Existing non-OK and cursor tests remain.
A separate operation pin protects the delivering host poller's spending read.

Expected request count is unchanged: one DueTickWakes per page, page size200,
with the same workerRefs and cursor. For100 workers fitting one page this is
one due-wake request, plus the existing worker/ticker/subscriber reads. Response
fields and per-row payload are unchanged; preview can return more rows because
it withholds none, still bounded by paging. QA records bytes and wall time for
a realistic fixture. No large prompt/body fields are selected.

The coverage scanner unions argument reach across operation callers. Its
Arguments gap for preview therefore disappears; its header now explicitly
identifies DueTickWakes as the internal binder and preserves #1833's distinct
public capability gap. This is no claim that team wakes due gained a flag.

No production host, real budget, live stack, acknowledgement or expiry test.
Independent QA must exercise the compiled CLI. Docs#733/#734 already own the
related warnings; none is duplicated. Vera owns the two spec Built-line
follow-throughs after merge. Finish the parent after verified merge.

## Review delta

A separate red-first test commit pins the older-server rejection path for
GraphQL HTTP 200 and HTTP 400. Both return an internal Usage (2) error,
one request, no fallback and no successful ticks. Terminal status keeps its
existing process exit behavior and classifies idle terminals locked/unrecognized.
Text shows the actionable cause; JSON keeps detail local and omits it.
Tests exercise both render paths through the real wire read. The monitoring-specific hint names dueWakes(preview),
hadron-server#3755 and upgrading the server. Other errors still use api.MapError.
This adds no capability probe or spending fallback and sets no global CLI
server-version floor. Xenophon confirmed this serves the gate in chat #105024;
server#3755 is merged at ee9b31f67d3d99c2240a7463c8a97023277d6052.

QA found an older warning in status --help and the start of the agent contract
that still described monitoring reservations/notices. Both copies now describe
preview and fail-closed compatibility, and the old help pin now tests the
corrected behavior. Help inventory follows.
