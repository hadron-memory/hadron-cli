# `team host run|status`: the host poller

Issue [#831](https://github.com/hadron-memory/hadron-cli/issues/831), the CLI
half of Task Management T6 (hadron-server#1704, #1709). It builds on server
slices 1 to 3: #1876 (the operation queue and `host:operate`), #1883 (launch
grants) and #1877 (close-on-finish). Xylon's CHECKED stamp sets the schedule
at **T2 Standard + §10**. Built by Jonas on 2026-10-14 against server main
`dd41c675`.

## What landed

```
hadron team host run --config <host.yaml> [--once]
hadron team host status --config <host.yaml>
hadron team session start (--as <worker> | --launch-grant <g>) …
```

- **`internal/hostpoller`** holds the loop and every operation's rules. It
  works through two narrow interfaces:
  - **`Server`**: register, collect and report, made with the host key.
  - **`Host`**: git worktrees and agent processes. `ExecHost` is the real one.

  Tests fake both. `ExecHost` also has a real-git lifecycle test.
- **`internal/cmd/team/host_run.go`** has the cobra commands and the GraphQL
  adapter.
- **`hosts.graphql`** gains the `RegisterHost`, `CollectHostOperations` (with
  `launchGrant`) and `ReportHostOperation` operations, so those three leave
  `unbound-ops.txt`.
- **`setHostWorkerAvailability` stays unbound.** The poller sets no
  OUT_OF_TOKENS or HOST_DOWN marks, because nothing yet defines when it
  should. That's a follow-up.
- **Schema refresh to `dd41c675`, from a throwaway clone.**
  - `SessionInput.launchGrant` gets its omitempty line, so an ordinary start
    never sends `null` (the #139 trap).
  - `setAgentBase` (server#1889) is newly unbound. It's annotated for its
    parity issue, #881.

## The loop

Every poll the poller:
1. **Registers the whole worker list:** `{worker, family, worktrees}`. The
   worktrees are the configured ones plus the ones this poller created. The
   server does not add a created worktree to the registration itself, so the
   host must list it next time. `workers` is always sent, `[]` included. An
   omitted list would not clear a host whose workers were all removed.
2. **Collects** and dispatches each operation in the background, once. The
   server re-delivers collected but unreported operations on every collect.
   An in-flight set makes sure they don't run twice.
3. **Reports** every outcome. A failed report is logged. The operation stays
   collected and comes back on the next collect, where it runs again, so
   every handler must be safe to repeat.

Registered workers are matched back to their configured references by id,
URN or name (case-insensitive, cor:agt:020:02), never by position. A worker
the poller can't match gets no operations.

## The five operations (cor:agt:070:03)

| type | what the host does | reported |
|---|---|---|
| `worktree.create` | Fetches `origin/<base>`, then runs `git worktree add --no-track -b <branch> <root>/<repo-basename>/<label>`. The repo must be in `repos:`. | SUCCEEDED with the name token. If the tree already exists on that branch (a re-delivery), it reports SUCCEEDED again. |
| `worker.start` | Runs the worker's configured `launch` argv in a registered, existing worktree with no live agent this poller launched, with `HADRON_LAUNCH_GRANT`. **The argv must stay in the foreground for the agent's life:** its exit is read as the agent's. | SUCCEEDED when a report of SUCCEEDED is accepted, within the grant's 10-minute lifetime. It stops at `HOST_OPERATION_ALREADY_REPORTED`, since the server already finished it. FAILED if the agent exits unbound (after one last ask) or the lifetime runs out. |
| `worker.deliver` | Nothing. | FAILED: "no delivery path on this host yet (hadron-cli#829)". |
| `worker.end` | Runs the optional `end` argv as a prompt, then waits up to `end_wait` (15m) or until the launched process exits. | **Always FAILED** (see below). |
| `worktree.remove` | Runs `git worktree remove`, but only on a tree with nothing to lose. | FAILED with the reason for each refusal (below). |

- **An unknown type is refused here too,** and reported FAILED with the rule.
  Leaving it unreported would keep it collected until the server's timeout
  (#1881), which isn't built. There is no shell passthrough. Every command is
  a fixed argv: git's own, or the one in host configuration.
- **`worker.start` checks first,** before launching. It reports SUCCEEDED
  once: if the session is already bound (a start collected before a poller
  restart), it's done, and the agent is not launched twice.
- **`--no-track` on create.** Started from `origin/<base>`, git would make the
  new branch track the base. Remove's "pushed" check would then measure the
  branch against `main` instead of against its own push. The real-git test
  found this.
- **`worktree.remove` acts only on a tree registered for the operation's
  own worker** (configured or created). It refuses a tree with:
  - uncommitted or untracked changes;
  - no upstream;
  - commits ahead of its upstream;
  - an agent this poller launched still running in it;
  - a failed `worker.end` for its worker;
  - any process working inside it, found through `/proc/<pid>/cwd` on Linux.
    This covers agents launched before the poller and a human's shell. It
    fails closed if the scan errors. Without `/proc` (macOS, Windows) it
    sees nothing, and only the recorded agents protect the tree.

  A missing tree is reported SUCCEEDED, as already removed.

### Why `worker.end` reports FAILED

cor:agt:020:10 says a worker.end prompts the worker to end its own session
and never kills it, and that ending with or without a handoff is success.
The server is built to match:
- **SUCCEEDED** ends the session itself if it is still open. That is the kill.
- **FAILED** on a session that did end is recorded as the success it is, with
  `{handoff}`.

The host key cannot read session state, so the poller can't see the session
end. Reporting FAILED after the bounded wait is correct either way. Liam
agreed this is fine for v1 (team chat #11415). He'd add a host-key read of
the session only if v1 shows a need, since it would be a new disclosure
surface.

**When the server keeps FAILED, the worktree is kept.** The kept tree is the
one the agent ran in, or every worktree the worker has here if this poller
didn't launch it. The keep goes in the state file, so it survives a restart,
and `worktree.remove` refuses those trees from then on (cor:agt:020:10:
never remove a worktree after a failed end).

## Key custody (cor:agt:070:04, Holger #10761 option A)

- **The host key comes from `key_file` only.**
  - The file must be mode 0600 or tighter; a group or world bit refuses.
  - It never comes from `HADRON_TOKEN` or the token store. Those are the
    holder's own credential, which the launched agent binds on.
  - It never comes from an environment variable either. A same-user agent
    could read one from the poller's `/proc/<pid>/environ`, so the review
    walk dropped the `HADRON_HOST_KEY` fallback.
- **No session on host calls.** The poller builds its own client
  (`api.NewClient` with the host key), with none of the factory's
  request-context hooks. So no `X-Hadron-Session` rides along. The server
  would refuse one with `APP_KEY_SESSION_BOUND`.
- **What a launched agent gets.**
  - The poller's environment minus `HADRON_TOKEN`, `HADRON_HOST_KEY`, any
    inherited `HADRON_LAUNCH_GRANT`, and any variable whose value contains
    the host key.
  - Plus `HADRON_LAUNCH_GRANT=<grant>`.
  - The configured argv, with nothing appended.

  Tests pin all of this: each variable by name, and a planted variable that
  carries the key.
- **What the poller guarantees, and what it doesn't.** The poller never puts
  the key in an agent's environment, argv or files. It cannot keep the key
  from the agent's OS user, though. The agent must run as the holder's OS
  user, because it binds on that user's stored token. If the poller runs as
  the same user, the agent can read `key_file` too. **Run the poller as its
  own OS user, with `key_file` readable only by that user,** to close this.
  The help and this doc say so rather than claiming more. This is a §10
  question for the deployment runbook (docs#369).
- **The agent redeems the grant** with
  `hadron team session start --launch-grant` or `HADRON_LAUNCH_GRANT`.
  - The variable is read **only when neither `--launch-grant` nor `--as` is
    given**. The poller sets it for the agent's whole process tree and
    nothing clears it, so a later ordinary `start --as` (a rotation or a
    shed) must not pick up the spent grant. That was review-walk P1.
  - `--force` is refused only with a grant the caller actually chose.
  - The worker is read after the bind, for the binding and the briefing. A
    failed read rolls the session back.

## Ordering and occupancy (Anne's blind review, #11490)

- **One worker's operations run one at a time, in collection order.** The
  server collects oldest first. Each dispatched operation waits for the
  previous one for the same worker, through a per-worker FIFO chain of done
  channels. A mutex wouldn't do, since it guarantees no order. So a remove
  collected with a start or an end runs after it, and sees the agent the
  start launched or the keep a failed end recorded. Different workers still
  run concurrently: their trees are disjoint, because a worktree token
  belongs to one worker. The config refuses a token listed for two workers,
  and `worktree.create` refuses a token registered for another worker.
- **Launched agents are recorded in the state file** by worker id, worktree
  and pid. An agent outlives a poller restart, since it runs in its own
  session, so an in-memory record alone would let a restarted poller remove
  a live agent's tree. A recorded pid that's still alive refuses the remove.
  A reused pid can only cause a false refusal, which is the safe direction.
- **`worker.start` launches only on `HOST_OPERATION_NOT_DONE`.** That is the
  only answer that shows the start is unbound. An already-finished operation
  stops. Any other failure leaves the operation collected, and the next
  collect retries it.

## `run`, `--once` and `status`

- **`run` exits on a failed check-in in the 2, 3 or 8 class,** such as a
  revoked or refused key or a malformed registration, with its code. Looping
  on one would hide it. Other failures are logged, and the loop goes on.
- **`--once` exits 7 if any report did not reach the server.** Those
  operations are re-delivered at the next collect.
- **`--json` prints one `hostEventDTO` per line:** `time`, `kind`,
  `operation`, `type`, `outcome`, `detail`. Every key is always present.
  Writes are serialized, since operations run concurrently.
- **`status` finds this host's registration by the host id** saved in the
  state file at the last registration. On the server, a host key reads
  every registration in the App, and a label is not unique, so matching on
  the label would be a guess.

## Exit codes (new mappings)

| code | exit |
|---|---|
| `HOST_KEY_REQUIRED`, `APP_KEY_SCOPE_REQUIRED`, `LAUNCH_GRANT_INVALID` | 8. Matched before the `_REQUIRED` suffix rule, which would read them as usage errors. |
| `BAD_HOST_REGISTRATION`, `APP_KEY_SESSION_BOUND` | 2 |
| `HOST_OPERATION_NOT_COLLECTED`, `HOST_OPERATION_ALREADY_REPORTED`, `HOST_OPERATION_NOT_DONE`, `LAUNCH_REFUSED`, `WORKER_NOT_HELD`, `WORKER_LIVE`, `WORKER_START_PENDING` | 5 |

Command-level errors:
- **No host key:** exit 3.
- **A bad or unknown configuration field:** exit 2. The YAML decoder refuses
  unknown keys.

## Depends on

- **The server's re-mint fix** (Liam, team chat #11415). Today every collect
  revokes a collected start's unconsumed grant and mints a new one
  (`mintLaunchGrants`). So a steady poller would void the grant its booting
  agent holds. The fix: a re-collect leaves out a start whose grant is still
  live. This poller relies on that rather than pausing collection, so its QA
  needs a server that has the fix.

## Not in this PR

- **Availability marks** (`setHostWorkerAvailability`).
- **A real `worker.deliver`:** it waits for cli#829.
- **Seeing a session end directly,** without the inference above.
- **Discovering existing worktrees by scanning.** They're declared in the
  configuration.
- **A durable in-flight ledger.** After a restart the server re-delivers, and
  each handler is safe to repeat.
- **`worker.end` after a restart:** with no tracked process, it waits the
  whole `end_wait` before reporting. Likewise, an agent launched before a
  restart that hasn't bound yet is invisible to the one-agent-per-worktree
  check; its revoked grant fails its own bind.
- **Name tokens:** a worktree's name token is `<repo basename>/<label>`, so
  two repos with the same basename can collide. Re-creating a removed
  worktree on the same branch fails, because `remove` keeps the branch.
- **Clearing a keep:** there's no command for it. A kept tree stays kept
  until its entry is removed from the state file by hand.
