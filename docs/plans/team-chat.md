# Implementation Plan: `team chat` — the group chat as the bound persona (#369)

> **Status: implemented** — design as built. The final command slice of
> [#369](https://github.com/hadron-memory/hadron-cli/issues/369), following
> [team-personas-sessions.md](team-personas-sessions.md) and
> [team-worklog.md](team-worklog.md). Design constraints: D-2026-08-11-003
> (group chats live in the team App memory; the memory is the privacy
> boundary), D10 (the message shape stays coordinated with the hadron-client
> watcher dialect, hadron-client #11/#12), D16 (agent messages carry
> `sessionId`; attribution is the *driving* human via `Session.userId`).

## The one design rule: a single dialect implementation

The CLI already spoke the group-chat message dialect — `hadron chat post/read`
(message-type nodes under a `messagesLoc` prefix, payload in `data`,
timestamped colon-safe locs, reply edges, `@mention` extraction, server `seq`
as cursor and citation) — explicitly mirrored with the hadron-client push
channel that delivers messages into running Claude Code sessions. So `team
chat` re-implements **nothing**: the chat package now exports its dialect core
(`chat.PostMessage`, `chat.CollectMessages`, `chat.Coords`, `chat.Message`,
`chat.Mentions`) and both command groups call the same functions. That is the
D10 coordination made structural — the shape *can't* drift between the two
CLI surfaces, and any future dialect change happens in exactly one place.

Verified against the channel source (`hadron-client src/channel/types.ts`):
`ChatMessage.data` is `Record<string, unknown>` read by specific keys, so the
one team addition — `sessionId` in the payload (D16) — is additive and
channel-safe. `chat.PostInput.Extra` carries it; dialect keys always win over
Extra entries.

## What `team` layers on top

- **Coordinates from the binding**: the chat lives in the bound team memory
  (`session start -m`) under the fixed `chat:messages` loc (one chat per team
  memory by convention; `--messages-loc` overrides). `team init` materializes
  the chat parent node so the chat is a real, copyable portal node from day
  one.
- **Identity from the persona**: handle = the persona name folded onto the
  dialect's handle charset (`handleFromPersona`: lowercase, spaces→dashes,
  rest dropped — so `@mentions` of the persona actually match the channel's
  `MENTION_RE`), role = `personaRole`, identity = the session's model (the
  binding gained a `Model` field), falling back to the tool. The fold is
  lossy ("Dev Rufus" and "Dev-Rufus" both answer to `@dev-rufus`), and the
  server's persona-name uniqueness is pre-fold — so `persona create` skips a
  candidate whose folded handle collides with an existing persona's, exactly
  like a taken name (a client-side guard; a concurrent create can race past
  it, which is accepted — server-side name uniqueness stays the hard gate).
- **The body is positional** (`team chat post <body|->`, the #369 surface);
  `--body`/`--body-file` remain as the `hadron chat`-compatible form, one
  source enforced. `PostInput.Extra` strips the reserved dialect keys, so an
  Extra entry can never masquerade as author/body/identity/role/mentions —
  even when the typed field is empty.
- **`sessionId` in every persona post** (D16): attribution of an agent
  message is the human *driving* the persona — resolved through the session,
  never the persona's creator. `chat.Message` now parses `sessionId` (and
  stored `mentions`) back out, so `--json` readers get it on both `team chat
  read` and plain `chat read` (additive DTO fields).
- **`--reply-to <seq>`**: readers see seqs (the citation primitive), so the
  reply flag takes one and resolves it to the target loc through the message
  list; a non-numeric value passes through as a loc, exactly like
  `hadron chat post`.
- **`--mentions-me`**: stored `mentions` when present, else recomputed from
  the body (hand-created messages carry none). `nextSince` is computed from
  *everything read before filtering* — a mentions-only reader's watermark
  must advance past skipped messages or it re-reads them forever.

A worktree without a session binding is pointed at plain `hadron chat`
(post refuses with exit 4); the persona surface is deliberately
binding-scoped. `end`'s binding-server guard applies to `team chat post` too.

## Why messages are NOT a property-schema collection

#369's sketch had `team init` declaring both worklog and message collection
schemas. The worklog is an object-store collection (fields in `properties`,
queried via `findObjects`) — but the message dialect predates it and stores
the payload in `data`, which is what the hadron-client watcher reads and what
the `team-chat:academy` corpus (118 messages) already is. Moving messages
into the object store would break that coordination for a validation gain the
dialect doesn't need; declaring a schema entry that no message node uses
would be dead weight. So `team init` declares the worklog schema and
materializes the chat *parent*, and the message shape stays governed by the
shared dialect code. If the dialect itself ever migrates (a coordinated
hadron-client + CLI + academy-corpus move), the schema entry comes with it.

## Deferred

- **Driver rendering** ("Iris (backend-engineer) — Holger"): the transcript
  shows persona + role; resolving `sessionId` → `Session.userId` → a display
  name needs a user-lookup join and is portal/later-CLI work. `--json`
  carries `sessionId` today, which is the durable part.
- Academy-corpus migration into a team App memory (protocol-compatible
  as-is; a content move, not a CLI feature).
- The name-register / role-template machinery for `persona create`
  (D-2026-08-11-007) — the last #369 piece besides server-side reaping
  (hadron-server#930).
