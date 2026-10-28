# cli#916 C1: compile boot prompts

Refs #916. This cut wraps the three merged GraphQL compile mutations from
server main (provider source inspected at 59ec40579). It leaves the umbrella
open; C1b provenance, C3b retention, C5 skill lint and C6 activation cleanup
remain separate. No source selector, client compiler, approval or serving
operation is added. No spec is minted: this implements the existing server
contract and P10 dispatch, without deciding a new cross-repository rule.

The three commands are agent compile, app agent compile and team worker
compile. The manager acts as themselves, without X-Hadron-Session. Worker
names resolve through the selected App using the existing worker resolver;
Agent and App refs go to the server (App refs use the existing canonicalizer).

A shared command package holds the explicit JSON DTO and output/error policy.
The generated operations select only node.urn, revNo, contentHash, pointerSet
and approvalIssued. Optional setPointer is omitted by default and false with
--no-set-pointer. App-Agent has no such input or flag. No client source or
output-address inference occurs, and every compile sends exactly one mutation.

The result's approvalIssued is an action receipt, not an approval-status read.
Human output sends the reader to node get for status. Compile failures retain
the server's concealed messages and use literal compile refusal classifications;
conflict and transport outcomes have bounded verification advice. No retry is
performed. Transport recovery advice does not imply a rollback.

Validation: command-level bound-worktree fixtures pin all three request/DTO
shapes, absent session header, pointer omission/false, App flag refusal, typed
refusals and one mutation, plus an HTTP 502 unknown-outcome control. Independent
review and compiled-CLI real-server QA are still required by the T2+§10 gate.


The worker name resolver reports the returned canonical worker reference and
which App-context branch it used in human output, before submitting. JSON
remains the five-field compile receipt. Names use --app, configured context
or the worktree binding; a fully-qualified worker URN is App-independent.
Pre-mutation failures (argument validation, credentials, App normalization,
worker lookup or output failure) send no compile mutation. A mutation failure
or missing result never triggers a second submission. Output failure after a
successful mutation cannot roll it back; callers must verify before retrying.
