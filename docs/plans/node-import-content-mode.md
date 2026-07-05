# Implementation Plan: `hadron node import` — content mode (URL / HTML / Markdown / PDF)

> **Status: implemented and verified.** Design as built. Extends the existing
> [`node import`](node-import-command.md) command with a second **mode** that
> surfaces the server's `importNode` mutation (hadron-server
> [#457](https://github.com/hadron-memory/hadron-server/pull/487),
> [#488](https://github.com/hadron-memory/hadron-server/pull/491) — the PDF
> path). Not backward-compatible: the `--json` shape gained a `mode`
> discriminator.

## Context

The server grew an `importNode` mutation (spec `cor:api:130`): take external
source — a URL fetched server-side, a captured HTML DOM, a Markdown file, or a
**PDF** — and store it as a node, converting to Markdown at the write seam. #488
added the PDF path (`content` base64-encoded, `contentType: application/pdf`,
text-layer extracted; scanned/image-only PDFs error). The CLI surfaced none of
it.

### Why one command with two modes (not a second `ingest` command)

`node import` already means "import into a node." It happened to do only one
thing — reconstitute a **node-export file** (frontmatter-md / canonical-JSON,
parsed by `internal/nodedoc`, written via `createNode`/`updateNode`). External
source material is a *different* operation (the server modelled it separately as
`importNode`), but from a user's point of view it's still "import." Rather than
grow a near-synonym command (`ingest`) users must disambiguate by memory, this
folds both under `node import` as two **modes**:

- **RESTORE** (default): reconstitute an export file. Unchanged behavior.
- **CONTENT**: ingest raw source through `importNode`.

The first draft shipped this as a separate `node ingest`; it was folded into
`import` after review — one verb, discoverable, with the mode chosen by input.

## Mode dispatch

Content mode is selected when the input is unambiguously (or explicitly) raw
source; otherwise restore, so `hadron node export … | hadron node import -` still
round-trips. Content mode iff **any** of:

- `--url` is set, OR
- `--as-content` is passed (the escape hatch to force an ambiguous `.md`/`.json`/
  stdin source into content mode), OR
- `--content-type` is passed, OR
- the source file's extension is `.pdf`/`.html`/`.htm` (a node-export file is
  never one of these, so no `--as-content` needed).

Everything else — a `.md`/`.json` file, or a stdin pipe — defaults to restore.
The ambiguity is real (a `.md` could be an export doc *or* raw Markdown) and is
resolved by rule, not by sniffing frontmatter (a raw `.md` starting with `---`
would fool a sniffer). RESTORE-only flags (`--format`, `--with-edges`,
`--create-only`, `--dry-run`) and CONTENT-only flags (`--url`, `--as-content`,
`--content-type`, `--name`, `--type`, `--properties[-file]`, `--node`) are
**rejected loudly** in the wrong mode rather than silently ignored.

## `--json` contract change

The two modes return different shapes, discriminated by a new `mode` field
(agents branch on it):

- `mode: "restore"` — `{memory, loc, action: created|updated, nodeId, edgesWired, unwiredEdges}`
- `mode: "content"` — `{status, memory, loc, nodeId, name, nodeType, jobId?}`

`mode` was added to the pre-existing restore shape too — a deliberate,
non-backward-compatible change (the CLI has no external consumers yet).

## Content-mode specifics

- **base64 is the CLI's job.** When the effective contentType is
  `application/pdf`, the file is read as raw bytes and base64-encoded into
  `content`; any other type passes bytes through as a string. A PDF over stdin
  therefore needs `--content-type application/pdf` (nothing is inferable from a
  pipe, and only that flag triggers the encode).
- **contentType inference** from extension (`.pdf`/`.html`/`.htm`/`.md`/
  `.markdown`), overridable by `--content-type`; unknown/stdin omits the field so
  the server applies its default (`text/html`).
- **Source dispatch:** exactly one of `--url` or a `<file>`/`-` argument.
- **Target dispatch:** `--node <urn>` XOR (`-m`/`--memory` + `--loc`), mapped
  1:1 onto `ImportNodeInput`'s own XOR so the server stays authoritative.
- **Omit, don't null.** The `ImportNode` operation carries per-field `omitempty`
  annotations (the server distinguishes omitted from explicit null); unset flags
  never reach the wire.

## Plumbing

- `make schema` refreshed the committed snapshot (it predated both `importNode`
  and `contentType`); the `ImportNode` operation was added to
  `internal/api/queries/nodes.graphql` and `make generate` produced the client.
  The refresh also surfaced `contentType` on `CreateNodeInput`/`UpdateNodeInput`,
  so `updateNodeInputFrom` (restore path) now maps it — the
  `TestUpdateNodeInputFromMapsAllFields` completeness invariant required it.
  - **genqlient gotcha:** `# @genqlient(for: …, omitempty: true)` only binds when
    the operation's variable list is on its **own line**; the single-line
    `mutation ImportNode($input: …)` form errors with *"for is only applicable to
    operations and arguments."*

## Testing

`internal/cmd/node_import_content_cmd_test.go`, asserting captured `ImportNode`
request variables and the `mode: content` DTO:
- `.pdf` file auto-routes to content → `application/pdf` + base64 content.
- `.md` + `--as-content` → `text/markdown`, verbatim (no base64).
- `--url` → url set, content/contentType omitted.
- PDF over stdin via `--content-type application/pdf` → base64.
- `--node <urn>` → nodeUrn only; `.html` infers `text/html`.
- Source XOR / target XOR → `Usage` errors.
- A restore-only flag (`--with-edges`) in content mode → `Usage` error.

The existing restore-mode tests (`node_export_import_cmd_test.go`) continue to
pass unchanged (a `.md`/`.json`/stdin source with no content flag stays restore).

## Out of scope

- The async URL path (`FETCH_PENDING` + `jobId`) — reserved server-side; the DTO
  already carries `jobId`, so no breaking change when it lands.
- A `taskUrn` post-import hand-off (deferred server-side).
