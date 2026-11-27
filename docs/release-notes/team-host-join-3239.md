# `team host join|leave|set|hosts|show` and the host credential store

Unreleased (cli#3239, slice C1 of hadron-server#5209 "Host file removal", over
hadron-server#5259 V1, merged as #5313 at `b8fba75a`; specs cor:agt:070:01,
:04, :07). A person makes this machine a host of a team App with one command,
and the CLI, not a person, keeps its key.

| Command | Behaviour |
|---|---|
| `team host join [--label] [--host-id] [--replace]` | one `joinAgentTeamHost` as the signed-in person (never a worker session, never retried); stores `{server, appId, hostId, key}` in `<config dir>/host-credentials.json` (0600 in a 0700 dir; an owner-only access list on Windows); prints the host id and the next commands; **never prints the key**. A machine that already holds a host for the App exits 5 before any request; `--replace` re-keys that host |
| `team host leave [--yes]` | `removeAgentTeamHost` first, then deletes the stored entry; a refusal or a lost answer keeps the entry and exits with its code (open sessions: 5, naming the workers). When the server no longer has the host, the stored key is presented once: only a key the server refuses is deleted |
| `team host set <host> …` | `updateAgentTeamHost`: label, check-in interval, wake delivery; a flag left out keeps the value, `--default-*` clears it to the default |
| `team host hosts` / `show <host>` | the platform's record of the App's hosts (`agentTeamHosts` / `agentTeamHost`). `team host list` is unchanged: it lists registrations |

Nothing reads the store yet: `team host run` still reads host.yaml and its key
file until C3 (cli#3237).

## Server floor

These five commands need **hadron-server 0.47.0** or later (the first release
carrying V1, `b8fba75a`); on an older server each is refused by name, exit 1.
Their documents are in their own file, so no other command changes its floor.
This branch first exported the snapshot at `b8fba75a6971e51369eb041d342931ee4bb58102`; the snapshot that ships is main's, exported at `da351eda5b49a00edef8383925c173df52374f62` (hadron-cli#3273), which carries V1 unchanged.
