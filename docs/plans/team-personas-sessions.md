# Implementation Plan: `hadron team` — personas and sessions (#369 slice 1)

> **Status: implemented** — design as built. First slice of
> [#369](https://github.com/hadron-memory/hadron-cli/issues/369) (`hadron team`:
> personas, sessions, group chat, worklog — supersedes #368). Server dependency:
> hadron-server [#928](https://github.com/hadron-memory/hadron-server/issues/928)
> (persona columns on Agent + coding-session fields and `agentRef` on Session),
> shipped in hadron-server PR #929. Decision record:
> `hrn:node:hadronmemory.com:hadron-cli:reference:team-command-design`; full
> rationale in hadron-concept
> `design-discussions/platform-apps/2026-08-11-agent-personas/` (registry
> D-2026-08-11-001…008).

## Scope

Slice 1 of 3: `team persona create|list|get|retire` and
`team session start|whoami|log|end|list`. Out of scope (later slices):
`team chat`, the worklog collection + `team init`, and the `--pr/--issue/--commit`
ref-normalizer (`session log` takes a bare PR number for now; `session list --pr`
— the worklog-backed provenance query — is slice 3).

## The model (decisions the code must honor)

- **A persona IS an Agent.** `personaName`/`personaRole`/`personaPrompt` are pure
  metadata columns — nothing may fork behavior on "is a persona" (that would
  rebuild the deprecated AgentType axis). So persona CRUD rides the ordinary
  agent surface; the new GraphQL operations only widen the selection set.
- **A name binds to one persona forever** (D-2026-08-11-006). The server's
  persona-name uniqueness indexes deliberately ignore `deleted_at`, which makes
  `deleteAgent` (a soft delete) exactly the retire semantics: the persona leaves
  the roster, the name stays reserved. Hence `persona retire`, and deliberately
  **no `persona rm`**.
- **"Taken right now" derives from an active Session** (`endedAt IS NULL`), not
  from any lease entity. Per the #928 verification note (Q-2026-08-11-002),
  session auto-expiry is schema-only — no reaper writes `autoExpiredAt` yet
  (hadron-server [#930](https://github.com/hadron-memory/hadron-server/issues/930)).
  Until it lands, a crashed session holds its persona, so `session start` must
  show who last drove the persona and since when, and require `--force` to take
  over. `--force` starts the new session *alongside* the stale one — it does not
  end another driver's session.
- **`Session.prNumber` is a denormalized display convenience, never the
  provenance mechanism** — provenance is the worklog (slice 3).

## Command surface

```
hadron team persona create --name <candidate>... [--role <r>] [--prompt <p>] [--org <ref> | --owner-me]
hadron team persona list [--org <ref>] [--role <r>]        (`ls` alias)
hadron team persona get <name-or-ref> [--org <ref>]
hadron team persona retire <name-or-ref> --yes
hadron team session start --as <persona> [--repo] [--branch] [--transcript] [--host] [--tool] [--model] [--force]
hadron team session whoami
hadron team session log --pr <number>
hadron team session end [--summary <text>]
hadron team session list [--active] [--as <persona>] [--repo <r>] [--limit N] [--offset N]   (`ls` alias)
```

## Key mechanics as built

### Persona create: the PERSONA_NAME_TAKEN retry contract

`createAgent` rejects a duplicate persona name (unique per owner,
case-insensitive) with the typed code `PERSONA_NAME_TAKEN`. `--name` is
repeatable: candidates are tried in order via `api.HasErrorCode`, each rejection
falling through to the next; exhaustion is exit 5 (Conflict) with a message
explaining the forever-binding. The first free name becomes both `personaName`
and the agent `name`. (`codeForExtension` gained a `_TAKEN → Conflict` suffix
rule.) The design's register-driven auto-allocation (role templates + name
register in the Team Agent's system memory, D-2026-08-11-007) arrives with
`team init`; explicit candidates are the slice-1 contract it will layer onto.

### Persona resolution and the roster read

`AgentFilter` has no persona clause (metadata, not a server-side kind), so the
roster is `agents()` paged to exhaustion (issue-#23 rule: an unbounded list is
one default page) with `personaName != null` kept client-side
(`scanPersonaAgents`). A persona argument containing `:` goes straight to the
server as a ref; a bare argument is matched case-insensitively against roster
persona names, then falls back to an agent-ID lookup. The same name existing
for two owners is an explicit Conflict asking for `--org` or a URN.

### Worktree binding

`session start` writes `hadron-team-session.json` under the worktree's
**resolved git dir** — `git rev-parse --git-dir`, never a literal `.git/` path,
because a linked worktree's `.git` is a file pointing at
`<main>/.git/worktrees/<name>` and the binding must be per-worktree.
`HADRON_TEAM_GIT_DIR` overrides the git call (tests; exotic environments).
`whoami` is the compaction-recovery read: **local only**, no server round-trip.
`end` calls `endSession` then clears the binding.

### Session provenance fields

`startSession` takes a client-minted UUID id plus `agentRef` (resolved
server-side to `Session.agentId`, read-gated so a session is never attributed
to an agent the caller can't see) and the #928 provenance fields:
`repo`/`branch`/`host` (defaults to the hostname)/`tool`/`transcriptPath`/
`llmModel`.

### Presence and the occupancy check

`sessions()` has no agent or active filter, so `personaActivity` and
`session list --active/--as` page to exhaustion and filter client-side. An
active session need not be the newest row (an old still-open session can hide
behind newer ended ones), so absence of an active session is only proven by a
full scan; the scan short-circuits as soon as one active match appears.
`session list` joins persona names onto rows via one roster read (sessions only
carry `agentId`).

### `session log`: local-only in slice 1

The server has **no mutation that updates an existing session** —
`SessionInput.prNumber` exists only at `startSession`, and `endSession` takes
no prNumber. Filed as hadron-server #931. Until that lands, `session log --pr <n>`
records the PR in the local binding (visible via `whoami`), prints a loud note,
and reports `"recorded": "local"` in `--json`. TODO(#369 slice 3): the worklog
collection + `Session.prNumber` denormalization replace the local record.

## GraphQL layer

New `internal/api/queries/team.graphql`: `PersonaAgents` / `GetPersonaAgent` /
`CreatePersonaAgent` (persona-field selection over the agent surface),
`StartTeamSession` / `EndTeamSession` / `TeamSessions`. Retire reuses the
existing `DeleteAgent`. Schema snapshot refreshed from hadron-server main
(picking up #929). `SessionInput`'s optional fields carry
`# @genqlient(for: …, omitempty: true)` so unset flags stay off the wire.

## Testing

Command-level tests against the fake GraphQL server
(`internal/cmd/team_cmd_test.go`): the retry-with-next-name contract (stateful
fake), all-taken → exit 5, roster narrowing, name resolution + NotFound,
retire confirmation gating and its DeleteAgent wiring, start's binding write /
occupancy refusal (naming the driver and #930) / `--force` takeover, whoami's
offline read, log's local record, end's binding clear, and `--active`
filtering with the persona join. `HADRON_TEAM_GIT_DIR` points the binding at a
temp dir.

## Deferred / follow-ups

- **hadron-server #931**: a session-update surface (set `prNumber` on a live
  session) — until then `session log` is local-only.
- **hadron-server #930**: the stale-session reaper; when it lands, `start`'s
  refusal message can distinguish stale from live.
- Slice 2/3 (#369): `team chat`, `team init` + worklog collection, the
  ref-normalizer (`owner/repo#N` canonical form), `session list --pr`.
