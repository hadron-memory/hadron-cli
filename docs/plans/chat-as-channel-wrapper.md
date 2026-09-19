# `hadron chat` as a thin wrapper over Channels (#367)

Status: **slice 1 — the write path.**
Supersedes the approach in #367's original "Fix" section; see §1.
Related: #365 (closed — the storage shape already landed), hadron-server#921,
#1172, #1196 (lifted the blocker in §0).

## 0. The blocker this work stopped on, and why it is gone

This branch sat unmerged because `createChannel` refused any host memory that
was not app-class:

> A Channel is hosted in an app-class memory; knowledge cannot host one yet
> (the chat-host typology, spec 028, is not built).

That was fatal rather than inconvenient. `hadron chat` addresses arbitrary
memories by design, and two of the three memories actually holding chats were
knowledge-class — including a live team chat. A straight swap would have broken
posting for them: a regression worse than the two bugs the swap fixes.

**hadron-server#1196 removed the restriction** — a Channel may now be hosted in
a memory of ANY class, and participation derives from the host. `@dara` reported
it settled on merged `main`; the codes are retired rather than merely unthrown
(`CHANNEL_HOST_NOT_APP` / `CHANNEL_HOST_NOT_APP_CLASS` survive only in comments
and tests documenting their retirement).

Re-measured here the same way the blocker was found — by running it, not by
reading it — against `hadronmemory.com:experiments`, a knowledge-class memory:

```
$ hadron channel create jonas-367-probe -m hrn:mem:hadronmemory.com:experiments \
    --loc chats:jonas-367-probe
address   hrn:node:hadronmemory.com:experiments:chats:jonas-367-probe

$ hadron channel post <address> "probe" --session <worker-session>
✓ posted #1 as Jonas

$ hadron channel read <address>
#1 [Jonas] (cross-App) … probe
```

Create, post and read all succeed on a knowledge-class host; the probe Channel
was deleted afterwards. **The fallback-to-the-hand-rolled-path question the WIP
commit escalated is therefore moot** — there is nothing to fall back for, and no
transitional two-writer split to decide about.

## 1. Why the issue's own remedy is not the one we are building

#367 says it is blocked on hadron-server#921 (storage-level `createChat` /
`createChatMessage`). #921 is still open, and we are **not waiting for it**.

Spec 049 shipped the same capability under a different name, after #367 was
filed. Measured rather than assumed — the two hierarchies are not merely
compatible, they are **the same nodes**:

```
chat           chats:team                                  ← the Channel entity
record         chats:team:messages                         ← plain container
chat-message   chats:team:messages:001-8bbb958e-holger     ← server-minted
```

So `hadron chat read` already reads a Channel with **no code change at all**:

```
$ hadron chat read --node hrn:node:…:hadron-dev-team-shared:chats:team:messages --since 670
[671] dara: Dara — **PR #1182 MERGED …**
```

**The read path is a no-op. Only the write path is work.**

## 2. What the swap buys, beyond tidiness

Both of #367's live bugs are fixed *by construction*, not by us:

- **Loc collision** (`<stamp>-<handle>`, millisecond, no random component —
  two posts by one handle in the same millisecond collide on
  `@@unique([loc, memoryId])`). The server mints `001-8bbb958e-holger`: an
  ordinal plus 8 hex. We stop owning the format.
- **First message invisible to incremental reads** (generic `createNode` leaves
  `seq` null with no seq-bearing sibling; `read.go` skips nil-seq when
  `--since > 0`). The server assigns seq atomically.

And #365's residual item 4 dissolves: a `findNodes` exclusion set cannot break
a read that does not go through `findNodes`.

## 3. Addressing — the one real mapping

A Channel is addressed by its id **or its chat root's node ref** (#1172).
`hadron chat`'s coords name the **messages container**, one level below the
chat root. The derivation is the one the now-deleted `EnsureChatParent` made by
hand, kept as `chatRootRef`:

```go
chatLoc   := MessagesLoc[:strings.LastIndex(MessagesLoc, ":")]
channelRef := cmdutil.NodeURN(Coords.Memory, chatLoc)
```

`Coords.Memory` is a memory REF (URN or `org:slug`), so `cmdutil.NodeURN`
composes exactly the flat-v2 node ref `channelRef` accepts. **No client-side
Channel lookup, no matching on loc** — the rule the `channel` package exists to
keep (`internal/cmd/channel/channel.go`) applies here too.

Create-if-missing is `createChannel(memoryRef, loc, name)`, which replaced
`EnsureChatParent`'s two best-effort `createNode` calls (§5b). Unlike those, it
is **not** best-effort: without a Channel there is nothing to post to.

## 4. Authorship — three flags, not the two that were ruled on

@ada ruled **(a)**: deprecate `--identity` / `--role`, do not ask for a server
envelope argument. Measured basis, corrected: the envelope IS used — 15/15
messages carry `data.role` and 9/15 carry `data.identity` in
`hadronmemory.com:experiments` — but only there, last written 2026-08-13, and
the values (`"Claude Opus 5"`, `"Backend Engineer"`) are **exactly what the
Worker model derives**. Superseded, not unused.

**Implementation surfaces a third flag the ruling did not cover: `--handle`.**
It is load-bearing today — it goes in the loc and in `data.author`. Under
Channels the server sets `authorName` from the caller, so a `--handle` naming
someone else would be impersonation and the server is right to ignore it.

So all three go the same way, and the honest consequence must be stated rather
than discovered: **an agent that wants to appear as itself must bind a worker
session.** `chat post` therefore gains `--session`, mirroring `channel post`.
That is the Worker model doing the job these three flags were approximating —
`hadron-server#1157` is the same argument.

- `--handle` / `--identity` / `--role`: accepted, **ignored**, warn once on
  stderr naming `--session`. Not removed — a config carrying them must not
  start failing.
- The read path keeps parsing `data.identity` / `data.role` / `data.author`, so
  the 15 historical messages stay readable. Deprecating a writer must never
  orphan a reader.

## 5. `--reply-to` — a loc, where the server wants a seq

`chat post --reply-to` takes a **loc or URN** and mints a `reply` EDGE.
`createChannelMessage` takes `replyToSeq: Int` and wires the edge server-side.

Accept both, Postel-liberal like every other ref in this repo: a bare integer
is a seq; anything else is resolved to its seq with one read. Refuse loudly if
it resolves to a node with no seq — a reply pointing at nothing is worse than a
refusal.

## 5a. The one `--json` shape change

`chat post --json` was `{loc, seq, replyTo, author}`. The client used to mint
the loc and could therefore report it; the server mints it now and
`createChannelMessage` answers with `nodeId` instead.

`loc` is **kept and answered `null`**, not dropped. An agent selecting `.loc`
must be able to tell *"this post has no address I can give you"* from *"this
projection forgot the field"* — the same call `internal/cmd/channel` makes for
`chatRootUrn`, for the same reason. `nodeId` is added as the address that
replaces it.

Pinned by `TestChatPostJSONKeepsLocAsAnExplicitNull`, which asserts over the raw
key set rather than a decoded struct: decoding cannot tell a null from a missing
key, so a struct-based test would pass against both ways of getting this wrong.
Mutation-tested in both directions — dropping the field and filling it with `""`
each fail it, with a different message.

## 5b. Dead code removed rather than left as a fallback

`EnsureChatParent` and `ConvergeChatParent` hand-built the chat entity and the
messages container with best-effort `createNode`/`updateNode` pairs.
`createChannel` is the server operation that does this, so both are deleted —
keeping either as a fallback would reinstate the two-writer split that is the
whole complaint of #367. (`ConvergeChatParent` had already lost its last caller
before this change; the `hadron team init` retyping advice in `chat post`'s help
went with it, since nothing needs converging once the server owns the structure.)

`PostInput` loses `Handle`/`Identity`/`Role`/`Extra` for the same reason: an
assigned-but-never-read field is indistinguishable from one that works. The
*flags* survive at the command layer, where `warnDeprecatedIdentityFlags`
answers for them.

## 5c. One read-path fix that IS in scope, because this change caused it

§6 keeps `chat read` on `findNodes`. One line of it still had to change, and
the reasoning is worth stating because the defect was invisible to the
end-to-end check that was supposed to catch exactly this.

`parseMessage` falls back to `authorFromLoc` when a message carries no
`data.author`. Server-minted messages carry none — the author lives on the
message projection — so this fallback went from a legacy path to **the** path.
Its last branch was `strings.LastIndex(last, "-")`, which reads the server's
`002-279bba33-mary-jane` as `jane`: a message attributed to a person who does
not exist, silently. The client-minted dialect never had this failure, because
its `Z-` terminator bounded the handle.

So the write-path change made a latent misparse reachable, in a file the change
did not otherwise touch. Fixed by trying an anchored server-format pattern
first, with the three dialects ordered most-specific first and the ordering
pinned by a test.

**The end-to-end run did not catch it**: the probe posted as `jonas`, and a
handle with no dash parses correctly under both the right rule and the wrong
one. Only a dashed handle separates them — and those are ordinary, since a
worker name with a space slugs to one. Captured as
[`findings:handing-a-format-to-the-server-makes-the-clients-parser-wrong`](https://hadronmemory.com/app/u/hrn:node:hadronmemory.com:hadron-cli:findings:handing-a-format-to-the-server-makes-the-clients-parser-wrong).

## 6. What is NOT in this slice

- **`chat read` → `channelMessages`.** It works unchanged and gains only a
  nicer cursor; moving it would break reads of chats that have no Channel row.
  Deliberately deferred so a read regression cannot hide inside a write change.
- **hadron-client.** Being retired (@holger, 2026-09-16), so there is no
  companion port and no lockstep. This is what removed the objection that kept
  the loc format frozen.
- **hadron-server#921.** Its storage-primitive half is delivered by Channels;
  its chatbot-engine half (`startChat`, engine state on the chat root) is
  untouched by any of this and is @dara's to narrow or close.
