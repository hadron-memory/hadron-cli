# One-page loc segment browsing (#1744)

Implements cor:api:090:05's existing read on the CLI, per Xabat T1 gate6698146146 and Xanthos's frozen FIT on #1744 (2026-11-07). No new server rule.

`memory loc-segments` explicitly names repeatable --memory values (StringArray, no -m). They and parent go unchanged; root is empty parent. No ambient/default widening, memory resolution, existence probe, local sorting/deduplication, recursion or auto-drain. One invocation makes one query. Optional positive limit and nonempty after are omitted unless supplied; server default20/cap200 remain server behavior. Same-name segments from different/inherited memories retain source ids and server order.

Public DTO: items[{segment,hasNode,memoryId}], hasMore, nullable endCursor. Empty items stays[]. Text table MEMORY ID / SEGMENT / HAS NODE uses SafeCell; only text hasMore prints the exact continuation footer on stderr. JSON preserves raw strings. Missing/null page/items, null/empty-identity item, more-without-continuation, and any cursor changed by SafeCell refuse before output without fallback. An endCursor with hasMore false is legal and preserved. Access-empty, unresolvable and ordinary empty answers are identical.

Genqlient observations: page and items are pointers; endCursor is *string; hasNode/hasMore are plain bool. Null (or missing) hasNode decodes as false and cannot be distinguished here; pinned per FIT, no raw decoder. No claim to reject malformed booleans beyond typed decoding. Server-skew uses existing api.MapError, no findNodes fallback.

The original regression commit precedes command/query implementation: the command is absent on main, so it fails before requests. Follow-up fixtures validate typed behavior at the implementation. Focused race, mutation and codegen evidence is attached on the PR; whole local suite is left to hosted CI under host rules. QA is compiled fake provider plus one fresh private stack, four FIT probes; no write-between-pages snapshot claim. Vera's Built-line update follows merge, no mint/new law.

Schema-check found drift against chosen server main cd0da474f51b8742754ed6456db9987b86c7fc77, so FIT-authorized refresh used that revision (direct tsx exporter; pnpm wrapper failed before export). Refreshed SDL adds one existing serve-refusal enum value and current descriptions; no hand edits. Seven precise guard mutants killed, one equivalent survived; exact match/read-back and test FAIL lines recorded, source restored. No claim that every guard branch has a mutation pin.
