# Display server-derived memory grant effectiveness

Issue #1945 implements the CHECKED CLI companion to server #4022/#4031.
The schema snapshot is exported from pinned server main merge
f9429dc19a8f9a19fcc3f933e9ab515d0ae0db4a. Its only snapshot delta is the two
nullable fields on AppMemoryWriteGrant: notEffective:Boolean and
notEffectiveReason:String. Generated input structs are unchanged.

The shared AppMemoryGrantFields fragment selects both fields for list,
instructed list, add, rm and instructed rm. The explicit CLI DTO passes
both through without omitempty; false and null remain distinct. The CLI
never derives eligibility, guesses a reason or adds an access check/read.
Server concealment returns memoryUrn=null, notEffective=null and reason=null.
The requested synthetic true/null response is also supported without an
invented reason. This schema-vs-issue wording distinction was reported to
Marco; merged schema wins per the dispatch.

List text appends [not-effective: <reason>] to the memory cell only when
the mark is true; true/null uses [not-effective]. Detail readbacks append
a not-effective line, with reason if supplied. All server strings in text
are sanitized through SafeCell. Unknown reason strings remain displayable.
False/null marks add nothing: effective-only list text is byte-identical
to the prior output. Revoked filtering, argument forwarding, response order,
confirmation and mutation semantics are unchanged. Unrevoked marked rows
remain listed until a manager revokes them; server-derived marks can also
clear if targets qualify again.

Compatibility: these existing operations now require the server fields from
#4031; older schemas return the ordinary schema-skew error (exit 2), never
an invented effective result. Each command sends the same single operation
as before, adding two scalar response selections and no request fanout.
No bytes/latency benchmark or live permission claim is made.

Red-first 4a188020 pins all three command surfaces in text/JSON, requested
fields on the actual wire, true/reason, true/null, null/null, false/null,
unknown reasons and the prior effective list text. Further tests pin server
order and omitted/explicit-false/true include-revoked forwarding. Existing
instructed-surface tests still exercise the shared fragment/DTO paths.

Spec FIT is inherited from #4022: V79 items1–3 apply (item3 concealment is an
acceptance pin). No new platform rule, permission, schema contract, mint or
Built-line change is authored here: the CLI renders the existing server rule.
The T1 gate requires distinct cross-family LOCAL and BLIND engineers plus
independent CLI QA; UX waived and security seat n/a. Author stops at ready.
