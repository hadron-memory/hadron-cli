# `memory ls --shared-with-me` (#316)

> **Status: implemented** — merged 2026-07-29 (`d72d9f7`). Closes
> [#316](https://github.com/hadron-memory/hadron-cli/issues/316).

## Gap

A `MemoryShare` grantee could read the shared memory's nodes but had **no way
to learn its URN from the CLI**. `memories()` draws the caller's own union by
default, and `sharedWithMe: true` is a distinct slice *selection*
([schema.graphql](../../schema/schema.graphql), `MemoryFilter`) that
[`memory ls`](../../internal/cmd/memory/ls.go) never reached — it only ever set
`memoryClasses`. Surfaced while verifying #311: the did-you-mean tail on a
failed `-m` listed every memory *except* the one being named. The portal has a
"Memories shared with me" tab; the CLI had no equivalent.

## Design decisions

**The flag switches the listing, it does not narrow it.** A grantee is never
their own grantor, so the shared slice is disjoint from the caller's own union:
`--shared-with-me` *excludes* every memory you own, and the plain listing stays
the only way to see those. The flag help and the long help say so in those
words — read as a filter it would look broken, with memories "missing".

**A dedicated `MemoriesSharedWithMe` operation, not a field on `Memories`.**
It selects `myShare` for the per-row role + grantor, which
`MemoryFilter.sharedWithMe`'s own docblock points at for exactly this listing.
Adding `myShare` to the shared `Memories` query instead would put a per-row
share resolution on the query **every whole-scope caller pages to exhaustion**
(the spec fan-outs) — a needless N+1 for callers that never read it. If a
future change is tempted to merge the two operations, this is the reason not
to.

**The filter is hardcoded in the document, so the flags are mutually
exclusive.** `filter: { sharedWithMe: true }` lives in the query, which leaves
`--include-agent-system` unreachable in this slice. Shares are personal-class
by definition, so the combination would silently mean nothing; it is rejected
(`MarkFlagsMutuallyExclusive`) rather than ignored. That surfaced a real gap:
cobra's flag-group errors were exiting **1** for every command declaring a
group (`chat post`/`read`, `auth login`, `mcpserver update`, …), so
`isUsageError` now classifies them as usage errors (exit 2) alongside the
`required flag` case.

**`sharedBy` is the shared `accessUserDTO`, not a display label.** A bare
handle-or-name string would drop the grantor's **id** — the ref
`memory share rm --grantee` takes — and can't disambiguate two grantors with
the same display name. Both are also nullable, so a label can come out empty.
It emits the same `{id,name,email,handle}` object `share list` / `member list`
already emit, keeping the three access surfaces one shape; the table renders it
through the shared `accessLabel` (email → handle → name → id), which cannot
come up blank. Widening a string to an object later would have been a `--json`
break, not an additive change.

**`--json` stays back-compatible.** `shareRole` and `sharedBy` are `omitempty`
pointers on the shared `memoryDTO`, so every other memory command's shape is
untouched and their *absence* is the signal "not a shared-with-me listing" — as
opposed to "shared, but with no role".

**A null `myShare` still lists.** The field is nullable server-side; such a row
keeps its place (the contract is "every memory shared with me") and omits the
two fields. Dropping it would reintroduce exactly the invisible-hole failure
mode #316 is about.

## What this does NOT fix

The default listing still omits shared memories, so any client-side code that
reasons about "all my memories" from one `memories()` call keeps its invisible
holes — see `findings:memories-list-slices-are-selections` in the `hadron-cli`
memory. This flag is human/agent discovery, not a fix for the fan-out; the
answer there remains resolving leftovers by ref.

## Tests

`internal/cmd/memory_shared_ls_cmd_test.go`:

- the flag switches operations and prints `ROLE`/`SHARED BY` (and **not**
  `Memories`, pinning the no-N+1 decision);
- `--json` carries `shareRole` and a `sharedBy` **object** whose `id` is the
  grantee ref, and a null-`myShare` row survives without a role;
- a grantor with no name/handle/email falls back to the id rather than a blank
  cell;
- the default listing emits neither key and never queries the shared slice;
- combining the two flags exits 2 (with the `isUsageError` classification
  pinned in `root_test.go`).

## Out of scope

`memory share list` (shares *you* granted on a memory — the outgoing direction)
already exists and is unchanged.
