# `host run` from the platform (hadron-cli#3237, C3 of hadron-server#5209) — design as built

Status: BUILT on `jonas/3237-host-run-platform`, for review. Also carries hadron-cli#3285 (accounts-file hardening,
the stamped precondition) and hadron-cli#3288 F8 (`EnsureHostStateDir`), both as its first commits.

## Built so far

1. **#3285** (`internal/hostfacts/accounts.go`, `accounts_trust_unix.go`): `LoadAccounts` Lstats and refuses a
   symlink or a non-regular file; on Unix it refuses a file another user owns, a directory above it that is
   group/world-writable and not sticky or owned by another user, and a symlink on the way another user owns
   (the poller's reload-trust walk); it reads through `O_NOFOLLOW` and only the file it judged (`os.SameFile`).
2. **#3288 F8** (`internal/config/paths.go`): `EnsureHostStateDir` tightens an existing `hosts` / `hosts/<id>`
   wider than 0700 and refuses a symlink there; on Windows `HostStateDir` refuses CON/PRN/AUX/NUL/COM1-9/LPT1-9
   (any case, any extension) and a trailing dot or space. `hostid.Valid` is unchanged (a platform id).
3. **Poller core** (`internal/hostpoller/platform.go`, `accountresolve.go`):
   - `BuildPlatformConfig(app, PlatformSettings, []Assignment, PlatformFacts)`: workers keyed by worker id;
     `Worktrees` holds the workdir only when `hostfacts.ResolveWorkdir` finds exactly one tree; `WorktreePath`
     returns Orca's path (or "" — `WorkdirRefusal` gives the coded reason); disk temp under
     `<state>/hosts/<id>/tmp/<workerId>`; state file `<state>/hosts/<id>/hadron-host-state.json`; interval null =
     30; delivery null = on, `AckMixed` true (product default); no capabilities.
   - `Poller.SetPlatformRefresh`: each `Step` builds the Config instead of `reloadConfig`; `ErrSetupSourceHost`
     holds (no register/collect, said once per flip, Q1 (a) mid-run); other errors end the Step, Config kept.
     `State.SetFromPlatform` makes a worker's trees only the resolved ones. The interval is read after each Step.
   - `AssignmentProfiles` (ProfileSource + `AccountResolving`): start's family/account name, else the
     assignment's; `AccountLookup.Resolve` (R7, Q5 (a) canonical equality, no fallback) with
     `LaunchRefusal{Code, Msg, Local}` — `Msg` (reported) names only; `Local` is logged on the host.
   - `AccountProfile.ClaudeConfigDir` (`yaml:"-"`) → `CLAUDE_CONFIG_DIR` in the launch env; Codex → `CODEX_HOME`.
   - `SetScrubKeys` / `agentEnvKeys`: every stored key scrubbed and redacted.

4. **Stage B** (33fe6f5b; pins in `internal/cmd/team/host_run_platform_3237_test.go`) `internal/cmd/team/host_run_platform.go` + `internal/api/queries/host_run_platform.graphql`:
   `runPlatformHosts` (no `--config` → this path; `cmd.Flags().Changed("config")`), seams in `platformDepsFor`
   (goos, credentials, facts, wire, delivery); `narrowEntries` (--app = App id, --server); `startPlatformHost`
   (myApp id check exit 3, source HOST exit 5, `platformNeedsV1` names server 0.47.0, `EnsureHostStateDir`,
   `SetScrubKeys`, `SetPlatformRefresh`, `wirePlatformOrca` in host_orca_launch.go, delivery wired once and
   gated in `deliverStep`); `runPollers` (Q4 a). `TestTeamHostRunRefusesABadConfig` updated (no --config → exit 3).
   `Poller.ScrubKeys` (exported) lets the command pin read what each poller scrubs.
   Pins (written after the code, each guard proved by a killed mutant: myApp check, source check, scrub-all,
   narrowEntries --app and --server, Q4 one-host-fails, schema-skew mapping, store refusal exit 3): two assigned
   workers register by worker id with their resolved worktree token; no request carries a local path, a config
   directory, a program or the person's token; host key only, no session; myApp mismatch / HOST_NOT_FOUND /
   HOST source (exit 5, nothing registered) / a server without V1 (names 0.47.0) refused naming the fix with no
   key on stdout, stderr or --json; two credentials run two pollers, each on its own key, each scrubbing both.
   The --config golden is `TestTeamHostRunOnceUsesOnlyTheHostKey` unchanged: exactly register/collect/report, and
   its fake fails any other operation, HostRunPlatform included.

5. **Stage C** (`internal/hostpoller/orcaworktree.go`, `orcaterm.WorktreeCreateGuard`): on a platform host
   `worktreeCreate` never reaches `c.Repo` / `WorktreePath` (a new token has no path there; it would have
   inspected and `git worktree add`ed at ""). The repo arg's last segment is the Orca repository displayName and
   the token's repo part, as under --config. Order: Orca's repos (unknown → names `orca repo add`; a displayName
   two repos share → refused); the token on Orca's live list (on the branch → SUCCEEDED, no create, the
   re-delivery case; another branch or ambiguous → FAILED); else `orca worktree create --repo name:<displayName>
   --name <label> --branch <b> [--base-branch <base>] --no-parent --setup skip --json` through the closed guard,
   then the list is read again and the outcome judged on it, not on the create's answer: `--branch` takes a
   numeric suffix when the name is taken, so a tree on another branch is FAILED naming both. `--no-parent`
   because Orca otherwise parents the tree to the caller's inferred context. Failures carry no Orca output; an
   ambiguous token names its candidates (as a start's refusal does). Once the create was asked, a failure never
   says "nothing was created": an unconfirmed create or an unreadable list after it says the tree may exist.
   `SetOrcaWorktrees(facts, run)` wires it (a nil half leaves it unwired: FAILED, nothing created);
   `platformDeps.worktreeRun` builds the runner on the found Orca binary (scrubbed env, 5 min bound). In platform
   mode the created token is recorded but a worker's trees stay its resolved assignment (`configuredOnly`).
   Pins `orcaworktree_3237_test.go` + the command wiring pin; 18 mutants, all killed.

6. **Stage D:** `host run` help (`Use: run [--config <host.yaml>] [--once]`, a WITH NO --config section at the
   top of Long, the --config flag usage, the worktree.create line), the `team host run` entry of agentic-usage
   (worded without other commands' `--flags`: the synopsis checker attributes every `--x` on the line to its last
   command), `make help-inventory`, release note `docs/release-notes/host-run-platform-3237.md` (server 0.47.0).
   `coverage-matrix-check` is in sync unchanged: the matrix names one command per field and `agentTeamHost`,
   `hostSetup` and `myApp` already have theirs.

## Review pass (hadron-cli review-changes, all 48 nodes) and what it changed

- A create Orca was asked for no longer reports "nothing was created" (pinned: list unreadable after the create).
- `narrowEntries`: `--server` matches by host name, ignoring case, as `hostcred.Entry.Matches` does; `--app` as
  a URN or `<root>:<slug>` is exit 2 naming the App id (the store keeps ids only).
- Events of several hosts carry `hostId` (`--json`; `[<hostId>]` in text) and a time when the emitter set none.
- "Nothing local is sent" was too broad: an ambiguous workdir's refusal names its candidate paths, which #3237's
  amendment asks for ("naming the candidates and the fix"). The help, agentic-usage and release note now say
  exactly what does not leave the machine (an account's program and directory; paths in registration).

## LOCAL review at 8bc735a4 (Joran, 6 P2 + 1 P3) and what it changed

- **F1 relaunch containment.** `relaunchTarget` required the tree inside `EvalSymlinks(WorktreesRoot)`, which a
  platform Config does not have, so no platform worker could relaunch (and the agentless-end sweep, which uses
  it, never ran). `containedWorktree(c, token)`: under `--config` the old rule; on a platform host the token's
  resolved Orca path with symlinks resolved, and none for a token that did not resolve. Pinned through
  `relaunchTarget`.
- **F2 worktree.remove of a tree no assignment resolves.** A tree the worker made with `worktree.create` is never
  an assignment's, so `WorktreePath` is "" for it. The remove now finds the token on Orca's live list
  (`platformRemoveTree`, the create's `orcaTree`): not listed is already removed (SUCCEEDED, as an absent tree
  under `--config`); unreadable or ambiguous refuses, saying nothing was removed. Ownership on a platform host is
  the worker's assigned workdir or a tree recorded as created for it (`State.createdBy`; `configuredOnly` keeps a
  created tree out of starts and registration only); a tree another worker is assigned, or one the worker neither
  is assigned nor created, is refused with a removal message. A worker the platform no longer places here is
  refused as not configured, as before: the host has no record that the tree was ever its own.
- **F3 Q4 (a) exit codes.** A host that fails to start now joins `runPollers`' selection (`hostSlot`): `--once`
  takes the worst code of the pass, otherwise the first host in credential order that ended in error. "no host
  could start" (exit 1) is gone: with every host refused the exit is that selection. The test that pinned exit 0
  for one refused host of two now pins 5.
- **F4 transient start reads.** `readPlatform` marks a failure the server did not refuse (`errTransientStart`:
  any code but 2/3/4/5/8 and not schema skew). Without `--once` that host retries its start in its own goroutine
  (`retryStart`, `platformStartBackoff` 5 s doubling to 5 min), says `host-start-retrying` once per episode and
  `host-started` when it starts, then runs; a fixed refusal on a retry ends it. With one host and no `--once` the
  process no longer exits on such a read. `--once` never retries.
- **F5 a workdir assigned twice.** The server keys an assignment by (App, worker, host), so two workers can be
  placed on one workdir. `BuildPlatformConfig` leaves such a token unresolved for both, with
  `WORKDIR_ASSIGNED_TWICE` naming the workers (new in `hostfacts`), so `CheckWorktreeAliases`' rule holds.
- **F6 pins**, each with a killed mutant: the remove's empty-path guard (a stub `Inspect("")` that would pass),
  `SetFromPlatform` (a created tree is not registered), the assignment `hostId` filter, scrub-all in the end
  prompt's env, the archived-worktree skip, the poll interval bounds, the `relaunch_exit` empty path (a stamp in
  the test's cwd), `ScrubbedEnv` on the create runner (a fake `orca` printing its env).
- **BLIND N2 (from the delta at 8bc735a4).** `stopWorkerProcess` read the live Config's path per call, so a
  platform poll landing between the check that allows a stop and the scan could send the scan to "". It now takes
  one Config for the whole stop (pin: `bound` swaps the Config mid-stop; the scan still gets the allowed path).
- **N7** is stated in the release note's Limits: a platform worker has no end prompt and no `stop_on_end`
  until C4, so `worker.end` waits the 15-minute default, reports FAILED and stops nothing.

## Deltas at f69e3655 (BLIND N-A/N-B, LOCAL D1/D2, SECOND P3) and what they changed

- **N-A one id, first entry refused.** `started[hostId]` is recorded only when a start succeeds or will retry
  (`transientStart`), so an entry refused for good (another host's claim, a revoked key) leaves the id to a later
  entry of it, which may be the claim's holder.
- **N-B / SECOND P3 the claim race.** `claimHostState` writes the stamp to a temp file in the directory and
  `os.Link`s it into place: the link fails on an existing file, so of two processes exactly one wins and the
  other reads the winner's claim; a reader never sees a half-written stamp.
- **D2 the claim outlived `leave`.** `team host leave`, once the key is deleted, removes `hosts/<id>` when its
  claim names the host it left (`releaseHostState`); another host's directory, or one with no claim, is kept, and
  a failed removal is a warning (the leave has happened). At run, a claim whose (server, App) no stored
  credential holds is refused naming the directory to remove (its state is not the new host's to inherit), not
  `team host leave`, which could not clear it.
- **D1 remove of an archived tree.** `platformRemoveTree` matches archived trees too (an archived tree may still
  be on disk; every check then runs on it) and reports a token absent only when its repository is listed and no
  tree of it has the name; an unlisted repository fails, nothing removed, the created record kept.
- **LOCAL P3-1.** A workdir assigned twice is refused for `worktree.remove` too (`WORKDIR_ASSIGNED_TWICE`),
  before the Orca lookup, so not even its creator removes it.
- **LOCAL P3-2 pins.** Every refused code of the start read (2/3/4/5/8) is pinned as not retried; a retry
  refused ends the retry; the claim compares server as well as App.

## Deltas at 10bf3a7b (BLIND) and what they changed

- **N-A, the transient half.** The claim is judged locally before the platform read (`peekHostClaim`, sharing
  `judgeHostClaim` with `claimHostState`), so a host whose server is down is refused for good by another host's
  claim and never takes the id; an unclaimed directory is still claimed only after the read.
- **P2-1 same host, D-1.** A started host holds `<state>/hosts/<id>/host-run.lock` (gofrs/flock, `TryLock`) for
  the life of the process; a second `host run` of it is exit 5. `leave` deletes the directory only if it can take
  that lock (released before `RemoveAll`, which Windows needs), else keeps it with a warning.
- **P2-5.** Each host reads Orca through its own copy of `hostfacts.Facts` (`hostSlot.facts`).
- **D-2.** Without hard links (`os.Link` failing other than "exists") the stamp falls back to an `O_EXCL` create.
- **D-3.** `platformRemoveTree` matches as `ResolveWorkdir` does (display name, which may hold "/", then one folder).
- **D-4, N-I.** The `leave` help says when the directory is deleted; `host run` help names the orphan claim and the
  run lock (both exit 5).

## Left for later (out of scope here)

- The rest of #3288 (F8 only here; the task AT0.11114.1 builds the rest after this merges).
- C4 (worker behaviour with no per-worker file setting), C5 (the other readers of the host file), C6 (the
  service install), X2 (deleting the loader).
- `reset_on_start` stays off until `cor:agt:070:06` is confirmed.

## Schema

No refresh: the committed snapshot (server `a5f79802`, from C1) already has `agentTeamHost`, `AgentTeamHost`
and `hostSetup.assignments.accountProfile`.
