# Implementation Plan: `hadron asset` — read surface

> **Status: implemented and verified** on this branch; reflects the design as
> built. First half of [#359](https://github.com/hadron-memory/hadron-cli/issues/359)
> (`list`, `get`, `url`); the write half (`upload`, `rm`, `restore`, `link`)
> follows in its own PR.

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
hadron asset list -m <memory> [--mine] [--mime <t>] [--include-deleted] [--limit N] [--offset N]
hadron asset get  <asset-ref> [-o <path>|-] [--force]
hadron asset url  <asset-ref> [-m <memory>]
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

### 2. `list` pages to exhaustion; `--limit` opts into one page

The server defaults to 20 per page. The command's contract is "the memory's
assets", so it follows `hasMore` to the end (#23). `--limit` is the explicit
single-page escape hatch, and it also sets the page size so it means what it
says.

### 3. The download deliberately carries no Hadron credentials

`assetDownloadUrl` returns a presigned URL pointing at object storage on a
different origin. `streamTo` builds a fresh `http.Client` and attaches no
Authorization header — sending the bearer token to that host would leak it for
no benefit. A test asserts the absence, because this is the kind of thing a
later refactor "helpfully" adds.

### 4. `get` will not clobber, and cleans up after itself

The default output path comes from **server-side metadata** — a filename chosen
by whoever uploaded the file — not from something the caller typed. So it is
`filepath.Base`'d (a filename must never escape the working directory) and an
existing file is refused with exit 5 unless `--force`.

On any failure mid-transfer the partial file is removed. A truncated file looks
like a successful download to everything downstream, which is worse than none.

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

`url` needs a memory because `publicUrl` is only reachable through the listing
— there is no single-asset query — so `findAsset` scans the memory's assets by
id, paging to exhaustion. That is a real inefficiency, and the honest fix is a
server-side `asset(id)` query rather than a client-side cache.

## Deliberately not done

- **The write half** — `upload`, `rm`, `restore`, `link` — is the follow-up PR.
  `upload` in particular is a three-step flow (begin → presigned PUT → complete)
  with typed server errors to surface verbatim, and deserves its own review.
- **`--org` and unscoped `list`** — no server surface; see above.
- **`agentAssets`** — the older agent-scoped listing is not wired. `memoryAssets`
  is the v2 surface and the one the issue's shape maps onto.

## Verification

Against the live server: `asset list -m hadronmemory.com::specs` returns
cleanly; usage errors exit 2 for a missing `-m`, a bare id without `-m`, and a
node URN passed as an asset ref.

Unit tests cover ref parsing (including every legacy spelling and the node-URN
rejection), memory-scope precedence, size formatting and the hotlink-absent
reasons. Command tests against the fake GraphQL server cover pagination to
exhaustion, `--limit` as a single page, null `publicUrl` surviving as null in
`--json`, the no-credentials assertion on the presigned GET, clobber refusal,
`--force`, and partial-file cleanup on a 403.
