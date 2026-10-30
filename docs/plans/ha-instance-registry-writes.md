# Implementation Plan: `hadron ha create|update|rm`: Home Assistant registry writes

> **Status: implemented; this is the design as built.** GH issue
> [#231](https://github.com/hadron-memory/hadron-cli/issues/231), delta gate
> [6535334162](https://github.com/hadron-memory/hadron-cli/issues/231#issuecomment-6535334162)
> (T2 + scoped §10). The read cut (`ha list|get|ops`, #1028) shipped earlier.
> Server contract read at hadron-server `e213b813a`.

## Server surface consumed

| Op | Notes |
|---|---|
| `createHomeAssistantInstance(orgRef, slug, name, url, accessToken, opAllowlist, enabled)` | org ADMIN/OWNER (`FORBIDDEN` otherwise); bad slug, URL, name or allowlist, or a duplicate slug, is `BAD_USER_INPUT` |
| `updateHomeAssistantInstance(ref, name, url, accessToken, opAllowlist, enabled)` | a missing row, or one in an org you are not a member of, is `NOT_FOUND`; a member who is not an admin gets `FORBIDDEN`. An omitted or null argument keeps the stored value. |
| `deleteHomeAssistantInstance(ref)` | the same gate; hard delete; returns `Boolean!` |

`opAllowlist: []` means **every** catalog op, not none. The token is encrypted
and never returned: the shared `HomeAssistantInstanceFields` has no token field.

## Command surface

```
hadron ha create --org <ref> --slug <s> --name <n> --url <u> [--token-file -|@path|path] [--allow-op <op>]... [--disabled]
hadron ha update <id> [--name] [--url] [--token-file ...] [--allow-op <op>]... | [--clear-allow-ops] [--enabled|--disabled]
hadron ha rm <id> [--yes]                                   (alias: delete)
```

## Design decisions

- **The token is never an argument.** The issue sketched `--token <value>`; the
  gate preferred the repo's existing secret-file and stdin convention, which is
  `secret create --value-file`. `--token-file -` reads stdin, and `@path` or a
  plain path reads a file. On `create`, omitting the flag reads piped stdin, or
  prompts without echo on a terminal. The reader moved from `internal/cmd/secret`
  to `cmdutil.ReadSecretInput` so both groups share one copy; `secret`'s
  messages are unchanged. A zero-length token is a usage error (exit 2), and
  nothing is sent.
- **`update` reads a token only when `--token-file` is given.** An agent's piped
  stdin must never silently replace the stored token. For the same reason a
  passed-but-empty source (`--token-file="$F"` with `F` unset) is refused on
  both commands rather than falling back to piped stdin
  (`review:an-empty-flag-is-not-an-absent-flag`).
- **Patch semantics.** Every optional variable carries `omitempty`, so an unset
  flag is omitted (preserve), never sent as `null`. The allowlist binds to
  `*[]string`: a plain `[]string` with `omitempty` would drop an explicit `[]`,
  and that `[]` is the only way to restore every op. `--clear-allow-ops` sends
  `[]`. It is mutually exclusive with `--allow-op`, and its help says it allows
  every op and points at `--disabled` for allowing none. `--enabled` and
  `--disabled` are mutually exclusive and send real booleans. An update with no
  field flag is a usage error. There is no slug flag.
- **Confirmation.** `rm` uses `cmdutil.ConfirmDeletion`: it prompts on a TTY,
  needs `--yes` otherwise, and sends nothing when refused or declined.
- **Not success.** A null payload from `create` or `update`, or `false` from
  `delete`, is exit 1 with a read-back hint. It never prints the ✓ line.
- **Errors.** Server refusals go through `api.MapErrorKeepingCode`, so `--json`
  keeps `serverCode`. A lost response (exit 7) is "outcome unknown": the write
  was sent once and is not retried, and the message names the `ha get` or
  `ha list` read that checks it. This is the pattern of `app update`. When the
  write sent a token, the error text has that token replaced by `[redacted]`
  (raw and JSON-escaped), in both the terminal and `--json` forms, so a server
  message that echoes it leaks nothing; exit code and `serverCode` are kept.
- **No device contact.** Nothing calls the Home Assistant host. URL and SSRF
  policy and allowlist validation are the server's, and the CLI does not
  pre-validate them.
- **`--json`.** `create` and `update` print the existing `instanceDTO` (the
  `ha get` shape). `rm` prints `{id, status: "deleted"}`, as `mcp-server delete`
  does.

## Verification

`internal/cmd/ha_write_231_test.go`, red before the commands existed:

- exactly one write per command, with exactly the named variables
- `false` and `[]` are sent; omitted fields are absent
- token from a pipe, from `-` and from `@file`; an argv `--token` is refused
- usage refusals send zero requests
- a declined or non-interactive `rm` sends nothing
- refusals keep their exit code and `serverCode`
- null and `false` answers are not success
- a lost response is sent once and reported as outcome unknown
- a sentinel token is absent from stdout, stderr and the error for create, for
  replacement, and for server refusals, including a refusal whose message
  echoes the token (200 and non-200, text and `--json`)
- `--clear-allow-ops=false` is refused before any request, alone or beside
  another field, since acting on the flag's presence would allow every op

The coverage matrix flips the three `gap #231` lines to `command ha …`.
`make schema-check` is clean at `e213b813a`.
