# Design as built: dropping the `-m` indirection — flag-free worklog + `team init --app` (#399, #400)

> **Status: shipped with this PR.** Both issues were re-cut on 2026-08-15
> against the Worker model (#428) and hadron-server#965 (`App.sharedMemory`,
> merged via hadron-server#982). One PR: the two changes remove the same
> indirection from sibling commands.

## The insight both issues share

The team memory was only ever an **indirection to the App**. `session log`
resolved `-m` → `Memory.appId` → `recordTeamWork(appRef, …)`; `team init`
resolved `-m` → `Memory.appId` → `updateTeamCollections(appRef)`. The server
owns worklog placement *inside* the App's shared memory, and under the
Worker model the CLI holds the App at `session start` — it is the bound
worker's `appId`. Requiring a user to name a memory that exists solely to be
dereferenced back to an App they are already bound to was ceremony.

## #399 — the flag-free worklog

- **The binding records `appId`** (the bound worker's App) at `session
  start`. `TeamMemory` keeps being recorded when `-m` is given, but only as
  the legacy/override trail.
- **`session log` needs no flag.** Resolution precedence: explicit `-m`
  (resolved to ITS App, so a mismatch still fails honestly as
  `SESSION_NOT_IN_APP` — with the remedy now including "drop -m") → the
  binding's `appId` → a pre-#399 binding's `teamMemory` (resolved, the
  back-compat path) → the degraded diagnostics, now reachable *only* by
  bindings predating the App-recording CLI.
- **The start-time worklog warning is gone**, not reworded: the condition it
  warned about (a session whose worklog home was unset) no longer exists
  for new bindings. The #414 lineage of diagnostics survives only on the
  legacy paths, each naming its own honest remedy.
- **The provenance query** (`session list --pr|--issue|--commit|--branch`)
  gains `--app` as a first-class scope. Precedence is specificity: explicit
  `-m` → explicit `--app` flag → binding `appId` → binding `teamMemory` →
  ambient App context. `-m` outranks the flag because it is the more
  specific claim (and resolving it to its own App — never through
  `resolveTeamApp` — keeps the PR #409 fix: an ambient App context must not
  hijack an explicitly named memory).

## #400 — `team init --app`

- The App resolves like the rest of the group (`--app` / context /
  binding); `updateTeamCollections(appRef)` was always App-addressed. The
  shared `resolveTeamApp` learned the binding's `appId` (review catch: the
  initial revision left the resolver on ambient-context + `teamMemory`
  only, which would have broken the advertised flag-free flow for every
  binding-scoped command — init, chat, worker, role — whenever no ambient
  App was configured).
- **The three-value status contract** (`created`/`updated`/`unchanged`)
  needs to know whether a worklog declaration existed before the write —
  and the pre-read must describe the memory the write LANDS ON, which is
  always the App's canonical shared memory (`App.sharedMemory`, #965; a
  null `sharedMemory` IS the "fresh App, nothing declared" answer). Both
  paths read it: basing `declared` on the `-m`-named memory (the initial
  revision) lies whenever `-m` names some other app-class memory — an
  already-correct target reporting `created`, a fresh one `updated`
  (review catch). The named memory contributes only its class check and
  the where-it-landed note, and is never read back.
- `-m` survives as the explicit override with its #384 class prechecks (a
  system memory would make the collection permanently unwritable) and the
  PR #413 where-it-landed honesty note (an `-m` naming some other app-class
  memory still declares on the team memory; the note fires only on that
  path).
- **Not done: deprecating `team init`.** The re-cut's open question —
  whether provision-time convergence should retire the command — is a
  hadron-server conversation first; until then init remains the repair
  surface for memories declared by older CLIs.

## Compatibility posture

Three binding generations now exist, each handled where it is read:

1. **Current** (`workerId` + `appId`): everything flag-free.
2. **Pre-#399 worker binding** (`workerId`, no `appId`): the worklog path
   falls back to the recorded `teamMemory`; behavior identical to before.
3. **Pre-Worker binding** (neither): the degraded diagnostics, with
   end-and-restart as the remedy — unchanged from PR #431/#433.

No binding is ever migrated in place: a binding is a session-lifetime
artifact, and every session started by this CLI writes generation 1.
