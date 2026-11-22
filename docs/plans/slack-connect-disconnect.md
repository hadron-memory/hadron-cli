# slack connect / disconnect (cli#206): design as built

Scope (gate: T2 + §10, CHECKED on cli#206; spec FIT by Victor/Vera): two commands over two existing
server mutations. `slack link` is out (no `mintSlackLinkToken` on the server yet, hadron-server#4615).
Matrix rows `createSlackConnection` and `deleteSlackConnection` move from `gap #206` to commands.

## slack connect

`hadron slack connect --org <ref> --app <ref> --bot-token-file <path|-> --app-token-file <path|->`
calls `createSlackConnection(orgRef, appRef, botToken, appToken)` once.

- **Two secrets, one flag each** (the gate's decision). Each token comes from a file or stdin, read by the
  shared `cmdutil.ReadSecretInput` (the same helper `org resend`, `memory sync` and `ha` use), so a read
  failure never names the path (#1490). The helper trims one trailing run of CR/LF and nothing else —
  trailing spaces are NOT trimmed (a mismatch is better than a silent edit of secret material), and the
  source is read whole, with no size cap before the token is held in memory and put on the wire. Both
  flags are required: the helper's "empty source reads stdin"
  default would make two tokens ambiguous. At most one may be `-`; both `-`, a missing flag, and `-` on a
  terminal (a typed token is shown in the clear) are refused before any request.
- **No token anywhere else.** There is no token flag (cobra refuses one as unknown); the result renders the
  11 fields `slack get` shows, never `lastError`. The server's text is untrusted everywhere it can
  reach the operator (blind P1 and local P2 on #2731): `redactConnectError` adds the command's own recovery
  text first (so an echo never costs the unknown-outcome notice), then returns the error through
  `api.Detached`, which cuts it from its raw chain and redacts every rendered string (message, `--json`
  serverCode, fault detail) with `api.RedactSecretText`, the transport's own redactor, which also covers the
  URL-encoded spellings an echoing gateway produces (`api.ContainsSecret` is its predicate). A clean server code
  and the exit class survive. Text built from the operator's own `--org` in the recovery pointer is redacted
  too (it can, by mistake, equal a token). Success needs a usable id (an empty id or object is unconfirmed,
  Unavailable); an ERROR row is shown as registered with a recorded failure, not "connected"; an unknown status
  ends in the one command that cannot loop, `hadron slack disconnect <id>` (get and status refuse the same
  status). Every command suggested in a message is shell-quoted with `cmdutil.ShellQuote`, never Go-quoted
  (blind P2, Andi: `%q` leaves `$` and backticks live when pasted). `ShellQuote` emits POSIX single-quote
  quoting: paste-safe in POSIX shells (the README's own Windows guidance points at WSL or Git Bash), but
  not in cmd.exe, where single quotes are not quoting, and only partially in PowerShell (single quotes are
  literal there, but the `'\''` escape idiom is POSIX-only) — the Windows decision is cli#2822. Suggested commands also use `--org=<value>`
  and put `--` before an id, because cobra reads a leading dash as a flag even when the word is quoted (blind
  P2, Dara); and a `--org` value that holds a token, or a control character, is never printed, quoted or not: it is
  checked on the ORIGINAL value (quoting splits an apostrophe-bearing token so exact redaction would miss it;
  held-delta P2, Jane) and dropped, so the pointer is the unfiltered `hadron slack status`, which runs as
  printed. A server id with control characters is not named either (`ShellQuote` does not escape them, unlike
  the Go quoting it replaced; SEC10 P3, Rodolfo), and the `slack get` suggestion carries no outer prose quotes
  (single quotes are literal inside double quotes). A suggested command sits ALONE on the last line of its message, indented under a "Check with:" label (connect's `status` pointer and disconnect's
  `get`/`status` pointer: prose and any server text first), so copying a whole line copies only the command (blind delta P2, Beckett), and nothing follows it: no punctuation or parenthetical (a colon after a quoted id pastes as part of the id; blind P2, Bruno). The unknown-status recovery is the one exception: it names its `disconnect` command inline, mid-sentence, so it carries no whole-line copy guarantee. An id this
  CLI will not print gets no command at all, only "upgrade hadron, or ask the server's administrator" (get and
  status refuse the same unknown status, so pointing there would loop). A success answer whose any field holds a token is withheld whole (Unavailable,
  nothing quoted back, not even the id). `--org` and `--app` are each trimmed once and the trimmed value is
  what is sent (cli#2756 P3-2). Limit: the exact, all-lower, all-upper and URL-encoded spellings are matched
  (cli#2756 P3-4); a partial echo or a mixed-case re-encoding is NOT — redaction is bounded, not complete.
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
connecting again registers anew (not a restore). Declining the prompt is `Cancelled`, exit 6, with nothing
sent (cli#2756 P3-3; the disconnect help and the agentic line say so). A `false` answer from the server is
Unavailable.

## Evidence

`internal/cmd/slack_connect_206_test.go`, fake HTTP only: request variables, one request, local refusals
with zero requests, secret-negative checks on stdout, errors and help, refusal pass-through, the echoed-token
case in message, code and success fields (rendered through the real error renderer), unknown outcome with
guidance kept, the confirmation paths, and (cli#2756) the U+2028/U+2029 separators.
`internal/cmd/slack_followups_2756_test.go` (cli#2756): the trimmed `--org`/`--app`, the short `--app` form,
the malformed-`--app` refusal, disconnect's text success line, the unknown-status refusal quoting a
hostile status (the allowlist refuses the whole value, which starts with ACTIVE so a prefix or trim
match would let it through; one rune of every guarded class in it exercises the quoting of the refusal;
the exit code and the rendered line are pinned), and the happy-path status
table whose whole row renders quoted, in column order. `internal/api/secrettext_test.go` (cli#2756): the case-flipped
spellings, from a mixed-case secret so either flip arm dropped fails, with the lower-hex escape spelled
as a precomputed literal (a test spelling built by calling `lowercasePercentHex` would be circular with
the function `secretSpellings` calls). Mutants killed: the redactor, the chain cut, the success-field
check, the recovery text, the code pass-through, a one-token secret list, the both-stdin refusal, the terminal refusal, the confirmation,
the Zl/Zp guard, the once-trim, the case-flip spellings (either flip arm), the lower-hex escape arm, the
status-allowlist refusal (toDTO's non-ACTIVE/ERROR refusal removed), and the status cell's quoted render
(reverting it to a raw render fails the happy-path table pin; the SafeCell inside it is
equivalent-under-allowlist and deliberately unpinned: the allowlist is the guard that is reachable).
