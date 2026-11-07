# Inherited node list provenance (#1708)

Consumes existing cor:dmo:050:14 and CHECKED6677941348. Fresh-main source check found both FindNodes documents omit urn/inheritedFrom, adapter drops provenance, and list text/JSON cannot identify inherited nodes. No existing implementation PR. Server marking/authorization already correct; no server or mutation changes.

Both plain and comment-summary list documents select canonical urn and inheritedFrom{agentId,agentUrn}. The plain-to-summary alias adapter explicitly preserves both. node list fills its existing shared URN slot and adds a list-only inheritedFrom DTO (omitted for own/concealed rows); no shared detail/search DTO expansion. Inherited rows with incomplete provenance refuse before rendering rather than invent an address. No base identity is inferred from memoryId.

Text: URN / INHERITED FROM columns appear only if an inherited row is displayed, leaving own-only table layout unchanged. Projected data/properties blocks add these two metadata lines only for inherited rows. Server strings use SafeCell in the new text surfaces; JSON keeps raw values. Sequence filtering/sorting, paging, properties/data and comment-summary paths preserve metadata.

The source address is not a new read/write grant. Listing through a child still uses server concealment; direct base reads still require base access. No address composition, existence lookup, fallback read or mutation. A child write still creates an override, unchanged server behavior. CLI QA per Xabat 6702757368: compiled shape cases plus one bounded live inherited/own/child-override observation; security is scoped delta-only per stamp.

Shared FindNodes projection gains a short URN on all rows and a nullable base pair on inherited rows; other consumers keep their existing output mappings. Request count unchanged: one query per existing page, no N+1 reads. Extra response size scales with URN and base-identity lengths; QA records request/byte/time evidence, no latency guarantee.

Red first 12e016da plus corrected-fixture 70a16815 precede implementation. Original red has one invalid comment-summary flag (setup issue); corrected commit uses the actual --has-unresolved-comments=false and is verified red for missing query selections/address across the proper paths. No failed setup counted as regression proof. No schema refresh needed: committed SDL has both fields; generated code is regenerated, not hand-edited.
