# telegram status / get / connect / disconnect (cli#2952): design as built

Scope (gate: T2 + §10, CHECKED on cli#2952; scoped FIT AT0.9823.1 by Vera, binds cor:api:240:02):
four commands over the hadron-server#4824 SDL (merged 79555b42, #597), the Telegram twin of the
Slack parity in #206 (`docs/plans/slack-connect-disconnect.md`). One shape decision differs from
Slack and everything else follows #206: the connection is **App-owned** — there is no `--org` on
get/connect/disconnect because the organization comes from the App, and the server allows one
ACTIVE connection per App and one per bot. Matrix rows `telegramConnection(s)`,
`createTelegramConnection` and `deleteTelegramConnection` enter as commands (no gap rows existed:
the surface is new).

## telegram status / get (reads)

`hadron telegram status [--org <id|urn>] [--limit 0..200] [--offset N]` calls
`telegramConnections(orgId, limit, offset)` — paged exactly like `slack status`: one page (server
default 20, cap 200, `0` returns only the total), the continuation hint on stderr, variables
omitted unless the flag was passed (the `# @genqlient(omitempty: true)` annotations in
`internal/api/queries/telegram.graphql`). `--org` narrows (a non-member gets an empty page, not a
refusal); `--app` and the saved organization never apply. `hadron telegram get <connection-id>`
calls `telegramConnection(ref)`; null (a missing row, an outsider, a revoked member — the server
answers all three identically) is exit 4. `lastError` is never selected and no field can return
the token (write-only input), so neither can leak; the fake rows in the tests carry both to pin
the selection. A status outside {ACTIVE, ERROR} is refused (`toDTO`, exit 7): status is a recorded
value, not a health check, and the CLI never invents a meaning for one it does not know.

## telegram connect

`hadron telegram connect --app <ref> --bot-token-file <path|->` calls
`createTelegramConnection(appRef, botToken)` once.

- **One secret, one flag.** The bot token comes from a file or stdin via the shared
  `cmdutil.ReadSecretInput` (never a flag value; shell history, process list). `-` on a terminal,
  a missing flag (its own named refusal, not an accident of reading an empty stdin — #206's F3),
  an empty file and a missing file are all refused before any request. One trailing run of CR/LF
  is dropped and nothing else.
- **`--app` goes through `cmdutil.CanonicalAppRef`** (ID, URN or `<root>:<slug>`; a bare slug is
  "not org-qualified", and the refusal never echoes the value — cli#2876 — which also covers a
  token pasted where the App belongs).
- **The server decides.** The ONE `Forbidden` renders as received (a non-admin, an unknown or
  ambiguous App, a user-owned App answer identically — the CLI does not guess which), as do the
  `BAD_USER_INPUT` duplicates and the `TELEGRAM_TOOL_*` refusals. The `telegramErrorCode` extension
  those carry (e.g. `invalid_token`) is surfaced next to the server's message — the promise the
  help and agentic-usage make is pinned by tests in both renderers, with `serverCode` in the
  `--json` envelope. Exit classes ride `api.MapError`'s table (8 / 2 / 4); the `TELEGRAM_TOOL_*`
  codes are unmapped there and land in the exit-1 default with their serverCode intact.
- **No token anywhere else.** The result renders the 10 fields `telegram get` shows. The server's
  text is untrusted everywhere it can reach the operator (the #2731 lineage): `redactConnectError`
  adds the command's own recovery text first (so an echo never costs the unknown-outcome notice),
  then returns the error through `api.Detached`, which cuts it from its raw chain and redacts every
  rendered string (message, `--json` serverCode, fault detail) with `api.RedactSecretText` — the
  transport's own redactor, which covers the URL-encoded spellings an echoing gateway produces.
  A clean server code and the exit class survive. A success answer whose any field repeats the
  token is withheld whole (Unavailable; not even the id is quoted back).
- **Success is validated.** A usable id is required (empty id or object → unconfirmed, exit 7);
  an ERROR row prints "registered … recorded a failure", not "✓ connected"; an unknown status ends
  in the one command that cannot loop, `hadron telegram disconnect -- <id>` (get and status refuse
  the same status), named inline mid-sentence.
- **One request, never retried.** A `TELEGRAM_TOOL_*` refusal is a *definitive* answer
  (`api.IsDefiniteRefusalAt` on `createTelegramConnection`, `TELEGRAM_TOOL_`-prefixed, not from a
  5xx envelope): it renders as "connect was refused" with no pointer — there is no registration to
  look for. Every other exit-1/7 failure (a lost answer, an unmapped code, a post-commit crash) is
  reworded as an unknown outcome and points
  at `hadron telegram status` — the UNFILTERED listing, because connect takes no `--org` whose
  value could be carried into the command (and a mistaken `--org` typed as a token can therefore
  never be echoed back through the pointer; the submitted token is still redacted everywhere). The
  pointer says the listing is paged (follow `--offset`) and that a lost registration answer can
  leave a tool-side row core cannot see (hadron-server#4829). A suggested command is shell-quoted
  (`cmdutil.ShellQuote`), carries `--` before the id, sits ALONE on the last rendered line, and an
  id with control/format characters (Cc/Cf/Zl/Zp, incl. U+2028/U+2029) is never named — then no
  command at all, only "upgrade hadron, or ask the server's administrator".

## telegram disconnect

`hadron telegram disconnect <connection-id> [--yes]` calls `deleteTelegramConnection(ref)` once,
with no existence pre-read (missing, other-org and revoked-member answer `NOT_FOUND` identically).
The prompt uses `cmdutil.Confirm`, not `ConfirmDeletion`: the server soft-deletes and `connect`
registers the bot again, so "cannot be undone" would be false. The prompt says soft delete,
best-effort token and Telegram-side teardown (not reported), and that connecting again registers
anew. Declining is `Cancelled` (exit 6, zero requests); without a terminal `--yes` is required.
A `false` answer is Unavailable. The outcome-unknown / did-not-confirm pointers carry the
shell-quoted id with the `--` terminator alone on the last line, falling back to the paged
`hadron telegram status` for an id the CLI will not print.

## Evidence

`internal/cmd/telegram_status_2952_test.go` and `internal/cmd/telegram_connect_2952_test.go`, fake
HTTP only: request variables (no org variable on connect), one request, local refusals with zero
requests (including `--org` on connect and a token pasted as a flag), secret-negative checks on
stdout, errors and help, refusal pass-through (Forbidden / both duplicates / a TELEGRAM_TOOL code),
the echoed-token case in message, code and success fields rendered through the real error renderer
(both renderers), unknown outcome with guidance kept and the pointer unfiltered, success-answer
validation (empty id, empty object, unknown status → disconnect not get, ERROR not "✓ connected"),
shell-safety of suggested commands (space/`$` id, dash-prefixed id after `--`, control characters
and the U+2028/U+2029 separators), the confirmation paths and prompt wording, the paged reads
(hint, final page, count-only, omitted defaults) and the red-first property: the same files fail
30 tests against `origin/main` (verified in a throwaway worktree before the dispositions were
written). The coverage matrix rows land as `command telegram get|status|connect|disconnect`
(`internal/api/coverage-matrix.txt`); the agentic contract is the `hadron telegram …` line in
`internal/cmd/agentic/agentic-usage.md`.
