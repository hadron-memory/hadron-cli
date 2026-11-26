# Host file removal, S1: the plan checked and the unknowns closed

Issue [hadron-server#5210](https://github.com/hadron-memory/hadron-server/issues/5210), slice 1 of the epic
[hadron-server#5209](https://github.com/hadron-memory/hadron-server/issues/5209). A check of the epic's plan, not a
design. No product code ships from it. Measured on 2026-11-26 by Antonio (Claude), on **orca1** (Linux, Orca 1.4.247,
hadron-cli `c5c90db45`, hadron-server `260495dad`). **No production access**: every server call below ran against a
throwaway test database. Nothing here carries a token, key or personal data; output shown is trimmed to the fields that
answer the question.

**What is not measured, and who can:** the macOS half of U4 and all of U5 need a Mac. Steps are at the end, for Holger.
The reboot proof of U4 on Linux is not run (orca1 is the live team host); see U4.

## Unknowns

### U1. Does an account entry's `program` / `configDir` give a session on that account in an Orca terminal?

Linux only; macOS is in the Mac steps.

**Claude. Yes: Orca does not override `CLAUDE_CONFIG_DIR`.** Command, run on orca1 over every live `claude` process of
the user (read-only, `/proc/<pid>/environ`, two variables):

```
for p in $(pgrep -u $USER -x claude); do tr '\0' '\n' < /proc/$p/environ | awk -F= \
  '/^CLAUDE_CONFIG_DIR=/{print "claude CLAUDE_CONFIG_DIR="$2} /^ORCA_CLAUDE_CONFIG_DIR=/{o=$2} END{print "  ORCA_CLAUDE_CONFIG_DIR="o}'; done | sort | uniq -c
```

Output: 87 live Claude sessions, all inside Orca terminals; every one has `ORCA_CLAUDE_CONFIG_DIR=/home/orca/.claude`
(Orca's own value, under its own name) and `CLAUDE_CONFIG_DIR` set to the launcher script's value, nine distinct
directories (`.claude-amalia` 15, `.claude-zai` 14, `.claude-team-rupali` 16, and six more). Orca sets its own
variable and leaves the tool's variable alone, so a launcher's directory is the account the session runs on. These are
working sessions: they are the team.

**Codex. Orca sets `CODEX_HOME` itself, and a program that sets it afterwards wins.** A throwaway terminal in Orca,
then closed (`orca terminal create --worktree path:<my worktree> --command "<env dump>"`, `orca terminal close`):

```
shell-start
ORCA_CLAUDE_CONFIG_DIR=/home/orca/.claude          # CLAUDE_CONFIG_DIR is NOT set
CODEX_HOME=/home/orca/.config/orca/codex-accounts/90d2971b-…/home
ORCA_CODEX_HOME=/home/orca/.config/orca/codex-accounts/90d2971b-…/home
wrapper-set        # CODEX_HOME=/tmp/probe-codex CLAUDE_CONFIG_DIR=/tmp/probe-claude sh -c 'env | grep …'
CODEX_HOME=/tmp/probe-codex
CLAUDE_CONFIG_DIR=/tmp/probe-claude
```

All 12 live `codex` processes carry that same managed home in both variables.

- **Default account.** Claude with no directory set: the tool's own default (`~/.claude`; `orca account list --json`
  reports `systemDefaultHome: /home/orca/.claude`, `claude.activeAccountId: null`). **Codex with no account named in an
  Orca terminal: Orca's active managed Codex account**, not `~/.codex` (`codex.activeAccountId` = the id in the path
  above). So "the tool's default" differs by family inside Orca; C2's doctor line and the docs must say which.
- **A Codex account's directory is derived, not listed.** `orca account list --json` gives Claude accounts a `home`
  field and Codex accounts only an `id`; the Codex home is `<orca user data>/codex-accounts/<id>/home` (seen in the
  environment). The accounts file's `configDir` for such an account is that path, written by the user.
- **Not exercised:** a Codex session on a home other than Orca's active one (orca1 has one managed Codex account), and
  `orca agent launch` (not used by the host; it launches through `orca terminal create` and a program). The result for
  Codex is therefore the environment, not a second working account.

Consequence for C3: the host launches `program` with `configDir` exported as `CLAUDE_CONFIG_DIR` / `CODEX_HOME` in the
launch command itself (as a launcher script does today); it needs nothing from Orca.

### U2. Which `orca worktree list --json` field matches an assignment's `workdir`?

`workdir` token `<repo>/<name>` = `<repos[].displayName>/<basename(worktree.path)>`, joined on `worktree.repoId`. The
worktree's own `displayName` does **not** match: 0 of 200 equal the path basename (they are labels like
`Abigail [Server Engineer, Sonnet]`).

```
orca worktree list --json     -> 200 worktrees, 0.089 s wall
orca repo list --json         -> 7 repos (5 git, 2 folder)
token = repoDisplayName + "/" + basename(path)    -> 197 distinct tokens for 200 worktrees
orca worktree show --worktree path:<p> --json     -> 0.074 s
```

- `hadron-server/antonio` resolves to exactly one path. Three tokens are **ambiguous** on orca1: `hadron-server/wt` (3
  paths, QA scratch directories) and `hadron-server/server` (2). The resolver must refuse an ambiguous token with its
  own message (suggested `WORKDIR_AMBIGUOUS`, named in the message with the paths) and never pick one.
- **Folder workspaces** appear as repos of `kind: folder` (`hadron-memory`, `worker`) and list under the same shape, so
  a no-Git workspace resolves the same way.
- **Speed:** one full list per poll is 0.09 s at 200 worktrees; `worktree show` is not needed.
- Corrected below: the epic's example `hadron-server/kit` is `<repo>/<name>` under one root; the join does not need a
  root, as the outcome table says.

### U3. Does `orca worktree create` replace the CLI's `git worktree add`?

Yes, on this Orca. Command and result (a throwaway worktree, removed after):

```
orca worktree create --repo name:hadron-docs --name u3-probe-antonio --branch antonio/u3-probe --base-branch main --setup skip --json
-> ok; path /home/orca/orca/workspaces/hadron-docs/u3-probe-antonio, branch refs/heads/antonio/u3-probe,
   head fdd2bc17 (hadron-docs main), `git status -sb` clean on antonio/u3-probe
orca worktree rm --worktree path:… --delete-branch --json   -> removed; directory and branch gone (verified)
```

- The path is `<Orca workspace root>/<repo displayName>/<name>`, which is the token form of U2: a worktree made through
  Orca is addressable by `<repo>/<name>` with no root configured.
- `--repo name:<displayName>` is the selector; a repo Orca does not know is the "not registered" refusal with
  `orca repo add` as the fix (not run: it would add a repo to the live Orca).
- **`TeamRole.repos`** is not read here: hadron-server `260495dad`, `prisma/schema.prisma` `TeamRole.repos` is a
  soft affinity list (epic). Which spelling it holds (repo name or `owner/name`) is **not settled by this slice**, and
  C3/C8 take `--repo` as the Orca `displayName` until they read the platform's data; the platform data is a
  production read and needs its own grant.

### U4. Boot-time reach of the host credential and the Orca runtime

**Linux, evidence short of a reboot.** orca1 is the live host, so no reboot was run. What is measured:

```
loginctl show-user orca -p Linger -p State     -> Linger=yes State=lingering
systemctl --user show orca-serve-runtime.service -p UnitFileState   -> enabled   (orca serve, headless, WantedBy=default.target)
systemctl --user show hadron-host-run.service    -p UnitFileState   -> disabled  (!)
~/.config/hadron/: auth.json, config.toml, each -rw------- ; host key files read from disk, no keychain
```

- Orca has a **headless runtime** (`orca serve`) that runs as an enabled user service under linger, so it needs no
  desktop login; the existing CLI reaches it from services (the hadron host unit does today).
- The CLI's credentials are 0600 files under `$HOME`; no keychain is needed (R2 settles the host credential as such).
- **Finding for X1/C6:** orca1's `hadron-host-run.service` is `disabled`, so it does not start by itself at boot; it
  has been started by hand since 2026-11-20. `host install` (C6) must `enable` the units.
- **Not measured:** an actual reboot with nobody logged in. That is C6's own acceptance (E2E container reboot) and stays
  there; it is not claimed here.

**macOS:** see the Mac steps.

### U5. macOS

Not measured; needs a Mac. Steps below. What is still unknown until then: where Orca's CLI lives, how the daemon runs,
the tty name of a launched agent, and which process facts exist without `/proc`. M1 to M3 cannot start before it.

### U6. Is a `hadron auth login` token admitted to the person-only key mutations?

**Yes.** `hadron auth login` requests the `account` scope (`internal/auth/browser.go:35`, `:140`); the GraphQL
surface admits `account` (`src/server.ts:1326`), and the person gate (`requirePersonAsThemselves`) refuses only a
missing user, an impersonation, a run principal and a worker-bound session. Measured over HTTP against a real server
instance on a test database (a scratch vitest file against `createServerOnCleanDb`, not committed), one manager of
an App, three keys:

| Key's OAuth scopes | `createAppKey` | `grantAppKeyScope(host:operate, hostId)` |
|---|---|---|
| `["account"]` (what `auth login` issues) | 200, raw key returned | 200, granted |
| `["mcp"]` | 403 `FORBIDDEN` | not reached |
| `[]` (portal-minted, legacy) | 200 | 200 |

- **A caution for C1:** the person gate also refuses a request carrying a worker-session header. The CLI sends
  `X-Hadron-Session` only on calls that opt in (`internal/api/session.go`, per call, "never a client default"), so
  `host join` must not opt in, and must be run by the person, not from a worker's bound session. C1's acceptance
  should include a join made from a worktree that has a worker binding.

## The outcome table, checked

Rows not listed keep the epic's outcome. "Verified" rows were re-read at the heads above.

| Setting | Final outcome | Evidence |
|---|---|---|
| `worktrees_root`, `workers.*.worktrees` | As the epic: `workdir` resolved through `orca worktree list --json` + `orca repo list --json`: token = repo `displayName` + `/` + basename of `path`. Ambiguous token: refuse with the paths | U2 |
| `repos` | As the epic: `--repo` token resolved via `orca repo list --json` `displayName`; creation through `orca worktree create --repo name:<displayName>` | U3 |
| `orca_launch.orca_bin`, `delivery.orca_bin` | Linux: `orca` on `PATH`, else `~/.config/orca/cli/bin/orca` (the path orca1 uses: `which orca`). macOS: Mac steps | Verified on Linux |
| `account_profiles.*.codex_home`, `workers.*.claude_config_dir`, `.codex_home` | The account entry's `configDir`, exported in the launch command as `CLAUDE_CONFIG_DIR` / `CODEX_HOME`. A Codex entry for Orca's managed account uses `<Orca user data>/codex-accounts/<id>/home` | U1 |
| `delivery.ack_mixed` | **Product default: on. No `AgentTeamHost.deliveryAckMixed` column.** The server change it asserts (hadron-server A′, PR #2102) merged 2026-10-19; orca1 runs `true`; no setting of `false` exists anywhere on orca1 | below |
| `stop_on_end`, `stop_wait` | **`worker.end` stops the launched process only for a short-lived worker; a long-lived worker is never stopped. Wait: 60 s.** `stop_on_end: manager` is not carried | below |
| `reset_on_start` | **Derived from `lifecycle`, as the epic says, but conditional:** short-lived resets, long-lived does not, *once Holger confirms `cor:agt:070:06`'s proposed reset*, which the spec still marks "Proposed, for Holger to confirm". Until then the default is off | below |
| `cleanup_dead_tabs` | CLI product default, off (R8) | epic |

**`delivery.ack_mixed`** (`internal/delivery/wake.go:423-431`, `host_run.go:561-565`): with it off a wake that names
channels or a mention is typed but held from acknowledgment; with it on the tick and task parts are recorded without
moving chat marks. The only reason to leave it off is a server without A′, and the CLI already requires a server new
enough for the rest of the plan. orca1: `delivery: ack_mixed: true` in `host-epic4229.yaml`; no host file anywhere sets
it off. So the setting disappears into the default; a CLI run against a server without A′ would be refused by the
server-release check each CLI slice states.

**`stop_on_end` / `stop_wait`** (`internal/hostpoller/config.go:231-243`; `docs/plans/2057-worker-end-stops-process.md`,
which calls the field transitional and names `Agent.lifecycle` as its replacement). orca1's 106 workers:

```
98  no launcher_profile.mode  stop_on_end never   (default)
 5  mode long                 stop_on_end never
 3  mode short                stop_on_end always
```

`stop_on_end: manager` is used nowhere. The three short workers are the three `always`; no long worker stops. So
"short-lived stops, long-lived never" reproduces orca1's current behaviour exactly. `stop_wait` is set nowhere (the
60 s default is what runs). The 98 workers with no `launcher_profile.mode` also never stop today; under the platform path their
behaviour follows each Agent's `lifecycle`, so any of them whose Agent is short-lived starts stopping at X1. X1 should
list them before the move (not done here: it needs the Agents' lifecycles, a platform read).

**`reset_on_start`**: the field is `false` on all 106 workers of orca1, **including the three short-lived ones**. The
epic's "short-lived resets" is therefore a behaviour change for those three at X1, not a preservation. It is the
spec's own proposal (`cor:agt:070:06` Abstract: "Proposed, for Holger to confirm: yes"), so C4 must not build the
derived reset before that confirmation, and X1 must tell the owners of those three workers. If it is not confirmed the
row is "off, no setting" and C4's dirty-tree-refusal acceptance drops.

## Account selection, from an assignment's `accountProfile` to a launched session

1. No `accountProfile`: launch the family's program with no account variable. Claude uses `~/.claude`; **Codex inside
   an Orca terminal uses Orca's active managed Codex account** (U1). `host doctor` prints which, per family.
2. `accountProfile: work`: read `accounts.work` from the accounts file at the moment of use; the entry's `family`
   must equal the assignment's family. Launch `program` (else the family's program from `PATH`: `claude`, `codex`,
   `claude-glm`, `claude-kimi`) with the entry's `configDir` exported as `CLAUDE_CONFIG_DIR` (family `claude`, `glm`,
   `kimi`) or `CODEX_HOME` (family `codex`), set in the launch command so a value Orca exports first is replaced (U1).
3. Failure, no fallback: the start is reported failed with `ACCOUNT_NOT_ON_HOST`; the message names the account and the
   cause, one of: no accounts file; no entry of that name; entry family differs from the worker's; `program` not found
   or not executable; `configDir` not a directory. The same text is `host doctor`'s line.
4. A custom launcher script keeps working: it is the entry's `program`. It can still set its own directory; if the
   entry also gives `configDir`, the exported value is the default the script may override.

### The accounts file

- **Name and place, both OSes:** `accounts.yaml` in the CLI's config directory (`internal/config/paths.go:8`:
  `$XDG_CONFIG_HOME/hadron`, else `~/.config/hadron`), so `~/.config/hadron/accounts.yaml` on Linux and on macOS. Mode
  0600 (refused if a group or world bit is set, as the credential files are). Read at the moment of use.
- **Keys.** Top level: `accounts` only (and `version`, below). Per entry: `family` (required: `claude`, `codex`, `glm`,
  `kimi`), `program` (optional, absolute path), `configDir` (optional, absolute path). The entry name is the
  `accountProfile` name: `[a-z0-9][a-z0-9_-]{0,63}`. Any other key is refused with the file, entry and key named.
- **Taking a `tool` key later (#5228) without invalidating a file.** Two rules, fixed now:
  1. `tool` is optional, and **an absent `tool` means the tool the entry's `family` implies today** (`claude` for
     `claude`, `glm`, `kimi`; `codex` for `codex`). So no existing entry changes meaning or becomes invalid when `tool`
     exists.
  2. The loader's refusal for unknown keys carries a short list of **reserved keys** (`tool`, `version`) that today's
     CLI recognises by name and answers "this key needs a newer hadron": a file written for a newer CLI is refused
     clearly, never with a generic "unknown key". The reserved list is the one place a later version adds names.

  Before and after (the first file is valid before and after the key exists, and means the same):

  ```yaml
  # before
  accounts:
    work:
      family: claude
      configDir: /home/you/.claude-work
  ```
  ```yaml
  # after, a later CLI: one entry names its tool, the first is untouched
  accounts:
    work:
      family: claude
      configDir: /home/you/.claude-work
    review:
      family: claude
      tool: claude-code
      program: /home/you/.local/bin/claude-review
  ```

  This note records the choice and the reason (old files stay valid; new keys fail loudly on old CLIs). It builds no
  `tool` key.
- **Only names leave the machine** (epic R7): nothing in this file is sent to the platform.

## Open rulings, carried

- **R3 (who may join a machine).** Recommended and built-in to V1 as written: only a signed-in manager, on that machine
  (U6 shows the `auth login` token qualifies). If instead a manager may issue a pairing code for someone else's
  machine: V1 gains a code table and a redeem call; C1's `join` gains `--code`; the person gate stays; U6 stops being
  the C1 gate for that path.
- **R6 (one service for several Apps).** Recommended: `host run` with no arguments serves every stored credential.
  If instead one service per App: C1 stores one credential and C6 renders one unit per App with the App named in the
  unit (against test 2 of the epic's "no local file"), and `host install` takes `--app`.

## Corrections to the epic

1. **U1 closed** (Claude yes, Codex yes; macOS pending); **U2, U3, U6 closed; U4 Linux partly; U5 open** (Mac).
2. **Default account differs by family inside Orca:** Codex defaults to Orca's active managed account, not `~/.codex`
   (epic row "launcher_profile.account", step 1 of "How an account name becomes a launched session").
3. **A Codex managed account has no `home` in `orca account list`**, only an id; the home path is derived.
4. **`workdir` matching is `<repo displayName>/<basename(path)>`**, not the worktree's `displayName`; three ambiguous
   tokens exist on orca1, so C2/C3 need an ambiguity refusal in addition to `WORKDIR_NOT_ON_HOST`.
5. **`delivery.ack_mixed` has no column** (product default on); **`stop_on_end`/`stop_wait`**: short stops, long never,
   60 s; V1's data model drops `deliveryAckMixed`.
6. **`reset_on_start`: the derived reset changes the behaviour of orca1's three short-lived workers** and waits on
   Holger's confirmation of `cor:agt:070:06`.
7. **orca1's `hadron-host-run.service` is not enabled for boot** (X1/C6 note).
8. **C1 acceptance:** add a join from a worktree that has a worker binding (person gate).

## Steps for the Mac (U5 and the macOS half of U4), for Holger

Run on the Mac the first-run test will use; paste the output on #5210 with tokens removed.

1. `sw_vers; uname -m; orca --version; which -a orca; ls /Applications | grep -i orca; ls ~/Library/Application\ Support | grep -i orca`
2. `orca status`, `orca account list --json` (the keys only: `home`, `systemDefaultHome`, `activeAccountId`), `orca repo list --json | head`, `orca worktree list --json | head`.
3. Open a terminal in Orca on a worktree and run: `tty; echo $TERM_PROGRAM; env | grep -E '^(CLAUDE_CONFIG_DIR|CODEX_HOME|ORCA_CLAUDE_CONFIG_DIR|ORCA_CODEX_HOME)='; ps -o pid,ppid,tty,comm -p $$`. Then `CODEX_HOME=/tmp/p CLAUDE_CONFIG_DIR=/tmp/q sh -c 'env | grep -E "^(CODEX_HOME|CLAUDE_CONFIG_DIR)="'` (this is U1 on macOS).
4. Process facts without `/proc`: with any agent running in that terminal, `ps -o pid,ppid,tty,lstart,command -p <pid>` and `lsof -p <pid> | grep cwd`.
5. How Orca's daemon runs: `launchctl list | grep -i orca; ps aux | grep -i '[o]rca' | head -5`.
6. U4 macOS: `launchctl print gui/$(id -u) | head -5`; `launchctl print user/$(id -u) | head -5`. Then, only if you are willing to reboot: put a trivial LaunchAgent (`~/Library/LaunchAgents/test.hadron.u4.plist` running `date >> /tmp/u4.txt`, `RunAtLoad`), reboot without logging in at the login window, and from another machine `ssh` in and read `/tmp/u4.txt`.
