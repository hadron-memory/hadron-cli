# Recorded-sequence diagnostics — cli#1675 Part 1

Built against main126d0ace. CHECKED6686679478 / FIT6686619690;
platform-opener clarification85292. Part 2 A/B/C remains held with Holger.
No resolve command, repair procedure, manual certification or new authority.

`ReportOutcome` adds optional reason/field/diagnosticError. Only the existing
recorded accepted-seq failures receive a reason: mismatch (author/body/both),
not-found, unavailable, invalid accepted record. Matching reads still reconcile.
A structured error carries classification, not remote prose. Read-back server
errors render a fixed unavailable line so they cannot echo a body/author id;
legacy mismatch/needs-a-person substrings remain: they live in
`ReportOutcome.error`, which preserves the old mismatch text, while the human
CLI line deliberately renders the new gated wording. Failed-record exit stays 7.

After failure, under the existing lock, write the six-key v1 `.lastfail`
observation via the existing synced-temp/rename/directory-sync writer. Only
existing 32-lowercase-hex bases qualify; max encoded size 1KiB. Write errors
add diagnosticError only. Run never reads this cache. Successful durable
`.reported` precedes cache removal; removal failure cannot undo that marker.
Recorded-seq failure paths preserve `.sending` bytes. First-send persistence,
posting/reconciliation eligibility and all delivery-file formats are unchanged.

PendingStatus reads local files only, without lock/write/API. Pending preserves
its old count API. Cache admission requires exact six-key schema, v1, valid
reason/field/time and matching event/acceptedSeq. Missing, unreadable and stale
observations never enter needsPerson/records; pending counts remain unchanged.
Unavailability observations are not needsPerson. `.reported` always wins.
Records sort heldAt then eventId, cap50, full count and truncation flag. JSON
uses one Encoder.Encode write. Human cached rows use SafeLine for record text
and the lastObservedAt cached time, with an advisory footer when rows exist.
Unrelated count-only human output remains byte-identical to the old command.

No-follow is local to `.lastfail`: Unix O_NOFOLLOW|O_NONBLOCK; Windows opens
reparse points themselves and rejects them by handle attributes. Lstat and
opened regular-file identity must match before a bounded read. Renaming a
status file replaces a symlink, not its target. No global filesystem helper
changed. Outbox-directory trust/other existing record readers are not widened
or represented as repaired. Windows/Darwin cross-builds are compilation only;
only Linux runtime has been exercised by the author.

Evidence: red8e571b3f fails six structured cases on main for missing reason,
not compilation. Synthetic temporary outboxes and fake chat only. Tests cover
privacy, advisory failures/ordering, accepted-state byte identity/no repost,
stale/corrupt/oversize/nonregular/schema variants, swapped symlink/regular/FIFO,
50/51 bounds/order, cached timestamp, safe human text and no API/lock in dryrun.
No real host/outbox/chat/config changes or live persistent-mismatch claim.

Specs cor:agt:090:04 and cor:agt:020:04 remain unchanged: no delivery count
or authorship asserted. Optional future recovery waits for a separate ruling.
