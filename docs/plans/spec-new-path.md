# `spec new --new-path` — one-shot citation-chain scaffold (#69 item 1, tail)

The last #69 friction: standing up a deep spec still takes one `spec new` call
per tier. `#77` made each tier scaffold its own shape and `#78` had each root
auto-create its contract, but you still walk `module → feature → rule` by hand.
`--new-path` collapses that into a single call.

## Behavior

```sh
hadron spec new cli:cha:010:01 --new-path -m <org>::specs --title "…"
```

Given a **full citation** (positional), create that node and **every missing
ancestor** along the chain, top-down, each with its tier-aware template — and,
for every root tier it creates (product/module/feature), that tier's
general-provisions contract (reusing `#78`'s auto-contract; `--no-contract`
opts out). Ancestors that already exist are left untouched.

- The **deepest** node is the target: it takes `--title` / `--content` /
  `--abstract` / `--tag`. Created **ancestors** get a placeholder title (their
  segment code) and the tier template — the author renames them later (the
  placeholder abstract already forces a revisit per tier).
- Product-vs-flat is inferred from the citation (segment 2 alpha ⇒
  product-rooted), so no `--product` flag is needed. `--new-path` is mutually
  exclusive with the tier-selection flags (`--module`/`--feature`/`--rule`/
  `--flow`/`--new-*`/`--contract`).
- Errors if the target already exists (use the normal per-tier commands to add
  under it).

## Implementation

- `spec new` grows an optional positional citation; `Args` becomes
  `MaximumNArgs(1)`, required when `--new-path`.
- `planChain(target, existing)` returns the ordered (top-down) set of citations
  to create — the target plus any missing ancestors.
- One creation loop reuses the `#78` machinery: for each node create it (tier
  template; target uses the user's body/abstract), and if it `ChildContract()`s
  and `!--no-contract`, create that contract too. A `created map[loc]id` lets
  every edge (ToC to parent, inheritance to the tier contract, contract→root)
  resolve **by id** when the endpoint was just made this run — `resolveUrn` lags
  a fresh node ~a minute — falling back to a loc resolve for pre-existing
  ancestors. Edge failures warn, never abort (same as today).
- `--json` reports the target with the whole chain under `also[]` (the field
  `#78` added); `--dry-run` previews every node.

## Tests

A `--new-path` that creates module+feature+rule (+ their contracts) from a bare
product asserts the full set of upserts and that fresh-node edges wire by id;
`--no-contract` suppresses the contracts; an already-existing target errors;
`--new-path` + a tier flag is a usage error. Smoke against a scratch citation
(dry-run) end-to-end.
