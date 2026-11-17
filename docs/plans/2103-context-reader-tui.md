# Context reader follows the terminal interface

The worker's model family identifies the provider; it does not determine the
transcript format. A GLM or Kimi model can run through Claude Code. The health
sweep now uses the existing `Config.Recipient` TUI choice: explicit `tui` first,
else a family with a native Claude/Codex interface. Unsupported interfaces yield
no measurement. Family/provider identity is never rewritten.

The selected reader still uses that worker's configured `claude_config_dir` or
`codex_home`, with the existing environment/home fallback. The Claude reader
requires the record's cwd to match the configured worktree; it reads only usage
numbers and timestamp into its typed projection. Existing plausibility checks,
both-or-neither serialization and old-server retry remain. Discovery caching is
unchanged from #2095. No production config or activation is part of this change.

The fixture `internal/cmd/team/testdata/glm-context-2103.jsonl` is a numeric-only
projection of a real orca1 `claude-glm` / `glm-5.3` record observed on 2026-11-15:
input 4,831 + cache-read 12,160 + cache-creation 0 = 16,991, recorded at
2026-11-14T20:51:22.382Z. Cwd is replaced; content, ids and other fields are
omitted. It demonstrates Claude-format GLM usage, not a general claim about all
provider launchers. Kimi is measured only with an explicitly supported TUI and
attributable compatible records, not from its provider name alone.

Red-first tests pin explicit TUI precedence in both directions, GLM/Kimi with
Claude TUI, native-family fallback, custom providers without TUI, unsupported
TUI, per-worker account roots, unchanged family identity and command wire output.
Existing context tests pin Codex reads, wrong cwd, numbers-only output and
both-or-neither on failure. QA independently runs the compiled sweep using the
sanitized real GLM numbers. No platform schema or cross-repo rule changes;
this is client-side reader selection under existing host configuration.
