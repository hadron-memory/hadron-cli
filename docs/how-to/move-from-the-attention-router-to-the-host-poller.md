# Move a host from the attention router to the host poller

The interim Orca attention router (`hadron-server` `scripts/orca-attention-router`,
configured by `~/.config/hadron-orca-attention-router/config.json`) and the
`ticker-wake` loop deliver chat into a host's Orca terminals today. The host
poller, `hadron team host run --config <host.yaml>`, takes that over once chat
delivery ships (hadron-cli#907, with #906's parity rows). This page maps one
configuration onto the other.

**Preparing the host config authorizes nothing.** Stopping the router or
`ticker-wake`, enabling `delivery`, and changing a host are the cutover's, and
the cutover has its own owner and sign-off. Until the batch source ships,
`team host run` refuses `delivery.enabled` with exit 2. `team host status`
already reads such a config, so you can check it beforehand.

## Order, per worker

1. The worker's interim acknowledgers stop **before** the host poller delivers
   batches to it: `ticker-wake` (and never `--ack-mentions`), and the router.
   Two deliverers acknowledging one worker can mark message text delivered that
   was never typed (hadron-server#2072).
2. Only then is that worker's delivery enabled on the host poller.

## Field mapping

| Router `config.json` | Host config (`host.yaml`) | Notes |
|---|---|---|
| `app` | `app` | Same App: an `hrn:app:` URN or an id. |
| `channel` | none | The server picks the channels a batch carries. |
| `hadronCli` | none | The poller is the CLI. |
| none | `key_file` | **New.** The App key with `host:operate`, plus `attention:deliver` for delivery, at mode 0600 or tighter. Point at the file; never paste the key into the config, a ticket or chat. The router authenticated through `hadronCli`'s own login, so there is nothing to carry over. |
| `orcaCli` | `delivery.orca_bin` | Absolute path. |
| `stateDir` | `state_file` (its directory) | Records, locks and the delivery status snapshot live beside the state file, under `delivery/`. Don't reuse the router's `stateDir`: the two must stay separate. |
| `busyWaitMs` | none | Not ported. The server times deliveries (`deliverAfter`, `busyDeadline`), and C1 proves liveness from the screen. |
| `submitWaitS` | none | Fixed in the CLI (`orcaterm.SubmitWait`). |
| `coordinatorWatchMinutes`, `coordinator`, `lifecycle` | none | Retired. A coordinator's standing wake is a ticker with the `every-tick` reason (hadron-server#2068). |
| `roster[].worker` / `.name` | a key of `workers` | A worker's name in the App, its URN or its id. The poller matches the registration by id, URN or name (names are unique per App, case-insensitively). |
| `roster[].worktree` (absolute) | `workers.<ref>.worktrees: [<token>]` | A token is relative to `worktrees_root`. For example, `/home/orca/orca/workspaces/hadron-cli/jonas-2` with `worktrees_root: /home/orca/orca/workspaces` becomes `hadron-cli/jonas-2`. |
| `roster[].terminal` | none | Not ported. The poller finds the one agent terminal in the worker's worktrees, fresh, each time. |
| none | `workers.<ref>.family` | **Required.** The model family it runs as (`claude`, `codex`, `glm`, …). |
| none | `workers.<ref>.tui` | Optional: `claude` or `codex`, when the family doesn't name its interface (a `glm` worker runs in Claude Code). |
| none | `workers.<ref>.launch` | **Required** by the host poller (`worker.start`), even when delivery is all you need. |

## What the host config checks at load

- Every worktree token is valid: no `..`, no `//`, no trailing `/`.
- **A worktree belongs to one worker.** One token listed for two workers is
  refused, and so is the same directory reached by two tokens through a symlink,
  configured or created by the poller. `team host run` refuses either before it
  registers.
- `family` matches the family pattern, `launch` names a command, and `key_file`
  is readable by its owner only (group- or world-readable is refused).

## Check it

```sh
hadron team host status --config host.yaml          # config, registration, delivery block
hadron team host status --config host.yaml --json
```

The delivery block is a **local snapshot** of what this host's poller last saw.
It is not authoritative delivery health, which is the server's (S5). It reads
`stale` when the snapshot is missing, unreadable, or older than three poll
intervals.
