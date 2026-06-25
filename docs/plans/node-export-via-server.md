# Route `node export` through the server renderer (#106)

## Goal

One renderer for every client. `hadron node export` currently renders MD/JSON
locally via `internal/nodedoc`; the server now exposes `Query.nodeExport`
(hadron-server #386), built to match `nodedoc` byte-for-byte. Routing export
through it guarantees the CLI's output is identical to the portal and any other
API client — and stays identical, instead of relying on two renderers never
drifting.

## Scope — `node export` only

`nodedoc` stays the source of truth for everything export doesn't cover, so it
is **not** removed:

- `node import` parses files with it (`ParseMarkdown`/`ParseJSON`).
- `memory export` renders the whole tree with it (per-node form differs — no
  self-keys — and `nodeExport` is single-node only).

Only the single-node render in `node export` moves to the server.

## Change

1. **Schema + codegen.** Add `Query.nodeExport` + its enums/result type to the
   committed snapshot and a `NodeExport` operation in `nodes.graphql`. The
   snapshot edit is surgical (only the `nodeExport` SDL) — a full `make schema`
   also pulls unrelated server drift (e.g. `OrgMember.role` going nullable per
   #384) that would break `org/member.go`; that belongs in its own change.
2. **`node export`.** Resolve the node id, call `nodeExport(id, format, full:
   true)`, and write the returned `data`:
   - stdout (default): write `data` verbatim — single round-trip, nothing else.
   - `-o <file>`: write `data`, then one light `nodeById` read for the
     `--json` summary's loc/name/memory (the render carries no identifying
     metadata). Best-effort: a failed metadata read just blanks those fields.
3. **Old servers.** Hard-require the field (per decision). A server without
   `nodeExport` returns a schema-validation error; `isUnknownFieldErr` turns
   that into a clear "this hadron-server is too old … upgrade the server"
   (exit 2, Usage) instead of a raw GraphQL dump.

The `--json` summary shape (`exportNodeSummaryDTO`) and all flags are unchanged.

## Not done (still server-side TODO, tracked in #106)

`--format html|pdf` waits on hadron-server Phases 2–3 (`nodeExport` is MD/JSON
only today; no HTML/PDF, no `asUrl`/BASE64). Nothing to wire until then.

## Tests

`node_export_import_cmd_test.go` rewritten for the server path: the CLI writes
the server's bytes verbatim (file and stdout), requests the right format
(MD/JSON), surfaces a server render error, and converts the old-server
unknown-field error into the upgrade message.
