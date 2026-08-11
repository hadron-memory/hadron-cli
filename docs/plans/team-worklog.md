# Implementation Plan: the team worklog — `team init`, milestone logging, and the provenance query (#369)

> **Status: implemented** — design as built. The worklog slice of
> [#369](https://github.com/hadron-memory/hadron-cli/issues/369), following
> [team-personas-sessions.md](team-personas-sessions.md) (slice 1, PR #370) and
> the `updateSession` wiring (PR #371). Design: D8/D13/D14 in hadron-concept
> `design-discussions/platform-apps/2026-08-11-agent-personas/`. No new server
> surface — everything rides the existing structured-storage stack
> (`Memory.schema` #725, the object store #745/#747).

## What this slice answers

The original #368 pain: *find the session behind this PR*. The worklog is the
N:M join that answers it — an append-only, schema'd object-store collection of
external-artifact milestones in the team App memory, where the **canonical
normalized ref string is the equality-lookup key**. `Session.prNumber` remains
a display convenience and is never consulted by the provenance query.

## Command surface

```
hadron team init -m <team-memory>
hadron team session start --as <p> [-m <team-memory>] ...      (worklog home → binding)
hadron team session log (--pr | --issue | --commit) <ref> [--action <a>] [--detail <json>] [-m <team-memory>]
hadron team session list --pr <ref> [-m <team-memory>]          (THE provenance query)
```

## Key mechanics as built

### The worklog collection (`team init`)

`team init -m <memory>` merges the canonical `worklog` collection into the
memory's property schema (`Memory.schema.objectTypes`, #725) — read, converge,
`updateMemory(schema:)`. Idempotent (semantically-equal definitions compare
equal via key-sorted re-marshal; an unchanged schema makes no write) and
non-clobbering (other collections are preserved). Fields per D13/D14, all
required so provenance rows need no joins: `sessionId`, `personaName`, `tool`,
`kind` (enum `pr|issue|commit|branch`), `ref`, `action`, `at` (datetime).
`detail` stays *undeclared* — the schema field-type vocabulary has no JSON
type, and `strict` defaults to false, so the optional display-extras bag rides
along unvalidated by design. The group-chat `message` collection is
deliberately **not** declared here: its shape must stay coordinated with the
hadron-client watcher dialect and lands with the `team chat` slice.

### Ref normalization (`refnorm.go`)

One pure function with a table test (D14): `normalizeArtifactRef(kind, raw,
defaultRepo)` → canonical string + PR/issue number. Canonical forms
`owner/repo#N` (PRs and issues share GitHub's number space; `kind`
distinguishes) and `owner/repo@sha` (lowercased, 7–40 hex). Accepted: the
canonical form, GitHub URLs (`/pull/`, `/issues/`, `/commit/`, tails
tolerated), and bare numbers/shas — qualified by `defaultRepo`, resolved from
the binding's recorded `--repo`, else the worktree's github origin remote
(`repoFromRemote`, suppressed under the `HADRON_TEAM_GIT_DIR` test override
like `gitDir`'s). Malformed input errors loudly naming the accepted forms —
never a silent pass-through, since a non-canonical stored ref would miss every
future lookup.

### The binding carries the worklog inputs

`session start` gained `-m/--memory` (the team memory) and now records `teamMemory`,
`tool`, and `repo` in the worktree binding — so `session log` needs no
per-call plumbing: `sessionId`/`personaName`/`tool` come from the binding,
bare refs qualify against its `repo`, and the worklog home is its
`teamMemory` (`-m` overrides per call).

### `session log`

Exactly one of `--pr`/`--issue`/`--commit` (cobra flag groups). Writes, in
order: `updateSession(prNumber)` for PRs (the #932 denormalization + liveness
touch), then `createObject(teamMemory, "worklog", fields)`. `--json`'s
`recorded` field says where the milestone durably landed: `"worklog"`
(normal), `"session"` (PR without a configured team memory — degraded, with a
stderr note), and an issue/commit milestone **refuses** without a team memory
(nothing durable would happen). The local `prNumbers` history keeps feeding
`whoami`; a failed local append after successful server writes degrades to a
note (tested via the symlink trick).

### `session list --pr <ref>` — the provenance query

`findObjects(memory, "worklog", match: {ref: <canonical>, kind: "pr"})` —
kind is part of the match because PRs and issues share GitHub's number space
— paged to exhaustion (the issue-#23 rule: `--pr` promises the *complete*
provenance set) → deduped `sessionId`s → `session(id)` each → rows with
persona names joined from the roster. Bare `--pr 371` qualifies through the
same `defaultRepo` fallback as `session log` (binding repo, then the github
origin remote), so the documented unbound-checkout form works. Several rows per PR are expected and desirable (a PR spanning three
sessions yields three transcripts — the table surfaces `transcriptPath`,
`host`, `tool`, `llmModel`). A recorded session the caller cannot read lists
as an **id-only stub** with a stderr note, per the nodes-list visibility-gap
rule (client-side fan-outs must surface unreadable ids, not drop them).
`--pr` does not combine with `--active`/`--as`/`--repo` (usage error).

## Out of scope (remaining #369 work)

`team chat` (+ the message collection in `team init`, coordinated with
hadron-client #11/#12), `--branch` milestones, richer provenance filters
(`session list --issue/--commit` generalization), and the role-template/name
register machinery for `persona create` (D-2026-08-11-007).
