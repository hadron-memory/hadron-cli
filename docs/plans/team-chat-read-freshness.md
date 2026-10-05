# Team-chat read discrepancy detection (#801)

Dan's `--since 5999 --limit 30` reads returned empty for two hours while
messages existed. Cody's tail omitted his own #6242 while a direct GraphQL
probe returned it. This change exposes disagreements; diagnosing the stale
layer remains the server lane of #801.

## Read sequence and output

1. A distinct chat-only `TeamChatReadHead` operation samples the latest
   surviving message using an unfiltered backward `limit: 1` query. Only a
   parsed old-schema validation refusal naming `beforeSeq` degrades to an
   explicit unavailable comparison (`head: null`). Other probe failures fail
   the command.
2. Separate best-effort `TeamChatReadMetadata` samples the default Channel ID
   and allocator `lastSeq`. Its stricter App access gate never denies readable
   chat: unavailable metadata leaves allocator/cursor null with a note.
   For a binding on this deployment and canonical App ID, `ChannelReadState`
   samples the bound Worker's default-Channel server cursor. This follows the
   existing acknowledgement channel; it does not establish which Channel a
   future server routing change might serve. Missing state means zero; a failed
   lookup leaves the cursor null with a note.
3. The requested page(s) are read without attaching a session. Empty/short
   unfiltered forward pages and newest pages must reach a surviving message
   already observed by the baseline. A `--before` bound above that head is also
   a newest-page request when the head is also greater than `--since`. Filtered pages and genuine backward/bounded windows
   have no such obligation; full forward pages may legitimately have more ahead.
4. Output retains `messages`, `nextSince`, and `prevBefore`. It adds `readState`
   with `head`, `allocatedHead`, `readCursor`, and `suspectedStale`. Text prints
   the sampled cursor and both head values, explicitly before this read.
5. Suspected stale pages remain visible but exit 5 with a retry-from-original-
   cursor remedy. Neither the local binding watermark nor server read state is
   advanced. Otherwise existing post-delivery contiguity, scope, and render-
   success guards still govern acknowledgement.

No `total` arithmetic is used: cursor-scoped counts and sequence gaps cannot
prove missing messages. `allocatedHead` is informational because deleted or
rolled-back messages can leave allocator gaps. No GraphQL schema change is
needed. This is CLI diagnostic/output behavior, not a new platform access,
read-state, or lifecycle rule; no platform spec citation is proposed.

## Limits

Two requests can both be stale and agree, including when a reused connection
or shared edge/backend pool sends both to the same stale source. No nonce or
transport workaround is asserted to solve the still-undetermined origin. A message deleted between the baseline
and page can produce a suspected discrepancy; retry is the remedy, and the
wording does not claim a diagnosed stale origin. Posts after the baseline do
not make a fresh response look stale. The diagnostic only compares the head;
it cannot prove every intermediate message was included. An unsuccessful cursor
lookup does not prevent delivery or existing acknowledgement behavior.

Every read adds one bounded chat-head probe and one optional metadata probe;
an applicable bound Worker adds one cursor lookup. These diagnostic requests carry no session header and never
mark read. The existing explicit post-delivery mark remains the acknowledgement
path.

## Validation

Command tests cover the exact Dan/Cody seq and argv shapes, empty/short pages,
full forward pages, seq gaps, newest pages (including the portable --before
idiom), filters, bounded/backward windows, post-baseline arrivals, probe failure,
allocator gaps, and populated/unknown cursors. Stale cases assert exit 5, JSON
suspectedStale, no server mark, and an unchanged local binding. Regressions for
existing read-state guards remain in the full suite.

Removing forward detection kills the Dan incident test. Removing tail detection
kills the Cody incident test. A read-only live-server smoke read shows real
head/allocator values and the inherited Worker cursor; it is not a reproduction
of the transient production incident. Author checks: full Go suite, race-enabled
chat-read tests, build, generated-client freshness, unbound-operation baseline,
and golangci-lint.

Review regressions cover readable App-key/cross-org chat with forbidden metadata,
unknown allocator values, wrong-App cursor guards, nil head/page responses, and
old-schema degradation without masking unrelated schema or authorization failures.
`--limit 0` is refused before any API call; the conditional review finding is
unreachable through this command. Race tests check handler safety, not freshness.
