# Design as built: the register writes — `team role create/update/names` (#410)

> **Status: shipped with this PR.** Server half: hadron-server#960
> (`createTeamRole`/`updateTeamRole`, merged via hadron-server#984). The
> issue was re-cut against that surface on 2026-08-15; the reads landed
> separately as [team-pre-cast-reads.md](team-pre-cast-reads.md) (#403/#404).

## What the server owns, and therefore what this is not

All three hazards the original issue documented are enforced server-side, in
one App-scoped critical section serialized with register-mode casting: a
name minted in this App may never leave the register
(`TEAM_ROLE_NAME_MINTED` — the entry records the allocation forever), an
added name may not appear in another of the App's registers
(`TEAM_ROLE_NAME_DUPLICATE`), added names validate against `nameRange`
(`TEAM_ROLE_NAME_OUT_OF_RANGE`, overridable with `--allow-out-of-range`),
and sibling `data` keys always survive. The CLI is **sugar and receipts**:
client-side composition can never smuggle a violation, so none of the
invariants are re-implemented here.

## Surface

- `role create <role> --names <a,b,c> [--name-range] [--name-convention]
  [--description] [--allow-out-of-range]` → `createTeamRole`. Refuses an
  existing role (`TEAM_ROLE_EXISTS`) — update/names are the edit paths.
- `role update <role>` — conventions and description only; the register has
  its own verbs.
- `role names set|add|rm|mv` — the register verbs.

## The three wire-semantics decisions

1. **Two update operations, not one.** `UpdateTeamRoleNames` carries only
   `names` (+ `allowOutOfRange`): the convention fields are absent from the
   operation, so they are *structurally* preserved — omitted-is-preserve
   enforced by the document, not by discipline. `UpdateTeamRoleMeta`
   carries `nameRange`/`nameConvention` with **no omitempty**: both are
   always sent, a value sets, an explicit `null` clears (the server's
   convention-key semantics).
2. **Clearing is explicit.** `--clear-name-range`/`--clear-name-convention`
   send the null; an empty-string value flag is refused (`--name-range ""`
   from an unset shell variable must not silently clear — the PR #418
   `--visibility` precedent). A value flag and its clear flag together are
   refused as contradictory.
3. **`role update` read-modify-writes.** Because the meta operation always
   sends both convention fields, an untouched field resends its current
   value from a fresh read. A race between read and write can re-assert a
   concurrently-changed convention; acceptable for an admin surface whose
   writes are rare and reviewed — the alternative (four single-field
   operations) buys atomicity nobody asked for at twice the surface.

## The sugar verbs

`set` is the wholesale replace (explicit, matching the server's own shape).
`add`/`rm`/`mv` compose the new ordered list from a **fresh `teamRoles`
read** and submit wholesale; the server's diff validation is the safety net
when that read goes stale — a conflict fails typed rather than clobbering.
`rm`/`mv` of a name not in the register refuse (`Usage`) instead of
silently no-opping: a typo'd rm must not leave the caller believing the
register shrank. `mv` is 1-based and bounds-checked, because order is
load-bearing — position IS allocation order.

## Receipts and exit codes

Every write prints the resulting register in allocation order with taken
markers (via the shared `roleDTO`/`registerLine` from #403) — the visible
outcome the original issue asked for, no follow-up read. New exit-code
mappings: `TEAM_ROLE_NAME_MINTED`/`_DUPLICATE`/`TEAM_ROLE_EXISTS` →
Conflict (5), `TEAM_ROLE_NAME_OUT_OF_RANGE` → Usage (2). Note
`TEAM_ROLE_EXISTS` needed an explicit case — the generic suffix rule
matches `_ALREADY_EXISTS`, not `_EXISTS` (the issue's claim that the
suffix rule covered it was wrong).

## Not in scope

- A `--prompt`/template flag: the prompt template is the role AGENT's
  persona dressing (`agent create/update --persona-prompt`), not role-node
  content, under the Worker model.
- Deleting a role definition: the server offers no `deleteTeamRole`; a
  register with minted names must survive regardless. If a genuine
  never-used-role cleanup need appears, it starts as a hadron-server issue.
