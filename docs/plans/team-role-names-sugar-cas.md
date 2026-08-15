# Design as built: the register sugar returns, CAS-safe — `role names add/rm/mv` (#436)

> **Status: shipped with this PR.** Server half: hadron-server#987 (merged
> via hadron-server#989) — `updateTeamRole(expectedNames:)`, the
> compare-and-swap precondition filed from PR #435's review, which chose
> the CAS shape over per-verb delta operations. This closes the loop
> [team-role-register-writes.md](team-role-register-writes.md) documented:
> the sugar was cut there for the lost-update race, "revisit only if the
> server grows a delta-based surface". It did.

## The mechanism

Every sugar write sends `expectedNames` — the register exactly as read.
When the stored register no longer equals it, the server refuses
`TEAM_ROLE_STALE` with the current `storedNames` in extensions, and the CLI
**rebases**: the edit is recomposed against `storedNames` (which also
becomes the next precondition) and retried — no second read needed. Two
admins racing `names add` now both land; the race the sugar was cut for is
closed by construction, because each retry re-derives from fresher state.

- **Bounded** (4 attempts): a register that will not hold still is
  surfaced as the conflict it is (`TEAM_ROLE_STALE` → exit 5), not spun on.
- **Narrated**: each rebase prints a stderr note, so a receipt that took
  two rounds says so.
- **Recompose can refuse**: an `rm`/`mv` whose target vanished between
  attempts fails as the same honest Usage error a typo gets — a concurrent
  removal is indistinguishable from naming something that was never there,
  and both deserve "not in the register".
- **A stale refusal without the payload** falls back to one re-read —
  and the extractor distinguishes an ABSENT/malformed `storedNames` from a
  legitimately empty register (review catch: fabricating `[]` there would
  rebase onto a register never observed).
- **`expectedNames` is bound `*[]string`** (review catch, P1): omitempty on
  a plain slice drops a present-but-EMPTY precondition, silently turning a
  sugar edit from an empty register into an unconditional write — the
  exact race this PR closes. The pointer keeps `[]` on the wire; nil (the
  `set` path) stays omitted. Same binding precedent as
  `UpdateMemory.tags`.

## What deliberately has no precondition

`names set` still submits unconditionally: an explicit whole-list
replacement asserts the FINAL state, not an edit of the observed one —
sending `expectedNames` there would turn "make it exactly this" into "make
it this only if nobody touched it", which is a different (and unasked-for)
command. The `set`-path test pins `expectedNames` absent; the sugar tests
pin it present and equal to the read.

## Placement notes

- `api.TeamRoleStaleNames` follows the `WorkerTakenDetail`/`DescendantCount`
  pattern: extensions extracted before `MapError`, message wording never
  parsed.
- `TEAM_ROLE_STALE` maps to Conflict — by the time a user sees it, the CLI
  already retried.
- The verbs themselves returned nearly verbatim from PR #435's pre-review
  revision (composition, absent-name refusals, bounds-checked 1-based
  `mv`), wrapped in the CAS loop; the review-node
  `review:no-rmw-sugar-over-wholesale-writes` now has its "acceptable
  shape 3" instance.
