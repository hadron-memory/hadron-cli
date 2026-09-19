# `whoami --check` — asking whether the bound session is still open (#484)

Status: **shipped, and deliberately much smaller than the issue as filed.**
Related: hadron-server#1114 (removed the inactivity reaper — this is what shrank
the issue), #623 (whoami's *reachability*; this is its *accuracy*), #1036.

## 1. The issue's original defect no longer exists

#484 was filed against a real incident: a worker session auto-expired after
~25.5h, `whoami` kept reporting it live, and **the first symptom was a failed
write** — mid-task, on the surface where it hurt most.

**That cannot happen any more.** hadron-server#1114 removed the inactivity rule,
and `startSession` leaves `expiresAt` null for DEVELOPER sessions, so nothing
ends one but an explicit `endSession`. The premise moved three times while this
issue was open, and the issue's own thread records each move — including two of
my asks that I withdrew rather than have Dara build:

- **`reapsAt` / `reapRule`** — withdrawn. With no deadline they would report
  "never", and a field whose only value is the absence of the thing it names
  invites a countdown that never counts.
- **Option 1, "warn on age alone, purely locally"** — withdrawn, and this one is
  worth restating because it is the tempting fix. The effective deadline had
  four inputs and the CLI could see one, so a local heuristic must hard-code a
  ceiling. Three different ceilings had each been published to this team and
  each contradicted. **Shipping it would have made the CLI the fourth publisher
  of a wrong number, and the worst-placed one, because it prints with a
  command's authority.**

## 2. What survives, and why it is still real

A session can be ended **from anywhere**: `session end --session <id>` needs no
binding (and #623 made the id easy to recover). It clears only the binding of
the worktree it ran in. So every *other* worktree keeps a file describing a
session that is gone — and under one-worktree-per-worker (#472), several
worktrees per person is the normal shape.

That is the same class as the original bug (a local view outliving what it
describes) with a different cause, and it is unaffected by #1114.

## 3. Opt-in, not default

`--check` costs a round trip. The default stays local because `whoami` is the
**compaction-recovery read** — an agent runs it to re-learn its own name, wants
the answer immediately, and is not asking about liveness.

`--json` therefore carries **two** fields, not one:

```json
{ "checked": false, "live": null, "endedAt": null, "autoExpiredAt": null }
```

`checked` and `live` are separate because **false and unknown are different
facts and one boolean cannot carry both**. With a single `live`, a default read
would have to emit `false` — asserting the session is dead when nothing asked.
`live` is a pointer and is null unless `checked` is true.

This is the same call `source` made in #623: when a command can answer from two
places, say which one answered.

## 4. `endedAt` answers WHETHER; `autoExpiredAt` answers HOW

The reaper stamps `endedAt` and `autoExpiredAt` to the same instant, precisely
so that "active" stays the single predicate `endedAt IS NULL`. The consequence
is that **a session someone ended and one the server reaped are
indistinguishable** without the second field — and the CLI selected it nowhere.

Adding it to `TeamSessionFields` was the half of #484 I called "mine and cheap"
months ago and never did. It costs one line of GraphQL and gives both `session
list --json` and `whoami --check` the distinction.

The two answer differently to the same question — *does carrying on help?* — so
`--check` renders them differently rather than collapsing both to "ended". That
difference is mutation-tested: removing the auto-expired arm fails a subtest the
plain-ended case still passes.

## 5. What it refuses to claim

**It says nothing about idleness.** An open session undriven for months is
*correctly* open under #1114. The platform does compute a last-driven instant
(`lib/sessionActivity.ts`) and a derived `isLive`, but **neither is on `type
Session`**, so the CLI cannot honestly report one. Inventing a staleness signal
from what it can see is exactly the mistake option 1 would have been.

That remains the open ask on the issue, and it is server-side: put
`lastDrivenAt` and `isLive` on the session read and `whoami` can say *"bound to
Jonas, last driven six weeks ago"* instead of implying currency.

**A session the server does not report is NOT rendered as ended.** `session(id:)`
answers null for a session that does not exist and for one the caller may not
read, identically — so `--check` reports that ambiguity and leaves the binding
alone. Reporting a death this read cannot see would be #484's own failure mode
inverted, and it has its own test.

**A cross-deployment binding is refused, not answered.** A binding from another
server describes a session this one never heard of, where "not found" would read
as "ended"; `checkBindingServer` already had the honest message.

## 6. Verified by running it

Both paths, live:

```
$ hadron team session whoami --check
  server: still open

$ (binding temporarily pointed at a genuinely ended session)
  server: ENDED 2026-09-19T14:57:58.592Z — this binding is stale;
          rebind with `hadron team session start --as Jonas`
```

The ended session it caught was this session's own predecessor, ended when the
work moved to a worktree — which is precisely the case in §2.
