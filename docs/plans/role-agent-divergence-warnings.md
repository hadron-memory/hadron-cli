# Warn on role↔persona-role divergence (#542)

Design-as-built for hadron-cli#542.

## The gap

Standing up a role writes the same string into two independent places, coupled
only by equality:

- the **agent's** `personaRole` (`agent create --persona-role …`) — what a
  `worker cast --role` actually resolves against;
- the **team's** role **definition** (`team role create …`) — documentation,
  since the name register was removed (hadron-server#1050).

Both `team role create --help` and `worker cast --help` are explicit that the
definition is inert and that `--role` is not validated against it. Together
that means **nothing checks the two agree**, and the failure modes are
asymmetric:

- a typo in the agent's `--persona-role` → `worker cast --role …` returns
  `WORKER_AGENT_NOT_FOUND`. **Loud, already fine.**
- a typo in `team role create`, or no definition at all → the cast resolves the
  agent, the worker is created and labelled, and the only artefact is a
  definition documenting a role nobody holds (or a worker whose role nobody
  defined). **Silent.**

So the one loud path is guarded and the silent one is not. `cor:agt:020:00` §1
makes cast/role lists ergonomics, never a gate — so the fix is to **warn, never
refuse**.

## What this adds

### `team role list` — both directions, human-only

The join already exists: a role's `roleAgent` is the definition→agent match, so
a blank AGENT cell (null `roleAgentName`) IS the forward case. This surfaces it,
and adds the reverse:

- **forward** — a definition no installed agent carries:
  `! role <r>: no installed agent carries this persona role …`. Roster-free
  (it reads `roleAgentName`), so it always fires.
- **reverse** — an installed agent whose `personaRole` no definition names:
  `! agent <name> (persona role "<r>"): no matching role definition …`. This
  needs the install roster (`approster.Fetch`), so it is **skipped under
  `--json`** (the forward case is already in the data as a null field, and the
  reverse is `app agent list`'s data — the ambient-scope rule: don't issue a
  decorating read under `--json`) and **skipped under `--team-agent`** (a
  narrowed listing sees only one branch's definitions, so it cannot judge the
  reverse without false positives). Best-effort: an unreadable roster drops the
  reverse half rather than failing a working list.

`--json` is unchanged — the warnings are human-branch lines, exactly like the
existing `{{name}}`-placeholder `!` warnings.

### `worker cast` — a note at the moment of divergence

A successful `--role` cast whose role has no definition prints one line on
**stderr**:
`note: no team role definition for "<r>" — hadron team role create <r> documents it …`.
Stderr keeps the `--json` receipt on stdout clean, and it fires in both modes
because an agent casting in `--json` benefits from the same signal. Best-effort
via `roleDefinitionExists`, which returns *true* on an unreadable/ambiguous
roles branch — a read failure after a successful cast must not manufacture a
note the operator cannot trust, and must never fail the cast. Only `--role`
fires it; with `--agent`, `--role` is just a label.

## Deferred: `hadron team doctor`

The issue floats a `team doctor` collecting this with other roster checks
(retired-but-bound workers, installed-but-never-cast agents). Left out
deliberately — it is a whole new command and an open-ended checklist, whereas
the two surfaces above put the signal exactly where the divergence is read
(`role list`) and created (`worker cast`). Worth its own issue if the checklist
grows.

## Verification

Live against `hadron-dev-team` (read-only): `role list` runs clean and emits no
warnings — the team is consistent (9 roles, 9 agents, all matched), which is
the correct no-divergence output.

Tests assert both directions in `role list` (qa: defined, no agent → forward;
ios-app-engineer: installed, no definition → reverse; backend-engineer: both →
silent), that `--json` and `--team-agent` do NOT read the roster, that the cast
note fires only for an undefined role, and that a failed roles read leaves the
cast silent. Three mutants — drop the forward warning, force-skip the reverse,
invert the cast-note condition — each turned a named test red.

## Not here

- **No `--json` change** — warnings are human-branch only; the roleDTO already
  carries `roleAgentName` (null == the forward case).
- **No spec** — this reports on an existing contract (`cor:agt:020:00` §1: lists
  are ergonomics), it does not decide a new rule.
