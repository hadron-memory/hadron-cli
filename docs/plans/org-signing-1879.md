# `hadron org signing` — design as built (hadron-cli#1879)

Client half of hadron-server#2973 PR 2 (merged in hadron-server#3894). Refs
hadron-server#2973, which stays open for PR 3. Schema snapshot: hadron-server
`6c4adcc4e` (alpha 0.0.1945, carries #3894).

## Surface

`hadron org signing get|preview|on|off <org>` (`internal/cmd/org/signing.go`).
Three separate GraphQL documents in `internal/api/queries/org_boot_prompt_signing.graphql`
so an older server rejects only these verbs (schema skew, exit 2), never an
existing command.

## The security promise, and where it lives

"No surface can switch on blind" (cor:agt:040:09):

- `on` runs `collectSigningPreview` unconditionally. No flag reaches the
  mutation any other way; pin 7 enumerates every flag `on` can see at test time
  and fails on one it does not classify.
- The whole preview is written to stderr (JSON under `--json`) before the
  confirmation. `--yes` skips only the prompt.
- R6: `on` sends the **first** page's token. Every page is read at size 200 with
  drift checks (repeated cursor, null cursor with `hasMore`, repeated row key,
  `total` changing, row count != `total`), each refusing with exit 1 and no
  output. Anything that changes after page 1 changes the server's full-preview
  digest, so the switch answers `BOOT_PROMPT_SIGNING_PREVIEW_STALE` and nothing
  changes: the user can never agree to rows they were not shown.
- `preview --json` has no `previewToken` (R10): the CLI offers no "preview now,
  switch later from a script" path.

## Audience and the existence oracle

The server answers an unknown org and a non-admin with the same `FORBIDDEN`. So
no verb looks the org up (no `GetOrganization` pre-read), `"org"` in `--json`
echoes the ref as typed, and `signingError` replaces the message with one
constant remedy. No verb opts in to `api.WithSession`: the server refuses a
worker session outright. An impersonated mutation gets the platform's
pre-resolver 403 `IMPERSONATION_READ_ONLY`; the CLI deliberately adds no case
(exit 1 with the server message).

## Exit codes

| Code | Exit | Note |
|---|---|---|
| `FORBIDDEN` | 8 | unchanged; one message for every cause |
| `BOOT_PROMPT_SIGNING_PREVIEW_STALE` | 5 | new literal case beside `SWITCHOVER_PROOF_STALE`; message says to run `on` again |
| `BOOT_PROMPT_SIGNING_NOT_CONFIGURED` | 1 | R11: an operator state (no `JWT_SECRET`), left unmapped on purpose |
| `BAD_USER_INPUT` | 2 | e.g. `--first 0`; `--first` is sent unchanged, only a non-integer is a client usage error |
| `BOOT_PROMPT_NOT_SIGNED` (bind/serve) | 1 | unchanged; pin 12 is a regression pin only (R9a) |

## Not in scope

Production switch-on/off (needs its own grant), PR 3's include-`signed`
setting, the portal companion.

Spec: client-only. The rules it consumes are cor:agt:040:09/:10; it decides none.
