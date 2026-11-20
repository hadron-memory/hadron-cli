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
  11 fields `slack get` shows, never `lastError`; and `withheld` replaces a server message that repeats
  either token (a proxy may echo the request) with a fixed line that keeps the exit class and server code.
- **The server decides.** Org ADMIN/OWNER, App-in-org and one live connection per workspace are the
  server's; refusals pass through (8 forbidden, 4 not found, 2 bad input). The CLI checks nothing of that
  and its help says so (cor:api:240:01).
- **One request, never retried.** An ambiguous failure (exit 1 or 7) is reworded as an unknown outcome and
  points at `hadron slack status --org <ref>`; a null answer is Unavailable, never "connected".
- `--app` goes through `cmdutil.CanonicalAppRef` (ID, URN or `<root>:<slug>`); `--org` is passed as the
  server's ID-or-URN (cor:api:140).

## slack disconnect

`hadron slack disconnect <connection-id> [--yes]` calls `deleteSlackConnection(ref)` once, with no
existence pre-read: the server answers a missing id and another org's id with the same NOT_FOUND
(cor:api:140:02) and a pre-read would let the CLI tell them apart. It asks on a terminal and requires
`--yes` without one. The prompt uses `cmdutil.Confirm`, not `ConfirmDeletion`: the delete is soft and
`slack connect` registers the workspace again, so "cannot be undone" would be false
(`review:confirm-prompt-tells-the-truth`). A `false` answer is Unavailable.

## Evidence

`internal/cmd/slack_connect_206_test.go`, fake HTTP only: request variables, one request, local refusals
with zero requests, secret-negative checks on stdout, errors and help, refusal pass-through, the echoed-token
case, unknown outcome, the confirmation paths. Mutants killed: the scrub, the both-stdin refusal, the
terminal refusal, the confirmation.
