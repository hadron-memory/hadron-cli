# entity refs gaps 2 and 3: the CLI follows the server's *Ref names

Unreleased (cli#3124, the companion of hadron-server#5080 and #5081, hard
renames with no alias: cor:api:140:01, ruling 0274).

**Minimum server version: 0.41.0** (`HADRON_SERVER_VERSION`; hadron-server#5081
ships in 0.40.0 and #5080 in 0.41.0, and this CLI needs both, so a server at or
above 0.41.0). If the two server PRs merge in the other order, the second one
takes the next minor and this line must follow it before the release.
This CLI works only with
a server at or above it, and a CLI older than this release is refused on the
affected commands once that server deploys.

## What changed on the wire

Only GraphQL names changed. **Every flag, every `--json` shape and every file
format is unchanged,** except the one batch-file key below.

- **21 organization arguments are `orgRef`** (were `orgId`, and `org` on the
  three boot-prompt signing operations): `findNodes`, `globalSearch`, `edges`,
  `impersonationSessions`, `memories`, `exchangeConnections`,
  `exchangeConnectionsPage`, `slackConnections`, `telegramConnections`,
  `mcpServers`, `homeAssistantInstances`, `resendSenders`, `apps`, `agents`,
  `scopes`, `channels`, `registerEntries`, `assets`, `orgBootPromptSigning`,
  `orgBootPromptSigningPreview` and `setOrgBootPromptSigning`.
- **Input-type fields that name an entity take `<entity>Ref`:** `memoryRefs` on
  the node filter, `memoryRef`/`sourceRef`/`targetRef` on the edge filter,
  `agentRef` on the app filter, `ownerRef` on the AI-config filter and on
  `createAiServiceConfig`, `memoryRef` on node create/import and asset upload,
  `ref`/`memoryRef` on node update, `targetRef` on an edge in a node write,
  `nodeRefs`/`memoryRefs` on replace, `parentTaskRef` on task create and clone,
  `channelRefs` on a ticker, and `channelRef` on a chat acknowledgement.

## The one input-file change

`hadron team task create --batch-file` passes each item's `task` object by its
API names, so **a batch file that says `"parentTaskId"` is now refused before
any call (exit 2)**, with a message naming the new key:

```
parsing --batch-file: task 1: "parentTaskId" was renamed to "parentTaskRef"; use "parentTaskRef" in the task object
```

Rename the key to `parentTaskRef` in the file. There is no alias.

## Unchanged on purpose

`team task clone --no-parent` still sends an explicit `null` (now under
`parentTaskRef`), and `--parent` still sends the value. The `--no-parent` help
now names `parentTaskRef`.

## Ships with

The same CLI release as hadron-cli#2897 (the agent-task list arguments) and
hadron-cli#3081 (S6), so an installed CLI breaks once.
