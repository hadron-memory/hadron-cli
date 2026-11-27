# `agent instructions` (new) and `agent tasks list --phrase` (replaces `--symptom`)

Unreleased (cli#3267, the companion of hadron-server#5284, PR #5347).

**Minimum server version: 0.48.0** (`HADRON_SERVER_VERSION`; the production server
reported 0.48.0 and served both commands on 2026-11-27). Against an older server
the new commands are refused: a field it does not know makes it reject the whole
query, which is why each new command has a document of its own and the plain
`agent tasks list <agentRef>` is unchanged and keeps working on any server.

## The old flag fails until the CLI is upgraded

`hadron agent tasks list --symptom` is **removed, not aliased**: on this CLI it is
an unknown flag (exit 2), and the server no longer accepts the `symptom` argument,
so a CLI released before this one fails with a validation error from a 0.48.0
server. Upgrade the CLI and use `--phrase`. Holger accepted this break (2026-11-26,
hadron-server#4360 comment 7203654564). `--limit` is unchanged in meaning but the
server's cap is now 10 (default 3) and it refuses a value outside 1 to 10; the CLI
sends it as given.

## What is new

- **`hadron agent instructions "<phrase>" [--event-code CODE] [--agent REF] [--app REF]`**:
  the best-fit role task and its body, or "No task fits" with the nearest tasks
  (marked not instructions) and the fallback task. Needs a phrase, an event code or
  both. `--json` carries the whole answer.
- **`agent tasks list <agentRef> --phrase "<text>"`** adds a `SCORE` column and a
  `score` field in `--json` (`<agentRef>` is still required).
- **A missing score is `null` in `--json` and prints "no score"**, on both commands.
  It is never 0.

## Behaviour to know

- A bound worker may leave out `--agent`: the CLI sends none and the server uses the
  worker's own Agent. A caller with no Agent of its own gets the server's refusal.
- **The call is App-less unless you name an App.** `--app` is sent when given, and
  the configured App is sent only with `--event-code` (the register needs one). A
  phrase-only call never sends the configured App.
- The CLI decides nothing and keeps nothing: no ranking, floor or class check, and
  no history, log or cache of the phrase or event code.

## Ships with

hadron-server#5284 (PR #5347, `a5f79802`). Schema snapshot refreshed from that
revision.
