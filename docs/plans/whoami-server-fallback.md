# `session whoami` falls back to the server (#623)

Status: **shipped as described.**
Related: hadron-server#1114 (no idle reaper — what makes an orphan permanent),
hadron-server#1034 (two things are called a session), #472 (one worktree per
worker — the rule that made the 1:1 coupling look natural), #484 (whoami's
*accuracy*; this is whoami's *reachability*).

## 1. The defect

The worker↔session binding is a JSON file under the worktree's **resolved** git
dir. `git worktree remove` takes the directory and the file with it, and
**nothing in that path calls `endSession`**. So after ordinary git hygiene:

- the session is still open, and the worker still taken
- `team whoami` cannot find it — it only read the file
- since #1114 nothing else ends it: no idle window, no reaper

The recovery path (`session end --session <id>`) already existed. What went
missing was **the id**, which only the deleted file knew.

## 2. Why the fix is convergence, not a new feature

| surface | answers *"what am I driving?"* by |
|---|---|
| MCP `hadron_whoami` | asking the **server** |
| CLI `team whoami` | reading a **local file** |

Same question, same name, two mechanisms — and only one survives losing a
directory. The server already holds the authoritative list; the CLI was keeping
state the server has. That is `cor:api:240:01` applied to state rather than
logic.

**The binding stays as a SELECTOR.** Two worktrees, two workers, no flags, and a
bare `team chat post` knowing who is speaking — that ergonomic earns its keep.
What should not be true is that deleting a directory silently orphans a session.
So item 2 of the issue is satisfied by *not changing anything*, which is worth
stating rather than quietly doing nothing.

## 3. The three filters, and why each is a test

`sessions()` is documented as *"Sessions VISIBLE to the caller … **not only the
caller's own**"*, and unfiltered it is also every session `type`, worker-bound
or not (#1034). None of the narrowing is available server-side, so the fallback
intersects three predicates client-side:

1. `userId == me.id` — attributed to you
2. `workerId != ""` — a worker session, not a general one
3. `endedAt == nil` — still open

**Too wide a filter here offers a colleague's session on a surface whose printed
remedy is `session end`.** So each predicate has its own case, and each was
mutation-tested separately: removing any one fails a *different* subtest.

The scan runs to exhaustion (`scanSessions`, issue #23). That is not caution —
since #1114 sessions are long-lived, so an open one is **not** necessarily among
the newest rows, and absence can only be proven by reading the whole list.

## 4. Two answers the fallback deliberately does not give

**It does not guess between several.** Several open worker sessions is the
NORMAL state here, not an edge case — measured live on this machine: **17**, one
driver, a worker per role. With more than one, `sessionId` stays empty and
`candidates[]` carries them all. Naming an arbitrary one would be a wrong answer
wearing a right answer's shape.

That number also changed the human rendering. Seventeen detail blocks is not a
list anyone can pick from, and picking is the whole purpose — so several
candidates print as a TABLE (worker, role, repo, started, session) and exactly
one prints as the familiar detail block. Found by running it; the unit tests
were written with one and two candidates and read fine at both.

**It does not re-create the binding.** `whoami` is a read. Rebinding is
`session start`'s job, and the output says so.

## 5. No worktree at all — the half that nearly shipped missing

`readBinding` returned a hard error outside a git worktree, so the fallback
never ran there. That is precisely the caller the fallback is most for: **a
non-coding worker, or Cowork, has no worktree to put a binding in**, so
requiring one to ask "what am I driving?" excluded them by construction.

Found by running it, not by reading it — the unit tests could not see it,
because the package sandbox sets `HADRON_TEAM_GIT_DIR` for every test and that
override makes a worktree always appear. `errNoWorktree` is now a sentinel that
`whoami` treats as "no binding" while the commands that WRITE a binding still
treat it as fatal, and the message no longer mentions a worktree the caller does
not have.

That near-miss is itself captured:
[`findings:a-sandbox-override-can-make-a-test-measure-the-sandbox`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-cli:findings:a-sandbox-override-can-make-a-test-measure-the-sandbox).

## 6. `--json` — additive only

```json
{ "...": "every pre-#623 binding key", "bindingPath": "…",
  "source": "worktree" | "server", "candidates": [] }
```

`source` because the same payload can now come from two places and a caller that
cares whether its answer survived a lost directory must be able to ask. Every
existing key survives — an agent reading `sessionId` predates this and is pinned
by a test.

**Every array field is `[]` and never null, on every branch** — and the first
version got this half right, which is the instructive part. The worktree branch
inherits `readBinding`'s normalisation; the two SYNTHESIZED server bindings did
not, so `prNumbers` rendered `null` there while `candidates` rendered `[]`.
I had checked one array field and missed the other, which is precisely the
mistake `review:stable-json-dto` names — *"sweep every array field in the DTO at
once — they fail as a family"* — so the test now sweeps the family rather than a
field. Flagged independently by @codex and @copilot.

Note the second synthesized binding **replaces** the first rather than mutating
it, so initialising only the empty one leaves the single-candidate path null.
Both sub-branches are mutation-tested separately for that reason.

## 7. Classifying the credential — and the claim that was wrong

Without a user identity the self-filter cannot run, and the unfiltered list is
other people's — so the fallback refuses. There are **two** ways to have no
identity and they need different answers:

| credential | answer |
|---|---|
| valid **App key** (authenticated, no user) | **NotFound** — "log in" would be a false remedy |
| **rejected** token (revoked / unknown / malformed) | **AuthRequired** — name the remedy |

The first version classified on `me`, and asserted in a comment that *"a genuine
auth failure never reaches here, because the query itself errors"*. **That was
false and @copilot caught it.** Measured against the real server with a bogus
`HADRON_TOKEN`: `me` answers null for a rejected credential too, so whoami told
a caller whose token was simply not accepted that they had *"no worker
session … nothing to recover here"*, while `auth whoami` — one command away —
correctly said the token was not accepted.

The remedy is not better prose on the same branch: it is asking **`authContext`**,
which is credential-type-agnostic and null *only* when the credential does not
resolve. That is what `auth whoami` already reads, so this converges on the
existing idiom rather than inventing one — and it replaces the `me` call
outright rather than adding to it, since it carries the user id too.

**The old test could not have caught this**, and that is the lesson worth more
than the fix: it stubbed `me: null` and asserted NotFound, which is correct for
an App key and wrong as a general rule. One input, two meanings, and the test
pinned the wrong one — `review:a-guard-proven-on-one-input-is-not-proven-on-the-input-that-matters`.
The two cases are now separate fixtures and separate tests.

## 8. Item 4 — the help now says when NOT to end a session

#1114 ruled worker sessions long-lived on the principle that *silence is not
evidence of abandonment*. The help explained at length how to end one and never
once said when not to, which is how ordinary git hygiene turned into a reason to
end a healthy session. `session end` and `whoami` now both say: **ending is for
when the WORK ends** — not when a chat session, a branch, a PR or a worktree
does — and name the re-bind cost (boot briefing, manual steps, the mistyped-name
failure mode of hadron-server#1157).

## 9. Not done, deliberately

- **Wrapping `git worktree remove`.** The issue rules it out and it is right:
  the CLI does not own that command. Recoverability is the fix.
- **Self-healing the binding from the server.** Tempting on the
  exactly-one-match path, but it makes a read write, and the two-match case has
  no answer anyway. `session start` rebinds.
- **`session list`'s own scoping.** It is honest already — it shows what you may
  see, and says so.
