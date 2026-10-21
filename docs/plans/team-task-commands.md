# `team task / roster / events / alarm / availability` — Task Management T4

Issue [#830](https://github.com/hadron-memory/hadron-cli/issues/830) (epic
[hadron-server#1704](https://github.com/hadron-memory/hadron-server/issues/1704)),
schedule **T1 Standard** per Xylon's CHECKED comment: cross-family blind +
local + QA through the CLI against a fresh stack; UX waived; §10 n/a (thin
caller over existing server gates). As built by Joran, 2026-10-12.

## What landed

```
hadron team task create|assign|reassign|start|progress|finish|cancel|get|list|tree
hadron team roster
hadron team events [--alarms] [--since …] [--after …] [--task …] [--type …]
hadron team alarm ack <eventId>
hadron team availability get | set
```

All thin callers over the #1705/#1706/#1707 surfaces, added as typed
genqlient operations in `internal/api/queries/agenttasks.graphql` (schema
snapshot refreshed from hadron-server `5b618578` == origin/main).

## The actor rule, and how it is implemented

The issue's rule: *write commands send the worktree's worker session when
one is bound — the worker is the actor; with none bound, a person writes as
themselves over GraphQL.*

The agent-task mutations carry **no `sessionRef` argument** (unlike
`createTeamChatMessage`), so the session cannot ride the variables. It rides
the `X-Hadron-Session` header instead: the ten new mutation root fields are
added to `authoredWriteField` in `internal/cmd/team/write_attribution.go`,
which is the reviewed middleware from #821/#822 — binding rechecked before
every authored mutation, App-identity cross-check when `--app` is set, and a
stderr note when attribution is unavailable. Nothing task-specific was
invented; the commands don't touch the binding at all for attribution.

**Amended (#868, 2026-10-13): `setModelFamilyAvailability` is no longer
authored.** A family's availability is the App manager's write
(cor:agt:060:09), and the server's `actorManagesApp` accepts a user actor
only, so sending it with the worker session refused every manager working
from a bound worktree (Chip reproduced it: exit 8 bound, exit 0 unbound). It
now always goes as the person, like `setAgentTaskStallSchedule` (#866). The
worker's OWN record (`setWorkerAvailability`) stays authored: a worker may
mark itself unavailable. See
`hrn:node:hadronmemory.com:hadron-cli:findings:manager-only-writes-must-not-ride-the-worker-session`.

Reads (`get`, `list`, `tree`, `events`, `roster`, `availability get`) are
plain: no header, no read-state effects.

## Design decisions

- **Party syntax is `worker:<ref>` / `user:<ref>`, loudly.** The server's
  party input takes `{worker | user}`; a bare CLI value could be a worker
  name or a person's handle — both bare words. Rather than guess (a wrong
  guess routes a task to the wrong party, irreversibly), a bare value is a
  usage error naming both spellings. A URN survives the split because the
  kind prefix ends at the FIRST colon.
- **`closeSessionWhenDone` is tri-state.** `--close-session` /
  `--no-close-session` on assign/reassign send `true`/`false`; **neither
  flag sends nothing** — the task keeps its value (the mutation doc:
  "when given, replaces"). On create, only `--no-close-session` exists
  (the server default is true). The text receipts of create/assign/reassign
  and `task get` print the **server-returned** value as `close session when
  done: yes|no` (yes adds what finishing or cancelling will request; no is
  the bare line) (#958) — never the flag
  that was passed, so a default reads truthfully; `--json` already carried
  the boolean.
- **`task list` is newest-first with `ls` aliased** (the house rule every
  `list` carries); filters map 1:1 onto the query arguments; `--status` is
  validated client-side so a typo is a usage error, not an empty result.
- **`task tree` joins alarms client-side, as presentation.** The tree query
  returns flat parent/child rows; one bounded `agentTeamEvents(alarmsOnly)`
  read supplies the unacknowledged alarm count per node. The alarm FACTS are
  the server's; only the join is ours — stated in the command's help. Orphan
  rows (parent outside the chain) render at the top level rather than
  vanishing.
- **`events` says its order out loud.** The stream reads OLDEST-first from
  the beginning (server `orderBy createdAt asc, id asc` — verified in the
  resolver, the schema prose is ambiguous). That is the #1534 shape wearing
  a different coat, so the help text states it, and when a page comes back
  full the receipt names the exact `--after <id>` that continues it.
- **`roster` renders, never derives** — every state is `teamRoster`'s. The
  states are plan-derived (design:agent-tasks §7.4; cor:agt:050 Out Of
  Scope): the help text and the receipt footer both say so, per the CHECKED
  brief.
- **`availability set` mirrors the server's input contract loudly**:
  `--reason` required with `unavailable` and refused with `available`;
  `--until` unavailable-only; exactly one of `--worker`/`--family`. A worker
  NAME needs the App (the server refuses a name without appRef) — every
  scope-needing command resolves the App from `--app`, the configured
  context, or the worktree binding, the same `resolveTeamAppScope` chat
  uses.
- **Task-id verbs need no App at all** — the id names the task; a person
  with no binding and no `--app` can still `task start <id>` as themselves.
  Only `create` (root), `list`, `events`, `roster` and `availability` resolve
  an App; `create --parent` may take it from the parent.

## The `--json` contract

Explicit DTOs in the command package (never genqlient types): `taskDTO`
(with `openAlarms` set only by `tree`), `teamEventDTO`, `rosterDTO`,
`teamAvailabilityDTO` / `workerAvailabilityDTO` / `familyAvailabilityDTO`.
Lists initialize to `[]T{}` so empty renders `[]`, never `null`. Party
projections are the sanitized `{id, kind, name, urn}` the server returns —
never a User object.

## Red-first evidence

`internal/cmd/team_task_commands_test.go` — 34 cases committed red first
(commit `bc4b1bc`: every verb failed with cobra "unknown command", the
CHECKED gate's per-command acceptance), then the implementation turned them
green. Coverage: every verb's happy path, the attribution rule (session
header on writes with a binding; none unbound; none on reads), party-syntax
and state/reason validation refusals, filter variables, the `--json` shapes,
the tree alarm join, and the roster help's plan-derived label.

## The Hadron review walk (before the PR)

`tasks:review-changes` run against the diff; children walked and their
outcomes:

- `omit-unset-optional-args` — pass. Every optional VARIABLE carries
  `# @genqlient(omitempty: true)`; the create-semantics input structs'
  pointer nulls are fresh-create fields (nothing to preserve), the one
  preserve-shaped case (`assign/reassign closeSessionWhenDone`) is an
  omitempty variable the tests pin all three ways.
- `regenerate-after-graphql-edit` — pass. `make generate` clean; CI's
  freshness gate sees the committed pair.
- `stable-json-dto` — pass. Explicit DTOs, `[]T{}` everywhere; a test pins
  `tags` as `[]`.
- `map-new-server-error-codes` — **found a real gap, fixed.** The new
  surface's typed refusals were landing on the default exit 1. Now mapped
  deliberately in `codeForExtension` with table rows: permission refusals
  (TASK_NOT_OWNER, TASK_NOT_ASSIGNEE, ALARM_NOT_IN_CHAIN,
  AVAILABILITY_NOT_PERMITTED) → 8; state conflicts (TASK_INVALID_TRANSITION,
  TASK_ENDED, ALARM_ALREADY_ACKNOWLEDGED, APP_UNINSTALLED) → 5; fixable
  inputs (TASK_OWNER_INVALID, TASK_ASSIGNEE_INVALID, TASK_CHILD_OWNER_MISMATCH,
  NOT_AN_ALARM, SESSION_NOT_BOUND) → 2; the `_NOT_FOUND` family rides the
  existing suffix rule.
- `ambient-scope-must-report-its-source` — **found a real gap, fixed.** The
  scoped reads printed no scope line; an empty task list on the wrong App was
  indistinguishable from an empty one (#470's exact shape). Every scoped
  render now opens `app: <readable urn> (<which branch answered>)`,
  unconditionally, lazily, render-only; `task create`'s receipt names the App
  it landed in; the degraded path (identity read fails → raw ref) is tested.
- `backticks-in-flag-usage-become-the-placeholder` — pass (zero backticks in
  the new flag usage strings).
- `entity-fields-not-display-labels` — pass (party fields carry the full
  sanitized `{id, kind, name, urn}` projection).
- `help-text-matches-changed-behaviour`, `a-pointer-must-be-followed-not-reasoned-about`,
  `an-empty-flag-is-not-an-absent-flag` (every empty value means the same as
  absence; `--state ""` refuses loudly), `gate-on-the-condition-not-the-flag`
  (the close-session flags ARE the instruction, not a proxy), 
  `a-new-meaning-for-an-existing-glyph` ("—" is only ever none/absent) — pass.
- `needs-a-spec-when-it-decides-a-rule` — reasoned NO: the commands implement
  existing law (cor:agt:050:02/08/09/10/20, 060:05/08/09); the party-flag
  syntax and the events order statement are CLI-surface conventions inside
  this repo, deciding nothing another repo must match.

One pre-existing flake observed under load
(TestTeamChatReadAllAttributesEveryPageWithoutEarlyMark) — passes in
isolation and in three consecutive full runs; unrelated surface, reported
here for honesty, not fixed here.

## Cycle 1 (Diego blind #9094, confirmed live by Colin's QA #9102)

Two P2s, both fixed failing-test-first:

- **P2-1 — tree alarm pagination.** The tree read one default page of alarm
  events (50, not even the cap) and joined whatever it held: an established
  team's LATER open alarms never rendered (QA: 49 shown of 205). Now paged to
  exhaustion with the `after` cursor at the 200 cap; the regression test
  stages a full first page of acknowledged alarms with the open alarm on
  page two, and pins the continuation cursor.
- **P2-2 — events `--limit`.** `--limit 0` panicked (an empty page hit the
  `len(events)-1` hint index); `--limit >200` was clamped silently
  server-side, which also suppressed the continuation hint. `--limit` now
  validates to 1–200 with a refusal naming the cap (events AND task list),
  and the hint compares against the effective size with a nonempty guard.
  The at-cap test pins a full 200-row page still naming the next cursor.

Both P3s taken too:

- **P3-1** — a worker id/URN now sets availability with no App (appRef
  omitted, matching the server's own input doc); a NAME still demands one,
  refused with the remedy. The self-sufficient spellings are the worker URN
  (`hrn:worker:` — the same prefix `resolveWorker` dispatches on) and the
  32-hex row id.
- **P3-2** — the availability tables' Flush errors propagate; a
  failing-writer test proves a mid-table broken pipe no longer reports
  success.

Diego's non-blocking note — task-create help still describing the
pre-#1792 owner rule — is a help fold for when #1792 lands, not this head.

## Cycle 2 (Xylon local seat #9250, routed by Ada #9261)

All three P2s fixed failing-test-first (red at `e35b4ed`):

- **P2-1** — the roster's null-worker entry was the diff's one unguarded
  party dereference (panic); guarded like every sibling, and the wakes
  loop nil-checks its entries like the other generated lists.
- **P2-2** — the claimed guards are pinned now: orphan rows render
  top-level, null/empty reads answer NotFound (exit 4), `create --parent`
  sends appRef nil with no ambient scope, blank ack args and multi-word
  outcomes refuse, and every scoped render (events, roster, availability)
  names its scope — not only task list's.
- **P2-3** — a cyclic or self-parented chain rendered NOTHING at exit 0.
  Empty roots now fall back to rendering every row flat, and the walk
  carries a visited set so a cycle cannot loop it either. (The healthy
  server mints no cycles; the render no longer assumes that.)

P3s taken: trimmed task refs (P3-1), the advertised outcome charset
enforced (P3-2), RFC 3339 validation for `--since`/`--until`/`--start-by`
and a non-negative `--offset` (P3-3), and a non-advancement safeguard on
the alarm paging — repeated cursor or 100 pages stops with a stderr note
(P3-4). The `--close-session=false` spelling caveat is in the help (P3-8).

Dispositions on the rest: **P3-5** — appRef rides along whenever ambient
scope resolves (the server accepts URN+appRef and cross-checks the App);
a 32-hex worker NAME is a theoretical collision with the id shape the
refusal names both spellings for — left, with the ambiguity documented
here. **P3-6** — a stale session answers SESSION_EXPIRED (exit 3,
already mapped) or the middleware's local binding checks refuse before
dispatch; SESSION_NOT_BOUND (exit 2) covers the worker-required writes.
**P3-7** — the two presentational derivations are disclosed above rather
than re-widened.

## What this does not do (and where it lives instead)

- Backlog/park verbs (`parkAgentTask`, `makeAgentTaskReady`) are still in
  review server-side (#1790); when they land they are a follow-up CLI slice
  alongside `--status BACKLOG`.
- The stall-schedule tunables (`agentTaskStallSchedule`) have no command —
  not in #830's scope; owner-facing surface can follow demand.
- `#839` (org names) waits on the urn-lib-go release per Eden (#8916).
