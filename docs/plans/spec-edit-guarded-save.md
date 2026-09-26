# Design as built: guarded `spec edit` saves (#738)

> **Status: built, stacked on cli#743 (#742).** Written 2026-09-26 by Jonas.
> From Tove's docs#327 verification (team chat #1985): released v0.17.0's
> `spec edit` had no conflict guard. An agent could read a spec, present a
> proposal, wait for approval, and then overwrite a change made in between.

## What was verified before changing anything

- **The server contract (hadron-server#1352, merged `d8404ad7`).**
  `UpdateNodeInput.expectedRevision` makes a write compare-and-swap on the
  node's live revision. On a mismatch it refuses atomically with
  `NODE_WRITE_CONFLICT`, before the revision snapshot, so nothing is written.
  A stale guard is refused even on a no-op write. The governed door
  `updateSpecNode` goes through the same `updateNodeCore`, so the guard applies
  there too (read on server `main`).
- **Deployed:** Gil's read-only production probe (team chat #2111) found
  `UpdateNodeInput.expectedRevision` accepted and a made-up control field
  refused.
- **Before this change** the CLI mapped `NODE_WRITE_CONFLICT` nowhere, so it
  exited 1.

## Design

1. **The revision comes from the same read as the body.**
   `GetSpecNodeForEdit` is `GetSpecNodeRaw`'s selection plus `revision`, in
   one query. A second read for the revision could return a newer one than the
   body was read at, and would then bless a stale proposal: exactly the loss
   this issue exists to remove. It's a separate operation rather than a field
   on `GetSpecNodeRaw` because `supersede` and `extract` share that read
   (cli#742) and don't need a revision. On a server predating `Node.revision`
   the new field would fail their whole query.
2. **Every save is guarded.** `editProposal.input()` always sets
   `expectedRevision` to the proposal's base revision. There is no unguarded
   path and no flag to request one.
3. **`--expected-revision N` carries an approval across turns.** The dry run
   reports `revision` (in `--json`, plus a text line naming the flag), and a
   later run passes it. If the spec moved on since, the run is refused with
   exit 5 **before** an editor opens or a preview is computed. The server
   enforces the same comparison on the write, so the client check is a
   courtesy and never the only gate.
4. **On a write-time conflict, the proposal is kept.** `NODE_WRITE_CONFLICT`
   exits 5, and nothing is retried or written. The proposed text (the edit
   buffer's abstract and body) is spilled to a temp file whose path the
   message names, since it may only have existed in `$EDITOR` or piped stdin.
   The message asks the caller to re-read, reconcile, and get renewed
   approval.
5. **An unsupported server is refused, never written to unguarded.** A server
   predating `Node.revision` (#1339) fails the read with a plain "predates node
   revisions" refusal. One predating `expectedRevision` (#1352) fails the one
   guarded write attempt with a plain "does not support guarded saves"
   refusal. Both exit 2, and there is no fallback retry.
6. **`NODE_WRITE_CONFLICT` → exit 5** in `codeForExtension`, for every command.

## Tests (`internal/cmd/spec_edit_guard_test.go`)

- A save sends `expectedRevision` equal to the read's revision (7).
- A matching `--expected-revision` saves.
- A stale one is refused with exit 5 and no write, in both a dry run and a
  real save.
- A server `NODE_WRITE_CONFLICT` gives exit 5 with **exactly one** write
  attempt (no retry), and the kept file holds the proposed body with its
  placeholders intact.
- A dry run reports `revision` in `--json` and names `--expected-revision 7` in
  its text.
- These are refused, with nothing written: a non-positive
  `--expected-revision`, a server without revisions, and a server without
  guarded writes (one attempt, no unguarded retry).
- **Mutation-checked with a validated harness:** a deliberately equivalent
  mutant survives, and each of these produces a real `--- FAIL`:
  - the guard not sent;
  - the stale check removed;
  - conflict handling removed;
  - the unsupported-guard message removed;
  - the no-revision message removed;
  - the dry-run revision line dropped.
- No production writes.
