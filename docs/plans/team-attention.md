# Design as built: team attention, switchover and own mark-read (#1353)

> **Status: built as a DRAFT against hadron-server#1362 at `2ece06ab`, a
> candidate that is not merged.** Written 2026-09-25 by Jonas. The schema
> snapshot must be re-exported from #1362's merge before this lands. This is
> the CLI-parity half of hadron-server#1353's first slice (A + B + F), per the
> issue's "Client follow-ups": `hadron team attention`.

## 1. What the server provides (#1362)

An **internal pilot**, gated per operator+App by two exact allowlists
(`TEAM_ATTENTION_PILOT_USER_IDS` / `_APP_IDS`). Outside the gate every entry
point refuses `FEATURE_NOT_AVAILABLE`, and ordinary reads behave exactly as
before.

| Operation | What it does |
|---|---|
| `teamAttention(appRef, since)` | The caller's LIVE workers with relevant unread newer than the token, plus a new signed token. No bodies, no read-state change. `since:"now"` is refused. |
| `teamAttentionSwitchoverPreview(appRef)` | Read-only: each worker's cursor, the captured head, the unread it would clear, plus a 10-minute proof. |
| `confirmTeamAttentionSwitchover(appRef, proof)` | Advances exactly the previewed cursors, atomically, or refuses on any drift. |
| `markOwnTeamChatRead(appRef, sessionRef, channelRef, seq)` | Advances one caller-owned live worker session's own cursor. It shares the MCP `hadron_team_chat_mark_read` helper and gate. |
| `teamChatMessages` / `channelMessages` | With a bound session (`ctx.sessionId`, from `X-Hadron-Session`) inside the pilot, a read advances that worker's cursor if it is unfiltered, contiguous and forward. |

## 2. What the CLI does

| Command | Operation |
|---|---|
| `team attention [--since <token>]` | `teamAttention` |
| `team attention switchover preview` | `teamAttentionSwitchoverPreview` |
| `team attention switchover apply --proof <p> [--yes]` | `confirmTeamAttentionSwitchover` |
| `team chat mark-read --through <seq> [--channel <ref>]` | `markOwnTeamChatRead` (plus `app.defaultChannel` when `--channel` is omitted) |
| `team chat read` | unchanged, except that a bound read now carries `X-Hadron-Session` |

### Decisions

1. **The token is passed through and never stored.** A router adopts a token
   only after every listed nudge was accepted, and keeps the previous one on
   any failure. A CLI that saved the token would adopt it before the nudges,
   which is the lost-nudge bug the server contract closes. An empty `--since`
   is refused (exit 2): it is nearly always an unset shell variable, and
   answering the no-token question instead would re-nudge every backlog.
2. **The session header is per CALL, never a client default.** `api.WithSession`
   puts the id on the context, and the transport's `bearerDoer` adds
   `X-Hadron-Session` only to requests whose context carries one. Exactly two
   operations carry it: the `team chat read` pages and `mark-read`. The header
   has server-side effects (a heartbeat, and in the pilot the worker's own read
   state), so Dara and Ada scoped it narrowly (team chat #1885, #1889).
   `team attention` never carries it: that poll is an operator's read, not a
   worker's.
3. **The CLI does not decide which reads count.** `team chat read` sends the
   header whether or not a filter or `--limit` is set. The server advances only
   unfiltered, contiguous, forward pages. A `--limit` page that is contiguous
   counts, which Dara confirmed in #1889, so a client-side rule would have been
   wrong. The binding's own client-side watermark (#474) is untouched: it is a
   separate, local claim.
4. **Never for another server's binding.** The header rides only when
   `bindingServerMatches`. A session id from one deployment means nothing on
   another.
5. **Mark-read uses the pilot door, not `advanceChannelReadState`.** The legacy
   mutation has a similar gate (the caller's live session on that worker), but
   it is neither pilot-gated nor pinned to one session. So a CLI built on it
   would be a wider door than MCP. #1362 added `markOwnTeamChatRead` for parity
   at the CLI's request (Q2 in #1878).
6. **Switchover apply asks for consent.** It discards a backlog, so it prompts
   on a TTY and requires `--yes` otherwise. The proof alone is not consent: a
   script can hold a proof it never showed anyone.
7. **Discovery is the call itself.** GraphQL `serverInfo` has no capability
   list (MCP's `hadron_server_info` advertises `team-attention` per caller), so
   the CLI calls the operation and maps the refusal.

### Exit codes

| Code | Exit | Why |
|---|---|---|
| `FEATURE_NOT_AVAILABLE` | 8 | a "not for you" refusal (the pilot gate) |
| `INVALID_ATTENTION_TOKEN`, `INVALID_SWITCHOVER_PROOF`, `SEQ_BEYOND_WATERMARK` | 2 | arguments the caller can fix |
| `SWITCHOVER_CONFIRMATION_REQUIRED`, `TEAM_ATTENTION_SCOPE_TOO_LARGE` | 2 | already mapped by the existing `_REQUIRED` / `_TOO_LARGE` suffix rules |
| `ATTENTION_TOKEN_STALE`, `SWITCHOVER_PROOF_STALE` | 5 | state moved: poll again without `--since`, or preview again |

`SESSION_NOT_LIVE` (mark-read with a session that isn't live) stays at the
generic exit 1. It is already returned by `team chat post`, and remapping it
here would silently change that command's contract too. It deserves its own
decision.

## 3. Not in this change

- `--wait` (long-poll) is #1353 slice D, and not in the server yet. No flag was
  added, because a flag with no effect is a defect.
- Delivery addresses (C) and router registration (E).
- Rewriting `core:tasks:claude-chat-monitor` around the poll. That is a memory
  task, not CLI code.

## 4. Verification

- Offline command tests (`internal/cmd/team_attention_test.go`) cover:
  - token passthrough, and an empty `--since` refused;
  - `workers: []` when idle;
  - every refusal's exit code;
  - switchover preview being one read, and apply refusing without a proof, and
    without consent (TTY decline and non-interactive both send nothing);
  - the session header present on a bound read, including `--limit` and
    `--mentions-me`, and absent from the App lookup, unbound reads,
    cross-server bindings and the attention poll;
  - mark-read's default-Channel lookup, `--channel`, session pinning, below-cursor
    wording and refusals.
- `internal/api/session_test.go`: the header rides only on a call that asked
  for it.
- Mutation-checked: dropping the server-match guard, the read header, the
  per-call scoping, the empty-`--since` refusal, the consent gate, the
  mark-read header or the already-read branch each reds at least one test.
