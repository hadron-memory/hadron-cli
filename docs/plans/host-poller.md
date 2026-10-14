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
| `worker.start` | Runs the worker's configured `launch` argv in a registered, existing worktree, with `HADRON_LAUNCH_GRANT`. | SUCCEEDED once the server answers anything other than `HOST_OPERATION_NOT_DONE`, within the grant's 10-minute lifetime. FAILED if the agent exits first or the lifetime runs out. |
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
- **`worktree.remove` refuses** a tree with:
  - uncommitted or untracked changes;
  - no upstream;
  - commits ahead of its upstream;
  - an agent this poller launched still running in it;
  - a failed `worker.end` for its worker.

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

- **The host key comes from `key_file`, or else `HADRON_HOST_KEY`.** It never
  comes from `HADRON_TOKEN` or the token store. Those are the holder's own
  credential, which the launched agent binds on.
- **No session on host calls.** The poller builds its own client
  (`api.NewClient` with the host key), so no `X-Hadron-Session` rides along.
  The server would refuse one with `APP_KEY_SESSION_BOUND`.
- **A launched agent gets the poller's environment minus `HADRON_TOKEN`,
  `HADRON_HOST_KEY`, any inherited `HADRON_LAUNCH_GRANT`, and any variable
  whose value contains the host key.** It also gets
  `HADRON_LAUNCH_GRANT=<grant>`. The argv is the configured one, with nothing
  appended. Tests pin all of this, including a planted variable that carries
  the key.
- **The agent redeems the grant** with
  `hadron team session start --launch-grant` or `HADRON_LAUNCH_GRANT`:
  - `--as` is optional, since the grant names the worker.
  - `--force` is refused, because a refused launch can't be overridden.
  - The worker is read after the bind, for the binding and the briefing. A
    failed read rolls the session back.

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
  whole `end_wait` before reporting.
