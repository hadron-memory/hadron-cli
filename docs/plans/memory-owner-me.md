# Implementation Plan: `hadron memory set --owner-me` — user-owned (org-less) memories

> **Status: implemented and verified**; this reflects the design as built. GH
> issue [#278](https://github.com/hadron-memory/hadron-cli/issues/278).

## Context

The server (spec 047 Memory slice, hadron-server `feat/user-owned-memory-create`)
made `createMemory`'s `orgId` **optional**: omit it and the memory is owned by
the authenticated caller in their own `@handle` namespace — `organizationId =
NULL`, `ownerUserId = caller`, and a bare `<handle>:<slug>` URN the server
computes.

Before this change the CLI's free-standing create **required `--org`**, so there
was no way to create `hrn:mem:<handle>:<slug>`. The only workaround was
hand-editing the DB, which caused a production incident: the *rendered* display
URN (`hrn:mem:holger:jens`) was stored instead of the bare form, double-prefixing
to `hrn:mem:hrn:mem:holger:jens` and silently dropping the memory from the
portal. (The server now also enforces `chk_memory_urn_not_prefixed`.) The lesson
baked into this design: **the CLI never constructs or prefixes the URN — the
server derives it and we echo it verbatim.**

## Server surface (the constraint that shapes the design)

| Op | orgId | Classes | URN root |
|---|---|---|---|
| `createMemory(orgId, …)` | present | knowledge/group/personal/private | org domain |
| `createMemory(…)` | **omitted** | **personal/private only** | caller `@handle` |

- Omitting `orgId` ⇒ user-owned; requires an authenticated (non-App) user.
- The org-less path rejects org-shared classes: `knowledge`/`group` need an org.
  The server default class (`knowledge`) is therefore invalid on this path.
- The server derives the handle (minting one if absent) and the bare URN.

## Codegen

`CreateMemory`'s `$orgId` went from `ID!` to `ID` with `# @genqlient(omitempty:
true)` (`internal/api/queries/memories.graphql`). Under `optional: pointer` this
regenerates the arg as `orgId *string` with `,omitempty` — a **nil pointer is
omitted** (not sent as an explicit `null`), which is what routes the server to
the user-owned path. The committed schema snapshot was refreshed with `make
schema` (the sanctioned wholesale re-export from `../hadron-server`), so it also
absorbs benign already-merged server drift the snapshot had lagged; the only
CLI-operation change is `createMemory.orgId`.

## Command surface (as built)

```
hadron memory set --owner-me --name <n> [--class personal|private] ...   # create
```

- **Routing** — the create block is a three-way switch: `appScoped` (—app/—agent)
  → `ownerMe` (—owner-me) → free-standing (—org). `--owner-me` is mutually
  exclusive with `--org` and `--app`/`--agent`, and only applies on create (the
  update guard rejects it alongside `--org`/`--class`/`--app`/`--agent`).
- **Class** — validated client-side to `personal`/`private` for a clear message;
  omitted `--class` **defaults to personal** (the server default, knowledge, is
  invalid here, so defaulting beats provoking a server rejection). Any other
  class is a usage error pointing at `--org`.
- **URN** — `createMemory` is called with `orgId = nil`. The server-returned URN
  is echoed as-is (bare or rendered) via the usual `output.Write` path; the CLI
  builds no URN.
- **Slug/schema follow-up** — unchanged. `--owner-me` is not app-scoped, so the
  existing post-create `--slug`/`--schema` `updateMemory` (with its partial-write
  reporting) applies as it does to free-standing create.

## Tests

`internal/cmd/commands_test.go`, against the fake GraphQL server:
- `TestMemorySetCreateOwnerMe` — `orgId` is **omitted** from request vars,
  `memoryClass=personal`, and the server URN is echoed verbatim.
- `TestMemorySetOwnerMeDefaultsToPersonalClass` — no `--class` ⇒ personal sent.
- `TestMemorySetOwnerMeValidatesFlags` — org-shared class, `--org`, `--app`,
  missing `--name`, and use-on-update are each usage errors.
