# `--as-worker`: compile and approve boot prompts as the bound worker (cli#1872)

Design as built. Server side: hadron-server#3892 (merged `65919dba`); it changed
no SDL, so the schema snapshot is untouched. Specs: cor:agt:040:11 r8, :12,
:18 r9 (server law). The flag, its default and the strict resolver are client
behaviour, not platform law.

## What it does

Six commands take an opt-in `--as-worker`: `agent compile`, `app agent compile`,
`team worker compile <w>`, the batch `team worker compile`, `node approve` and
the batch `team worker approve`. Without the flag nothing changes: the command
acts as the person and sends no worker-session header (same output, same
`--json`, same `authority` constant). With it, only the compile and approve
mutations carry the worktree's bound worker session; every read in the same run
stays the person's. The server judges the signed `bootprompt.compile.approve`
grant; the CLI never picks, checks or guesses one, and prints the server's
refusals verbatim with their codes (exit 8).

## Strict resolver (`team.WorkerWriteSession`)

`AuthoredWriteContext` is best-effort: it attaches nothing when it cannot verify
a binding. That is right for ordinary authored writes and wrong here, where a
compile the caller asked to make as the worker would silently become the
person's. So `--as-worker` uses `internal/cmd/team/worker_write_session.go`,
reached from `bootcompile` and `node` through `Factory.WorkerWriteSession`
(`cmdutil/factory.go`, `ResolveWorkerWrite`). It refuses with exit 2 and sends
nothing for: no worktree, no binding file, an unreadable or incomplete binding,
a binding for another server, an `--app` naming another App. Initial binding
faults refuse before the first request, including a batch's dry run. An App
identity read may precede an App mismatch refusal; no mutation is sent.

It returns a function called immediately before each mutation. That function
re-reads the binding and compares it with the snapshot taken at resolve time; a
changed binding refuses that write (single command: exit 2; batch: the worker is
filed under `failed` with exit 2 and no later write is sent). One mechanism
covers a rebind during the App lookup, between a batch's writes, and any longer
gap, with one guard to test.

`authoredWriteField` is deliberately not extended: that would make every bound
worktree's compile and approve a worker write by default and refuse managers
working from a bound worktree (the #868 trap).

## Output under the flag

- Single compile: "compiled as the bound worker …"; `pointerSet` is whatever the
  server returned (it forces false for a worker compile). The CLI sends no
  `setPointer` it did not send before.
- Batch compile: the printed `approveCommand` ends ` --as-worker` when the batch
  ran with it.
- `team worker approve --json`: `authority` is a second constant naming the
  grant path; the default constant is byte-identical. No JSON key is added.
- `node approve`: unchanged; `approverLabel` already renders the server's
  `approvedByInfo` (`worker:<id> (batch grant)`).

## Pins (`internal/cmd/as_worker_1872_test.go`)

P20 default path sends no session (all six); P21 mutation carries `s-new`,
reads none; P22 strict refusals send nothing; P23 rebind during the App lookup
and between a batch's writes; P24 server refusals exit 8 and batch rows keep
code and exitCode; P25 approve line; P26 authority text; P27 approver
rendering; P28 no `setPointer`; P29 help truth. Mutants killed: resolver
fallback to the person, dropped per-mutation re-check, wrong authority on the
worker path, missing approve-line suffix, session not applied on `node approve`
and on single compile, and `approveNode`/`compileWorkerBootPrompt` added to
`authoredWriteField` (P20/P21).

## Not done here

Asking for or signing grants (`team grant request`, hadron-cli#1868, shipped);
docs (hadron-docs#703). `bootprompt.source.edit` rides `updateNode`, which
already carries the bound session.


## App-identity follow-up (#1896)

The shared `bindingsAppIdentity` helper treats a pure App-read FORBIDDEN as a
missing App. The worker-write --app, positional-App and signature/trailer
callers produce byte-identical unreadable/missing refusals, exit 2, with no
mutation. The helper also serves worker-bound grant reads, grant request
verbs and `team task mine --app`: each now returns its existing missing-App
usage refusal (exit 2), rather than the former permission error (exit 8).
Optional authored-write attribution silently omits the session for either a
missing or unreadable App and still sends the legitimate person write;
previously unreadability printed a diagnostic note. This silent fallback
matches the existing missing-App path and does not grant access.

The conservative `isBindingsApp` wrapper already discarded lookup errors, so
its register, chat-attribution and chat-cursor callers keep their prior bool
behavior. The complete caller inventory is that wrapper, grant reads, grant
requests, task mine, worker-write initial scope and late target, signing, and
authored-write attribution.
Known credential-scope refusals, mixed failures and transport errors retain
normal classifications, including diagnostic notes in optional attribution.
This follows the #1896 gate's App-scoped refusal rule; the server's generic
App read is unchanged. Pins compare complete text/JSON streams for the four
additional callers and check that credential failures retain their exits or
diagnostic notes. The helper-normalization mutants are described in "Follow-ups from the #1896 review (#2239)" below, with a runnable recipe.

A single worker compile first validates its binding, then resolves the worker
as the person. With --as-worker, its returned App is checked against the SAME
captured binding snapshot before any worker label or compile mutation. The
returned `WorkerWriteContext` accepts late-resolved target Apps and rechecks
the binding before and after identity lookup; a refusal returns nil context
and stays latched. The final pre-mutation recheck remains. Explicit worker
ids/URNs still ignore ambient scope on the default person path; no grant is
checked locally. Unknown workers retain their original refusal.

Pins cover four positional App spellings, all shared-helper callers in text
and JSON, pure permission vs credential/mixed errors, cross-App workers,
unchanged person/same-App paths, and rebinding during positional/worker/late
App lookups. The server grant remains the authorization barrier; these are
client consistency and refusal-oracle checks. No schema or request projection
changes or real server/production claims.

## Follow-ups from the #1896 review (#2239)

The behaviour is unchanged by these follow-ups; they pin it, document it and record two decisions. Authority: V58
(`hrn:node:hadronmemory.com:hadron-dev-team-shared:decisions:2026-11-10-vera-rulings-app-refusals`), folded into
cor:agt:050:21 and cor:dmo:050:11. Both lines are **Proposed**, not Decided, so the docs explain and cite them without
calling them settled law.

**Why an unreadable App shares the missing-App refusal.** An App whose read access is gone reads, to the CLI, exactly
like an App that does not exist: the server's pure FORBIDDEN on the App read is normalized to "no such App", with the
same text, stream and exit 2. A distinct "forbidden" answer would confirm the App exists to a caller who may not know
it does: an existence oracle. This is deliberate, and the same helper serves the worker-write callers, signing, grant
reads and requests, and `task mine`.

**What is NOT normalized: mixed and failed lookups.** Only a PURE permission refusal collapses. A credential or
token-scope refusal and a MIXED answer (FORBIDDEN together with UNAUTHENTICATED) keep the server's own messages, in the
order the server sent them, and exit by the FIRST error's code. A failed lookup keeps its ordinary mapped diagnostic, not
the server's text: an internal error is masked to "the server hit an internal error" and exits 1 (the internal-fault
contract, cli#898; the pin's fixture sends "lookup failed" and expects the masked text). So a mixed answer led by
UNAUTHENTICATED exits 3 and one led by FORBIDDEN exits 8; none of these can be mistaken for a missing App, and the
outcome is repeatable for the same lookup answer, which is all the CLI sees (the pin runs the identical fixture twice; it
does not vary App state, since nothing but the lookup answer reaches this path; cli#2297). This first-error
rule is the documented mapping, not part of the normalization; whether a mixed answer should always prefer the
credential exit is an exit-code product decision, not made here. The pins are `TestMixedAndFailedAppLookupsKeepTheirMappedExit2239`
and `TestMixedLookupAsWorkerRefusalSaysItCannotConfirm2239`, on all eight callers, text and JSON, both error orders.

**Order independence of the tests.** The missing-App baseline is now run explicitly before the other kinds in
`TestAsWorkerAppRefusalParity1896` and `TestBindingsAppOtherCallerParity1896`, so no kind depends on loop order. The
positional-spelling controls build their commands literally (the legacy empty-segment URN spelling,
`hrn:app:acme.com::eng-team`, stays in them: cor:urn:010:01 accepts it on input forever).

**Runnable recipe for the helper-normalization mutants** (replaces an earlier claim without a recipe). In
`internal/cmd/team/team.go`, function `bindingsAppIdentity`:

1. Change `api.OnlyErrorCodes(err, "FORBIDDEN")` to `api.HasErrorCode(err, "FORBIDDEN")` (normalizes a mixed answer):
   `go test ./internal/cmd -run '2239|1896'` fails 28 subtests at the time of writing.
2. Change the final `return false, api.MapError(err)` of the error branch to `return false, nil` (normalizes every
   error): the same command fails 36 subtests.

Restore the file after each run. The numbers are the count of failing subtests when the pins were written and will
move as callers are added; what matters is that both runs fail.

**Decisions, not built.**
- *`WorkerWriteContext` naming (P3-4).* Kept as the function type, now with its late-target parameter documented at
  its definition and the call sites. A named guard type or interface would change `cmdutil.Factory` and four commands
  for no behaviour gain, and any product change on the refusal path adds a §10 review (the #2239 gate's condition). Revisit
  if a second consumer shape appears.
- *A generic "missing or unreadable" attribution-skipped note (D-P3-2).* Not added. Optional authored-write attribution
  is silent for a missing App and for a pure permission refusal, which is exactly what keeps the two indistinguishable;
  a note for both would be parity-safe but changes the ordinary output of the person's own write. Credential, mixed and
  failed lookups keep their diagnostic note.
- *The mutex held across the late App lookup (P3-5).* Documented at the lock: command-local and used serially today; a
  parallel batch consumer would need it revisited.
