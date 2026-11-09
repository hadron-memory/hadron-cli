# `chat config set` — the #477 writer cut (design as built)

Gate: CHECKED T2 + §10 (hadron-cli#477, comment 6744022764). The read-only cut
(`chat config show`, `chat doctor`) shipped in #1029; this is the separately
gated writer.

## What it does
`hadron chat config set --node REF | -m MEMORY [--messages-loc LOC]` writes
`chat.node` / `chat.memory` / `chat.messagesLoc` into the nearest
`.hadron/config.json` — the same ancestor search `chat read|post|show` use
(`findProjectChat`), so it edits the file the readers actually load.

## Decisions
- **Raw-preserving edit.** The file is parsed into an ordered object whose
  member values stay `json.RawMessage`; only the named `chat.*` keys are
  replaced. Unrelated keys (appKey and any other secret included) keep their
  bytes and order. Output is re-indented, which changes whitespace only.
- **Refuse, never repair.** Invalid JSON, non-object file, non-object `chat`
  (including `null`), duplicate keys (a decoder would silently keep the last),
  trailing content, a symlinked file, blank values, an unparseable node URN, or
  no flag: exit 2 and nothing written (no backup either).
- **Backup, atomic write.** The original is copied to `config.json.bak`
  (`config.WriteFileAtomic`) before the replace; the file's mode is kept; a new
  file is 0600.
- **No impersonation surface.** No handle/identity/role/session flag exists;
  authorship still comes from `--session`.
- **Explicit only.** Nothing writes config at bind time or from `show`/`doctor`.
  Absent file: creates `./.hadron/config.json` (the explicit verb is the
  consent).
- **Supplied-empty is refused**, not read as "clear"; there is no unset verb
  in this cut.
- `--node` is mutually exclusive with `-m`/`--messages-loc` (the readers' node
  form beats the pair, so writing both would leave a dead half).

## Not in this cut
Archived/superseded Channel state (provider gap), auto-repoint, an unset verb.
