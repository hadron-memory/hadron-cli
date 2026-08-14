# Implementation Plan: adopt the Worker model (#428)

> **Status: implemented** — this PR is the migration. The plan was written
> ahead of the work (originally PR #430); the "Open questions" section below
> records the decisions as taken. Server side:
> hadron-server#974 (merged via #978, plus the #979 mention-token reversal),
> specs `cor:dmo:050:11` and `cor:agt:020` v0.0.3.

## What changed upstream

A persona is now pure **dressing** on an Agent (`personaRole` + a
`{{name}}`-templated `personaPrompt`). The named identity — "Iris" — is a
**Worker**: the casting of an installed Agent into an App. `Agent.personaName`
and `Agent.teamAppId` are gone; `createTeamPersona` is gone.

The consequence that drove it: names are scoped **per App**, not per owner org.
Locking the persona namespace org-wide behind whichever App minted first does
not scale for a larger organization.

## This migration is ATOMIC — plan for one big PR

`make schema` deletes `createTeamPersona` and `Agent.personaName` from the
snapshot, so `make generate` breaks the build in every file that touches them.
There is no incremental path: the schema refresh and the command rewrite land
together or not at all.

**The genqlient build is the loud check** for the renames
(`findings:sdl-resolver-arg-drift-invisible-to-tests`) — lean on it rather than
grepping. Refresh first, then let the compiler enumerate the work.

### Blast radius (15 files, as of `1721160`)

| Area | Files |
| --- | --- |
| GraphQL | `internal/api/queries/team.graphql`, `apps.graphql`, `internal/api/gen/generated.go` |
| Team commands | `internal/cmd/team/{persona,session,chat,team,state}.go` |
| Shared read | `internal/approster/approster.go` |
| App commands | `internal/cmd/app/{agents,agent_membership}.go` |
| Tests | `internal/cmd/team_cmd_test.go`, `app_agent*_cmd_test.go` |
| Docs | `internal/cmd/agentic/agentic-usage.md` |

## Suggested order

1. `make schema && make generate` — accept the breakage; the compiler is now the
   work list.
2. **Rewrite the GraphQL operations** in `team.graphql`: `castWorker`,
   `retireWorker`, `deleteWorker`, `workers(appRef, includeRetired, limit,
   offset)`, `worker(ref)`. Drop `CreateTeamPersona`. Re-point
   `PersonaAgentFields` at whatever the Worker type exposes.
3. **`team worker` command group** — `cast`, `retire`, `list`, `get`. Keep
   `team persona` as deprecated aliases for one release, or remove outright
   (decide; see open questions).
4. **Session binding** — `SessionInput.workerRef` takes the worker's **id**
   (workers have no URN). `Session.agentId` is stamped server-side from the
   casting, so the CLI stops resolving an agent.
5. **Chat + worklog field renames** — `authorWorkerId`, `workerName`/`workerId`,
   and the new error codes.
6. **Docs** — `agentic-usage.md` surface lines and prose; the `team --help`
   Getting Started block (#402) names `persona create` at step 4 and is now
   wrong.

## Traps, learned the hard way this cycle

- **Three drift gates will fire, all of them correctly.** `make schema-check`,
  `make unbound-ops-check` (new operations = a changed baseline; run
  `make unbound-ops` and commit), and `TestAgenticUsageDocumentsEveryCommand`
  (every new subcommand must appear on its group's surface line). None of them
  are optional and all three failed at least once this cycle.
- **The worktree binding format changes.** `internal/cmd/team/state.go` stores
  `agentId`/`personaName`; it needs `workerId`. Bindings written by older CLIs
  will lack it — decide whether an old binding is an error or a degraded read,
  and remember `session end --session <id>` is the recovery path when a binding
  is unusable. (`appBound` was added the same way in #414 and treats absent as
  "unknown", taking the conservative branch.)
- **`omitempty` on every new optional argument**, and a test asserting the unset
  field is ABSENT from the captured variables. A refreshed input field does not
  inherit it — that trap cost a `"appRef": null` on every session start in #395.
- **DTOs stay explicit structs in the command package**, never genqlient types.
  `approster.MemberDTO` is the one shared exception, and deliberately so.
- **Mention tokens carry no uniqueness** (hadron-server#979). Do not build UX
  that assumes `@token` names exactly one worker — no "did you mean" that
  presumes a single match, no resolution that errors on ambiguity as if it were
  a user mistake.
- **Retirement is idempotent and the name is reserved forever**; `deleteWorker`
  is the hard escape and refuses `WORKER_IN_USE` unless the worker never did
  anything. Keep `retire`'s confirmation copy honest about which is which.

## Open questions — as decided in this PR

1. **`team persona *` removed outright, no aliases.** Shipped days ago with
   near-zero adoption; carrying a deprecated surface would cost more than it
   saves. The persona-dressing editor (`personaRole`/`personaPrompt`) moved to
   `agent update --persona-role/--persona-prompt`, where agent metadata lives.
2. **`team roster` retired** per Holger's 2026-08-14 call: `app agent list` is
   the install roster (cast pool), `team worker list --app` the staff. The
   shared read (`approster`) now serves only the `app` noun, re-pointed at a
   slimmer `AppAgentRoster` query (no `personaName` — it no longer exists).
3. **PR trailer key stays `Persona:`**, value becomes the app-qualified
   compound `Persona: eng-team/Iris` — matching the epic and cor:agt:020:03's
   own spelling.
4. **Sub-issue re-scoping** stays follow-up work: #403 (role list), #404
   (dry-run), #407 (per-App list — partly answered by `team worker list`),
   #410 (register writes) were written against the persona model and need
   re-cutting against `castWorker`'s server-side allocation.

## Not in scope

hadron-server #960, #964, #965, #970, #972 remain open; #972 (the namespace
decision) is what produced this model, so re-read it before assuming any of the
others still apply as written.
