# Paged spec-reference reads

`spec backlinks`, `spec unresolved`, and draft `spec lint` reference enrichment
consume the existing `specBacklinksPage` / `specUnresolvedReferencesPage` fields.
The typed operations select the existing item projections plus `total` and
`hasMore`. Required item fields and page metadata use pointers so absence cannot become
empty strings or zero/false. Legacy selections get the same presence bindings
without changing their static operation documents.
The committed schema already has these fields; no snapshot refresh is needed.

Each request explicitly asks for 20 rows ([cor:api:120:04](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:specs:cor:api:120:04)). The shared injected
collector advances offset by rows actually served, including short pages,
and stops only when `hasMore` is false and the consumed count equals total.
It preserves provider order and multiplicity, including identical rows across
page boundaries. Nothing renders or becomes a lint finding until the drain
succeeds. Missing/null page, items, metadata, item or required item field; negative/changing total;
excess rows; empty progress with hasMore; and contradictory terminal counts
are failed reads (exit 1), never empty or partial success. Server and transport
errors retain their normal mapped exits, taking precedence over malformed
partial data beside that error. An ORIGINAL null member in the errors array
is a separate malformed-envelope case: it takes precedence over a valid typed
sibling (including FORBIDDEN/NOT_FOUND) and remains exit 1 at both the page and
legacy stages. No partial payload is rendered. Malformed page diagnostics name
a spec-reference page; malformed legacy diagnostics name a legacy spec-reference
list. Their exits, compatibility eligibility and refusal handling are unchanged. Lint inScope and strict promotion
happen after the whole read; successful DTO/text output is unchanged.

Only a first-request exact parsed unknown Query-field validation rejection can
select the full legacy twin once. Mixed/permission errors, resolver paths,
unrelated validation, malformed data/errors, 5xx and plain-text lookalikes do
not qualify; an accepted page prevents fallback. A populated result beside
errors is not an unsupported field. A null entry removed by the existing API
sanitizer is observed and fails closed. Only an operation-provenance marker from an actually attempted legacy call
can classify that legacy unresolved report as unsupported. If it is
genuinely unsupported, draft lint retains its prior ordinary-rules-only
compatibility, without claiming that references were checked. The legacy
operations remain selected only for this compatibility path; their removal is
tied to retirement of the provider's legacy fields.

Provider source inspected at [hadron-server 93e69d2083bcaa1c27635681ef2835c771da7478](https://github.com/hadron-memory/hadron-server/blob/93e69d2083bcaa1c27635681ef2835c771da7478/src/api/graphql/schema/resolvers.specCorpus.ts):
`src/api/graphql/schema/resolvers.specCorpus.ts` page twins call the same list
builders/gates as their old fields and slice with pageOfReferences. The page
capability is observed at merged [#3226](https://github.com/hadron-memory/hadron-server/pull/3226) / [455366f1d3ff943579e2f365b65287291dbff95c](https://github.com/hadron-memory/hadron-server/commit/455366f1d3ff943579e2f365b65287291dbff95c);
this is source evidence, not an earliest released/deployed floor. Existing
provider tests cover unchanged-list traversal, order, identical rows and gates.
Independent provider QA at reviewed 5c4fec8130a40f966db7c71214bcbc8f67e6447c
([PR #3226 QA receipt](https://github.com/hadron-memory/hadron-server/pull/3226#issuecomment-6569922213);
[full Cameron evidence](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-dev-team-shared:sprints:sprint-2:qa:hadron-server-3226:cameron-5c4fec813) read) observed exact
total/hasMore on 260 backlinks and 315 unresolved rows and byte/order/multiplicity
parity with the legacy fields. Its fresh private-stack component ran Sonnet,
215 assertions / 606 HTTP requests; encrypted/alias/foreign-edge and concurrency
limits remain. This is reused historical provider evidence, not Jane execution.
At that inspected source 93e69d20, the two page resolver bodies and the existing provider
pin file are unchanged; specBacklinksOf, specUnresolvedReferencesOf,
loadScannableCorpus, pageOfReferences, pageArgs and pageEnvelope function bodies
are SHA256-identical. The file's other changes are mutation authoring metadata;
this map does not recertify every transitive auth dependency or a deployment.
No new provider execution is claimed here. [FIT6684916415](https://github.com/hadron-memory/hadron-cli/issues/1554#issuecomment-6684916415) finds this consumption of
existing law compliant; no new policy or spec mint is introduced.

Each page still scans the whole corpus. For an unchanged corpus and a provider
serving 20, reference requests are max(1, ceil(N/20)), without a count preflight
or writes. There is no revision token or cross-request snapshot. Total checks
catch some changes, not same-count edits, nor a provider that repeats a page
and falsely declares a consistent terminal count. No throughput guarantee.

Author fixtures use synthetic HTTP and pure collector tests: all three callers
read 205 duplicate rows under a short-page clamp, both output modes reject
non-first-page failures, metadata faults and exact-fallback negatives, and
later-page lint scope/strict behavior is pinned. No production or live corpus
is read as a test; #1554's independent QA was the stamped compiled stateful-fake leg.
The #1768 cleanup adds a compiled CLI QA pass for its bounded diagnostic wording;
it refreshes no historical provider applicability.

The #1768 follow-up selects backlinks versus unresolved explicitly at each
caller; an empty citation never dispatches an unresolved query. Public backlinks
still validates its citation before client creation. Shared fixture helpers and
first-page negative tests cover all three consumers; the missing-extension-code
unknown-field compatibility case remains accepted. The first nil-member guard
precedes errors.As; the redundant second nil check is removed only after that
precondition. No legacy field is retired here (#1766), and no provider evidence
or deployment floor is refreshed by this adapter/test/documentation cleanup.
