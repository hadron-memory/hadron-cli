# `coding preflight route --dry-run`: `alreadyRouted` and the text line

Shipped in v0.58.0 (cli#2915, PR #2998, merge `63e12bac`); this note records it for callers, as cli#3002
asked. It is additive: no behaviour changed in cli#3002 itself, which only reflowed the `preflight route`
help and added tests.

## `--json`

| Field | Before | Now |
|---|---|---|
| `alreadyRouted` on a `--dry-run` | absent | `true` when every edge the run would write already exists and none needs the `--cross-memory` marker, the same judgement the real run makes; otherwise the key is omitted (never `false`) |

A caller that ignores unknown keys is unaffected.

## Text

A dry run that is already routed reads `already routed (nothing to write)`, or
`already routed (edges); would add the body line` when only the routing line is missing, instead of
`would route`. A dry run that is not already routed is unchanged.
