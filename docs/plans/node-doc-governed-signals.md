# Design as built: the governed signals in node files (#714, #720)

> **Status: built (cli#714; `objectType` added by cli#720).** Written
> 2026-09-25 by Jonas. Part of hadron-memory/hadron-concept#74 (CLI item 4),
> from Bo's node-kinds plan.

## What was verified before changing anything

Bo reported that `internal/nodedoc/document.go` omits `role` and `isRunnable`.
Measured on `main` at `c01d30e`:

| Surface | Carries role / isRunnable? | Where |
|---|---|---|
| `NodeBatch` query | **yes**, both selected | `internal/api/queries/nodes.graphql` |
| `api.DocumentFromBatchNode` | **no**, dropped | `internal/api/nodedoc.go` |
| `nodedoc.Document`, both codecs | **no field for either** (edges only) | `internal/nodedoc/document.go`, `markdown.go` |
| `memory export` | **no**, renders through the codec | `internal/cmd/memory/export.go` |
| `node export` | **no**. Rendered SERVER-side by `nodeExport` (`src/lib/nodeExport.ts`), which passes neither to `buildNodeFrontmatter` or the canonical JSON | hadron-server |
| server GitHub mirror | **yes**, since server#1218 (`a29ad13e`): `role`, `runnable`, emitted when non-null | `src/integrations/github/nodeFrontmatter.ts` |
| `node import` (restore) | writes through the **generic** `createNode` / `updateNode` | `internal/cmd/node/import.go` |

The last row was a second defect, and it was already live. The server's governed
gate (`governedRole.ts`, `assertGovernedWrite`) takes **before ∪ after**: a write
must go through the door of every governed kind it touches, the one the node has
now included. So re-importing an exported task, spec or review onto its own loc
was refused `ROLE_GOVERNED`, even from a file that said nothing about the kind.

Tracing it found a third: `api.UpdateNodeByKind` routed on the RESULTING state
only. So a write that removes a kind went to the generic door, which refuses it
(`removes`). `node update <task> --runnable=false` hit this.

## Design

1. **Format: the server mirror's, exactly.** `role` and `runnable` in the
   frontmatter sit after `seq` and before `data`, emitted when non-null. That
   includes an explicit `runnable: false`, because a node's `isRunnable` has
   three states; an edge's `runnable` omits false, since there false and absent
   are one state. The JSON codec uses `role` / `isRunnable`, `null` when absent.
   `Document` holds both as pointers, so nil is distinct from a value.
2. **Absent is "no opinion".** A file without the keys, including every file
   written before this change, sends neither field, and the server preserves
   the stored value. An empty `role` is read as absent too, since the server
   keeps `""` as a role no kind recognizes (the reason `node update --role ""`
   is refused).
3. **Import routes by kind, like `node add` and `node update`.** Import reads
   the stored kind once (the existing-node probe now returns the id, then
   `GetNode`), updates through `UpdateNodeByKind`, and on `NODE_NOT_FOUND`
   creates through `CreateNodeByKind`. `--create-only` creates by kind. This is
   routing, not a grant. Each door is still the server's gate, and `node add
   --role spec` / `--runnable` already reach the same doors.
4. **`UpdateNodeByKind` routes on before ∪ after.** One kind touched: that
   kind's door, which is also the door that may remove it. None: generic. Two
   or more: no door can do it (each is exempt from its own kind only), so the
   client refuses with exit 2 before sending, and `api.MapError` now passes an
   already-coded error through unchanged.
5. **A file declaring two kinds** (`role: spec` + `runnable: true`) is refused
   straight after parsing: before any request, before the overwrite prompt,
   and under `--dry-run` and `--create-only` too. A file whose kind meets a
   *different* stored kind is refused exit 2 when the stored kind can be read,
   and that check too runs before the dry run reports and before the prompt
   (@codex on #717);
   if that read fails, the server refuses it (`ROLE_GOVERNED`). Nothing is
   written either way.

### Carrying authority versus changing it

The issue asks the design to keep "carrying existing authority" apart from
"minting or changing a governed role". As built:

- A file that restates the stored kind, or states nothing, **carries** it
  through its own door.
- A file that **changes** the kind (adds one to an ordinary node, or removes one)
  goes through the door of the kind involved, exactly as `node update --runnable`
  / `--role` does. The server decides whether the caller may use that door
  (hadron-server#1202 owns who). The CLI adds no route the flags did not
  already have, and no generic bypass.

## Out of scope, routed

- **`node export`** is server-rendered, so its files still lack the keys until
  hadron-server's `nodeExport.ts` passes `role`/`isRunnable` to
  `buildNodeFrontmatter` and adds them to `canonicalJsonDocument`, after `seq`,
  in this codec's order. Until then those files import with no opinion. That is
  safe, but it is not a round trip.
- **`objectType` is fixed in the CLI by cli#720; see the addendum below.**
  Server-side `node export` omits it too, like the governed signals.
- **`ROLE_GOVERNED` exits 1.** The server's refusal is unmapped in
  `codeForExtension`, so it takes the default. The CLI refuses the same
  condition with exit 2 when it can see it. This predates #714 (`node add` /
  `node update` too), and mapping it deliberately is a follow-up.

## Evidence

- Tests:
  - `internal/nodedoc/governed_test.go`: placement, explicit false, old-file
    parse, JSON, round trip.
  - `internal/api/nodedoc_governed_test.go`: the export mapping.
  - `internal/cmd/node_governed_roundtrip_test.go`: import through each door; a
    pre-existing file onto a task; two kinds refused, including
    `--create-only`; the empty role; and `node update --runnable=false` on a
    task.
- Mutation-checked, 15 mutations, each red: the 13 below, plus the early two-kind check being skipped (reds the `--dry-run` and `--create-only` cases) and the stored-kind check being skipped (reds the dry run onto a task). The render drops role; the parse
  drops runnable; an explicit false is dropped; the export mapping drops a
  field; import drops role; import reads false as absent; import sends an empty
  role; import skips the stored-kind read; import creates generically; import
  skips the two-kind check; routing is after-only; the two-kind refusal is off;
  `MapError` re-maps a coded error.

## Addendum: `objectType` (cli#720)

`objectType` (#725) had the same gap. `NodeBatch` selects it, but the export
mapping and the codec dropped it, while the server mirror writes it.

- **Format:** the mirror's. `objectType:` sits right after `type:` and is
  written only when set. In JSON the `objectType` key is always present and
  `""` when unset. When the server's `canonicalJsonDocument` adds it, it
  should use this struct order: after `type`, `""` when unset.
- **Absent, empty or blank means "no opinion".** Import sends the key only
  when the value is non-blank. A whitespace-only value is refused too, because
  the server normalizes it to null, which would clear the stored collection.
- **What about an explicit clear?** A file cannot express one, deliberately:
  the mirror never writes an empty `objectType`, so a file carrying one is not
  a round trip of anything. The supported clear is `node update --object-type ""`.
- **The server is still the authority.** On a memory that declares a property
  schema, it validates `objectType` with the properties and refuses an
  undeclared collection as `BAD_USER_INPUT` (exit 2). On an unschema'd memory
  it accepts any value.
- **The #717 routing is unchanged.** `objectType` rides whichever door the
  node's kind needs.
- **Tests:**
  - `internal/nodedoc/objecttype_test.go`;
  - `TestDocumentFromBatchNodeCarriesObjectType`;
  - `TestNodeImportCarriesObjectType` (update, a task created through its
    door, blank and empty values, and a silent file);
  - `TestNodeImportUndeclaredObjectTypeIsTheServersRefusal`, which uses the
    server's text verbatim.
- **Mutation-checked, each red:** render drops it; parse drops it; the export
  mapping drops it; import drops it; import always sends it; the blank guard is
  removed. The review pass independently ran nine more, including removing
  `omitempty`, moving the key, renaming the JSON key, and
  `updateNodeInputFrom` dropping it.
- **Routed, not fixed here:**
  - hadron-server's `node export` omits `objectType`.
  - Its local-fs/git-sync importer reads an absent key as null (`?? null`),
    and git sync does not write `objectType` back at all.
  - Spec `cor:int:020:01` reads an absent field as unset, while this codec,
    like the mirror's governed signals, reads it as "no opinion".

