# Design as built: retire `team attention` and `team router` (#935)

The operator attention surface is retired with no replacement API
(hadron-server#2027, decision
[issuecomment-6567317725](https://github.com/hadron-memory/hadron-server/issues/2027#issuecomment-6567317725)).
Nothing reproduces the unread scan, the `--since` token or the switchover
proof. Under cor:api:240:02 this is a retired platform capability, not a CLI
omission. This slice is the CLI's half: retirement stubs plus removal of the
client code only those commands fed. It removes nothing on the server and is
not a release.

## The stubs persist

`team attention`, `team attention switchover preview|apply` and
`team router poll|loop|status` stay as commands with no expiry, so an old
script or habit gets a readable answer instead of `unknown command`
(`internal/cmd/team/retired.go`). Removing them outright is a separately gated
change with its own compatibility note.

Every stub:

- returns `exitcode.Usage` (2) with fixed text, and under `--json` the
  standard error envelope;
- does nothing else: it reads no config, credentials, binding or router state
  directory, makes no request, types into no terminal and marks nothing read.
  The factory is not touched (`newCmdAttention(_ *cmdutil.Factory)`);
- never echoes a `--since` or `--proof` value. The message is a constant
  passed as a `%s` argument, never as a format;
- still parses its old flags (`--since`, `--proof`, `--yes`, `--config`,
  `--interval`) so the message is reached. An unknown flag stays an ordinary
  cobra usage error.

The text is the gate's final copy, verbatim:

| Command | Message |
|---|---|
| `team attention` | `hadron team attention is retired (hadron-server#2027). It has no replacement unread or token view. Read your own chat with "hadron team chat read".` |
| `team attention switchover preview\|apply` | the same, plus ` No backlog was marked read.` |
| `team router poll\|loop\|status` | `hadron team router is retired (hadron-server#2027): the attention API it polled is being removed. Wakes come from tickers (hadron-server#2138). Nothing was polled or typed.` |

Each `Short` begins `Retired (hadron-server#2027)`, and each `Long` repeats the
message and says the flags are accepted and ignored.

## What was removed, and why nothing else calls it

Every symbol below was checked with `git grep` for non-test callers at the
frozen head.

| Removed | Only caller on main |
|---|---|
| `team.graphql`: `TeamAttentionPage`, `TeamAttention`, `TeamAttentionPreviewPage`, `TeamAttentionSwitchoverPreview`, `ConfirmTeamAttentionSwitchover` (+ generated code) | `internal/cmd/team/attention.go` |
| `internal/cmd/team/attention.go` (the commands, their DTOs and renderers) | `team.go` command wiring, now pointing at the stubs |
| `teamrouter.graphql` `RouterAttentionPage` (+ generated code) | `teamrouter.GraphQLServer.AttentionPage` |
| `GraphQLServer.AttentionPage`, `AttentionRequest`/`AttentionPage` types | the router engine (`router.go`) |
| `internal/cmd/team/router.go` (poll/loop/status) | `team.go` wiring |
| `teamrouter` engine: `router.go`, `liveness.go`, `nudge.go`, `screen.go`, `state.go`, `status.go`; the `router` methods in `orca.go` (`listTerminals`, `isIdle`, `readScreen`, `sendNudge`) and helpers only they used (`parseEnvelope`, `orcaEnvelope`, `clip`, `findRequestID`, `optional`, `Terminal.identity`, `RosterEntry.flagged`) | `internal/cmd/team/router.go` |
| `writeSwitchoverApplyHint` (`terminal_metadata.go`) | `attention.go`. Its quoting pin moved to `writeHostCursorHint`, which shares the quoting |
| Tests of the removed code: `router_*_test.go`, `fakes_test.go`, `team_router_test.go`, `attention_test.go`, the attention cases in `team_attention_test.go` (its chat-read tests and helpers stay), the attention rows of the #1112 metadata test with their goldens, and the #1101 safe-cells attention case | — |

**Survivors** (named by the gate; `team worker onboard` uses them):
`teamrouter.LoadConfig`, `Config`, `ExecOrca`, `OrcaRunner`, `RunResult`,
`Terminal`, `NewGraphQLServer`, `RegisterRow` (+ `RouterRegister`),
`CheckRosterEntry`, `PutRosterEntry` and the `withLock` it takes.
`GraphQLServer.ReadCursor`/`ChannelHead` (`RouterReadCursor`,
`RouterChannelHead`) are kept because the gate names them, although no
production code calls them after this slice. `RouterError` and `jsonInt` moved
into `config.go`, and `RegisterRow`/`CursorResult` moved into `server.go`,
because the files that used to hold them were deleted. `BreakStaleLock` had no
production caller even on main and is left as it was.

The coverage matrix classifies the five removed `team.graphql` fields with the
existing class: `excluded deprecated: retired with no replacement
(hadron-server#2027, comment 6567317725)`. No new class was invented.

## Onboarding

`team worker onboard --apply` waits for a router reload receipt that only the
retired `team router loop` wrote. The fix (drop the wait, add a
routing-unverified line, remove `ReadReload`/`RecordReload`) is a gate
addendum awaiting a scoped FIT
([#935 comment 6568325982](https://github.com/hadron-memory/hadron-cli/issues/935#issuecomment-6568325982)).
This section is updated with the as-built result once the addendum is ruled.

## Not in this slice

Server removal and its release note (S11, hadron-server#2027); the TS router's
retirement (C6, hadron-server#2030); skills and docs retirement (M5);
spec amendments cor:api:020:01 and cor:agt:020:04; a CLI release. The v0.34.0
tag still carries every removed operation, so the first release after this
merges is the one to scan for released no-caller evidence.

## Release-note draft (CLI)

**Changed**

- `hadron team attention`, `team attention switchover preview|apply` and
  `team router poll|loop|status` are retired (hadron-server#2027). They now
  exit 2 with a retirement notice and do nothing else: no request, no config or
  credential read, no terminal typing, nothing marked read. Read your own chat
  with `hadron team chat read`; wakes come from tickers (hadron-server#2138).
  Scripts that polled `team attention --json` must stop: there is no
  replacement unread or token view.

## Pins and mutants

`internal/cmd/team_retire_935_test.go` (red on main first):

- every stub argv, in text and `--json`: exit 2, the exact copy, and the
  envelope; trap seams on `ConfigFn`, `TokenStoreFn`, the HTTP transport, the
  binding path and a sentinel router config (an orca marker and a state dir
  that must stay untouched); no `--since`/`--proof` value in any output;
- unknown flags are usage errors;
- every stub's help says it is retired, and its row in the parent's command
  list (its `Short`) starts `Retired (hadron-server#2027)`;
- `team_retire_935_fifo_test.go` (unix): the router config named by
  `--config`, `HADRON_ROUTER_CONFIG` and `ORCA_ROUTER_CONFIG` is a FIFO, and a
  non-blocking writer probe detects any open of it, so a stub that only parses
  the config and ignores the result is caught too.

One production mutant per guard, each killed: non-Usage exit; changed
message (attention, switchover tail, router); flag value echoed; a config
read; a removed old flag (`--proof`, `--interval`); help no longer saying
retired, in its own help and in the parent listing; the router stub parsing
its `--config` with the error ignored (caught only by the FIFO pin).
