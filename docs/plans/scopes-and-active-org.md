# Named search scopes + active organization (#578)

Status: **as built** — all four slices in review (#594 → #595 → #596 → #597).
Server: spec 049 Phases 1–2, on `hadron-server` `main` (#1158 active org, #1160 scopes).
Issue: [#578](https://github.com/hadron-memory/hadron-cli/issues/578).

## 1. Why this is small

The instinct on reading #578 is that the CLI has to implement a resolution
ladder: flag › config › active App › active org › refuse. **It does not.** The
ladder is the server's, and this was probed against the live server (`0.18.0`)
before any code was written.

```
findNodes(scope: String)
  "a scope id, a bare scope name (needs appRef), 'app' (the App's attached
   memories; needs appRef), or 'global' (the active organization's view —
   orgId, or your single membership)"
```

The server takes the string, resolves it, intersects the scope's memories with
what the caller may read — *a lens never grants* — and reports back what it did:

```graphql
type SearchScopeInfo {
  kind: String!          # what sort of scope resolved
  label: String!         # its display name
  source: String!        # WHICH RUNG OF THE LADDER was used
  ownerUrn: String
  memoryUrns: [String!]!
  droppedCount: Int!     # in the scope, outside your access
}
```

So "every search prints the scope it ran under, plus the outside-scope count"
(#578) is **rendering a struct**, not deriving a fact. The portal renders the
same struct. That is the thin-client line: if this package ever grows a function
that *decides* which scope applies, it is in the wrong repo.

`scopeExplain` is the same story for the explain path — it returns the resolved
memories, `droppedCount`, `winner` for an address, and `shadowed[]` (the
lower-precedence scopes of the same name), and raises `SCOPE_NAME_AMBIGUOUS`
when two installed Agents own a name. App › Agent › organization, never unioned.

## 2. What the CLI does own

Three things, and they are all client-side by nature:

1. **Config keys** — `org` and `scope`, alongside the existing `app`, `memory`,
   `spec_memory` in `internal/config`. Config is local state, not business logic.
2. **Flag → argument plumbing**, including passing `appRef` whenever a bare
   scope name is used, because the server requires it to disambiguate.
3. **Rendering**, human and `--json`, including the disclosure.

## 3. Addressing: ids, names, and one thing that is not a bug

`scope(ref: ID!)` takes the scope's **id** only — *"a Scope has no URN yet"*.
Same for `updateScope` and `deleteScope`. A bare name resolves through
`scopeExplain(name, appRef)`, which is **one server call using the server's own
ladder** — calling a resolver, not reimplementing one. That is the rule for
every write verb here: never match names client-side.

`scope(ref:)` returns `null` for a scope that does not exist **and** for one the
caller may not read, identically — *"no existence disclosure"*. This is a
deliberate anti-enumeration guarantee, not a gap (the same rule governs
`channel(ref:)`, see [#593](https://github.com/hadron-memory/hadron-cli/issues/593)).

**Consequence for the exit-code contract:** the CLI must render ONE message
covering both cases and must not guess between `NotFound` and a refusal.
Phrasing that asserts only what is known — *"no scope `x` is readable here"* —
rather than *"no such scope"*, which claims something the server declined to say.
See `findings:a-deliberate-server-ambiguity-is-not-a-gap-to-close`.

## 4. `global` and `org use` are one feature

#578 lists `--scope global` and `hadron org use` as separate bullets. They are
not separable: the server defines `global` as *the active organization's view —
`orgId`, or your single membership*. Ship `--scope global` without a way to set
the active org and its meaning is undefined for anyone in more than one org.

They land together, in slice 3.

**And slice 3 has a message to fix, not just a feature to add.** Driven live,
`hadron search --scope global` with no active organization prints:

> You belong to 5 organizations and none is active: name one (orgId), select one
> (**hadron_set_active_org**), or select an App.

That names an **MCP tool** to a CLI reader. `orgId` is a GraphQL argument and
`hadron_set_active_org` is not a command anyone can run here — the same
surface-vocabulary leak that `hadron scope get` had with `appRef` (#594), and
that `--scope app` / a bare name had until this slice guarded them.

It is NOT guarded here, deliberately: the honest remedy is `hadron org use`,
which does not exist until slice 3, and string-matching a server message to
rewrite it would be worse than the leak. Slice 3 closes it by giving the CLI an
active organization to select, so the refusal stops firing.

The general version — *a server message naming another surface's remedy* — is
reported to the coordinator rather than patched here, since the portal reads the
same strings.

**Slice 3 closed it**, and turned up a second server-side issue on the way:

```
organization(ref:"nosuch.example")
  message:    "ORGANIZATION_NOT_FOUND: nosuch.example"
  extensions: { "code": "INTERNAL_SERVER_ERROR" }
```

The not-found is in the MESSAGE only; the CODE says the server broke. `MapError`
routes `*_NOT_FOUND` **extension codes** to `exitcode.NotFound`, so this exits 1
(internal) instead of 4, and no client can tell a missing organization from a
real fault. Not worked around here — string-matching the message to override the
code is exactly the client-side compensation this plan exists to avoid.
Reported, not filed (cross-repo).

## 5. Slices

| # | contents | why this cut |
|---|---|---|
| **1** | `hadron scope list/get/create/update/rm` + `explain` | The noun itself. Self-contained; no other command changes. |
| **2** | `hadron search --scope` + disclosure in table and `--json` | Touches `search`, which every agent parses — kept apart from slice 1 so a `--json` regression has one suspect. |
| **3** | `hadron org use` + `org` config key + `--scope global` | The pair from §4. |
| **4** | `scope` config key (session/default scope) + provenance disclosure | Last because it is the only part that changes what a *flagless* search does. |

**Slice 4's disclosure is the feature, not decoration.** Two provenance facts
are reported and they are NOT the same thing:

- `scope.source` — the SERVER's: which rung of its ladder resolved the string.
- `scope.selectedBy` — the CLIENT's: `"flag"` or `"config"`.

Neither side can supply the other. The server cannot know a value came from
local config; the client cannot know how the server resolved it. A search
narrowed by a stored setting, with no way for the reader to see it, is
indistinguishable from missing data — so the human header names the override
and the clear command, and `--json` carries `selectedBy` for an agent.

`app` and `global` are stored as typed and never pinned to an id: they resolve
per-invocation against whatever App / organization context the later search runs
in, which is the point of them.

Each slice is one PR.

## 6. Command surface (slice 1)

Naming follows the repo convention — `list` primary, `ls` a cobra alias
(`cli-list-primary-ls-alias`), and `rm` for deletion with `cmdutil.ConfirmDeletion`.

```
hadron scope list   [--owner-org <ref> | --owner-app <ref> | --owner-agent <ref>] [--name <n>]
hadron scope get    <name|id> [--by-name]
hadron scope create <name> --memory <ref> [--memory <ref> …]
                           (--owner-org <ref> | --owner-app <ref> | --owner-agent <ref>)
                           [--description <text>]
hadron scope update <name|id> [--by-name] [--name <new>] [--memory <ref> …] [--description <text>]
hadron scope rm     <name|id> [--by-name] [--yes]
hadron scope explain <name|id> [--by-name] [--loc <address>]
```

Notes that are contract, not taste:

- **The owner flags are `--owner-*`, NOT `--app`/`--org`/`--agent`.** `--app` is
  a PERSISTENT root flag meaning the App *context*; a local flag of that name
  would shadow it for this group only, so the group would be the one place
  `--app` stopped meaning what it means everywhere else. The App context is
  never a local flag here — it comes from the persistent one or the active App.

- **`--memory` is ORDERED and replaces.** `CreateScopeInput.memoryRefs: [ID!]!`
  is an ordered list and `updateScope`'s *"memoryRefs replaces the ordered
  list"*. So `--memory` is a `StringArrayVar` whose order is preserved and whose
  semantics on update are REPLACE, not append — the help text says so, because
  a user who expects append silently loses memories.
  This differs from `search -m/--memory`, which is an unordered set. Same flag
  name, different semantics, in different commands — called out in both helps.
- **Exactly one owner.** `createScope` takes `organizationRef` / `appRef` /
  `agentRef` and requires exactly one. Enforce with a client-side
  mutual-exclusion check that refuses loudly (`exitcode.Usage`) rather than
  letting the server reject a combination we could have named better — this is
  flag arity, not business logic.
- **The two input domains OVERLAP, and nodes' reasoning does not carry over.**
  `IsNodeID` is safe for nodes because a loc cannot be 32 hex characters. A
  scope NAME allows `[a-z0-9_-]`, so `deadbeefdeadbeefdeadbeefdeadbeef` is a
  legal name that is also id-shaped (@codex, #594). Shape still decides — it
  costs no round trip and the collision needs a pathological name — with
  `--by-name` as the explicit escape hatch. The rejected alternative was a
  speculative id lookup falling back on null, which charges EVERY id-based
  command an extra round trip to serve that one name.
- **The App context is resolved lazily, and only for a name.** An id is
  unambiguous, so a hand-edited config whose App ref no longer parses must not
  break `scope get <id>` (@codex, #594).
- **A name is 1–64 `[a-z0-9_-]`, unique per owner, never `global` or `app`.**
  Do NOT validate this client-side. `SCOPE_NAME_TAKEN` and the reserved-word
  refusal are the server's, and `api.MapError` already renders them. A
  client-side copy is a rule in two places that will drift.

## 7. DTOs

Explicit structs in the command package, per the repo's `--json` contract.
`memories` and `shadowed` initialize to `[]T{}` so they render as `[]`.

```go
type scopeDTO struct {
    ID                string              `json:"id"`
    Name              string              `json:"name"`
    Description       *string             `json:"description"`
    OwnerType         string              `json:"ownerType"`
    OwnerURN          *string             `json:"ownerUrn"`
    MemoryCount       int                 `json:"memoryCount"`
    HiddenMemoryCount int                 `json:"hiddenMemoryCount"`
    Memories          []scopeMemoryDTO    `json:"memories"`
}
```

**`hiddenMemoryCount` is not optional and not cosmetic.** The server documents
it as *"How many of them you may NOT read (count only — never their names)"*.
A fan-out that silently renders a shorter list than the scope contains is the
visibility-gap failure CLAUDE.md names: unreadable entries must be surfaced, not
dropped. It goes in the table footer and in `--json`, and it is never omitted
when non-zero.

## 8. Testing

Command-level against `fakeGraphQL` / `captureGraphQL` in `internal/cmd/`, keyed
by operation name. Specifically asserted:

- `--memory` **order reaches the wire** in the order given (`captureGraphQL` on
  the request variables) — an ordered list silently reordered is invisible in
  any output-level assertion.
- The **owner mutual-exclusion** refusal drives the real command, not a helper.
  A test that calls the validator directly passes with the wiring deleted.
- `hiddenMemoryCount > 0` renders in both branches.
- A bare name on a write verb issues `scopeExplain` **first** and sends the
  resolved id — asserted on the wire, since a client-side match would produce
  identical output.

Nothing here needs a live server, and there are currently **zero** scopes on
the production server, so no fixture is copied from one.

## 9. Not in scope

- A `Scope` URN grammar. Both `scope` and `channel` say *"no URN yet"*; that is
  hadron-server's open question (team chat #605) and the CLI adopts whatever
  lands rather than inventing an address.
- Channels, the register, Channel-addressed chat — items 2–4 of the parity
  split, tracked separately ([#593](https://github.com/hadron-memory/hadron-cli/issues/593)).
