# `secret create|update --protected` (cli#2815)

Status: built (branch `jimmy/2815-protected-arg`). Decision (Petra, on the issue, 2026-11-21): option (a) — a `--protected`
tri-state flag on `secret create` and `secret update`, closing the coverage-matrix gaps
`Mutation.createSecret(protected)` / `Mutation.updateSecret(protected)`.

## Why

With `secret protect` as the only way in, a secret that should be protected from the start
must be created unprotected first and protected afterwards — a window in which it changes
without a passkey, and two ceremonies. `createSecret(protected: true)` stages the whole
creation in one act; `updateSecret(protected: …)` stages a protection change alongside (or
instead of) a value/metadata change.

## Server contract (read at hadron-server `04b5c66c8`, resolvers.secret.ts)

- `createSecret(protected: true)` — App scope only (`--scope app`); any other scope is a
  clean `BAD_USER_INPUT` the server words. Stages `STORE/absent` via `stageSecretChange`;
  a taken name refuses at staging with the duplicate-name refusal and nothing staged.
  `protected: false` on create is the default shape (unprotected); non-App ignores it.
- `updateSecret(protected: …)` — any explicit value on a non-App secret is `BAD_USER_INPUT`.
  On an App secret: an explicit value alone is a valid update (the all-omitted guard counts
  it); `protected: false` is manager-only (FORBIDDEN otherwise — the server judges, the CLI
  never pre-checks); `secret.protected || protected !== undefined` stages, so every explicit
  value on an App secret stages a change request (`protectedResult`, `writeVersion`).
- The staged answer is the #2738 shape: `SECRET_PROTECTED_NEEDS_PASSKEY` with
  `extensions.requestId` / `extensions.ceremonyUrl`, already mapped by `mapChange` in
  `internal/cmd/secret/staged.go` — the create/update doors ride it unchanged.

## Design

- One flag per command, `BoolVar` + `cmd.Flags().Changed("protected")` — the tri-state is
  presence-based, the repo's `Changed()` precedent (`authored-with`, channel #638):
  - omitted → pointer nil → `# @genqlient(omitempty: true)` omits the key (never `null`:
    `null` is not "preserve" here, it is a third server meaning we do not want);
  - `--protected` → `true`;
  - `--protected=false` → `false`.
- `internal/api/queries/secrets.graphql`: `$protected: Boolean` (omitempty) added to
  `CreateSecret` and `UpdateSecret`; `make generate`. `secret set` passes `nil` (it never
  touches protection).
- `update`'s "nothing to update" guard admits the flag: `--protected` alone is a change.
- Output contract: a staged create/update prints the request id and ceremony URL byte for
  byte via the existing `stagedChange` path (exit 5) and never says "created"/"updated" —
  the error return precedes `output.Write`, so no success line can print. `--json` carries
  the envelope unchanged. No `--json` DTO changes: the flag changes input only.
- Help (both commands) names the flag, its three states, that it applies to App secrets,
  and that protection changes go through a signed request. `agentic-usage.md`: the
  synopsis gains the flag; the trailing sentence "create|update do not yet reach the
  protected argument (cli#2815)" is replaced by the flag's contract; the long `secret
  update` paragraph gains the flag.

## Acceptance (red first — `internal/cmd/secret_protected_arg_2815_test.go`)

1. `secret create --protected` sends `createSecret(protected: true)` once; a staged answer
   prints request id + ceremony URL, never "created"/URN; `--json` envelope unchanged.
2. `secret update` without the flag sends NO `protected` key (request-body pin) — a plain
   member update is not silently turned manager-only.
3. `secret update --protected=false` sends `false`; a non-manager FORBIDDEN renders as the
   server returned it (exit 8).
4. `--protected` alone on update is a valid change (no "nothing to update" refusal).
5. Help names the flag, three states, signed-request wording on both commands.
6. `make coverage-matrix` moves both `protected` rows from `gap #2815` to the commands;
   `make coverage-matrix-check` green.

## Out of scope

- `secret set` protection changes (it is a value-only convenience door; `protect` and
  `update --protected` cover it).
- Any local pre-check of scope or manager status — the server judges both (Petra's
  condition; keeps one source of truth).
