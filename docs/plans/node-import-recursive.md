# Implementation Plan: `hadron node import -r` — recursive directory-tree import

> **Status: implemented and verified** (design-as-built). Extends the
> single-node importer ([`node import`](node-import-command.md)) with a
> `-r/--recursive` mode that maps a local directory tree into a Hadron memory
> graph. Anticipated as the "directory/bundle import" follow-up in the original
> import plan's *Out of scope* section.

## Context & goal

Map a filesystem directory tree onto a memory subtree:

```
docs/                       →  branch node   loc: docs             name: "docs"
  how-to/                   →  branch node   loc: docs:how-to      name: "how-to"
    setup.md    (text)      →  leaf node     loc: docs:how-to:setup   name: "setup.md"   content: <file bytes>
    README.md              ──┐ folded into the how-to branch node's content
  diagram.png  (binary)     →  skipped (reported)
```

- **Directories → branch nodes** (name = dir name, content empty *unless* a
  README/index folds in).
- **Text files → leaf nodes** (loc = slugified path *sans* extension, name =
  filename *with* extension, content = file bytes).
- **Directory hierarchy → parent→child `contains` edges** (a real graph, not
  just loc-prefix strings).

Decisions locked with the user:

1. **One-shot import** — an existing loc is a hard error by default
   (`--on-conflict skip` to tolerate). No update/delete/mirror in v1.
2. **Binaries skipped & reported** — the object store holds structured records,
   not blobs, so file bytes have no home there; text-only for now.
3. **README/index folds into its directory node** — a folder's landing doc *is*
   the branch node, no redundant child.
4. **Materialize parent→child edges** — via the nested `CreateNodeInput.Edges`
   field, at zero extra round-trips (see *Creation order* below).

## Command surface

Hosted on the existing importer as a third mode (alongside RESTORE and CONTENT):

```
hadron node import -r <dir> -m <memory> [--under <loc>]
    [--type <nodeType>] [--on-conflict error|skip]
    [--include <glob>]... [--exclude <glob>]... [--hidden]
    [--max-file-size <bytes>] [--dry-run] [--json] [--yes]
```

- **`-r/--recursive`** selects tree mode; the positional arg must be a
  directory. Mutually exclusive with the content/restore-only flags
  (`rejectFlags` guards it, mirroring the existing mode dispatch).
- **`--under <loc>`** — a loc prefix prepended to the whole imported tree
  (default: none, so the tree roots at the walked dir's slugified basename).
  `import -r ./docs` → root `docs`; `--under manuals` → `manuals:docs`.
- **`--type`** — nodeType applied to every created node (default: server
  default). Reuses the content-mode `--type` flag.
- **`--on-conflict error|skip`** (default `error`) — `error` aborts on the first
  existing loc; `skip` leaves the existing node in place, resolves its id (so its
  parent's `contains` edge still wires), and records it as skipped.
- **`--include` / `--exclude`** — repeatable doublestar globs matched against the
  path *relative to the import root* (`**` supported). `--exclude` wins.
- **`--hidden`** — include dotfiles/dot-dirs (skipped by default). `.git` is
  always skipped.
- **`--max-file-size`** (default 1 MiB) — larger text files are skipped &
  reported (the server caps a node body ~1 MB).
- **`--dry-run`** — print the would-create tree + the skip list; no mutation.
- **`--yes`** — reserved for symmetry; tree mode is create-only, so nothing is
  ever overwritten (an existing loc is a conflict, not an overwrite).

## Path → loc mapping

Each path segment is slugified to satisfy the loc `slugRule`
(`[A-Za-z0-9._-]`, 1–64 chars, alnum-bounded — [urnvalidate.go:13]):

1. lowercase; 2. runs of illegal chars → single `-`; 3. collapse repeated `-`;
4. trim leading/trailing `-`/`.`; 5. truncate to 64; 6. empty result → `n`
(defensive). The **leaf loc atom drops the file extension** (`setup.md` →
`setup`); the **name keeps it** (`setup.md`).

**Collisions** (`setup.md` + `setup.txt` → `setup`; or a dir `foo/` beside a
file `foo.md`) are disambiguated by a numeric suffix on the loc atom (`setup`,
`setup-2`), assigned in a stable sorted order and reported in the summary. The
suffix advances past any atom a sibling already owns — so a literal `setup-2.md`
beside two `setup.*` yields `setup-2`, `setup`, `setup-3`, never a duplicate loc.
The final validated full loc is re-checked with `ValidateURNPath`.

## Creation order — post-order, edges for free

The tree is created **bottom-up (post-order): children before their parent.**
When a directory node is created, its children already exist with known ids, so
its `contains` edges are attached inline via `CreateNodeInput.Edges`
(`{name: "contains", targetId: <childId>}`) — **one `createNode` per node, no
separate `createEdge` calls, and no `resolveUrn` lag** (the ~1-minute
post-create resolution gap `spec new` documents). Leaf nodes have no outgoing
edges. This is the same two-pass insight the server's full-graph sync uses,
collapsed into a single call per node by ordering.

Creating a deep-loc child before its prefix parent exists is safe: hierarchy is
pure loc-prefix convention (no stored parent FK — `node ls --prefix` and the
`NODE_HAS_DESCENDANTS` delete guard both compute descendants from the loc
string), so there is no ordering constraint on the nodes themselves; ordering is
chosen only to make edges free.

**`--on-conflict skip`** on an existing child loc: catch `NodeLocConflictError`,
resolve the existing node's id (compose the node URN and `ResolveUrn`; a raw
memory id can't form a URN, so fall back to a loc-prefix listing + exact match),
use that id for the parent edge, mark it skipped. Because a skipped **branch**
node is never rewritten, its `contains` edges to the children created *this run*
are wired explicitly via `createEdge` (best-effort — a duplicate is rejected on
its derived loc and ignored), so those children aren't orphaned under a
pre-existing directory. A skipped node whose id can't be resolved is recorded
under `unresolved[]` (its parent edge is dropped) rather than dropped silently.

## README / index folding

Within a directory, a case-insensitive `README.md` / `README.markdown` /
`index.md` (first match, in that priority) is **not** emitted as its own child —
its file bytes become the **content of the directory's branch node**. Everything
else in the directory becomes a child. A directory with no such file gets an
empty-content branch node.

## Binary & filter handling

- **Binary sniff**: a file is text iff its first 8 KiB is valid UTF-8 with no NUL
  byte; otherwise skipped with reason `binary`.
- **Non-regular entries** (symlinks — even to a regular file — and
  FIFO/device/socket) are skipped with reason `irregular`: a symlink could pull
  content in from outside the import root, and a special file would block
  `os.ReadFile`. Directory symlinks are therefore never traversed.
- **Filters** (applied per entry, path **relative to the import root**):
  `--exclude` glob → `excluded`; not matching a non-empty `--include` set →
  `not-included`; dotfile without `--hidden` → `hidden`; over `--max-file-size` →
  `too-large`. `.git` always skipped. Glob patterns are validated up front (an
  invalid pattern is a usage error, not a silent no-match). Every skip is
  recorded with its reason.
- An empty directory (all children skipped/empty) still yields its branch node,
  so the tree shape is preserved.

## `--json` summary DTO (stable contract)

```jsonc
{
  "mode": "tree",                 // discriminator vs restore/content
  "memory": "acme.com::kb",
  "root": "docs",                 // root loc of the imported tree
  "created": [                    // one entry per created node, post-order
    { "loc": "docs:how-to:setup", "name": "setup.md", "kind": "leaf",   "nodeId": "…" },
    { "loc": "docs:how-to",       "name": "how-to",   "kind": "branch", "nodeId": "…" }
  ],
  "existing": [],                 // locs left as-is under --on-conflict skip
  "unresolved": [],               // skipped locs whose id couldn't be resolved
  "skipped": [ { "path": "diagram.png", "reason": "binary" } ],
  "collisions": [ { "path": "setup.txt", "loc": "docs:how-to:setup-2" } ],
  "edgesWired": 3,
  "nodesCreated": 2,
  "nodesSkipped": 1
}
```

All arrays initialized to `[]T{}` (render `[]`, never `null`). Rendered via
`output.Write` — a human tree/summary in the non-JSON branch. `--dry-run`
produces the same shape with empty `nodeId`s and an `edgesWired` count it
*would* wire.

## Failure semantics

- **Pre-flight, then write.** The whole tree is walked, slugified,
  collision-resolved, and validated *before* any mutation — so a bad loc or a
  filter mistake fails with zero side effects.
- A `createNode` failure mid-write (transport/authz) stops the run, reports the
  nodes already created (partial), and exits non-zero via `api.MapError`. Because
  creation is post-order, a failed parent leaves its children as valid,
  loc-addressable orphans (re-runnable with `--on-conflict skip`).

## Package layout

| File | Contents |
|---|---|
| `internal/cmd/node/import_tree.go` | walk → plan (slugify/collision/fold/filter) → post-order create-with-edges → summary; `importTreeSummaryDTO`, `treeNode` plan struct, `slugifyLoc` |
| `internal/cmd/node/import.go` | `-r/--recursive` + tree flags; mode dispatch & `rejectFlags` |
| `internal/cmd/agentic/agentic-usage.md` | document the tree mode |

Pure logic (`slugifyLoc`, collision assignment, the plan builder, README
detection, binary sniff) is unit-tested in-package with a `t.TempDir()` fixture
tree; the create/edge wiring is a cmd-level test against `captureGraphQL`
asserting the post-order call sequence, nested `contains` edges, conflict-skip
resolution, and the DTO shape (arrays are `[]`, not `null`).

## Out of scope (follow-ups)

- Repeatable **sync** (update existing, detect deletions) and **full mirror** —
  the two lifecycle options declined for v1.
- **Binary → object/blob** storage once a blob home exists.
- **`.gitignore`** honoring (needs a matcher lib; `--exclude`/`--hidden` cover
  the common cases for now).
- **Frontmatter parsing** (YAML → tags/properties) and per-extension nodeType
  mapping.
