# `hadron team host doctor`

Unreleased (cli#3236, over hadron-server#5210).

New: `hadron team host doctor [--host <host-id>] [--json]` reports what this machine has for hosting workers: Orca (found, its version, the
repositories and worktrees it lists), the program each family runs (`claude`, `codex`, `claude-glm`, `claude-kimi`), whether the state directory is
on disk and not tmpfs, and the optional accounts file. Each fact is detected when you run it; nothing is read from a host file or kept.

With `--host` it also checks each worker assigned to that host: its workdir is one worktree Orca knows, its account is in the accounts file, and its
program is on `PATH`. Exit 0: the host can run every assigned worker. Exit 5: something is missing. Exit 10: Orca is not on this machine or did not
answer.

The accounts file is `~/.config/hadron/accounts.yaml` (mode 0600), optional, and names extra accounts (`family`, `program`, `configDir`). It takes accounts
only. The names `tool` and `version` are reserved for a newer hadron. Nothing in it leaves your machine.

`hadron team host setup` is unchanged.
