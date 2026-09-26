# Design as built: team attention, switchover and own mark-read (#1353)

> **Status: built as a DRAFT against hadron-server#1362, a candidate that is
> not merged.** Built at `2ece06ab`; re-pinned at `5378b4fb` after the review
> hardening (#1945, #1954), then at `338b6578` and `6a2b359`. The snapshot
> re-exported at each is byte-identical, so the GraphQL contract did not move. Written 2026-09-25 by Jonas. The schema
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
| `team chat read` | unchanged output; a counted bound read is marked read on the server after it is printed |

### Decisions

1. **The token is passed through and never stored.** A router adopts a token
   only after every listed nudge was accepted, and keeps the previous one on
   any failure. A CLI that saved the token would adopt it before the nudges,
   which is the lost-nudge bug the server contract closes. An empty `--since`
   is refused (exit 2): it is nearly always an unset shell variable, and
   answering the no-token question instead would re-nudge every backlog.
2. **The session header is per CALL, never a client default.** `api.WithSession`
   puts the id on the context, and the transport's `bearerDoer` adds
   `X-Hadron-Session` only to requests whose context carries one: today that
   is only `mark-read` (explicit, and the one after `chat read`). It never
   follows a redirect to another host (`stripSessionCrossHost`, both redirect
   policies; PR review, @copilot). `team attention` never carries it: that poll
   is an operator's read, not a worker's.
3. **`team chat read` marks read AFTER delivery, and carries no session.**
   First built as "the read carries the header and the server advances as it
   pages" (Dara, #1889). Codex's P1 on this PR showed why that loses messages:
   the server marks each page as it is FETCHED, but the CLI prints only after
   the whole loop, so a later page failing — or the render failing — left
   messages marked read that were never shown, and the router stopped nudging.
   So the read is sessionless, and after a successful render the CLI calls
   `markOwnTeamChatRead` through the highest seq it DELIVERED — for exactly the
   reads that record the binding's own watermark (#474: unfiltered, contiguous
   with what the binding has seen, not `--before`, own App, same server; a
   `--limit` page counts). One predicate decides both claims, so they cannot
   disagree. A failed mark errs the loss-safe way (a duplicate nudge), prints a
   stderr note naming the retry, and never fails the read; outside the pilot
   (`FEATURE_NOT_AVAILABLE`) it is silent. Cost: two small calls after a
   counted bound read (the team Channel id, then the mark).
   Known difference from the server's own auto-advance: marking through the
   highest VISIBLE seq leaves a trailing deleted message's seq unmarked, which
   the server's exhausted-page rule would have covered; attention counts only
   live messages, so no nudge results.
4. **Never for another server's binding.** A binding made against another
   deployment neither marks nor sends its session: its id means nothing here.
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

8. **An empty `--channel` or `--proof` is refused, not defaulted.** Each is
   nearly always an unset variable. An empty `--channel` would otherwise mark
   the App's team chat read, which is a different Channel from the one the
   caller meant.
9. **Receipts name their scope.** The App comes from a fallback chain (`--app`,
   the App context, or the binding), so the switchover prompt and receipt and
   the mark-read receipt each say which App, and which branch answered.

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
- Review round 1 (PR #732): Copilot's cross-host redirect finding and
  Codex's P1 (mark after delivery) and P2 (a lost token line fails the poll)
  are fixed, each with a test that reds when the fix is reverted.
- Review round 2 (PR #732, @copilot): the server mark now goes out only if
  the worktree is still bound to the read's session under the binding lock
  (`recordChatWatermark` reports it; a rebind or `session end` mid-render
  skips the mark, "someone read further" still marks), and every human
  receipt after a completed action — switchover preview/apply, mark-read — is
  write-checked. Each reverted fix reds a test.
- Mutation-checked: dropping the server-match guard, the read header, the
  per-call scoping, the empty-`--since` or empty-`--channel` refusal, the
  consent gate, the mark-read header or the already-read branch each reds at
  least one test, and each mutation compiles.
- `App.defaultChannel.id`, which mark-read's default rests on, was observed
  populated on production by a read-only query before building on it.
