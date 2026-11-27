# `team host run` with no `--config`: the host takes its workers from the platform

Unreleased (cli#3237, slice C3 of hadron-server#5209; also carries cli#3285 and cli#3288 F8).

**Minimum server version: 0.47.0** (hadron-server#5259, `agentTeamHost`). With no `--config`, an older
server is refused at start with a message that names 0.47.0. The platform read is a separate query, so
`team host run --config <file>` still works against any server.

## What is new

- **`hadron team host run [--once]`** with no `--config` runs one poller for each host this machine joined
  with `hadron team host join`. `--app <App id>` and `--server <url>` narrow the set. Each poll reads the
  host's label, poll interval and delivery switch, plus the workers the App placed on it, using only the
  host's key, with no session. A worker registers when its workdir (`<repo displayName>/<worktree folder>`)
  matches exactly one Orca worktree on this machine.
- **What a start runs with:** the start's own family, model, effort and account come first, then the
  assignment's. An account name is looked up in the accounts file at the moment of use, with no fallback.
  An unknown or unusable account fails the start (`ACCOUNT_NOT_ON_HOST`, `PROGRAM_NOT_ON_HOST`) and nothing
  is launched. The account's `configDir` is exported as `CLAUDE_CONFIG_DIR` (Claude) or `CODEX_HOME` (Codex).
- **`worktree.create` goes through Orca** (`orca worktree create --repo name:<displayName> … --no-parent
  --setup skip`). A repo Orca does not know fails and names `orca repo add`. Orca gives a taken branch name a
  numeric suffix, and a create that lands on such a branch is reported FAILED.
- **Nothing local leaves the machine:** no request carries a path, a program or an account directory.
  Every stored host key is removed from an agent's environment and redacted from what the host reports.

## Refusals at start

| Situation | Exit | Fix named |
|---|---|---|
| No joined host, or none matches `--app`/`--server` | 3 | `hadron team host join` |
| The stored key does not answer for its App, or the host was removed or its key revoked | 3 | `hadron team host join --replace` |
| The App's host setup source is `HOST` | 5 | `hadron team host setup --source SERVER` |
| Server older than 0.47.0 | 1 | upgrade the server, or use `--config` |

If one of several hosts cannot start, a `host-not-started` event reports it and the others keep running. If
the setup source flips to `HOST` mid-run, the poller registers nothing and waits, and says so once.

## Also in this release

- **cli#3285:** the accounts file is refused if it is a symlink, is not a regular file, is owned by another
  user, or sits under a directory others can write to (Unix).
- **cli#3288 (F8 only):** an existing `hosts/<id>` state directory wider than 0700 is tightened, and a symlink
  there is refused. On Windows, reserved device names never name a host state path.

## Unchanged

`team host run --config <host.yaml>` sends the same requests as before and never reads the platform row.
