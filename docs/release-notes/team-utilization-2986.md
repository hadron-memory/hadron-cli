# `team utilization`: an empty last-match-failure reason is named

Unreleased (cli#2986, a follow-up to cli#2896 and cli#2964, which shipped in v0.58.0). Table output only;
the `--json` contract is unchanged.

| Reason served for `lastMatchFailure` | LAST MATCH FAILURE column before | Now |
|---|---|---|
| null (nothing recorded) | the time alone | the time alone (unchanged) |
| `""`, or only blanks | the time alone | the time and `(empty)` |
| any other text | the time and the text | the time and the text (unchanged) |

`—` still means no failure at all. `--json` carries the reason exactly as served: an empty string stays
an empty string, null stays null, and `(empty)` never appears in it. A reason the server serves as the
literal text `(empty)` is shown as served, so the word alone does not prove the server sent nothing: read
`--json` when it matters.
