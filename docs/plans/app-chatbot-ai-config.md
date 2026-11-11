# App chatbot AI config (cli#1913)

Design as built. Provider: hadron-server#3957 (merged, PR #3960 at `28e2e9ef`); law:
`cor:dmo:050:10` (the App's id pin) and `cor:dmo:050:12` (who manages the App).
The snapshot was refreshed to exactly that revision.

## Surface

- `app get` gains `chatbotAiConfigId`, `chatbotAiConfigName`, `chatbotAiConfigListed` (additive).
- `app chatbot-ai-config get|list|set|clear <app-ref>`.

## Decisions

- **The server returns only the id** (`App.chatbotAiConfigId`). The name is taken from the
  narrow `chatAiConfigChoices` list (the App's own enabled configs plus a `default`
  marker), never from the inherited `ai-config` walk. A pin the list does not carry
  (a stored disabled config is skipped until enabled) is shown by id, name null,
  `chatbotAiConfigListed` false, and never called in use.
- **Own operations.** The pin read, the choices read and the set/clear mutation are
  separate operations, so a server that predates #3957 still serves `app get`
  (only a validation error on the field reads as capability absence).
- **`set <name-or-id>`** matches an exact id, else an exact name, among the choices
  only (an ambiguous name is a usage error listing ids). An unmatched value is sent as
  given; the server answers a missing, foreign or deleted config alike, and an own
  *disabled* config can be stored by id. `default` is a marker, not a config
  (usage error, no mutation); an empty value is a usage error before any request.
  If the choices read fails, set says so on stderr and sends the value as given.
- **`clear`** sends an explicit `configId: null` (the operation is not omitempty) and
  removes only the selection. No confirmation: nothing is deleted.
- **Set and clear cannot be combined** by construction (separate subcommands), which
  replaces the proposed `--chatbot-ai-config` / `--clear-chatbot-ai-config` flag pair.
- **No client authority check.** Manager authority (platform admin included) is the
  server's; refusals are surfaced as returned. One mutation, never retried.
- **No key material.** Neither the choices nor the pin operation selects a key, preview,
  params or endpoint.

## Not in scope

Per-chat choice (`Chat.aiConfigChoice`, `resolveAIConfig(chatId)`), pricing, Kimi
endpoints, MCP. These server fields stay unclassified gaps only if the matrix check
flags them; it did not.

## Tests

`internal/cmd/app_chatbot_ai_config_1913_test.go` (client pins against a fake server;
authority and refusals are the server's and are not proven by a stub).
`app_reads_1057_test.go` is updated deliberately: `app get` now makes one extra bounded
read for the pin (none for `app key ls`), and its JSON gains the three fields.
