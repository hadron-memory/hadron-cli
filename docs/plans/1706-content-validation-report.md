# Content-validation reports: CLI as built

Scope: cli#1706, cor:dmo:160:01, CHECKED 6677935238, step-3 restatements
6705464951 / 6705477233 and reconciliation 6705487959. Server#3664 merged
9568052d supplies writer-only Node.validationTasks. This follow-through removes
the shipped interim manager-config derivation. Report history remains portal work.

`node validation record <ref> --outcome PASS|FAIL|ERROR [-m memory]
[--task ID-or-listed-URN] [--run-id key] [--json]` records a result of a task
already run. It does not execute the task. Node/task revisions come from reads;
users must record before either changes. No reporter is selected or sent.
The server owns access, author comparison, revision/task validation and replay.
No mutation failure triggers a retry.

## Reads and selection

1. Resolve the usual node reference (IDs avoid a resolve request).
2. Read node id/revision and validationTasks {state, tasks {broken, taskId,
   taskRef, revision}} in one operation. The server owns rule resolution and task
   readability. No config/history fallback or separate task read exists.
3. Validate the projection. READY needs healthy entries with identity/ref and a
   positive revision; identical task IDs are deduplicated, conflicting duplicates
   fail closed. One distinct task is automatic; multiple require explicit --task.
   Choice outside the set exits 2. NOT_CONFIGURED with [] maps to existing local
   CONTENT_VALIDATION_NOT_CONFIGURED (5). BROKEN with redacted broken entries
   blocks even healthy siblings (5). UNAVAILABLE with [] and null both refuse
   generically (8), without diagnosing their hidden cause. Contradictory, unknown
   or malformed shapes fail closed (1). Missing field on older servers uses the
   existing schema-skew exit 2, no automatic fallback.
4. Send exactly one RecordContentValidation using the selected returned identity
   and revision, not a synthesized one. --run-id remains omitted when unset and
   forwarded otherwise; replay succeeds. All author/access/report gates remain
   server-owned. Missing/malformed receipt remains uncertain (7), never retried.

Source pin: schema exported from isolated merged server9568052d, with the generated
client regenerated normally. Its other schema change is deleteWorkerPreview's
source comment, carried into generated comments only. No hand-edited generated code.
Economics: ID input is one discovery read plus one mutation; URN adds ResolveUrn.
Every discovery refusal sends zero mutations. No body, reporter identity, rule
keys or memory configuration is selected. QA must qualify synthetic versus real
provider evidence; no deployment floor is invented.

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

## Step-3 evidence

Red ac7ef300 pins the projection query and forbids legacy discovery operations;
it fails against fresh main for the missing selection and attempted config read.
The former local rule-resolution tests are removed with the deleted rule mirror;
wire selection/state tests replace them. Existing mutation-refusal and input
controls remain, now supplied a READY projection. Prior interim evidence above
is historical, not a claim that removed derivation code remains exercised.
