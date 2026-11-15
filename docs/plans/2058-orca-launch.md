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

  The prompt slot expands to `"$HADRON_FIRST_PROMPT"`. There is no `sg` wrapper: `orca_launch.group` is refused at config load (cli#2121, below).
- **Env file.** It holds the launch grant, the first prompt, `TMPDIR`, `CLAUDE_CODE_TMPDIR` and `NODE_COMPILE_CACHE` (from `disk_tmp`), and `CODEX_HOME`. It is created exclusively, mode 0600, in `<state dir>/runs/launch` (0700, on the home disk, never `/tmp`; Wolfgang (c)). The command deletes it before writing the marker (pinned), and the launcher deletes it, with the marker, on every return. The agent gets only these additions over the Orca shell's own environment, never the poller's (so no host key, no `HADRON_TOKEN`).
- **Proof that this is the right process.** No inference from Orca's `ptyId` (#2074, Rodolfo F1). The probe is in the finding `hrn:node:hadronmemory.com:hadron-cli:findings:orca-terminal-create-processid-links-pid-and-tty`. The proof:
  1. Orca's reply names the terminal's `processId`.
  2. The marker names the pid and tty the command saw.
  3. The pid must be that `processId` (or a descendant of it).
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
Holger ruled (relayed by Wolfgang #108664): stay as Orca-conform as possible, so the agent runs **plainly in Orca's terminal scope**. The `unshare` wrapper that an earlier head of this PR added (Wolfgang #108196, after the container check) is removed, together with its startup preflight. The marker's `$$` is the agent's own pid after the `exec`.

What this gives up: a `pkill -f` from one terminal can reach an agent in another, since the container check showed that Orca's cgroup scopes do not isolate signals. The epic's "a pkill costs one run" item is withdrawn. What stays is Orca's scope caps: the memory limit, the process ceiling, and `memory.swap.max=0` (Hugo).

## Fix head after review (cli#2088)
- **A tty is required** (Julius P2, Jamir F1). The marker is parsed strictly as exactly `<pid> /dev/pts/N\n`; anything else, including "not a tty", is refused. After the pidfd pin, the process's `fd/0` must be that pts.
- **Pin, read, then prove alive** (Rook's note; Julius's delta P2). A pidfd does not bind `/proc/<pid>` reads, so a process that exits during them could hand its pid to another. The parentage, `fd/0` and cwd reads are all made after the pin, and only then is the pinned process checked alive. A live pidfd holds its pid, so a process alive after the reads was the one read. Only a positive liveness answer counts: a clock or signal-check error refuses and closes (Julius's second delta P2). A number another process holds by the time of the pin pins that process, and its parentage, read after the pin, refuses it. A process gone by then (or before the pin) counts as a finished launch only when the marker's pid **is** Orca's `processId`: two independent sources naming the same number. Any other gone pid can't be proven the terminal's, so it is refused and the terminal closed (Jamir's delta P2: a dead foreign marker pid).
- **The pidfd is released** when the agent is gone (Julius P2, Jamir F2).
- **A failed close is no longer reported as closed** (Julius P2). An unproven launch whose terminal won't close comes back as an `UnclosedLaunchError` together with an agent that is not done. `worker.start` records it, so the one-agent-per-worktree guard holds, and a later stop retries the close.
- **Stop order:** authorize, then close, then release (Rook F1). The order is the same on the retried close of an unclosed terminal (Rook's delta P3).
- **Tests:** the fake Orca now gives its shell a real pty (`/dev/ptmx`), as Orca does. The wiring test drives a real `worker.start` into a fake `orca` binary (Julius P3).

## The go-gate (cli#2130)
Orca can start the terminal's shell and then lose or garble its create reply (Jibril's P2, reproduced by Chip against real Orca). Launch then has no handle, so it can close nothing. Before this fix, the shell had already sourced the launch file and exec'd the agent. The agent then ran untracked: no `agents` entry, no stop, and a second start became possible once the orphan's cwd left the worktree. The fix makes "may have started" impossible instead of reconciling it (Wolfgang #109089, Rook #109091).

- **The command waits for a gate.** The typed text starts with a bounded wait (600 × 0.1s) for `<id>.go`. After the wait, `[ -f <id>.go ]` is a **hard** condition, so running out never falls through to sourcing the launch file.
- **Launch writes the gate only after a good reply.** That means a fully parsed `ok:true` reply with a handle, before `awaitMarker`, and never on any other path. It is written atomically (a private temp file, then a rename), in the 0700 launch directory.
- **No gate when cancelled or late.** A cancelled launch writes none. A reply that comes in the last tenth of the window or later writes none, because the command may have stopped waiting. Both fail honestly ("nothing was started") and close the terminal, which held only an empty shell.
- **An ambiguous reply.** A lost (non-zero) or malformed reply means Launch has no handle. The shell gives up and stays at its prompt as a bare shell; no agent, no grant. The launch file is deleted on return, but the gate, not that cleanup, is what holds: a poller that dies before cleaning up still starts nothing.
- **The leftover tab.** It classifies as a bare shell. The health sweep closes a bare-shell tab when another, non-bare tab of the worktree remains, for example the retried start. A worktree whose every tab is a bare shell is reported `no_agent`. The shell deliberately does not exit: a terminal whose process has ended does not classify as a bare shell.
- **The window.** 60s equals the bound on one `orca terminal create` call (`orcaLaunchCallTimeout`). Since the shell starts after the call does, any reply the poller accepts at all lands inside the command's wait. Orca's create latency has not been measured as a distribution (the probes so far returned within seconds). A create slower than 54s fails closed. QA records the measured latency.
- **Residual (Rook's (5)).** A poller crash after the gate is written and before the agent is tracked leaves an untracked agent, the same as any crash after a launch. C5's absence census still has to cover that case.

**Pins** (red first at efeedcef):
- A lost and a malformed reply after a real shell start, each with and without the cleanup: no agent.
- A late good reply: no gate, a FAILED report, the terminal closed.
- A cancel after a good reply: the same.
- A gate that appears only after the command's wait: no agent, with the launch file still present.

**Mutants killed:** no hard check, the command ignores the gate, the gate created before the create, a late reply writing it, a cancel writing it, the gate never written. Non-atomic writing at the same point is an equivalent mutant (not observable).

## No `orca_launch.group` (cli#2121)
`orca_launch.group` ran the typed command under `sg <group>`. A process started through `sg` cannot be read through `/proc` by the poller, **whatever the group**. I probed this on orca1: `sg incus`, a group `orca` already holds, made `cwd` and `fd/0` EACCES, while a process started without `sg` read fine. Finding: `hrn:node:hadronmemory.com:hadron-cli:findings:sg-makes-a-process-uninspectable-through-proc`. So such a launch could never pass its proof. The config now refuses the key at load (exit 2, led by `ORCA_GROUP_UNPROVABLE` and naming why), with or without an Orca worker. This is reversible: if a proof channel that works under `sg` turns up, the option can come back behind it. orca1's live host configs set neither `orca_launch` nor `group` (checked 2026-11-15), so the refusal stops no running poller. The `sg` wrapper is removed and the proof is unchanged. Start the Orca daemon with the groups the agent needs.

The same change pins the busy-tree refusal for an Orca worker: a process working in the tree means nothing is launched through Orca (`TestWorkerStartRefusesABusyTreeForAnOrcaWorker2121`). It also tightens the go-gate pins (cli#2136):
- a negative check fails on any `stat` error other than not-exist;
- a late or cancelled launch is shown never to call the gate writer;
- after an ambiguous reply, the env file (grant and prompt) and any gate are gone.

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
