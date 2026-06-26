# Spec memory hints + product inference (#99 item 4)

Two friction points hit while scoping a lint in `hadronmemory.com::specs`.

## 4a — memory-not-found is a dead end

**Problem.** A wrong `-m hadronmemory.com::platform-specs` resolved to
`memory "…" not found`, full stop — no hint that the real memory is `::specs`.

**Design.** `lookupSpecMemory` already has every `myMemories` entry in hand when
it fails to match, so it now appends a suggestion via `memorySuggestion`:

- prefer memories in the **same org** as the failed ref;
- when the ref names a *specs* memory (contains "spec"), prefer the spec
  memories within that org — so `::platform-specs` lands on `::specs`;
- one candidate ⇒ `— did you mean "<urn>"?`; several ⇒ `— available: a, b, …`
  (capped at 8); none ⇒ no tail.

Shared resolver, so every `spec` subcommand that resolves a memory by id/name
benefits. (The `::`-containing fast path in `resolveSpecMemoryURN` still
short-circuits without a lookup — that's deliberate, to keep the common path
round-trip-free — so the hint surfaces on the id-resolving commands.)

## 4b — product-scoping required even with one product

**Problem.** `spec lint -m … --module acl` finds nothing in a product-rooted
corpus because the loc is `<product>:<module>`. The error said "scope with
--product", but with a single declared product the tool can infer it.

**Design.** Only on the `--module`-with-no-`--product` dead end (empty scan),
`discoverProducts` does one loc-only corpus scan and:

- **exactly one product** ⇒ re-scope to `<product>:<module>`, emit
  `note: inferred --product <ppp>` to stderr, and lint;
- **zero products** (flat corpus) ⇒ a plain `no specs found under <module>`
  (no misleading "scope with --product" hint — there are no products);
- **several products** ⇒ usage error listing them: "module … is ambiguous …
  scope with --product".

Scoped to `lint` (the issue's example); the inference scan only runs on the
already-empty path, so the common case keeps its single prefix scan.

## Tests

`TestMemorySuggestion` covers the single-spec-memory hint, the multi-candidate
list, and the empty case. The lint inference paths are exercised end-to-end
against the corpus; `discoverProducts` reuses the paginating `scanAllNodes`.
