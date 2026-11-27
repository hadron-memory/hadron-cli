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
- **What leaves the machine:** no request carries an account's program or directory, and workers
  register with workdir names, never paths. The one path reported is an ambiguous workdir's refusal, which
  names its candidate worktrees so they can be told apart. Every stored host key is removed from an agent's
  environment and redacted from what the host reports.
- **Events name their host:** with no `--config`, each `--json` event carries `hostId`, and text lines show
  `[<hostId>]` after the time. New kinds: `host-not-started`, `facts-unavailable` (once per Orca outage; the
  worktrees Orca last answered are kept) and `facts-available`, `setup-source-host`, `setup-source-server`,
  `assignments-changed`, `launch-refused` (the local detail of an account or program refusal).

## Refusals at start

| Situation | Exit | Fix named |
|---|---|---|
| No joined host, or none matches `--app`/`--server` (the server is matched by host name) | 3 | `hadron team host join` |
| `--app` given as a URN or `<root>:<slug>` (the store keeps App ids only) | 2 | the App id `team host show` prints |
| The stored key does not answer for its App, or the server refuses the key (revoked) | 3 | `hadron team host join --replace` |
| The host was removed from the App (`HOST_NOT_FOUND`) | 4 | `hadron team host join --replace` |
| The key is no longer permitted to act as this host (`host:operate` taken away) | 8 | `hadron team host join --replace` |
| Orca is not found on this machine | 10 | install Orca, or put it on `PATH` |
| The App's host setup source is `HOST` | 5 | `hadron team host setup --source SERVER` |
| The host's state directory is claimed by another host of the same id (another App or server) | 5 | `hadron team host leave` on the other |
| Server older than 0.47.0 | 1 | upgrade the server, or use `--config` |

If one of several hosts cannot start, a `host-not-started` event reports it and the others keep running.
A host's state is kept under `<state dir>/hosts/<hostId>`, claimed by its server and App on first run
(`host-identity.json`). A host id is unique only on its server, so two stored hosts of one id (two servers)
never both run on one machine: the second is a `host-not-started` event, and in a later process it is refused
as above. If
the setup source flips to `HOST` mid-run, the poller registers nothing and waits, and says so once.

## Also in this release

- **cli#3285:** the accounts file is refused if it is a symlink, is not a regular file, is owned by another
  user, or sits under a directory others can write to (Unix).
- **cli#3288 (F8 only):** an existing `hosts/<id>` state directory wider than 0700 is tightened, and a symlink
  there is refused. On Windows, reserved device names never name a host state path.

## Unchanged

`team host run --config <host.yaml>` sends the same requests as before and never reads the platform row.
