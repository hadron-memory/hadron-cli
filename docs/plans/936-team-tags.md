# Team tag snapshots and read filters (#936)

This CLI consumes the existing server contract; it changes no tag derivation,
metadata admission, routing, activation or bare-@team policy. The CHECKED gate is
issue comment 6627675207, with compatibility clarification 74081. The provider
shape was inspected at server `d984e5c3`; the committed schema already contains
it. This is source evidence, not an earliest-version or deployed-provider claim.

## Output and identity

The team-chat and channel fragments select `teamTags { headId label }`. Local
command DTOs add the `teamTags` key, including on post results sharing those
fragments. Unlike ordinary list DTOs, this field deliberately preserves `null`
(withheld/historical), `[]` (known untagged), and populated snapshots. It never
uses labels as identity or derives IDs. JSON retains the raw strings; transcript
text displays each label through SafeCell, which neutralizes controls and caps
each server label at 256 runes. There is no aggregate tag-count bound here.
Text adds no marker for null or empty lists; UX reviews that choice.

## Presence and request paths

Both reads accept one of `--team-head <id>`, `--untagged`, and `--tagged`.
The head is sent verbatim, without resolution or normalization. Blank heads,
explicit false booleans, conflicting filters, and thread+tag filters return
usage before client construction or any HTTP. A tagged read sends the present
Boolean `untagged:false`; absence omits both new variables.

Separate TeamChatTags / TeamChatTagsAttributed / ChannelTags operations keep the
legacy read argument and variable shapes unchanged. The additive shared field
selection intentionally changes their documents (Xylon 74081); an older schema
without the selected field returns the existing schema-skew error. There is no
retry dropping the selection or filter. The existing advanceReadState fallback
may retry an attributed team page headerless, but retains every filter and
explicit false value. A null page is a failure through the existing read path,
not a synthesized empty answer.

There is one filtered page operation per ordinary page, with the existing
team binding/identity/head diagnostics unchanged. Pagination adds one page
request per page, including its terminal empty page. Wait repeats the same
filtered read under the existing bounded wait policy. The compatibility retry
adds at most the existing one suppression fallback per attribution state.
No head lookup, roster scan or client-side filtering is introduced. Payload
increase is proportional to returned snapshots (two strings per stored tag),
not to the team roster; server page sizes and existing body projections remain.

## Read evidence and refusals

Every tag-filtered path (single page, --all, --wait, mentions composition and
suppression fallback) is excluded from the binding watermark and mark-read
condition. `nextSince` only resumes the returned window. An unfiltered positive
twin still advances its watermark. Head probes remain unfiltered diagnostics;
they cannot convert a filtered page into evidence of a complete read.

TEAM_CHAT_TEAM_HEAD_INVALID maps uniformly to exit 4 for reads and posts;
TEAM_CHAT_TEAM_METADATA_FORBIDDEN maps to 8; BAD_USER_INPUT remains 2. Filtered
reads preserve the typed server code through the root renderer. Posts retain
the two new codes through its existing bounded chat-code envelope. No refusal
is converted to empty history or retried as a wider request.

## Verification and scope

The first commit contains failing command pins before implementation. The fake
HTTP tests inspect actual documents, presence-aware variables, headers, final
JSON/text and rendered error envelopes. They distinguish all three tag states,
raw head IDs, zero-request conflicts, both boolean values, unfiltered twins,
pagination/polling, unknown field/argument and suppression fallback. Mutants
exercise the watermark exclusion, null preservation, false presence, refusal
classification and fallback filter retention. Focused race verification covers
the recording fake. Independent compiled-binary QA remains a separate seat.

Codegen is regenerated from the committed snapshot, without a schema refresh.
Coverage generation removes four reached argument gaps (92 to 88 argument rows,
64 to 60 gaps); its argument section lists only unused arguments, so no new
matrix section or generator behavior is added. Help and agentic usage describe
server-owned filtering and deployment stamping, retaining the existing truthful
push-to-no-one wording. Platform specs are unchanged: this is the gated consumer
of existing server behavior, with Proposed contract dependencies accepted by the
gate, rather than new platform law.
