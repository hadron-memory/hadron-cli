# Effective boot prompt display

`app agent list` and `team worker get` show the decision a **new bind would get
now**. A worker's current session may have received a different revision.
The server owns WORKER → APP_AGENT → AGENT precedence and refusal decisions.
A pointer alone proves neither approval nor the revision that would be served.

## Read and output boundaries

The listing opts into `AppAgentEffectiveRoster`; role warnings keep
`AppAgentRoster`. The legacy `AppAgentInstallationRoster` stays unchanged.
Only worker get adds `GetWorkerBootPromptServe`; shared `WorkerFields`, casts,
updates, rosters and other commands keep their selections and provider floors.
No compiled-content, node or Agent lookup is added. The preview selects only served pointerLevel/compiledNodeId/servedRevSeq and
refusal. Worker get also selects the own pointer/deleted cue in that same
response, avoiding association with its earlier worker lookup. This is not a
transactional snapshot guarantee across resolver fields.

Existing JSON keys are retained. Four nullable keys are added: `pointerLevel`,
`compiledNodeId`, `servedRevSeq`, `bootPromptRefusal`. Optional `bootPromptUrn`
comes only from the target's own visible pointer selected in the decision
read when the served level matches APP_AGENT or WORKER. No inherited URN is constructed or
looked up. Agent-level inherited decisions still show their returned id and
revision, without implying permission to read compiled content.

Human text labels the next-bind result. Only typed `BOOT_PROMPT_NOT_SET` means
none. A null preview means not visible; inconsistent or value-level malformed decisions
stay unknown and supply no served fields. Structurally invalid JSON/typed wire
values fail through the existing error mapping with no output or fallback. Other typed refusals carry no level.
The existing visible own deleted-pointer cue remains, labelled as that own
pointer, without claiming that an inherited refusal came from it.

## Compatibility

A wrapper captures data before typed unmarshalling. Only an initial structured
GRAPHQL_VALIDATION_FAILED for missing bootPromptServe on the exact AppAgent or
Worker type/operation can use the legacy path once. The exact checked message
may include graphql-js's single bootPromptRef suggestion. HTTP 200 and 400
accept absent or null data equally; every nonnull data shape excludes fallback. Auth, transport/5xx,
partial data, unrelated/mixed errors, paths and other message shapes fail via
the existing error mapping. An opt-in API observer records whether the original
error list contained a null entry before ordinary sanitization; that disqualifies
fallback even if the remaining errors match. It stores no messages/data and
changes neither normal sanitization nor error mapping. Structurally malformed
error collections fail decoding or the exact predicate. No retries or later-error fallback exist.

The listing's legacy path performs one legacy installation read. Worker get
has already obtained its ordinary worker row; an absent preview capability uses
that row's existing label without repeating it. Other worker ancillary reads
retain their own existing compatibility policies.

The named schema/source is hadron-server
`d5f71aa0f6e533861c2c0a610f98577ca89ce8d3`, containing #2708 and #3237.
Refresh and generation produce no SDL change from the starting CLI snapshot.
That source pin is neither a deployed SHA nor an earliest-version guarantee.

`bootPromptServe` was introduced at server `de7cb1690b`; the listing already
requires later `installMethod`/`importer` (`9cc235f671`). Therefore this does not
promise generic old-stock listing success. The single-missing-capability
fallback fixture is explicitly synthetic. A real pre-#2708 source validation
is a mixed-schema negative, and must not fall back. No legacy selection is
downgraded to disguise an existing floor.

## Source-checked fixture

`internal/cmd/testdata/boot_preview_pre2708_1655.json` is derived by graphql-js
validation of the generated AppAgentEffectiveRoster document against SDL
exported from `eb05c29a2c06fa331ec5815d2721e0ef28e221fc` (parent of the field
implementation). It is not a captured network response. Messages come from
validation; GRAPHQL_VALIDATION_FAILED extensions are the test envelope policy.
Locations are not retained. The three errors name installMethod, importer and
bootPromptServe, the latter suggesting bootPromptRef.

- SDL SHA256: `ab387ac9c071efed345767b0a5502f31567f89780474701e95070601710cf29a`
- operation SHA256: `01fbb1d6090baaaa9cf3f19b8ea7366e09e0d175d24152e373e4cc0a5bf2ce77`

## Access and governance

The client does not replace target gates with a manager test. Install exposure
is the App-participant gate (including participants who are not managers).
Worker exposure uses the worker read gate. Denied projections stay null, with
no alternate lookup. Real permission controls require independent compiled-CLI
QA on one private fresh stack at the named provider; fake inputs do not prove
server permissions.

Serving order and deleted-target no-fallthrough are Decided cor:agt:040:07.
Pointer visibility and preview semantics in cor:agt:040:07/:10 are Proposed,
as identified by the corrected issue scope; this implementation does not mint
or promote them. The participant/inherited compiled-id projection is existing
provider behavior, not a new content grant. JSON and compatibility policies are
CLI engineering policy. No platform spec/Built-line change is required.

## Verification and limits

Red commit c7e47eb5 pins inherited/install/worker display, null/refusal behavior,
additive JSON, no extra lookup and exact fallback boundaries before the fix.
Focused race tests, golden projection JSON/labels and legacy negative controls
are author evidence. Production guard mutants, generation/schema, lint/build,
help inventory, coverage matrix and hosted CI complete the author checks.
Independent cross-family LOCAL and FULL BLIND, scoped security at every head,
and one qualifying compiled-CLI fresh-stack QA remain required before ready.
No production probe or worker-session bind/end is part of this change.


Review-fix red pins are 8a3ee3bf (worker own-pointer/cue and projection/status
boundaries) and 7c06e918 (null-plus-exact original error envelopes through the
configured client). The modern provenance cases are characterization controls,
not claimed red failures. Direct modern ROW/DERIVED/importer/id-join/withheld
cases, own URN/deleted cues, HTTP200/400 request counts, malformed-wire failures,
and ordinary API mapping parity cover the necessary fix. Mutation packets retain
both killed runs and the initial weaker null-data mutation that survived; the
replacement restoring the original predicate is killed. Equivalent revision
comparison and repeated mutations are not counted as new guards.
