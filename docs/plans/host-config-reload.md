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
restart.

The configuration file is always judged. Of the entries, only those that
changed are judged: an entry that is the same in the new file as in the
running one is not judged again. For each worker that was added or whose entry
changed: the first word of its `launch`, `end` and `recover` (when not blank),
its `codex_home`, `claude_config_dir` and `disk_tmp`, and the program of the
account its launcher profile names (even when that account itself did not
change). For each account that was added or changed: its program (`bin`) and
its `codex_home`. For each repo that was added or whose path changed: the new
path. A worker that was removed names nothing to judge. A setting that is read
only at start (key file, state file, `orca_launch`, `delivery`,
`reset_context`, and the rest) is not judged, because it is not applied. A
name with no slash is looked up on `PATH` and the program found is judged; a
name that is not found is skipped.

Each judged file and directory must belong to the poller's user or root and
not be writable by group or others. So must every directory above each of them and
above the file, up to `/`, unless it is sticky (like `/tmp`: others may add
entries, not replace ours): someone who can write a directory on the way can
replace what is below it without touching its mode. The config file is judged
on the one open file its bytes were read from (so it cannot be swapped between
the read and the check; a symlink is followed once, at the open); named paths
are resolved one component at a time, as the kernel does, and every directory
the resolution touches is judged: each directory a component is looked up in
and, when a component is a symlink, those its target is resolved through (a
chain `a/link -> b/link2 -> c/prog` passes through a's, b's and c's directories:
whoever can write any of them can repoint a link). Each symlink followed must
itself belong to the poller's user or root, since a sticky directory (which the
directory rule accepts) lets anyone add one and its owner chose where it
leads. A path that does not exist yet is judged as far as it does. Not
enforced on Windows, where a restart applies a change. A host whose config
lives under a group-writable, non-sticky directory will see every reload
refused until that is tightened.

**Trust boundary.** The owner and mode check stops other users and loose
modes. A process running as the poller's own user (every agent on a host is)
is inside the poller's trust boundary, as it is for a restart: it could already
edit the file and wait for a restart, or replace the poller's own binary, so a
reload grants it nothing a restart did not. No check is added that a same-user
process could not satisfy.

**A removed worker.** Taking a worker out of the file does not stop its agent.
`worker.end` for it is still carried out, with the entry the worker last had
(kept by worker id when the reload applies, forgotten if the worker comes
back), and a `removed-worker-end` event says so: ending a session only
de-escalates. Every other operation for it is reported failed "not configured
on this host", and a recovery that would *start* an agent stays refused. A
reload on the very first poll has no `workerRefs` yet (registration has not
run), so a removed worker's id is not remembered; it only matters for an agent
left from a previous poller process.

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
- `hostpoller/reload_followups_2653_test.go` and
  `reset_removed_2653_test.go` (cli#2653): the profile-account program of a
  changed worker though the account did not change, a `Readlink` failure on a
  followed link (through the per-poller `trustWalk` seam, which also lets the
  lstat-override test run in parallel), `noteRemoved` forgetting a worker that
  came back, and both reset-handler arms for a worker a reload removed
  mid-handler (the Git read) and after the checkpoint.

## Not done

Restarting the live poller, regenerating `host-epic4229.yaml` and the dormant
workers stay with the host's operators.
