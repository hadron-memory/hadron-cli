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
is omitted, retired leader is retained. Never infer unbound or withholding.
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
old snapshot. Incoming schema additions retained; generated differences beyond
the model selection are comments for clone refusal codes and sync-not-attempted.
No earliest/deployed provider revision inferred from these sources.
