# Agent and App model groups, S1 (#1485 item 1)

Six commands cover hadron-server #2831 S1 (cor:agt:060:12, merged 2c6962c0):

| Command | Operation | JSON |
|---|---|---|
| `agent model-group list <agent>` | `agentModelGroups` | `[{name, patterns, updatedAt}]` |
| `agent model-group set <agent> <name> [--pattern <glob>]...` | `setAgentModelGroup` | `{name, patterns, updatedAt}` |
| `agent model-group delete <agent> <name> [--yes]` | `deleteAgentModelGroup` | `{agent, name, deleted}` |
| `app model-group list <app-ref>` | `effectiveModelGroups` | `[{name, patterns, origin}]` |
| `app model-group set <app-ref> <name> [--pattern <glob>]...` | `setAppModelGroup` | `{name, patterns, updatedAt}` |
| `app model-group clear <app-ref> <name> [--yes]` | `deleteAppModelGroup` | `{app, name, cleared}` |

The schema snapshot is refreshed from hadron-server `c86d4c9ef`, which carries
all six operations. Under cor:api:240:01 the CLI makes none of these decisions.

## Design as built

- **Pass-through.** The CLI sends names and patterns exactly as given. It does
  no lowercasing and checks no name grammar or limit, because the server owns
  all three (Vera FIT 6552186934, note 3). The output is the stored value the
  server returns. Agent refs are trimmed and sent the way the other `agent`
  verbs send them. App refs go through `requiredReadAppRef`, like the other
  `app` reads.
- **Whole-list replace.** `--pattern` is a `StringArray`, so `a,b` stays one
  glob. With no `--pattern`, set sends `patterns: []` on both the Agent and the
  App side. genqlient marshals a nil slice as `null`, so each command copies
  the patterns into a non-nil slice before it sends them.
- **Origin labels** are display formatting. The mapping is `AGENT` → `agent`,
  `APP_OVERRIDE` → `app-override` and `APP` → `app-only`. A value the CLI does
  not know is printed as the server sent it, never guessed into one of the
  three. The mapping is documented in agentic-usage.
- **Agent-only clear.** `app model-group clear` always calls
  `deleteAppModelGroup`. For a name that only the Agent declares, the server
  answers `MODEL_GROUP_NOT_FOUND`. That answer goes through
  `MapErrorKeepingCode` unchanged and exits 4. The CLI reads no effective list
  first and retries no write.
- **Confirmation.** `delete` and `clear` remove stored data, so they use
  `cmdutil.ConfirmDeletion`. They prompt on a TTY. Without one they need
  `--yes`, and without it they exit 2 and send nothing. This matches the
  group's other removals.
- **No worker session.** None of the six operations takes a `sessionRef`, and
  the commands do not attach the worker-session header. A manager acts as
  themselves, as with `agent set-boot-prompt`. The worker-session refusal V19
  (hadron-server#3030) is the server's to apply.
- **Help** says a group is a matching input only, never an authorization input.
  It does not claim that tasks match on groups yet; that is S2.

## Tests

The red-first commit fails on main because the commands are absent. The pins
cover:

- literal repeated patterns, the comma kept inside one glob, and the name sent
  unlowercased;
- an explicit `[]` on both set commands;
- all three origins, in JSON and in human output;
- `[]` for empty lists;
- no origin on Agent rows;
- an Agent-only clear: exit 4, the message verbatim, one call, no pre-read;
- refusal mapping and no retry for `FORBIDDEN`, `AGENT_NOT_FOUND`,
  `APP_MANAGER_REQUIRED`, `APP_NOT_FOUND` and `BAD_USER_INPUT`;
- confirmation, where nothing is sent without `--yes`;
- the matching-only help.

Nine production mutants are killed: dropped patterns, a nil list on either
side, a refusal replaced on clear or on set, swapped origins, a lowercased
name, and confirmation skipped on either side.

## Out of scope

S2 (`--model-group` task filters) and S3 (worker groups in the roster) each
need their own gate after their server slice. Refs #1485 leaves both open.
