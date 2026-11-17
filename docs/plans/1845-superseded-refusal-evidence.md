# Superseded-session evidence (#1845)

Implements the decided issue body and CHECKED6830171560 / FIT6830124522.
The server contract is already in the schema snapshot; no schema or operation
change is needed. Fresh main had no implementation under Refs: root rendering
did not preserve the current pair or print either decided hint.

The root curated error envelope adds sessionId and startedAt only for a
definitive SESSION_SUPERSEDED error carrying both strings, a nonempty ID, and
a valid RFC3339 time. Values come from the SAME matching error, never joined
across errors. Other extensions are not copied. JSON retains exact strings;
human output uses the shared terminal-safe renderer. Numeric exit and JSON
message are unchanged. serverCode uses the existing field convention;
this named code is now explicitly preserved on curated errors that otherwise
did not carry a server-code wrapper.

The definitive helper requires an error list containing only that typed code
and excludes FromNon200Envelope (5xx). Code-only/partial/invalid pairs use the
same generic hint. A typed gateway error remains uncertain, with the prior
message/category/exit, no pair and no definitive hint. SESSION_SUPERSEDED is
the final serverCode writer, after internal-fault and generic wrapper codes:
the specific marker wins in either envelope order, while the fault's message,
numeric exit and requestId are unchanged (Wolfgang ruling111707).
Ended/expired paths and whoami --check are unchanged.

The first parsed error is indexed only after a local nonempty/nonnil guard.
It is independent of OnlyErrorCodes's invariant: if a custom Error.As changes
its list between reads, the helper returns the normal non-definitive path.

No successor fetch, historical query, retry or rebind is added. This is
display of server-read-gated evidence, not an access/liveness policy.

## Evidence

Red b3575d37 fails all current-evidence hints and curated serverCode projection;
the 5xx and ordinary-ended/expired controls pass. The initial draft used node
get, whose current implementation makes two prerequisite reads; that harness
was corrected to the existing direct worker-ref read before the red commit.
Only the later actual missing-output failures count as red evidence.

Status-aware loopback tests execute the root command and final renderer with
HTTP409 (and200 controls),502/503; one GetWorker request only, binding file
bytes unchanged. Cases cover currentC versus historicalB, omitted/no-open,
partial/wrong types/invalid time, extension allowlist, raw JSON escaping and
terminal controls. API tests prove mixed error lists do not become definitive
and pairs cannot be assembled from separate errors.

Independent compiled-binary QA remains required. No real worker binding,
production mutation, deployment or server access probe is authorized here.
Spec Built-line follow-through belongs to Victor after merge; no new spec or
citation is introduced.


## Review fix

Blocking F1: real status-aware502/503 mixed envelopes in both error orders
fail on the reviewed head because INTERNAL_SERVER_ERROR replaces the marker.
The fix pins SESSION_SUPERSEDED priority and unchanged fault message/id/exit,
with no current-session fields or definitive hint.

Blocking F2: ordinary empty/nil lists already followed the normal path under
OnlyErrorCodes. An adversarial legal Error.As that returns a superseded list
then an empty list reproduces the old helper index panic; the local guard
removes that dependency. This is fault injection, not a production-transport
panic reproduction. Both cases are pinned.
