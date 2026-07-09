# Implementation Plan: `hadron memory set --slug` — settable URN slug

> **Status: implemented and verified**; this reflects the design as built. GH
> issue [#108](https://github.com/hadron-memory/hadron-cli/issues/108) part 1.

## Context

Issue #108 raised three memory-CLI friction points. Two were already fixed:
short-form memory refs now resolve with an honest not-found error (#2, PR #138),
and `memory set` echoes the effective `class`/`visibility` on create (#3, commit
`01bfd8d`). The name→slug derivation is documented and the create output already
echoes the resulting URN.

The remaining gap (part 1): **the URN slug wasn't settable** — it was always
kebab-derived from `--name`, so a caller wanting display name `"Hadron PDF Tool"`
*and* slug `hadrontool-pdf` couldn't express it. This adds `--slug`.

## Server surface (the constraint that shapes the design)

| Op | Slug control |
|---|---|
| `createMemory(orgId, name, …)` | **No** slug/urn input — slug is `deriveSlugFromName(name)`, server-side. |
| `updateMemory(id, …, urn)` | `urn` accepts a **bare slug** (rejects a full/prefixed URN), kebab-normalizes it, and recomposes the memory URN — i.e. a rename. |

So the slug is settable only via `updateMemory`. There is no atomic
create-with-slug on the server.

## Command surface (as built)

```
hadron memory set --org <org> --name <n> [--slug <bare-slug>] ...   # create
hadron memory set <memory-ref> --slug <bare-slug> ...               # update = rename
```

- **Validation** — `--slug` is checked client-side with `cmdutil.ValidateURNSlug`
  (the same validator behind `org --urn`, `node --loc`, `agent --urn`), so a
  malformed slug is a usage error (exit 2) before any network call.
- **Update path** — `--slug` is passed straight through as `updateMemory(urn:)`.
  A no-slug update omits `urn` (genqlient `omitempty`), preserving it.
- **Create path** — `createMemory` runs first (slug derived from `--name`); when
  `--slug` was given **and differs** from the derived slug (compared
  case-insensitively against the created URN's trailing segment), the CLI issues
  a follow-up `updateMemory(urn: <slug>)` to rename. Matching slugs skip the
  second call, so the common case stays one round-trip.

## Partial-write semantics (create + rename)

Create-then-rename is two calls and can't be atomic (server limitation). If the
rename fails, the memory still exists under its derived slug. The command then:
prints the created record, and **exits non-zero** with a message naming the
memory's current URN and the exact retry (`hadron memory set <urn> --slug
<slug>`). This matches the CLI's existing partial-write contract (`node import
--with-edges`, `spec new/extract/supersede`): never report a clean success on a
half-finished write.

## Renaming caveat

A memory URN is the prefix of every node URN under it (node URN = memory URN +
loc), so renaming the slug changes those node URNs too. Node **ids** are stable
(URNs are composed, not stored), and edges reference ids, so links survive — but
any saved URN strings must be updated. The `--help` and docs call this out.

## Not in scope

Atomic create-with-slug — it needs a server change (`createMemory` gaining a
slug/urn input). Tracked as a possible hadron-server follow-up; until then the
create path's two-call approach is the CLI-only path.

## Tests

`internal/cmd/commands_test.go`: update-renames-via-urn, create-then-rename (+
renamed-URN echo), matching-slug-skips-the-rename, and bad-slug client
rejection. The existing update test also asserts `urn` is omitted when `--slug`
is absent.
