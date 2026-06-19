# Implementation Plan: `hadron spec edit` — in-place body edit

> **Status: implemented and verified** on branch `feat/spec-edit` (not yet
> merged). Design *as built* and the review artifact for the change. Closes the
> `$EDITOR` half of item #1 of
> [hadron-cli#41](https://github.com/hadron-memory/hadron-cli/issues/41)
> ("No spec-aware edit path"). The agent-friendly half (`spec get --body-only`
> for a `… | node update --content -` round-trip) already merged in #45. Ships
> as its own PR; item #4 (`spec link`) is tracked separately under the same
> theme.

## Context

Editing a spec today drops out of the `spec` abstraction: there is no
spec-aware edit, so you reach for `hadron node update <urn> --content-file …`,
a **full-body replace**. The motivating walkthrough (the issue) reconstructed
an ~80-line body in a temp file to change four spots — a transcription slip on
that copy silently corrupts the node, and there is no before/after signal.

`spec get --body-only | node update --content -` (shipped in #45) covers the
*non-interactive* round-trip. What remained is the **interactive** affordance:
open the *current* body in `$EDITOR` so a human changes only the lines they
mean to, never retyping the rest.

## Command surface

```
hadron spec edit <citation> -m <memory> [--content -|--content-file <f>] [--dry-run]
```

- Default: open the current body in `$EDITOR` (resolved `$VISUAL` → `$EDITOR` →
  `vi`) pre-loaded via a temp `.md` file; the saved contents become the new
  body.
- `--content -` / `--content-file` replace the body non-interactively — the same
  effect as the `node update` round-trip, but spec-scoped (validates the target
  is a spec, gives the abstract-staleness reminder). Also the path when stdin is
  not a TTY.
- An **unchanged** body writes nothing (no spurious `updatedAt` bump).
- `--dry-run` prints a line-count change summary without writing.
- `--json` emits the stable `{citation,memoryId,name,changed,dryRun}` DTO.

## Design decisions

- **Pre-load the current body.** The whole point over a full-body replace: the
  editor starts from what's there, so the failure mode (retyping 80 lines)
  disappears. `fetchSpecNode` supplies the current body.
- **Content-only update.** The write is `UpsertNode{MemoryId,Loc,Name,Content}`
  — abstract, tags, and data are *omitted*, which the server reads as "preserve"
  (the same content-only shape `spec extract --strip-source` uses). The abstract
  is deliberately left to `node update --abstract-file`; `spec edit` only reminds
  you it may now be stale (spec‑032).
- **Editor seam for testability.** The launch is behind a package var
  (`editorFunc`, default `launchEditor`) swapped by `SetEditorFuncForTest`, so
  command-level tests drive the interactive path deterministically without
  spawning a process. The TTY requirement lives *inside* `launchEditor`, so
  overriding the seam in tests bypasses it cleanly; in production a non-TTY
  caller with no `--content` is told to pass `--content`/`--content-file`.
- **`$EDITOR` may carry args.** `editorArgv` splits the env value on whitespace
  (`code --wait`, `emacs -nw`) and falls back to `vi`.
- **Spec-tag guard.** Editing a non-spec node via the spec command is almost
  always a mistake; it's a `Usage` error pointing at `node update`. (`requireSpecTag`
  is not shared with `spec link` — that helper lands on a separate branch; the
  check is a one-liner here over the existing `hasTag`.)
- **No confirm prompt.** Saving in the editor *is* the confirmation (like a git
  commit message); the `--content` path is an explicit replace. `--dry-run` is
  the preview. No `--yes` needed.

## What it deliberately does not do

- It does not edit the abstract — that field has its own `--abstract-file`/`-`
  path on `node update`, and conflating the two in one editor buffer is more
  surprising than helpful.
- It does not show a full unified diff — the repo has no diff dependency and a
  line-count delta is enough signal next to the editor itself. A richer diff is
  a possible follow-up.

## Testing

- Unit (`internal/cmd/spec`): `TestEditorArgv` (VISUAL/EDITOR precedence + `vi`
  fallback, arg-carrying values), `TestLaunchEditorNonTerminal` (non-TTY →
  `Usage`), `TestCountLines`.
- Command-level (`internal/cmd`, fake GraphQL + faked editor seam): interactive
  change writes a content-only update preserving the abstract; an unchanged save
  writes nothing ("no changes"); `--content -` replaces from stdin; `--dry-run`
  makes no write; `--content` + `--content-file` is `Usage`; a non-spec target
  is `Usage`.

## Follow-ups (out of scope for this PR)

- A real before/after diff (line-count delta is the v1 signal).
- Sharing the spec-tag guard with `spec link` once both branches merge.
