# `team host assign` takes `--effort` and `--account-profile`; `--model` is checked like them

Unreleased (cli#2157, over hadron-server#4230, S1b). `hadron team host assign` records the
launcher profile of a worker's assignment on a host:

```
hadron team host assign Jonas --host orca1 --workdir jonas-3 --family codex \
  --model gpt-5 --effort high --account-profile team-a
```

## New

- `--effort <token>` and `--account-profile <token>`, next to `--model`. While the App's
  switch is SERVER, a worker start carries the ones that are set; the host builds its launch
  command from a fixed template for the family, never from these values as text.
- `team host setup` (text and `--json`) and the `team host assign` receipt show `effort` and
  `accountProfile`. In `--json` the keys are always present: a token, or `null` when unset.
  The table gains `EFFORT` and `ACCOUNT` columns.
- The server's `BAD_LAUNCHER_PROFILE` (a bad profile value, or a family with no launcher: claude,
  codex, glm or kimi) is exit 2, with the code kept.

## Changed (user-visible)

- **`--model`, `--effort` and `--account-profile` all take a profile token**: letters, digits and
  `.` `_` `-`, starting with a letter or digit, at most 128 characters. A `--model` value with a
  slash, `@` or `+` was accepted by an older CLI and is now refused locally (exit 2, nothing
  sent), as the server refuses it.
- **`--replace` rewrites the whole assignment.** The server stores exactly what a replace
  carries, so a profile value (the model included) that you do not pass again is **cleared**.
  The CLI never sends a null to do that: an omitted flag is omitted from the request. On a new
  assignment an omitted flag simply leaves the value unset. Pass every value you want kept.

## Older servers

The profile is read and written through its own documents, so nothing else changes on a
server that predates it (hadron-server#4230): `team host up`, `team host import` (the dry run
too) and a plain `team host assign` use the same documents as before. On such a server,
`team host setup` prints the assignments without the profile (`--json` says
`"profile": "unavailable"`, and `effort` and `accountProfile` are `null`, meaning unknown, not
unset), and `--effort` or `--account-profile` is refused by name (exit 2) with nothing written.
