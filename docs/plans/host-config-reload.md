# Host poller config reload (cli#2623)

Design as built.

## Problem

`team host run` read its YAML once, at start. A worker added to the file while
the poller ran (Billie, added 22:50Z to a poller started 18:39Z) was never in
the registration, so the server never assigned her a recipient and her wakes
were never typed; a restart was the only fix, and nothing said so.

## What it does

Each `Poller.Step` begins with `reloadConfig`. `LoadConfig` records the file's
absolute path and the SHA-256 of the bytes it parsed on the `Config`; the
poller re-reads the file each poll and compares. New content is applied on the
second poll that sees it (so a file an editor is still writing is never applied
half-written). It is then parsed and validated exactly as start does
(`loadConfigBytes`, `CheckWorktreeAliases`), checked for trust (below), and:

- **applies** `workers`, `repos` and `account_profiles` by building a new
  `Config` and swapping the poller's pointer, with one `config-reloaded` event
  naming the workers added, removed or changed;
- **names, but does not apply**, every other setting that differs (key, poll
  interval, capabilities, state file, delivery, reset context, orca launch):
  those were read at start by code that wired itself from them, so a
  `config-restart-needed` event names the keys. A new `Config` field is
  restart-only until it is added to the `reloadable` list on purpose;
- **refuses** a file that does not load, is unreadable, fails a start-time
  check, fails the trust check, or makes a worker launch through Orca when no
  worker did at start (that wiring is made at start): nothing changes and
  `config-reload-refused` says why once per content and reason. A refusal that
  can be fixed without editing the file (a file mode) lifts by itself.

A comment-only edit updates the recorded hash and logs nothing.

**Trust.** Hot reload turns "whoever can write this file" into "whoever can
make the poller run a command on the next poll", so it is held to more than a
restart: the file, and each program or directory a *changed* entry names (the
launch, end and recover programs of an added or changed worker, a changed
account's program, a changed repo's clone), must belong to the poller's user
or root and not be writable by group or others. Names that do not resolve are
skipped (starting that worker fails on them as before). Not enforced on
Windows, where a restart applies a change.

**Trust boundary.** The owner and mode check stops other users and loose
modes. A process running as the poller's own user (every agent on a host is)
is inside the poller's trust boundary, as it is for a restart: it could already
edit the file and wait for a restart, or replace the poller's own binary, so a
reload grants it nothing a restart did not. No check is added that a same-user
process could not satisfy. (Not judged: the parent directories of the programs
a changed entry names.)

## Why this shape

- A `Config` is a snapshot: never changed once built. The poller reads it
  through `config()` once per pass and uses that one snapshot throughout, so a
  reload cannot land between, say, listing the workers and looking one up. There
  is no lock to forget: the only shared state is the poller's atomic pointer.
  Components wired at start that need the workers as they are now ask the poller
  (`Poller.Current`, used by the launcher-profile source).
- No new `team host status` field: the status JSON is a contract, and the
  events are what an operator already reads from the poller.
- Held `screen-unknown` was already shown by `team host status` (kind and
  reason per worker). It now also prints `SCREEN UNKNOWN` with the four ways
  the deliverer returns that code, since most are host setup, not the screen.

## Tests

- `hostpoller/reload_2623_test.go`: add, remove, change in place, repos,
  an operation for a removed worker (fails with a reason), invalid edit (once,
  and again when the same bad content returns), unreadable file, a duplicated
  worktree, a restart-only setting, Orca wiring (refused; re-adding what was
  wired at start is not), comment-only edit, half-written file, group-writable
  config, a world-writable launch program (and an unchanged one not re-judged),
  the reloadable classification, and readers vs reload (meaningful under
  `-race`; CI runs plain `go test`).
- `cmd/team/host_reload_wake_2623_test.go`: a poller started from a temp config
  does not type a due wake for an unconfigured worker, and after the file gains
  her, the second poll that sees it types the wake into her terminal (fake
  `orca`, temp dirs, an httptest server; red on main). A help pin names the
  events and the rules.
- `host_wakes_1178_test.go`: the `screen-unknown` hint, not on other holds.

## Not done

Restarting the live poller, regenerating `host-epic4229.yaml` and the dormant
workers stay with the host's operators.
