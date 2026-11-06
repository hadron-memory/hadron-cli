# Worker terminal title (cli#1738)

Read-only presentation command, implementing the folded issue and COMPLIANT
FIT 6684553278 / CHECKED T1 gate. No new product contract or spec mint:
cor:agt:060:04 governs family lifecycle; Worker.model is session-reported,
nullable/advisory SDL, never authorization. cor:api:240:01 permits local rendering.

One shared GetWorker/GetWorkerInApp query, same reference and retired-worker
resolution as worker get. Add model to WorkerFields; no extra profile/terminal/
rendered-prompt/model-group calls. No host or terminal calls, writes or refresh.
The returned title is not a parseable identity.

Explicit command DTOs retain nullable raw model/family/role/reporting and the
server-returned worker/leader references. Source classification follows usable
scrubbed model, then family, then none. Null role falls back honestly, null leader
or scrub-empty leader is omitted, retired leader is retained. A scrub-empty
worker name becomes `worker unavailable`, with raw identity retained in JSON. Never infer unbound or withholding.
Each element uses output.SafeLine before replacing delimiters and collapsing
whitespace. Render first, then cap at 256 runes with an ellipsis and closing ].
JSON raw parts are not scrubbed. Existing worker DTOs have no added JSON key.

Red commit pins missing command; fake-server matrix covers reported/family/none,
blank/control-only fallback, masked/retired workers, retired/null leader,
unsafe Unicode/delimiters, long multi-byte title, JSON keysets/raw nulls,
refusals/no partial output, id/URN/name one-query resolution, gateway failure,
and unchanged worker-get text. Normal generation/schema/full-suite/lint and
independent compiled-fake QA are required; no live or host claim.

Schema refreshed from explicit server main d5f71aa0f6; existing model already in
old snapshot. Incoming SDL additions are LocSegment/LocSegmentsPage and
memoryLocSegments (#3456; CLI companion gap #1744); SDL comment changes cover
clone refusal spelling, sync-not-attempted, sendChatMessage (#3501) and
updateChatTitle (cor:dmo:060:04/16). Generated differences beyond the model
selection are only comments for clone refusal codes and sync-not-attempted.
No earliest/deployed provider revision inferred from these sources.

Compatibility evidence (source only, not deployment or a semver floor): model
was introduced in 25894376d1 (#2252), an ancestor of the existing required
teamPlacement introduction d523a06709 (#2468). That latter SDL already contains
model. Existing worker help already requires #2468 for all shared-fragment
commands. This does not claim compatibility for arbitrary forked providers.

Fix-head scope: scrub-empty worker name -> `worker unavailable`; scrub-empty
non-null leader -> omitted display element, with the raw leader object retained.
Red-first tests pin both failures; raw JSON, cap boundaries and worker-get JSON
parity are pinned. The cap counts Unicode runes, not grapheme clusters, and can
split a combining sequence as the original contract permits.
