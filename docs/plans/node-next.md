# Node next (cli#1009, Refs #102)

A stateless wrapper of existing `Query.nextNode`, not a session or action
runtime. Parent/current refs normalize via `BatchNodeRef` (PK, qualified URN,
legacy compound or bare loc with memory), without a `resolveUrn` prelookup.
This preserves the provider's deliberately identical null for end, missing,
denied and a current ref outside the parent's direct children.

Omitted `--after` omits currentRef. An explicit empty after/memory is Usage2.
`raw` is sent as the actual bool (default false). One nextNode request selects
the same projection and generated typename as GetNode; genqlient rejects any
projection divergence. It reuses the explicit ordinary node DTO/renderer,
sets rendered from raw, keeps arrays non-null and leaves separately fetched
advisory revision/stamp/comment/signature fields null. It does not fabricate
revision identity or a consistent advisory snapshot.

A null result prints end of sibling chain; JSON is null; exit0. Errors retain
MapError classification and full provider messages. No retry, lookup fallback,
traversal state or provider-policy reimplementation.

CHECKED T1 Xavier6358376747, Patrick attach6358307674, Ada31774. Tests first
commit b4cb34c fail against main because node next is not wired. Schema already
contains nextNode; no sibling export or SDL change is needed. Separate render
work is #1010; umbrella #102 remains open for closure audit.
