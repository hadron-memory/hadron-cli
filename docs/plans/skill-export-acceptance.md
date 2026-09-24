# `hadron skill export` acceptance tests, and the matrix's `writer` section (#621)

The writer (#692, #694, #696, #697) shipped with in-package tests of its own
filesystem guard (`internal/cmd/skill/export_*_test.go`). This change adds the
**command-level acceptance layer**: the writer cases of the cross-host
acceptance matrix, run against the real `hadron skill export`. It also moves
those cases out of `pending`, since they now execute.

## What the tests prove, and what they don't

The server **plans** every action. The client walks the disk, submits file
facts, guards the filesystem, carries out the plan and reports it. So each case
(`internal/cmd/skill_export_acceptance_test.go`) asserts the client half end to
end:

- **What the command sends** for the files on disk. It asks once per host, with
  the host named (P01). A no-id legacy file is sent with its `sourceUrn` and
  nothing hashed (P08). The hand-edit evidence travels as
  `hasExtraFrontmatter` (P14). `--force` travels only when given. Nothing is
  read through a link (P06/P17/P18) or from the deprecated `~/.codex/skills`
  (P20/P21).
- **What it does with the plan the contract calls for:** the bytes on disk, the
  report's classification, and the exit code (5 on any refused or failed
  item, #1326).
- **Every decision the client owns outright:** roots, links, foreign and
  unattributable destinations, a linked `SKILL.md`, and dry-run parity (the
  same classes as the real run, and nothing changed on disk). These come from
  team chat #1332.

**Whether the server returns that plan** for those facts is proven by the
resolver suite (hadron-server `resolvers.skillPlan` tests), not here. A fake
plan in these tests stands for **the contract's answer**, never for an
implementation's. That is why P08/P12–P14 are green on `main` while the planner
defect #1346 (hadron-server#1309, a legacy `::` source URN not paired to its
node) is open: the red half belongs to the server's resolver tests
(`cor:agt:030:01`, D-2026-09-24-A).

Each case runs against a disposable `HOME`, resolved the way the command
resolves it (on darwin a temp directory sits under a symlinked `/var`).

## Mutation checks (compiling, each red on its target)

| guard broken in `export.go` | caught by |
|---|---|
| the root / ancestor link guard off | P17, P18 |
| a linked skill directory's facts submitted | P06 |
| the linked-directory refusal off | P06 |
| `--force` never sent | P05, P08 |
| exit 5 dropped | P05 and the destination cases |
| `unrecognized` reported per host instead of once | P16 |
| **both** foreign-file guards off | foreign WRITE/MOVE, unattributable |

**Either foreign-file guard alone is enough** for a file already on disk.
The pre-plan `foreignSkillDirs` check and the write-time ownership check
(`checkOwned`, #696) are deliberate defence in depth, so switching off just one
of them leaves these tests green. The in-package
`TestHostFSWriteRechecksOwnershipBeforeWriting` (a foreign file planted
mid-write) is what isolates the second guard.

## The matrix: version 2, with a `writer` section

`pending` used to mean "skipped by name, never passed", which would be false
once these cases run. So:

- **A new top-level section, `writer`.** Each case is
  `{id, layer, contracts, given, expect, test}`. `given` and `expect` are carried
  over verbatim, since they are contract-cited prose, not generated. `test`
  names the subtest that executes the case
  (`internal/cmd TestSkillExportAcceptance/P03`).
- **The version is now 2**, so a runner that does not know `writer` fails loudly
  instead of silently dropping it. The Go loader
  (`internal/skilldoc/crosshost_acceptance_test.go`) reads version 2, requires
  every `writer` key, and refuses an empty `writer`.
- **A two-way guard**, `TestSkillExportAcceptanceCoversTheMatrix`. Every `writer`
  case has a subtest, every subtest has a `writer` case, and no `pending` case
  has a subtest. Mutation-checked in both directions.
- **`pending` keeps only what still has no executing test:**
  - **P02:** its planning half is a server implementation choice, not a
    contract, and its export-run half is P07 from the writer side.
  - **P07:** it can't be reached at command level. The CLI plans only the hosts
    in its own table, and every host has a root (`TestExportRootsCoverEveryHost`).
    A focused in-package test of `exportHost`'s `host-has-no-root` path is owed
    in `internal/cmd/skill`: every item failed *and named*, and other hosts still
    written.
- `TestCrossHostLoaderRefuses` no longer indexes `pending[0]` unconditionally,
  since `pending` may drain.

## Re-vendor (hadron-server)

`crosshost-acceptance.json` moves from sha256 `9c779fbf…` to the hash in the
PR. The server's runner (`src/lib/skilldoc/crosshost.acceptance.test.ts`) must
read version 2 and decode `writer`. It executes nothing in `writer`, since
that is the CLI's command layer, but it should pin that the section is present,
so a copy without it can't pass.

## Specs

No platform-spec change. This is test coverage and a fixture format change
inside this repo and its vendored copy. The contracts the cases cite are
unchanged.
