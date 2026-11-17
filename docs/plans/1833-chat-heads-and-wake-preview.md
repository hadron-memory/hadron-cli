# Three argument bindings (#1833)

The CHECKED gate (6752254635), following the FIT (6752185195), requires all
three person-facing capabilities. No exclusion or new product choice is made.
Monitoring already sends preview through DueTickWakes (#1984); this change
adds the public command while leaving that monitoring path alone.

## Chat heads

The shared ChatTagFlags binds --team-head as StringArray: repeated values are
kept verbatim and in order, including commas. One occurrence keeps the old
TeamChatTags/ChannelTags document and singular teamHeadRef. Multiple values
select distinct plural documents and send teamHeadRefs without teamHeadRef.
There is no local resolver, deduplication or 20-entry cap; the server owns
those rules. Every blank member and conflict with tagged/untagged/thread is
refused locally before HTTP, as in the existing single-head contract.

Both attributed and headerless team-chat paths retain all heads, including
the existing unknown-advanceReadState compatibility fallback. Failure of the
plural argument itself never falls back to a broader transcript.
Channel reads use the same flags. Team chat check has no head-filter option
and remains unchanged. Filtered read-state/watermark behavior is unchanged.

## Public wake preview

--preview selects TeamDueWakesPreview, whose document always sends
preview:true. Ordinary TeamDueWakes never mentions that new argument.
Both read as the person without a worker session. The server owns no-reserve,
no-withhold/no-notice semantics and the absence of a stop-lane declaration,
including when --accept-stop is also passed. No acknowledgment is sent here.

An explicit preview labels text and omits the proof line. Its package-local
JSON DTO adds preview:true and keeps each proof empty, even if a skewed
response contains a nonempty proof. Ordinary JSON omits the new key and
retains its proof strings. Both operations share the selected fields through
a fragment; schema, existing selection and default behavior are unchanged.
An older server refusing preview gets a normal usage refusal, never a
spending retry.

## Evidence and limits

Red commit 97deef3d: four plural wire cases retain only the last head; both
preview cases fail unknown flag. Single-head and default-wake controls pass.
The first draft of the test needed a pointer-dereference compile correction;
only the later compiling behavioral failures count as red evidence.

Focused tests inspect actual HTTP documents and variables, rendered JSON/text,
blank/conflict zero-request guards, a 21-head server refusal, older-server
unknown-argument refusals without widening, and preview-proof suppression.
All runtime evidence is synthetic loopback. No production data, wake-budget
reservation or activation is exercised; independent compiled CLI QA is required.
The preview spec clause remains Proposed; this implements the shipped server
and does not change its legal status.
