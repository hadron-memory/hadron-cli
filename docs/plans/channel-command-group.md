# `hadron channel` — Channels (#593)

Status: **as built** — the whole group in one PR (#598).
Server: spec 049 Phases 4 and 8, plus hadron-server#1172 (addressing).
Issue: [#593](https://github.com/hadron-memory/hadron-cli/issues/593).

## 1. Why this issue was paused, and what unpaused it

`channelRef` originally took only an opaque ULID, while `createChannel`
addressed a Channel as `(memoryRef, loc)`. **A Channel could be created by name
and never named again.** The only client-side remedy was: filter `channels` by
`memoryRef`, match `loc`, take `id` — one addressing rule, reimplemented in the
CLI, the portal and MCP, free to drift.

That is not hypothetical. Two Channels in one account are both `name: "team"` at
`loc: "chats:team"`, separable only by host memory. Three surfaces
disambiguating that independently is three chances to post to the wrong room.

So the work stopped and the gap was reported rather than worked around
(hadron-server#1171 → #1172, merged).

## 2. The rule the group exists to keep

**Nothing in `internal/cmd/channel` classifies a ref.** The server resolves both
the id and the address at every site, so the string the user typed reaches the
wire unexamined.

This is asserted for five spellings — including one that is *not* a ref at all,
so that a future matcher makes the test fail rather than merely changing
behaviour. If that test ever needs relaxing, the question to ask is whether a
third copy of the addressing rule has appeared.

## 3. `chatRootUrn` is nullable, and no renderer may require it

Where a host memory's stored URN predates the flat grammar, the server
advertises **no** address rather than one it would itself reject. Consequences,
both asserted:

- `--json` emits `chatRootUrn: null` as a **present key**. An absent key is
  indistinguishable from "not projected", which is the same defect
  `scope explain` had with a nested `memories: []`.
- The human listing explains the blank ADDRESS cell and names the id as the
  remedy, instead of leaving a reader to wonder why one row differs.

`id` always works. `channel list` prints both columns for that reason.

## 4. Authorship is explicit, because the server's default is the quiet failure

`createChannelMessage(sessionRef:)` is **optional**, and omitting it records the
**human** as the author — no error, wrong authorship, indistinguishable from
success.

It is the only mistake in this surface that cannot announce itself, and the CLI
is the only place it can be caught. So `channel post` requires `--session <id>`
or `--as-me`, refuses *before* the round trip, and **echoes back the author the
server recorded** rather than the one requested.

## 5. Four places the obvious rendering would have lied

| surface | the lie avoided |
|---|---|
| `read --since` | strictly-greater, so the next watermark is the LAST seq seen, not one past it. Reported as `nextSince` so no consumer re-derives it. |
| `read --before` + `--offset` | the server IGNORES offset when before is given. Refused rather than silently half-applied. |
| `mark-read` | monotonic — a lower seq is a no-op. Reports the cursor the SERVER holds, and says when it did not move. Echoing the request would report a rewind that never happened. |
| `read-state` absent | "has seen nothing here" is a real answer, not an error. Making it one would be indistinguishable from "unreadable". |

Spec 049 item J marks a post that crossed Apps; the transcript keeps the mark,
because dropping it misrepresents who was speaking where.

## 6. `rm` is a SOFT delete

`deleteChannel` soft-deletes the Channel and its chat root together; the address
stays reserved while either exists and a restore brings both back. So `rm` uses
`cmdutil.Confirm` with accurate wording, **not** `ConfirmDeletion`, whose prompt
says *"This cannot be undone"* — telling an operator that a recoverable action
is permanent makes them refuse a safe change, which is its own harm.

It also returns a **boolean**: `false` means nothing was deleted. That is checked
(as `schedule rm` and `webhook rm` do, and as `org rm` does not), because
reporting a failed delete as success lets automation treat it as done.

## 7. Errors assert only what is known

`channel(ref:)` returns null for a Channel that does not exist, a ref that names
nothing, and one whose host memory the caller may not read — **identically**, so
it is never an existence oracle. One message covers all three and never claims
non-existence. Same rule as `scope(ref:)`; see
`findings:a-deliberate-server-ambiguity-is-not-a-gap-to-close`.

## 8. The schema refresh was treated as the hazard

Exported from `../hadron-server @ 3e64a5f (main, == origin/main)` — the revision
is stated, per #503 — and then checked for the class #589 hit, where a refresh
silently made `schedule update` clear a scope:

- no new optional input field arrived without `omitempty`;
- `generated.go` did not change at all, so no operation already shipping was
  touched.

`findings:a-schema-refresh-can-regress-an-unrelated-write` carries the grep.

## 9. Not here

- **A `hrn:channel:` URN and a `resolveUrn` branch.** hadron-server left that as
  a spec decision — it is a four-parser grammar change — and the resolver is
  additive, so minting one later costs nothing at any call site here.
- **`--channel` on the existing `hadron chat` verbs.** Those predate Channels and
  hand-roll their own storage shape (#367, #365); wiring a Channel selector into
  them belongs with that migration, not with this group.
- **The register** (`registerEntries`, `attendeeRegister`) — item 3 of the
  spec-049 parity split, still unstarted.
