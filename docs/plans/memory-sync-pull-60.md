# Memory sync pull (cli#60 slice A v2)

Implements author attach 6627531391 and CHECKED 6669136019. The governing
contract is cor:int:010:01; encrypted Git sync refusal follows cor:sec:040:05
(R206). Ruling0168(c) excludes push: pushMemoryToGit remains gap #60. Slices B
and C are not part of this change.

`memory sync pull <memoryRef>` canonicalizes the explicit ref locally and asks
cmdutil.Confirm BEFORE making any request, including a lookup. syncMemory
accepts an ID or URN, so one mutation suffices for all accepted memory spellings.
The prompt names this canonical reference (not a fetched display name). --yes
bypasses the prompt; no automatic retry or fallback is offered.

The operation selects only syncStatus/syncError/lastSyncedAt/lastSyncReport.
The explicit command DTO reuses parseSyncReport and writeSyncBlock; it adds no
second JSON report decoder. Unknown v1 entry vocabulary is unavailable at this
door rather than a certified result. A clean result requires no report, a
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
