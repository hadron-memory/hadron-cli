# C3: `worker.start` launches the agent through Orca, from launcher profiles (design as built)

hadron-cli#2058, epic hadron-memory/hadron-server#4229, CLI lane. Re-scoped by Holger's G3 ruling (2026-11-15, relayed by Peter): agents stay children of the Orca daemon, in Orca's own per-terminal scopes, and the resource caps go on those scopes (`ORCA_TERMINAL_MEMORY_MAX`, `ORCA_TERMINAL_PROCESS_CEILING`). Built to Wolfgang's re-scope comment on #2058 and his additions (a) to (c) in team chat #108152. The S6 systemd-unit and tmux design is **not** built.

## Switch and scope
- **Per-worker switch.** `launch_via: orca` (host config), off by default. `exec` or empty keeps today's path, the poller's own child from `launch`. TRANSITIONAL: the exec path goes with `worker-supervise` (C4).
- **No fallback.** A worker configured for Orca that cannot launch there launches nothing, and its start reports why.
- **Linux only**, because the launched process is pinned by pidfd. `team host run` refuses to start with an Orca worker elsewhere.

## Launcher profiles (G1)
- **Typed profile.** `launcher_profile: {family, mode, model, effort, account}` per worker. `account` names a host-local `account_profiles` entry: `{bin, codex_home}`. The launcher binary (`claude-holger`, `claude-glm`, `claude-kimi`, `codex`, …) selects the account, and `codex_home` is Orca's per-account `CODEX_HOME`.
- **One seam.** `ProfileSource` serves the profile; `ConfigProfiles` is today's host-config source. S1b (hadron-server#4230, typed launcher fields) replaces it behind the same seam once it merges.
- **Fixed templates** (`LaunchArgv`), the only place an argv is built for a profile:

  | family | long (interactive TUI in the Orca pty) | short (runs the task, exits) |
  |---|---|---|
  | claude, glm, kimi | `<bin> --dangerously-skip-permissions [--model m] "$PROMPT"` | the same plus `-p` |
  | codex | `<bin> --no-daemon --dangerously-bypass-approvals-and-sandbox [-m m] [-c model_reasoning_effort=e] "$PROMPT"` | `<bin> exec --dangerously-bypass-approvals-and-sandbox [-m m] [-c …] "$PROMPT"` |

- **What a template refuses.** Model and effort must be plain tokens, so they can never become flags. An effort on a Claude-family profile refuses (no fixed flag carries it). An unknown family or mode refuses. The bin must be a bare name or an absolute path. No template can carry `--resume` or `--continue` (G9: always fresh).
- **First prompt** (fixed): bind through the launch grant (`hadron team session start --tool claude-code|codex`, no `--as`), then `hadron team task start <taskId>`. With no task (a long-lived relaunch, G2 and C5), the prompt is to run `hadron team session recover` and act on it, never resume (Wolfgang (b)). A task id is interpolated only if it is a plain token.
- **Load-time validation.** Everything the launch needs is checked when the config loads: the profile and account, an argv that builds, an absolute `disk_tmp` and `orca_launch.orca_bin`, a group name, an absolute `codex_home`, and a profile family that is the worker's own (Claude-family profiles fit a claude worker).

## The launch (`OrcaLauncher`)
- **Command.** `orca terminal create --worktree path:<dir> --title <worker> --command <text> --json`. `--command` is **typed into the terminal's login shell**: it is on screen and possibly in shell history, and a newline would run early. The typed text is therefore fixed and holds no secret and no untrusted text:

  `. '<env>' && rm -f -- '<env>' && printf '%s %s\n' "$$" "$(tty)" > '<marker>.tmp' && mv -f -- '<marker>.tmp' '<marker>' && exec <quoted argv>`

  The prompt slot expands to `"$HADRON_FIRST_PROMPT"`. With `orca_launch.group` set, it is wrapped in `exec sg <group> -c '…'`, for an Orca daemon started without `incus`. The default is none, because the spike's Orca-launched agent already had the group.
- **Env file.** It holds the launch grant, the first prompt, `TMPDIR`, `CLAUDE_CODE_TMPDIR` and `NODE_COMPILE_CACHE` (from `disk_tmp`), and `CODEX_HOME`. It is created exclusively, mode 0600, in `<state dir>/runs/launch` (0700, on the home disk, never `/tmp`; Wolfgang (c)). The command deletes it before writing the marker (pinned), and the launcher deletes it, with the marker, on every return. The agent gets only these additions over the Orca shell's own environment, never the poller's (so no host key, no `HADRON_TOKEN`).
- **Proof that this is the right process.** No inference from Orca's `ptyId` (#2074, Rodolfo F1). The probe is in the finding `hrn:node:hadronmemory.com:hadron-cli:findings:orca-terminal-create-processid-links-pid-and-tty`. The proof:
  1. Orca's reply names the terminal's `processId`.
  2. The marker names the pid and tty the command saw.
  3. The pid must be that `processId` (or a descendant of it, under `sg`).
  4. Its `/proc/<pid>/fd/0` must be the tty it wrote.
  5. After it is pinned by pidfd, its cwd must be inside the worktree.

  Any failure closes the terminal this launch created, so nothing is left running unrecorded. A process that already exited before it was proven (a very short task) is a launch whose agent is done only when its pid is the terminal's own (see the fix head below), and `worker.start`'s bind loop then reports "exited before binding".
- **`OrcaAgent`.** It implements `Agent`.
  - `Done` comes from the pinned pid: Orca keeps showing a terminal after its process exits, so terminal presence says nothing.
  - `PID` is the pinned pid.
  - `TerminateWith(authorize)` authorizes (C2a's binding lock, held through the close and released before the wait), runs `orca terminal close` (which ends the whole per-terminal scope), and confirms only when the pinned pid is gone.
  - `Terminal()` returns (handle, pid, tty), the verified association a future C2c idle proof can bind to.
- **Call allowlist.** `orcaterm.LaunchGuard` admits only `terminal create` and `terminal close`. The delivery and sweep guards still refuse `create`.

## No per-agent PID namespace (Holger, 2026-11-15)
Holger ruled (relayed by Wolfgang #108664): stay as Orca-conform as possible, so the agent runs **plainly in Orca's terminal scope**. The `unshare` wrapper that an earlier head of this PR added (Wolfgang #108196, after the container check) is removed, together with its startup preflight. The marker's `$$` is the agent's own pid after the `exec` (under `sg`, a descendant of the terminal's process, as before).

What this gives up: a `pkill -f` from one terminal can reach an agent in another, since the container check showed that Orca's cgroup scopes do not isolate signals. The epic's "a pkill costs one run" item is withdrawn. What stays is Orca's scope caps: the memory limit, the process ceiling, and `memory.swap.max=0` (Hugo).

## Fix head after review (cli#2088)
- **A tty is required** (Julius P2, Jamir F1). The marker is parsed strictly as exactly `<pid> /dev/pts/N\n`; anything else, including "not a tty", is refused. After the pidfd pin, the process's `fd/0` must be that pts.
- **Pin, read, then prove alive** (Rook's note; Julius's delta P2). A pidfd does not bind `/proc/<pid>` reads, so a process that exits during them could hand its pid to another. The parentage, `fd/0` and cwd reads are all made after the pin, and only then is the pinned process checked alive. A live pidfd holds its pid, so a process alive after the reads was the one read. Only a positive liveness answer counts: a clock or signal-check error refuses and closes (Julius's second delta P2). A number another process holds by the time of the pin pins that process, and its parentage, read after the pin, refuses it. A process gone by then (or before the pin) counts as a finished launch only when the marker's pid **is** Orca's `processId`: two independent sources naming the same number. Any other gone pid can't be proven the terminal's, so it is refused and the terminal closed (Jamir's delta P2: a dead foreign marker pid). That includes an agent under `sg` that exits before it is proven: it is refused, not taken as done.
- **The pidfd is released** when the agent is gone (Julius P2, Jamir F2).
- **A failed close is no longer reported as closed** (Julius P2). An unproven launch whose terminal won't close comes back as an `UnclosedLaunchError` together with an agent that is not done. `worker.start` records it, so the one-agent-per-worktree guard holds, and a later stop retries the close.
- **Stop order:** authorize, then close, then release (Rook F1). The order is the same on the retried close of an unclosed terminal (Rook's delta P3).
- **Tests:** the fake Orca now gives its shell a real pty (`/dev/ptmx`), as Orca does. The wiring test drives a real `worker.start` into a fake `orca` binary (Julius P3).

## The go-gate (cli#2130)
Orca can start the terminal's shell and then lose or garble its create reply (Jibril's P2, reproduced by Chip against real Orca). Launch then has no handle, so it can close nothing. Before this, the shell had already sourced the launch file and exec'd the agent, which ran untracked: no `agents` entry, no stop, and a second start became possible once the orphan's cwd left the worktree.

The typed command now starts with a bounded wait (600 × 0.1s, the 60s Orca call bound) for a gate file `<id>.go`, and does nothing without it. Launch creates the gate (O_EXCL, 0600) only after the reply has parsed with a handle. So a lost or malformed reply means no agent ever starts. The shell gives up, the launch file is deleted, and at most an empty shell tab is left, which the health sweep's bare-shell close handles. The gate, not the cleanup, is what holds: a poller that dies before deleting the launch file still starts nothing. A create slower than 60s fails closed.

Pins (red first at efeedcef): a lost (non-zero) and a malformed (exit 0) reply after the shell really started, each with and without the cleanup, assert that no agent ever runs. Mutants killed: the command ignores the gate, the gate is created before the create, the gate is never created.

## Entry point for C5's relaunch (#2060)
`Poller.orcaLaunchSpec(ref, w, dir, grant, taskID)` is the one builder of an Orca launch, and `worker.start` uses it with its grant. A relaunch under a still-open session passes no grant. The spec then carries no `HADRON_LAUNCH_GRANT`, and the first prompt says the session is still open, not to start a new one, and to run `hadron team session recover` (never a new bind, never `--resume`). The launch itself is `OrcaLaunchFunc`, which returns an `OrcaAgent` with the same proof, pin and authorized stop. Identity, two-sweep and rate-limit logic stay with C5.

## Restarts
An Orca terminal does not restart a crashed agent. Restarts come from the sweep (C5 #2060, C1 #2056), not from the launcher.

## Not in this slice
- The S1b server source (it waits on hadron-server#4230).
- A context reset relaunch of an Orca worker: `worker.reset-context` still relaunches through `launch`. C2c is fail-closed today (#2074), so it never gets that far.
- C2c's terminal-aware idle proof, which can now bind to `OrcaAgent.Terminal()`.
- Activation on orca1, which needs its own recorded authorization.

## Container check: pkill and runaway under Orca's scopes
Real Orca 1.4.247 (`orca serve`) in a disposable incus container, with caps of 64 processes and 256 MB. Finding: `hrn:node:hadronmemory.com:hadron-cli:findings:orca-terminal-scopes-contain-forks-not-pkill-memory-goes-to-swap`.
- A fork runaway was contained.
- A memory runaway pushed about 12 GB into swap, because `memory.swap.max` was unlimited. Hugo has since added `ORCA_TERMINAL_MEMORY_SWAP_MAX=0`, effective at the next daemon start.
- A `pkill` crossed terminals. An earlier head answered that with a per-agent PID namespace, which Holger's ruling removed (see above).

## Tests
- **hostpoller:**
  - templates per family and mode, refusals, never-resume, the profile source;
  - the real launch against a fake Orca that runs the typed text in bash, as Orca's shell does: no secret typed, the prompt intact and never executed, the grant in the agent's environment, the env file gone at the marker, the pid being the terminal's;
  - an authorized, confirmed stop, a declined one, `Done` on exit, a foreign pid refused, no marker refused, a process outside the worktree refused;
  - the poller's Orca path, the default exec path, and no fallback;
  - load-time validation.
- **cmd:**
  - `team host status` accepts an Orca worker config;
  - `team host run` refuses incomplete ones (naming what's missing) and wires the launcher;
  - `orcaterm.LaunchGuard`.
- **Mutants:** one per guard, all killed.
- **Plain launch** (`TestOrcaLaunchRunsTheAgentPlainlyInOrcasScope2088`, red first): the typed text has no `unshare`, and the pinned pid is the pid the agent sees as `$$`.
