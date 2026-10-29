# Move a host from the attention router to the host poller

The interim Orca attention router (`hadron-server` `scripts/orca-attention-router`,
configured by `~/.config/hadron-orca-attention-router/config.json`) and the
`ticker-wake` loop deliver chat into a host's Orca terminals today. The host
poller, `hadron team host run --config <host.yaml>`, takes that over once chat
delivery ships (hadron-cli#907, with #906's parity rows). This page maps one
configuration onto the other.

**Preparing the host config authorizes nothing.** Stopping the router or
`ticker-wake`, enabling `delivery`, and changing a host are the cutover's, and
the cutover has its own owner and sign-off. With `delivery.enabled`,
`team host run` reads and types this host's chat batches, so turning it on
for a live host is that cutover step. Since hadron-cli#1178 it also reads and
types the host's due tick and task wakes, so it replaces `ticker-wake` too.
`team host status` reads such a config, so you can check it beforehand.

## Order, per worker

1. The worker's interim deliverers stop **before** the host poller delivers
   batches to it: the router, and `ticker-wake` if it would type chat text
   (never `--ack-mentions`). One deliverer per worker (G5): two can type the
   same message twice. Since R53 (hadron-server#2072, on server main since
   `ad7f1c2`), acknowledging a wake moves no message mark, so a wake ack can
   no longer mark untyped text delivered; only a batch ack or the worker's
   own read does.
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

**A message too long to type blocks its worker** (hadron-server#2121). `orca
terminal send` carries text as one argument, at most 131,071 bytes of UTF-8,
while the server delivers a single message whole up to 65,536 characters.
So a message of about 43,700 or more 3-byte characters (or about 32,700 or
more 4-byte ones) can't be typed. It shows as `too-long-to-type` in the
poller's events and `team host status`, every poll. It is never truncated or
acknowledged, and it stays due. Since the server hands the oldest message
first, **no later chat reaches that worker** until #2121 lands. That is why
activation for real workers is blocked on #2121; this guide authorizes none.

**Holds are not reported to the server yet** (hadron-cli#940). Until the
server has the delivery hold report (S5), a held delivery stays on this host.
`team host status` shows it as `UNREPORTED` (`"unreported": true` in JSON),
and there is no server hold, alarm or escalation behind it. A ghost draft, a
question prompt or a mismatched binding reaches a human only if someone reads
this status or the poller's events. The batch stays unacknowledged, so the
server hands it again and it is delivered once the hold clears.

## Read a worker's hold (hadron-cli#1178)

Each worker's row in `team host status` shows its newest delivery event:
outcome, reason code, time, and age. Codes only, never text, a draft or a
proof. `--json` puts these in `delivery.workers[]` as `lastKind`,
`lastReason`, `lastAt` and `lastAgeSeconds`.

A worker's **run of holds** starts at its first `held` event. Later holds,
deferrals, skips and errors don't end it; only a `delivered` or `reconciled`
event does. The run survives a restart of `team host run`: the poller starts from
the workers its last snapshot saved. So a worker removed from the config
keeps its last row, with its age, until the state is cleared. The row shows `held since <time>` (JSON `heldSince`). Once the run
is older than 30 minutes, the row reads `HELD OVER 30m: no delivery since
<time>` (JSON `heldOver30m: true`). Check the poller for that worker first.

| Reason | What it means | What clears it |
|---|---|---|
| `draft` | Someone else's text is in the composer. | The human sends or clears it. Delivery never types over it. |
| `question` | The agent is asking a question (a menu or prompt). | The human answers it. |
| `not-submitted` | Orca refused the send, or accepted it with no proof the agent submitted it. | Nothing to clear. No record is kept, so the next pass sends a fresh request. If the line is still in the composer, the next pass submits it with Enter alone and never retypes it. If a newer line arrives, the old one is cleared first. A worker that stays here is a terminal problem: check the screen. |
| `screen-unknown` | The screen proves neither an empty composer nor the expected agent (a dialog, another interface, a disconnected or read-only terminal). | Fix the terminal. |
| `two-agents` | More than one agent terminal is in the worker's worktrees. | Close the extra one. |
| `binding-mismatch` | The worktree's session binding names another worker. | Rebind the right worker there. |
| `timing-unavailable` | A wake's timing parsed as zero (`deliverAfter`, or `busyDeadline` for a busy agent). | Server-side. The wake stays due. |

A wake whose timing is missing or unparseable is not a worker hold. Both
fields are required, so the poller refuses the host's whole wake read and
types no wake that pass, for any worker. `team host status` shows
`wakes: read-failed` (JSON `wakeRead: "read-failed"`), not a worker row.
The run log's `delivery-failed` line names the code: `reading wakes
failed: bad-deliver-after` (or `bad-busy-deadline`). Report it to the
server owner.

A refused or unconfirmed send leaves no record that holds the worker's later
wakes or chat. Each pass judges the terminal afresh. So a run that keeps
growing points at the terminal (a composer that won't clear or submit), not
at a stuck record. That is the opposite of hadron-server#2746, where
`ticker-wake` held every later wake behind one ambiguous receipt.

## Cut over and roll back (hadron-cli#1178)

**Precondition: the installed `hadron` includes hadron-cli#1178.** Earlier
releases deliver chat batches only and never read `dueWakes`. Check that
`hadron team host run --help` says it reads due tick and task wakes. If it
doesn't, `ticker-wake` must stay on.

The cutover is cold: no soak (Holger, 2026-10-29,
`hrn:node:hadronmemory.com:hadron-business:decisions:wake-delivery-cli-cold-cutover`).
Hugo runs it on the host
(`hrn:node:hadronmemory.com:dev:ops:baragaun-orca:router-retirement-runbook`).
This section is the CLI's side.

`delivery.enabled` is per host. A worker receives delivery when it is listed
under `workers` in that host's config. That list is also the host's
registration for operations, so narrowing it for a smoke check narrows
operations too.

1. **Before.** Run `hadron --version` and record it. Then run
   `hadron team host status --config host.yaml` and check that the config
   loads, the registration shows, and the key has `host:operate` and
   `attention:deliver`. Without `attention:deliver` the first step reads
   `off-by-scope`.
2. **One deliverer.** Stop `ticker-wake` (and the router, if it is running)
   for this host's workers, following the host runbook. Never run both: two
   deliverers can type the same line twice.
3. **Switch on.** Set `delivery.enabled: true` and `delivery.orca_bin`, then
   restart `hadron team host run --config host.yaml`.
4. **Verify within a few polls.**
   - `team host status` shows `delivery: enabled`, not `STALE`, and
     `wakes: read`.
   - Each worker's row turns `delivered` as its wakes come due.
   - No row shows `HELD OVER 30m`.
   - `--json` gives the same view for a script.

**Roll back:**

1. Stop `team host run` (SIGTERM or Ctrl-C) and **wait for the process to
   exit**. It reads its config only at start, so editing `delivery.enabled`
   under a running poller changes nothing. On a stop signal it finishes
   in-flight deliveries, each bounded, before it exits.
2. Set `delivery.enabled: false`. If the host still needs its operations,
   restart `team host run` with delivery off.
3. Only then restart `ticker-wake` per the host runbook. Never run both
   deliverers at once.

The poller's records under `delivery/` stay. When delivery is switched on
again, it reconciles from them and never retypes a line it confirmed.
