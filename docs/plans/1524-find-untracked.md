# find-untracked, App properties and task externalId (#1524)

This is the design as built. It works over hadron-server#3125, sliced S1 #3131 (App.properties), S2 #3137
(AgentTask.externalId) and S3 #3150 (untracked items and claim). The approved plan is
`hrn:node:hadronmemory.com:hadron-dev-team-shared:plans:1524-find-untracked`
(draft 10); the gate is CHECKED T1. Specs: `cor:dmo:050:02`, `cor:dmo:050:20`,
`cor:agt:120:01`–`:03`.

The CLI is a thin client (D1, `cor:agt:120:03`):
- **No local policy.** Every rule, bound and authorization is the server's
  (`cor:api:240:01`). The CLI's only checks are usage shapes: a missing or
  blank argument, mutually exclusive flags, and an enum spelling.
- **One call per invocation.** Each command makes exactly one GraphQL call,
  with no read before a write and no retry.
- **Server defaults apply.** An omitted flag sends no key.
- **No MCP surface.** This follows Holger's 2026-10-31 ruling that the agent
  team gets no new MCP tools.

## Commands

| Command | Operation | Notes |
|---|---|---|
| `app properties get <app>` | `AppPropertiesGet` | Text output is the object alone, as valid JSON (`output.SafeJSON`: control and invisible runes inside strings are `\uXXXX`, so the value is unchanged). `--json` is `{appId, propertiesRevision, properties}` |
| `app properties set <app> <json\|@file\|->` | `SetAppProperties` | Replaces the whole object. `-` is stdin (a terminal is refused). `--expected-revision` is sent only when given |
| `app properties clear <app> --yes` | `SetAppProperties` | Sends an explicit null, behind `ConfirmDeletion` |
| `team task create --external-id <id>` | `AgentTaskCreate` | Sent as given. A blank value is exit 2. `serverCode` is on the envelope when the flag is named |
| `team task list --external-id <id>` | `AgentTaskListFiltered` | Exact match. Refused together with `--team` |
| `team task get` / every task `--json` | `AgentTaskFields` | `externalId` (null when none) and an `external id:` text line |
| `team task find-untracked …` | `UntrackedTaskItems` | Flags pass through. `ReadyFilter` is the spelling `only\|exclude\|any` |
| `… --assign-to-me` | `ClaimUntrackedTaskItems` | No `skip`. Prints the tasks, then applies A3 |

`cmdutil.ParseJSONValue` is the task `--properties` parser moved out of
`team`, and both commands now use it. The task messages are byte-identical.

## Exit codes

Literal rows in `codeForExtension` sit ahead of the `_NOT_INSTALLED` and
`_REQUIRED` suffix rules, which would otherwise file configuration state under
usage:

| Exit | Codes | Meaning |
|---|---|---|
| 2 | `TASK_INTEGRATION_INVALID`, `…_REPOSITORY_NOT_CONNECTED`, `…_TEAM_UNKNOWN`, `GITHUB_INVALID_ARGUMENT`, `PROPERTIES_KEY_RESERVED` | The caller fixes the value |
| 5 | `GITHUB_NOT_INSTALLED`, `GITHUB_TOKEN_SCOPE_MISMATCH`, `TASK_INTEGRATION_ORG_REQUIRED`, `APP_PROPERTIES_CONFLICT` | Configuration state, or a stale revision |
| 7 | `GITHUB_UNAVAILABLE` | Retry later |
| 8 | `GITHUB_PERMISSION_MISSING`, `GITHUB_REPOSITORY_NOT_ALLOWED`, `TASK_CLAIM_PRINCIPAL_UNSUPPORTED` | The grant or caller kind is missing |

`TASK_EXTERNAL_ID_TAKEN` (5) and `TASK_INTEGRATION_NOT_FOUND` (4) ride the
existing suffix rules.

## Incomplete results (plan A3)

How a result with `complete: false` exits depends on `incompleteReason`:
- **`claim_refused` with a `refusalCode`.** The claimed tasks print on stdout
  first (one JSON document under `--json`). The command then exits with the
  code's mapping, and `api.WithCode` puts the code on the envelope's
  `serverCode`. The stderr message names how many tasks were claimed and that
  they are yours.
- **`claim_failed`, or `claim_refused` with a null code.** The tasks print the
  same way, and the command exits 1. It is never reported as success.
- **Any other reason** (a tracker read cut short: `rate_limited`,
  `github_error`, `page_limit` or `item_truncated`). The command exits 0 with
  a one-line stderr note.

## Deviations from plan draft 10

1. **One PR rather than three (A2).** The dispatch (Marco #64064) asks for one
   PR pinned to the S3 merge SHA.
2. **`app get` stays identity-only.** Plan C2 had it show the properties, but
   `TestAppReads1057` pins the cli#1057 identity contract, and widening it
   would make every `app get` need an S1 server. `app properties get` is the
   read instead.
3. **No `--tag` on the claim.** S3's `claimUntrackedTaskItems` takes no `tags`
   argument.
4. **`claim_failed` is handled.** S3 added this incomplete reason, with a null
   `refusalCode`. It exits 1 with the tasks printed, as A3 does for a null
   code.
5. **`TASK_INTEGRATION_ORG_REQUIRED` and `PROPERTIES_KEY_RESERVED` are
   mapped.** Both are S1/S3 codes the plan predates (rulings:0071). They map to
   5 and 2.
6. **`serverCode` on `task create` only when `--external-id` is named.** This
   follows the existing model-group pattern: ordinary task-write error output
   is unchanged.

## Release

Every task read now selects `externalId`, and the new commands call S1–S3
fields. So no CLI release carries this until #3125 S1–S3 are deployed to
production; that is checked when the release is cut.
