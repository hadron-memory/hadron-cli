# slack connect / disconnect (cli#206): design as built

Scope (gate: T2 + §10, CHECKED on cli#206; spec FIT by Victor/Vera): two commands over two existing
server mutations. `slack link` is out (no `mintSlackLinkToken` on the server yet, hadron-server#4615).
Matrix rows `createSlackConnection` and `deleteSlackConnection` move from `gap #206` to commands.

## slack connect

`hadron slack connect --org <ref> --app <ref> --bot-token-file <path|-> --app-token-file <path|->`
calls `createSlackConnection(orgRef, appRef, botToken, appToken)` once.

- **Two secrets, one flag each** (the gate's decision). Each token comes from a file or stdin, read by the
  shared `cmdutil.ReadSecretInput` (the same helper `org resend`, `memory sync` and `ha` use), so a read
  failure never names the path (#1490). Both flags are required: the helper's "empty source reads stdin"
  default would make two tokens ambiguous. At most one may be `-`; both `-`, a missing flag, and `-` on a
  terminal (a typed token is shown in the clear) are refused before any request.
- **No token anywhere else.** There is no token flag (cobra refuses one as unknown); the result renders the
  11 fields `slack get` shows, never `lastError`. The server's text is untrusted everywhere it can
  reach the operator (blind P1 and local P2 on #2731): `redactConnectError` adds the command's own recovery
  text first (so an echo never costs the unknown-outcome notice), then returns the error through
  `api.Detached`, which cuts it from its raw chain and redacts every rendered string (message, `--json`
  serverCode, fault detail) with `api.RedactSecretText`, the transport's own redactor, which also covers the
  URL-encoded spellings an echoing gateway produces (`api.HoldsSecret` is its predicate). A clean server code
  and the exit class survive. Text built from the operator's own `--org` in the recovery pointer is redacted
  too (it can, by mistake, equal a token). Success needs a usable id (an empty id or object is unconfirmed,
  Unavailable); an ERROR row is shown as registered with a recorded failure, not "connected"; an unknown status
  ends in the one command that cannot loop, `hadron slack disconnect <id>` (get and status refuse the same
  status). Every command suggested in a message is shell-quoted with `cmdutil.ShellQuote`, never Go-quoted
  (blind P2, Andi: `%q` leaves `$` and backticks live when pasted). A success answer whose any field holds a token is withheld whole (Unavailable,
  nothing quoted back, not even the id). Limit: exact and URL-encoded spellings only, not an arbitrary
  re-encoding.
- **Recovery order.** The server's duplicate check (`deletedAt: null`, no status condition; hadron-server
  `resolvers.mutation.slack.ts`) counts an ERROR registration as live, so recovering one is disconnect first,
  then connect (blind P2 on #2731, Baloo). The group, connect and disconnect help and the agentic guide say so.
- **The server decides.** Org ADMIN/OWNER, App-in-org and one live connection per workspace are the
  server's; refusals pass through (8 forbidden, 4 not found, 2 bad input). The CLI checks nothing of that
  and its help says so (cor:api:240:01).
- **One request, never retried.** An ambiguous failure (exit 1 or 7) is reworded as an unknown outcome and
  points at `hadron slack status --org <ref>` with the reminder that it lists one page, so follow `--offset`; a null answer is Unavailable, never "connected".
- `--app` goes through `cmdutil.CanonicalAppRef` (ID, URN or `<root>:<slug>`); `--org` is passed as the
  server's ID-or-URN (cor:api:140).

## slack disconnect

`hadron slack disconnect <connection-id> [--yes]` calls `deleteSlackConnection(ref)` once, with no
existence pre-read: the server answers a missing id and another org's id with the same NOT_FOUND
(cor:api:140:02) and a pre-read would let the CLI tell them apart. It asks on a terminal and requires
`--yes` without one. The prompt uses `cmdutil.Confirm`, not `ConfirmDeletion`: the delete is soft and
`slack connect` registers the workspace again, so "cannot be undone" would be false
(`review:confirm-prompt-tells-the-truth`). The prompt says soft delete with best-effort token teardown and that
connecting again registers anew (not a restore). A `false` answer is Unavailable.

## Evidence

`internal/cmd/slack_connect_206_test.go`, fake HTTP only: request variables, one request, local refusals
with zero requests, secret-negative checks on stdout, errors and help, refusal pass-through, the echoed-token
case in message, code and success fields (rendered through the real error renderer), unknown outcome with
guidance kept, the confirmation paths. Mutants killed: the redactor, the chain cut, the success-field
check, the recovery text, the code pass-through, a one-token secret list, the both-stdin refusal, the terminal refusal, the confirmation.
