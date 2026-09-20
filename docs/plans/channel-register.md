# Implementation Plan: `hadron channel register` — Channel participation

> **Status: implemented and verified** (design-as-built). Closes
> [#636](https://github.com/hadron-memory/hadron-cli/issues/636), split out of
> [#476](https://github.com/hadron-memory/hadron-cli/issues/476). Backed by spec
> 049 Phase 5's register resolvers.

## Context

`hadron channel` could create, read, post to and delete a Channel, but could not
say **which attendees take part in one**. That is the register — spec 049
Phase 5 — and it
shipped server-side with five operations the CLI wired none of:

```graphql
createRegisterEntry(input: CreateRegisterEntryInput!): RegisterEntry!
updateRegisterEntry(ref: ID!, input: UpdateRegisterEntryInput!): RegisterEntry!
deleteRegisterEntry(ref: ID!): Boolean!
registerEntries(filter: RegisterEntryFilter, limit: Int, offset: Int, orgId: ID): RegisterEntriesPage!
registerEntry(ref: ID!): RegisterEntry
```

MCP has `hadron_register_add` / `_list` / `_remove`. An agent on MCP could manage
participation and a CLI user could not, which inverts the usual direction of our
parity gaps and breaks the rule that the CLI is a superset of the MCP tools.

### Why nobody noticed

`grep -rn -i register internal/api/queries/*.graphql` is **not empty**. Every hit
is the **retired worker-name register** ([hadron-server#1050](https://github.com/hadron-memory/hadron-server/issues/1050))
— the thing that allocated worker names, removed outright, with
`--transfer-register-to` and the `names` sugar gone alongside it. Our own prose
in `internal/cmd/team/role.go` and `team.go` describes its removal.

So a search for "register" in this repo returns confident, plentiful, irrelevant
results, and anyone checking coverage would conclude we had it. That is the
finding worth keeping from this change; the command surface is the easy part.

## The model

**A `RegisterEntry` is NOT a grant.** The schema says so twice — *"The register
never grants: what the attendee may read or post is the Channel's host memory's
decision"*, and on `RegisterRole`: *"What a register row declares: INTENT, never
permission (D-2026-09-13-008)."*

I had this wrong in the first draft, describing the surface as permission
management throughout, and @codex caught it as a P1. It matters because an
operator who runs `register rm` believing they revoked access leaves the door
open: an attendee who can reach the host memory keeps reading and posting.

A `RegisterEntry` is a declaration:

| field | meaning |
| --- | --- |
| `attendeeUrn` | a Worker or Agent URN — **or NULL, meaning every attendee in the owner's context** |
| `role` | the part declared: `BOTH` (reads and posts), `POST` (posts), `WATCH` (reads) — a declaration, not a capability |
| `mentionOnly` | only messages mentioning the attendee count as new |
| `ownerType` / `ownerId` | an App or an organization — the context the attendee is named in |
| `installDefault` | the row an App install materialised for its default Channel |

A Channel's declared participation was previously **unauditable from the CLI**:
you could not ask "who is registered here, and as what?" without `hadron api`.
That is a real gap — but it is a question about intent, not about access.

## Surface

```
hadron channel register list [--channel <ref>] [--attendee <ref>] [--owner <ref>] [--org <id>] [--limit N] [--offset N]
hadron channel register add  --channel <ref> --owner <ref> (--attendee <ref> | --all-attendees)
                             [--role both|post|watch] [--mention-only] [--description <d>]
hadron channel register set  <entry-id> (--role <r> | --mention-only[=false] | --description <d>)...
hadron channel register rm   <entry-id> [--yes]
```

Refs pass through **unexamined**, as everywhere else in this package: a
`channelRef` is an id or an address and the server resolves both
([hadron-server#1172](https://github.com/hadron-memory/hadron-server/issues/1172)).
A client-side matcher here would be a third copy of one addressing rule.

## The decisions

### 1. `--all-attendees` is required; an omitted `--attendee` is refused

**This is the CLI declining to expose a server default, not disagreeing with
it.** `CreateRegisterEntryInput.attendeeRef` is genuinely optional and null
genuinely means "everyone" — the server is right to model it that way. The
client is wrong to let it happen by omission.

The ruling (@Ada, #636) rests on three arguments; the second is the load-bearing
one and the third is the one to put in front of a reviewer:

1. **Widening by forgetting.** Three instances of that shape landed in one week —
   a coverage gate reporting coverage it did not have, a `sessions()` filter that
   would have offered a colleague's session to `session end`, a grep against a
   path that did not exist. Each time the instrument gave a confident answer
   while measuring nothing. **Here the confident answer would be a write.**
2. **It contradicts the platform's explicitness posture.** Hadron's declarations
   and grants alike are additive and explicit; nothing in the model says
   "unspecified means everyone". A CLI that declares broadly where the server
   model is explicit is the client inventing policy. *(@Ada's original wording
   argued this from the GRANT posture. Since the register does not grant, the
   argument survives on explicitness rather than on permission — a narrower
   claim than the one it was first made with.)*
3. **The asymmetry.** A forgotten flag declaring too LITTLE errors and costs ten
   seconds. One declaring too MUCH *succeeds*, and nobody finds out until the
   consequences surface. The default follows the cheap failure. *(Note this is
   no longer a security argument, since the row grants nothing — it is about a
   silent, wide write that is hard to notice.)*

**On the wire the wide entry is the ABSENCE of `attendeeRef`**, not a null — so
the input carries `# @genqlient(for: "CreateRegisterEntryInput.attendeeRef",
omitempty: true)` and the command only sets the field when `--attendee` was
given.

The reasoning deliberately does **not** extend to `register list --attendee`,
which is a read: an omitted filter there means "do not narrow", and narrowing a
listing by forgetting is the harmless direction.

### 2. Guards key on `Changed()`, never on the value

`--attendee=` is *"asked for nothing"*, which is a different mistake from not
asking at all. A value test collapses them, and the collapse is dangerous in
exactly one direction: `--attendee= --all-attendees` would read as "did not ask"
and proceed with the wide entry. See
`review:an-empty-flag-is-not-an-absent-flag`.

### 3. `set` changes only what is mutable, and refuses a no-op

`UpdateRegisterEntryInput` carries `role`, `mentionOnly`, `description` and
nothing else. The attendee and the Channel are immutable, so moving a
registration is `rm` + `add`, and the command does not pretend otherwise.

Unset flags are omitted (preserve) rather than sent as null (clear), so
`--mention-only=false` is how the setting is turned off — a bool's zero value is
indistinguishable from its absence by value, which is why this one is keyed on
`Changed()` too.

A `set` naming no field would be accepted by the server and return the row
unchanged, reading as success. It is refused: the likely cause is a mistyped
flag name.

### 4. `rm` tells the truth about what it does

`deleteRegisterEntry` sets `deletedAt` — a **soft** delete. So this uses
`cmdutil.Confirm` rather than `ConfirmDeletion`, whose prompt says *"This cannot
be undone"*: telling an operator that a recoverable action is permanent makes
them refuse a safe change, which is its own harm. Same call `channel rm` made.

The prompt also must not promise revocation. The register never grants, so a
prompt saying the attendee "loses access" would send an operator away believing
they had closed a door that is still open — the host memory is the door. The
`Long` text points at `memory member` / `memory share` for the real thing.

The prompt **reads the entry first** so it can name the attendee and role being
withdrawn rather than only an id — the wide entry is where that matters most.

The mutation returns a **boolean**, and `false` means nothing was removed.
Discarding it would report success (exit 0) on a failed removal, and automation
would treat a live registration as removed.

### 5. An empty `list` under a filter explains itself

The server's own wording: *a ref naming nothing matches nothing*. So an empty
page is ambiguous — nothing registered, or a mistyped ref. The note goes to
**stderr**, leaving `--json` a clean `[]`.

## Verification

- `make test`, `make lint`, `make generate` (generated output unchanged).
- **Nine mutations**, each verified to apply *and compile* before its test ran.
- Two of the nine initially reported a false result and were fixed rather than
  accepted:
  - **the `--attendee` guard** looked covered, but all three original cases
    refuse under a value test as well, so the guard could have been keyed wrongly
    with every assertion still green. Added `--attendee= --all-attendees`, the
    case that actually separates them — it *succeeds* under value-keying.
  - **the `rm` boolean mutation did not compile** (unused `resp`), so it measured
    nothing. Re-run as `_ = resp`, it goes red.
- The synopsis was checked against a **measured** matrix of 13 invocations rather
  than written by eye.
