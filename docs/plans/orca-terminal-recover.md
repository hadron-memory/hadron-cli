# `hadron orca terminal recover` and `recreate` (cli#2015)

Design as built. Gate: cli#2015 comment 6874275079 (CHECKED, T1); FIT FITS (Victor, 6874237566).

## What it is

Two commands in `hadron orca terminal`, the group's first that change anything. `recover <worker>`
brings a dark worker back: one new titled terminal running the worker's `host.yaml` launch line plus
`--resume <id>`, verified. `recreate <worker>` closes every terminal Orca lists for the worker's
worktree, then does the same create. Both need `--config <host.yaml>` (the file `team host run` reads),
so the launch line, worktrees and Orca binary are the host's, never guessed.

## Flow (`internal/cmd/orca/recover.go`)

1. Load the host config exactly as `status --config` does (`loadHostMode`); list Orca (exit 10 if down).
2. Resolve the worker (name, URN or id) from the App; `host.yaml` entry by `cfg.RefFor`. No entry or no
   worktree: exit 17. Family other than claude: exit 18.
3. Classify with the `status` logic (`observe` + `applyDarkRule`), and each terminal in the worktree with
   `orcaterm.Classify`.
4. Refuse before touching anything: live agent without `--force` (16); an agent process no terminal
   shows (16); running inside the worktree it would close (16); process scan failed (16).
5. Choose the transcript (newest written at or before `--died-at`, default now) **before** any close, so a
   failure costs nothing (19). `--fresh` needs a staged prompt instead.
6. `--dry-run` stops here and prints the plan.
7. Close (`orca terminal close --tab`), then wait until no agent or supervisor process is left in the
   worktree; still there after the bound: refuse (16), nothing created.
8. `orca terminal create`, then verify: exactly one agent process in the worktree and the new terminal
   shows an agent, not a shell. Otherwise exit 20 (the terminal exists; the message says what was seen).

## Never two agents

*Before*: terminal list + a `/proc/<pid>/cwd` scan. An agent is a process whose **argv[0]** is the agent
binary (`claude`, `codex`); a process that merely mentions it is not one. A supervisor is a shell
running the launch line's own first word (`worker-supervise`); left alive it would start a second
agent, so it must be gone too. *After*: exactly one agent. The command never signals a process: it
closes the Orca tab and proves the result. An orphan agent with no terminal is refused, not killed.

## Not a bind

It never calls `session start`, takeover or `session end`, and the help does not describe itself as the
platform's start-a-worker or end-session operation (cor:agt:070:05). `--json found.session` reports
whether a binding is present in the worktree (read from its git dir); the relaunched agent does the
ordinary bind.

## Exit codes (`internal/exitcode`)

10 Orca unreachable (reused) · 16 refused · 17 no launch entry/worktree · 18 not a Claude worker ·
19 no safe transcript · 20 created but unverified.

## `--json` (`terminal-recover@1`)

`contract, action, worker, worktree, dryRun, result, found, plan, transcript, interrupted, closed,
created, verification, refusal`. A locked agent's pending question is screen text, so it is printed in
text mode only, never in the JSON.

## Reuse by the sweep (#1947)

The relaunch core (`runRecover`'s steps, `agentsIn`, `waitNoAgents`, `verify`, `chooseTranscript`,
`shellJoin`) is dependency-injected through `RecoverEnv`; #1947 can lift it into a package as-is.

## Known limits

- Orca's `terminal create` result shape is read as the first `handle` in `result`; not exercised on a
  live Orca here (fake `orca` only).
- The transcript's config dir comes from the launch line's launcher script (`CLAUDE_CONFIG_DIR=`), else the
  environment, else `~/.claude`; `--transcripts-dir` names the project directory outright.
- The `resetting` availability refusal and the live stop-signal note (gate build notes, non-blocking) are
  not built: they need a new GraphQL read.
- "Verify on the roster" is the screen and the process scan; the server roster is a host report, not a
  check that can be made at this moment.
- Linux `/proc` only; elsewhere the scan fails and the command refuses.
