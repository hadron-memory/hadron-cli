# Design as built: the pre-cast reads — `team role list/get` + `worker cast --dry-run` (#403, #404)

> **PARTLY SUPERSEDED — the register half of these reads is gone.**
> hadron-server#1050 removed the worker name register and hadron-cli#496
> removed the CLI surface over it, so `role list/get` no longer project
> `register[]`, `freeCount`, `exhausted`, `nameRange` or `nameConvention`,
> the dry-run JSON no longer carries `teamAgentId`/`teamAgentName`, and
> `worker cast` takes an explicit `--name` instead of allocating one.
> `cor:agt:020:07` is WITHDRAWN rather than superseded.
>
> What SURVIVES is the other half and still describes current behaviour: the
> dry-run preview reserves nothing (`cor:agt:020:03` — no lease by law), the
> preview is a Query rather than a `dryRun` flag, and its typed refusals are
> the cast's own. See
> [docs/plans/remove-name-register.md](remove-name-register.md).

> **Status: shipped (2026-08-15); register half since removed — see above.** Server halves: hadron-server#960
> (`teamRoles`, merged via hadron-server#984) and hadron-server#964
> (`castWorkerPreview`, merged via hadron-server#985). Both issues were
> re-cut against these surfaces on 2026-08-15 before implementation.

## Why one PR

Casting a worker is the one irreversible act in the team feature — a name is
permanent per App (`cor:agt:020:02`). These are the two reads that de-risk
it, they arrived in the same schema refresh, and each issue names the other
as its sibling: `role list` answers "which name is next" coarsely (the first
free register entry), the dry-run answers exactly (allocation + prompt
composition). Splitting them would have meant two PRs racing over
`team.graphql` and `agentic-usage.md` for no review benefit.

## The role read (`team role list` / `get`)

- **One `teamRoles(appRef, teamAgentRef?)` call**, paged to exhaustion (the
  issue-#23 rule). The server projects the one answer a client cannot
  compute: per-name `taken`/`heldBy` judged against the App's FULL worker
  roster — retired workers hold their names forever. The CLI **never
  recomputes** free/taken/exhausted; they are server truths.
- **Allocation order is load-bearing** — the register renders in order,
  never sorted (position determines who is cast next).
- `--json` contract: `roleDTO` with `register[]` entries carrying `heldById`
  beside `heldByName` (the actionable ref — `entity-fields-not-display-labels`).
  A taken name whose holder is unreadable renders as *"taken (holder not
  visible to you)"* — taken-but-masked must not read as free (the
  visibility-gap rule) and must not leak the holder.
- **The prompt template is not here.** Under the Worker model the template
  is the role AGENT's persona dressing; `TeamRole.roleAgent` names the agent
  and `hasNamePlaceholder` carries the server's nameless-template check
  (a template that never binds `{{name}}` mints workers whose own briefing
  never names them — surfaced as a `!` warning on both commands).
- `get` filters the `list` page client-side: there is no single-role query,
  the page is small, and one projection for both commands means they can
  never disagree.

## The cast dry-run (`worker cast --dry-run`)

- **A flag on `cast`, not a separate verb**: the flags are identical by
  construction and the muscle memory transfers. `--dry-run` routes the same
  arguments to the `castWorkerPreview` **query** — the server's design call
  (#964): previewing needs no mutation permission and mints no fake Worker
  row.
- The preview runs the cast's exact resolution — same typed refusals, same
  mint gate — up to but not including the writes. **A refusal on the dry
  run IS the answer** ("would this work?" → no, and here is the same typed
  reason the cast would give), so refusals surface verbatim with the
  ordinary exit-code mapping, not softened.
- **The preview reserves nothing** (no lease exists by law,
  `cor:agt:020:03`): the receipt says so explicitly, because the natural
  misreading of a dry run is "I now hold this name". The cast's uniqueness
  constraint remains the only allocation primitive.
- `--json` contract: `castPreviewDTO` — scalars plus the ids behind each
  displayed name (`agentId`, `teamAgentId`), and `reserved: false` as an
  explicit, always-false field so machine consumers cannot miss the
  non-reservation semantics.

## Test posture

- The dry-run fake omits the `CastWorker` mutation entirely — a dry run
  that reaches a write fails the suite loudly, not silently.
- The role fixtures cover the three register states (free / held-with-id /
  taken-with-masked-holder) and the exhausted register.
- Both `--json` contracts are asserted field-by-field, human renderings by
  their load-bearing strings (the `✓` marker, the warnings, the
  not-reserved caveat).

## Not in scope

- Register **writes** (`role create/update/names`) — #410, over
  `createTeamRole`/`updateTeamRole`, including the exit-code mappings for
  `TEAM_ROLE_NAME_MINTED`/`_DUPLICATE`/`_OUT_OF_RANGE`.
- Any local recomputation of allocation ("which name is next" beyond the
  first-free reading) — if a richer projection is ever wanted, it belongs
  on the server next to the verdicts it depends on.
