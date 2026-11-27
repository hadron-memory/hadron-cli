# `host run` from the platform (hadron-cli#3237, C3 of hadron-server#5209) — design as built

Status: IN PROGRESS on `jonas/3237-host-run-platform`. Also carries hadron-cli#3285 (accounts-file hardening,
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

## Still to build

- Stage B, `internal/cmd/team/host_run.go`: no `--config` → platform mode (Linux only); `hostcred.Load`, no entry →
  exit 3 naming `team host join`; `--app` narrows; one poller per entry (R6 recommended), each `SetScrubKeys` of
  all keys; query `HostRunPlatform` (agentTeamHost + hostSetup + myApp); start checks (myApp id == entry AppID,
  agentTeamHost answers, source SERVER else exit 5 naming SERVER); skew → refuse naming the V1 release; Q4 (a)
  one fatal stops that poller only; `EnsureHostStateDir`; delivery wired once, gated per step.
- Stage C: `worktree.create` through `orca worktree create --repo name:<displayName>` with a closed guard.
- Stage D: help text, agentic-usage, help-inventory, coverage matrix, release note.

## Schema

No refresh: the committed snapshot (server `a5f79802`, from C1) already has `agentTeamHost`, `AgentTeamHost`
and `hostSetup.assignments.accountProfile`.
