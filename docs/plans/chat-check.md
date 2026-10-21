# Team chat check (#957)

`hadron team chat check` combines a tagged channel scan, private artifact output,
and an own-session mark. It is ordered, not a distributed transaction. Provider
schema comes from server merge `5705c2ca43752aa12de5912307299afc44f0e577` (#2194,
#2181). Gate6306283744, FIT6306216826, retry amendment6307282821 apply.

## Range and authority

Only the worktree's canonical bound App/server/worker/session is used. A complete
binding is required, an ended/wrong-worker session is refused, and the binding is
rechecked before attributed requests. An open lapsed session may be revived by
the server's guarded served-page path. A worktree lock excludes duplicate local
checks; cross-worktree checks may show duplicates, never roll back a cursor.

The first suppressed team-chat envelope resolves the actual convenience channel
(the server's oldest live team channel, which is not assumed to be App.defaultChannel).
Sample cursor C, then committed message head H on that pinned channel/incarnation.
Every subsequent page uses channelMessages, explicit sinceSeq C, beforeSeq,
expectedIncarnation, bound worker mentionsRef and advanceReadState:false.
The server performs canonical mention reach; the CLI never reparses @all/team.

**Wire-aligned traversal:** beforeSeq selects a backward page even when sinceSeq
is set. Start before H+1, request the full window C<seq<before, lower before to
the returned minimum, and drain to an empty page (including after short pages).
Sort the complete result ascending for output. Forward nextSince with beforeSeq
would silently skip older pages; the sparse three-page fixture pins this.
Duplicate/non-progress/out-of-window sequences or changed envelopes fail closed.
New arrivals above H are neither included nor marked by this invocation.

## Output and exits

Every nonempty result goes to a fresh exclusive0700 directory under the user's
cache; its JSON file is0600. Write, byte count, file sync/close, rename and directory
sync must all succeed. Then print the quoted path on stderr and check that write
before marking. No message bodies reach stdout, including --json; tool-level
truncation is not observable, so the path receipt is bounded instead. A complete
artifact is retained after later failures; partial directories are cleaned on
ordinary failures. An uncatchable crash may leave private partial files. The
cache filesystem must support directory sync; an unsupported/full/unwritable
filesystem fails before mark. Actual Windows runtime/filesystem behavior is not
author-validated. No automatic artifact deletion or retention policy is imposed.

Open the artifact to **read** the messages; the command proves written/marked,
not human comprehension. Marking H also marks untagged messages in the gap.
Empty ranges print a short notice before advancing H and create no artifact.

| Exit | Meaning |
|---|---|
|0|nonempty artifact/path completed; sampled cursor coverage confirmed|
|9|empty tagged range; sampled cursor coverage confirmed|
|10|mark attempted, outcome unconfirmed|
|1–8|existing failures; inspect receipt markStatus after any mark attempt|

The bounded JSON receipt is a package DTO with error (null or code/message),
outcome, sampledCursor, sampledHead, messageCount, artifactPath (null if absent),
markAttempted, markStatus and markVerification. Errors before a snapshot use the
ordinary root error envelope. Errors after it use this receipt with error set;
no second JSON error is appended. A final receipt failure may follow a confirmed
mark; stderr states the known mark status and there is no rollback.

## Response loss and lifecycle

Mark only original H with channelRef and expectedIncarnation on both attempts.
A successful fenced response with the expected channel/current incarnation and
lastSeenSeq>=H proves coverage even when cursorIncarnation remains null on a
legacy/no-op mark (including H=0). After response loss, readback requires stored
cursorIncarnation and current channelIncarnation both equal the sampled value,
as well as the same channel id and lastSeenSeq>=H. Otherwise allow one identical
fenced retry, never an observed mismatch or changed binding. If confirmation
still fails, report exit10. A refusal after an earlier lost reply does not prove
the original mark did nothing, so uncertainty remains. No stale cursor relabel,
unguarded compatibility fallback, later-head retry or cursor rollback exists.

Own-session served reads and successful marks may count as check freshness even
if a later command step fails. The API supplies no stamp acknowledgement, so the
command never prints stamped/roster OK; the roster CHECK column is authoritative.

## Validation and follow-through

Author tests cover complete sparse backward draining, output-before-mark,
short/failed writes, sync/close/path failure, permissions, empty range, binding
and incarnation refusals, response loss/readback/single retry and local locking.
Five applied semantic mutants were killed: newest-only, lost middle page,
later-head mark, ignored output failure, and mark-before-output. Author local
HTTP fixtures are synthetic, not real server stamp/cursor proof. Independent
compiled CLI QA plus one private provider campaign remains a review seat.
No production unread-mark experiment, release, host action or nudge edit here.
Elena's nudge change follows compatible CLI merge, release and installation.
Platform stamping/lifecycle rules are implemented, not redefined; exits/artifact
layout are CLI choices accepted by the gate. No spec citation is minted.
