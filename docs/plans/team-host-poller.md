# Implementation Plan: `team host run|status` — the T6 host poller

> **Status: designed ahead of build.** This doc fixes the contract-independent
> design (host configuration, the five executors, grant custody, bounded waits,
> report shapes). The build lands **after hadron-server
> [#1709](https://github.com/hadron-memory/hadron-server/issues/1709)** (the
> `HostOperation` queue, the `host:operate` App-key scope, the public
> `registerHost` path) merges — until then the GraphQL section below is pinned
> to #1709's shapes and names nothing the server has not shipped. Implements
> [#831](https://github.com/hadron-memory/hadron-cli/issues/831) (T6 CLI half,
> epic [hadron-server#1704](https://github.com/hadron-memory/hadron-server/issues/1704)),
> per the plan node `design:agent-tasks` §7.1/§7.3/§8 and the draft specs
> `cor:agt:070` (feature), `:01` (registration), `:02` (operation),
> `:03` (closed set), `:04` (key reach), `:07` (key issuance).
> Review schedule per Xylon's CHECKED: **T2 Standard+§10** — this is the
> credential-custody half of T6.

## Context

A team's workers run on hosts: machines or containers with agent terminals and
checkouts. When the platform needs to reach a worker with no live session —
launch on assign, end on finish — it does not run commands on the host by hand
or by shell. It queues a **typed operation** for the host that registered the
worker, and a poller on that host carries it out and reports the outcome.

The rules that shape this design (draft `cor:agt:070`, decided
D-2026-10-07-005):

- **A closed set of five operations, nothing else.** An unknown type is refused
  by the server *and locally* by the poller (`cor:agt:070:03`). There is no
  shell passthrough of any kind — "never shell" is the reason the set is typed.
- **A host key reaches one App and one host** (`cor:agt:070:04`). The server
  derives the host's identity from the key, never from what the client sends;
  the poller cannot ask for another host's operations or act in another App.
- **The host key never reaches a worker.** `worker.start` carries a
  single-use launch grant the server minted for (worker, host, task); the
  poller hands only that grant to the agent it launches, which redeems it at
  session start (hadron-server#1709). Never in the agent's environment,
  arguments or files.
- **`worker.end` prompts, never kills** (`cor:agt:020:10`): the worker ending
  its session with or without a handoff is a success and reports whether a
  handoff was written; failure is only "did not end within the bounded wait".
  The poller never removes a worktree after a failed `worker.end`.
- **A worktree is created and removed deliberately** (`cor:agt:070:06`): a task
  never creates or removes one — only typed operations do.

## Commands

```
hadron team host run    [--config <file>]            # the poller (long-running)
hadron team host status [--config <file>] [--json]   # registration + last outcomes
```

- `--config` is required for `run` (an operator surface; no implicit default
  file to mistakingly pick up). `status` takes the same file; with `--json` it
  prints the DTO below.
- `run` is one-per-host: it takes a local lock (the same flock pattern as the
  team binding lock, `internal/cmd/team/lock_unix.go`) keyed by the config
  file's resolved path, and refuses with exit `exitcode.Usage` if another
  poller holds it. One poller per team host or container is the operating
  assumption (#831); the lock keeps one host's typo from running two.
- SIGINT/SIGTERM: stop polling, finish the in-flight operation, report it, then
  exit `0`. An operation is never abandoned mid-flight; one never started is
  simply not polled again.
- Exit codes: `0` clean stop; `exitcode.Usage` for config/key errors;
  authentication and transport failures route through `api.MapError` as usual.
  A single operation *failing* is a report, not a poller exit code — the
  poller's job is to report failures, not to have them.

## Host configuration (host-owned, JSON)

```json
{
  "server": "https://srv.example.com",
  "hostId": "orca1",
  "label": "orca1",
  "pollIntervalSeconds": 60,
  "workers": {
    "anne": { "family": "codex", "launch": "codex exec --grant {{grant}} --cwd {{worktree}}" },
    "liam": { "family": "glm",   "launch": "claude-worker --grant {{grant}} {{worktree}} {{worker}}" }
  },
  "worktrees": { "root": "/srv/teams/acme/worktrees" },
  "endTimeoutSeconds": 300
}
```

- **The launch command per worker is host configuration and never comes from
  the server** (#831; `cor:agt:070` Out-of-scope). It is what the poller
  registers as each worker's family. Template placeholders, substituted at
  launch time: `{{worktree}}` (absolute path), `{{worker}}` (worker name),
  `{{grant}}` (the single-use launch grant from the operation args).
- **The host key is not in this file.** It comes from `HADRON_HOST_KEY` (env)
  or `--key-file <path>` (which must be mode `0600`, else refuse). The config
  file may be committed to a host's config repo; the key may not travel with
  it. The key identifies the App *and* the host server-side — the config's
  `hostId`/`label` are display and registration inputs, not identity claims
  the server trusts.
- Validation refuses loudly (`exitcode.Usage`): unknown fields, an empty
  `workers` map, a worker with no `family` or no `launch`, a family that is
  not a lowercase token, `pollIntervalSeconds` outside 1–600, a `launch` that
  references `{{grant}}` zero times (a worker that cannot receive the grant
  cannot start) or contains any other `{{…}}` placeholder.
- `server` defaults to the configured server (same resolution as the rest of
  the CLI); an explicit value in the config wins.

## Authentication

An App key rides the existing transport unchanged: `Authorization: Bearer
hdr_app_…` is already how the server resolves an App-key principal
(`src/middleware/auth.ts`, `resolveAppKey`), with scopes enforced per
resolver (`requireKeyScope`, the closed list in
`src/lib/appKeyScopes/scopes.ts` — #1709 adds `host:operate`). The CLI needs
only a **key source** for the poller's client: `HADRON_HOST_KEY` or
`--key-file`, feeding the standard `graphql.Client` the Factory builds. No
user token is used or required; `team host run` never falls back to
`hadron auth login` credentials, because a poller speaking as a human is a
custody violation waiting to happen. A missing/revoked key surfaces as the
server's typed refusal through `api.MapError`.

## The poll loop

Every `pollIntervalSeconds`:

1. **Register** — the refresh the design plan §3.4 describes: App (from the
   key), `hostId`, `label`, `pollIntervalSeconds`, and `{worker: family}` from
   the config. A registration not refreshed within 3 intervals reads as lost
   server-side, so the poller registers on *every* poll, not on start only.
2. **Poll outbound** — operations queued for this key's host only. The
   operation record carries: id, type, args, and (for reporting) nothing else
   the poller needs to trust — the server signed the queueing, the key scoped
   the reading.
3. **Dispatch** — exactly one operation in flight at a time per poller
   (execution order = queue order). An unknown `type` is refused **locally**:
   reported as a failed outcome with reason `unknown operation type` and never
   executed (`cor:agt:070:03` S2 — the server refuses to queue it, and the
   poller refuses to run it if one arrives anyway).
4. **Report** — each outcome lands as the operation's result and
   `host.operation.*` events (`cor:agt:070:02`), with the timestamped
   terminal state: `succeeded`, `failed: <reason>`, and for `worker.end`
   whether a handoff was written.

The loop is at-least-once per report: if the report call itself fails, the
operation is re-reported on the next successful poll (reports are idempotent
by operation id server-side; the shape is #1709's). A `status` call renders
the registration and the last reported outcomes — the source of truth is the
server, so `status` works from any machine with the key.

## The five executors

### `worktree.create` (args: repo, base branch, branch name, label)

`git worktree add -b <branch> <root>/<label> <base>` inside the repo checkout
named by `repo`. The `-b` is load-bearing — the bare form takes an *existing*
commit-ish, so a fresh branch name fails `invalid reference`, and a tag or sha
silently yields a **detached HEAD** whose commits are lost on the next
checkout. Refusals (each a `failed:` report, never a panic): the target path
exists; the branch name already exists; the base ref does not resolve; the
`root` is not inside a git repository. Done when the worktree exists and
`git -C <path> rev-parse --verify HEAD` agrees with the resolved base.

### `worker.start` (args: worker, worktree, family, taskId, launch grant)

Substitute the launch template (`{{grant}}`, `{{worktree}}`, `{{worker}}`),
`exec` it detached (its own process group; the poller is not the agent's
supervisor — the terminal command owns the agent's lifetime), and then wait,
bounded, for the worker's session to be bound (the grant's redemption is
observable server-side; the wait polls liveness with backoff up to a
configurable `startTimeoutSeconds`, default 120). Outcomes:

- The session binds → `succeeded`.
- The wait expires → `failed: session not bound within <n>s` — the launch may
  still succeed later; that fact is the report, and the server owns any
  re-issue decision.
- The launch command itself fails to start (not found, not executable) →
  `failed: launch command <reason>`.

Custody, enforced structurally: the child's environment is a **minimal copy**
(`PATH`, `HOME`, `TERM`, `LANG` — an explicit allowlist), never the poller's
own; the host key appears nowhere in argv, env, or any file the child can
read. This is testable and tested (below).

### `worker.deliver` (args: worker, wake proof)

Reports `failed: no delivery path` until the router port (cli#829) lands —
the delivery path is the heartbeats contract, and pretending to deliver would
be worse than the honest refusal (#831 pins this). When #829 lands, this
executor hands off to it; the report then carries the delivery outcome.

### `worker.end` (args: worker)

Prompt the worker to end **its own** session with a handoff — never kill the
agent, never call a session-end mutation on the worker's behalf (the handoff
is the worker's act; `cor:agt:020:09` — it may be a colleague who inherits
it). The prompt travels the channel the worker already reads (the team chat
mention naming the worker and the bounded window), then the poller waits for
the session to read ended, polling with backoff up to `endTimeoutSeconds`
(default 300). Outcomes:

- Ended within the wait → `succeeded`, reporting `handoff: written` or
  `handoff: none` (both are success — `cor:agt:020:10`).
- Not ended → `failed: session did not end within <n>s`. **The poller never
  removes the worktree after a failed `worker.end`** — the worktree holds the
  un-ended worker's state, and removing it would destroy the recovery path
  (#831, `cor:agt:070:03` S3 spirit).

### `worktree.remove` (args: worktree)

Refuse — and report the refusal reason — when the tree has:

- uncommitted changes (`git status --porcelain` non-empty) →
  `failed: uncommitted changes`,
- unpushed commits (`git log @{push}..` non-empty, falling back to
  `git cherry` when no upstream is configured) →
  `failed: unpushed commits (<n>)`.

Clean → `git worktree remove` (refuses a dirty tree itself; our checks give
the *reasoned* refusal first) and the branch is left for the server to record
as merged-or-not (`cor:agt:070:06`: removal is deliberate, the branch is
recorded, never silently dropped). Done when `git worktree list` no longer
shows it.

## `--json` contract (`team host status`)

Explicit DTOs in the command package, as everywhere — never genqlient structs;
slices initialized `[]T{}` so empty renders `[]`:

```json
{
  "host": {"appId": "…", "hostId": "orca1", "label": "orca1",
            "registeredAt": "…", "pollIntervalSeconds": 60,
            "workers": [{"worker": "anne", "family": "codex"}]},
  "lastOutcomes": [{"id": "…", "type": "worker.start", "outcome": "succeeded",
                     "at": "…", "handoff": null, "reason": ""}]
}
```

`run` logs each poll cycle's actions to stderr in the house human format (one
line per operation: type, id, outcome); `--json` on `run` switches the cycle
log to one JSON object per line (NDJSON), so a supervisor can tail it.

## Pinned on #1709 (not built until it merges)

- The GraphQL names/shapes: `registerHost` input, the outbound-poll query, the
  report mutation, the `HostOperation`/result fields, and how a launch grant
  is carried in `worker.start` args and redeemed (hadron-server#1709's
  design). This doc deliberately names none of them as fact.
- The `host:operate` scope in the server's closed scope list, and how a host
  key is minted (ADMIN-minted, stored, revocable — like `attention:deliver`).
- Build steps once merged: `make schema` (sibling-source rules apply — state
  the revision), `make generate`, wire the loop, red-first tests on.

## Testing (red-first per command — none exist today)

- **Executors against temp git repos**: the `-b` trap (a fresh branch name via
  the bare form fails; a tag via the bare form detaches — both asserted as the
  refusals/behavior we ship), existing-path refusal, uncommitted/unpushed
  refusals with exact reasons, remove-after-failed-end never happening.
- **Custody, tested not asserted**: launch a stub command that dumps its argv
  and env to a file; assert the grant appears where the template puts it and
  that the host key string appears **nowhere** — argv, env, or any file under
  the worktree. A second test asserts the env allowlist (no `HADRON_*`, no
  inherited extras).
- **The loop against `fakeGraphQL`** (keyed by operation name): register-every-
  poll, unknown-type local refusal reported not executed, report-retry after a
  failed report, bounded waits (injected clocks), graceful shutdown finishing
  the in-flight op.
- **Contract tests**: `--json` DTO shape and `[]`-not-null empties, exit codes
  (config errors, lock held, auth refusal via `api.MapError`).

## Out of scope

- The router's delivery path (cli#829) — `worker.deliver` stays the honest
  refusal until it lands.
- How a host runs a worker beyond the launch template: terminal layout,
  checkout strategy, supervision — the host's own runbook (hadron-docs, T9).
- The server half: the queue, scope, `registerHost` surface, launch-on-assign
  and close-on-finish triggers (hadron-server#1709).
- The Orca attention router and its port (cli#829) — separate surface.
