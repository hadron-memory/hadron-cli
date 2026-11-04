# Chat config show and doctor (#477, read-only cut)

A `.hadron/config.json` that names the wrong chat fails silently: `chat read`
and `chat post` accept whatever it resolves to, and a malformed file is
ignored without a word. This cut adds two read-only commands that say what the
chat resolves to and why. Gate: Xavier CHECKED T1 (6368862543), accepting
Jane's attach (6359117143). The config writer, setup at bind time and any
successor/retired-chat fact stay on #477 behind their own gate.

## One resolution path

`resolveCoords` now delegates to `resolveCoordsTraced`, which returns the same
`Coords` plus a trace: the form taken and every non-blank input, in precedence
order, with its source and whether it won. `chat config show` and `chat doctor`
print that trace; read and post discard it. Because there is only one resolver,
the report cannot drift from what a post does.

Precedence is unchanged. It runs flag, then `HADRON_CHAT_*`, then config. The
node form, from any source, beats the two-field `memory`+`messagesLoc` form.
A blank value counts as absent.

`loadProjectChat` likewise delegates to `findProjectChat`, which also returns
how the ancestor search ended:

- `loaded`: the file was found and parsed.
- `malformed`: the file was found but did not parse. The search stops there
  and uses nothing from it.
- `absent`: no file was found.

A file the search could not read at all is walked past to an ancestor, and is
listed under `skipped`. The search behaviour itself is unchanged.

## Allowlist, not redaction

The DTOs are built from the parsed known keys only, never from the raw file.
The known keys are `chat.node`, `chat.memory`, `chat.messagesLoc`, and the
legacy `handle`, `chat.identity` and `chat.role`. So `appKey` and any
unrelated key cannot reach output by construction. A parse error is
encoding/json's message, which names an offset or a Go type and never echoes
the file's bytes.

The legacy identity values are reported under `deprecated` with
`effect: "none"`. They are deprecated no-ops for a post, and are never
presented as an identity or binding.

## Doctor

Doctor runs the same report, then makes one `GetChannel` read at the chat
root. It composes that root with `chatRootRef`, so an opaque memory id costs
the same single `GetMemory` read a post would. Doctor never posts, never
creates the Channel and never writes config. The tests fail if it sends any
operation other than `GetChannel`.

- `channel.status` is one of:
  - `readable`: the read returned the Channel's id, name, `chatRootUrn` and
    host memory URN.
  - `unavailable`: the read returned null. That null covers a missing Channel,
    a ref that names nothing, and a host memory the caller may not read; the
    server answers all three identically, so doctor does not say which.
  - `error`: the read failed outright (not signed in, a refusal, a transport
    failure). The mapped error is the problem's detail.
  - `not-checked`: the coordinates did not resolve, so no read was made.
- `channel.lifecycle` is always `"not-provided"`. The Channel SDL has no
  archived/live/superseded fact. `lastSeq` and `lastMessageAt` are left out
  rather than offered as a proxy: an old timestamp means a quiet chat, not a
  retired one.
- Exit codes: doctor ALWAYS prints the report, then exits with the first
  failing check's code, in the order config, coordinates, channel.
  - 2: a malformed or skipped config file, or unresolvable coordinates.
  - 4: no readable Channel.
  - A failed read's own mapped code (for example 3, 8 or 7), when the channel
    is the first failing check.
  - 0: every check passed.

  A malformed file fails doctor even when flags resolve the chat, because that
  file is what a bare post silently ignores; it keeps exit 2 even when the
  Channel read is then refused. Every failure is a check recorded in
  `problems`, never an early return (Joost's BLIND P2 on cli#1029: the first
  freeze returned on a failed read, losing the report and overriding an
  earlier config failure).

`config show` never fails on any of these conditions. It reports them and
exits 0; doctor is the pass/fail check.

## Limits

- The behaviour is pinned against the fake GraphQL server only. No stack or
  live config was used.
- No successor or superseded mapping exists on the server, so neither command
  can name the chat a stale config *should* point at. That requires a server
  contract first.
