# Agent clone S1 CLI (#994)

`hadron agent clone <source> --to <org> [--name N] [--slug S]` makes exactly
one typed `cloneAgent` mutation. It does not look up the source, ask S2
readiness, install the result, subscribe through a second CLI step, or retry.
The server owns eligibility, the transactional copy and dependency licensing.
CHECKED: issue#994 comment6355920908; final smaller attach6355797959.

## Inputs and returned values

Exact one nonblank source and a nonblank organization target are required
before client creation. Refs are forwarded as written; the server resolves
IDs/URNs and decides whether the target is an organization. Optional name/slug
are omitted unless their flags were supplied; explicit empty strings are sent
as such for the server's existing defaulting. No client slug derivation.

The mutation selects existing `AgentFields`. Text is the returned NEW Agent
URN plus a newline. JSON uses the shared explicit `agentDTO`, including empty
surfaces as `[]`. No origin field, null placeholder or source-argument echo:
S1 stores origin internally but does not expose it. Server#2281 and a separate
client cut own origin and expanded exclusion teaching. Current help only
claims S1's PERSONAL draft, system-memory copy, links/imports and source/target
eligibility. Linked memories remain links. It does not promise that target
dependency licenses are never provisioned or subscribed.

## Refusals and uncertainty

Only this operation wraps errors with `MapCloneAgentError`. It keeps normal
transport/schema mapping and original GraphQL error chains. Known source or
license permission refusals use8, encrypted/unsupported-target input uses2;
not-found4, conflict/taken5 and unknown fallback1 retain existing categories.

The root renderer adds details ONLY for the operation marker. The closed
projection is code/reason/retryable plus blocker code/count and subject
position/optional supplied name. Unknown extension keys and malformed nested
values are discarded; order is kept, and absent names stay absent. No extra
read enriches redacted subjects. Server messages are kept, including multiple
messages. JSON uses the existing error envelope plus these extensions; text
adds a JSON-formatted detail suffix. Other operations' errors are unchanged.

A source-changed `retryable:true` is metadata, not a retry loop. A lost response
is exit7, explicitly an unknown clone outcome, retaining the normal instruction
to verify before retrying. The mutation may already have committed. There is no
manual create+copy fallback for old providers: schema validation failure names
version skew and returns2. An output-write failure after success also cannot
roll back the clone and does not initiate another mutation.

## Schema source and incidental compatibility

The canonical SDL was exported by the repository exporter from an isolated
checkout of merged provider c8da96c1393bc11170eb7ce83c94533dbe5dfbe1, explicitly
selected through HADRON_SERVER_DIR. The export recipe strips trailing horizontal whitespace (`sed "s/[[:blank:]]*$//"`) before snapshotting; the upstream descriptions contain three whitespace-only lines. Generated client code is never hand edited.
The full refresh includes already-merged non-clone fields/descriptions (boot
pointers, includes and task changes among them). Those are schema inventory,
not client feature claims.

Provider#2044 makes CreateNodeInput.memoryId optional beside a new install
alternative. Existing CLI node writers always pass memoryId: a genqlient
pointer:false annotation retains their existing string type and install is
omitted when unused. The create→update import adapter carries that new shared
field, as its existing reflection completeness test requires; no install flag
is added. Dedicated role-clear operations retain their older documents so old
providers still accept ordinary clears. They refuse a nonnil install input
locally instead of silently dropping it; a negative no-call test pins that
boundary. Their existing callers leave it nil. No ordinary node writer was
rerouted to a different mutation.

## Evidence and limits

Red-first command tests committed before implementation cover new command,
input variables and refusals. Tests pin empty/absent options, returned DTO/URN,
absent origin, exactly one operation, actual root exit/rendering, redacted
subjects, blocker order, unknown-extension negatives, source-changed no retry,
schema skew and a dropped connection with an atomic call counter. API tests
exercise scope and malformed values. Existing CRUD/root/node-role tests protect
incidental paths. Named independent review/compiled synthetic QA receipts are
required at the frozen green head; author checks do not transfer their seats.

No real provider clone, production write, server stack, host action, release,
installation, S2 readiness or new platform rule is part of this PR. New command
requires a provider exposing S1; the refresh does not make it installed.
