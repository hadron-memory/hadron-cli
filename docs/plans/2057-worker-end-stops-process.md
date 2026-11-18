# worker.end stops the worker's process (C2a of hadron-cli#2057)

Slice of the Team on Orca epic (hadron-server#4229). Design as built.

## What changed
`worker.end` used to prompt, wait `end_wait` (15 m) and report FAILED; it never
touched the process. With the per-worker host-config field `stop_on_end` it now
also stops the worker's agent.

- `stop_on_end: never` (default, today's behaviour) | `manager` (only when the
  operation's issuer is a USER: a person who manages the App, not the
  PLATFORM's own and not a bound reports-to WORKER) | `always` (a short-lived worker). `WorkerConfig.stopsOnEnd`
  is the only lookup: **transitional**, removed with `host.yaml` (C4); S3's
  `Agent.lifecycle` replaces it. Decision recorded by Wolfgang (#107617).
- Wait: after a *delivered* end prompt, `stop_wait` (default 60 s) instead of
  `end_wait`, cut short if the launched agent exits. No end prompt, or one that
  failed: nobody was asked, so the stop is immediate. The host cannot read
  "session ended", so the bound is all it has.
- Outcome code unchanged: still FAILED, the server records an ended session as
  success (cor:agt:020:10: the host ends no session). The stop result (pid,
  SIGTERM/SIGKILL, or "not touched" and why) is in the report's reason.
  A session still open when its process is stopped stays open: S2
  (hadron-server#4223) ends it server-side.

## Which process
1. The agent this poller launched: `Agent.Terminate()` (its own launch group;
   records dropped only on a confirmed stop), after the same binding check.
2. Otherwise (a `worker-supervise` terminal agent, or a launch from before a
   restart): `ScanWorktreeAgents` (#2027's /proc cwd census, not a second
   implementation) over the worker's registered worktrees. A tree is skipped
   when its session binding names another worker, another session than the one
   being ended, or cannot be read. No binding (the worker ended its session) is
   the worker's own tree by this host's config.

## How it stops
`ExecHost.StopAgents` (Linux): every signal through a pidfd, with the start time
re-proved, so a reused pid is never signalled. The agent's process group is
signalled as a group only when every member works inside the worktree and none
is the caller or its ancestor; otherwise only the agent process, and the detail
says so. SIGTERM, `TerminateGrace` (5 s), SIGKILL, 2 s. Other platforms: no
`/proc`/pidfd, nothing is touched and the report says so.

## Not in this slice
C2b (workspace reset inside `worker.start`), C2c (wire `worker.reset-context`),
the server ending a session whose process was stopped (S2), a spec change
(client-only host config; the rule it serves, cor:agt:050:10, is unchanged).

## Review round 1 (at 3a46c23a)
- A launched agent is held to the same binding rules as a scanned one.
- The binding is asked again immediately before the first signal (`still`
  guard into `StopAgents`). A separate process writes the binding, so the
  window cannot be closed, only made as short as the signal.
- "Gone" is a definitive absence only (ESRCH / no /proc entry); a failed pin or
  probe is "not touched" / unconfirmed, never "stopped".
- The lead process's current directory is re-checked against the worktree.
- `manager` admits USER only.

## Follow-ups #2068 and #2070
- **Lock (#2068).** `delivery.LockBinding` takes the same `hadron-team-session.lock`
  `session start` writes under. Session writers block on flock / LockFileEx
  without a deadline; the STOPPER's acquisition is a bounded try-lock (2 s). Every
  signal is preceded by `Authorize`: take the lock, re-read the binding under it,
  on Unix signal and release at once, before the SIGTERM grace or any wait.
  On Windows the authorization instead spans the synchronous taskkill command,
  including waiting for it to return (bounded cancellation described below). On Unix
  `execAgent.TerminateWith` asks before SIGTERM and again before
  SIGKILL. A lock that cannot be taken declines the stop and the report says so.
  A plain directory without a binding cache creates and locks that cache before
  checking the binding only when the Unix host uid matches the worktree owner
  (#2076). The same owner proof is required to create a missing lock file in
  an existing cache; existing locks are not subject to this proof and are
  opened under ordinary file permissions (a user the permissions exclude is
  refused by the OS). An unreadable owner declines creation. Windows behavior is unchanged.
- **Session identity (#2070).** The reset relaunch records the session the reset names;
  a launch the server had already finished has none recorded, so only a binding that
  names this worker AND the session being ended identifies it (an empty binding declines).

## Security review (§10) on #2073
- The stop creates and locks inside a tree the worker can write, so `LockBinding`
  follows no link: `Mkdir` (not `MkdirAll`) for a plain cache, `Lstat` the lock
  location and the lock file (a link or non-regular file is refused), `O_NOFOLLOW`
  on unix, and an `fstat` regular-file check after the open.
- **Residual, stated plainly:** the stop is fail-safe, not containment. A hostile
  agent can still decline its own stop (hold the lock for the 2 s bound, rewrite
  its binding, move its working directory); each of those makes the host signal
  nothing and say so. The host also trusts the worktree's `.git` pointer, as every
  binding reader here already does; on orca1 the poller and the agents share one uid.
- **Open without a check-then-open gap (blind delta).** On unix the lock is opened
  relative to a descriptor for its directory (`O_NOFOLLOW|O_DIRECTORY`, then
  `openat(..., O_NOFOLLOW)`), so a directory swapped for a link after the check is
  refused. On Windows each open uses `FILE_FLAG_OPEN_REPARSE_POINT` and the handle's
  attributes are checked, and the directory handle stays open (no delete-sharing) until
  the file is open and checked, so the directory cannot be renamed or replaced between
  the two opens. Not run on a Windows host (none available): it builds and vets clean,
  and the reparse-attribute rule is pinned as a platform-neutral unit.

## #2082: authority is proved again after the lock wait
`signalAuthorized` re-checks `stopAuthorityGone()` once the authorization (the binding lock, which can wait up
to 2 s) is held and immediately before the signal. On a launch without the zombie hold the leader's exit frees
the group's number unserialized with a stop, so a leader that exits during the wait could otherwise leave the
numeric group signal to reach a reused group. A lost authority signals nothing and reports the stop unconfirmed,
records kept.
Residual, by design and already documented in `team host run`'s help: on a launch that could not take the zombie
hold (a kernel or policy that refuses `waitid`, and the non-Linux unixes), the instant between the final
authority check and the `kill(2)` itself is not serialized with the leader's exit; the 2 s lock-wait window
that #2068 added is closed by the check above, and a hold-capable launch has no such gap.

## #2099: Windows checks exit after authorization too
The Windows launch stop checks `Done` and the waiter's recorded exit again after
authorization returns, before `taskkill /PID /T /F`. A launch that exited during
the binding-lock wait is reported as already exited, with no process touched;
the authorization is released and taskkill is not run. The portable decision
is shared with Windows production code so its interleavings run on Linux tests;
the Windows adapter has a separate test, cross-compiled but not run here.
This does not retain a process handle or establish identity atomically with
taskkill: the remaining final-check-to-taskkill PID-reuse window and descendant
tracking after the leader exits still need a handle/Job Object design. The
binding lock continues to span synchronous taskkill, as described for #2087 below.
No Windows runtime or PID-reuse reproduction is claimed.

## #2087 and #2106: Windows command lifetime and test coverage
Keep binding authorization until the taskkill runner returns. Releasing before
starting it would reopen the binding-versus-kill race: a new session could bind
while the old session's numeric taskkill is still about to act. Unix's short
signal syscall followed by a separate grace wait is a different boundary.

Taskkill receives a cancellation deadline of 10 seconds. The Windows command
uses CommandContext to terminate the helper on cancellation, with a 1-second
WaitDelay for retained output pipes. These request cancellation and limit pipe
draining; they are not a hard real-time guarantee about OS scheduling/reaping.
Authority is NOT released on a timer or by an abandoned runner goroutine: the
runner must return first. A deadline makes the stop unconfirmed and keeps its
records, even if the runner returns a successful exit status afterward. Taskkill
may already have killed some targets when cancellation happens; the timeout
report does not claim that nothing was touched: the tree may already be dead
or partly stopped. Available runner error and trimmed output are retained as
bounded, control-safe single-line context on cancellation. If the helper exits
successfully but inherited output pipes remain open past WaitDelay, Go returns
exec.ErrWaitDelay: output collection is incomplete, the stop is unconfirmed and
records remain. Cancellation observed after a successful runner return still
wins, including when ErrWaitDelay also occurs. A session start waiting on the
binding lock can wait through synchronous taskkill cancellation/drain; the
stopper's 2-second acquisition limit is not a writer timeout or a hard 11-second
stop bound. Live success and ordinary failure still propagate as before;
refusal still invokes no runner.

The shared stop decision is named stopTreeAuthorized. One helper describes an
exit observed after authorization without reading the waiter's mutex-protected
exit status: the waiter can be blocked on that mutex after publishing exited.
The pre-existing already-Done path deliberately remains silent success. Portable
tests cover nil authorization and success/failure propagation, a blocking runner
through deadline and return, and the REAL no-hold waiter using a private child
process while its exit publication is held before Done. Windows adapter tests
pin both exit states and an injected blocking taskkill with its deadline and
held/released authorization. These adapter tests are cross-compiled here; no
Windows runtime or measured taskkill latency is claimed. The final-check-to-
taskkill numeric-PID race still requires a held-handle/Job Object design.

## C2b: workspace reset inside worker.start (G6)
Behind `reset_on_start` (default off; transitional like `stop_on_end`, one lookup `resetsOnStart` that
`Agent.lifecycle` replaces). It runs inside `worker.start` after every check that no process works in the
tree and before the launch, so nothing is moved under a running agent and no new operation type exists.
`ResetWorktree` (internal/hostpoller/gitreset.go): not a Git tree: no reset. Dirty or untracked:
`WORKTREE_DIRTY`. Then fetch origin (a failure is `WORKTREE_RESET_FAILED`, never a guess), record
`refs/hadron/rescue/<utc>/<branch>` for every local branch tip and a detached HEAD that no remote branch
contains, refuse with `WORKTREE_UNPUSHED` when HEAD itself is on no remote branch (`git branch -r --contains`;
the upstream/ahead rule of ruling 0012 would wrongly refuse a detached HEAD left at origin's tip by an earlier
reset), then `checkout --detach origin/HEAD` (else origin/main). A refusal reports its code as the start's
failure, so the task waits with the not-started alarm. Nothing is created or removed (cor:agt:070:06).
Residual: a squash-merged and deleted remote branch reads as unpushed and refuses (the safe direction; the
team merges with merge commits).

### C2b review round 1 (at 615cf4b9)
- **Base by commit id.** The base is the remote's CURRENT default branch, read with `ls-remote --symref` (a fetch never
  refreshes the cached `origin/HEAD`), qualified as `refs/remotes/origin/<branch>` and resolved to a commit id that
  is checked out detached: a local branch named `origin/main` is never taken for it. A default that cannot be read or
  was not fetched refuses.
- **Rescue refs.** One `rev-list --no-walk --branches --not --remotes` finds every unpushed tip (a shared clone has
  hundreds of branches); a commit already kept by an earlier rescue ref is not recorded again; branch tips live under
  `.../heads/<branch>`, a detached HEAD under `.../detached` (no name collides); every ref is created with an all-zero
  old value so none is ever replaced, a taken name gets `-2`, `-3`.
- **Worker-writable repository.** Every git run in the tree carries `core.hooksPath=<null>`, `core.fsmonitor=false`,
  `credential.helper=`, `core.sshCommand=ssh`, and `GIT_TERMINAL_PROMPT=0`; the fetch and the remote read are bounded
  (2 min). A hook or config line planted in the repository never runs as the poller's user.
- Residual, accepted: the occupancy check and the checkout are not one atomic step; a process that begins working in
  the tree in between meets a tree that was clean and pushed, the same window the launch has always had.

### C2b review round 2 (at 4fe02dff): repository config, class by class
The reset's git runs against a tree the worker writes. The user's own global/system git config stays (a private
remote's credential helper lives there, and clearing it or pointing the global config at /dev/null would break the
fetch); what the REPOSITORY's local and worktree config adds is handled by class:
- Overridden for every run: `core.hooksPath`, `core.fsmonitor`, `core.sshCommand`, `core.askPass`, `core.gitProxy`,
  `protocol.ext.allow=never`, submodule recursion, `gc.auto`, `maintenance.auto`; `GIT_ALLOW_PROTOCOL` allows only
  file, http, https, ssh and git (no `ext::`); `GIT_ASKPASS` and `SSH_ASKPASS` empty; `GIT_TERMINAL_PROMPT=0`.
- Enumerated from the repository's own config and overridden, per driver: `filter.<name>.smudge|clean|process`
  (and `required=false`), `diff.<name>.textconv|external`.
- Refused with `WORKTREE_RESET_FAILED`, before anything is touched, because they cannot be neutralised: a
  repository credential helper (an empty one clears the user's own list) and `remote.<name>.uploadpack|receivepack|
  proxy|vcs` (git keeps the FIRST value, so a later override does not replace it; found by probing).
One test per class: hook, filter, fsmonitor, uploadpack and its siblings, ext transport, ssh command, credential
helper; askpass is pinned structurally (a prompt cannot be provoked from a local fixture).

### C2b review round 3: the allowlist (Wolfgang #108163/#108170, Rook #108165/#108166)
The worktree's git config is untrusted; the reset allowlists rather than chasing names.
- **Transports:** `protocol.allow=never` with only `protocol.https.allow` and `protocol.ssh.allow` `always`, and
  `GIT_ALLOW_PROTOCOL=https:ssh`, in production. The reset's remote URL must be `https://`, `ssh://` or
  `user@host:path`; anything else (a local path, `file://`, `ext::`, `git://`, `http://`) refuses before the fetch.
  The tests' local fixtures run through a test-only seam (`localRemotesForTests`, flipped only in a `_test.go`).
- **Explicit commands:** `--upload-pack=git-upload-pack` on `fetch` and on `ls-remote` (git keeps the first config
  value, so a `-c` cannot replace the repository's `remote.*.uploadpack`; the command-line option wins);
  `--no-recurse-submodules`.
- **Refused, not overridden** (redirection poisons the clean base the next task starts from; no `-c` unsets them):
  a repository `url.*.insteadOf` / `pushInsteadOf`, `http.*proxy`, `http.*sslVerify=false`, `remote.*.proxy|vcs`, and
  a repository credential helper.
- The user's own global and system git config is kept (a private remote's credential helper lives there).

### C2b review round 4 (at ab8925f2)
- **Includes.** An explicit-scope `git config --local --list` does NOT expand `include.path` / `includeIf` (probed);
  the scan now passes `--includes`, so a filter or helper in an included file is seen. A config that cannot be read
  (a syntax error in an include, an unreadable scope) refuses the reset: nothing is taken for "no overrides".
- **TLS verification** is judged on the effective (last) value of each `http.*sslVerify` key, in every git spelling of
  false (false, no, off, 0, empty), included files and worktree scope after local.
- **Every git call the host makes** (`Inspect` before the reset, `worktree.create`, `worktree.remove`, the reset's
  own) now goes through the one hardened runner: `status` no longer runs a worker's clean filter, `worktree add` no
  longer runs its smudge filter, and a repository config that cannot be neutralised refuses the host's network
  commands (found by QA's compiled-CLI run).

### C2b review round 5: an allowlist, not a denylist (Wolfgang #108206)
The list kept growing (hooks, filters, uploadpack, includes, TLS, the pre-reset `status`), so the approach changed.
- **One gate for every git call the host makes in a worker tree** (`ExecHost.git`: `Inspect` before the reset,
  `worktree.create`, `worktree.remove`, and the reset's own): before it, the repository's own local config (and
  `config.worktree` when `extensions.worktreeConfig` is on) is read with includes expanded, and every key must be one
  of a small set of inert ones; anything else refuses, naming the key, before a command runs. `Inspect` reports it
  (`UntrustedConfigError`) rather than "not a worktree". A config that cannot be read refuses too.
- **The set:** `core.repositoryformatversion|filemode|bare|logallrefupdates|ignorecase|precomposeunicode|symlinks`,
  `remote.<n>.url|fetch`, `remote.pushdefault`, `branch.<n>.remote|merge|base|pushremote`, `push.autosetupremote`,
  `user.name|email`, `extensions.worktreeconfig`. Five are additions to the ruled list (`symlinks`, `base`,
  `pushremote`, `pushdefault`, `autosetupremote`): a survey of this host's clones shows every one carries
  `branch.*.base` and `push.autosetupremote`, so the ruled list alone would refuse every reset.
- The denylist (per-driver overrides, refusal classes) is gone: everything it caught is outside the set. What stays is
  the fixed hardening (`core.hooksPath`, `core.fsmonitor`, ssh and askpass cleared, the transport allowlist and
  remote-URL check), as defence in depth for what git reads from global and system config and from `.gitattributes`.

### C2b residuals (Wolfgang's ruling on Jamir's P1 and P3 at df95c30b)
- The allowlist is check-then-run: the repository config is read, then git reads it again when it runs. The reset runs
  inside `worker.start` before the agent launches, so only another process of the SAME user could change the config
  in between, and that process can already read the poller's environment and config; the real boundary would be
  separate uids, an epic-level question, recorded. Narrowed, not closed: the config is re-read right before the
  fetch's follow-up steps and right before the checkout (a key planted after the first read refuses there), and every
  host git call re-reads it.
- The occupancy check and the checkout are not one atomic step (the window the launch has always had).
- #2095 (contextsize, from merged C8) is not this PR's.

### C2b: the worker directory must be a worktree root (Julius's LOCAL P2, Wolfgang #108365)
`rev-parse --is-inside-work-tree` is true for a plain directory (or a subdirectory) inside some enclosing repository,
and a reset there would move that repository. `ResetWorktree` first requires `git rev-parse --show-toplevel` to equal
the worker's directory (symlinks resolved); otherwise the worker is a worker without Git: no reset, nothing refused,
and the enclosing repository's state, branch, rescue refs and config are not touched or judged. `Inspect` follows the
same rule (such a directory is not a worktree and the enclosing repository's config is not refused). Only a definite
"not a repository" or a different top level means "no tree"; an answer that cannot be read falls through to the config
gate, which refuses.

Correction after §10 (Rook, a4e836e8): the root is decided by the directory's own `.git` entry (a repository's `.git`
directory or a linked worktree's `.git` pointer file), not by `rev-parse --show-toplevel`, which honours the
repository's own `core.worktree`: a root whose config redirects its work tree elsewhere read as "not a root" and slipped
past silently. With the entry present the tree goes through the config allowlist, which refuses `core.worktree` by name;
a plain directory or a subdirectory of a work tree has no entry of its own and stays a worker without Git. A root whose
repository cannot be read fails closed (the allowlist read refuses), never a worker without Git.

### A broken .git is not a worker without Git (hadron-cli#2119)
The root check decides by the directory's own `.git` entry; an entry that is there must also work. An EMPTY `.git`
directory (no HEAD, config or objects) is skipped by git's repository discovery: standalone, git answered "not a
repository" and the reset took the no-Git branch (the agent launched into an un-reset tree); inside an enclosing
repository, git resolved the OUTER one and the reset fetched and checked out that (QA's reproduction over verified
HTTPS). Now, after the config allowlist, `ownRepoProblem` requires `rev-parse --show-toplevel` to succeed and to equal
the directory (symlinks resolved); otherwise `ResetWorktree` refuses `WORKTREE_RESET_FAILED` and `Inspect` returns
`BrokenRepoError`, never "not a worktree". A plain child with no `.git` of its own is still a worker without Git; valid
roots, linked worktrees and symlinked roots are unchanged; an invalid `.git` FILE already refused.

## Follow-up #2128: portable diagnostics and construction verification

The Windows default runner uses the shared taskkillCommand constructor so
portable tests inspect the supplied cancellation context, WaitDelay=1s and
exact taskkill /PID <pid> /T /F argument order. Injected runners pin cancellation
context and ErrWaitDelay without executing taskkill. Linux execution and
Windows cross-compilation/vet do not establish native taskkill behavior, OS
latency or PID-reuse safety. No held handle, Job Object or stop-policy change.
