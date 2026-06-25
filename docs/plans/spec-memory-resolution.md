# Unified `spec` memory resolution + loud edge failures (#91)

Two related `hadron spec` bugs, both rooted in how `-m/--memory` was turned into
the values the server needs:

- **Bug 1 — `spec describe` couldn't resolve a memory by any form.** `describe`
  matched the `-m` value (scheme-stripped, `::`→`:`) against `myMemories[].urn`,
  but the server reports that urn as `hrn:memory:<org>::<memory>` — so URN, bare
  `org::memory`, PK, and name all missed and reported "not found". (The unit
  test fixture used an unrealistic single-colon, no-scheme urn, which is why
  this passed tests but failed in production.) Meanwhile `register` accepted a
  PK (it flows straight to the nodes filter) and `new` accepted a URN —
  resolution rules differed per subcommand.

- **Bug 2 — `spec new -m <PK>` built malformed edge targets.** `memoryURNFromFlag`
  only stripped a scheme prefix; a PK passed through unchanged, so
  `specNodeRef(memURN, loc)` produced `<pk>::<loc>` instead of
  `<org>::<memory>::<loc>`. Both auto-wired edges (table-of-contents → parent,
  inheritance → contract) failed FQN validation and were skipped — yet the
  command still printed `✓ created`, leaving a silently orphaned node.

## Fix

**One shared resolver** (`spec.go`), used by every spec subcommand:

- `resolveSpecMemoryURN(cmd, client, ref) → <org>::<memory>` — the canonical
  form node-ref FQNs and the nodes filter need. A ref already in
  `<org>::<memory>` / `hrn:`/`urn:` shape is normalized **without a round-trip**
  (the fast path — so the common `-m org::memory` invocation costs nothing and
  existing tests need no new stubs); a PK or name is resolved via `myMemories`.
- `resolveSpecMemoryID(cmd, client, ref) → (id, <org>::<memory>)` — for
  `describe`/`lint`, which call `Query.memory`/`updateMemory` (PK-only). Always
  consults `myMemories`.
- `lookupSpecMemory` matches a ref against `myMemories` by **PK, urn (any
  scheme/colon form, via `collapseColons` + `canonicalMemoryURN`), or name**.
  A readable-but-unsubscribed memory passed as a bare `<org>::<memory>` still
  works for scan/read paths because `resolveSpecMemoryURN` fast-paths that shape
  without a lookup at all.

Every `-m` consumer (`new`, `describe`, `lint`, `register`, `ls`, `find`, `get`,
`edit`, `supersede`, `extract`, `link`) now routes through these, so a PK, URN,
bare `org::memory`, or name resolves the same everywhere (#91 ask 1). `new`
derives edge-target FQNs from the **resolved** `org::memory` (ask 2). The
not-yet-implemented `import` stubs keep the pure `memoryURNFromFlag` normalizer.

**Loud edge failures (`new.go`, ask 3).** Both `spec new` and `spec new
--new-path` collect any skipped/failed ToC/inheritance edge and, after creating
the node(s), return a non-zero `exitcode.Error` naming the orphaned target(s) —
instead of printing `✓ created`. The per-edge warnings still print to stderr for
detail. This is safe against the resolveUrn-lag caveat: `new`'s edge targets are
either pre-existing nodes (parent/inherit, which `planTarget` requires to exist)
or nodes created earlier this run (wired by id), so a skip is a real failure,
not a lag.

## Tests

- `canonicalMemoryURN` unit test: scheme-prefixed, single-colon, double-colon
  all canonicalize; a PK passes through.
- The `myMemories` fixtures now use the **real** `hrn:memory:<org>::<memory>`
  urn, so the resolver tests actually guard the production bug.
- `spec describe -m <PK>` resolves (Bug 1); `spec new -m <PK>` builds edge
  targets from the resolved `org::memory`, never `<pk>::<loc>` (Bug 2); a
  skipped required edge makes `spec new` exit non-zero (ask 3).
