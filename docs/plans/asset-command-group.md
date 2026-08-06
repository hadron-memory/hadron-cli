# Implementation Plan: `hadron asset`

> **Status: implemented and verified.** The read half (`list`, `get`, `url`)
> merged 2026-08-06 (`98c6e94`); this document now also covers the write half
> (`upload`, `rm`, `restore`, `link`) as built on the follow-up branch.
> Together they close [#359](https://github.com/hadron-memory/hadron-cli/issues/359).

## Context

The server has had a complete Asset surface for a while and the CLI covered
none of it. #359 filed the gap with a caveat: `list` beyond `--mine`, and `get`
for assets the caller did not upload, were **blocked** on the server widening
its asset read gates from uploader-identity to memory-read, and `link` / `url`
needed server surfaces that did not exist.

**Every one of those dependencies has since landed**, which is worth recording
because the issue still reads as blocked:

| #359 dependency | state on `origin/main` (`4e18d56`) |
|---|---|
| read gate widening for `memoryAssets` | landed — the resolver comment now reads *"That filter is gone (listing is now gated on the memory, per cor:dmo:060:10)"* |
| read gate widening for `assetDownloadUrl` | landed — gated on *"the memory's read gate rather than `uploadedBy === caller.userId`"* |
| a surface for `url` | landed — `Asset.publicUrl` |
| a surface for `link` | landed — `createAssetReferenceNode` |

One gap remains, and it is the reason this plan's `list` looks narrower than
the issue asked for.

## The one thing that could not be built

#359 specifies `asset list` with no `-m` ("everything readable") and `--org`.
**Neither has a server surface**: `memoryAssets(memoryId: ID!)` is the only
listing query, and its memory argument is non-null.

The tempting workaround — list readable memories, then query `memoryAssets` per
memory and merge — was rejected. It is an N+1 whose cost scales with the org's
memory count, and whose partial failures (a memory listed but not readable)
would be invisible in the output. This CLI's whole-scope commands promise
exhaustive results (#23); faking one over a fan-out would make that promise
unverifiable. `-m` is required instead, and the help text says why and links
[hadron-server#891](https://github.com/hadron-memory/hadron-server/issues/891),
which tracks the uniform cross-memory `assets()` query.

## What shipped

`internal/api/queries/assets.graphql` — `MemoryAssets` and `AssetDownloadUrl`.

`internal/cmd/asset/` — the group, plus three commands:

```
hadron asset list    -m <memory> [--mine] [--mime <t>] [--include-deleted] [--limit N] [--offset N]
hadron asset get     <asset-ref> [-o <path>|-] [--force]
hadron asset url     <asset-ref> [-m <memory>]
hadron asset upload  <file> -m <memory> [--mime <t>] [--name <n>] [--description <d>]
hadron asset rm      <asset-ref> [--yes]
hadron asset restore <asset-ref>
hadron asset link    <asset-ref> --node <node-urn> [--name <n>] [--description <d>]
```

## Design decisions

### 1. Asset refs carry their memory, so `-m` is usually unnecessary

An asset URN is `hrn:asset:<root>:<memory>:assets:<id>` — it *contains* the
holding memory. `parseAssetRef` returns both, so `asset url <urn>` needs no
`-m` while `asset url <id>` does. Parsing is Postel-liberal like the rest of
the CLI (optional `hrn:`/`urn:` prefix, legacy `::` accepted — #239), but the
`assets` marker is **required**: it is the only thing distinguishing an asset
URN from a node URN, and without it there is no way to tell which trailing
segment is the id. A node URN is rejected rather than silently mis-parsed.

The marker is located by scanning for it rather than by fixed index, so a root
or memory slug containing a colon cannot shift the id.

### 2. `list` pages to exhaustion; `--limit`/`--offset` opt into one page

The server defaults to 20 per page. The command's contract is "the memory's
assets", so it follows `hasMore` to the end (#23). An explicit `--limit` **or
`--offset`** is deliberate user-driven pagination and is honored verbatim as a
single page, matching `spec list` — paging on from a caller's `--offset` would
return far more than they asked to page through. `--limit 0` is rejected rather
than sent as a zero-row page that reads as "no assets".

### 3. The download deliberately carries no Hadron credentials

`assetDownloadUrl` returns a presigned URL pointing at object storage on a
different origin. `streamTo` builds a fresh `http.Client` and attaches no
Authorization header — sending the bearer token to that host would leak it for
no benefit. A test asserts the absence, because this is the kind of thing a
later refactor "helpfully" adds.

### 4. `get` will not clobber, and the write is atomic

The default output path comes from **server-side metadata** — a filename chosen
by whoever uploaded the file — not from something the caller typed. So it is
`filepath.Base`'d (a filename must never escape the working directory) and an
existing file is refused with exit 5 unless `--force`.

The download goes to a `.part-*` temp file **in the destination's own
directory**, renamed over the destination only after the transfer fully
succeeds. The first version wrote straight to the destination, which review
caught as data loss: `os.Create` truncates up front, so `--force` plus an
expired presigned URL destroyed the very file the caller was replacing, and the
cleanup-on-failure then removed it entirely. Renaming makes the replacement
atomic — the destination is either untouched or completely replaced — and the
same-directory temp keeps the rename within one filesystem.

`-o -` streams to stdout for piping, and suppresses the completion report —
the bytes *are* the output.

### 5. `url` is loud about what the link is

`Asset.publicUrl` is an **unauthenticated** hotlink; anyone holding it can fetch
the file. The command prints the URL on stdout (so it pipes cleanly) and the
warning on stderr (so it does not).

A null `publicUrl` is an **error with a reason**, exit 5 — not an empty line a
script would consume as a URL. The reason distinguishes the causes the caller
can act on (scan PENDING → wait; BLOCKED → never) from the ones they cannot
(encrypted memory, no configured origin).

The exit lives **outside** the human-render callback. Review caught that
`output.Write` never invokes that callback in `--json` mode, so the first
version exited 0 there — automation would have read "not hotlinkable" as a
successful lookup. Both modes now emit their reason (stderr for humans, the
DTO's `reason` field for `--json`) and both exit 5.

`findAsset` searches **live assets only**. Including soft-deleted ones would
turn a clean "no such asset" into a misleading "no hotlink, because its
scan…", for a file that has no hotlink by virtue of being deleted.

`url` needs a memory because `publicUrl` is only reachable through the listing
— there is no single-asset query — so `findAsset` scans the memory's assets by
id, paging to exhaustion. That is a real inefficiency, and the honest fix is a
server-side `asset(id)` query rather than a client-side cache.

### 6. `upload` fails before it transfers, not after

The three-step flow (`beginAssetUploadV2` → presigned PUT → `completeAssetUpload`)
exists so the size cap and MIME allowlist are enforced on the **reservation**.
Declaring `sizeBytes` and `mimeType` up front means a rejected upload costs one
round-trip instead of a full transfer, and the typed rejection comes from
Hadron rather than as an opaque 4xx from object storage.

The MIME type is derived from the extension, falling back to
`http.DetectContentType`; `--mime` overrides when the extension lies or is
absent. `mime.TypeByExtension` appends a charset for text types, which is
stripped — the server matches on the bare type.

The PUT mirrors the download's posture: no Hadron credentials (the presigned
URL *is* the authorization), and **only** the headers the server returned —
inventing or dropping one breaks the signature. `ContentLength` is set
explicitly because an `*os.File` is not a body type `net/http` can
length-detect, and a chunked PUT also breaks the signature. All three are
asserted by tests.

A failure after the PUT leaves the asset **reserved but not completed**: it
does not appear in `asset list` and the upload can simply be retried. The
command never completes an upload whose bytes did not land — that would publish
a listable, downloadable, empty file.

### 7. `rm` is soft, and says so

`softDeleteAsset` is recoverable within a retention window, so the confirmation
prompt reads *"It stays restorable for the retention window"* rather than
borrowing the permanent-deletion wording. An operator who reads "permanent" on a
reversible action learns to skim the prompts that are not. The success line
names the exact restore command. `restore` is non-destructive and does not
prompt.

### 8. `link` forwards the ref verbatim

`createAssetReferenceNode` accepts an id or a URN. The command forwards whatever
the caller passed rather than the parsed bare id, because the URN carries its
memory qualification — which matters precisely in the case the mutation is
built for, where the reference node lands in a *different* memory from the
asset (READ on the asset's, WRITE on the target's).

The resulting pointer is a **soft reference**: there is no schema-level
Asset→Node link (`cor:dmo:060:10` reserves it), so deleting the asset leaves the
node with its `asset` resolving to null. The help says so, because "the file is
gone but the node records that one was attached" is usually the desired audit
trail rather than a bug.

## Deliberately not done

- **`--org` and unscoped `list`** — no server surface; see above.
- **`agentAssets`** — the older agent-scoped listing is not wired. `memoryAssets`
  is the v2 surface and the one the issue's shape maps onto.

## Verification

Against the live server, **read-only**: `asset list -m hadronmemory.com::specs`
returns cleanly, and every usage error exits 2 — missing `-m` on `list` and
`upload`, a bare id without `-m` on `url`, a node URN passed as an asset ref, a
directory or missing file passed to `upload`, `rm` without `--yes`, and `link`
without `--node`.

The mutating paths (`upload`, `rm`, `restore`, `link`) were **not** run against
production data — that would write to a live corpus — so they are covered
against a fake object store and GraphQL server instead, including the exact PUT
semantics the presigned signature depends on. Worth an end-to-end run against a
scratch memory before relying on them.

Unit tests cover ref parsing (including every legacy spelling and the node-URN
rejection), memory-scope precedence, size formatting and the hotlink-absent
reasons. Command tests against the fake GraphQL server cover pagination to
exhaustion, `--limit` as a single page, null `publicUrl` surviving as null in
`--json`, the no-credentials assertion on the presigned GET, clobber refusal,
`--force`, and partial-file cleanup on a 403.
