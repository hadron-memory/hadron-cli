# `spec lint`: Rule specs need a Why, limiting sentences need a reason

Unreleased (cli#3127; server half hadron-server#5109, merged at e07173d7).
`hadron spec lint` now relays two content checks the server computes
(`Node.specLint`). The CLI carries no copy of them; its own checks (abstract
length, wiring, links, frame) are unchanged, so one report mixes both sources.

| Finding rule | Severity | Meaning |
|---|---|---|
| `rule-why-missing` / `rule-why-empty` | error (exit 5) | a Rule spec has no `## Why We Need This Rule` section, or an empty one |
| `limit-without-reason` | warning | a sentence restricts something (excluded, never, refused, only, must not, hidden...) with no reason in its paragraph and no link to a ruling or Why section |
| `spec-lint-unavailable` | info | the server has no `Node.specLint`: the two checks did NOT run |
| `spec-lint-not-read` | info | the server did not load the text of the listed specs |

- **`--json`** findings from the server also carry `line` and `sentence`
  (omitted on every other finding, so existing shapes are unchanged).
- **Warnings never fail a run** (the sentence check gave 4,444 warnings over 603 of
  704 live specs at its first scan); `--strict` promotes them to errors as before.
- **Older servers:** no failure; the info finding says the checks were skipped.
- **Merge order:** this build pins the server at e07173d7; it merges after the
  deploy-verified announce for that revision (0274).
