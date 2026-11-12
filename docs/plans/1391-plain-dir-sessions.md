# Worker sessions from a plain directory

Design-as-built for hadron-cli#1391 (slice S2 of hadron-server#1800, "decouple the agent team from git").

## The problem

The server has held the binding since hadron-server#1956, and a fresh context recovers its worker with a resume claim. Since then, cor:agt:020:03 lets the per-directory binding file be an **optional cache**.

The CLI still needed a git dir to keep that cache. `session start`, a resume and `session shed` therefore failed outside a git checkout with "not inside a git worktree" (exit 2). A non-coding worker had to carry a whole repository checkout just to have somewhere to put one JSON file.

## The ruling

Ruling 0230 (Holger, 2026-11-06) chose **B, a file in the directory**:

- Outside a git checkout the cache lives in the directory itself, under the same lock.
- A directory binds one worker. A second worker's start there is refused, never shared.
- The file's name and place inside the directory are the builder's choice.

## As built

**Where the cache lives** (`internal/cmd/team/state.go`, `locateBinding`):

| context | cache directory |
| --- | --- |
| `HADRON_TEAM_GIT_DIR` set | that directory (unchanged) |
| inside a git checkout | the worktree's git dir (unchanged) |
| git answers "not a git repository", or there is no git binary | `<cwd>/.hadron-team/` (new) |
| any other git failure | none: the hard `errNoWorktree`, as before |

- `.hadron-team/` holds the same `hadron-team-session.json` and `hadron-team-session.lock` that a git dir holds. The directory is created owner-only (0700), and only by a bind.
- Only the **current** directory is consulted, never a parent. A command run in a subdirectory therefore cannot pick up the binding of a directory above it.
- A dubious-ownership refusal, or a corrupt repository, may sit inside a real worktree. That is why only git's own "not a git repository" makes a directory plain: reading a real worktree as plain would put its binding in the wrong place.

**Readers and writers differ on purpose:**
- A **read** (`readBinding`, `updateBinding`, `clearBinding`, `bindingIsSession`) sees a plain directory's cache only when the binding file already exists. When there is none, it gets `errNoWorktree`, exactly as before.
  - Every existing fallback keys on that error, so none of them changed: `whoami` falls back to the server, signing reads do the same, and the cor:agt:020:03 unbound-write refusal still refuses.
  - Nothing reads or writes a cache that isn't there.
- A **bind** (`writeBinding`, `readBindingForBind`, called by `session start` and the resume) reaches the cache directory, and the lock creates it.

**One worker per plain directory.**
- `session start --as <worker>` refuses **before any session is started or ended** (exit 5) when the directory's binding names a different worker. This includes `--force`, which would otherwise end the other worker's session and then share its directory.
  - First a local check, with no server call: a ref that matches the binding by none of name (case-insensitive), worker id or `hrn:worker:` URN's last segment is another worker, and refuses.
  - A local match proves nothing, because a name is App-scoped and a slug repeats across Apps (Gizelle's LOCAL F1 on #1968: another App's `Iris` passed, and `--force` ended the cached session before the lock-time check refused). So the start then resolves the named worker, with reads only, and refuses unless its id is the binding's worker id. A binding with no worker id refuses: sameness can't be shown.
- `writeBinding` re-checks under the lock, against a fresh read, by worker id. This backstop covers:
  - a launch grant, whose worker is known only after the bind (start rolls the bind back);
  - a resume (the session stays open, as every failed resume write leaves it);
  - two starts racing in one directory.
- A resume in a directory whose binding names a worker also sends that worker as `expectedWorkerId`, so the server refuses another worker's claim instead of consuming it (cli#1839).
- Worktrees keep their documented behaviour: a fresh bind replaces whatever binding was there.

**shed.** The cooldown stamp sits beside the binding (`shedStampPath`), in the git dir or in `.hadron-team/`.

## Acceptance → pins

| # | pin |
| --- | --- |
| 1 | `TestPlainDir1391StartBindsAndWhoamiReportsTheWorker` |
| 2 | `TestPlainDir1391ResumeRedeemsAndCaches`, `TestPlainDir1391ShedWritesCheckpointAndClaim` |
| 3 | `TestPlainDir1391UnboundPostRefusesWithNoWrite` (green on main by design: it pins the wall) |
| 4 | the existing session suites, unchanged; `TestPlainDir1391WorktreeStillReplaces` |
| 5 | `TestPlainDir1391SecondWorkerStartIsRefusedBeforeAnyWrite` (with and without `--force`), `TestPlainDir1391SecondWorkerIsRefusedUnderTheLock` |

More pins:
- `TestPlainDir1391ReadsSeeNoCacheAndCreateNothing`: reads stay `errNoWorktree`, and only a bind creates the directory.
- `TestPlainDir1391OtherGitFailuresStayHard`.
- `TestPlainDir1391BindWritesTheCacheUnderTheLockAndReadsBack`: 0700, the same lock file, update and clear.

`TestTeamSessionStartResumeOutsideAWorktreeRefusesFirst` is re-pinned from "no worktree" to "a git failure other than not-a-repository". That case keeps refusing before a claim is spent.

The tests use a `git` shim on `PATH`, so "plain directory" is decided by what git answers, not by where the test's temp dir happens to sit.

## Not done here

- **Issue ask 3:** the lifecycle manager's provisioning procedure gaining a no-checkout mode lives in Hadron role tasks, not this repo. It is routed to the coordinator.
- **Docs:** the hadron-docs pages that say a session needs a git worktree are a companion, filed once this contract lands.
- Host delivery's terminal matching by worktree (`internal/delivery`) is out of scope, as the issue states.
