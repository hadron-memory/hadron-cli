# Host join, leave and the host credential store (cli#3239), as built

Slice C1 of hadron-server#5209 ("Host file removal") over hadron-server#5259
(V1, merged as #5313 at `b8fba75a`, first in server 0.47.0). The gate is
CHECKED T2 (Xenophon, cli#3239 comment 7206794117); its rulings RC1 to RC5 bind
what is below. The issue body's plan is the design; this records what was
built and where it differs.

## The store: `internal/hostcred`

- `<config dir>/host-credentials.json`, `{"version":1,"entries":[{server,
  appId, hostId, key}]}`. RC1 = B (Holger, 2026-11-26): the ids are identity
  written from the server's answer, never a setting and never a user's input.
  Entries match by `auth.Host(server)` and the App id, as tokens match.
- Reads (`Load`, and `Lock`, which reads under the lock) open with
  `O_NOFOLLOW` and `fstat` the opened file, so the mode check runs on the file
  that is read; refuse a group/world bit on the file (`chmod 600`) or on the
  config directory (`chmod 700`: `MkdirAll` never tightens an existing one), a
  non-regular file, more than 1 MiB, malformed JSON, an unknown field
  (`DisallowUnknownFields`), data after the document, an unknown version, an
  entry that is not whole, and a repeated server+App. Every refusal is a
  `hostcred.Error`: the path and a fixed reason, never content (a decoder's
  message can quote the key).
- Writes go through `secretfile.Open(path, true)` + `Commit` (a temp file and a
  rename; 0600; an owner-only DACL on Windows), refusing a symlink at the path.
  An empty store deletes the file. A temp file left behind is named, since it
  may hold key material.
- Every read-modify-write holds `<config dir>/host-credentials.lock`
  (gofrs/flock), its own lock rather than `config.WithLock`, so a join's
  network call never holds up `hadron auth`. `join` and `leave` hold it across
  their server call, so two joins on one machine cannot both mint.
- RC5: built on every OS. On Windows `tooOpen` is false (a Go mode reflects
  only the read-only bit), a reparse point is refused before the open, and the
  DACL is pinned by `dacl_windows_test.go`, run in CI's `windows-secretfile`
  job beside secretfile's own.
- `Keys(entries)` is the hook for C3 to scrub every stored key from a child
  environment. The package imports nothing of `hostpoller`.

`config.StateDir` / `HostStateDir` / `EnsureHostStateDir`: `$XDG_STATE_HOME/
hadron` else `~/.local/state/hadron`, `hosts/<hostId>` at 0700, the id checked
against `internal/hostid` (shared with `host_setup.go`), and a `:` refused on
Windows. C1 creates nothing under it.

The rule itself is `config.StateDirFrom(getenv, home)`, and C2's
`hostfacts.Facts.StateDir` (#3236, merged first) calls it through its
`Getenv`/`Home` seams, so `team host doctor` reports the directory the store
writes to (pinned by `internal/hostfacts/statedir_shared_3239_test.go`). The
switch runs this way round because `hostfacts` already imports `config`
(through `orcaterm`), so `config` cannot import `hostfacts`.

## The commands: `internal/cmd/team/host_join.go`

Every request is the person's, under `api.WithoutSession` (join and remove are
person-only on the server; a bound worktree would otherwise risk a session
header), and never carries the stored key, except leave's dead-key check.
No server or transport text is echoed: a failure is its exit code plus fixed
words (`failureClassHost`), as `app key create` does.

- **join**: flags checked first (label 1-128, host id), then the App id
  (`TeamAppIdentity`; the join payload carries none), then the lock. A held
  entry without `--replace` exits 5 before the join request (after the App lookup); `--replace` with a
  different `--host-id` exits 5 (leave first); `--replace` alone sends the held
  host id. One `JoinAgentTeamHost`, never retried. A transport or internal
  failure exits with its code (7/1) and says a key may exist: run
  `join --replace --host-id <id>` (or with the same `--label` when no id was
  given), which revokes it. The recovery names the host because the machine
  now holds nothing, so a bare `--replace` would re-key whichever host the
  server derives (`rejoinHint`). A store write failing after the mint exits 1
  with the same route after "fix that"; an answer the store refuses as not a
  whole entry (a host id or key this version cannot store) says a newer hadron
  comes first, because the same join would get the same answer (blind review of
  #3283, P2 2). `--json {server, appId, hostId, label, replaced,
  next[]}`, no key field.
- **leave**: the entry must be held (else 4, no remove request); `ConfirmDeletion`
  (prompt, or `--yes`). Server first: `RemoveAgentTeamHost`, no force (RC4).
  - `HOST_NOT_FOUND`: RC3's dead-key guard. The stored key is presented once
    to `authenticationStatus` (a client built with the key, recorded as
    `CredentialHostKey`); only `REFUSED` deletes the entry, exit 0
    (`removedOnServer: false`). Anything else keeps it, exit 4.
  - `AGENT_TEAM_HOST_HAS_OPEN_SESSIONS`: exit 5, naming (best effort, from
    `HostDesiredState`) the workers with an open session; the refusal carries
    no names.
  - Any other refusal or a lost answer: the entry stays byte-identical, exit
    with the mapped code.
  - Confirmed removal, then the local delete; if that fails, exit 1: the key
    is already revoked and a rerun goes through the dead-key path.
- **set**: `gqltypes.AgentTeamHostUpdateInput` (bound in genqlient.yaml) with a
  custom `MarshalJSON`: a flag left out is omitted, `--default-*` sends null.
  Contradicting flags, an interval outside 1-86400 or no flag exit 2 before any
  request. `--instruction` as for `assign` (the update is the App manager's
  write; join/remove are person-only, so they take none).
- **hosts** / **show** (RC2 = a): new verbs; `list`/`ls` is byte-unchanged.
  DTO `{id, hostId, label, pollIntervalSeconds, deliveryEnabled, createdAt,
  updatedAt}`; `hosts` is a bare array, `[]` when empty.

The documents are `internal/api/queries/agent_team_hosts.graphql`, used by no
other command, so an older server fails only these (`IsFrameworkValidationOnly
For`, exit 1 naming server 0.47.0). Error codes: `AGENT_TEAM_HOST_EXISTS` and
`AGENT_TEAM_HOST_HAS_OPEN_SESSIONS` 5, `AGENT_TEAM_HOST_NOT_PERMITTED` 8,
`BAD_AGENT_TEAM_HOST_ARGS` 2; `FORBIDDEN` 8 and `HOST_NOT_FOUND` 4 as before.

## Differences from the issue's plan

- The "next two commands" are `team host show <host>` and `team host assign
  <worker> --host <host>`: `host doctor` (C2) does not exist and `host run`
  does not read the store until C3; those slices change the list.
- `leave` has no `--instruction`: removal is person-only on the server.
- `removeAgentTeamHost(input.force)` is a coverage-matrix gap (RC4), tracked
  by cli#3282.

## Tests

`internal/cmd/team_host_join_3239_test.go` (a fake server recording each
request's operation, Authorization, `X-Hadron-Session` and variables; every
refusal quotes the key, and every test asserts the key is absent from stdout,
stderr and the error), `internal/hostcred/hostcred_test.go`,
`internal/config/paths_state_3239_test.go`, `internal/hostcred/dacl_windows_test.go`.
