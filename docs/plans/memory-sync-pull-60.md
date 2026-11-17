# Memory sync pull (cli#60 slice A v2)

Implements author attach 6627531391 and CHECKED 6669136019. The governing
contract is cor:int:010:01; encrypted Git sync refusal follows cor:sec:040:05
(R206). Ruling0168(c) excludes push: pushMemoryToGit remains gap #60. Slices B
and C are not part of this change.

`memory sync pull <memoryRef>` canonicalizes the explicit ref locally and asks
cmdutil.Confirm BEFORE making any request, including a lookup. syncMemory
accepts an ID or URN. Per corrective rulings6670029964/6670195918, confirmation
is followed by one narrow memory read (source + isEncrypted, internal only),
then at most one mutation. No source on a readable unencrypted row exits2 with no
mutation. Encrypted rows always reach the server, irrespective of source, so
the writer gate and R206 typed refusal are not replicated client-side. Both
fields are discarded without output/logging/persistence. A null read cannot
establish absence or authority and delegates to SyncMemory (6671180247);
preflight transport/errors still refuse without mutation. No pre-confirm read.
The prompt names this canonical reference (not a fetched display name). --yes
bypasses the prompt; no automatic retry or fallback is offered.

The mutation selects only syncStatus/syncError/lastSyncedAt/lastSyncReport.
The separate preflight selects only source/isEncrypted. A returned non-OK
status exits1/not-completed/clean:false before historical-report interpretation;
the report is unavailable at that door, not evidence this invocation synced.
The explicit command DTO reuses parseSyncReport and writeSyncBlock. The shared
parser rejects unknown vocabulary, including codes paired with the wrong kind.
A separate raw outcome-only read preserves exit 1 for a stored failed report
even when its vocabulary is unavailable; it does not certify that report. A clean result requires no report, a
nonempty last-sync timestamp, OK status and no sync error. Partial/unknown/
unavailable outcomes exit 0 with clean:false and a warning, as authorized by
the gate; failed outcomes exit 1. An absent mutation result exits 7.

Errors use api.MapError plus operation-local preservation of the server code.
Encrypted refusal is exit2, without a workaround. URL userinfo is masked in
both result syncError and mapped error messages; this is a bounded URL masking
claim, not a general arbitrary-secret scrubber. The server wrapper owns
credential containment (findings:git-execfile-error-carries-auth-url). Human
error messages use SafeLine and report entries use the existing quoted/capped
renderer. JSON keeps the shared report DTO.

Provider source verified at server3f3b13c5331840153920373d23716039ff7b5b2e:
resolvers.mutation.memory.ts syncMemory resolves refs, enforces CONTRIBUTOR,
and throws typed failures instead of returning a failed Memory. No provider
runtime or deployed-version claim. The committed snapshot already contains
all four selected fields and syncMemory(id:ID!); regeneration needs no refresh.

Validation is synthetic transport ONLY: no repository, GitHub or provider
mutation. Red commit6e0752f0 fails on main because the command is absent.
Tests cover clean/partial/unknown/unavailable/never-run reports, confirmation,
refusals, URL masking, variables, and counted one-request transport/no-answer
paths. Independent seats: LOCAL, isolated FULL BLIND, scoped §10 every head,
and compiled-binary fake-transport QA. UX waived by gate.

## LOCAL P2 fix cycle

The original attach assumed no source throws BAD_USER_INPUT. Provider source
shows no-source returns{} with no update, and missing installation returns{}
after stamping SYNCING. The preflight source gate plus post-mutation OK gate
close those no-op/stale-report shapes under the bounded gate ruling. A source
change between the read and mutation is outside this receipt; typed server
not-attempted/completion semantics are separate work routed by Ada81259. No
server change is part of this CLI PR.

Red eaa150f4: eight incomplete-status assertion failures. Red8f301bff: all six
source/encryption prerequisite shapes fail before the read exists. Fix folds
stored-response error masking, hostile-terminal stderr, clean-with-error,
stored-failed and unknown report-code pins. The previous clean predicate's
status check is now enforced before decoding reports; removing that earlier
guard is the meaningful mutant (a duplicate status term in clean would be
redundant). Source and encryption fields never join the eight-key public DTO.

The shared 30s per-request timeout remains; slow synchronous syncs can return
exit7 with may-have-happened. Real duration evidence and timeout policy are
outside this fix. Owner Jane revisits on a sync timeout contract change or
observed real-provider duration evidence; no standalone optional head move.

## BLIND null-preflight correction

Gate6671180247 sustains the access mismatch finding: null memory(ref) is not
an authoritative absence/no-source result. Red cc6e1311 pins four mutation
outcomes after null (FORBIDDEN8, success0, NOT_FOUND4, encrypted2), all failed
on prior c770 due to premature local4/no mutation. Fix bb325b90 delegates;
its test harness success path also avoids calling renderError on nil.
The principal labels in the fake are scenario names, not real ACL probes.
P2-1 codegen concern is answered by clean byte-identical make generate.
Raw BLIND CR is retained, no repeat judge requested. Required help wording
is narrowed to readable records; optional P3 polish remains deferred.
