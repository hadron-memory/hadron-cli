# `team host doctor`, machine facts and the accounts file (C2 of hadron-cli#3236)

Slice C2 of "Host file removal" (hadron-server#5209). Design as built. It consumes the measurements and the accounts-file
format of hadron-server#5210 (`docs/plans/5210-host-file-removal-s1.md`).

## What changed

A host used to read everything about its machine from a hand-written `host.yaml`. Nothing in the CLI detected those facts. Now
`internal/hostfacts` detects them when they are needed, and `hadron team host doctor` reports them. Nothing is cached and nothing
is read from a host file.

- **`internal/hostfacts`** (pure functions over an injected runner and lookups; each returns the value or a `*Missing` carrying the
  fix text): `OrcaBin`, `OrcaVersion`, `Repos`, `Worktrees`, `AgentProgram(family)`, `StateDirOnDisk`, `ResolveWorkdir`, and the
  problem-code constants `WORKDIR_NOT_ON_HOST`, `WORKDIR_AMBIGUOUS_ON_HOST`, `ACCOUNT_NOT_ON_HOST`, `PROGRAM_NOT_ON_HOST`
  (hadron-cli#3237 imports these names and this package).
- **The accounts-file loader** (`hostfacts.LoadAccounts`).
- **`hadron team host doctor [--host <id>] [--json]`.**

## The facts

| Fact | How it is found |
|---|---|
| Orca | `orca` on `PATH`, else `~/.config/orca/cli/bin/orca` (Linux) or the app bundle (macOS). Missing is "unreachable": exit 10. |
| Orca version, repos, worktrees | `orca --version`, `orca repo list --json`, `orca worktree list --json --limit 5000`. |
| Agent program per family | `claude`, `codex`, `claude-glm`, `claude-kimi` on `PATH`. |
| State directory | `$XDG_STATE_HOME/hadron`, else `~/.local/state/hadron`; on tmpfs or ramfs it is MISSING (it is emptied at reboot). Linux only; elsewhere the kind is unknown and counts as on disk. |

The runner is closed over those three Orca reads (`hostfacts.Guard`), so a doctor run can never call `terminal`, `account` or
`send`, and the child environment is scrubbed of every `HADRON_*` variable and provider key (`ScrubbedEnv`).

**A finding worth keeping.** `orca worktree list --json` returns 200 rows by default and answers `truncated: true` past that (280
rows on orca1 today; #5210 measured 200 of 200). Resolving a workdir against the first 200 would report a real worktree as missing.
So the call asks for 5000 rows, and an answer Orca still marks truncated is refused as unreachable, never read as "not found".

**A workdir token** is `<repo displayName>/<basename of the worktree path>` joined on the worktree's repo (#5210: the worktree's own
`displayName` does not match). One match is found; none is `WORKDIR_NOT_ON_HOST`; more than one is `WORKDIR_AMBIGUOUS_ON_HOST` naming
every candidate, and none is picked. An archived worktree never matches.

## The accounts file

`~/.config/hadron/accounts.yaml` (the CLI config directory on Linux and macOS, mode 0600, refused if a group or world bit is set),
optional, read at the moment of use. Keys `accounts.<name>.{family, program, configDir}`; a name is `[a-z0-9][a-z0-9_-]{0,63}`; the
family is `claude`, `codex`, `glm` or `kimi`; `program` and `configDir` are absolute paths. Accounts only: a `workers`, `worktrees`,
`repos` or host-settings key is refused, naming the file, entry and key. `tool` and `version` are **reserved** and answered "needs a
newer hadron", never "unknown key".

Taking a `tool` key later (hadron-cli#3227) invalidates nothing: an absent `tool` means the tool the family implies (`claude` for
`claude`, `glm` and `kimi`; `codex` for `codex`), implemented in the one function `Entry.Tool()`. Before and after:

```yaml
# valid before and after a later hadron knows `tool`, and means the same
accounts:
  work:
    family: claude
    configDir: /home/you/.claude-work
```

Only names leave the machine: nothing in this file is sent to the platform, and `doctor` sends no program, directory or any other
account detail in any request (pinned on the recorded requests).

## `team host doctor`

One line per fact (found, or missing with the step that fixes it), then each accounts-file entry (its program and directory),
then with `--host` each assigned worker: its workdir, account and program.

- **`--host`** is optional (gate Q1a). It makes the one existing assignments read (`TeamHostSetupWithProfile`, falling back to the
  S1a document), as the signed-in person or through the worktree binding, never a host file's key. It sends only `appRef` and
  `hostId`. Without `--host` the command is fully offline. It is refactored out of `team host setup` as `fetchHostSetup`; that
  command's output is unchanged.
- **Under `hostSetupSource: HOST`** it prints one line ("App does not take its setup from the platform") and checks no worker.
- **Accounts:** no account named is "default" (claude, glm and kimi use `~/.claude`; codex uses Orca's active managed Codex
  account: #5210) and never a complaint; a server that predates the S1b profile leaves the account "unknown", never absent. A named
  account must be in the file, with the worker's family and a present program and `configDir`; an entry with no `configDir` has
  nothing to check.
- **Exit:** 0 everything checked is fine; 5 something is missing; 10 Orca is not on this machine or did not answer, and 10 dominates 5.
  Without `--host` no worker is read, so exit 0 means the machine's own facts only and the last line says so (`workersChecked` is false in
  `--json`). A family program not found is `NOT_FOUND` (informational: without `--host` nothing says a worker needs it; with `--host`
  no assigned worker runs it by default) and `MISSING` only when an assigned worker runs that family's default program (a worker whose
  program comes from its account entry does not make the default needed). A broken accounts-file entry nobody uses is 5 (the user
  declared it); a refused file is 5 and lists no entry as if it counted; a tmpfs state directory is 5 (gate Q5).
- **Codes:** a worker gets `WORKDIR_NOT_ON_HOST`, `WORKDIR_AMBIGUOUS_ON_HOST`, `ACCOUNT_NOT_ON_HOST` (naming the cause) and
  `PROGRAM_NOT_ON_HOST`; an accounts-file entry gets `ACCOUNT_PROGRAM_NOT_FOUND` or `ACCOUNT_CONFIGDIR_NOT_FOUND` (exported from
  `hostfacts` with the others, so C3 shares them).
- **`--json`** prints the whole DTO at exit 0, 5 and 10 (gate Q7): `{orca, facts, accountsFile, accounts, setup, workers, ok}`; empty
  lists are `[]`, absent values `null`.
- **A failed assignments read** (gate Q6) prints the local lines, writes the error to stderr, and exits with that read's own code (3,
  7 or 8): `setup.read` is `FAILED` in the JSON.

## Not in this slice

macOS: the recorded Orca JSON for macOS needs a Mac (hadron-server#5210 U5), so those cases are marked pending; where Orca's CLI lives on
macOS is covered by the search-order table. `host run` taking its workers from the platform (C3), joining and the credential store (C1,
hadron-cli#3239, which also adds the shared host-id rule and state-directory helpers: whichever merges second merges main), the service
install (C6), and any server or Orca change. `registerHost` carries nothing new.
