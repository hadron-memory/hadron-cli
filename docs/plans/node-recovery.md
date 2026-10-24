# Deleted-node recovery (design as built, cli#985)

Server side: hadron-server#2234 (`recoveryNodes`, `recoveryNode`, `recoveryEdges`, `restoreNode`). Policy is the
server's and is not reopened here: writer-only audience, no purge window, no client retention clock.

## Commands

| Command | Operation |
|---|---|
| `node recovery list\|ls -m <memory> [--limit 1-200] [--offset N \| --all]` | `recoveryNodes` |
| `node recovery get <ref> [-m <memory>] [--content]` | `recoveryNode`, plus `recoveryNode { content }` only for `--content` |
| `node recovery graph -m <memory> [--limit] [--offset \| --all]` | `recoveryEdges` |
| `node restore <ref> [-m <memory>]` | `restoreNode` |

`node get|ls|rm` are untouched and stay live-only; a regression test pins that they never send a recovery operation.

## Decisions

- **References are not resolved.** `cmdutil.BatchNodeRef` composes id / URN / `-m` + bare loc and the result goes straight to the
  server. `ResolveNodeRef` is not used: the ordinary resolve omits tombstones and would refuse every deleted node.
- **Content is a separate operation.** The server's `content` field resolver decrypts and throws on an unreadable key, so the default
  point read never selects it. `--content` writes the raw stored body byte for byte in text mode (no header added) and sets
  `content` + `contentIncluded:true` under `--json`. A failed body read is an error, never an empty body.
- **Paging.** `limit` is always sent explicitly (default 20, cap 200); a zero offset is omitted. A single page reports continuation on
  STDERR from the exact `total`. `--all` is `api.CollectAll` over 200-row pages, advancing by rows received, and fails whole on any
  page error with nothing printed. Pages are separate reads, so concurrent restores can move rows between them; documented, not hidden.
- **One miss sentence.** A `recoveryNode` that returns null (live, absent, comment, not a writer) is exit 4 with one fixed sentence.
- **Restore sends one request, never retries.** A lost response is the existing exit 7 plus a line telling the caller to verify with
  `node recovery get` / `node get`; a receipt-write failure after success says the restore may have succeeded. The mutation selects
  identity and timestamps only (no content, description or edges), so no template or include expansion can run. The receipt is the plain
  node; the wire carries no changed/idempotent flag, so none is invented.
- **Exit codes** (`internal/api/errors.go`, accepted by Xan on #985): `NODE_PARENT_DELETED` 5 (the message names the ancestor), `BOOT_PROMPT_COMPILED_WRITE` 2,
  `ROLE_WRITER_LEVEL_REQUIRED` / `ROLE_WRITERS_UNSUPPORTED` 8 (placed before the `_REQUIRED` suffix rule that would file the first under usage).
  `NODE_WRITE_CONFLICT`, `ROLE_GOVERNED` (incl. comment), `LOC_PROTECTED` and concealed `NOT_FOUND` keep their existing mappings.

## Schema provenance

The committed snapshot already carried the recovery fields (it was refreshed from a later server revision than the #2234 merge, which is
an ancestor of it). `make schema` against hadron-server `cef136bb` (a detached worktree, `HADRON_SERVER_DIR` set) produced no diff.
Regenerating at `916e0b9d` itself would have removed unrelated newer fields (`cloneAgent`), so it was not used.

## Not in scope

No resolve-to-successor fallback, raw/template compilation, recursive restore, revision restore, client purge window or automatic retry.
