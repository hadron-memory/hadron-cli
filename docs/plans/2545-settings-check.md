# Idle-compaction drift check (cli#2545)

Ruled in cli#2544 (Holger, 2026-11-22): Claude Code's `idleCompaction` is off
everywhere for team workers. `hadron team host settings-check --config <host.yaml>`
shows where it is not. It is local and read-only. Setting the value is the host
manager's own step, under a production grant, and no hadron command writes it.

## What was checked, 2026-11-25

Settings reference and settings pages (code.claude.com/docs): the key is a Boolean,
default `true`, allowed in any settings file, from Claude Code 2.1.290.
`autoCompactEnabled: false` and `DISABLE_AUTO_COMPACT` also stop idle compaction; the
check does not count them (the gate's builder's choice) and the help says so.

Precedence was **measured** on 2.1.332 and 2.1.328, offline, with a scratch
`CLAUDE_CONFIG_DIR`, a scratch repository with a linked worktree, and SessionStart
hooks and an `env` value in each file:

| In a linked worktree, highest first | Loaded |
|---|---|
| main checkout `.claude/settings.local.json` | yes |
| worktree `.claude/settings.local.json` | yes |
| worktree `.claude/settings.json` | yes |
| main checkout `.claude/settings.json` | **no** |
| account `settings.json` | yes |

The documentation says a worktree uses the main checkout's local file, and the issue
assumed the worktree's own. Both are read, and the main checkout's wins. So one `true`
there overrides every worktree of that clone. A file with invalid JSON or a value of the
wrong type was ignored **as a whole** in `-p` mode (the next level applied); a byte order
mark was accepted. An interactive session may ask instead, so the check reports a broken
deciding file as drift and never looks past it.

Not measured: the managed level (documented order used), `idleCompaction` itself
(another key carried the probe), a session started in a repository subdirectory, and the
ownership exception (documented).

## Decisions

- `--config`, not `--host <id>`: every host-local command takes `--config`, and a
  registration names worktrees only as tokens. The check needs no server and no key.
- **Account unknown.** A launcher script picks `CLAUDE_CONFIG_DIR`, which host.yaml does
  not record, and parsing shell scripts is not proof. Without `claude_config_dir` a
  worker is off only if it is off under every `~/.claude*` directory. A stray directory
  of that name therefore makes workers drift until they name `claude_config_dir`.
- **GLM and Kimi workers are checked.** They run the Claude Code binary against another
  backend (`internal/hostpoller/launchprofile.go`: "GLM and Kimi run Claude Code against
  another backend through their launcher scripts", and one launch template for all three).
  Read on the host, 2026-11-25: the GLM and Kimi launchers set `CLAUDE_CONFIG_DIR` and
  exec the same `~/.local/bin/claude`. So the same settings files and version apply.
- **Version** comes from the native installer's symlink. Running `claude --version` would
  start a program that can write to an account directory. An agent already running may be
  an older version; not checked.
- Drift exits 5, like `spec` and `skill status --strict`; the report is still printed.
- Out of sight, and said in the help: `--settings`, server-managed and MDM settings.

## JSON

`{key, requiredValue, default, minVersion, claudeBin, claudeVersion, drift, accounts[{dir, file,
status}], worktrees[{worker, worktree, path, status, effective, source, reasons[]}]}`.
`claudeVersion`, `effective` and `source` may be null.
