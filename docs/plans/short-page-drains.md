# Short-page drains (cli#967, slice B)

Companion of hadron-server#2211/#2214 (cor:api:120:04: a drain stops only on an empty page or at total, never on the first short page).

## As built
Every client-side scan of a findNodes-shaped listing now goes through one rule: `api.PageNodes` (internal/api/nodepages.go). It advances by the rows actually received, visits each node id once, and stops on an **empty page**. A **non-empty page that brought nothing new** (a server ignoring the offset) is a **failed read** (`api.ListNotAdvancing`, exit 7), never a successful partial list; the same rule holds in the three inline scans (`chat read`, the placeholder scan, `spec find`). `findNodes.total` is nullable (live: NULL), so it is not relied on. A fetch error on any page is returned, never read as the end.

Sites moved to it: `memory export` (`listAllNodeRefs`), `spec` `paginateNodes` (lint, list, register, describe, the number allocator), `skill` `listDeclaredIDs`, `coding` `scanPrefix`, `node import` and `import_tree` overwrite probes (`findExactLoc`, which now RETURNS a fetch error — a failed later page fails the import and the tree `--on-conflict skip`, distinct from true absence, instead of reading as "not found"). Same rule, inline: `chat read` (`CollectMessages`), the draft placeholder scan (`spec/corpus.go`) and `spec find` (`collectSpecFindResults`, still bounded by its result limit).

Cost: one extra empty-page request per scan. Page sizes (200/500) are unchanged and exempt.

## Not in this slice
Slice A (command defaults and listing commands, PR #969), the host poller (cli#968), `memory validate`, #2212 continuation.

## Tests
Each site is pinned against a server that serves fewer rows than asked for (clamped pages): `TestPageNodes*`, `TestPaginateNodesPastAClampedPage`, `TestScanPrefixPastAClampedPage`, `TestListAllNodeRefsPastAClampedPage`, `TestListDeclaredIDsPastAClampedPage`, `TestFindExactLoc*`, `TestNodeImportDryRunFailsWhenTheOverwriteProbeFailsOnALaterPage`, `TestNodeImportTreeSkipFailsWhenTheExistingNodeScanFails`, the `*FailsWhenThePageDoesNotAdvance` tests, `TestChatReadDrainsPastAClampedPage`, `TestSpecDescribePlaceholderScanPastAClampedPage`, `TestCollectSpecFindResultsPastAClampedPage`. Existing tests that encoded "a short page ends the scan" were updated to expect the empty probe. Production mutants (restore the short-page stop at each site) fail the suite.

Static test fixtures: `pageBeyondFirst`/`translateFindNodesAt` (internal/cmd/root_test.go) answer any request past offset 0 with an empty page, so a single-page fixture models a real server instead of a provider that ignores the offset.
