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
  its own verb.
- `role names set` — the ONE register verb (see below for why the sugar
  was cut in review).

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

## The register verb — and the sugar cut in review

`set` is the wholesale replace, explicit by design: read (`role get`),
edit, resubmit — the honest model of the server's own mutation shape.

`add`/`rm`/`mv` were implemented and then **removed before merge**. Both
review bots flagged the same defect in the initial revision: the sugar
composed the new list from a fresh read and submitted wholesale, on the
claim that "the server's diff validation is the safety net when that read
goes stale — a conflict fails typed rather than clobbering." That claim is
wrong for the case that matters: the diff checks protect only **minted**
names, so removing an unminted name is legal by design, and two admins
racing `names add` have the later wholesale write silently drop the
earlier one's free-name addition. No client-side fix exists — there is no
revision/precondition on `updateTeamRole` to hang a compare-and-swap on,
and the loss is invisible post-hoc (the response is exactly the list we
submitted).

The repo had already made this call once: `docs/plans/user-set-roles.md`
refused `--add`/`--remove` over the wholesale `setUserRoles` for the same
lost-update hazard — *"revisit only if the server grows a delta-based
surface."* Following that precedent: hadron-server#987 asks for delta
operations (or an `expectedNames` precondition), and hadron-cli#436 tracks
the sugar's return on top of it. What `set` retains from the original
issue's motivation: the retype-all-five typo hazard is now largely defused
by the server's diff validation — a typo that would drop a minted name
refuses instead of silently un-recording an allocation.

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
