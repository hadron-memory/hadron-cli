# `coding preflight route --dry-run --json`: `alreadyRouted` is now reported

Unreleased (cli#3002, follow-up to cli#2915). One additive change to the `--json` contract of
`hadron coding preflight route`.

| Field or behaviour | Before | Now |
|---|---|---|
| `alreadyRouted` on a `--dry-run` | absent | `true` when every edge the run would write already exists and none needs the `--cross-memory` marker, the same judgement the real run makes; absent otherwise |

The key is additive and only ever present as `true`, so a caller that ignores unknown keys is
unaffected. A rehearsal never writes: its text output also reads `already routed (nothing to write)`
instead of `would route`. The `--help` text of `preflight route` is reflowed to 80 columns and its
lint sentence now names a marked route; no behaviour changed there.
