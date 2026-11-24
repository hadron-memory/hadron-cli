# `-m` accepts a memory ID on `node get` and the bare-loc edge commands

Unreleased (cli#3000). `hadron node get -m <memory ID> <loc>` was refused ("not a fully-qualified node URN") although `spec get` and `node list` accept the same ID, and although the CLI itself prints that form (the shed recovery prompt, `app nodes list`).

| `-m` value | Before | Now |
|---|---|---|
| `hrn:mem:<root>:<slug>`, `<org>:<slug>`, `<org>::<slug>` | composes the bare loc | unchanged |
| a 32-hex memory ID | refused (exit 2) | the memory is read first (one extra read, once per `node get` batch), then the bare loc is composed against its URN |
| an ID that reads as nothing (missing, foreign or forbidden) | refused (exit 2) | the same refusal, unchanged |
| an ID when the server fails the read | refused (exit 2) | the server fault's own error (for example exit 7 for a gateway error) |

Covers `node get` (one ref and several), `edge add` and `edge list`, and every other command that resolves a bare loc through `-m`. Nothing new is disclosed: the read is the one a URN already passes, and a memory the caller cannot read is refused exactly as before. Commands that read a node batch without `node get` (for example `comment`, `node open`, `node provenance`) still take a memory URN or the short `<org>:<slug>` form with `-m`, not an ID.
