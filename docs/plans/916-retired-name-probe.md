> **Partly superseded by #1173:** literal Agent/App systemPrompt and Worker
> promptOverride reads, writes and flags described below are retired. Use the
> source → compile → node approval workflow; the other behavior below is historical
> context or remains applicable as stated. See [1173-retired-prompts.md](1173-retired-prompts.md).

# #916 C6: retire the obsolete name-placeholder probe

S12 server#2405 is merged; TeamRole.hasNamePlaceholder and
CastWorkerPreview.hasNamePlaceholder are deprecated, always null. S10 serving
is active per the coordinator's B4 receipt (Marco42968). C6 removes these two
selections from TeamRoleFields/castWorkerPreview, regenerates the client, and
removes the three obsolete human warnings that inferred a worker would be
nameless from that probe.

Stable public role/preview DTO keys stay present as `hasNamePlaceholder:null`,
including if an older/malformed provider sends an extra boolean no longer in
the query. Null is the retired probe, never a newly guessed true or false.
Other role fields, association-consistency warnings, cast preview prompt,
read/write operations, arguments, authorization and reserved:false stay intact.

This is not a broad {{name}}/{{role}} sweep. The legacy platform-variable
compatibility path, Worker.prompt/systemPrompt and cast-preview behavior are
expressly retained by #2405 and remain unchanged. No production prompt cleanup,
column deletion, unknown-variable policy or new law is part of the client cut.

Red first0579872: current role/preview queries still request the probe and emit
booleans. New tests assert actual query omission, stable nullable JSON and no
obsolete human advice for role list/get/cast preview, with zero mutations.
Existing dry-run contract changes only the retired field to null. Metadata1112
snapshots update those booleans and drop the removed warning line; the two
safe-cells1101 role-list hashes update for those exact output changes; the one
removed role-template-role test site has no output site left. Active role
interpolation/sanitization remains covered by role-missing-role and the reverse
association cases, not weakened or skipped.

Independent reviews/QA remain required. Author uses fake transport and strict
snapshot checks, not production or a new live stack. Refs916, leaves umbrella
open. No release/activation/install action.
