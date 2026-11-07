# Content-validation reports: CLI as built

Scope: cli#1706, cor:dmo:160:01 r7, CHECKED 6677935238 and discovery
ruling 6703322576. Server#1332 merged as bc40260e. The writer-readable
projection is tracked by server#3659; this PR implements the expressly allowed
interim discovery path, not that projection. Report history remains portal work.

`node validation record <ref> --outcome PASS|FAIL|ERROR [-m memory]
[--task ID-or-listed-URN] [--run-id key] [--json]` records a result of a task
already run. It does not execute the task. Node/task revisions come from reads;
users must record before either changes. No reporter is selected or sent.
The server owns access, author comparison, revision/task validation and replay.
No mutation failure triggers a retry.

## Reads and selection

1. Resolve the usual node reference (IDs avoid a resolve request).
2. Read id, memoryId, revision, role, isRunnable from that node.
3. Read only role, enabled, strictSubRoles, validationTask/Id/State from
   memoryConfig. The interim manager-only read's not-found/forbidden maps to
   exit 8 and explains the missing discovery permission; no past report is used.
4. Mirror server `ruleKeysOf` and `resolveNodeRoleRule`: role plus runnable
   `task`, no duplicate task key, closest enabled dotted ancestor, strict
   undeclared subroles do not inherit. Disabled rows still declare a subrole.
   A rule with no task state is omitted, but broken/unreadable rules are retained
   to fail closed. Deduplicate selected task IDs, not the rule walk.
5. Read each applicable task's id, current revision and runnable marker, once.
   One task is automatic. Multiple tasks list refs/revisions and require an
   explicit --task; a task outside the effective set exits 2 before any write.
   Empty set: Patrick's 88137 issue fold directs a local typed
   CONTENT_VALIDATION_NOT_CONFIGURED with server-identical text and mapped exit 5;
   no sentinel ref/revision and no mutation.
6. Send exactly one RecordContentValidation. --run-id is omitted when unset,
   otherwise forwarded unchanged. A replayed receipt succeeds. A missing/malformed
   receipt is uncertain (exit 7), never success or a reason to retry blindly.

Source trace: server main 387927bd, contentValidationReport.ts validationRules
and tasks.find, governedRole.ts resolveNodeRoleRule, nodeRoleWriters.ts ruleKeysOf;
resolvers.contentValidationReport.ts validates task refs before rule discovery.
The CLI mirror is interim, and the server rechecks current state authoritatively.

Economics: ID input sends 2 discovery requests + T task reads + 1 mutation,
where T is the deduplicated applicable task count (normally 1-2); URN input adds
one ResolveUrn. No node/task bodies, author identities or reporter details are
selected. Multiple-choice refusal uses 2+T reads and zero writes. Realistic small
rulebooks produce kilobytes, not a corpus scan; QA measures actual response bytes
and wall time on the compiled fake matrix. No live-provider or ACL proof is made.

## Output and errors

Receipt DTO: nodeId, replayed, report {nodeRevision, validationTaskRef,
validationTaskRevision, outcome, reportedAt, authorCheck}, state. Text calls it
advisory. CHECKED is recorded-author comparison, never independence;
AUTHOR_UNKNOWN is inability to compare, never a failure. All nullable receipt
fields stay present as null. No generated structs are marshalled.

Mapping: missing/unreadable node 4; FORBIDDEN/reporter-unknown/reporter-is-author 8;
invalid node/task revision or wrong-task/BAD_USER_INPUT 2; not-configured,
broken-task/run-ID conflict/CONFLICT 5; transport uncertainty 7; unexpected fault 1.
Server messages retain the existing shared mapper. No new reporter policy is made.

`node get` uses a distinct authorCheck projection with a strict parsed-schema
fallback to existing stamp reads. HTTP failures, mixed errors, populated partials
and resolver-path errors cannot trigger fallback. Historical revision JSON stays
unchanged (the new field is emitted only by node get and the new recorder).

## Evidence and limits

Read red f10c74f7, green c435bc26; five assertion-red mutants and equivalent
control, one earlier compile-only mutant excluded. Recorder red 639e6c38 is the
missing command. Its initial UUID fixture was not a server-shaped ID; corrected
to a 32-hex ID for executed wire tests (no rewritten history). Malformed receipt
pin caught a nil dereference during development, now guarded. Fixture execution
is isolated and synthetic; it does not prove real access gates, signing, live
validation, server deployment or cross-platform runtime behavior.

Recorder mutation evidence: eight assertion-red mutants (node/task revisions,
multiple-task auto-selection, omitted run-ID, strict inheritance, runnable key,
disabled exact rule, ignored explicit choice), and equivalent parentheses green.
Every source anchor was asserted unique, each edit restored. One final choice
anchor initially matched zero lines: explicitly stopped without testing/counting,
then corrected and run separately; the prior seven results were retained.

The corrected 32-hex-ID single-task test was independently copied to a detached
c435bc26 checkout (before the recorder existed): it fails on unknown --outcome,
zero requests. The temporary checkout was removed. Full suite, focused race, lint
and matrix checks passed after the implementation; the last source edit changed
help wording only.
