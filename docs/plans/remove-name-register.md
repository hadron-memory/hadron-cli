# Design as built: removing the worker name register (#496)

Design-as-built for hadron-cli#496, following hadron-server#1050
([PR #1059](https://github.com/hadron-memory/hadron-server/pull/1059), `21fec53`).

## What happened

The server deleted the worker name register. Not deprecated, not replaced —
`cor:agt:020:07` is **WITHDRAWN**, and nothing takes its place. The CLI had a
large surface built over it, and that surface was targeting arguments and
fields the server no longer has: `main` was broken against the live server, not
merely stale.

The register was an ordered cast-list on each `roles:<role>` node
(`data.names`), plus an initial-letter range and convention prose. A bare
`worker cast --role backend-engineer` walked it server-side past taken names
and allocated the first free one.

## The sentence that decides how to read this change

> `cor:agt:020:02` was amended to v0.0.3 and **no durable clause changed.**

A worker's name is permanent within its App. That was true with the register
and is true without it, because permanence is enforced against the **roster**,
not against any ledger of allocations. The register governed ALLOCATION; the
roster governs IDENTITY. Removing it could not weaken the guarantee, and the
unchanged spec clause is the proof rather than an assurance.

Every removal below follows from that one fact, which is why this is a deletion
and not a migration.

## Removed

**`worker cast`**

- `--team-agent` — casting reads no system memory at all now, so the argument
  had already become accepted-and-ignored server-side.
- The nameless cast. `--name` is now **required**.
- `--json`: `teamAgentId` / `teamAgentName` from the `--dry-run` preview.

**`team role`**

- The entire `names` subcommand group (`set`, `add`, `rm`, `mv`) and the
  `expectedNames` compare-and-swap that made the sugar safe (#436).
- `create`: `--names`, `--name-range`, `--name-convention`,
  `--allow-out-of-range`. What remains is `--description`.
- `update`: `--name-range`, `--clear-name-range`, `--name-convention`,
  `--clear-name-convention`. What remains is `--description`, which is now
  required — an update naming no field is refused rather than sent, since
  omitted is "preserve" and the write would be a no-op reporting success.
- `rm`: `--transfer-register-to`, and the minted-name gate that made it
  necessary. Retirement is unconditional.
- `list`: the REGISTER / FREE / RANGE columns.
- `get`: the register block with holders, and the conventions line.
- `--json`: `register`, `freeCount`, `exhausted`, `nameRange`,
  `nameConvention` from the role shape; `transferredNames`, `transferredTo`
  from the `rm` payload.

**`internal/api`**

- `TeamRoleStaleNames` — the CAS rebase helper, dead with the sugar.
- Error-code cases for `TEAM_ROLE_NAME_MINTED`, `TEAM_ROLE_NAME_DUPLICATE`,
  `TEAM_ROLE_NAME_OUT_OF_RANGE`, `TEAM_ROLE_STALE`, `TEAM_ROLE_IN_USE`. The
  server cannot produce them, and a case for an unreachable code documents an
  exit code no caller can exercise.

## Added

One suffix rule: `_REQUIRED` → exit 2 (Usage), covering the server's
`WORKER_NAME_REQUIRED`.

## Decisions worth the words

### The `--json` keys are dropped, not emptied

Dropping keys from a `--json` shape is a breaking change to a documented
contract, and this repo treats those shapes as load-bearing. The alternative
was keeping `register: []`, `freeCount: 0`, `nameRange: null` forever.

Rejected: a permanent empty register **keeps promising an allocation surface**.
An agent reading `freeCount: 0` concludes the register is exhausted and looks
for the flag to extend it. A reader seeing the key absent learns the true thing
immediately. A contract that lies quietly is worse than one that breaks loudly,
especially while the surface is this young.

### `worker cast` refuses a nameless cast locally

The thin-CLI directive says: map the server's typed error to an exit code and
surface the message verbatim. `WORKER_NAME_REQUIRED` carries the *reason* — a
name is permanent, so it is chosen — but not the *remedy*.

This one gets a local refusal anyway, because of who hits it. `cast --role <r>`
with no name was the NORMAL way to cast: it is what every task node, every doc,
and every operator's muscle memory still says. Someone reaching this refusal is
following instructions that were correct last week. The message has to say what
to type now, and point at `worker list` for what is already taken.

That is a deliberate exception, noted at the call site so the next reader does
not "fix" it back into a passthrough.

### `--role` is no longer validated

An unrecognized role now simply resolves no agent (`WORKER_AGENT_NOT_FOUND`),
and with an explicit `--agent` it is just the casting's label. This is
intended, not an oversight: `cor:agt:020:00` §1 makes cast lists ergonomics and
never a gate. Worth stating loudly because a role typo used to be caught and
now is not.

### The superseded plan docs keep their content

`team-role-register-writes.md`, `team-role-names-sugar-cas.md` and
`team-role-retirement.md` describe a surface that no longer exists. They carry
a SUPERSEDED banner rather than being deleted or rewritten: they remain the
fastest way to understand what the register was for, and — for
`team-role-names-sugar-cas.md` especially — why a read-modify-write over a
wholesale server field needs a precondition. That reasoning outlived its
subject and is still cited by
`hadron-cli:review:no-rmw-sugar-over-wholesale-writes`.

## Schema refresh, and a trap in it

`make schema` exports the SDL from the sibling `../hadron-server` checkout. On
this machine that checkout was on another engineer's unmerged feature branch,
so a plain `make schema` would have baked **unreleased server fields** into our
committed snapshot — silently, since the generated client would have compiled
fine.

The export was taken from a throwaway `git worktree` of `origin/main` instead,
with `HADRON_SERVER_DIR` and `SDL_EXPORT` overridden (both already exist for
exactly this reason — CI uses them to export without a full server install).

Worth knowing generally: **`make schema` is only as correct as whatever branch
the sibling checkout happens to be on**, and it will not tell you. `make
schema-check` inherits the same blind spot.

## Not done here

- **The task nodes.** `tasks:start-worker-session-cli` and
  `tasks:start-worker-session-desktop` tell a reader facing a taken worker to
  `hadron team worker cast --app <app> --role cli-engineer` — a command that
  now fails, and the documented remedy for hadron-cli#492. @Ada owns those
  nodes; the rewrite wants to land close to this.
- **hadron-cli#492** — its premise ("the register usually holds a free name
  that is the right answer") no longer describes anything. The underlying
  complaint was answered by the holding model instead: `WORKER_HELD` and
  `WORKER_TAKEN` are distinct refusals and `--force` cannot bypass a hold.
  Closeable with a note.
