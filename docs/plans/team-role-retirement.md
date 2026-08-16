# `team role rm` — retiring a role, and superseding one in a single call

Design-as-built for hadron-cli#441, riding `deleteTeamRole`
(hadron-server#1002, shipped in hadron-server#1014).

## Why this exists

`createTeamRole`/`updateTeamRole` shipped in #410, so the `role` group could
mint and edit definitions but never remove one. Retiring a role meant reaching
past the team surface entirely:

```bash
hadron node rm hrn:node:<org>:<team-agent>-system:roles:<old> --yes
```

That node delete knows none of the register invariants. Worse, the operation
people actually perform is not a *delete* but a **supersede** — a role gets
renamed, split, or absorbed, and its register has to reach the successor. Doing
that by hand was a four-step sequence whose ORDER was load-bearing and
invisible until it failed, because a name may not sit in two of one App's
registers:

1. capture the old register (nothing else holds a copy),
2. delete the old node,
3. widen the successor's `nameRange`,
4. append the names.

Wrong order, and you get `TEAM_ROLE_NAME_DUPLICATE` or
`TEAM_ROLE_NAME_OUT_OF_RANGE` — each after some steps already landed.

#447 documented that sequence in `team role --help` as the interim remedy.
This change deletes the sequence rather than documenting it better: the server
now does the whole thing in one App-scoped critical section.

## The surface

```
hadron team role rm <role> [--transfer-register-to <successor>]
                           [--team-agent <ref>] [--yes]
```

Thin over one mutation, per the group's standing rule that register logic is
server-side so MCP and the portal get it too:

```graphql
deleteTeamRole(appRef: ID!, teamAgentRef: ID, role: String!, transferTo: String)
  : DeleteTeamRolePayload!
```

The flag is named `--transfer-register-to`, not `--transfer-to`: what moves is
the **register**, and being explicit about that is the whole point — the thing
people get wrong is thinking a role owns its names.

## Semantics worth knowing (and the one we got wrong)

- **Retiring never frees a name.** #447's help text asserted the opposite
  ("Deleting the old definition is what FREES its names"). That conflated two
  different things. A name's permanence is enforced against the App's whole
  roster (`workers_app_name_uniq`, `cor:agt:020:02`) irrespective of any
  register. The register governs **allocation**; the roster governs
  **identity** — which is exactly what makes moving a register between roles
  safe. A test now pins the corrected wording, because the old claim would
  leave a reader expecting a name back.
- **Minted names gate the bare delete.** A taken register entry is the ledger
  recording that a name was allocated to this role. Dropping it would erase
  that, so a role holding minted names refuses `TEAM_ROLE_IN_USE` unless a
  successor is named. A fully free register retires with no ceremony.
- **Transferred names are exempt from the successor's range** — they are
  re-homed allocations, not new ones — and the successor's own conventions are
  untouched. Changing those stays `role update`'s job.
- **The delete is soft.** The `roles:<role>` node and its sub-nodes (a
  `roles:<role>:notes` is content *of* the role) are tombstoned, so a mistake
  is recoverable. That is also what lets the server restore the source role if
  the transfer half fails.

## Client-side decisions

**Resolve both roles from one scan, before anything is deleted.** The server
compensates a failed transfer by restoring the source, but a typo'd successor
should never reach that machinery at all. `scanTeamRoles` already pages the
App's roles to exhaustion for the group's other verbs, so one scan resolves the
source *and* the successor, turning both unknowns into an honest `NotFound`
(exit 4) with nothing written. It also lets the confirmation prompt name what
is at stake, and it sends the role slug **as the server spells it** rather than
as it was typed.

**Self-transfer refuses client-side** (exit 2). It is meaningless, and the
server would have to special-case it.

**The prompt differs by mode**, because the stakes do:

| mode | subject |
|---|---|
| transfer | `role X — its register (a, b, c) moves to Y, and the definition is tombstoned` |
| bare, free register | `role X and its register (a, b, c — none minted)` |
| bare, minted names | `role X — N of its names are already minted (…), so this will refuse without --transfer-register-to` |

The third exists so a caller is not asked to confirm a destructive action that
is then refused anyway.

**`transferTo` carries `omitempty` for a stronger reason than usual.** A bare
delete and a transfer are genuinely different operations server-side, so an
explicit `null` would have to mean "no successor"; omitting is the only
spelling that reliably says so. A test asserts the key is absent from the wire.

**The receipt shows the successor's register**, not the tombstone — that is
what tells the caller the move landed. The payload carries it, so no second
read is needed, and `--json` exposes the whole thing under `transferredTo`.

## Also in this change

- **`make unbound-ops` was destroying its own annotations.** The generator
  harvests existing `# reason` comments to carry them across a regen, but the
  Makefile redirected stdout onto the very file it reads — the shell truncates
  before the program runs, so every reason was silently lost. It now writes a
  temp file and moves it. This was found because the #439 annotations,
  committed an hour earlier, vanished on the first regen of this change.
- The schema refresh also pulled in `updateWorker` (server #1010), which no
  command wraps yet; it is recorded in the baseline with a reason.

## Testing

Command-level, against the fake GraphQL server: the transfer (wire variables +
receipt), the `--json` shape including the successor's full register with a
transferred minted name still marked taken, the bare retirement omitting
`transferTo`, an unresolvable successor deleting nothing, and the self-transfer
refusal. Plus two help tests: that the group help teaches the one-call form and
the "does not free a name" correction, and that it no longer teaches the
obsolete four-step sequence.
