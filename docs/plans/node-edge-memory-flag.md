# `-m <memory>` addressing parity for node/edge (#69 item 3)

`spec` addresses a node as `-m <memory> <bare-citation>`, while `node` and
`edge` require a fully-qualified `<org>:<memory>:<loc>` URN — so muscle memory
from `spec`/`node add`/`node ls` (which all take `-m`) hits
`unknown shorthand flag: 'm'` on `node get`, and the URN is easy to
mis-assemble. This adds the `-m` form to the node-addressing commands.

## Design — additive, not a replacement

A memory is `org:memory` and a node URN is `<org>:<memory>:<loc>`, so a memory
+ bare loc is just `memory + ":" + loc`. New shared helper:

```go
// cmdutil.ResolveNodeRef resolves a node reference that is either a
// fully-qualified URN (memory == "") or a bare loc within --memory.
func ResolveNodeRef(cmd, client, memory, ref string) (string, error)
```

When `--memory` is set the positional/flag is a **bare loc**, joined onto the
memory and resolved; when it's empty the existing `ResolveNodeURN` path runs
unchanged. **The strict-URN behavior is the default** — `-m` is an opt-in
convenience, so the "node refs are always fully-qualified URNs; bare locs
rejected" contract still holds whenever `-m` is absent. (A bare loc with **no**
`-m` is still rejected with the same usage error.)

Wired into every node-addressing command: `node get` / `update` / `rm` (via
`fetchNode`), `node export`, `edge add` (`--from`/`--to`, same memory), and
`edge ls`. `edge update` / `edge rm` address an edge by id and are untouched.

```sh
hadron node get   -m acme.com:kb findings:flaky-ci      # ≡ node get acme.com:kb:findings:flaky-ci
hadron node update -m acme.com:kb findings:flaky-ci --name "…"
hadron edge add   -m acme.com:kb --from findings:flaky-ci --to start-here --label routes-to
```

## Tests / docs

Command tests assert that `-m <memory> <loc>` resolves the same URN the full
form does, that a bare loc **without** `-m` still errors, and that `edge add -m`
applies to both endpoints. `agentic-usage` and the node/edge addressing rules
(and the CLAUDE.md note) are updated to document the additive `-m` form.
Cross-memory `edge add` still uses full URNs (omit `-m`).
