# Team-chat read discrepancy detection (#801)

Dan's `--since 5999 --limit 30` reads returned empty for two hours while
messages existed. Cody's tail omitted his own #6242 while a direct GraphQL
probe returned it. This change exposes disagreements; diagnosing the stale
layer remains the server lane of #801.

## Read sequence and output

1. A distinct `TeamChatReadHead` operation samples the default Channel ID,
   allocator `lastSeq`, and the latest surviving message with an unfiltered
   backward `limit: 1` query. The explicit upper bound works on servers predating
   cursorless newest-page defaults. A failed probe fails the command.
2. For a binding on this deployment and canonical App ID, `ChannelReadState`
   samples the bound Worker's server cursor. A missing row means cursor zero;
   a failed lookup prints a note and leaves the cursor unknown. Another App,
   another deployment, or no Worker binding leaves it inapplicable/null.
3. The requested page(s) are read without attaching a session. Empty/short
   unfiltered forward pages and newest pages must reach a surviving message
   already observed by the baseline. A `--before` bound above that head is also
   a newest-page request. Filtered pages and genuine backward/bounded windows
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

Two requests can both be stale and agree. A message deleted between the baseline
and page can produce a suspected discrepancy; retry is the remedy, and the
wording does not claim a diagnosed stale origin. Posts after the baseline do
not make a fresh response look stale. The diagnostic only compares the head;
it cannot prove every intermediate message was included. An unsuccessful cursor
lookup does not prevent delivery or existing acknowledgement behavior.

Every read adds one bounded head probe and, for an applicable bound Worker,
one cursor lookup. These diagnostic requests carry no session header and never
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
