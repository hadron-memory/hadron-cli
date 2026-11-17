# PR protocol CLI — design as built (#1664)

Six thin `team pr` commands consume merged server#3448/3452 at 06f4b59c54a8e63b50c04d3f08c9d5d610acc25e.
The server alone owns authority, event facts, freeze/negative-seat rules and
readiness; there is no property-mirror read or local matching/merge policy.

The five writes select only task id/handle/status and the shared explicit
ActionAnswerDTO. Readiness selects ready/head plus each slot's currentTaskId,
state/verdict/head/counts; its null response is concealed not-found. JSON shapes
are pinned by command tests; nulls and empty lists retain their distinct shapes.
No command infers a head. Local SHA/slot/verdict validation precedes the client.
Slots follow provider colon-separated ASCII atoms and its 64-character total cap.

Optional metadata/supersedes variables use omitempty, never explicit null for
absence. Task references go through the existing server resolver, with no local
lookup. Writes are added to authoredWriteField: both owner-chain and assignee
gates admit workers, so verified per-call session attribution is required.
The query stays headerless like task get; it may read via an App key.

Protocol refusals preserve provider message/code. PR_NOT_READY's typed failing
slots are projected into the root JSON error's slots and rendered safely in text;
no unrelated extensions or raw error enter its chain. Protocol state conflicts
exit 5. A malformed/missing result never prints a successful write receipt.
One protocol request per command, no retry; inspect events after unknown writes.

The required full schema refresh also introduces unrelated authoredWith inputs
and schedule-preview types. They are not implemented here: coverage gaps point
to existing CLI#684 and#1678. New authoredWith generated fields omit nil, so
legacy commands do not start sending null. All six reached protocol fields are
classified as commands; no generator expansion or silent exclusion.

Red-first 50becc55 pinned missing six commands, zero-call usage failures,
per-command JSON/request shapes and named refusal envelopes. Expanded tests pin
present/omitted metadata, task refs, four action states, non-null task locations,
SafeCell text, readiness null/empty/failing slots and failing-slot errors. Required
schema check targets the same detached named server, never a shared checkout.

Independent LOCAL, supported isolated BLIND and compiled fake-transport QA are
required at one green head (T2, no security/UX seat by stamp). Synthetic author
checks are not real server/DB/concurrency/authorization proof. The snapshot SHA
is source compatibility evidence, not a deployed-provider floor.

Refresh carry (Xanthos78216): the reflective clear-role fixture acknowledges
unsupported attestation without changing its compatibility documents; six nil
input omission pins keep old requests unchanged, and create-to-update forwards
authoredWith. The generator enumerates22 authoredWith argument gaps under #684
(including16 nested task/revision leaves); previewAgentSchedule remains #1678.

Acceptance5 uses the server state/verdict/head/counts, not a separate reason
field (Patrick78210, Xanthos78216). PR_NOT_READY renders those same typed facts.
The compiled loopback smoke covers12 success projections, two typed refusals
and one zero-request validation failure. Actual middleware tests cover all six
commands with bound/unbound synthetic bindings; only writes carry the session.

LOCAL fix: readiness and failing-slot contracts now assert complete JSON keys,
values and nulls. Non-protocol refusals use the existing terminal-safe task
mapper with a retained JSON serverCode; typed protocol conflicts keep their
separate slot projection. Red-first52bb1210 exposes raw control bytes/missing
text code;12 applied field-tag/mapper mutants are assertion-killed. Earlier
author evidence established exits/JSON codes, not TASK human-code safety; that
limit is corrected here. Optional validation/SafeCell/null-slot pins are folded
into this necessary fix head; no new refusal policy or server change.
