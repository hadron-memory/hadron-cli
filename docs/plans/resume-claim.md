# Worker resume claims in the CLI (hadron-cli#842)

**Status:** design as built. **Server contract:** hadron-server#1956, merged as #1966 (4333d600).
**Spec:** cor:agt:020:03 ("a post or a work record is never silently attributed to the human"; the
optional-cache and 60-second rulings, Holger, team chat #12291 items 7 and 8).
**Gate:** Xylon's CHECKED T2 + §10, hadron-cli#842 comment 6190022709. That gate sets the scope from
the issue's title: the `.hadron.json` marker half of the original body is withdrawn.

## The incident class

A CLI agent recovering from a context reset could run `hadron team chat post` from a context with no
worker binding. The CLI then sent no session, and the server attributed the post to the human driving
it. Nothing told the agent that the message did not go out as the worker.

## What changes

### 1. An unbound post refuses (the red)

`team chat post` with no resolvable worker binding and no `--as-me` now refuses. That covers no binding
file, a directory outside any git worktree, and an ambient or `--app` App with no binding. It exits 4
(NotFound, the code `team session log` already uses for "no active session"), and nothing is sent. The
message names the three ways forward: `team session start` with no arguments to recover a worker,
`--as <worker>` to bind one, or `--as-me` to post as yourself. It names no worker, session or claim. The
client knows none, and naming one would leak which bindings exist.

`--as-me` keeps posting as the person, exactly as before. Work records (`team session log`) already
refused without a binding, so only their message changes, to point at the same recovery path.

A binding that exists but is bound to another server still refuses with the existing
`checkBindingServer` error. A binding whose session the server no longer accepts is still refused by the
server.

### 2. `team session start` with no worker named redeems

Before #842, `start` with neither `--as` nor a launch grant was a usage error. Now it calls
`resumeSession`, which takes no worker argument and is judged only by the caller's token:

| Server outcome | CLI |
|---|---|
| `RESUMED` (a claim for an open session, or the caller's one live worker session) | writes the worktree binding (the optional cache), prints the receipt, continuity and briefing exactly as a bind does, and adds `resumeOutcome: "RESUMED"` to `--json`. `tookOver` is null: the session was picked because it reads live, so another context may still drive it |
| `STARTED` (the claimed session had ended, so the worker's next session was started) | the same, with `resumeOutcome: "STARTED"` and `tookOver: false` (a new session, admitted only with none live) |
| `CHOOSE` | binds nothing. It lists the candidates (worker name, description, source, and for a claim its expiry) with the `--session <id>` to pick each one, and exits 2. `--json` prints `{"resumeOutcome":"CHOOSE","candidates":[…]}` |
| `NOTHING_TO_RESUME` | exit 4, with the server's message. No fallback, and nothing is posted |
| `RESUME_REFUSED` | exit 5. The claim is spent, and the message says so |
| `RESUME_AMBIGUOUS` | exit 2 (the existing `_AMBIGUOUS` rule): name the worker with `--as` |

`--session <id>` picks a listed candidate. It applies only to a resume, so it is refused alongside `--as`
or a launch grant. The flags `ResumeSessionInput` can't carry (`--repo`, `--branch`, `--reports-to`,
`-m`) are refused on a resume instead of being silently dropped. `--force` is refused too: it is a
takeover flag for a named worker, and a resume names none. `--tool`, `--host`, `--model` and
`--transcript` ride along as provenance for a session the redemption starts.

**An empty `--as` is not an absent one.** `--as "$W"` with `W` unset is refused (exit 2), so it can
never fall through to a resume that binds a different worker.

**The existing binding guard runs first.** Redeeming spends a claim, so a worktree still bound to a live
session refuses (exit 5) before `resumeSession` is called. The message says the binding already carries
the worker (`whoami`), which is the ordinary shape of a shed in the same worktree, instead of the
"another agent may be working here" takeover text that a named bind gets. A claim is for a context with
no binding, and an unused one lapses on its own. A lapsed or ended
binding is replaced, as a plain start replaces one. Outside a git worktree the start refuses before the
call too: the CLI has nowhere to cache the binding, and consuming the claim anyway would strand it.

**The cache describes the session the server bound, not the flags** (Anne, blind P2 at e720abf5). A
RESUMED session keeps the provenance it was started with, except `llmModel`/`family`: with
hadron-server#2086 a resume refreshes those two on the same session (an explicit family wins, otherwise it
is derived from the model, as `updateSession` does since #1948), and an omitted `--model` never clears what
the session already carries. `tool`, `host` and the transcript path are still ignored for a RESUMED session.
Only STARTED passes all of them to `startSession`. This is server behavior: the CLI sends the flags it
was given and changes nothing, so against a server without #2086 the old behavior remains. So the binding's tool, model and repo come from the returned
session, and a flag fills only a field the session lacks. Caching the flags left a no-argument resume
with an empty tool, and the next `session log` sent it and was refused. A flag that disagrees with a
resumed session gets a stderr note saying it was not applied.

**A resume never rolls back.** If the worker read or the binding write fails after `resumeSession`
answered, the CLI does not end the session, unlike a plain or launched start. A RESUMED session is the
worker's continuing session, and ending it would destroy exactly what the claim recovered. The error
names the session and says that running `team session start` again recovers it through the server's
fast path (the one live session).

### 3. Writing the claim

* **Rotation:** `team session end --resume-claim` sends `endSession(resumeClaim: true)` through a
  separate operation (`EndTeamSessionWithResumeClaim`). An older server rejects an unknown argument at
  validation even when its variable is omitted, so the plain end keeps its old document. The server
  writes the claim after the handoff and before the end, and refuses the end if it can't.
* **Shed:** `team session resume-claim` (new) calls `createResumeClaim` for the bound session, or for
  `--session <id>`. The receipt prints the worker and the expiry, and says the claim must be redeemed
  within 60 seconds by running `team session start` with no worker named. The shed tooling (#1958)
  calls this before the reset. `--session ""` is refused, not read as the bound session. With
  `--session`, `--json`'s `workerName` is null, because no binding names the worker. `SESSION_ENDED` maps
  to exit 5.

## §10: custody of the claim

The claim is server-side state, judged by the caller's token. The receipt names no claim id, so the CLI
has nothing to hold, store, put in argv or log. Its only local identity material is still the worktree
binding cache, written 0600 by `config.WriteFileAtomic` as before. A CHOOSE answer lists only the
caller's own sessions (the server keys every read on the caller's user). The unbound refusal is a fixed
string that names no worker, session or claim.

## Out of scope

* The shed and rotation tooling switching over to these commands: hadron-server#1958 (Hugo and Yuri).
* The docs: docs#414's CLI half.
* A launcher-set selector for CLI workers with no directory: still the recorded path from the original
  issue, and not needed yet.
