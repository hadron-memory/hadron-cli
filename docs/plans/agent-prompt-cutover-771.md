# Agent prompt field cutover (#771)

Server #1438 removes `Agent.personaPrompt` and the corresponding arguments on
`createAgent` and `updateAgent`. Its migration moves the old text into the
templated `systemPrompt`. This CLI change is the matching client artifact.

## Wire and command behavior

- `GetAgent`, `Agents`, `PublicAgents`, `CreateAgent`, and `UpdateAgent` select
  only `systemPrompt` and `personaRole`; mutation documents no longer declare
  or pass `personaPrompt`. Unset update fields remain omitted. An explicit
  `--system-prompt ""` still sends the empty string to clear the prompt.
- `--system-prompt`, `--system-prompt-file`, and piped `--system-prompt -`
  preserve the exact template bytes, including `{{name}}` and `{{role}}`.
  `--persona-role` remains role metadata. The removed prompt flags stay
  parseable, hidden from help, only to return a local exit-2 error naming the
  replacement before credentials or a network request are touched.
- Agent `--json` drops the old `personaPrompt` key. Keeping it as an invented
  null would imply a server response the new schema cannot give. The existing
  `systemPrompt` key now carries the one shared Agent/Worker template. Human
  `agent get` labels the template as the system prompt.
- Worker and session operation selections and file writes are unchanged.

## Schema source and verification

The schema snapshot and generated client were refreshed from the rebased
server #1438 head `31a50294658938b85b15a23e37dc562a6d83d057`, which
includes current server main. The Agent GraphQL document was separately
validated by GraphQL's validator against that **exact** SDL, with zero errors
for all five affected operations (and `DeleteAgent`). Re-run this source check
if #1438 changes before merge.

Command tests cover each Agent operation, old-flag pre-request refusal,
template byte preservation through file/stdin, omitted versus explicit-empty
update, and the JSON key disposition. The existing Worker/session command
tests provide synthetic smoke; production cutover and binary installation
are coordinated separately.
