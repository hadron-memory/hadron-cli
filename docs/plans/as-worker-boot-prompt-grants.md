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
missing App. The --app, positional-App and signature/trailer callers therefore
produce byte-identical unreadable/missing refusals, exit2, with no mutation.
Known credential-scope refusals, mixed failures and transport errors retain
normal classifications. This follows the gate's V58/FIT interpretation on
App-scoped team surfaces; the server's generic App read is unchanged.

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
