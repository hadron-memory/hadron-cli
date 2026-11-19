# Host poller config reload (cli#2623)

Design as built.

## Problem

`team host run` read its YAML once, at start. A worker added to the file while
the poller ran (Billie, added 22:50Z to a poller started 18:39Z) was never in
the registration, so the server never assigned her a recipient and her wakes
were never typed; a restart was the only fix, and nothing said so.

## What it does

Each `Poller.Step` begins with `reloadConfig`. `LoadConfig` records the file's
path and the SHA-256 of the bytes it parsed on the `Config`; the poller
re-reads the file each poll and compares. On a change it parses and validates
the new bytes exactly as start does (`loadConfigBytes`, `CheckWorktreeAliases`)
and then:

- **applies** the three maps workers depend on: `workers`, `repos` and
  `account_profiles`, swapped under the `Config`'s lock, with one
  `config-reloaded` event naming the workers added, removed or changed;
- **names, but does not apply**, every other setting that differs (key,
  poll interval, capabilities, state file, delivery, reset context, orca
  launch): those were read at start by code that wired itself from them, so a
  `config-restart-needed` event names the keys;
- **refuses** a file that does not load, fails a start-time check, is
  unreadable, or makes a worker launch through Orca (that wiring is made at
  start): nothing changes, and `config-reload-refused` says why once for that
  content, then again if the same bad content returns after the file was good.

A comment-only edit updates the recorded hash and logs nothing.

## Why this shape

- No new `team host status` field: the status JSON is a contract, and the
  events are what an operator already reads from the poller. (The coordinator's
  exception waived a specs read on that condition.)
- The three maps are read by goroutines the poller starts (carried-out
  operations, delivery). The fields stay public for the loaders and tests, but
  everything that runs while the poller does reads them through `Worker`,
  `Repo`, `Account` and `WorkerRefs`, which take the read lock; the swap takes
  the write lock. `TestReloadAndReadersDoNotRace2623` fails under `-race` if
  `Worker` loses its lock.
- Held `screen-unknown` was already shown by `team host status` (kind and
  reason per worker). It now also prints `SCREEN UNKNOWN` with the four ways
  the deliverer returns that code, since most are host setup, not the screen.

## Tests

- `hostpoller/reload_2623_test.go`: add, remove, invalid edit (once), a
  restart-only setting, Orca-launch refusal, comment-only edit, readers vs
  reload.
- `cmd/team/host_reload_wake_2623_test.go`: a poller started from a temp config
  does not type a due wake for an unconfigured worker, and after the file gains
  her, the next poll types it into her terminal (fake `orca`, temp dirs, an
  httptest server; red on main). A help pin names the three events.
- `host_wakes_1178_test.go`: the `screen-unknown` hint, not on other holds.

## Not done

Restarting the live poller, regenerating `host-epic4229.yaml` and the dormant
workers stay with the host's operators.
