# Holder-only model report (hadron-cli#995, server#2252 slice B1)

Design as built. Gate: CHECKED T2 + integrity/security (Xylon, 6355920556) on the attach (6355595374) plus the association delta (6355788582).

## What ships
- `internal/modelreport`: reads a native session's model from its transcript. `team session sync-model [--hook]` (hook entry) and `team session set-model <model>` report it through the existing `updateSession(llmModel)`. One optional `$llmModel` variable on `UpdateTeamSession` (omitempty; `session log` passes nil, so its wire is unchanged).

## Association (the one canonical rule)
The bound session's server-recorded `Session.transcriptPath` is the only transcript ever read. The hook's `transcript_path`, `session_id` and `cwd` are claims verified against it, never an association source:
1. recorded path present and under the tool's transcript roots (`$CLAUDE_CONFIG_DIR|~/.claude/projects`, `$CODEX_HOME|~/.codex/sessions`);
2. hook path canonicalises to the same file;
3. the file's own header carries the hook's native session id (Claude/GLM `sessionId`, Codex `session_meta.payload.id`; Codex also `payload.cwd` == hook cwd);
4. the hook cwd is the worktree (git top level of the process cwd) or inside it; a cwd above it, including `/`, is refused.
Any gap is "unavailable": nothing is sent. No "newest file" selection, no adoption.

## File safety
Canonical path is `Lstat`ed (regular file) and its FileInfo kept; the file is opened `O_NOFOLLOW` and the **opened fd's** FileInfo must be `os.SameFile` as the pre-open one (a pathname swap between canonicalisation and open is rejected; `O_NOFOLLOW` alone only covers the final component). Reads use the fd only. After reading: fd size must not have shrunk, the recorded path must still canonicalise to the same file and still be the same file as the fd. Budgets: header 64 KiB, tail 4 MiB, record 1 MiB, model 128 bytes printable. Over budget (including an oversized unterminated final record, which is never skipped to an older model), malformed, rotated or truncated: unavailable. The open is also `O_NONBLOCK`, so a FIFO swapped in cannot hang a hook.

## Write
`set-model` re-reads the binding after its ended-session check too and refuses (exit 5, nothing sent) if it changed. Binding is re-read right before the single `UpdateTeamSession(id, llmModel)`; changed/removed binding discards the observation (no retry against either session). This narrows the race; it is not an atomic transaction. `family` is never sent (server-owned). Last-arrival-wins is accepted and disclosed in help; no monotonic/latest-observation claim.

## Modes
`sync-model` refuses a terminal stdin (usage error) rather than wait on it. `--hook` is explicit (never inferred from a TTY): stdout empty (a hook's stdout can reach the agent), exit 0 on unavailable or any failure, one fixed sanitized stderr line. Without `--hook`, unavailable exits 11 (command-specific, like `chat check`'s 9/10); server/auth failures keep their usual codes. Existing commands' exit codes and `--json` are unchanged.

## Not shipped
No hook installation or fleet adoption, no host sweep, no `modelReportVersion`, no tier claim, no Codex hook-wiring claim: the Codex adapter is a parser tested on synthetic `session_meta`/`turn_context` fixtures, separate from native hook invocation. The hook snippet below is documentation for the operator:

```json
{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"hadron team session sync-model --hook"}]}]}}
```
