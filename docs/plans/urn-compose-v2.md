# Implementation Plan: compose grammar-v2 flat URNs

> **Status: implemented and verified** on this branch; reflects the design as
> built. Fallout from hadron-server
> [#691](https://github.com/hadron-memory/hadron-server/issues/691) (URN grammar
> v2), child [#699](https://github.com/hadron-memory/hadron-server/issues/699)
> "CLI composes v2". Follows [#261](https://github.com/hadron-memory/hadron-cli/pull/261),
> which adopted `urn-lib-go` but explicitly deferred v2 composition until the
> library exposed it.

## Context

Grammar v2 replaces the v1 `hrn:<type>:<org>::<memory>::<loc>` chain (double
colon, ownership hierarchy) with a flat, pool-rooted form:

```
hrn:<type>:<root>[:<segment>…]      (single colon, fixed per-type arity)

hrn:mem:acme.com:kb                 memory
hrn:node:acme.com:kb:findings:x     node  (<root>:<mem>:<loc…>)
```

Two facts from hadron-server made this safe to ship now:

1. **The server already emits flat v2** (`src/lib/urnEmit.ts`, #697 Stage 2) —
   so users and agents now paste v2 URNs back into the CLI.
2. **Input stays Postel-liberal** — every v1 spelling is accepted *forever*
   (#239), resolving via the urn-lib `mem`→`memory` alias and the stored alias
   map. The server's `resolveMemoryRef` / `resolveUrn` / node `memoryIds` filter
   all accept v1 and v2 alike (`resolvers.query.node.ts`).

So the CLI's emitted URNs are cosmetic to the server (it accepts either), but
aligning the CLI's output with the server's — and, more importantly, making the
CLI **accept** the v2 URNs the server hands out — is the real deliverable.

## What changed

`urn-lib-go` bumped to the v2-capable pseudo-version (`v2.go`: `ComposeUrnV2`,
`ParseUrnV2`, `IsFlatV2`, and the per-entity `ComposeSecretUrnV2` /
`ComposeAppRunUrnV2` / `ComposeNodeRevUrnV2`).

- **`cmdutil/memref.go`** — a single `MemoryParts(ref) (root, slug, ok)`
  decomposer accepts every spelling (bare `org:slug` / `org::slug`, and
  `hrn:memory:` / `urn:memory:` / `hrn:mem:` / `urn:mem:` URNs). `CanonicalMemoryRef`
  now emits `hrn:mem:<root>:<slug>`; `NodeURN` now emits
  `hrn:node:<root>:<slug>:<loc…>`. Both via `ComposeUrnV2`.
- **`cmdutil/noderef.go`** — `ResolveNodeRef` composes a v2 node URN from
  `-m <org::memory>` + a bare loc. A **compound app-mem memory**
  (`<org>::<agent>:app-mem:<slug>`) can't be a fixed-arity flat node URN, so it
  falls back to the legacy `<memory>::<loc>` join the server still resolves —
  preserving `-m <compound-memory> <bare-loc>` (PR #266 review).
- **`cmd/chat/chat.go`** — `splitNodeURN` accepts a scheme-prefixed v2 `--node`
  (the memory canonicalizes to the flat `hrn:mem:` form); a bare ref still
  requires the two `::` separators to stay unambiguous.
- **Input acceptance is unchanged for v1** everywhere. `SplitNodeUrn` and
  `AssertFullyQualifiedUrn` in urn-lib-go already accept flat v2 node URNs, so
  `ResolveNodeURN` / `CanonicalNodeURN` pass a pasted v2 URN straight through.

## Deliberately kept on v1: the `spec` surface

The `spec` command group keeps composing v1 internally (`memoryRefV1` in
`cmd/spec/spec.go`), for two reasons:

1. Its loc-as-citation plumbing (`isFullyQualifiedMemoryURN`, node-ref FQN
   building, the `nodes` filter) is built on the `::` shape and is
   heavily tested; rewriting it to v2 is churn with no functional gain (server
   accepts v1 forever).
2. **Compound app-mem memories** (`<org>::<agent>:app-mem:<slug>`) can't
   round-trip through a fixed-arity flat node URN — the exact caveat called out
   in the server's `urnEmit.ts`. Keeping spec on v1 preserves their resolution.

`memoryRefV1` still *accepts* the flat v2 `hrn:mem:` form the server emits (via
`cmdutil.MemoryParts`) — it only normalizes back to the v1 `::` shape for spec's
internal use.

## Not in scope

- **Secret URN shape / `--scope` narrowing (#696).** Secrets become
  `hrn:secret:<root>:<name>`, org/user owners only. The CLI doesn't compose
  secret URNs (the server does), and dropping `--scope app|memory` is gated on
  the server-side migration; tracked separately.
- The server-side sequencing pieces (#692 principal pool, #697 alias map, #700
  `@` reservation) are hadron-server work.

## Testing

`cmdutil`: `MemoryParts`, `CanonicalMemoryRef`, `NodeURN` (v2 output),
`CanonicalNodeURN` (accepts v1 + v2). `cmd`: node/edge/memory/move `-m` paths
assert v2 output; a new `TestChatReadAcceptsV2NodeURN` proves a pasted
server-emitted v2 `--node` resolves. Full `go test ./...` green; spec suite
unchanged (v1 behavior preserved).
