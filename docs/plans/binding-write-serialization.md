# Serializing writes to the worktree binding

Design-as-built for hadron-cli#499. **The design changed during
implementation**, and the reason is the most useful thing in this document.

## The defect

`<worktree-git-dir>/hadron-team-session.json` has four writers and nothing
between them:

| site | operation |
| --- | --- |
| `session start` | writes a fresh binding |
| `session log --pr` | read-modify-write (appends `PRNumbers`) |
| `session end` | removes the file |
| `chat read` watermark | read-modify-write (sets `chatSeenSeq`) |

Each individual write is atomic — same-dir temp plus `rename` — so nobody ever
sees a torn file. But **atomic replace is not compare-and-swap.** Two
read-modify-write sequences interleave, and the later writer silently discards
the earlier one's edit:

- `session log --pr` appends a PR number; a watermark write that began from an
  older snapshot writes its own copy back and the PR number is gone. Provenance
  loss, no error.
- `session end` removes the file; a concurrent read-modify-write **recreates**
  it, leaving the worktree bound to a session the server has already closed.
  `whoami` then reports a live binding for a dead session.

Two agents in one worktree is the normal case on this team, not a hypothetical
(`dev:findings:concurrent-agent-sessions-share-one-worktree`).

## What was planned, and why it does not work

The issue sketched — and I argued for, and the project lead chose —
**compare-and-swap**: a `rev` counter in the binding, writers refusing on
mismatch and retrying. The appeal was real: no lock lifecycle, no stale-lock
recovery, no timeout policy, and it composes with the atomic rename already in
place.

**It cannot be built on a plain file.** CAS needs an atomic
compare-and-swap primitive, and the filesystem offers none for this shape:
`WriteFileAtomic` is temp-plus-rename, an *unconditional* replace. The closest
an optimistic scheme can get is write-then-verify — write, then read back and
check your bytes survived.

That is not equivalent, and the gap is not small. **Reading back only proves
nobody had clobbered you by the moment you looked**, not that your write
survived. Measured, with the implementation in hand:

```
6 concurrent writers → all six return success → 2 edits land
final: rev=2 prs=[0 2]
```

Every one of those writers verified its own bytes, and four of them were
overwritten immediately afterwards. Adding randomized backoff changed nothing,
because livelock was never the failure.

Worth keeping: the optimistic version **did** fix the case the issue
describes — a writer starting from a snapshot taken seconds earlier, because
`updateBinding` re-reads inside the critical section. Staged by hand, both
edits survived. It failed only under true simultaneity. That distinction is why
it looked correct in every test written before the concurrent one.

## What shipped

**`flock(2)`** (`LockFileEx` on Windows), taken by **all four** writers through
one `withBindingLock` helper.

The objection that steered the issue away from locking was stale locks — a
crashed holder leaving a lock nobody can clear, and the timeout and recovery
policy that follows. **That objection does not apply to this primitive.** The
kernel releases an `flock` when the descriptor closes, *including when the
process dies*. There is no stale lock, no age heuristic, nothing to clean up.
An `O_EXCL` sidecar would have had exactly the problem the issue feared; `flock`
does not.

Three decisions worth stating:

**The lock lives on its own file, never on the binding.** `WriteFileAtomic`
renames a fresh inode over the binding, and a lock is held on an *inode* — so a
lock taken on the binding itself would be silently replaced mid-critical-section
by the very write it was meant to serialize. `hadron-team-session.lock` is
created once and never removed or renamed. An empty leftover is inert, which is
precisely why this needs no recovery path.

**Every writer, including `clearBinding`.** A lock three of four writers respect
is not a lock, and removal is exactly the operation a concurrent
read-modify-write must not straddle.

**An update never creates.** `updateBinding` refuses a binding that is not
there. This is structural rather than a consequence of locking, and it is what
kills the resurrection bug: `session end` removes the file, and no update can
put it back.

### The `rev` counter survives, doing a smaller job

It is no longer load-bearing — the lock provides the serialization it was meant
to enforce. It stays as a monotonic record of how many writes have landed:
cheap, useful when reading a binding by hand, and it makes concurrent behaviour
observable in tests. Absent (`0`) on a binding written before this change, and
no migration is needed: the first update reads 0 and writes 1.

### The mutation closure sees current state, and callers must rely on that

`updateBinding` takes `func(*binding) error` and runs it *inside* the lock,
against a fresh read. So preconditions belong in the closure, not before the
call — the caller's snapshot is stale by definition. Both callers were
rewritten accordingly:

- `session log --pr` re-checks PR membership inside the mutation. Checking
  against the pre-call snapshot would let the serialized write faithfully
  preserve a duplicate a concurrent agent had just added.
- `recordChatWatermark` re-checks the session id and the existing high-water
  mark inside the mutation, and aborts with a sentinel when the write is not
  applicable — an aborted mutation writes nothing and does not bump `rev`.

## Verification

The concurrency test is a **real race with real goroutines**, not a staged
interleaving: 12 appenders and 12 watermark writers against one binding. A
hand-staged sequence only proves the one ordering its author imagined, and the
ordering I would have imagined is the one that passed on the broken design.

- Passes with the lock; **fails without it** — bypass `withBindingLock` in
  `updateBinding` and the same test reports lost PRs.
- Clean under `-race`, `-count=3`.
- The resurrection case is covered on both paths, including
  `recordChatWatermark`, which is the one that actually did it.

## What this does not do

It serializes writers **within one machine's filesystem**, which is the whole
scope of a per-worktree file. It says nothing about two checkouts, and nothing
about the server-side session state — a binding is a local recovery record, not
the source of truth.

It also does not make sharing a worktree a good idea. The rule is still one
worktree per worker (#472); this removes one silent failure mode from a
situation that has several.
