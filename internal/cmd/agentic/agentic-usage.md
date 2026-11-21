# hadron CLI — agentic usage

`hadron` is the command-line interface to the Hadron AI-memory
platform. This document is the single reference an agent needs to
drive the CLI. Read it once per session; everything below is stable,
documented contract.

## Setup and authentication

```
hadron auth status            # server answer: AUTHENTICATED exit 0; REFUSED/ANONYMOUS exit 3; --json = {status} only, no cause
hadron auth login             # interactive browser OAuth (human only)
echo $TOKEN | hadron auth login --with-token   # store a PAT
HADRON_TOKEN=hdr_user_...     # env var, overrides stored tokens (CI)
hadron auth token create      # mint a PAT (after login); list | revoke <id> to manage
echo $TOKEN | hadron auth token validate   # check a PAT: exit 0 valid / 3 rejected
hadron auth impersonate <user> --org <org>   # admin: view read-only as a member
hadron auth impersonate --stop               # end the impersonation session
```

`hadron auth impersonate` is for org ADMIN/OWNER support/diagnostics: it
starts a read-only session as another member of `--org` (peer-or-below — an
ADMIN can't impersonate an OWNER), scoped to that one org, and files a
short-TTL token that every later command prefers until `--stop` or expiry.
`hadron auth whoami` announces the acting-as state. `auth status` reports only
whether the request credential authenticates; it does not identify the actor.

`hadron auth login` (either path) names the account it signed in as: a second
stdout line `  account: <name> (@<handle>) <email>` (or `App <id>` / `Agent <id>` for
a non-user key) after the unchanged `✓ Signed in to …` line, and `--json` adds
`account: {id,name,email,handle,principalType,appId,agentId} | null`. It is read
with the token just stored, never `HADRON_TOKEN` or an impersonation token. If
the read fails, login still exits 0 and keeps the token, `account` is `null`,
and stderr says `note: signed in, but the account could not be read (…); run
hadron auth whoami`. The browser login reuses the Hadron session already open
in the browser, and `auth logout` does not sign that out: to log in as another
account, sign out of Hadron in the browser or use a private window.

After successful login, a terminal offers enabled task skills for detected Claude
Code/Codex hosts, default **no**. `--install-skills` accepts without prompting;
`--no-install-skills` skips the offer and hint. `hadron config set
skills.installPrompt false` silences automatic offers (the explicit install flag
wins). Detection uses `.claude`, `.agents`/`.codex` under the user's home or the
`claude`/`codex` binary on PATH; only those hosts are installed. Non-TTY/`--json`
prints a command on stderr, without asking. Explicit `--install-skills --json`
adds the existing export report as `skills` in the single login JSON document;
a partial installation returns its report plus a nonzero exit. Installation
uses the token login just stored, never ambient `HADRON_TOKEN` or impersonation.
It shares `skill export`'s writer and ownership checks, never forces/prunes, and
prints a restart hint only when files changed. An optional offer's failure keeps
the login successful; an explicit install failure is reported after storing the
token. No enabled skills means no prompt; enable task exports in the portal.

Browser `auth login` opens the server's sign-in page, which offers the
methods that server supports (GitHub, Google or email). If the server
requires a passkey for email sign-in, its page asks for one before the CLI
receives anything. Where the server offers passkey
registration, it emails a second link: open it in the same browser.
Otherwise choose GitHub or Google on that page, or pass `--provider google` to
go straight to Google (`--provider github` sends no hint and opens the same
sign-in page as the default). An agent cannot complete a browser
passkey step; agents use `HADRON_TOKEN` or `--with-token`. The browser step
must finish within 15 minutes (a CLI-set limit, not a server rule); on
timeout it exits 6 and stores nothing. Where the server has you add a first
passkey (with its second link), that can take longer than 15 minutes; if it
does, the CLI stops and stores nothing: finish adding the passkey in the
browser, then rerun `hadron auth login`. When the server rate-limits the browser sign-in at its
token step (HTTP 429, hadron-server#4659), `auth login` exits 1 with "wait N seconds, then run
`hadron auth login` again", following the server's Retry-After: nothing was signed in or stored,
it is not a rejected credential, and the CLI does not retry by itself. A refused STORED credential exits 3
with "your stored credential was refused; run `hadron auth login`": the CLI
does not retry, does not delete the stored token, and does not claim why it
was refused. The hint follows the credential actually sent: only when every
request carried the stored login, never for `HADRON_TOKEN`, an impersonation
token, a host key, a stdin token, or a failure before any request.

Browser `auth login` requests the `account` OAuth scope (the data API
refuses an `mcp`-only key). If the server refuses `account` or grants a
narrower scope, login fails with exit 1 and stores nothing: never a silently
narrower key. Use `--with-token` there.

Tokens are long-lived `hdr_user_*` personal access tokens, minted with
`hadron auth token create` (after an interactive `auth login`) or in the
Hadron portal. `auth token` requires a user login — an app/agent key can't
manage user tokens; the raw key is shown once, so store it on creation. The
server defaults to
`https://srv.hadronmemory.com`; override per-invocation with `--server
<url>` or persistently with `hadron config set server <url>`. The CLI refuses
to send your token to a non-`https` server (cleartext credentials); a loopback
host is exempt, and `HADRON_ALLOW_HTTP=1` overrides the check for a trusted
self-hosted backend.

## Output contract

- Every command supports `--json`. JSON goes to stdout; progress goes to
  stderr. Without `--json`, output is plain aligned text.
- **With `--json`, stdout is ALWAYS valid JSON — success or failure** (#334).
  A failure writes the envelope
  `{"error":{"code":<exit-code>,"message":"..."}}` to **stdout**, so
  `json.loads(stdout)` never throws and you read the diagnosis on the stream
  you were already parsing. Before this it went to stderr and stdout was left
  empty, so the obvious consumer died with *"Expecting value: line 1 column
  1"* — which reads like corrupt data rather than a server error.
  - **Branch on the exit code first, then look at the document.** The code
    tells you THAT it failed; the envelope tells you what. Do not detect
    failure by testing for an `error` key alone — a successful payload could
    in principle contain one.
  - **Two exceptions, both deliberate.** First, some commands do not emit
    JSON on SUCCESS at all, because the bytes are the output and `--json` is
    ignored: `asset get -o -` (the asset), `node export` to stdout (the
    document), and `agentic-usage` (this page, as Markdown). Do not parse
    their stdout.
    Second, a command that has already written such a payload keeps its
    error on stderr, because appending an envelope would concatenate two
    values and make stdout unparseable — the very failure this rule removes.
  - So read the guarantee precisely: **for a command that emits JSON on
    success, stdout is valid JSON on failure too.** It is not a promise that
    every command emits JSON.
  - Exit codes are unchanged by all of this.
- `message` NEVER carries the GraphQL document location and field path the
  client used to render around it (#566) — a leading `input:<line>: <field> `
  naming a position in a query you never wrote and a field you never typed.
  That is gone for every error. If you match on message text, do not strip
  such a prefix and do not expect one.
  - **Whose sentence it is depends on the error.** For an otherwise-unhandled
    GraphQL refusal it is the server's wording verbatim. But many messages are
    written by the CLI and always were: flag and usage errors (exit 2), the
    schema-skew hint, the transport/`Unavailable` classification (exit 7), and
    command-specific guidance such as the asset scan refusals or `worker get`'s
    not-found. `hadron api` is separate again — it is the raw path and formats
    its own HTTP errors.
  - So **branch on the exit code first**, and on `extensions.code` where you
    have it, rather than pattern-matching wording you assume the server chose.
- **Messages-waiting line (cli#1084).** When a successful command's server
  response carries the optional `extensions.hadron.deliveryPending` metadata
  with a positive exact count, the CLI prints ONE extra line on **stderr**, in
  text and `--json` mode alike: `N messages waiting; pull with hadron team chat
  inbox` (`1 message waiting; …` for one). stdout, the result, the exit status
  and the number of requests are unchanged; the CLI computes no count and makes
  no extra call. Zero, absent, malformed, unsupported or incomplete metadata
  prints nothing, and a failed command never prints it. `hadron api` is not
  covered. Do not parse this line: pull with `hadron team chat inbox`.
- JSON field names are stable. New fields may be added; existing
  fields are never renamed or removed without a major version bump.

## Exit codes (stable contract)

| Code | Meaning |
|------|---------|
| 0    | success |
| 1    | generic failure |
| 2    | usage error (bad flags/arguments, missing --yes) |
| 3    | authentication required or rejected |
| 4    | not found (or not visible to this principal) |
| 5    | conflict (e.g. duplicate install, a loc already taken); task create --match: task created, match failed, receipt printed — do not recreate |
| 6    | cancelled / timed out waiting for the user |
| 7    | unavailable — the request never got an answer (gateway 5xx, reset, timeout) |
| 8    | forbidden — authenticated, but not permitted |
| 10   | Orca not reachable — `hadron orca …` run off the Orca host (command-specific; see below) |
| 13   | `team chat read --wait` timed out: every poll succeeded and no new message arrived (command-specific; not an error — wait again from the same `--since`) |
| 14   | `team session whoami --check` found the bound session ENDED (command-specific; the answer is still printed, end time included, text or `--json`; not an auth failure — rebind with `session start`) |
| 15   | `team task find-untracked --adapter` found an intake adapter that speaks another contract version (cor:agt:120:04; command-specific; nothing was asked of the adapter and Hadron was not touched: upgrade the adapter or the CLI) |

`hadron orca …` exits **10** when the Orca CLI/app does not answer (this is not the host that runs
Orca). A worker with no terminal is NOT that: it is a `dark`/`no_terminal` status and exit 0. The other
command-specific 9–12 below belong to `team push` / `team chat check` and are never emitted by `orca`; 13 belongs to `team chat read --wait`; 14 to `team session whoami --check`; 15 to `team task find-untracked --adapter`.

**Internal server faults exit 1, never 7** (cli#898, cor:api:260:01). The server
answered, so it is not "unavailable". A response whose GraphQL `extensions.code` is
`INTERNAL_SERVER_ERROR` (also the plain-HTTP `code` of asset calls, and an OAuth
`server_error` carrying `request_id`) prints one CLI-written sentence,
`the server hit an internal error (request id: <id>); report this id when you ask for help`
(`…internal error; no request id was returned` when there is none), and never the
server's own text. The code alone decides: not the text, not the HTTP status, and no
other code gets this message. The id is `extensions.requestId` (else the body's
`requestId`/`request_id`, else the `X-Request-Id` header) and is printed only if it
matches `^[A-Za-z0-9._:-]{1,64}$`; otherwise it is treated as absent. Under `--json`
the failure envelope gains `serverCode` (`"INTERNAL_SERVER_ERROR"`) and, when an id is
known, `requestId`, additively; `code` stays the exit code. `hadron api` stays verbatim.

A typed SESSION_SUPERSEDED error gains serverCode:"SESSION_SUPERSEDED".
Only when the server supplies both a nonempty string sessionId and a valid
RFC3339 startedAt does the curated JSON error add those two exact values;
otherwise both fields are absent. These name the independently readable current
open session, not a historical successor pointer. Text names that pair or says
no current-session details are available, with no distinction between unreadable
and absent. No retry, successor fetch or automatic rebind is attempted.
A 5xx carrying that typed code retains the existing uncertainty/message/exit and
serverCode:"SESSION_SUPERSEDED", but adds no successor fields or definitive hint.
When it also carries an internal fault, this more specific marker wins regardless
of error order; the internal-fault message, numeric exit and requestId stay as before. Ended
and expired sessions keep their existing answers and exits (cli#1845).

`team chat check` additionally uses **9** for an empty tagged range whose sampled
head is marked, (9 is not an error; handle it explicitly in `set -e` scripts), and **10** for an uncertain mark outcome. These are command-specific;
0 means messages were written and coverage confirmed, not that an agent read the file.

`team session sync-model` (without `--hook`) uses **11** when it could not prove a model
for the bound session: nothing was reported and nothing was refused. With `--hook` it
exits 0 in that case (and on any failure), prints nothing on stdout, and writes one fixed
sanitized line on stderr. Command-specific; 11 is not an error class of its own.

`team session set-model [<model>] [--family <token>] [--effort <level>]` (cli#887, cli#2206) needs at least one of the three; --effort reports the effort the session runs at (LOW|MEDIUM|HIGH|XHIGH|MAX|UNSET|NOT_APPLICABLE), a report never a control, omitted when not given, and `sync-model` never reports one; a resume (`session start` with no worker) takes --effort too;
only what you give is sent (an omitted model or family is preserved, never nulled). Zero values,
more than one model, or an empty/blank/over-long/multi-line model or family is a usage error
(exit 2, nothing sent); an empty `--family` does not clear. The family goes to the server as typed
and wins over its derivation; the server's refusal is surfaced and no receipt is printed. The
`--json` receipt names only what was sent: `{sessionId, model (null when family-only), family
(omitted unless sent), reported}`. `sync-model` (the hook) stays model-only and has no `--family`.

`team session shed [--auto] [--print-prompt] [--offline] [--cooldown <d>] [--transcript <path>] [--note-file <path>]`
(cli#952) **exits 0 on every failure after the flags parse** and never blocks a reset: one fixed
sanitized line on stderr, and under `--json` `{status: written|skipped|failed|printed, node, claim:
written|failed|none, reason}`. With `--auto`, flag-parse errors also exit 0 (nothing on stdout
without `--json`); without it a bad flag exits 2 as usual. It writes `sheds:<stamp>` (or
`sheds:auto-<stamp>`) into the worker's own memory, reads it back, and only then writes the resume
claim: a failed node write never leaves a claim, a failed claim leaves the node (`claim: failed`).
`--auto` inside `--cooldown` (default 10m, stamp in the git dir beside the binding) is `skipped`
and retries only a failed claim. `--print-prompt` mutates nothing. No claim value is ever printed.
`--transcript` is opt-in and NOT scrubbed: its lines land in a node the team can read.
`--note-file <path>` (cli#1648) appends your own narrative (in flight, next action) exactly as
written as `## Notes` after the Recovery prompt and before any Transcript tail: UTF-8, 64 KiB
(65,536 bytes) at most, refused not truncated, NOT scrubbed. A missing/unreadable file, a
non-regular file (directory, FIFO, device), an empty or whitespace-only file, an over-cap file,
invalid UTF-8 and an empty flag value are each refused with one fixed line naming only the class
(never the path or text): exit 2 on a manual run, exit 0 + that stderr line under `--auto`, and in
both no node, claim or cooldown stamp. `--print-prompt`, a cooldown skip and a claim-only retry
never open the file. `--json` keeps its shape.

`team session reset-context [--note-file <path>] [--transcript <path>]` (cli#2161) is a context
reset that comes back at once in the same session. It writes the checkpoint as `shed` does but
under `checkpoints:<YYYY-MM-DD-HHMMSS>`, then the resume claim, then a relaunch stamp beside the
binding, and then sends SIGTERM to exactly the pid the host's Orca launch exported as
`HADRON_AGENT_PID`, only while that pid is the command's ancestor. The host's poller relaunches the
worker when that agent exits, through its periodic relaunch's own guards and hourly cap. Otherwise
the ordinary sweep brings the worker back. `--json`: `{status: written|failed, node, claim:
written|failed|none, relaunch: pending|failed|none, exit: signal-pending|exit-now|none, reason}`.
`signal-pending` is **not** success: it means the CLI signals next, and the outcome is not in this receipt. The pid is proven and pinned, and SIGTERM follows once this receipt is written. A receipt that can't be written signals nothing, and a signal that fails says so on stderr. `exit-now` means nothing will be signalled (the pid could not be proven), so exit the agent yourself. The new agent's first prompt names the checkpoint. A failed checkpoint
writes no stamp and ends nothing. Exit 0 unless the command line is wrong.

**A permission refusal exits 8, not 1 (#619).** This CHANGED: `FORBIDDEN` — the
platform's general refusal, named at 23 sites in the SDL and already arriving —
fell through to the generic 1, where it was indistinguishable from a bug or an
outage. `CHANNEL_HOST_NOT_WRITABLE` joins it: `createTeamChatMessage`'s
successor to the retired `SESSION_NOT_IN_APP` / `SESSION_WORKER_NOT_IN_APP`,
raised when a worker's on-behalf-of user may not write the Channel's host
memory.

**8 is not 3.** Exit 3 means "no credentials, or credentials rejected" and its
remedy is `hadron auth login`; exit 8 means the server knows exactly who you
are and will not let you do this. Branching on 3 vs 8 is branching on *sign in*
versus *ask someone for access* — two different humans and two different next
actions, and telling an authenticated caller to log in is a false remedy.

Expect this class to grow: hadron-server#1220 migrates ~59 further call sites
onto typed `FORBIDDEN`, 17 of them inside seven shared gates.

**One `FORBIDDEN` exits 3, on purpose: an MCP-only key (#681).** A user key
whose OAuth grant is `mcp` alone (what a browser login from hadron v0.14.0 or
earlier stored) is valid for MCP clients and refused on every CLI surface. The
fix is a different credential, not somebody's permission, so commands normally exit
**3** on it (`auth status` has the no-answer exception below). That includes
`app agent add/remove` and `agent create --install-into`, whose usual "needs CONTRIBUTOR+" guidance would be a false
remedy here. The latter still reports the agent as created, and puts
`app agent add` AFTER replacing the key. The message keeps the
server's sentence (without Apollo's `Context creation failed: ` prefix, which
the live `/graphql` refusal carries) and appends both recoveries:
`hadron auth logout && hadron auth login` (v0.15.0+ requests `account`), or a
key from the portal's API keys page (`/app/account/api-keys`) passed to
`hadron auth login --with-token`. If the key is in `HADRON_TOKEN`, replace
the variable: it outranks any stored login.
- `auth token validate --json` carries `rejectedReason` beside `valid: false`.
  It is set only when that command knows the scope refusal; an unknown value
  should be treated as an unknown reason.
- `auth status` instead asks `authenticationStatus` (cor:aut:010:05, ruling 0224).
  Its answer-only JSON is `{"status":"AUTHENTICATED"}` (exit 0),
  `{"status":"REFUSED"}` or `{"status":"ANONYMOUS"}` (exit 3).
  It never adds an authentication refusal cause, identity, credential source or
  token. A scope refusal before the resolver runs is a failed check with no
  answer: exit 1, no status report, and stderr carries only the CLI's fixed
  recovery for that typed scope refusal (never the server's sentence, never the
  REFUSED line; cli#1752). Other check errors retain their ordinary
  mapped exits except 3 becomes 1; missing/unknown status values exit 7. Older-server
  schema errors stay explicit (exit 2), without authContext fallback or retry.
- **`hadron api` is the exception.** It is the raw path, so it prints the
  server's envelope as is and exits 8 by `extensions.code`, like any other
  `FORBIDDEN`.
- **How it is recognised:** a `FORBIDDEN` whose `extensions.reason` is
  `OAUTH_SCOPE_INSUFFICIENT` (→ `rejectedReason: "mcp-only-scope"`) or
  `OAUTH_SCOPE_UNSUPPORTED` (→ `"unsupported-scope"`: the key carries a scope
  this server does not support, so it is refused everywhere; same exit 3, a
  new-credential remedy). A server that predates `reason` (server#1306) is
  recognised by the MCP-only sentence instead. Anything else keeps the plain
  `FORBIDDEN` mapping (exit 8, no remedy); it is never misreported as a scope
  refusal.

Two codes that look like they belong here and do NOT, so a reader does not
complete the family: `HOST_MEMORY_NOT_WRITABLE` and `HOST_MEMORY_NOT_READABLE`
are members of the `RegisterDisclosure` **enum** — rendered explanations of why
an attendee cannot act on a row, never an `extensions.code` — and `LOC_PROTECTED`
is a wrong-door or reserved-address refusal rather than a permission one: the
caller may hold full write access, so "ask for access" would be the wrong next
action (it exits 2, #727, below).

**A governed-kind refusal exits 2, not 1 (#721).** This CHANGED: the server's
`ROLE_GOVERNED` fell through to the generic 1, while the CLI's own refusal of
a write touching two governed kinds (task, spec, review) exits 2. It now exits
2 whichever side catches it. It is not 8: the caller may hold full write
access. The message is the server's, kept whole, and says which case it is:
- **a wrong door, or two kinds at once** (`node add`, `node update`, `node
  import`; the CLI routes by kind, so from it this mostly appears when the
  node's kind could not be read first — rerunning may be enough);
- **a governed node's revision restore** (`node revision restore` on a task,
  spec or review): no door restores one yet (server#1204), so there is no
  route to take and nothing to retry.

`hadron api` exits 2 on it too, and prints the envelope with its
`extensions` (`kind`, `door`, `refusal`, and `kinds` for the two-kind case).
**A write to a chat Channel's reserved address exits 2, not 1 (#727).** This
CHANGED too: the server's `LOC_PROTECTED` fell through to the generic 1. It
exits 2 in all three of its cases, and the message, kept whole, says which:
- **the Channel's own operation is the route** (a create or write at a live
  Channel's address, including a move INTO it: post with `hadron team chat
  post`, or `hadron channel post <id|address>` for another Channel);
- **no operation may do this here** (an update, delete, merge, restore, copy
  or a move OUT of a live Channel's address);
- **a deleted Channel still reserves the address**: recreate the Channel to
  write there, or delete what it orphaned. (`hadron api` shows this case as
  `extensions.channelLive: false`; the curated commands print the message.)

None is a permission (8) or a transient (7), and `hadron api` exits 2 on it
too. Like the #608 rule below, this is keyed on the server's refusal, not on a
list of commands: **any command that writes, deletes, moves, merges, copies or
restores a node at or over a Channel's address exits 2 on it** (verified on
`node add`, `node update` and `node rm`).

**An include tag the server refuses exits by its class, not 1 (#916).** This
CHANGED: all three include write refusals fell through to the generic 1. The
message is the server's, kept whole, and names only the authored tag:
- **`INCLUDE_TARGET_UNAVAILABLE` exits 4.** A tag the write adds or changes
  names a target that is missing, deleted or not readable by you. The server
  gives all three the same answer, and so does the CLI: it looks nothing up.
  Tags already stored are not re-checked.
- **`INCLUDE_CYCLE` exits 2.** The tag would make the node include itself,
  directly or through other nodes. Change the content.
- **`INCLUDE_EDGE_DERIVED` exits 2.** An INCLUDE edge belongs to its node's
  content, so `edge update` and `edge rm` cannot change it. Edit or remove the
  tag in the content instead.

Like the rules around it, this is keyed on the refusal, not on a list of
commands: any node write (`node add|update|import|merge|clone|revision
restore`, `memory clone|extract`, `spec edit|replace`) exits this way, and
nothing is written.

**Writing a node at a loc that is already taken exits 5, not 1 (#608).** This
CHANGED: it used to exit 1, because the server stamps that `extensions.code`
from an Error class name (`NodeLocConflictError`) rather than from the
SCREAMING_SNAKE vocabulary the mapper recognised, so it fell through to the
generic failure. The rule is keyed on the server's refusal, not on a list of commands: **any
command that creates a node and is refused because the loc is live now exits
5.** That is what makes "this citation already exists" branchable without
reading the message, which is the whole point of an exit code.

Commands whose exit code CHANGES: `node add`, `node import --create-only`,
`asset link`, `chat post`, `spec new`, `spec supersede`, `spec extract`,
`coding review create`, `coding preflight add`. (`node import`'s tree form
already exited 5 — it special-cased this refusal at its own call site, which is
also where `--on-conflict skip` is handled, so it is unchanged.) Take the list
as the commands verified, and the rule above as the contract — a create added
later inherits 5 without this list being updated.

**If you branch on exit 1 for a duplicate loc, move to 5.** A `move` onto an
occupied destination already exited 5, so the two now agree.

**7 is the only retryable code, and the only one that certifies no usable
answer arrived** — the request may never have reached the server, *or* it
arrived and committed while the response was lost. Codes 2–6 are the server's
opinion. Code 1 stays the generic fallback and is NOT such a certificate: it
also covers local failures (building a request, an unrecognized error), so
"not 7" does not by itself mean the server decided. Retry on 7; on 1, read the
message. A 5xx is only 7 when the body is **not** a GraphQL
envelope — Apollo answering 500 with a real `errors[]` is an opinion and stays
1. The usual cause is the server restarting for a deploy, which resolves on its
own within about a minute.

After a **mutation**, do not treat 7 as "nothing happened": verify current
state before retrying, or a non-idempotent write can be double-applied.
A **mutation is sent once to the `--server` URL and never replayed across a redirect**
(hadron-cli#1571): if the endpoint answers any redirect (301/302/303/307/308), the CLI
refuses it ("redirect refused by policy", nothing was sent to the target). Only 307/308
would re-send the body; the others drop it and follow as a GET, and are refused too so
there is one rule. Fix the `--server` URL. Queries and the login flow still follow redirects.
`hadron api` knows whether your document was a mutation and says so outright;
curated commands state the caveat conditionally, since the mapper is shared by
reads and writes.

An **unknown subcommand** is a usage error (2), at every depth: `hadron spec
update` exits 2 with `unknown command "update" for "hadron spec"; did you mean
"edit"?` rather than printing the group's help and exiting 0. This holds with
`--help` attached too, so a probe like `hadron <group> <guess> --help` is a
reliable way to test whether a subcommand exists.

A **partial write** exits non-zero (generic failure, 1): a command that creates
the primary entity but cannot wire one or more of its edges — `node import
--with-edges` (see `unwiredEdges`), and `spec supersede` when the old spec's
`superseded-by` edge can't be confirmed (that edge's `status` is `unknown`) — reports the
detail on stdout/stderr and
still exits 1, so a
caller branching on the exit code never reads a partial success as complete. The
node/spec exists but is under-linked; fix the target(s) and wire the edge(s).
For supersede, the old spec is **re-read after every `superseded-by` write**,
successful or not, before anything is retired or prescribed:
- **Another replacement also supersedes it** (a concurrent supersede, whose
  edge to a different target succeeded too): supersede **exits 5 (conflict)**,
  retires nothing and prescribes no write. The message names both
  replacements, and which one stands is a human call. It narrows the race; it
  cannot close it, since that needs a server-side conditional retirement.
- **The write errored but the edge landed** (a lost response): the run finishes.
- **The write errored and the re-read doesn't show the edge, or fails**:
  status `unknown`, because a read can lag a committed write, so this is never
  proof of absence. Exit 1. The error says to check with `spec get <old>`.
  If after a minute there is still no edge, link it with the exact `spec link`
  it names. Once `spec get` shows the link, rerun the supersede to finish the
  retirement. Don't rerun it first: with no edge to find, a rerun mints a
  second replacement.
- **The write succeeded but the re-read failed, or doesn't show the link
  yet**: status `created`, exit 1, and the spec is **not** retired. It retires
  only on a link it has *seen* to be the sole successor. **Rerun only once
  `spec get <old>` shows that `superseded-by` edge.** A rerun that still
  can't see it takes the new-supersede path and mints a *second* replacement.

`spec edit|new|rescind|supersede|extract --dry-run` and `node update --dry-run`
(cli#1912, cli#1938) answer a caller who cannot write to the
memory with the real run's refusal, `Forbidden: no write access to this memory`, exit 8,
and prints no plan. The verdict is the server's (`effectiveAccess` for the caller on the
memory), asked only after the target spec/node/memory was read, so an unreadable one still gives
the real run's not-found. A credential with no user (an App key) is not asked; its dry
run is the plan as before. Auth/access lookup failures are errors, never a plan.
This does not change `spec renumber` or `spec replace`, whose plans are server-computed.

`spec supersede` (both modes, `--dry-run` too) refuses a RESCINDED or WITHDRAWN
source (lead or `rescinded`/`withdrawn` tag, cor:spc:010:01) with exit 5 and no
write. If the source becomes one after the link or replacement landed, the
retirement is refused (exit 5), that link stays and is reported, and no rerun is
advised: a rerun gets the early refusal.

`spec supersede <old> --by <existing-citation>` creates no node and does not
rewrite the old body. It links and tags the old spec to an existing written,
live spec in the SAME memory (cross-module and cross-product allowed). It
refuses a missing/unreadable, non-spec, placeholder, deleted, retired, self or
cyclic target before writing. --by conflicts with --title, --to, --feature,
--rule-after, --copy-body and --reason; blank --by is usage, not omission. It rechecks
both source and target after confirmation. Its retirement update targets the
source ID with expectedRevision; a concurrent source edit is refused, without
an unguarded fallback. Target validation and edge creation are not atomic.
Unknown/partial link writes are inspected before retry;
--by never allocates a second replacement node.

Both supersede paths rewrite the old abstract to
`SUPERSEDED (<YYYY-MM-DD UTC>) by <citation> (<successor name>); do not apply. Historical: <summary>`.
The history defaults to its stored abstract verbatim; --summary replaces it.
The abstract replacement re-fingerprints the body in that update. The existing
create path retains its retirement body note and subrole behavior.
A same-successor lead leaves the abstract unchanged; missing edge/tag facts
are completed. Only a matching lead plus tag and edge is a full no-op
(JSON adds `unchanged:true`);
a different-successor lead is refused. Existing-target results add
`existingSuccessor:true`; existing keys/edge status and retired semantics stay.
After a lost retirement response, success requires both the tag and exact
requested abstract on read-back; a visible tag alone returns an error.
A lead/history exceeding 2000 UTF-16 units is refused; supply a shorter
--summary instead of truncating history. `spec lint` warns with rule
`superseded-abstract` if the tag says superseded but the abstract never mentions
it or carry a RESCINDED/WITHDRAWN lead. Strict lint promotes this warning to
exit 5, including on legacy supersessions. Inspect the sole successor, then
rerun --by to repair a legacy abstract under the source revision guard;
never rewrite a rescinded/withdrawn tombstone as a supersession. Explicit
--summary "" keeps empty history. Already-retired conflicting state is exit 5
(previously 2 on the create path). No automatic backfill runs. This rule is
independent of existing length, body-fingerprint and other lint.

A `superseded-by` edge whose target you cannot read still counts as a
successor, so it blocks retirement rather than vanishing from the count.

A rerun on a spec that already has a `superseded-by` edge finishes the
retirement, but only when there is exactly **one** successor. With more than
one, even on a spec already tagged `superseded`, it exits 5 and retires
nothing.

Edge `status` values are `planned` (dry run), `created` and `unknown`. There is
no `failed`: no answer this command gets can prove an edge is absent.
The result's `retired` is `true` only once the old spec has actually been
tagged `superseded`, `false` when the update was refused outright, and `null`
when that can't be known (the retirement update got no answer, and the re-read
didn't show the tag, which may just be a stale read). Branch on it, not on the edge list: a partial run can have created
everything and still not retired anything. A retirement update that gets no
answer is re-read before it is reported, so a committed retirement is never
reported as `false`.

`spec rescind` (cli#809) retires a spec **without a successor** (`supersede` always means one
exists): the `superseded` tag, a `Rescinded ... no successor` banner at the top of the body
(`--withdrawn`: `Withdrawn, never in force`) and a `RESCINDED (<date>); do not apply. <reason>
Historical: <summary>` abstract, in **one** guarded `updateNode` (node id + the revision read), with
no edge, allocation or mint. It refuses before any write a non-spec node, a spec that already has a
`superseded-by` edge (exit 2, naming the successor and `spec supersede`), a spec tagged `superseded`
whose abstract carries no RESCINDED/WITHDRAWN prefix (exit 5), an abstract past 2000 characters (counted as the server counts them, in UTF-16 code units), and a
missing `--reason` or empty `--summary` (exit 2, before any request). A stale revision exits **5**
(`NODE_WRITE_CONFLICT`) with nothing written; rerun to start from a fresh read. Rerunning on an
already-rescinded spec writes nothing and reports `alreadyRescinded: true`. A write that gets no answer
is re-read: the tag **and** the prefix prove it (`retired: true`), anything less exits 1 with `retired:
null` ("unknown"). `--json`: `loc`, `memoryId`, `nodeId`, `revision` (the revision the write is guarded
on), `retired`, `wording`, `abstract` (the one written, or planned with `--dry-run`; the STORED one for an already-rescinded spec, whose `wording` and `withdrawn` are the stored ones too), `withdrawn`, `dryRun`, `alreadyRescinded`, `tags`. A result that cannot be written to stdout exits non-zero (when the write landed, the error says so and names the `spec get` check). Rescinding ends the node's
approval (the abstract is hashed content); re-approval or re-mint is a separate step.

`spec new`, `spec extract` and `spec supersede` cannot leave a spec without its
table-of-contents / inheritance edges unless told to: without `--no-edges`
(new/extract only; it deliberately creates the node edge-less), each spec node
is written together with those edges and (extract) its cross-ref, and a target
that does not resolve refuses the command before anything is written. A `spec new` that creates several nodes (a root with its
contract, or `--new-path`) writes each separately, so a failure part-way exits
non-zero naming the nodes it already created, each complete with its edges.

## Error text from the server (cli#2739)

Server and provider error text reaches you through ONE renderer, which neutralises what a terminal could act on. On stderr (text mode) escape sequences (7-bit `ESC [`, `ESC ]` and the 8-bit C1 forms) are removed whole, and every other control, format and separator character (a carriage return, BEL, NUL, bidi overrides, zero-width characters) becomes a space; a newline is kept, since some of the CLI's own messages are multi-line, so a server message can still start an extra line. Ordinary text (accents, CJK, emoji), a server code, a request id and the exit code are unchanged. In the `--json` envelope the `message` keeps the text as sent where the JSON encoder already makes it inert (the C0 controls, an ESC among them, are written as `\u` escapes and decode to the original) and replaces only what the encoder leaves raw: the C1 controls and format characters (bidi overrides, zero-width characters). Do not print a decoded `message` to a terminal without your own filtering.

## Command surface (v1)

```
hadron auth login | logout | whoami | status | token create|list [--include-hidden]|validate|revoke <id>|delete <id>|hide <id>|unhide <id>   # the `auth token create --help` capture example checks GNU ln -T before minting; after any non-zero exit, run `hadron auth token list`, find any unexpected token by its label, then `hadron auth token revoke <id>` (add --yes when non-interactive)  # token delete <id> removes your own key for good, live or revoked (a live one is revoked first with its live subtree; keys it minted directly stay, revoked, parentless): exit 2 without --yes when non-interactive and nothing is sent, one request never retried or replayed (a 307/308 redirect is refused, not followed), unknown/foreign id exit 4; --json = id,keyId,keyPreview,revokedKeyIds,mintedKeyIds,deletedAt; revoke keeps the row, delete removes it  # token hide|unhide change DISPLAY only and only your own REVOKED keys (an active key is refused, exit 2, KEY_ACTIVE; foreign/unknown id exit 4); list --include-hidden also shows hidden keys and every key's JSON has hiddenAt (null when visible)
hadron auth grants [--limit N] [--offset N] [--json]   # cli#2021: one page of grants
  directly naming the signed-in person as holder, across Apps/organizations, every state,
  newest first; not grants only issued/requested for others. Uses current credential without
  the worktree worker-session header; --app does not scope this read. Default limit 20,
  offset 0, server caps rows at 200 (larger valid limits pass through), both flags
  0..2147483647. --limit 0 = {total} only. Otherwise JSON
  {items:[],total,hasMore,nextOffset}; items preserve
  id,appId,action,values,holderKind,holderId,startsAt,expiresAt,maxUses,useCount,reason,createdAt,activatedAt,revokedAt,allows,signatureStatus{state,integrityProblems}.
  Raw facts only: no derived lifecycle label. hasMore/nextOffset are page metadata;
  nextOffset null at end, never auto-fetch. Empty text is neutral because App keys, runs and
  impersonation also receive empty pages; auth whoami inspects the current credential.
  Missing credential exit 3; server without myGrants validation exit 2; other failures map
  normally, never empty success. No signatures/signer identities/manager-only end details
  requested. Read-only: signing needs the person's passkey on the server browser ceremony
  page; no CLI activation. Manager revoke/withdraw have separate authority and confirmation
  flows (cli#1846), not this listing. Existing top-level grant group unchanged. auth grants
  human count: total matching: N; page footer: total matching: N; rows on this page: R;
  offset: O; requested limit: L; has more: B. The next-offset line is retained. Limit 0
  returns the total matching and no rows. Offset pages are not a snapshot; grants added or
  removed between calls can shift later pages.
hadron auth passkey list [--json]   # cli#878 part 1: your OWN passkey records, newest first, revoked included; READ ONLY (no revoke verb: removal is the server's interactive revoke page; `auth passkey revoke-page` prints the address the server advertises, and the CLI builds no URL). Reads GET /capabilities first: without `passkey-registration` (switch off, older server, failed discovery) exit 2 with ZERO GraphQL requests, even with no credential: "this server does not offer passkeys" when the capability is absent, "could not confirm that this server offers passkeys (<why>)" when discovery itself failed (unknown, not no). App key exit 3 (serverCode UNAUTHENTICATED); impersonation exit 8 (FORBIDDEN). --json = [] or array of id (record id, NOT the credential id),label,state,registeredAt,lastUsedAt,revokedAt,window{endsAt,endedAt,outcome,registrationMethod}|null,cancel{at}|null,transports[],backupEligible,backedUp; never a credential id, public key or cancel IP/user agent. state is derived on the CLI clock: revoked (revokedAt set) > cooling-off (window not ended, endsAt ahead) > active; it is display, not authority
hadron auth passkey revoke-page [--open] [--json]   # cli#878 revoke-page handoff: prints ServerInfo.passkeyRevokeUrl (the server's fixed, parameterless interactive revoke page) VERBATIM; never revokes, never builds or joins a URL, no same-origin check against --server (the value is the deployment's canonical baseUrl). No positional. No sign-in needed (public client; an existing credential still rides under the usual transport rules). Order: `--open` with --json, or `--open` when stdout is not a terminal, exit 2 before ANY request (no /capabilities read either); then refuse an insecure --server with exit 2 before discovery (https, loopback http, or HADRON_ALLOW_HTTP=1 permitted, for print and --open); then GET /capabilities exactly as `list` (absent `passkey-registration` or failed discovery: exit 2, zero GraphQL); then ONE query `PasskeyRevokeUrl` selecting only serverInfo{passkeyRevokeUrl}, no mutation. A validation envelope naming only passkeyRevokeUrl (an older server, or the switch went off since discovery) exit 2 "this server does not provide a passkey revoke page (it may be an older server, or passkey registration was switched off since the check)"; other validation keeps the skew advice; transport exit 7. The value must be an absolute http(s) URL with a host, no userinfo/opaque form, no leading '-', no control/format character, and pass the CLI's transport rule (https, http to loopback, or HADRON_ALLOW_HTTP=1), else exit 1 "the server returned an address that is not a valid, secure web address, so it was not printed or opened; tell the server operator" and nothing is opened. A null serverInfo, missing address or empty string exits 1 "the server did not return a passkey revoke page address". Text = the URL alone on one line; --json = {"passkeyRevokeUrl":"<url>"}. No auto-open: only an explicit --open in a terminal asks the default browser through the platform launcher (argv, no shell), once, then prints; launcher failure exit 1 with the URL in the message. A launcher start is not proof the page loaded or anything was revoked
hadron usage activity [--from <iso>] [--to <iso>] [--bucket DAY|WEEK|MONTH] [--memory <ref>]... [--time-zone <IANA-zone>]  # omitted flags stay omitted; no zero-fill, client resource-count cap or fallback from empty resource scope (cor:acl:090:01); JSON [{date,reads,writes}]
hadron usage events [--memory <ref>] [--loc <loc> | --node <PK-or-URN>] [--type <type>] [--since <iso>] [--until <iso>] [--app-key <id>] [--limit 0-200] [--offset N]  # cli#1203: --app-key is one pass-through flag over the server's appKey (an App key's id, sent unchanged; never resolved or looked up client-side, never echoed); omitted flag = byte-identical request. No resource named = your own activity; a memory = every caller's events there; --app-key = that key's events in memories readable now, only for the key's App manager or the key itself; a manager's read excludes the key's no-memory events, the key itself also reads its own no-memory events. A key you may not filter by is APP_KEY_NOT_FOUND exactly as an unknown key (exit 4); any other refusal exits non-zero via the usual mapping, never as an empty result. Rows name no key, so there is no unattributed cell
hadron usage record --type <type> [--node-loc <loc>] [--session <id>] [--model <name>] [--tokens-in N] [--tokens-out N] [--action-args <json>]  # WRITE, cli#1409: one request, no retry or read-back; omitted flags are omitted, explicit 0 is sent; tokens are signed GraphQL Int sent unchanged; non-integer, out-of-range or invalid --action-args JSON exits 2 before any request; text "Usage event recorded", JSON {"recorded":true} identical whether or not the node or session was attributed (cor:dmo:080:02: an unreadable loc is recorded without attribution); --session on a live session you own counts as its activity, an App key can resume its own lapsed (never dormant) session, an owned ended/expired/idle session takes the record late without revival or activity; explicit empty strings are forwarded, only --type is required
hadron memory loc-segments --memory <id-or-URN> [--memory <id-or-URN> ...] [--parent <loc>] [--limit <n>] [--after <cursor>] [--json]
# One page of next loc segments (including node-less branches), not a recursive tree. --memory is repeatable without -m and never comma-split; explicit nonempty values and parent go unchanged, with no ambient scope/existence read. Limit and after are omitted unless supplied; positive limit is forwarded (server default20/cap200). Rows keep server order and source memory, including inherited memory ids; equal segment names remain separate. JSON: {items:[{segment,hasNode,memoryId}],hasMore,endCursor}, [] for empty, nullable cursor. Text columns MEMORY ID / SEGMENT / HAS NODE; only hasMore adds stderr `more segments remain — continue with --after <cursor>`, the cursor shell-quoted when it holds anything outside `a-zA-Z0-9:._/@-` (JSON carries it raw). Repeat the same scope with that opaque cursor. No auto-drain or snapshot guarantee. Unreadable/unresolvable/empty scope shares the identical empty answer. Null/missing items, null/empty-identity rows, missing/empty continuation when more, and control-altered cursor refuse before output, no fallback. Typed bool decoding cannot distinguish hasNode:null from false (pinned); no raw decoder. Old-provider unknown-field errors use existing schema-skew mapping.

hadron memory list [--shared-with-me | [--owned-by-me] [--include-agent-system] [--org <ref>] [--visibility ORGANIZATION|GROUP|PUBLIC]] | get <id-or-urn> (cli#1014: shows the stored last-sync outcome, Memory.lastSyncReport v1 or v2; `--json` adds `lastSyncedAt`, `syncError` (URL credentials masked), `syncReport` (the parsed object {version,outcome,recordedAt,entries[{kind,loc,fromLoc?,code}],code?} — the wire version is carried through; v2 adds the `attestation_refused` kind with AUTHORING_ATTESTATION_MISSING, AUTHORING_ATTESTATION_WRONG_TASK, AUTHORING_ATTESTATION_STALE, AUTHOR_TASK_BROKEN, closed per version so v1 refuses that kind; every entry; unknown kind, code, code belonging to another kind or report-level code makes the whole report unavailable) and `syncReportStatus` recorded|none|unavailable; an unreadable report is `syncReport: null` + `unavailable`, never clean; text prints a `sync:` block only for a memory with a source or sync history, quoting server strings and capping at 50 entries) | set [<id-or-urn>] [--org <ref> | --owner-me | --app <ref> [--agent <ref>]] [--class <c>] [--max-rev-count <n>] [--category0|--category1|--category2|--license|--icon-url|--hero-url|--home-url|--git-source|--read-branch|--write-branch <v>] [--data <json>|--data-file <path>] [--embedding-source abstract|contentChunks|both] [--vector-index|--no-vector-index] [--chunk-tokens <n>] [--chunk-overlap <n>] [--force-fixed-size|--no-force-fixed-size] [--acknowledge-vector-inversion-risk] [--draft-corpus] [--schema <json> | --schema-file <path>] [--listed-on-marketplace[=false]] (update only, cli#592: the opt-in marketplace catalogue flag, cor:acl:080:04, advertisement not access; omitted preserves it; `get` shows it as `marketplace: listed|not listed` and `--json` `listedOnMarketplace`, also on a `set` update's receipt) | attach <memory> --app <ref> --agent <ref> | set-active <id-or-urn> | rm <id-or-urn> | clone <id-or-urn> --target-urn hrn:mem:<root>:<slug> | transfer <memoryRef> (--org <ref> | --user <ref>) [--class <c>] [--reset-group-members] [--apply | --yes] | extract <parentRef> <targetUrn> [--move] | export <id-or-urn> [--out <dir>] | member list|add|set-role|rm <memory> --user <id> [--role <r>] | share list|create|set-role|revoke <memory> --grantee <user-ref> [--role <r>] | subscription list|create|set-role|rm <memory> --org <id> [--role <r>] | encrypt <memory> (--data-key - | --passphrase-file <file|->) | merge <memoryRef> --into <memoryRef> [--field CONTENT|ABSTRACT|DESCRIPTION|TAGS|DATA|PROPERTIES|EDGES]... [--delete-source] --yes | verify <memoryRef> | link-user <memoryRef> --external-user <id> [--data-key -] --yes | approve-all -m <memory> [--yes] | validate <memoryRef> [--check <kind>]... [--limit N] [--fail-on-findings] | reembed-failed <memoryRef> | config get <memoryRef> | config rule add|update <memoryRef> <role> [--author-task <ref>] [--validation-task <ref>] [--description-node <ref>] [--writers all|admin|owner] [--validate-by agent|platform] [--strict-sub-roles[=false]] [--enabled[=false]] | config rule rm <memoryRef> <role> --yes | config rule unlock <memoryRef> <role> [--yes] | config template list [--owner-server | --owner-org <ref> | --owner-me | --owner-app <ref>] | config template get <templateId> | config template create --file <path> (--owner-server | --owner-org <ref> | --owner-me | --owner-app <ref>) | config template update <templateId> --file <path> [--expected-revision <n>] | config template rm <templateId> --yes | config template apply <templateId> [<memoryRef>...] [--memories-file <path|->] [--dry-run] [--expected-revision <n>] [--yes] | sync pull <memoryRef> [--yes] | sync set-token <memoryRef> [--token-file <path|->] [--expires-at <rfc3339>] (cli#2511: write-only source token read from a file or stdin, never argv; stores only, starts no pull or push; never in output, --json or errors; --json {memory,operation:"set-token",tokenSet,expiresAt}) | usage <memoryRef>
# node list inheritance (cli#1708): server-provided canonical urn is retained; inherited rows add inheritedFrom:{agentId,agentUrn} in JSON. Mixed/inherited tables add URN / INHERITED FROM; projected blocks add metadata lines, own-only table layout stays unchanged. Source identity is never inferred from memoryId or composed under the requested child. Base addresses do not grant base read/write access; writing through a child still makes its own override. Both list documents and seq/projection modes preserve this metadata, without per-node lookup or changing scope/refusal rules.
hadron node list [--has-unresolved-comments[=false]] [-m <memory>] [--prefix <loc>] [--children-of <loc>|""] [--type <t>] [--types <t,t>]... [--object-type <t>] [--tag <t>]... [--where <json>] [--sort-property <json>] [--with-properties] [--with-data] [--sort-seq asc|desc] [--seq-gt N] [--created-by <user>] [--role <family>] [--updated-after <rfc3339>] [--updated-before <rfc3339>] [--include-deleted] [--memory-class <c>]... | get <urn>... [--raw] | get <loc>... -m <memory> [--raw] | get <urn>|<loc> -m <memory> --body-only | get --prefix <loc> -m <memory> | add [--allow-empty] [--type <t>] [--object-type <t>] [--data <json>|--data-file <path>] [--properties <json>|--properties-file <path>] [--edge <name>=<target>|<json>]... [--id <id>] | update <urn> [--expected-revision N] [--content <text|-> | --content-file <path|-> [--allow-empty]] [--force] [--dry-run] [--type <t>] [--object-type <t>|""] [--data <json>|--data-file <path>|--data-merge <json>|--data-merge-file <path>] [--properties <json>|--properties-file <path>] [--edge <name>=<target>|<json>... --replace-edges] | set-properties <urn>|<loc> -m <memory> (--patch <json>|--patch-file <path>) [--reason <text>] | move <urn> (--to-urn <urn> | --to-memory <memory>) | clone <urn> (--to-urn <urn> | --to-memory <memory>) | merge <urn> --into <urn> [--field <f>]... [--delete-source] --yes | rm <urn> [--hard [--cascade-cross-memory-edges]] [--recursive|-r] | export <urn> [-o <file>] [--format md|json|pdf] [--section abstract|content|data|edges|metadata|properties]... | import <file|-|--url <u>> [-m <memory>] [--with-edges] [--task <ref> [--task-args <json>] [--app <ref>] [--agent <ref>]] | approve <node-ref> [-m <memory>] [--revision N] [--as-worker] | verify <node-ref> [-m <memory>] | mint -m <memory> [--dry-run] [--yes] | sign <node-ref> [-m <memory>] [--revision N] [--signer <user-id>] | signatures <node-ref> [-m <memory>] [--check] | signing-request get <request-id> | signing-request list <node-ref> [-m <memory>] [--state open|completed|declined|expired|stale|withdrawn] [--limit <n>] [--after <cursor>] | signing-request withdraw <request-id> | revision list <node-ref> [-m <memory>] [--limit N] | revision get <revision-id> | revision restore <revision-id> [--truncate [--yes]] | revision label <revision-id> --label <text> | revision delete <revision-id> [--yes] | revision clear <node-ref> [-m <memory>] [--yes] | recovery list -m <memory> [--limit 1-200] [--offset N | --all] | recovery get <node-urn>|<node-id>|<loc> [-m <memory>] [--content] | recovery graph -m <memory> [--edge <id>]... [--limit 1-200] [--offset N | --all] | restore <node-urn>|<node-id>|<loc> [-m <memory>] | next <parent> [--after <current>] [-m <memory>] [--raw] | open <node-ref> [-m <memory>] [--print] | edge-removals <node-ref> [-m <memory>] [--limit N]
hadron object create -m <memory> --type <t> --fields <json>|--fields-file <path> [--key <k>] [--name <n>] | get <ref> | update <ref> --fields <json>|--fields-file <path> [--reason <r>] | delete <ref> [--hard] --yes | find -m <memory> --type <t> [--match <json>] [--where <json>] [--sort <json>] [--limit N] [--offset N]
hadron asset list -m <memory> | --all [-m <memory>]... [--org <org>] [--scan-status PENDING|CLEAN|BLOCKED] [--mine] [--mime <type>] [--include-deleted] [--limit N] [--offset N] | get <asset-ref> [-o <path>|-] [--force] | url <asset-ref> [-m <memory>] | upload <file> -m <memory> [--mime <t>] [--name <n>] [--description <d>] [--agent-request <id>] | rm <asset-ref> [--yes] | restore <asset-ref> | public-link <asset-ref> (--on | --off) [--yes] | link <asset-ref> --node <new-node-urn> [--name <n>] [--description <d>]
hadron task run <task-urn>|<loc> -m <memory> [--arg k=v]... [--app <ref> [--as-self] [--agent <ref>]]
hadron task render <node-ref>|<loc> -m <memory> [--args <json-object|@file>]
hadron chat read [--since <seq>] [--node <urn> | -m <memory> --messages-loc <prefix>] | post (--body <text|-> | --body-file <path>) [--node <urn>] [--session <id>] [--reply-to <seq|loc>] | config show [--node <urn> | -m <memory> --messages-loc <prefix>] (offline, exit 0) | config set (--node <urn> | -m <memory> and/or --messages-loc <prefix>) (the only config writer; edits just those chat.* keys, backup + atomic, exit 2 and nothing written on a bad file) | doctor [same coordinate flags] (read-only Channel check, report always printed; exit 0 ok, else first failing check: 2 bad config/coords, 4 Channel unavailable, or a failed read's own code)
hadron channel list [--owner-app <ref>] [-m <memory>] | get <id|address> | create <name> -m <memory> --loc <loc> [--description <d>] | update <id|address> [--name <n>] [--description <d>] | rm <id|address> [--yes] | read <id|address> [--since <seq>] [--before <seq>] [--limit N] [--offset N] [--mentions <ref>] [--thread <seq>] [--team-head <id>... | --untagged | --tagged] | post <id|address> <body|-> (--session <id> | --as-me) [--reply-to <seq>] | mark-read <id|address> --attendee <ref> --seq N [--owner-app <ref>] | read-state <id|address> --attendee <ref> [--owner-app <ref>] | register list [--channel <ref>] [--attendee <ref>] [--owner <ref>] [--org <id>] [--limit N] [--offset N] | register add --channel <ref> --owner <ref> (--attendee <ref> | --all-attendees) [--role both|post|watch] [--mention-only] [--description <d>] | register set <entry-id> (--role <r> | --mention-only[=false] | --description <d>)... | register rm <entry-id> [--yes] | search <id|address> <query> [--limit <n>] [--cursor <c>]   # post REQUIRES --session or --as-me (the server records the human silently otherwise); read --since is strictly-greater and the output reports nextSince; read --thread <seq> = the DIRECT replies to that message (server-filtered; unknown seq exit 4, reply-less message an empty page); a ref is the Channel id OR its address (chatRootUrn, printed by list); chatRootUrn is NULL for some Channels — the id always works; list/get/create/update preserve readable Channels with null host memory: memoryUrn and memoryName are explicit JSON null, text says "not readable", memoryId unchanged, no extra memory lookup; read-state exits 0 with lastSeenSeq 0 for a never-read attendee and 4 for an attendee that is unknown, unreadable or not registered on the Channel (one message for all three).
# channel get records a node read and may decrypt internally during its optional authorized root read; identity/URL selection does not imply no provider read effects. It also emits a best-effort chat-root node portalUrl (nullable JSON key; text URL: only when supplied). One authorized node read by chatRootNodeId; missing/concealed/failed pointer read gives null without failing the Channel read. List is unchanged; no transcript fetch. Portal-tab destination and default-team shortcut are not delivered by this pointer.

hadron search <query> [-m <memory>]... [--scope <name|id|app|global>] [--mode hybrid|keyword|vector|regex] [--prefix <loc>] [--type <type>] [--object-type <t>] [--tag <t>]... [--where <json>] [--sort-property <json>] [--with-properties] [--with-data] [--comments-only] [--expand 0-3] [--field <f>]... [--granularity node|chunk] [--min-score <x>] [--limit N] [--offset N] [-l|--long] [--json]
hadron replace text <old> <new> --field <f> (--node <urn> | -m <memory>) [--prefix <loc>] [--regex] [-i] [--dry-run] [--yes] [--max-nodes N]   # --json results is [] (never null) when no nodes change, for both --dry-run and a real no-op; --json also carries nodesSelected and nodesSkipped (always present, 0 included), and text mode prints "Skipped N node(s): ..." under the totals only when N > 0
hadron edge list <node-urn> | <loc> -m <memory> | <node-id> [--direction incoming|outgoing] [--name <substr>] [--to <ref>] [--from <ref>] | list [-m <memory>] [--source <node>] [--target <node>] [--runnable[=false]] [--include-deleted] [--org <org>] [--limit N] [--offset N] | get <edge-id>|<edge-urn> | add | update <edge-id> | rm <edge-id>
hadron spec label-report [-m <memory>] [--prefix <citation>] | list [-m <memory>] | get <citation> [--body-only] |--prefix <prefix> | describe | use [<memory>] | register [--check] | find <query> [--match-exactly] | grep <pattern> [--regex] [-i] [--field content|abstract] [--prefix <loc>] | replace <pattern> <replacement> [--regex] [--word-boundary=false] [--field content|abstract] [--dry-run] [--yes] [--max-specs N] | new [<loc>] ... | edit <citation> [--content-file <path> [--allow-empty]] [--title <title>] [--force] [--dry-run] [--expected-revision N --expected-node-id ID --expected-proposal-hash HASH] | extract <citation> --to-feature <fff> | link <from> <to> | lint [<citation>] | check-tools [--prefix <loc>] | citations [--src <path>]... [--exclude <glob>]... [--loose] [--stale-abstracts] [--strict] | ledger [-m <memory>] [--registry <file>] | supersede <citation> (--title <title> [--to <loc>] | --by <existing-citation>) [--summary <text>] | rescind <citation> --reason <text> (--summary <text|-> | --summary-file <path>) [--withdrawn] [--yes] [--dry-run] | import spec-kit|code | reserve <citation> [--name <name>] | renumber <from> <to> [--dry-run] | document <citation> [-m <memory>] [-o <file>] | backlinks <citation> | unresolved | mint [--dry-run] [--yes]
hadron question log create <memory> --loc <loc> --decider <user-id>... [--eligible <user-id>...] [--name <n>] [--prefix <p>] | log list (--app <app> | --memory <memory>) | log get <log> | log set-deciders <log> --decider <user-id>... [--eligible <user-id>...] --expected-revision <n> --reason <text> | ask <log> --input-file <path|-> [--title <t>] [--background <b>] [--tier ANSWERED|APPROVED|SIGNED] [--priority LOW|NORMAL|HIGH|URGENT] [--assignee <user>] | get <question> | get <code> --log <log> | list (--app <app> | --log <log> | --awaiting-me) [--answered[=false]] [--state <s>...] [--priority <p>...] [--tier <t>] [--assignee <user>] [--unassigned] [--asked-by-me] [--asked-by-worker <id>] [--asked-by-app <id>] [--answered-since <rfc3339>] [--authorizes[=false]] [--sort PORTAL_DEFAULT|CODE|ASKED_AT_DESC|ASKED_AT_ASC|PRIORITY_DESC|ANSWERED_AT_DESC] [--limit N] [--offset N] | amend <question> --expected-revision <n> --reason <text> [--input-file <path|->] [--title <t>] [--background <b>] [--tier <t>] [--priority <p>] [--assignee <user>] | answer <question> --expected-revision <n> [--option <key>] [--text <t> | --text-file <path|->] [--reason <t>] | retract <question> --expected-revision <n> [--reason <t>] | withdraw <question> --expected-revision <n> --reason <text> [--yes] | verify <question> | purge <question> --reason <text> [--yes]   # cli#2465 (replaces `decision`, cli#1335), hadron-server#1641 S1-S4: one call per verb, refs verbatim, authority server-owned; `hadron decision` is retired (prints one sentence, exit 2, sends nothing); see "Questions"
hadron decision [any arguments]   # RETIRED (renamed to `hadron question`, hadron-server#1641, cli#2465): prints "hadron decision was renamed to hadron question (hadron-server#1641). Nothing was sent.", exits 2, reads no config and sends nothing; not an alias
hadron skill lint (-m <memory>... | --all | --node <ref>...) [--strict] [--json] | status (-m <memory>... | --all) [--host <host>] [--to user|project|plugin|<dir>] [--strict] [--json] | export [--node <ref>...] [--dry-run] [--force] [--prune] [--json] | plugin --out <dir> [--name <name>] [--scope <name|id>] [--zip] [--dry-run] [--json]
hadron coding review run [-m <memory>] [--base <ref>] [--head <ref>] [--diff <path|->] [--root <loc>] [--all] [--limit N] [--offset N] [--json] | review list [-m <memory>] [--root <loc>] [--broken] [--json] | review create <check-name> [-m <memory>] --trigger <cond> --description <d> [--scope <s>] [--tag <t>]... [--link <ref>[=<label>]]... [--seq N] [--content <text|-> | --content-file <path>] | review lint [-m <memory>] [--root <loc>] [--toolchain <t>|-] [--strict] [--suggest] [--fix [--yes]] [--json] | preflight list [-m <memory>] [--root <loc>] [--broken] [--route-model leaf|mirrored] [--json] | preflight create <loc> [-m <memory>] --route <action> --description <d> [--name <n>] [--symptom <s>] [--section <heading>] [--type <t>] [--tag <t>]... [--link <ref>[=<label>]]... [--seq N] [--content <text|-> | --content-file <path>] [--no-back-edge] [--no-body-line] [--route-model leaf|mirrored] [--attach-to <loc>] [--dry-run] | preflight route <node-ref> [-m <memory>] --route <action> [--description <d>] [--symptom <s>] [--section <heading>] [--no-back-edge] [--no-body-line] [--route-model leaf|mirrored] [--attach-to <loc>] [--dry-run] | preflight lint [-m <memory>] [--root <loc>] [--strict] [--route-model leaf|mirrored] [--json]
  Cross-memory marker (cli#2736): `coding preflight route <node> --route <r> --cross-memory` stores `{"crossMemory":true}` in the route edge data (route edge and the mirrored back-edge; never the edge name), refuses a target in the router's own memory (exit 2, nothing written), and on an EXISTING route adds the marker through updateEdge keeping the other data keys. `preflight lint` then skips only `route-target-moved-memory` for a marked route (both the router's outgoing route and the leaf model's incoming edge); unresolved, retired and label rules still apply. `preflight list` shows a MARKER column (`cross-memory` or `-`) and `--json` rows carry `crossMemory` (always present); `route --json` carries `crossMemory` only when set. `preflight create` takes no such flag (it always creates in the router's memory).
  Route model (cli#1025): `--route-model leaf|mirrored`, else `preflightRouteModel` in .hadron/config.json, else `mirrored` (the default, byte-identical to before; flag > config > default; a value other than the two, or an unparseable config, is exit 2). `leaf`: `create`/`route` write exactly ONE edge, the node -> the router (or `--attach-to <loc>`, an area node), labelled `to <action>`; nothing on the router's outgoing set, no back-edge (`--no-back-edge` and `--no-body-line` are no-ops), body line OFF unless `--section` is given (it then splices into the attach target; `--section` with `--no-body-line` is still exit 2); `--attach-to` under mirrored is exit 2; `--json` gains `model:"leaf"` and `router` is the attach target. `list`/`lint` under leaf read the router's INCOMING edges plus its outgoing (legacy) ones: list rows gain `model` `leaf|legacy` (one row per node, leaf wins; absent under mirrored); lint adds `legacy-outgoing-route` and `leaf-missing-edge` (a `[[link]]` in the router's OWN body with no edge to it; area bodies are not scanned) and carries label/retired/moved-memory to the incoming edges, and its human output ends with `N incoming edges visible to this caller` (never in --json). Every incoming edge counts as a route, so a non-`to` label is a `route-label-phrasing` warning.
hadron app agent customize <app> <agent>   # read install-memory metadata only, never create it; JSON memory may be null (unavailable/uncreated), not a node read
hadron app member roles [--app <ref>] (cli#1226, R211 cor:dmo:050:04: App.availableMemberRoles, the roles `member add --role` accepts — the union of the App's installed Agents' member roles, server order; text one per line, `--json` a bare array, `[]` when none) | agent compile <app> <agent> [--as-worker] | get <app-ref> | key list <app-ref> [--scopes [--host-uses <n>]] [--instruction <kind>:<ref>] | key create <app-ref> --out <file> [--label <t>] [--force] [--print-key] | key revoke <key-id> [--yes] [--instruction <kind>:<ref>] | key rotate <app-ref> --out <file> [--yes] [--force] [--print-key] | agent list [<app-ref>] (uses --app) | agent add <app> <agent> [--training-mode] | agent remove <app> <agent> --yes | agent set-boot-prompt <app> <agent> (<node-ref> [-m <memory>] | --clear) | list [--org <org> | --owned-by-me] [--agent <agent-id-or-URN>] | install (--org <id> | --owner-me) --agent <ref> --name <n> [--type <t>] [--urn <slug>] [--description <d>] | uninstall <ref> [--yes] (cli#1917, BREAKING: no longer deletes; uninstallApp, cor:dmo:050:21: kept read-only, writes refused APP_UNINSTALLED, handle freed; repeat prints the same state) | reinstall <ref> (reinstallApp: the SAME App, id/keys/members/memories, nothing created; APP_URN_IN_USE exit 5 while another installed App holds the handle) | delete|rm <ref> [--yes] (deleteApp, the old `app uninstall`: no longer serving, kept, no undelete; name an UNINSTALLED App by id, since its former URN may name another installed App; a repeat delete by id is refused APP_DELETED; a URN resolves afresh each call: FORBIDDEN while no installed App holds it, but it names and deletes another App once that App reclaims the handle, so delete and retry by id) — all three: id or URN sent unchanged; org:slug normalized to hrn:app:org:slug, no preflight (for uninstall/reinstall a URN names the installed holder, else the one uninstalled App with it; for delete a URN names only the installed holder, so delete an uninstalled App by id); JSON {app,status} with status the resulting state deleted|uninstalled|installed; APP_DELETED exit 5; --json error envelope carries the server code as serverCode; a missing App and a refused caller get one identical FORBIDDEN (exit 8) | join <app-ref> | leave <app-ref> [--yes] [--instruction <kind>:<ref>] | set-active <ref> | key scope grant <key-id> <scope> [--host-id <host>] | key scope revoke <key-id> <scope> [--instruction <kind>:<ref>] | model-group list <app-ref> | model-group set <app-ref> <name> [--pattern <glob>]… | model-group clear <app-ref> <name> [--yes] | chatbot-ai-config get|list|clear <app-ref> | chatbot-ai-config set <app-ref> <name-or-id> (cli#1913, hadron-server#3957, cor:dmo:050:10/:12: the App's chatbot AI config pin, an id of one of the App's OWN configs. `get` and `app get` show `chatbotAiConfigId`, the server-returned `chatbotAiConfigName` and `chatbotAiConfigListed` (JSON; `chatbotAiConfigListed` false when unset or when the pin is not among the App's enabled choices, which the text says and never calls in use, and null when the choices read failed so membership is unknown; a null App is exit 4 and the explicit `get` fails on a server without the field; the name comes from the choices list, never the Agent/Org/Host walk; a server without the field still serves `app get`). `list` = the narrow chatAiConfigChoices (App-own enabled configs + a `default` marker that is not a config and has no id; JSON array of {value,name,isDefault,isAppChatbotConfig,provider,model}), NOT `ai-config list`. `set` matches an exact id, else an exact name, among those choices only; an unmatched value is sent as given and the server refuses missing/foreign/deleted alike; the argument is sent as given (not trimmed); the word `default` (never matched by name, a config so named is set by id), an ambiguous name and an empty value exit 2 with no mutation. `clear` sends an explicit null and removes only the selection. One mutation, never retried; the server owns manager authority; no key, preview or endpoint is ever requested) | user create --external-id <id> [--handle <h>] [--name <n>]   # App key details on the indented lines below
  App key scopes: attention:deliver, host:operate; grant is person-only; revoke also admits the manager's held worker with --instruction (cli#1595); host:operate names ONE host (exit 5 APP_KEY_SCOPE_HOST_CONFLICT: revoke first); scope-write output never carries key material
  App key create: `key create` (cli#1223) writes the raw key to --out (new 0600 file, opened BEFORE the mint: an existing path/symlink is exit 2 and nothing is sent; --force = same-dir temp + rename, symlink replaced not followed;
    on windows 0600 only clears the read-only attribute, so the file is created with a protected DACL granting only the current user and SYSTEM, set at create and kept by --force (cli#1325)), never to output unless --print-key (refused with --json, exit 2);
    an explicit empty or whitespace-only `--label` is exit 2 before the file is opened or anything is sent (omit --label for the server's default); the lost-answer message says "no file was kept" only when the cleanup removed it; --json {id,keyPreview,label,out}; a refused mint removes the file it created;
    createAppKey is sent once and never retried: a lost answer (exit 7/1) names the label and says to run `app key list <app-ref>` and revoke any unexpected key; write-after-mint failure names the key id and `app key revoke <key-id>` (exit 1); a new key holds no scope, so follow with `app key scope grant`;
    a person who manages the App mints, as themselves (App key / impersonation / worker session refused, exit 8)
  App key list: `key list --scopes` (cli#1333) pages through `appKeysPage` to the end, adding each key's `scopes` ([] when none) and, with `--host-uses <n>` (needs --scopes, n>=1, else exit 2), its newest n `hostUses` (absent from --json otherwise); the default list stays metadata-only (#1057)
  App key revoke and rotate: `key revoke <key-id>` (revokeAppKey, irreversible) and `key rotate` (regenerateAppKey: revokes EVERY active key of the App, the new key holds NO scopes so scope grants are redone) both confirm on a TTY and exit 2 with nothing sent when non-interactive without --yes;
    revoke --json {id,revoked}; revoke is never retried: a transport-class lost answer (exit 7), or an answer that is not a definite refusal (an internal server error, an unreadable reply or an unrecognised error: exit 1, serverCode and requestId kept, cli#1441), says the key may already be revoked and names `app key list <app-ref>`; server refusals keep their mapped exit and serverCode;
    rotate keeps `create`'s secret-file contract (--out 0600 opened first, raw key never in stdout/--json unless --print-key, never retried: a lost answer (exit 7/1) says every key may be revoked and to run `app key list <app-ref>`), --json {appId,appUrn,out,allOtherKeysRevoked};
    rotate stays person-only (exit 8); revoke also accepts --instruction, see Instructed credential acts (cli#1595)

hadron app nodes <app-ref> [--prefix <loc>] [--type <type>] [--types <type,...>] [--object-type <collection>] [--tag <tag>] [--runnable[=false]] [--created-by <user-id>] [--role <family>] [--updated-after <RFC3339>] [--updated-before <RFC3339>] [--include-deleted] [--limit N] [--offset N]   # cli#1626: appNodesPage over the App's readable node scope; required App ID/URN (or root:slug), one page ordered loc then id across memories; default20, cap200,0/negative count-only,negative offset0; table loc/name/type/memory ID and stderr total/more/next-page edit; JSON {items:[{id,memoryId,loc,name,nodeType}],total,hasMore}, [] not null. Memory filters, children-of and where are local usage2/no request: use app memories then node list -m for those. No content/properties/data fan-out, no writes; server not-found/permission refusal unchanged (4/8 by code).
hadron app list --agent <agent-id-or-URN> [--org <org> | --owned-by-me]   # cli#1626: filter.agentId matches the installed-Agent join, not a primary-agent label; no Agent pre-resolution; an unreadable/missing Agent gives []; filters AND, no active-org inference; follows pages <=20 to the complete selected list, existing bare-array --json unchanged; empty --agent is usage2/no request.
hadron app memories [<app-ref>] [--limit N] [--offset N] (uses --app)   # cli#1209 over server#2190 appMemoryScope (cor:dmo:050:17): the memories App-wide reads (appNodes) cover for you, one row per memory with every source kind (AGENT_SYSTEM|AGENT_LINK|INHERITED_BASE|TEAM_SPACE|APP_MEMORY; APP_MEMORY is the App's own tenant memory) and the agent name only when you may read that Agent; one page ordered by memory URN, --limit 1..200 (default 20), next-page hint on stderr; WRITE (ADVISORY) yes|no|inherited, "inherited" = viewerCanWrite true but in scope only as an inherited base, so not a write target; --json {items[{memoryId,memoryUrn,name,class,sources[{kind,agentId,agentName}],viewerCanWrite,writeTarget}],total}; refusals are appNodes' (exit 8/4 by code)
hadron app update <app-ref> [settings flags] [--yes]   # omitted = preserved; no changes = exit 2; confirmation names fields, except --urn names old/new full URNs; JSON {app,id,urn,name,sessionTimeoutSeconds,status,changed}; sent once, no retry; lost answer = exit 7 outcome unknown `--policy-file <path>` sets the App's action-policy link (cli#1640, server#3423/#3425; one JSON value such as `{"allow":["run.execute"]}`), `--clear-policy` removes it (sends null); mutually exclusive, and an empty file, invalid or multi-value JSON, an unreadable path or a JSON `null` is refused locally (exit 2, nothing sent). Neither flag sends no `policy` at all (separate `UpdateAppSettingsPolicy` document), so an older server keeps working; with a flag the older server refuses `policy` as returned, sent once. The confirmation and `changed` name `policy` or `policy (cleared)`, never its value. The server owns who may set it (App owner, or org ADMIN for an org-owned App) and its shape.
  `--role` on install/update is refused (exit 2, nothing sent): the App's own role was removed (cli#2515); a member's `--role` on `app member add` is unaffected.
  Settings on install/update: --create-user-permission DENY|EXPLICIT|IMPLICIT, --identify-user-method SECRET|USER_ID, --session-timeout-seconds <n>, --anonymous-ttl-days <n>, --expires-at <ISO8601>, --training-mode[=false], repeated --agent-tool <string>, repeated --surface <string>.
  Lists replace the whole stored list; strings pass verbatim. Surfaces are descriptive (portal chat tab iff chat is present), not authorization; --type does not change surfaces. Omitted install surfaces use the provider default ['chat'].
  Update also: --urn <slug>, --type <enum>, --session-dormancy-days <n>, --agent-sessions-expire[=false], --clear-expires-at (null), --clear-agent-tools/--clear-surfaces ([]), --name and --description/--clear-description. Explicit false/zero are sent, numbers remain server validated.
  Install also: repeated --install-optional <Agent-ref>. `--policy-file <path>` sets the App's action-policy link on install (cli#1628; the same reader as update's, #1640): one JSON value; an empty file, invalid or multi-value JSON, an unreadable path or a JSON `null` is refused locally (exit 2, nothing sent); it counts as a setting for the confirmation (`policy`, never its value); install has no clear flag, leave it out for no link. Without it the request never mentions `policy` (separate `CreateAppSettingsPolicy` document), so an older server keeps working. New settings require confirmation/--yes; legacy-only install keeps its existing no-prompt behavior. Install JSON stays the seven-field App receipt. The action-policy link is set with `app install --policy-file` and changed or cleared with `app update --policy-file` / `--clear-policy`. Legacy AI shims have no argv flags: use ai-config create/update.

hadron ai-config list [--app <ref>] [--agent <id>] [--with-endpoints]   # cli#1882: each config carries the server-computed `urn` (JSON `urn`, null when the server gives none or is too old; text shows it only when present); an older server without the field still lists
hadron ai-config list (--owner-type APP|AGENT|ORGANIZATION|HADRON_SERVER --owner-id <id-or-urn> | --all-owners) [--limit N] [--offset N]   # cli#1882: each config carries the server-computed `urn` (JSON `urn`, null when the server gives none or is too old; text adds a URN column only when some config has one); an older server without the field still lists
hadron ai-config test <id> [--override-file <path|->] [--chat-probe] [--json]
hadron ai-config get <id-or-urn> (cli#1882: shows `urn`; a URN ref is sent unchanged, never parsed; URN ref the caller cannot read = exit 4 identical to a missing one (cor:api:120:01); an id ref keeps the server's answer incl. FORBIDDEN exit 8; older server: no urn, URN ref = exit 4) | endpoints <provider> | create (--app|--agent|--org <ref>) --name <n> --provider <p> --model <m> [--endpoint <url>] [--api-style CHAT_COMPLETIONS|RESPONSES] [--api-key -] [--file <path>]
hadron ai-config update <id> [--endpoint <url|"">] [--api-style CHAT_COMPLETIONS|RESPONSES | --clear-api-style] ... | rm <id>
hadron org list [--mine] | create --name <n> --urn <urn> | get <id> | public <org-ref> | update <id> [--name] [--urn] [--marketplace] [--policy <json> | --clear-policy] | rm <id> | member list|add|set-role|rm <org-id> --user <id> [--role <r>] | invite create <email> --org <id> --role <r> | invite accept <slug> | invite show <slug> | signing get|off <org> | signing preview <org> [--first N] [--after C] [--all] | signing on <org>   # boot-prompt signing switch (#1879); off|on take [--yes] | github link <org> --installation-id N | github unlink <org> [--yes] | github set-app <org> --app-id <id> [--private-key-file <path|->]   # GitHub App config (#2512); unlink removes ALL of it, confirm/--yes; key write-only | set-active|use <orgRef> [--no-verify]   # "" clears; the active org is what --scope global resolves against
hadron scope list [--owner-org <ref> | --owner-app <ref> | --owner-agent <ref>] [--name <n>] [--app-context <app>] | get <name|id> [--by-name] | create <name> (--owner-org|--owner-app|--owner-agent <ref>) -m <memory>… [--description <d>] | update <name|id> [--by-name] [--name <new>] [-m <memory>…] [--description <d>] | rm <name|id> [--by-name] [--yes] | explain <name|id> [--by-name] [--loc <address>] | set-active|use <name|id|app|global> [--no-verify]   # "" clears; applied when search omits --scope, and reported as scope.selectedBy="config". a NAME needs an App context (--app or the active App); an ID never does. --description cannot clear.
hadron agent compile <agent> [--no-set-pointer] [--as-worker] | list [--org <id> | --owned-by-me] [--type ASSISTANT|CHATBOT] [--visibility ORGANIZATION|PERSONAL|PUBLIC] | list --public [--type <t>] [--limit N] [--offset N] | get <ref> | create --name <n> [--lifecycle LONG_LIVED|SHORT_LIVED] [--org <id> | --owner-me] [--type <t>] [--visibility <v>] [--description <d>] [--system-memory <id>] [--surface <s>]… [--persona-role <r>] | update <id> [<field flags>] [--lifecycle LONG_LIVED|SHORT_LIVED] [--tasks-index <node-ref> | --processes-index <node-ref>] (explicit empty clears) | rm <id> --yes | tasks list <agentRef> [--symptom <text> [--limit N]] (experimental, hadron-server#2421: up to 3 tasks matched on the abstract, adds MATCHED BY VECTOR/KEYWORD/BOTH and `matchedBy` in --json; --limit is ignored without --symptom, sent as given, server rules) | processes list <agentRef> | memory add <agent> <memory> [--role <r>] | memory set-role <agent> <memory> --role <r> | memory rm <agent> <memory> --yes (refs sent verbatim, one mutation each; prints the Agent DTO; the server owns authority and role validation) | ai-config clear <agent> --yes (cli#58: one clearAgentAIConfig, destructive; a user-owned Agent loses every named configuration, an org-owned one only `default`, as the server does it; never reads or prints a key; prints the Agent DTO; NOT the top-level `ai-config` group) | import publish <parent> <imported> --position <n> [--required[=false]] (one publishAgentImport; org-owned parents only, the server refuses a user-owned one; `--position` required, zero requests without it; `--required` sent only when set, `--required=false` sends false, omitted keeps the server default true; prints {parentAgentId,importedAgentId,position,required,createdAt}) | import rm <parent> <imported> --yes (one removeAgentImport, idempotent; destructive: can also end the imported Agent's grants in any App where it was installed only through the import, and reinstalling does not revive them; `--json` {removed:<the server's boolean>}) | repair-system-memory <agent> (one repairAgentSystemMemory; prints "ran system-memory repair for", not a claim that anything changed; platform admins only, idempotent, no confirmation; refusals FORBIDDEN, AGENT_HAS_NO_SYSTEM_MEMORY, SYSTEM_MEMORY_NOT_FOUND pass through; prints the Agent DTO). All four send refs as typed with no pre-resolve and no retry; who may run each is the server's call | revision create <agent> [--description <t>] (prints revisionLoc/label/published/createdAt) | revision publish <agent> <revision-loc> [--version-label <t>] | revision restore <agent> <revision-loc> (cli#2495: both refs verbatim, one mutation, the same DTO; unset --version-label omitted = server default, explicit empty sent as-is) | revision list <agent> (cli#2533, server #4488: one query, ref verbatim; newest first; text table + a slots line; --json `{"revisions":[{"revisionLoc","label","published","createdAt"}],"slotLimit","highestSlotUsed","slotsRemaining"}`, `revisions` is `[]` when none; slot counts must be whole non-negative numbers up to 9007199254740991 (2^53-1, the largest a JSON number holds exactly; a real server's `slotLimit` is the constant 999999999999999, shown as is; a larger or fractional count is refused, exit 7), and a missing or null required field (`revisions`, `slotLimit`, `highestSlotUsed`, `slotsRemaining`) is refused the same way, exit 7, nothing printed: it is never read as 0) | revision rm <agent> <revision-loc> [--yes] (one mutation, never retried; asks on a terminal else --yes is required, nothing sent without it; prints `✓ deleted revision <loc>`, --json `{"revisionLoc","status":"deleted"}`; REVISION_PUBLISHED (the published revision) and REVISION_ONLY (the only revision) exit 5, an unknown loc is refused by the server as bad input (BAD_USER_INPUT "Revision not found": hadron-server 699dccd1 resolvers.mutation.session.ts `revisionNotFound`, L526 and L2459/L2473 of deleteAgentRevision) exit 2, a non-manager 8; a lost answer says "outcome unknown" and names `hadron agent revision list <agent>` alone on its last line (an internal fault keeps its requestId/serverCode in the --json envelope); **a delete does NOT free a slot**: slot numbers are never reused, so after rm an Agent at REVISION_SLOTS_EXHAUSTED is still at the limit and restore/create are still refused; no help, output or test claims otherwise) | lock acquire <agent> (locked:false prints holder/expiry and exits 5, no retry) | lock release <agent> (released:false exits 5) | set-base <agent> (<base> | --clear) | set-boot-prompt <agent> (<node-ref> [-m <memory>] | --clear) | boot-prompt <agent> [--app <app>] [--model <m>] (cli#1853: the boot prompt the Agent is served now, no worker binding; stdout is a header line `served boot prompt: <LEVEL> <compiledNodeId> revision <N>` then the served text raw (final newline on a TTY only); --json `{decision,text}`; without --app only the Agent's own pointer, never the configured default App; exits 4 AGENT_NOT_FOUND; BOOT_PROMPT_NOT_SET/_TARGET_DELETED/_NOT_APPROVED/_NOT_SIGNED exit 1 (branch on the code); a malformed decision exits 1; valid decision with empty text is success) | model-group list <agent> | model-group set <agent> <name> [--pattern <glob>]… | model-group delete <agent> <name> [--yes] | subscription list (--org <id|urn> | --agent <ref> | --mine) [--limit N] [--offset N] [--all] | subscription add --org <ref> --agent <ref> | subscription revoke <agent> (--user <ref> | --org <ref>) [--yes] (cli#2788 --org, cli#2789 --user; see the subscription notes below)
  # Agent detail (`hadron agent get <ref>`): when systemMemoryId is present, one optional memory read adds the server-returned URN as systemMemoryUrn and displays `system memory: <urn> (<id>)`. If that read is unavailable, refused, missing or unsupported by the server, the command silently keeps its ID-only output and omits systemMemoryUrn. Agent listing is unchanged.
hadron agent ticker-templates list <agent-ref> | create <agent-ref> --name <n> --cadence-seconds <s> [--relevance all|mentions] [--reasons read-team-channel,every-tick,live-check] [--quiet-hours <json>] | update <template-id> --expected-revision <n> [--name <n>] [--cadence-seconds <s>] [--relevance all|mentions] [--reasons <csv>] [--quiet-hours <json> | --clear-quiet-hours] | delete <template-id> --expected-revision <n> [--yes]   # singular alias ticker-template; Agent managers write, future casts only, existing workers/tickers untouched; explicit revision, no fetch/retry; requested cadence (cast raises to App floor)

hadron agent clone <source> --to <org> [--name N] [--slug S]   # one server clone mutation, sent once and never retried, into an organization you administer; new PERSONAL draft with its own system-memory copy, links/imports retained; linked memories stay links. Source must be PUBLIC or managed in its own org. Text prints the returned NEW URN; --json prints the normal Agent DTO (no origin field). No install/subscription second step or manual-copy fallback. **Exit codes:** a typed refusal is a definite answer, nothing was created, so fix the cause and rerun: exit 8 for AGENT_NOT_PUBLIC and unreadable/unusable linked-memory, base or import; exit 2 for an unsupported target or encrypted content; a source-changed conflict (exit 5) carries retryable=true but is never retried for you, rerun by hand. Exit 3 is rejected credentials: nothing was created. Exit 1 with `(clone server error: …)` is a server fault whose effect on the clone is not known: verify as for exit 7 before rerunning. **UNKNOWN (exit 7, never success)** means the answer was lost, or a success reply carried no Agent (null/missing `cloneAgent`): the clone may exist. Verify before cloning again: `hadron agent list --org <org> --visibility PERSONAL` (`--org` takes the same id, slug or URN as `--to`; a new clone is a PERSONAL draft and is listed for org members); with `--name` look for that name, without it the server copies the SOURCE's name, so look for a PERSONAL agent bearing it. Refusal details are the first GraphQL error's own `code`/`reason`/`retryable` and blocker counts, positions and supplied names only (a hidden subject never gets a name); an empty-details first error yields none, never a later error's. The text suffix (stderr, human mode only) names the answer: `(clone refusal: …)`, `(clone authentication: …)` or `(clone server error: …)`, followed by the code, `reason <R>` and, when retryable is true, `retryable: rerun by hand`; each blocker then gets its own indented line, `blocker <CODE>  count <N>  "<name>" (position <P>), position <P>`, with positions as received and a quoted name only where the server supplied one. Server strings are made one-line and control-free; a name is backslash-escaped for `\` and `"` inside its quotes, and a reason containing a comma, quote or backslash is quoted the same way; parse `--json`, not this text. Requires server cloneAgent S1; older providers reject as schema skew.
hadron team app sweep-mode [OFF|OBSERVE|APPLY] [--instruction <kind>:<ref>] (uses --app, the context, or the binding)
hadron team review-stats --month YYYY-MM [--app <ref>] [--json] (read only; uses --app, else the App context or binding, else exit 2)
hadron team worker leave-alone <worker> [--on|--off] [--reason <text>] [--instruction <kind>:<ref>] (names use App scope; ids/URNs resolve server-side)
hadron team init [--app <ref> | -m <team-memory>] (uses --app, the context, or the binding)
hadron team worker cast --name <n> (--role <role> | --agent <ref>) [--reports-to <worker> | --no-team] [--dry-run] [--instruction <kind>:<ref>] (uses --app) | list [--include-retired] (uses --app or the binding) | get <name-or-id> | terminal-title <name-or-id> [--json] | update <name-or-id> [--signature <text>] [--trailer <text>] | set-reports-to <worker> (<target> | --clear) | set-boot-prompt <worker> (<node-ref> [-m <memory>] | --clear) | release <name-or-id> [--yes] | retire <name-or-id> --yes [--instruction <kind>:<ref>] | rm <name-or-id> --yes [--instruction <kind>:<ref>]
# Cast success text includes the server-returned worker URN when available; its existing JSON urn and request count are unchanged.
hadron team worker find-filtered [worker] [--json]   # cli#953 slice A: operator-run, report-only Orca terminal inspection for this App. Only terminal list/read; no signals, writes, prompt delivery or session changes. BLOCKED requires an idle provider header plus contiguous notice body; incomplete/running evidence is AMBIGUOUS, other screens NOT_BLOCKED, failed reads UNREADABLE. agent_tree is diagnostic (ok/multiple_agents/multiple_wrappers/no_wrapper/no_agent/foreign_uid/cwd_outside_worktree; cwd_outside_worktree also covers a wrapper below the worktree root or a boundary-crossing wrapper-agent edge, no_wrapper also an agent that is not the wrapper child, and it is empty when nothing was scanned), never action authority. JSON is [{worker,worktree,terminal,classification,agent_tree,note}] with bounded metadata and fixed codes; no screen/draft/argv content. Unbound worktrees are omitted; duplicate terminals are AMBIGUOUS, and across worktrees the worktree and tree stay empty. Roster reads carry fixed codes (team_roster_unauthenticated / _unreachable / _unreadable, worker_app_unavailable; a request that never got a server answer — a transport failure, or a gateway 5xx with no GraphQL body — is _unreachable; every answered refusal, the other HTTP statuses and any answer carrying GraphQL errors, is _unreadable). Exit 0 when the scan ran, 2 off Orca (unanswered or refused list), 1 if it could not run — including a malformed terminal-list answer (terminal_list_malformed). For BLOCKED, only the provider's Daybreak/human-capacity route is reported; no recovery flag.
hadron team worker onboard --name <worker> --host <id> --repo <token> --branch <branch> --label <label> --family <family> --worktrees-root <absolute-path> --config <router.json> [--base main] [--display-name <text>] [--coordinator] [--apply] [--as-person] [--timeout 2m]   # Orca-hosted workers only; dry-run by default, submits nothing; --family is compared and queued as the server's canonical family (anthropic=claude, openai=codex, case/space folded; custom tokens stay themselves); prerequisites and partial-outcome contract below
hadron team role list [--team-agent <ref>] (uses --app or the binding) | get <role> [--team-agent <ref>] | create <role> [--description <d>] [--team-agent <ref>] | update <role> --description <d> | rm <role> [--yes]
hadron team grant list [--limit N] [--offset N] | get <id> | actions [--limit N] [--offset N] | action <name> | request --action <name> --values <json|@file> --reason <text> [--holder worker|agent] [--holder-id <id>] [--starts-at <time>] [--expires-at <time>] [--max-uses <n>] | request-signature <grant-id> | signing-request get <request-id> | signing-request withdraw <request-id>   # worker-bound; see "Worker-own grants" below
hadron team session work-items --worker <ref> [--limit N] [--offset N] | start (--as <worker> | --launch-grant <g>) [-m <team-memory>] [--repo <r>] [--branch <b>] [--transcript <path>] [--host <h>] [--tool <t>] [--model <m>] [--reports-to <worker>] [--force] | start [--session <id>] [--transcript <path>] [--host <h>] [--tool <t>] [--model <m>]   (no worker named: RESUME, cli#842) | whoami [--check | --signature | --trailer] | recover [--mentions-limit N] | log (--pr | --issue | --commit | --branch) <ref> [--action <a>] [--detail <json>] [--model <m>] [-m <team-memory>] | end [--handoff <text> | --handoff-file <path>] [--summary <text>] [--session <id>] [--resume-claim] | resume-claim [--session <id>] | list [--active] [--as <worker>] [--repo <r>] [--limit N] [--offset N] | list (--pr | --issue | --commit | --branch) <ref> [-m <team-memory>] | sync-model [--hook] | shed [--auto] [--print-prompt] [--offline] [--cooldown <d>] [--transcript <path>] [--note-file <path>] | set-model [<model>] [--family <token>] [--effort <level>]
# cli#960: a bind whose resulting binding records no tool prints `warning: this binding has no tool recorded…` on STDERR (exit unchanged, --json stdout unchanged): `session log` then refuses, so pass --tool when you start a session that will log work; a resume of a session that already has a tool stays quiet without --tool
hadron team chat post <body|-> [--reply-to <seq>] [--as-me] [--orca-host-config PATH [--push]] (uses --app or the binding; with NO binding it REFUSES, exit 4, unless --as-me — cli#842) | read [--since <seq>] [--before <seq>] [--limit <n>] [--all] [--wait <dur> [--poll-interval <dur>]] [--mentions-me | --mentions <ref>] [--thread <seq>] [--team-head <id>... | --untagged | --tagged] (uses --app or the binding) | mark-read --through <seq> [--channel <ref>] (needs the binding)
# Team chat text keeps [seq] first, then RFC3339 at formatted as a UTC minute (2026-11-06T01:20Z). Missing/invalid times are omitted without failure; JSON at stays verbatim and read/cursor operations are unchanged.
# **TEAM TAGS (cli#936):** both `team chat read` and `channel read` accept repeatable `--team-head <id>` (raw stable IDs, one sends teamHeadRef and several send teamHeadRefs; no label/name resolution), `--untagged` (sends `untagged:true`) or `--tagged` (sends `untagged:false`). Omitted means no tag filter; explicit `=false`, any pair, and `--thread` plus any tag filter refuse locally (exit 2 before HTTP). Mention filters/cursors/paging/wait still compose where supported. No client-side filtering, metadata grant or post-head selection. Tags appear only where the deployment stamps them: additive `teamTags` in JSON (including posts) is `[{headId,label}]`, `[]` known untagged or `null` historical/withheld; key on headId, labels are display-only. Text shows nonempty labels, nothing for null/empty. TEAM_CHAT_TEAM_HEAD_INVALID is uniform exit 4, TEAM_CHAT_TEAM_METADATA_FORBIDDEN exit 8, BAD_USER_INPUT exit 2; serverCode retained, never an empty transcript or widened retry. Filtered reads never update a binding watermark or mark read, including --all/--wait; nextSince is a returned-page cursor only. Activation, routing and bare-@team retirement are outside this slice; existing --push mention semantics stay unchanged.
# **WAIT (cli#1261):** `--since <seq> --wait <dur>` POLLS (team chat is never pushed): the same ordinary read every `--poll-interval` (default 5s, min 2s) until one returns messages (printed, exit 0) or `<dur>` passes (last empty read printed, exit **13**); a failing poll stops with its own code.
# **THREAD (cli#1262):** `--thread <seq>` returns the DIRECT replies to that message (never the message itself, never replies-to-replies; a reply's own thread is `--thread <its seq>`), filtered by the server, composing with `--since/--before/--limit/--all`. A seq naming no message is exit **4** (typed refusal, never an empty page); a message with no replies is a valid empty page, exit 0. A thread read is a filtered window: it never advances the watermark or server read state (use `team chat mark-read`).
# Team-chat post refusals (cli#1306): TEAM_CHAT_DUPLICATE and TEAM_CHAT_POST_RATE_LIMITED exit 5 (Conflict), retaining the server's message (earlier seq, or limit/reset time). --json error adds serverCode for these two refusals beside numeric code and message. A post or reply is sent once; the CLI never retries either refusal. TEAM_CHAT_BODY_TOO_LARGE remains exit 2. The node-addressed `hadron chat post --node …` door (cli#1319) maps the same three the same way (pinned): one CreateChannelMessage, no retry; only CHANNEL_NOT_FOUND creates the Channel and posts once more, and a refusal of that second post is not retried either.
# Chat-history search (cli#1327): `hadron team chat search <query> [--limit <n>] [--cursor <c>]` (uses --app or the binding) and `hadron channel search <id|address> <query> [--limit <n>] [--cursor <c>]` search ONE chat newest-first for a literal, case-insensitive substring. Results are REFERENCES only (`items[]`: seq, urn, nodeId, channelId, channelIncarnation; no body or snippet); read a hit with `team chat read [--app <ref>] --since <seq-1> --limit 1` (the text hint repeats the `--app` you searched with) / `channel read <id> --since <seq-1> --limit 1`. `--json` is the server page: `{channelId, channelIncarnation, complete, cursor, items}` (`items` is `[]` when empty, `cursor` null when complete). `complete:false` means a full page or the server scan bound stopped the search, NOT "no matches": rerun with the SAME query and `--cursor <cursor>`; only an empty page with `complete:true` means none. The server owns query length, default/max `--limit` and the scan bound; the CLI passes `--limit`/`--cursor` through unclamped. Refusals keep the server code in the `--json` error as `serverCode`: TEAM_CHAT_SEARCH_QUERY_LENGTH and TEAM_CHAT_SEARCH_CURSOR_INVALID exit 2, TEAM_CHAT_SEARCH_ENCRYPTED exit 5 (an encrypted chat is refused, never an empty page), `--limit` below 1 BAD_USER_INPUT exit 2, an unreadable/unknown Channel exit 4. A search changes no read position and marks nothing read.
hadron team attention [--since <token>]   # RETIRED (cli#935, hadron-server#2027): exits 2 (Usage) with "hadron team attention is retired (hadron-server#2027). It has no replacement unread or token view. Read your own chat with "hadron team chat read"."; --json gives the standard error envelope; reads no config or credentials, makes no request, never echoes --since
hadron team attention switchover preview | apply [--proof <proof>] [--yes]   # RETIRED (cli#935): exits 2 with the same text plus " No backlog was marked read."; nothing is marked read, --proof is never echoed
hadron team pr schedule <build-task> --slot <id> [--slot <id>...] | freeze <build-task> <40-hex-sha> | seat <seat-task> --slot <id> --head <sha> [--supersedes <seat-task>] | verdict <seat-task> --head <sha> --verdict <APPROVE|CHANGES_REQUESTED|PASS|FAIL|CARRY|REFUSED> [--node <urn>] [--receipt <url>] | readiness <build-task> --head <sha> | ready <build-task> --head <sha>
hadron team task create <title> [--priority 1..5] [--blocked-by <task>]… [--description <d>] [--assignee <party>] [--owner <party>] [--parent <id>] [--project] [--start-by <iso>] [--finish-by <RFC3339>] [--tag <t>]… [--source <s>] [--external-id <id>] [--ref <kind>=<target>[|<label>]]… [--status backlog|pending] [--require-role <r,...>]… [--team <lead-worker-ref>] [--match] [--min-tier low|mid|high] [--family-in|--family-not-in <a,b>] [--model-group <name,...>] [--model-group-not <name,...>] [--exclude-worker <w>]… [--family <f>] [--model <m> | --tier high|mid|low] [--effort LOW|MEDIUM|HIGH|XHIGH|MAX|UNSET] (cli#2206, cor:agt:050:01: what to launch, not whom to match; create-only, a clone copies none; a model with a tier or NOT_APPLICABLE exits 2 before any call; the server's TASK_RUN_SETTINGS_INVALID maps to exit 2 with its reason; applies only to a short-lived assignee; `get` prints runSettings and appliedRunSettings together, text and --json; a batch item carries a runSettings object inside its task) [--properties <json|@file>] [--close-session|--no-close-session] (uses --app or the binding) | assign|reassign <taskId> --assignee <party> [--close-session|--no-close-session] | start|progress|cancel <taskId> | release <taskId> (cli#1962, hadron-server#4084, cor:agt:120:02: give the task's outside item back so the next create or find-untracked claim gets it; whoever may cancel the task may release it, V87 Proposed, server-enforced; the item is read first and printed as `released: <item>`, --json = the task + `releasedExternalId` + warnings; refusals are the server's: TASK_NOT_FOUND 4, TASK_NOT_OWNER 8, APP_UNINSTALLED 5, TASK_EXTERNAL_ID_NONE 5 for a task that holds no item, a second release included; cancel keeps the item, release does not cancel) | park|ready <taskId> | finish <taskId> --outcome <word> [--summary <s>] [--evidence <url>] [--findings-file <path|->] [--worked-from <role-task node id|URN>]… (cli#2514, hadron-server#4414, cor:agt:050:30: the role tasks you followed; repeat, at most 10, an eleventh or a blank one exits 2 before any request; the server keeps each as {nodeId, urn, revision} in the order given, once; a ref it refuses — unreadable, missing, not runnable or repeated — is TASK_ROLE_TASK_REFS_INVALID, exit 2, a fixed message that names no reference, the task stays started; absent flag = the request, text and --json exactly as before; the stored refs print under `worked from:` in the finish receipt and `get`, and as result.workedFromRoleTaskRefs [{nodeId, urn, revision}] in --json, omitted when none; combines with --findings-file; the result selection is part of the shared task fields, so EVERY task command needs a server containing hadron-server#4414, and an older one rejects each task query as a schema-skew refusal, exit 2) | findings <task> [--limit N] [--offset N] | hand-back <taskRef> --code missing-input|cannot-do|refused|out-of-scope|duplicate [--text <t>] (cli#1517, server#2651, cor:agt:050:29: the assignee hands a STARTED task back to the queue; code is case-insensitive and the enum names MISSING_INPUT etc. are accepted; a missing or unknown --code exits 2 before any call; --text up to 2,000 characters, the server's limit, never pre-trimmed and omitted when not given; refusals map TASK_NOT_FOUND 4, TASK_NOT_ASSIGNEE 8, TASK_ENDED/TASK_INVALID_TRANSITION/APP_UNINSTALLED 5, BAD_USER_INPUT/SESSION_NOT_BOUND 2, with --json carrying the error envelope, no partial task; text prints `Handed back: <CODE> at <time>[: <text>]` with the text made one-line and control-free and no dangling colon; --json = the task + warnings) | get <taskId> | list [--match-failed[=false]] [--status <s>]… [--text <words>] [--assignee <party>] [--owner <party>] [--tag <t>] [--external-id <id>] [--ref <target> [--ref-kind <kind>]] [--queued] [--roots-only] [--parent <id>] [--limit N] [--offset N] (uses --app or the binding) | list --team[=worker:<ref>] [--status <s>]… [--tree] [--stale-over <duration>] (cli#1044: a coordinator's work in flight. Team = the coordinator (bare --team = the bound worker; the value must be attached with =) plus every worker whose roster reportsTo chain reaches them, in this App. Rows = the App's tasks a team member owns, is assigned or created (a task a person created, or a worker outside the team created, does not join through its creator; match stays party), plus all their descendants. It reads every page, never truncates, and refuses a non-advancing page (exit 1). --status applies after the join. --stale-over keeps PENDING/STARTED tasks idle longer than the duration, measured from updatedAt, else startedAt, else createdAt, and adds a stderr note that records lag. An unknown coordinator exits 4. No other App is read (server#2370). The only flags it combines with are --status/--tree/--stale-over, and --text (case-insensitive title/description substring, server-validated 1–200 characters after trimming); text uses a second paged server read and intersects after the full team join. Other plain filters are refused (exit 2). --json = {appId, coordinator, coordinatorSource, members, staleOverSeconds, tasks:[task + ownerTeam/assigneeTeam team|outside|unknown|user|null, match party|descendant, ageSeconds, ageFrom]}; --tree changes text only) | find-untracked --integration <name> [--team <worker>] [--ready only|exclude|any] [--repo <owner/name>] [--skip N] [--limit N] [--assign-to-me] (cli#1524; see "Task integrations") | mine [--limit N] [--offset N] (needs the binding: the bound worker's PENDING+STARTED tasks, fixed filters) | tree <rootTaskId>   # a party is worker:<id|urn|name> or user:<id|handle>; close-when-done defaults to NO at create (the assigner opts in with --close-session; hadron-server#2249), while assign|reassign with neither flag keep the stored value; writes are the bound worker's when a session is bound; create|assign|reassign|finish|start|progress|cancel|park|ready may succeed WITH server warnings (cli#962, server#2198; the five simple verbs since cli#1283, e.g. task.cancel.open-children): text mode adds stderr lines `warning: [<code>] <text>` (+ ` → <taskUrn>` when the register maps it, ` (no task mapped)` when unmapped) after the unchanged stdout receipt; --json adds `warnings: [{code, text, register: {state, taskLoc, taskUrn} | null}]`, always present, [] when none; codes and text are the server's verbatim (unknown codes included); exit code unchanged, and a failed call prints no warnings; the create|assign|reassign receipts and `get` print `close session when done: yes|no` from the server-returned value (the same boolean as --json `closeSessionWhenDone`, cor:agt:050:10) — yes adds that finishing or cancelling asks the assignee to end its worker session and the platform may end it after a grace period (through a live host), no is the bare line; the command itself never ends one, and declining the request does not keep the session, so pass --no-close-session at create (or on the assign that first names a queued task's assignee) for a long-lived assignee (cli#958); the platform may end a flagged session through the worker's live host after a grace period (10 minutes by default, set per App), the choice has no in-place setter, and reassigning a task to its current assignee is refused (TASK_INVALID_TRANSITION, exit 5) with the choice unchanged (cli#959)
# Task handles (cli#1131, cor:agt:050:01): every task carries a server-derived handle (`AT3.405` = project AT3, then its number under each parent; `AT0.<n>` = no project) and `taskNumber`, which are additive: --json keeps `id` and every existing key and adds `handle` and `taskNumber`; text output prints the handle BEFORE the full id (list/mine/--team tables gain a leading HANDLE column; get, tree and the mutation receipts name `AT3.405 <id>`), and a server that returns no handle prints today's output (list/mine/--team then omit the HANDLE column); the task commands REQUIRE a server that serves `handle`/`taskNumber` (hadron-server#2579): an older server rejects every task query as a schema-skew refusal (exit 2, no fallback); task refusals print terminal-safe on stderr (controls neutralised, 1024-rune bound) while the --json error envelope keeps the server's raw text; every command that takes a task id (get|tree|start|progress|finish|cancel|assign|reassign|park|ready, `create --parent`, `list --parent`) also takes a handle, bare (`AT3.405`, case-insensitive) or qualified (`hrn:agenttask:<root>:<app-slug>:AT3.405`), passed to the server byte for byte (the CLI never parses, composes or allocates one); a bare handle held by several Apps is refused by the server with TASK_HANDLE_QUALIFY naming the qualified form, and an unknown handle or another App's handle answers as an unknown id; `create --project` makes a top-level project (AT1, AT2, …) instead of a task under the reserved no-project AT0: sent only when the flag is given (`--project=false` sends false), refused by the server with --parent or an AT0 parent, no client-side authority check; AT0 never appears in `list`/`--roots-only` output; event/roster/alarm lines still print bare ids (epic #2614 S6).
hadron team roster (uses --app or the binding)   # states as defined by hrn:node:hadronmemory.com:specs:cor:agt:050:22 (Proposed), rendered as the server derives them, in the server's first-that-holds order; MODEL (self-reported) and TIER (server high/mid/low, null = unbound/unreported/unmapped) sit beside FAMILY, advisory only and never authorization; the table ends with a 'free for work by tier' line tallying the rows the server marked freeForWork (NOT state AVAILABLE), unrecognised/null tiers under unknown (table only; --json adds model, modelReportedAt, tier and keeps every earlier key). `team worker list` gains the same MODEL/TIER columns and --json keys, plus a FAMILY column and `family` key (cli#887; also on `worker get --json`): the server's recorded family as sent, null/dash = unknown, never inferred from a tool or host (`claude-code` is not a family). Needs a server that serves these fields (older servers reject the query, no fallback) ; HELD BY (name, else @handle) + SINCE show the holder of the worker name (heldBy {id,handle,urn,name}|null + heldAt in --json, additive; a name the server withheld is null and renders like an unset one, cli#1032) TERMINAL column (cli#1375, hadron-server#2771): the worker's latest host terminal report as `status (age)` / `status (reason, age)` (busy|idle|locked|dark; age from checkedAt on every reported row, display only), a dash when null; cli#1856: a report checked more than 30 minutes ago reads `stale (8h)` (status withheld, age kept) and, when every reported row is stale, the table ends with a `terminal reports: all N are stale` note; --json adds `terminal {host{id,label},status,prompt,reason,lastTurnAt,checkedAt,reportedAt,stale} | null` (`stale` is a boolean computed by the CLI at read time), additive. Like model/tier it needs a server that serves the field: an older server's rejection surfaces as the schema-skew refusal. `team worker get` shows a `terminal:` line and the same `terminal` key from its own operation, so an older server falls back to null. GROUPS column (cli#1485 S3, hadron-server#3026, cor:agt:060:12): the model groups the worker's model is in NOW, computed live by the server — live matching information, never authorization, never computed by the CLI. `--json` adds `modelGroups` (a list; `[]` is a real answer: no reported model or no group matches, shown as a dash) and `modelGroupsError` (null, or the server's error code for a row whose group lookup failed, in which case `modelGroups` is null and the text reads `unavailable (CODE)`). That per-row field error is the ONLY server error the roster tolerates, and only at exactly `teamRoster.workers[i].modelGroups` on a null value; any other or mixed error fails the whole command, and a null with no matching error fails closed (never `[]`). `team worker get` shows a `model groups:` line (nothing for `[]`, `unavailable` for a withheld null) and `modelGroups` in `--json`, read in its own operation, and fails on ANY error there (no tolerated path). Needs a server with #3026: an older server's rejection is not tolerated on either command. `team worker get` shows an `availability:` line and an `availability` key in `--json` (cli#1911, hadron-server#3865 slice 2, cor:agt:060:02): `{state, reason, until, setAt, setBy, setByParty, inEffect}` or `null`, from its own operation so an older server reads null. `null` means withheld from you OR no record, which the CLI cannot tell apart, so it prints nothing for it and never `available`/`no record`, and never fills it from `team availability get`; `reason` is set only for an UNAVAILABLE record; `setBy` LAPSE is the platform's own rewrite (text: `AVAILABLE (lapsed at <time>)`, set by lapse); a person/worker setter whose `setByParty` is null reads `identity withheld`. REPORTS TO column (cli#1085): the server reportsTo party, "(retired)" marked, a dash for none; --json already carries reportsTo/reportsToRetired.
hadron team composition [--json] (uses --app or the binding)   # cli#1085: the server teamComposition read, rendered as returned: direct-report lanes in the server order (coordinator name, the unassigned lane last), a retired coordinator stays named and flagged, non-retired workers per lane with URN/role/family/state (the coordinator URN rides the lane heading; a missing URN is a dash), plain countsByRole and countsByRoleFamily where a null role or family is its own bucket (a dash in text, null in --json); --json {appId,asOf,lanes:[{coordinator|null,coordinatorRetired,countsByRole,countsByRoleFamily,workers}]} with [] never null. A read, not a capacity policy: no balance judgment, no second roster join. A refused read, a server without teamComposition, or a null result is a failure (mapped exit code, nothing rendered), never an empty team; only a server-confirmed empty team prints lanes [].
# Deleted-node recovery (cli#985; writers only): `node recovery list|get|graph` and `node restore` are the ONLY way to read or bring back a soft-deleted node — `node get|list|rm` stay live-only. A ref goes to the server unresolved (an ordinary resolve omits deleted nodes). `recovery list|graph` page 20 by default with an explicit `--limit` (1-200); when more exist, STDERR says `showing a-b of N; more: --offset b` (stdout/--json unchanged) and `--json` carries `{items,total,limit,offset,hasMore}`; `--all` reads every page and fails as a whole if any page fails (never a partial list; pages are not a snapshot, so concurrent restores can move rows). `recovery get` never reads the body unless `--content`, which writes the raw stored body byte for byte (text) or `content` + `contentIncluded:true` (--json); an unreadable body is an error, never an empty one. A `recovery get` miss is exit 4 with one fixed sentence whatever the cause (live, absent, not a writer). `node restore` restores ONE node (and the comment threads its own deletion removed), sends one request and never retries: refusals keep the server's message (`NODE_PARENT_DELETED` exit 5 names the ancestor to restore first; governed/comment/compiled-boot-prompt/protected exit 2; `ROLE_WRITER_LEVEL_REQUIRED`/`ROLE_WRITERS_UNSUPPORTED` exit 8); a lost response is exit 7 and means VERIFY with `node recovery get` before retrying. The receipt is the plain node (id, urn, memoryId, loc, name, nodeType, updatedAt, deletedAt) with no changed/idempotent flag.
# Direct children (cli#1253): `node list --children-of <loc>` lists only the nodes exactly one level below that loc (grandchildren excluded); `--children-of ""` lists the top level. The empty string is a value and is sent; omit the flag for no constraint. Judged on loc text, so a child whose parent has no node is still listed; it ANDs with `--prefix`, `--type`, `--limit`/`--offset` and the other filters; `--json` shape unchanged. Needs a server with NodeFilter.childrenOf (hadron-server#2832); an older server refuses the unknown argument.

# Unresolved comment grooming (cli#1699, provider server#3520/#3494): `node list --has-unresolved-comments` sends NodeFilter.hasOpenThreads:true; `=false` sends false; omission sends no key, selects no commentSummary and preserves existing text/JSON. Both values combine with the existing filters in the one server-side NodeFilter on EVERY page (including search/install/seq paths); no client thread scan or unfiltered retry. Opt-in JSON adds commentSummary {openThreads,oldestOpenThreadAt}, timestamp string verbatim/null. Text adds OPEN THREADS and OLDEST after normal columns; projected blocks show both labels after metadata and before data/properties. Age floors: under1h Nm, under1d Nh, otherwise Nd (400d stays400d); under1m and future are <1m; zero/null is -. Missing/null summary, missing/negative count, unparseable time or count/time inconsistency fail the whole command (exit1, no partial rows). A provider rejecting the field fails as schema skew(exit2), no fallback. Empty output stays unchanged. Groom boot prompts with `--has-unresolved-comments --memory-class system --role boot-prompt`, role tasks with `--has-unresolved-comments --memory-class system --runnable`, processes with `--has-unresolved-comments -m <team-system-memory> --prefix processes:`. SYSTEM is excluded by default unless named with -m or --memory-class system. Resolve applied/rejected threads instead of deleting grooming history. `app nodes` is unchanged. Provider addition remains Proposed; this CLI adds no access rule.
# Paging (cli#967): `agent list`, `node list` and `team session list` list EVERYTHING unless you pass --limit/--offset (one page). `team task list|mine`, `team events`, `team host ops`, `comment list`, `node revision list`, `node edge-removals` and a bounded `team chat read` default to 20 per page; a full default page prints "more may exist" on STDERR (stdout and --json shapes are unchanged), and `team chat read --all` walks every page. `node revision list`, `usage events` and a bounded `team session list` read the server's page twin (cli#1339): stderr says `showing a-b of N` with the REAL total and, when more exist, `more: --offset b` (stdout and --json stay the bare array; `--limit 0` counts only). `ticker list` and an unbounded `team session list` page to the server's total and FAIL (exit 7) rather than print a partial list. A row inserted while either one pages shifts the offsets, so it restarts from offset 0 (up to 3 restarts per list read, cli#2030; the session list is put back in newest-first order, a ticker found on a restart may print after the rest) before it fails: exit 7 "the … list changed while it was being read" means retry, not unbound, while "not advancing" (a restarted pass read nothing new) is a server that cannot be retried into working. Only these two scans restart; the others named here fail with exit 7 on the first repeated page. A list with no total (`node edge-removals`, `team task list`) cannot prove it ended, so treat a full default page as "there may be more". A scan that drains a list (`chat read`, `memory export`, `spec` scans, `node import` overwrite probes, …) stops only on an empty page; a server that keeps returning a page already read makes it FAIL with exit 7 (`Unavailable`: "the server returned a page already read … the list is not advancing"), never succeed with a partial list.
hadron orca host plan --roster-file <path> [--json]   # READ-ONLY snapshot, no apply/launch/terminal input/session binding. Explicit version-1 UTF-8 JSON roster <=1MiB, 1..200 workers: {version:1,app:<App ref>,workers:[{worker:<existing ref>,repo:<absolute existing checkout>,family:claude|codex|glm}]}. Ambient App ignored, --app refused. Unknown/duplicate keys and duplicate resolved workers refuse; no shell expansion. Reads typed App/worker identity, git worktrees/binding metadata, and orca terminal list only (no due-wake or screen read). JSON version/server/app/readOnly/complete/discoveryScope/workers/actions/conflicts/unavailable, arrays []; proposed actions are data and confer no execution authority. Partial discovery is incomplete/nonzero; malformed/unreachable Orca10, local input2, conflicts5, provider refusal's mapped exit retained. Binding worker/App IDs must also match the selected canonical server endpoint; missing binding-server metadata is unavailable, a different server is a conflict. Server URLs with userinfo/query/fragment refuse locally. Discovery covers roster repositories and reported terminal roots only, not all host repos: absence and complete refer only to that scope. Explicitly prunable missing git worktrees are ignored without pruning; unresolved terminal paths stay incomplete with exit1, not Orca10. Recorded bindings are not proof of a live session, launched model or safe prompt. Nothing omitted retires/closes/deletes resources. Linux-tested; no production fixture or host-write grant.
hadron orca terminal status (<worker|terminal-id> | --all) [--config <host.yaml>] [--json]   # ORCA INTEGRATION: reads Orca terminals, needs the Orca CLI/app on THIS host (exit 10 off-host); never types. One worker, or --all (--all --json = ONE JSON array, one entry per active App worker; without --json, one text line per worker). --config (cli#1378, the file of `team host run`) is the unattended form: Orca = delivery.orca_bin (absolute, exit 2 if relative) else PATH; server read with the host key from key_file only (0600; never HADRON_TOKEN/env/token store; no session); --app with --config exit 2; --all = the App workers the file configures; bad file exit 2, key file missing/too open exit 3 before Orca is listed; the dark rule needs the host key's two reads (tickers: `host:operate`; `dueWakes`: `attention:deliver`) and an idle worker fails closed if either is refused. Contract `terminal-status@1`, see "Orca terminal status" below.
hadron team push <worker> (--text <message> | --message-file <path>) [--seq N] [--wait-timeout 120s] [--draft-wait 30s] [--draft-stable-for 30s] [--confirm-timeout 10s] [--poll-interval 250ms]   # bound sender, one guarded console input; CPU-only busy/unknown/draft decisions and fresh on-screen turn confirmation. Command-only exits: 0 sent, 9 sent-with-draft (SUCCESS: NEVER RETRY), 10 held-busy, 11 held-unknown (zero composer input), 12 unconfirmed (mutation may have occurred: DO NOT RETRY). Draft wait and exact-byte stability overlap; stability cannot be below30s. JSON: outcome/phase/reason/draftIncluded/recipient/seq; no message/draft/screen text. Prefer --message-file (text argv is visible); no chat post or mark. Own-attempt lock only; ms read-to-input race remains and process death can lose a draft. Existing unguarded callers are not migrated by this command.
hadron team alarms [--team[=worker:<ref>]] [--all] [--type <t>,...] [--status <s>,...] [--since <iso>] [--json]   # cli#1829: the per-task join of alarms — one row per alarm IN EFFECT (the server's alarmsInEffect filter, cor:agt:050:20; never re-derived here) beside its task (handle, title, status), the assignee and the assignee's roster state exactly as the server returns it (TeamRosterState, bound, liveSession, checkStatus incl. noSessionReason). --team = the bound worker's team, --team=worker:<ref> another coordinator's (the set `task list --team` shows); omitted = every task of the App. --all adds acknowledged and past-escalation alarms and, per task, taskAcknowledgedAlarms and taskActivityAgeSeconds (taskAcknowledgedAlarms counts only the alarms this command's walk read, a lower bound whenever the walk is incomplete; taskActivityAgeSeconds is absent, not zero, when the task's own updatedAt is unreadable — the age is unknown, the roster's rule for a missing timestamp). Tasks are PENDING/STARTED unless --status. Only task alarms (task-less ones are read with `team events`). Newest alarm first; walks the stream to its end. --json = {appId, scope: team|app, coordinator|null (team scope), coordinatorSource (team scope only; absent for the App scope), all, alarms:[{id, type, severity, createdAt, ageSeconds, acknowledgedAt, acknowledgedBy|null, escalationEndedAt|null, task:{id, handle, title, status}, assignee|null, assigneeRoster|null, [taskAcknowledgedAlarms, taskActivityAgeSeconds]}], [incomplete:{complete:false, reason, lastEventId, pages, [resumeCursor]}]} (`[]` when empty; incomplete only when paging could not finish: lastEventId is the last event id the walk read, empty when it read none, never a cursor; resumeCursor is a page-limit stop's continuation cursor — use it to continue — while a cursor-stalled stop carries none, its cursor being one the walk just proved only re-reads pages, whether repeated immediately or cycling A→B→A). Exit 2 for a bad --team (not worker:<ref>), --status, --type or --since before any read; 4 for a coordinator not on the roster. Requires hadron-server#4135 (which includes server#3934): it walks alarms through the same event selection as `team events`.
hadron team events [--alarms] [--alarms-in-effect] [--since <iso>] [--after <eventId> | --newest [--before <eventId>]] [--task <id>] [--type <t>]… [--limit N] (uses --app or the binding)   # oldest-first; --after pages forward; --newest reads newest-first, --before pages toward older. --json is {events,hasMore,nextCursor} (was a bare array, cli#1919); a continuation is named only when hasMore. `--alarms-in-effect` (cli#1940) passes the server's filter through: only alarms unacknowledged and with no recorded escalation end; it implies alarms only (same rows with or without `--alarms`), is never worked out client-side, and omitted sends nothing. Every event now carries `escalationEndedAt` (--json: an RFC 3339 string or `null`; text: the ESCALATION ENDED column, blank when null): when the server recorded the alarm's escalation end, null while in effect and on non-alarm events. An alarm out of effect is not acknowledged and not hidden (read without the flag); the end can lag up to one evaluator pass (about a minute), so it is not a real-time value. `alarm ack` --json carries the same key. A task event's TASK cell reads `<handle> <first 12 hex>` (cli#2005, e.g. `AT0.7234 01a1d71218bb`; the bare id when the server names no handle, e.g. the task is gone) and --json adds `taskHandle` beside `taskId` (exactly as sent, null for a task-less event); this needs a server with `AgentTeamEvent.taskHandle` (hadron-server#4135); an older one is observed to refuse the read. `team task tree` / `team task list --team` count each task's alarms IN EFFECT (cli#2053 F4): the walk sends the server's `alarmsInEffect` filter (not `alarmsOnly`) and counts every row returned, so the count matches `--alarms-in-effect` on the same alarm; an ended but unacknowledged alarm is not counted, and the end can lag up to one evaluator pass. They mark open-alarm counts incomplete (≥N, INCOMPLETE line; --json openAlarmsIncomplete / alarmCounts) when the alarm walk stops early. Requires hadron-server#4135, which postdates and includes server#3934: every event selects `taskHandle` (#4135), and the events document also selects `escalationEndedAt` and declares `$alarmsInEffect` (#3934). `team events`, `team task tree`, `team task list --team`, `team alarms` and `team alarm ack` all read through that one event selection (`AgentTeamEventFields`), so each needs the same floor. An older server is observed to refuse the query as naming an unknown field, so these commands fail rather than degrade; that is what older servers do today, not a guaranteed behavior.
hadron team alarm ack <eventId>   # stops the alarm waking higher owners (cor:agt:050:20); --json is the acknowledged event, with `escalationEndedAt` and `taskHandle` (as in `team events`). Requires hadron-server#4135 (which includes server#3934), the floor of the shared event selection; see `team events`. Server warnings on an acknowledge (e.g. `alarm.ack.outside-chain`) print on stderr in text mode on either path, with or without `--instruction`; a plain `--json` stays the bare event with no `warnings` key, and only the instructed `--json` adds `warnings` ([] when none) (cli#2552).
hadron team availability get | set (--worker <ref> | --family <token>) --state <available|unavailable> [--reason HOST_DOWN|OTHER|OUT_OF_TOKENS|STALLED] [--until <iso>] [--instruction <kind>:<ref>] (uses --app or the binding)   # every flag shown is `set`'s — `get` is a read and takes none of them (`--app` or the binding still selects its App, as the tail says); `--instruction` in particular is set-only (cli#2491). cli#2450: without the flag, --worker keeps its authored attribution and --family acts as the signed-in person. With it, a verified unchanged same-server binding for the target App carries the worker session; the server admits the manager's held worker, including --family, and decides the worker's own arm. Unusable/changed binding = person write with the claim checked but not recorded; never retry a refusal plain. Requires server 2026-11-04 (hadron-server#3376). A `set` (plain or instructed) whose mutation returns no record (null or absent) exits 1, outcome unknown, with no success receipt: read availability (`team availability get`) before retrying (cli#2493).
hadron team utilization (--all | --family <f> [--tier high|mid|low] [--role <r>]) [--window <N>h] [--fail-on-saturated] [--json]   # cli#1836 slice 1, hadron-server#3828 (needs a server with teamUtilization; an older server's rejection is the schema-skew refusal): advisory current utilization per family/tier/role, read-only, computed by the server. --json = {appId, windowHours, computedAt, combinations:[{family, tier|null, role|null, eligibleWorkers, busyWorkers, utilization (0..1), saturated}]} (`[]` when empty), ranked highest utilization first (ties keep server order). saturated is true ONLY for zero eligible workers in this slice (utilization then 1). --all lists all and takes no --family/--tier/--role (exit 2); without it, several matching combinations = exit 2 naming --all, none = empty list, exit 0. --window is whole hours, 1h to 168h (24h, 168h; above 168h = exit 2); omitted = none sent and the server's window is printed. Exit 0 whatever the numbers; 5 only with --fail-on-saturated and a shown combination saturated (applied after the output is written); 2 for a bad flag, --tier or --window. A null teamUtilization result is exit 1. No canAddWorker, lastMatchFailure or --fail-on-no-capacity yet (not in the schema).
hadron team chat check   # bound worker's team channel only; complete tagged range to private JSON artifact (stored C is trusted only for matching cursorIncarnation; otherwise scan from0 and disclose both values, possibly repeating messages), path on stderr BEFORE fenced H mark; includes server @all/team selection; mark covers untagged gap too; open artifact to read it, delete it yourself afterward; no inbox proof ack. Exit 0 written+marked, 9 empty+marked, 10 uncertain; errors1–8 retained. --json bounded receipt {error,outcome,sampledCursor,scanCursor,cursorIncarnation,sampledHead,messageCount,artifactPath,markAttempted,markStatus,markVerification}; no message bodies on stdout. Fenced mark response proves coverage, lost response uses strict stored/current incarnation readback then at most one identical fenced retry, never a mismatch/unguarded fallback. Page/file/path failure sends no mark; final receipt failure can follow confirmed mark (no rollback). Own-session reads/marks may count as checks even on command failure; no stamp/roster OK claim. Requires server#2194 and a private cache filesystem supporting file+directory sync; no production cursor experiment.
hadron team chat inbox [--after <endCursor> | --ack-file <path|->]   # own bound live worker session only, same canonical App/server; one rendered page, output+flush before ack; --json stable page/proof ids, no hidden journal; retain privately for ack-only recovery {"batchIds":["proof"]}; '-' requires piped input; ack recovery does not reread/redisplay; same-session proof/15-minute server boundary; no host-key fallback, no mark-read compensation; before ack a repull can redisplay
hadron team chat search <query> [--limit <n>] [--cursor <c>]   # search the team chat history; references only, see the cli#1327 note above (uses --app or the binding)
hadron team delivery-settings get | set [--check-period-seconds N] [--failed-checks-to-overdue N] [--batch-window-seconds N] [--busy-delay-seconds N] [--stuck-window-seconds N] (uses --app or the binding)   # check period default 600s and failed checks default2 + 5min slack; server-derived check status; pair period with team ticker cadence; batch/busy/stuck settings deprecated; batch window IS ticker cadence floor (min 60s, default 120); existing tickers retain cadence, floor raise above a live ticker refuses; server defaults busy/stuck 600/900s; server enforces its current limits; omitted preserves, no null/reset; no-field set is manager-only current-values readback (not a database no-op); use get for a participant read; ordinary authenticated caller, server controls manager rights
hadron team pause | resume [--app <app>] [--instruction <kind>:<ref>]   # App-manager setting; no worker-session header without --instruction (cli#2774: with it the claim rides setTeamPause through the manager-held worker's verified binding, as the shared instruction help describes); regular wakes held while paused, PENDING(TEAM_PAUSED), no interruption; separate from stop signals and worker self-pause. Pause preserves the original timestamp; resume clears only the App pause and restarts OVERDUE clocks; resuming an unpaused team stamps nothing. JSON {app,paused,teamPausedAt}, timestamp nullable, returned state authoritative; FORBIDDEN 8, APP_NOT_FOUND 4; mutations sent once.
hadron team worker change-agent <name|id|URN> --agent <id|URN> [--instruction <kind>:<ref>] [--yes]   # App-manager write, no worker-session header (without --instruction, even from a bound worktree); with --instruction (cli#1592, ruling 0096, cor:dmo:050:12; server 2026-11-03+) the instruction document goes out through the worktree session only when the binding is complete, for this server and for the TARGET worker's own App, and unchanged since the command started, otherwise headerless as the person with a stderr note (the server checks the person's ref too, records nothing); name needs App scope (otherwise exit 2 before prompting); direct worker refs identify their own App, never the ambient one. Confirm before command requests (non-TTY needs --yes). Clears the custom boot-prompt selection and replaces agent-generated wake schedules including edits; hand-created tickers stay; next bind uses new agent/role, open session finishes under old casting (server containing #2504 required). Agent shape only checked locally, server resolves installation/role/grants. JSON worker DTO, text identity/agent/role. WORKER_AGENT_UNCHANGED/WORKER_CASTING_STALE/WORKER_AGENT_HAS_NO_ROLE exit 5; unchanged is a refusal, never success. No mutation retries; lost answer means inspect worker first.
hadron team stop list [--worker <ref>] [--include-ended] | set --scope worker|team|host --level pause-dispatch|pause|halt [--worker <ref> | --host <id>] [--until <RFC3339>] [--instruction <kind>:<ref>] | lift <signal-id> [--instruction <kind>:<ref>] (uses --app or the binding)   # #911 B1 over server #2022: stop signals, ids and times only; list pages to the end (200 a page; a non-advancing cursor refuses, exit 1, nothing shown); list worker filter = signals COVERING that worker now (a team signal on a head covers its reporting tree); set: worker/team scopes name a worker (a team by its head), host scope names a host (a host key may omit it), until is RFC 3339 with offset sent as UTC, omitted = until lifted; lift one id: changed=false is "already ended", never a lift, and lifting one never ends another; JSON list = array ([] when none), set = signal, lift = {changed,signal}; person credential, no worker session attached without --instruction (cli#2774: on set and lift the claim rides the write through the manager-held worker's verified binding); results show the server's STORED signal, never proof that a worker stopped, resumed, was told or obeyed; the host consumer types notices into a verified-empty worker input without an interrupt key (a busy input or unsent draft delays them), pause-dispatch notices go to coordinators not the covered workers, and a manager-only notice has no worker delivery path; a set is refused STOP_SIGNAL_UNAVAILABLE (exit 5, nothing set, sent once) only while a required worker stop-notice lane is not freshly served, not every set; human set output starts `Stop signal stored. This is not a delivery or stop acknowledgement.`; APP_MANAGER_REQUIRED/HOST_SCOPE_REQUIRED/STOP_SIGNAL_SCOPE_FORBIDDEN exit 8, STOP_SIGNAL_NOT_FOUND 4, BAD_USER_INPUT 2; stop refusal codes print fixed sentences, never the server text; an unrecognised error on set/lift says its outcome is unknown; a lost write answer exits 7, outcome unknown, sent once (check with list including ended)
hadron team wakes due [--worker <ref>]… [--first N] [--after <cursor>] [--accept-stop] [--instruction <kind>:<ref>]   # cli#1279: the person's read of the wakes due (cor:agt:080:08); never a worker session (the server refuses one: due conceals the App, APP_NOT_FOUND exit 4). cli#2774: --instruction (plain and --preview alike) rides the dueWakes read itself through the manager-held worker's verified binding; the claim is bound wherever the flag is set, an empty page still an admitted instructed read. You see the workers you hold, or the whole App as its manager. --worker names resolve client-side and an unknown one exits 4 (the server answers an unresolvable ref with silence). A WAKE_BUDGET_EXHAUSTED page is printed and then exits 5 naming the status (its wakes stay due). --accept-stop sends accepts [MENTION,TICK,TASK,STOP] and declares the lane you read (cor:agt:090:04); omitted, no STOP notice is returned and nothing is declared. due --json = {status, withheld, wakes:[{workerId, workerName, proof, tickText, unread, unreadMentions, anchorAt, deliverAfter, busyDeadline, channels:[{channelId, loc, fromSeq, throughSeq, latestMentionSeq}], reasons:[{kind, tickDueAt, ticker:{id,name}|null, task:{id,handle,title,event,chainIndex,eventId}|null, stop:{eventId,signalId,level,notice,interrupt}|null}]}], dormant:[{workerId, workerName, mentions}], hasNextPage, endCursor}
hadron team wakes ack <proof>…   # cli#1279: record delivery of the proofs from `team wakes due` (cor:agt:080:16), as the person; never moves a read position. --json = {acknowledged, unchanged}; refusals lead with the server code (WAKE_PROOF_REQUESTER_MISMATCH 8, WAKE_PROOF_STALE 5, WAKE_PROOF_INVALID 2, WAKE_ACK_FORBIDDEN 8 for a worker's session, which this command never sends)
hadron team wake-settings get --worker <id-or-URN> | set --worker <id-or-URN> [--mentions immediate|off] [--paused-until <RFC3339>] [--clear-pause] [--instruction <kind>:<ref>]   # cli#1292 over server #1747 (cor:agt:080:03, :21): one worker's wake settings; --worker takes an id or hrn:worker: URN (a name is exit 2: the calls take no App). get --json = {worker:{id,kind,name,urn}, mentions:"IMMEDIATE"|"OFF", pausedUntil, subscriptions:[{id,ticker:{id,urn,name,cadenceSeconds,enabled}|null,viaGrant,createdAt}] ([] when none), effectiveCadenceSeconds, dormant}, the server's defaults when nothing is stored; set sends ONLY the flags given (omitted keeps, nothing is sent as null), --clear-pause sends clearPause:true and no pausedUntil, no flag / --clear-pause with --paused-until / a bad --mentions or time is exit 2 with no request; cli#2774: --instruction on set rides the write through the manager-held worker's verified binding (get is a read and takes none); set prints the returned settings in the same shape; who may set is the server's rule (the worker, its holder or the App manager, never the may-subscribe-workers grant); refusals keep their code as the prefix (WAKE_SETTINGS_FORBIDDEN 8, FORBIDDEN 8, WORKER_NOT_FOUND 4, BAD_USER_INPUT 2); retired isolation commands/fields are absent (cli#1743)
hadron team stall-schedule get | set [--assignment-minutes N|default] [--not-started-minutes N|default] [--no-progress-minutes N|default] [--owner-reaction-minutes N|default] [--host-lapse-check-ins N|default] [--host-report-timeout-minutes N|default] [--session-end-grace-minutes N|default] (uses --app or the binding)   # an omitted flag keeps its value, `default` restores the platform default; set is the App manager's, never the bound worker's (exit 8 APP_MANAGER_REQUIRED); host report timeout (5..1440, cor:agt:070:02) and session-end grace (1..1440, cor:agt:050:10) are range-checked before any call (exit 2, nothing sent, even alongside valid flags); get/--json show the server-returned values (sessionEndGraceMinutes, hostReportTimeoutMinutes; platform defaults 10 and 60 today), never client-computed (cli#983)
hadron team host list | ops [--host <id>] [--status issued|collected|succeeded|failed] [--worker <ref>] [--limit N] [--after <cursor>] | issue <type> --host <id> --worker <ref> [--arg k=v]… [--as-person] (uses --app or the binding)   # ops: newest first, --json {items, hasNextPage, endCursor}; issue: worktree.create|worktree.remove plus worker.start|worker.end issued by App managers (worker.deliver exit 2 HOST_OPERATION_NOT_ISSUABLE, other types exit 2 HOST_OPERATION_TYPE_UNKNOWN, bad args exit 2 BAD_HOST_OPERATION_ARGS, not permitted exit 8, unknown host exit 4); bound: issued as the worker (the target's reports-to), --as-person: as yourself (holder or App manager); arguments are ids and name tokens, never a credential
hadron team host run --config <host.yaml> [--once] | status --config <host.yaml> | report-stops --config <host.yaml> (--reporter-worker <name> | --dry-run [--stale-after 10m])   # the host poller (cli#831, cor:agt:070): registers each poll, collects, runs worktree.create|worker.start|worker.deliver|worker.end|worktree.remove plus the worker.reset-context protocol on Linux when capabilities explicitly declares it and reset_context.orca_bin is configured; this driver is built/off and new resets refuse NOT_IDLE/TARGET_UNBOUND until C3 proves the terminal/process association (anything else reported FAILED locally), reports every outcome; authenticates with the host:operate key from key_file only (mode 0600; NEVER HADRON_TOKEN or an env var); a launched agent gets HADRON_LAUNCH_GRANT and no host credential, and binds with session start reading it (the grant names the worker, so no worker argument; never forced; LAUNCH_GRANT_INVALID exit 8, LAUNCH_REFUSED exit 5); worker.start with a worker's reset_on_start (default off; TRANSITIONAL, goes with host.yaml) first resets its EXISTING worktree to a clean base: a dirty/untracked tree is refused WORKTREE_DIRTY and a HEAD on no remote branch WORKTREE_UNPUSHED (reported as the start's failure reason, nothing launched), unpushed commits are kept as refs/hadron/rescue/<utc>/<branch>, then origin is fetched and its default branch checked out detached; a directory with no Git is not reset (cli#2057 C2b); worker.end prompts and reports FAILED after end_wait (the server records an ended session as success; the host ends no session); for a stop_on_end: always worker with no live agent it first runs the strict process census and, when that proves no agent runs in its worktree(s), reports FAILED with a reason LEADING AGENT_ABSENT, which the server turns into the session's end (cli#2201; any weaker result reports as before, never AGENT_ABSENT); with a worker's stop_on_end (never default | manager | always; TRANSITIONAL, goes with host.yaml) it then also stops the worker's agent process group (SIGTERM, 5s, SIGKILL; stop_wait default 60s after a delivered end prompt, immediate with none) and the report names each pid stopped or says no process was touched (on Unix each signal holds the worktree's session-binding lock and releases it right after the syscall, before the grace wait; on Windows authorization spans synchronous taskkill until it returns, with cancellation requested after 10s and a 1s output-pipe WaitDelay (not a hard OS latency guarantee); a timeout is unconfirmed with records kept even after a successful runner return: the tree may already be dead or partly stopped, with available runner error/output retained as bounded control-safe single-line context; successful helper exit with inherited output pipes open past WaitDelay yields incomplete output collection (exec.ErrWaitDelay), also unconfirmed with records kept; cancellation takes precedence if both occur; the stopper declines a lock not acquired in 2s, while session writers block on flock/LockFileEx without that deadline and can wait through synchronous taskkill cancellation/drain) (#2068) (Unix: a missing plain-directory binding cache or lock file is created only when the host uid equals the worktree owner; an unreadable owner or uid mismatch declines creation with recovery guidance; existing locks and Windows are unchanged, cli#2076) (a process not found in the worker's registered worktree, or in a tree bound to another worker/session or an unreadable binding, is never touched) (cli#2057); with a worker's launch_via: orca (default exec; Linux; TRANSITIONAL until S1b/C4) worker.start launches through `orca terminal create` from the worker's launcher_profile {family claude|glm|kimi|codex, mode long|short, model, effort (Codex only), account} and the host's account_profiles {bin, codex_home}, with fixed per-family/per-mode templates (never --resume), TMPDIR/CLAUDE_CODE_TMPDIR/NODE_COMPILE_CACHE from disk_tmp and CODEX_HOME from the account; the grant and the first prompt travel in a 0600 file under <state dir>/runs (never typed); the launched process is proven to be the terminal's processId (pid, tty, cwd) and pinned, and an unproven launch closes its terminal; the command starts the agent only after Orca's create reply has named its terminal, so a lost or malformed reply starts no agent (cli#2130); orca_launch.group is refused at config load (exit 2, ORCA_GROUP_UNPROVABLE): the launch proves its process through /proc, which an sg-started process did not allow on the tested deployment (cli#2121); the agent runs plainly in Orca's per-terminal scope (no per-agent namespace); it never falls back to exec (cli#2058); worktree.remove refuses dirty, untracked, no-upstream, unpushed, or after a failed end; --json: one event object per line; no host key exit 3, bad config exit 2, APP_KEY_SCOPE_REQUIRED/HOST_KEY_REQUIRED exit 8; delivery.enabled (cli#907): each poll also reads the host's chat batches (key also needs attention:deliver) and types each only into an empty agent composer via orca_bin, then acks it; events delivery-delivered|reconciled|held|deferred|skipped|error (codes, ids, spans; never text or proof); a read APP_KEY_SCOPE_REQUIRED turns delivery off for the run (delivery-off event, exit unaffected); holds are UNREPORTED to the server until cli#940 (no server alarm); a message over 131,071 UTF-8 bytes fails delivery-error too-long-to-type (never truncated or acked, stays due) and, oldest first, blocks that worker's later chat until hadron-server#2121; tick/task/stop wakes (cli#1178, cli#1629): each poll also reads dueWakes with accepts MENTION|TICK|TASK|STOP on the whole-lane first page (no workerRefs, no after), which declares the host's lane as a stop consumer, and types each wake's TICK/TASK reasons as one own-prefixed line (its free text, a task title or ticker name, has control characters and Unicode format characters such as bidi overrides, zero-width characters and the byte order mark folded to single spaces, the rule `team wakes due` uses; cli#2154), recorded before acknowledgeWakes; a wake naming channels or a MENTION is typed once and by default held from ack (delivery.ack_mixed opts into deployed A′ as described below); a refused or unconfirmed send leaves no blocking record (the next pass sends fresh; our own unsubmitted line is Enter-submitted or cleared, never retyped); status --json delivery.workers[].unreported; per worker also lastAgeSeconds, heldSince (start of the current run of holds; only a delivered or reconciled event ends it; null when none) and heldOver30m (text row: "HELD OVER 30m: no delivery since <time>") and notRevisited (a held record whose newest event is older than three poll intervals: no newer local event, so the hold may be stale; the cause is not recorded: the server may list no wake for that worker, or the poller stopped or its reads failed; text: "NOT RE-EVALUATED") (cli#1178, cli#1322); stop notices (cli#1629): a STOP reason's notice is typed first in the server's wording, at most once per eventId, through the same empty-composer guard, and never with Escape or any interrupt key (interrupt: true changes nothing sent); a HALT that cannot be typed (draft, question, unknown screen, unsubmitted line, changed incarnation, no terminal) is neither typed nor acked, stays due, and is recorded once per notice in <state>/delivery/stop-dropped (ids and codes only); report-stops flags each such record with one team-chat post authored by the live session bound in the current worktree, which must be --reporter-worker's (no binding, ended or another worker: nothing posted, exit 7, reports stay pending; never as you), tagging the halted worker's reportsTo, else its holder; at most one post per notice, never silently none, NOT exactly-once (an intent is written before posting, a retry first finds an earlier post, a TEAM_CHAT_DUPLICATE refusal records its earlierSeq; an accepted seq and its author are saved before read-back, then retries only read that seq and never re-post; a mismatch stays failed/pending with "needs a person", and a later matching read-back can finish it; one run per outbox); its reads never move a read mark; a failed report exits 7; recorded-seq failures add reason (readback-mismatch, readback-not-found, invalid-accepted-record, readback-unavailable) and optional field (author, body, author,body), never body text/author id/hash/length; .lastfail is only a last-run observation, never posting authority; diagnosticError lastfail-write-failed or lastfail-remove-failed changes neither result nor exit; --dry-run takes no lock, calls no server and writes nothing: pending/stale plus needsPerson and records (cached mismatch/not-found/invalid-record observations only, oldest heldAt/eventId first, cap50, recordsTotal/recordsTruncated), lastObservedAt is the cached time, not since/current state; missing/unreadable/stale cache stays pending without needsPerson, and reported markers win; human cached rows end with “as last observed by report-stops, not a current check or a final diagnosis”; no recovery/certification command Under an App whose setup source is SERVER (hadron-server#4241, the registration first asks for `refusedWorkers` in a separate document and, on a schema-skew refusal from an older server, registers with the unchanged plain document and remembers that, so no host poller breaks (it asks the extended document again every 30 minutes, so a server upgraded while the host runs is noticed); the poller then says once (`refusals-unknown`) that it cannot tell which workers were left out), each registration worker the platform did not assign to this host is reported as one `worker-refused` event (`<worker>: HOST_WORKER_NOT_ASSIGNED …`) when the refused set changes, never every poll, and one `worker-refusals-cleared` event when a refused set empties; the registration and collection still proceed, exit 0, host key only, no worker session (cli#2091). The server text is made safe to print. The poller re-reads its file every poll (cli#2623): a worker added to or removed from `workers`, a worker entry changed in place, and changes to `repos` and `account_profiles` apply on the next poll with no restart (event `config-reloaded`, naming the workers); content is applied on the second poll that sees it (a half-written file is never applied); every other setting is read at start, so a change to it is logged as `config-restart-needed` (naming the keys) and waits for a restart; the file, every program or directory a changed entry names (launch/end/recover programs, account programs, repo clones, codex_home, claude_config_dir, disk_tmp) and every directory above them and above the file must be owned by the poller's user (or root) and not group- or world-writable (a sticky directory such as /tmp is accepted above them), or the reload is refused (the check stops other users and loose modes; a process running as the poller's own user is inside the poller's trust boundary, as it is for a restart); a file that does not load or fails the start-time checks changes nothing (event `config-reload-refused` with the reason, once per content and reason), and a worker newly launching through Orca is refused the same way (that wiring is made at start); a worker removed from the file keeps any live agent: `worker.end` for it is carried out with the entry it last had (event `removed-worker-end`), other operations for it are refused as not configured; directories on the way to a path are judged as the kernel resolves it, one component at a time, including those a symlink chain passes through (a writable directory holding a symlink to a safe place is refused, and each symlink followed must be owned by the poller's user or root); `status` prints SCREEN UNKNOWN, with what it can mean, on a worker whose newest delivery event is held/screen-unknown. A launched agent's process group that the host keeps held (the exited leader's zombie, kept while membership is uncertain or a member still lives; never bounded or reaped on that account, #2042) for longer than 10 minutes is announced once as a `drain-held` event (outcome = code live-member|unseen-pid|vanished-member|late-fork|unreadable, detail `pgid=<n> held=<duration>`) and once more as `drain-ended` (outcome drained|released) when the hold ends; codes and numbers only, never /proc text, no repeat per pass, nothing signalled or reaped for it (cli#2142).
hadron team host up --host <id> --worktrees-root <path> [--desired-state-file <file>] [--dry-run | --apply] [--worker <ref>] [--config <host.yaml>] [--run-id <token>] [--orca-bin <path>] [--timeout 2m]   # cli#1389: reconcile one host's workers through the typed queue; dry-run default; see "## Host up" S10 (#2056): --config supplies a separate 0600 key_file-only reporting client, never reconciliation credentials; completed OBSERVE/APPLY passes record times, worker counts and at most 50 token/outcome/reason-code lines; OFF records none. --run-id replays the same host token (default UUID). Without config prints sweep run not recorded: no host key (pass --config); JSON sweepRun={recorded:false,reason:NO_HOST_KEY,runId}. Schema absence says S10_UNAVAILABLE; a config that cannot be read says HOST_CONFIG_UNREADABLE, an absent or unreadable key HOST_KEY_UNAVAILABLE, any other client failure HOST_CLIENT_UNAVAILABLE (closed codes, no path or key); record errors are codes and never change reconcile exit. No CLI chat posts; server owns summaries/stale alarm.
hadron team host sweep --config <host.yaml>   # C1 (#2056): one host-up pass from saved poller registration/config App/server; App sweepMode gates actions. Systemd user timer every 1m, Restart=on-failure/10s, StartLimitBurst=3 in 60s, fixed-code OnFailure journal logger; no script schedule. Existing health/report-stops timers retained. C5 poller loop owns open-session recovery; no direct relaunch. C5 owns recovery; applying managed-daemon outages queue nothing, record ORCA_DOWN metadata, and the poller reports the first failure once per persisted episode. Unknown readiness remains RELAUNCH_UNPROVEN.
hadron team host sweep install --config <host.yaml> [--bin <abs path>]   # one-minute systemd user timer, crash-loop limit and fixed-code OnFailure logger; retains health/report-stops timers and App mode
hadron team host sweep status   # local scheduler state and last run
hadron team host sweep uninstall   # removes only sweep units, including failure logger
hadron team host sweep-runs --host <id> [--limit N]   # S10 newest-first history, limit1..100 default20; normal read credential, no worker session; JSON array with explicit DTOs, [] when empty.
hadron team host import --from <host.yaml> --host <id> [--apply [--yes]]   # cli#2061 (C6): DRY RUN by default, writes nothing; per host.yaml entry: create|unchanged|differs|unchecked|conflict|skip (+why; unchecked = the server lacks hostSetup, hostSetup:"missing"; conflict = two entries name one worker with different asks; fileApp, fileServer and warnings report the file's App/server vs the ones read: the App is compared by server identity, the server by origin; a comparison that could not be made also warns, and ANY warning refuses --apply, nothing written), launch line as program+arg count only (never its arguments), launcher profile "unmapped" until S1b, plus roster workers with no entry ("missing"); json {appRef,hostId,applied,hostSetup,fileApp,warnings,entries[],missing[],counts}. --apply (asks; --yes when not interactive) writes ONLY the create entries, per worker: setWorkerLeaveAlone FIRST, then assignHostWorker, both the signed-in manager's own writes by worker id, each counted only when the server's answer confirms it; an already-set leave-alone mark is kept (leaveAloneKept), never overwritten; refused (usage, nothing written) when the file's App is not the App read; a refusal on one entry is recorded in entries[].applied {leaveAlone,leaveAloneKept,assigned,unknown,error} (marked-not-assigned is the safe half-state; unknown = a lost/unconfirmed answer, check server state before rollback) and the others still run, except a systemic failure (auth/forbidden/unreachable) which stops the run; exit 1 when any entry failed; a re-run is idempotent; refused (usage, nothing written) when the server lacks hostSetup; a differs is never replaced
hadron team host assign <worker> --host <id> --workdir <token> --family <family> [--model <id>] [--effort <token>] [--account-profile <token>] [--replace] [--instruction <kind>:<ref>] | unassign <worker> --host <id> [--instruction <kind>:<ref>] | setup [--host <id>] | setup --source HOST|SERVER [--instruction <kind>:<ref>] (uses --app or the binding)   # hadron-cli#2080 over hadron-server#4241 (cor:agt:070:01, the change Proposed): the platform's record of which workers each host of the App runs, and the per-App switch. assign: one `assignHostWorker`; workdir is the NAME the host knows the directory by (a token: letters, digits and . _ / @ + -, starting with a letter or digit, no "..", "//" or trailing "/"; never a path), the launcher profile `--model`, `--effort` and `--account-profile` are optional (S1b, hadron-server#4230; while the App's switch is SERVER a worker start carries the ones that are set), `--effort`/`--account-profile` need S1b (hadron-server#4230): on a server that predates it the command is refused by name, exit 2, nothing written, and never falls back to a write without them; without them the plain S1a documents are used and any server works (so host up, host import and a plain assign never send a document selecting the profile). Each is a profile token: letters, digits and . _ -, starting with a letter or digit, at most 128 characters (the model too: the older looser model rule is gone; never a path, a command or a credential). A profile flag that is not given is OMITTED from the request, never sent as null; on a NEW assignment that leaves the value unset, but `--replace` makes the server rewrite the whole assignment, so a profile value not given again is cleared (the model included). `--replace` is sent ONLY when given (else an existing assignment is HOST_ASSIGNMENT_EXISTS, exit 5). The tokens are checked with the server's pattern BEFORE any request (exit 2, nothing sent); the server's own refusals are still mapped: BAD_HOST_ASSIGNMENT_ARGS 2, BAD_LAUNCHER_PROFILE 2 (a bad profile value, or a family with no launcher: claude, codex, glm or kimi), HOST_ASSIGNMENT_NOT_PERMITTED 8, WORKER_NOT_FOUND 4, WORKER_RETIRED 5, HOST_ASSIGNMENT_EXISTS 5; a refusal prints nothing partial. unassign: "removed" or "there was none" (exit 0 both; `--json` {appRef,hostId,worker,removed}); a missing or null answer is an UNKNOWN outcome (exit 1, nothing printed, never retried): run `team host setup` before repeating it (cli#2090). setup (no --source): READ `hostSetup`, the switch plus the assignments in effect by host then worker (a retired worker's are left out); `--json` {appRef,source:"HOST"|"SERVER",hostId (null unless --host),assignments:[{id,hostId,workerId,workerName,workdir,family,model,effort,accountProfile,createdAt,updatedAt}]} (effort and accountProfile: a profile token, or an explicit null when unset) and `profile` "available"|"unavailable" (a server that predates S1b refuses the profile-bearing document at validation: setup then falls back to the S1a document, effort/accountProfile read null meaning UNKNOWN, and the text says so once), `[]` when none. setup --source = `setHostSetupSource` (`--json` {appRef,source}); HOST (default) keeps host.yaml authoritative, SERVER makes registration and starts follow the assignments (takes effect at the next registration/start decision). WHO writes: whoever manages the App; without --instruction a write is the signed-in person's even in a bound worktree, with --instruction the holder's claim goes through the worktree's session when its binding is for this server and the target App (else as the person, with a note), never retried as the person after a refusal. Reads never send X-Hadron-Session. Importing host.yaml is cli#2061; `host up` reading `hostSetup` is a follow-up on cli#1389.
hadron team health sweep --config <host.yaml> [--dry-run[=false]] [--status-timeout <d>]   # code-only terminal health reporting (cli#1181) plus the #2017 title pass and the #2027 duplicate phase; uses host key_file (0600) and the terminal-status@1 command JSON, computes no alarm, installs no cadence (the health install entry below does) and never types into a terminal; after the reports are stored it sets the title of every Orca tab in a registered worker's worktree to the worker's bare name when it differs (a tab is matched by its worktree's session binding, so a plain shell or an unbound worktree is never touched), then detects more than one agent process and more than one terminal tab per bound worker worktree: dry (the default) lists them, --dry-run=false (what the scheduled entry passes) applies the renames, stops the unsupervised duplicates — a pidfd-pinned SIGTERM, never the supervised agent, never a lone agent, never an unbound worktree — and closes extra bare-shell tabs (a fresh listing and re-read immediately before, so nothing mid-turn and no pane added since goes unread), one team-chat line from the sweep's own bound session (an unbound sweep posts nothing); a team pause or an unfinished context reset means report only; json {reports,refused,renames,orcaVersion,dryRun,fixes[{worker,action,subject,applied}],alerts[{worker,code,count}],skips[{worker,code}]} (renames: one entry per tab renamed or that a dry run would rename; fix codes stop-agent|close-tab; alert codes unsupervised-agents|multiple-supervised|stop-failed|close-failed; skip codes resetting|team-paused|pause-unreadable|close-gone|close-not-bare|close-recheck-failed (a close declined by its read-adjacent recheck: the tab left, turned, or cannot be re-verified)); a tab that fails to rename is listed with its error, the rest still go ahead, and the sweep then exits 1; duplicate findings it cannot act on become alerts and never fail the sweep; exits 0/1/3/7/8/10 (usage 2), per-item refusals exit 0
hadron team health install --config <host.yaml> --interval <Nm> [--bin <abs hadron>]   # writes one user-level scheduler entry `hadron-team-health` running the sweep; see "Team health install" (cli#1374); exit 2 on no scheduler / orca unresolved / bad interval / different --config
hadron team health uninstall   # removes exactly what install wrote, also after a failed install
hadron team health status   # read-only; json {installed,scheduler,active,interval,binary,config,orca,files,lastRun{at,exitCode}}; lastRun unknown under cron
hadron team usage collect --config <usage.yaml> [-m <memory>]   # cli#1472: record one account-usage object per configured model account (allowance windows, family claude/codex/glm; glm records "no local source"); validates every account first, exit 2 and NOTHING written on any bad config/snapshot/ccusage file; see "Team usage"
hadron team usage show [--config <usage.yaml> | -m <memory>] [--limit 1-200]   # per-account current use, resets, burn rate %/h, projected exhaustsAt; json {memory,accounts:[]}; unknown is null
hadron team usage accounts   # cli#1875: the SERVER's list of the team App's usage accounts (accountUsageAccounts, hadron-server#3853), printed as returned: server order (label, host, family), one row per account+host+family, never filtered/deduped/re-sorted; json is the envelope {asOf,accounts:[{account,host,family,newestSnapshotAt,workers:[],workersConfigured}],complete} (no history: accounts [] + complete true, never a bare []); complete false (over 50 accounts, or the search stopped short) is exit 0 with a stderr note in text mode; workersConfigured false prints "not configured", an empty configured list "none"; APP_NOT_FOUND passes through unchanged; a server without the field (EVERY error exactly `Cannot query field "<field>" on type "Query".`; cli#1895): exit 1 "too old" + "(server said: <its message>)"; "Unknown argument", a mixed envelope (that error beside APP_NOT_FOUND or any other) and any other error keep their own mapping
hadron team usage project [--account <label> [--host h] [--family f]] [--add-workers N | --move-worker <worker> --to-account <label> [--to-host h] [--to-family f]]   # the SERVER's token-burn projection (accountUsageProjection, hadron-server#3820), printed unmodified; per window lowPctAtReset..highPctAtReset + confidence LOW|MEDIUM|HIGH, runsOutAt only when the server returns one, status PROJECTED|INSUFFICIENT_DATA|UNKNOWN (+reason, windows null); json {app,accounts:[{account,host,family,asOf,newestSnapshotAt,workers:[],workersConfigured,windows:[],whatIf:{adjusted:[],target:{account,baseline:[],adjusted:[]}}|null}],complete?,whatIf}; no --account (cli#1878) = the SERVER's list (accountUsageAccounts, as `team usage accounts`), each entry's account/host/family projected unchanged, one call each, in server order (no client read/sort/dedupe); "complete" is present ONLY then (the server's value), absent with --account; complete false = exit 0 + one stderr warning line in text mode; empty list prints the empty result; APP_NOT_FOUND passes through; a null list from the server: exit 1 "server returned no account list"; -m/--memory or --config without --account: exit 2 before any call (ignored with --account); --account given empty or blank (`--account ""`, `--account=`, a quoted unset variable): exit 2 before any call, never read as omitted (cli#1899); what-if needs --account and exactly ONE kind; bad flags/--add-workers<1/half a move pair exit 2; a server without the field (EVERY error exactly `Cannot query field "<field>" on type "Query".`; cli#1895): exit 1 "too old" + "(server said: <its message>)"; "Unknown argument", a mixed envelope (that error beside APP_NOT_FOUND or any other) and any other error keep their own mapping
hadron team usage install --config <usage.yaml> --interval <Nm> [--bin <abs hadron>] | uninstall | status   # a second user-level scheduler entry `hadron-team-usage` beside team health's; same refusals and json shapes minus orca
hadron team router poll | loop [--interval 60s] | status [--config <path>]   # RETIRED (cli#935, hadron-server#2027): each exits 2 (Usage) with "hadron team router is retired (hadron-server#2027): the attention API it polled is being removed. Wakes come from tickers (hadron-server#2138). Nothing was polled or typed."; --json gives the standard error envelope; it reads no config, state directory or credentials, polls nothing and types into no terminal; old flags are accepted and ignored, unknown flags stay usage errors
hadron org resend-sender create --org <ref> --from <identity> [--api-key-file <path|->] [--enabled=false] | get <sender-id> | list [--org-id <raw-id>] [--limit N] [--offset N] | update <sender-id> [--from <identity>] [--api-key-file <path|->] [--enabled=false] | rm <sender-id> --yes
hadron org github link <org> --installation-id N | unlink <org> [--yes] | set-app <org> --app-id <id> [--private-key-file <path|->]   # cli#2512 (gap #60): the org's GitHub App installation and credentials; the server resolves the org ref as typed (id or URN) and checks org ADMIN or platform ADMIN — that is the server's fact, not a CLI rule; the CLI never runs the browser install flow (install the App on GitHub first and pass the installation id it produces); each verb sends exactly ONE mutation, never retried; --installation-id must be a positive integer up to 2147483647 (exit 2, no request); unlink removes ALL GitHub App config (installation + app id + private key), destructive: prompts on a TTY, non-interactive needs --yes (exit 2, no request); set-app sets app id and key together every call: the private key is WRITE-ONLY — --private-key-file takes a path or -, omitted reads piped stdin (a masked prompt is refused: the key is multi-line PEM a single-line prompt would truncate, so on a terminal the flag is required, exit 2), there is NO inline key flag, and the key appears in no stdout, stderr, --json, error text or server-code echo (redacted out of reflected metadata); DTO {id, urn, githubAppId, githubInstallationId, hasGithubAppPrivateKey} (nullable fields null when unset), same for text; FORBIDDEN exit 8, NOT_FOUND exit 4, no follow-up mutation after a refusal
hadron user invite [--email <email>] [--github-username <name>] [--name <display-name>] [--expires-in-days N] [--max-activations N]   # platform invitation, not org membership; no email sent; deliver the returned acceptance slug privately
hadron user search [query] [--limit N] [--offset N] | set-roles <userRef> --role <r>... --yes | merge <source> --into <target> --yes | delete [--user <ref>] [--reason <text>] --yes
hadron admin impersonation list [--org <org>] [--active] [--limit N] [--offset N] | get <id>   # audit reads only (start/stop is `auth impersonate`); the server decides who may read: org ADMIN/OWNER for that org, platform admin for any, else refusal (exit 8); get of a denied or missing id is one exit 4; list drains all pages unless --limit/--offset
# user delete is IRREVERSIBLE erasure: no --user deletes YOUR OWN account (no role needed, then run `auth logout`); --user needs a platform admin and a --reason of 1-500 characters; anyone else naming a target gets the same exit 8 whether or not the user exists; an org you solely own with other members blocks it (server code SOLE_ORG_OWNER, nothing deleted); --json is {"deleted":true,"user":<ref or null>}
hadron profile set [--name <n>] [--handle <h>] [--email <e>]   # --email is accepted and REFUSED locally (exit 2, "Changing your email address is not supported here.") — the flag stays so scripts fail loudly (#844)
hadron server-info
hadron run trigger --app <ref> --entry <node-urn> [--as-self] [--agent <ref>] [--arg k=v]... [--ai-config <n>] [--budget-tokens N] [--budget-actions N] [--wait] | list [--statuses <csv>] [--parent <id>] [--sort createdAt|startedAt|finishedAt|status] [--dir asc|desc] [--app <ref> | --org <ref>] [--status <s>] | get <id> | cancel <id> --yes
hadron ticker list | get <id-or-URN> | create --name <n> --cadence-seconds <n> [--reasons read-team-channel,every-tick,live-check] | update <id-or-URN> [--reasons <list>] | delete <id-or-URN> --yes | subscribe <id-or-URN> --worker <id-or-URN> | unsubscribe <id-or-URN> --worker <id-or-URN> | apply-templates --worker <id-or-URN>
hadron schedule preview --app <ref> --entry <node-urn> (--cron '<expr>' | --run-at <RFC3339>) [--tz <zone>] [--agent <ref>] [--as-self] [--ai-config <n>] [--arg k=v]... [--disabled] [--budget-tokens N] [--budget-actions N] [--json]
hadron schedule create [--scope <id|name>] --app <ref> --name <n> (--cron '<expr>' | --run-at <RFC3339>) [--tz <zone>] --entry <node-urn> [--as-self] [--policy <json>] [--ai-config <n>] [--arg k=v]... [--budget-tokens N] [--budget-actions N] | list --app <ref> | update <id> [--run-at <RFC3339>] [--scope <id|name|"">] [--agent <ref>] ... [--budget-tokens N|--clear-budget-tokens] [--budget-actions N|--clear-budget-actions] | rm <id> --yes
hadron automation preview <kind> <ref> | re-adopt <kind> <ref> --expected-hash <digest> [--yes] | list | approval (<name> | --run <run-id>) [--depth] | approve (<name> | --run <run-id>) [--yes] | decline (<name> | --run <run-id>) [--yes]   # cli#1071: an App owner reviews and approves schedules/webhooks without a row id or hash; name is schedule/<name>, webhook/<name> or a bare <name> that exactly one trigger has; --app on every subcommand
hadron automation team-snapshot-grant set <app-ref> --trigger-kind SCHEDULE|WEBHOOK --trigger-id <id> [--yes] | revoke <grant-id> [--yes] [--instruction <kind>:<ref>] | list <app-ref> [--include-revoked] [--trigger-kind <k>] [--trigger-id <id>] [--limit n] [--offset n] [--instruction <kind>:<ref>]
hadron automation grant set <app-ref> --memory <ref> --trigger-kind SCHEDULE|WEBHOOK --trigger-id <id> [--prefix <loc>] [--yes] | revoke <grant-id> [--yes] [--instruction <kind>:<ref>] | list [--app <ref>] [--memory <ref>] [--include-revoked] [--instruction <kind>:<ref>]
hadron webhook create [--scope <id|name>] --app <ref> --name <n> --entry <node-urn> [--as-self] [--policy <json>] [--args-schema <json>] [--ai-config <n>] [--budget-tokens N] [--budget-actions N] | rotate <id> --yes | list --app <ref> | rm <id> --yes
hadron ticket mint --org <ref> [--app <ref>] --action comm.outbound --count <n> [--note <why>] [--expires <iso>] | list --org <ref>
hadron billing prices [--provider <p>] [--model <m>] [--tier <n>] [--limit N] [--offset N] | prices get <id> | balance <org> | ledger <org> [--limit N] [--offset N] | prices set --provider <p> --model <m> --input <n> --output <n> [--tier 1-3] [--markup-bp N] [--effective-at <t>] [--yes] | grant <org> --amount <n> [--source-type <t> --source-id <i>] [--expires-at <t>] [--note <s>] [--yes]   # spec-046 metering (cli#215): READS, then two platform-admin WRITES; <org> is an ID or hrn:org:<slug>. Money fields (inputMicroCentsPerMTok, outputMicroCentsPerMTok, balanceMicroCents, amountMicroCents) are base-10 integer STRINGS of micro-cents (1e-8 USD), signed on the ledger and unbounded, passed through byte for byte: parse with a bigint, never a float. Null markupBasisPoints = the server's default markup. prices get <id>: one row (the id is the ID column of the prices table, or "id" in prices --json), the same DTO as a prices row as one object; a null row exits 4 NotFound "not found or not visible" (the server does not say which); an empty id exits 2 with no request. prices and prices get: any signed-in caller; balance/ledger: org ADMIN, else FORBIDDEN exit 8. No --limit/--offset lists everything. WRITES (`prices set`, `grant`; platform admin, else FORBIDDEN exit 8): they change money records, so each asks y/N on a terminal and, without one, REFUSES with exit 2 and ZERO requests unless --yes; required flags absent or empty exit 2 with no request. Money flags (--input, --output, --amount) are opaque strings sent byte for byte: the CLI does not parse or range-check them (the server owns positivity/domain and its refusal passes through, exit per error class). Optional flags (--tier, --markup-bp, --effective-at, --expires-at, --note, --source-*) are OMITTED when unset so server defaults apply; an explicit `--markup-bp 0` is a value; an empty string flag exits 2. `grant`: --source-type/--source-id are a pair (both or neither, else exit 2 before any request); a repeated pair makes the server return the EXISTING entry instead of crediting twice, but the response carries no created/reused flag, so the CLI prints the returned entry identically either way and never claims 'already granted'. Without the pair a repeat credits again; the CLI NEVER retries a write. --json prints one DTO (a rate row as in `prices`, or a ledger entry as in `ledger`) with money as strings. --expires-at is recorded, not enforced
hadron grant create --org <ref> --user <ref> --action <a>[,...] [--expires <iso>] | list [--org <ref>] [--user <ref>] | get <id> | revoke <id> --yes   # organization-level grant (org member management actions), retiring; App grants: `hadron team grant` (cli#2672)
hadron policy explain --action <a> --app <ref> [--user <ref>] | set --mine (<json> | @file | --clear)   # cli#1343; see that section
hadron quota list --org <ref> [--app <ref>] | set --org <ref> [--app <ref>] --action <a> --limit <n>   # cli#1343; org ADMIN
hadron calendar list --connection <ref> | events --connection <ref> [--calendar <id>] [--from <RFC3339>] [--to <RFC3339>] [--query <text>] [--top 1..100] | event get <event-id> --connection <ref> [--calendar <id>] | event create --connection <ref> --title <t> --from <t> --to <t> [--all-day] [--tz <iana>] [--attendee <email>]... [--description <t>] [--location <t>] [--calendar <id>] [--no-notify] [--idempotency-key <k>] | event update <event-id> --connection <ref> [same flags, all optional] [--clear-attendees] | event delete <event-id> --connection <ref> --yes | event respond <event-id> --connection <ref> --response accepted|declined|tentative | freebusy --connection <ref> --from <RFC3339> --to <RFC3339> [--schedule <id>]...
hadron drive list --connection <ref> [--folder <id>] [--query <text>] [--name-contains <text>] [--mime-type <type>] [--modified-after <RFC3339>] [--top 1..100] [--page-token <token>] | get <file-id> --connection <ref> | export <file-id> --connection <ref> [--format MARKDOWN|TEXT|HTML]
hadron drive create-doc <node-id-or-qualified-URN> --connection <ref> [--folder <id>] [--title <text>] [--yes]   # one exportNodeToGoogleDoc mutation, never retried; creates a NEW Google Doc in the connection owner's external account, never updates an existing one. Content copied out cannot be recalled from Hadron. Requires confirmation: without a terminal, omitting --yes exits 2 with zero requests; declining the terminal prompt exits 6 with zero requests. Only the connection owner acting as themselves can create; drive.read, App/headless and impersonated callers do not authorize it. Server reads/resolves the node with the caller's access; locked encrypted nodes need global --unlock. Omitted folder/title are absent on the wire (Drive root/node name defaults); empty supplied values refuse locally. Success returns the same file DTO as drive get, including nullable size, parents[], and nullable times/webLink. An unanswered/malformed response exits 7: the outcome is unknown; check the drive before retrying, since a repeat creates another Doc. Server refusals and unknown-code fallback retain normal mapping. drive export stays read-only to local stdout; it never calls this mutation.
hadron email connections --org <id|urn> [--limit N] [--offset N] [--json] | messages --connection <id> [--top N] [--unread] [--json] | folders --connection <id> [--json] | test <connection-id> [--json] | disconnect <connection-id> [--yes] [--json] | reply <message-id> --connection <id> --body-file <path|-> [--yes] | draft save --connection <id> (--reply-to <message-id> | --to <addr>... --subject <s>) --body-file <path|-> [--idempotency-key <k>] | draft update <draft-id> --connection <id> --body-file <path|-> | draft send <draft-id> --connection <id> [--idempotency-key <k>] [--yes] | mark-read <message-id> --connection <id> [--unread] | flag <message-id> --connection <id> [--flag FLAGGED|NONE|COMPLETE] | categorize <message-id> --connection <id> (--category <name>... | --clear) | delete <message-id> --connection <id> [--yes] | move <message-id> --connection <id> --folder <folder-id>   # cli#198: reads (Slice A), test/disconnect (Slice B), mailbox effects (Slice C); every command is ONE request, never retried
hadron slack status [--org <id|urn>] [--limit 0..200] [--offset N] | get <connection-id> | connect --org <ref> --app <ref> --bot-token-file <path|-> --app-token-file <path|-> | disconnect <connection-id> [--yes]   # status/get are read-only Slack workspace connections; one page (server default 20), stderr names the next --offset; tokens/lastError never requested; get on an invisible id exits 4; status is a recorded value, not a health check: ACTIVE = registered, not proof it works (a failed token is not always recorded), ERROR = recorded as failed, reason not shown; recovery is an org admin running disconnect on the ERROR connection first and then connect again (the server refuses a workspace that is still registered, whatever its status); --app and the saved org do not apply to status. connect: one request, never retried, tokens ONLY from files (one flag per token; "-" = stdin for at most ONE of them, both "-" or "-" on a terminal or a missing flag is refused before any request; a token is never a flag value or in any output/error/JSON), --org/--app take an ID or URN (--app also <root>:<slug>), the server decides org ADMIN/OWNER, App-in-org and one connection per workspace (refusals pass through: 8 not admin, 4 not found, 2 bad input), prints the connection like get (11 fields, never a token or lastError; an answer without an id or with an unknown status is unconfirmed, exit 7 (an unknown status names "hadron slack disconnect <id>" inline, since get/status refuse the same status); suggested commands are shell-quoted, use --org=<value> and "--" before an id (a leading-dash id), and never print a --org value that holds a token or a control character (the pointer is then the unfiltered "hadron slack status") or an id with control characters (then no command, only "upgrade hadron / ask the administrator", since get/status refuse the same unknown status); a suggested command is alone on the last line of connect's status pointer and of disconnect's get/status pointer (the unknown-status recovery names its disconnect command inline, mid-sentence); connect refuses "-" on a terminal for EITHER token flag before opening any source; status ERROR is shown as "registered, but ... recorded a failure", not "connected"), a lost answer is "outcome unknown": check hadron slack status --org (one page: follow its --offset hint); every server-supplied string (message, serverCode, success fields) is scrubbed of the submitted tokens before it is shown; disconnect: one request, never retried, asks on a terminal (prompt says soft delete, best-effort token teardown, reconnect registers anew) else --yes is required (nothing sent without it), NOT_FOUND passes through unchanged (an unknown id and another org look the same, no pre-read), the server soft-deletes and tears down the tool side best-effort (not reported), --json {connectionId,disconnected:true}
hadron connection connect <ms-exchange|gmail|google-cal|google-drive> --org <id|urn> [--no-browser] [--wait <duration>|--no-wait] [--json]
hadron connection flow <flow-id> [--json]
hadron connection grant create --connection <ref> --app <ref> --scopes <s>[,...] [--expires-at <iso>] | list [--connection <ref>] | get <grant-id> | revoke <grant-id> --yes
hadron mcp-server list [--org <ref>] | get <id> | tools <id> | create --org <ref> --slug <s> --name <n> --url <u> [--header 'Name: value']... [--allow <tool>]... [--disabled] | update <id> [--name <n>] [--url <u>] [--header ...]... [--clear-headers] [--allow <tool>]... [--clear-allow] [--enabled|--disabled] | delete <id> --yes
hadron web-poll list --org <ref> [--limit <n>] [--after <cursor>] | cancel <job-id> --org <ref> --yes   # org ADMIN page-monitor watches; list shows ONE page (default 20, the server clamps --limit to 100); --json is {items,hasMore,nextCursor} (BREAKING: it was a bare array before cli#2032); pass the printed next cursor to --after to continue, opaque and exactly as printed; no total, not claimed complete; a cross-org or unknown job id is NOT_FOUND; a repeat cancel succeeds with alreadyCancelled
hadron ha list [--org <ref>] [--limit 1..200] [--offset N] | get <id> | ops <id>   # Home Assistant registry READS only (cli#231). list is ONE explicit page (default --limit 20, sent explicitly; the server default is 50), slug-ascending; --json is a bare array and, when more rows exist, stderr prints `showing N of TOTAL; next page: --offset M --limit L`. get/ops: null = absent OR not visible to you (one answer, exit 4; no existence proof). opAllowlist [] means EVERY op, not deny-all. runToolName/inputSchema are the provider's strings. A disabled instance's `ops` is refused by the server (HA_INSTANCE_DISABLED), message shown unchanged. The access token is write-only and never shown; no host/device is contacted.
hadron ha create --org <ref> --slug <s> --name <n> --url <u> [--token-file -|@path|path] [--allow-op <op>]... [--disabled]   # org ADMIN/OWNER (cli#231). The token is NEVER an argv flag: --token-file - reads stdin, @path/path a file; omitted, piped stdin is read (empty = exit 2, nothing sent); an explicitly empty --token-file= is exit 2, never a stdin fallback. No --allow-op = every catalog op; --disabled sends enabled:false. --json is the `ha get` shape (no token). Slug is permanent.
hadron ha update <id> [--name] [--url] [--token-file ...] [--allow-op <op>]... | [--clear-allow-ops] [--enabled|--disabled]   # only the named fields are sent; omitted = kept. No flags = exit 2, nothing sent. Without --token-file stdin is never read and the token stays. --clear-allow-ops sends [] = EVERY catalog op (not none; use --disabled to stop use). --allow-op/--clear-allow-ops and --enabled/--disabled are mutually exclusive. No slug flag.
hadron ha rm <id> [--yes]   # hard delete; TTY prompts, non-interactive needs --yes (else exit 2, nothing sent). --json {id, status:"deleted"}; false from the server is exit 1, never success. Nodes naming ha__<slug>__<op> then fail at run time; nothing is repaired.
  ha create/update/rm: server refusals keep their exit code and --json serverCode (not admin exit 8, missing or another org's instance exit 4 NOT_FOUND, invalid input exit 2). A lost response is exit 7 "outcome unknown": sent once, never retried; read back with `ha get`/`ha list` before retrying.
hadron secret create --name <n> --scope user|org|app [--owner <ref>] --kind generic|webfetch-auth [--value-file -|@file] | list --scope user|org|app [--owner <ref>] | get <id|urn> | update <id|urn> [--value-file -|@file] [--meta k=v...|--meta-json <obj>] [--type bearer|basic|header [--username <u>|--header-name <h>]] [--url-prefix <u>] | set <id|urn> --value-file -|@file [--type bearer|basic|header [--username <u>|--header-name <h>]] | versions <id|urn> [--limit N --offset N|--all] | events <id|urn> [--limit N --offset N|--all] | rm <id> --yes | protect <id|urn> --on|--off | change get <request-id> [--wait [--wait-timeout <dur>]] | change withdraw <request-id> | exec --client <client-id> --env <NAME> -- <command> [args...] | reveal <client-id> [--out <file>] [--allow-non-terminal] | client issue <secret> --reason <text> [--worker <worker-id>] [--starts-at <ts>] [--expires-at <ts>] [--max-reads <n>] [--auto-delete] [--wait [--wait-timeout <dur>]] | client list [--limit N --offset N|--all] [--as-worker] | client revoke <client-id> --yes
# **protected-secret changes (cli#2738, hadron-server#3999 S4, cor:sec:070:02):** on a PROTECTED App secret every change is STAGED, never applied. `secret create|update|set|rm|protect` send their one mutation (never retried); when the server answers `SECRET_PROTECTED_NEEDS_PASSKEY` carrying a request id, the command prints it and the `ceremonyUrl` BYTE FOR BYTE (stderr in text mode, a URL with control or format characters is withheld from text with exit 5 and read with `--json`), says the change is staged until a person signs, and exits **5**; the `--json` error envelope `{code:5,message,serverCode:"SECRET_PROTECTED_NEEDS_PASSKEY",requestId,ceremonyUrl}` carries both exactly (`requestId` here is the change request's id). The CLI NEVER opens, calls or completes the ceremony: hand the URL to the person. The same code with NO request id (signing off, or a caller that is not a person acting as themselves) is not "a request is open": nothing was staged, exit 2, "this server does not offer protected-secret changes". `secret protect <ref> (--on|--off)` opens the protection change (`setSecretProtected`; neither or both flags exit 2, nothing sent). `secret change get <request-id>` is one read of YOUR request (`--json` `{id,state,operation,createdAt,expiresAt}`; state open|completed|declined|expired|withdrawn|stale; never the value): any state exits 0, a null answer (unknown or not yours, one answer) exits 4. `--wait` (`--wait-timeout`, default 10m, polls every 2s) ends: completed 0; declined/expired/withdrawn/stale 5 (the record is printed first); still open at the deadline **13** (nothing is withdrawn); Ctrl-C 6; a state the CLI does not know exits 1. `secret change withdraw <request-id>` sends ONE `withdrawSecretChangeRequest` (never retried): true exits 0 `{id,status:"withdrawn"}`, an explicit false (not open) exits 5, unknown or not yours 4, FORBIDDEN 8; a missing or null result, or a lost or faulty answer after the write (5xx, internal error), is an unknown outcome (7 for a missing result; the code of the failure otherwise) that says it may have been withdrawn and to run `secret change get <id>`. A staged refusal whose ceremony URL is missing, null or blank still exits 5 with the request id kept (`--json` `ceremonyUrl` empty): the text says no person can sign it and to run `secret change withdraw <id>` and try again. `secret protect` always expects a staged request (the server never applies it directly): a secret returned instead is NOT reported as a change, exit 7 "not confirmed", with `secret get <ref>` to check. `protect` and `change` on a server whose signing is off (the fields are absent from its schema) exit 2 with the same sentence. SECRET_CHANGE_REQUEST_LIMIT (too many open requests on one secret) exits 5; the limit refusal, and either protected-change code inside an answer that does not prove what was staged (several errors, an error below the mutation, an HTTP 5xx), print a FIXED sentence, never the server's message (it can repeat a submitted value), and keep the exit class and the `--json` `serverCode`; the per-requester cap's number and code are the server's, never assumed here. `create|update` do not yet reach the `protected` argument (cli#2815). # **secret client (cli#2631 slice 1):** a read client is one secret.reveal grant that a PERSON signs. `client issue` is a person's act and NEVER sends a worker session, even from a bound worktree: one issueSecretReadClient, then one requestGrantSignature on the returned grant id (each sent once, never retried), then it prints clientId, secret, selfIssued, grantId, the signing request (id, state, expiresAt) and the `ceremonyUrl` byte for byte (never composed; text refuses a URL with control or format characters, exit 1; `--json` carries it exactly). The grant allows nothing until a person signs on that page. `--json` `{clientId,grantId,secretName,selfIssued,signingRequest:{id,state,expiresAt,endedAt,ceremonyUrl}|null}`; `signingRequest` is null when no signing request was received after the issue: the server refused the step, or its answer was lost (exit 7: the error says a request may exist and to run `team grant request-signature <grant>` to ask again). The issue is still printed, the exit code carries the outcome, and the error names the grant id. With `--wait` and a ceremony URL carrying control or format characters, or when the URL cannot be written to stderr, nothing is polled: the issue is printed (text prints every field except the URL, then refuses it with exit 1; `--json` carries the URL exactly) and the command exits 1 naming the open signing request. An issue answer with an empty client or grant id is refused (exit 1) before any signing request. A signing request answer with no id or no ceremony URL is not a success either: the issue is printed (signingRequest null), nothing is polled, and the command exits 1 with the same recovery as a lost answer. Cancelling the command during `--wait` exits 6, not 13. Holder: you (`USER`, selfIssued) or `--worker <id>`. `--reason` is required; unset optional flags are omitted. `--wait` (off by default) prints the URL to stderr first, then polls the signing request every 2s up to `--wait-timeout` (10m): COMPLETED exits 0; DECLINED/EXPIRED/STALE/WITHDRAWN exit 5 with the record printed first; a timeout exits **13**, withdraws nothing and names `team grant signing-request get`. A lost answer to the issue (7) is never resent and says a pending client may exist: check `secret client list`. Refusals: FORBIDDEN and GRANT_HOLDER_NOT_ALLOWED exit 8; SECRET_AUTO_DELETE_NOT_AVAILABLE and _NEEDS_LIMIT exit 2; SECRET_READ_CLIENT_SUPERSEDED and _LIMIT exit 5; a server without signing exits 2 ("does not offer"). A signer the server refuses leaves the request OPEN, so that wait ends by timeout. `client list` lists the clients the caller issued or holds (`--all` stops with an error after 1000 pages; `--json` an array, `[]` when none, rows `{clientId,grantId,secretId,secretName,selfIssued,endReason,createdAt}`); acting as you by default, `--as-worker` sends the bound session and lists that worker's clients; an App key, a run or an impersonated session gets an empty list, which is not proof that none exist. **`client revoke <client-id> [--yes]` (cli#2680)** revokes a read client: it pages `mySecretReadClients` for that exact client id (a client you neither issued nor hold never lists: exit 4, `no read client ...`, no revoke sent), then sends ONE `revokeGrant` on its `grantId` (never retried), as you (no worker session, even from a bound worktree; no instruction). It cannot be undone, so on a terminal it asks (naming the client and the secret) and off one it needs `--yes` and sends nothing, not even the read, without it (exit 2). `--json` `{clientId,grantId,state,revokedAt,allows}`: the grant as the server returned it. `REVOKED` exits 0; a grant that had already ended (`EXPIRED`, `WITHDRAWN`, `SPENT`, `HOLDER_GONE`) is returned as it is, nothing changes, exit 0 with 'already ended'; success needs the SAME grant id back in an explicit ended state (`REVOKED`, `EXPIRED`, `WITHDRAWN`, `SPENT`, `HOLDER_GONE`): a null answer, another grant's id, a missing or unknown state, a grant still `ACTIVE` or `PENDING`, or one in an ended state whose `allows` is true or missing/null, is 'not confirmed' (exit 7); a client row with no grant id is refused (exit 1) before the prompt, nothing sent; the text says the grant `is REVOKED` (an earlier revoke reads the same: the first end stands), and the DTO never reports the requested grant as ended on the strength of another's answer. Only a pre-write refusal at the mutation's own level keeps its typed exit (`GRANT_NOT_MANAGER`, `FORBIDDEN`, `UNAUTHENTICATED`, `BAD_USER_INPUT`, GraphQL validation/parse); EVERY other failure (a lost answer, a 5xx, an internal error, an error beneath `revokeGrant` such as a result field failing after the write) is exit 7, an unknown outcome: the error names the grant and says to run `secret client list --all`, the server code is kept for `--json`, and the revoke is never resent. `GRANT_NOT_MANAGER` (not allowed, and a grant that does not exist or cannot be seen, one answer) and FORBIDDEN exit 8; UNAUTHENTICATED exits 3. The server decides who may revoke: the issuer (while still in the App's Org) or whoever manages the App. # **secret exec / reveal (cli#2664 slice 2):** a read client is revealed by the WORKER it names, in that worker's bound session; a person reveals on the server's passkey page and gets the server's one uniform answer here (`SECRET_READ_CLIENT_NOT_FOUND`, exit 4, never which cause applied) and no link to that page yet (hadron-cli#2758). Unbound, the call is still sent, without a session and with a stderr note; a binding for another server or App is refused (exit 2, nothing sent). ONE `revealSecret` per process, never retried: a lost answer (7, or 1 with no server code) says the read may be spent and to check `secret client list`. Every local check runs BEFORE the call (a reveal spends a read): bad `--env` (`^[A-Za-z_][A-Za-z0-9_]*$`), no command after `--`, a blank client id, the command not found, `exec --json` (refused, exit 2: the child owns stdout), `reveal` to a non-terminal without `--allow-non-terminal` or `--out`, an existing `--out` path. `exec`: the child gets your environment plus `NAME=<value>`, stdin/stdout/stderr pass through, SIGINT/SIGTERM are forwarded, hadron prints nothing; once the child has STARTED hadron exits with the child's own status (128+n for a signal), every hadron refusal happens before and keeps hadron's codes (a child exiting 2/4/5/8 looks the same: tell them apart by whether the child ran). A child that cannot start after the read was spent: exit 1, "one read was spent … not delivered"; a value with a NUL byte cannot be an environment variable (read spent, nothing run). `reveal`: value alone plus a newline on a terminal; `--out` writes the exact bytes to a NEW 0600 file opened before the call and prints nothing; `--json` `{secretName,value,readsLeft}` (value = the server's JSON text; same terminal rule; refused with `--out`); `reads left: N|unlimited` on stderr. A JSON string value is passed as its content, any other JSON as its compact text. Reveal refusals: SECRET_READ_CLIENT_SPENT/_PENDING/_NOT_STARTED/_EXPIRED/_REVOKED/_ISSUER_GONE and SECRET_DELETED exit 5, FORBIDDEN (an inject-only kind) 8, SECRET_REVEAL_ONE_PER_OPERATION 2. The value is selected by this one operation and appears in no error, log, receipt or argument; a failed reveal's message is the server's own structured `errors[].message` sentences (an error carrying an `extensions.code`) or a fixed class sentence, never raw response data (a non-200 body, plain text, HTML or malformed JSON, is not echoed); an internal fault keeps its request id. A `--out` write that fails part-way and cannot be cleaned up names the file and says it may hold part or all of the secret (delete it).
# **secret client (cli#2631 slice 1):** a read client is one secret.reveal grant that a PERSON signs. `client issue` is a person's act and NEVER sends a worker session, even from a bound worktree: one issueSecretReadClient, then one requestGrantSignature on the returned grant id (each sent once, never retried), then it prints clientId, secret, selfIssued, grantId, the signing request (id, state, expiresAt) and the `ceremonyUrl` byte for byte (never composed; text refuses a URL with control or format characters, exit 1; `--json` carries it exactly). The grant allows nothing until a person signs on that page. `--json` `{clientId,grantId,secretName,selfIssued,signingRequest:{id,state,expiresAt,endedAt,ceremonyUrl}|null}`; `signingRequest` is null when no signing request was received after the issue: the server refused the step, or its answer was lost (exit 7: the error says a request may exist and to run `team grant request-signature <grant>` to ask again). The issue is still printed, the exit code carries the outcome, and the error names the grant id. With `--wait` and a ceremony URL carrying control or format characters, or when the URL cannot be written to stderr, nothing is polled: the issue is printed (text prints every field except the URL, then refuses it with exit 1; `--json` carries the URL exactly) and the command exits 1 naming the open signing request. An issue answer with an empty client or grant id is refused (exit 1) before any signing request. A signing request answer with no id or no ceremony URL is not a success either: the issue is printed (signingRequest null), nothing is polled, and the command exits 1 with the same recovery as a lost answer. Cancelling the command during `--wait` exits 6, not 13. Holder: you (`USER`, selfIssued) or `--worker <id>`. `--reason` is required; unset optional flags are omitted. `--wait` (off by default) prints the URL to stderr first, then polls the signing request every 2s up to `--wait-timeout` (10m): COMPLETED exits 0; DECLINED/EXPIRED/STALE/WITHDRAWN exit 5 with the record printed first; a timeout exits **13**, withdraws nothing and names `team grant signing-request get`. A lost answer to the issue (7) is never resent and says a pending client may exist: check `secret client list`. Refusals: FORBIDDEN and GRANT_HOLDER_NOT_ALLOWED exit 8; SECRET_AUTO_DELETE_NOT_AVAILABLE and _NEEDS_LIMIT exit 2; SECRET_READ_CLIENT_SUPERSEDED and _LIMIT exit 5; a server without signing exits 2 ("does not offer"). A signer the server refuses leaves the request OPEN, so that wait ends by timeout. `client list` lists the clients the caller issued or holds (`--all` stops with an error after 1000 pages; `--json` an array, `[]` when none, rows `{clientId,clientPrefix,grantId,secretId,secretName,selfIssued,endReason,createdAt}` (`clientId` is null for a manager who is neither the issuer nor the holder: the server then gives only `clientPrefix`, which the table shows with an ellipsis)); acting as you by default, `--as-worker` sends the bound session and lists that worker's clients; an App key, a run or an impersonated session gets an empty list, which is not proof that none exist. **`client revoke <client-id> [--yes]` (cli#2680)** revokes a read client: it pages `mySecretReadClients` for that exact client id (a client you neither issued nor hold can still appear in `list` as a prefix-only row with a null `clientId` when you manage its App, but this exact-id lookup does not match it: exit 4, `no read client ...`, no revoke sent), then sends ONE `revokeGrant` on its `grantId` (never retried), as you (no worker session, even from a bound worktree; no instruction). It cannot be undone, so on a terminal it asks (naming the client and the secret) and off one it needs `--yes` and sends nothing, not even the read, without it (exit 2). `--json` `{clientId,grantId,state,revokedAt,allows}`: the grant as the server returned it. `REVOKED` exits 0; a grant that had already ended (`EXPIRED`, `WITHDRAWN`, `SPENT`, `HOLDER_GONE`) is returned as it is, nothing changes, exit 0 with 'already ended'; success needs the SAME grant id back in an explicit ended state (`REVOKED`, `EXPIRED`, `WITHDRAWN`, `SPENT`, `HOLDER_GONE`): a null answer, another grant's id, a missing or unknown state, a grant still `ACTIVE` or `PENDING`, or one in an ended state whose `allows` is true or missing/null, is 'not confirmed' (exit 7); a client row with no grant id is refused (exit 1) before the prompt, nothing sent; the text says the grant `is REVOKED` (an earlier revoke reads the same: the first end stands), and the DTO never reports the requested grant as ended on the strength of another's answer. Only a pre-write refusal at the mutation's own level keeps its typed exit (`GRANT_NOT_MANAGER`, `FORBIDDEN`, `UNAUTHENTICATED`, `BAD_USER_INPUT`, GraphQL validation/parse); EVERY other failure (a lost answer, a 5xx, an internal error, an error beneath `revokeGrant` such as a result field failing after the write) is exit 7, an unknown outcome: the error names the grant and says to run `secret client list --all`, the server code is kept for `--json`, and the revoke is never resent. `GRANT_NOT_MANAGER` (not allowed, and a grant that does not exist or cannot be seen, one answer) and FORBIDDEN exit 8; UNAUTHENTICATED exits 3. The server decides who may revoke: the issuer (while still in the App's Org) or whoever manages the App. # **secret exec / reveal (cli#2664 slice 2):** a read client is revealed by the WORKER it names, in that worker's bound session; a person reveals on the server's passkey page and gets the server's one uniform answer here (`SECRET_READ_CLIENT_NOT_FOUND`, exit 4, never which cause applied) and no link to that page yet (hadron-cli#2758). Unbound, the call is still sent, without a session and with a stderr note; a binding for another server or App is refused (exit 2, nothing sent). ONE `revealSecret` per process, never retried: a lost answer (7, or 1 with no server code) says the read may be spent and to check `secret client list`. Every local check runs BEFORE the call (a reveal spends a read): bad `--env` (`^[A-Za-z_][A-Za-z0-9_]*$`), no command after `--`, a blank client id, the command not found, `exec --json` (refused, exit 2: the child owns stdout), `reveal` to a non-terminal without `--allow-non-terminal` or `--out`, an existing `--out` path. `exec`: the child gets your environment plus `NAME=<value>`, stdin/stdout/stderr pass through, SIGINT/SIGTERM are forwarded, hadron prints nothing; once the child has STARTED hadron exits with the child's own status (128+n for a signal), every hadron refusal happens before and keeps hadron's codes (a child exiting 2/4/5/8 looks the same: tell them apart by whether the child ran). A child that cannot start after the read was spent: exit 1, "one read was spent … not delivered"; a value with a NUL byte cannot be an environment variable (read spent, nothing run). `reveal`: value alone plus a newline on a terminal; `--out` writes the exact bytes to a NEW 0600 file opened before the call and prints nothing; `--json` `{secretName,value,readsLeft}` (value = the server's JSON text; same terminal rule; refused with `--out`); `reads left: N|unlimited` on stderr. A JSON string value is passed as its content, any other JSON as its compact text. Reveal refusals: SECRET_READ_CLIENT_SPENT/_PENDING/_NOT_STARTED/_EXPIRED/_REVOKED/_ISSUER_GONE and SECRET_DELETED exit 5, FORBIDDEN (an inject-only kind) 8, SECRET_REVEAL_ONE_PER_OPERATION 2. The value is selected by this one operation and appears in no error, log, receipt or argument; a failed reveal's message is the server's own structured `errors[].message` sentences (an error carrying an `extensions.code`) or a fixed class sentence, never raw response data (a non-200 body, plain text, HTML or malformed JSON, is not echoed); an internal fault keeps its request id. A `--out` write that fails part-way and cannot be cleaned up names the file and says it may hold part or all of the secret (delete it).
hadron config get <key> | set <key> <value> (an empty value removes the key; server then returns to its default) | list
hadron api <query-or-mutation>                       # raw GraphQL
hadron version
hadron completion <shell>
hadron agentic-usage                                 # prints this doc
```

Host mixed-wake acknowledgment (cli#1229): `host.yaml` `delivery.ack_mixed`
is a boolean, default false. It must be a real boolean wherever it is written,
including through a YAML merge (`<<`) or an alias of `delivery`: `null`, `~`,
`yes` and `on` are refused at config load (exit 2) naming the line, and a
merged non-boolean is refused even when an explicit `ack_mixed: true` beside it
would win. The strictness is deliberate (cli#1263); relaxing it needs its own gate. Set true only after the operator verifies server
A′ (issue #2072, merged as hadron-server#2102 on 2026-10-19; the date is for orientation, since a build from that day can predate the merge: confirm the deployed build includes that change) is deployed. It permits tick/task acknowledgment in a mixed
wake without moving channel delivered-through or worker read marks (R53);
mention text still needs its own batch. Off retains `ack-held`; mention-only
wakes are never acknowledged here. Previously typed wakes reconcile with a
fresh proof on the next pass, not a proof older than 15 minutes. Host status
adds `delivery.ackMixed` JSON and `ack-mixed on|off` text, configuration only,
not capability detection. A live-host flip remains the host operator's step.

Conventions:

- A new URN slug/path or identity you supply is validated client-side before the
  call, with the same `urn-lib` rule the server enforces — a bad value fails fast
  with a usage error (exit 2) instead of a round-trip:
  - **node locs** (`node add --loc`) — colon-joined atoms, each 1–64 chars of
    `[A-Za-z0-9._-]` starting/ending alphanumeric (so `--loc "Flow Lab"` is
    rejected; `@` is rejected).
  - **slug flags** (`memory set --slug`, `app install --urn`) — the atom rule
    plus **lowercase-only** and reserved-word rejection.
  - **org roots** (`org create/update --urn`) — a **bare, lowercase slug** (no
    scheme prefix or colon), dotted or not: `acme.com` and `acme` are both
    accepted. `Acme.com` (uppercase), `hrn:org:acme.com` (prefixed), the
    reserved words `hrn` and `urn`, and an ID-shaped name (32 hex characters, or
    a 25-character cuid such as `c` plus 24 lowercase letters/digits) are all
    rejected, the last with `root-id-shaped`.
  - **user handle** (`profile set --handle`) — the same root rule, and it must
    also be **dot-free** (so `has.dot` is rejected). An empty `--handle` defers
    to the server.
  - `agent update --urn` also accepts the spec-047 user-author form
    `@handle:slug`.
- **URN grammar: the CLI emits flat grammar-v2, and accepts every spelling.**
  What it prints back — and what the server hands out — is
  `hrn:mem:<root>:<slug>` for a memory and `hrn:node:<root>:<slug>:<loc…>` for a
  node (e.g. `hrn:node:hadronmemory.com:dev:start-here`). Prefer those when
  composing a reference; every legacy form below stays accepted indefinitely, so
  a URN you captured earlier keeps working.
- Memory references accept the memory id, the canonical `hrn:mem:<root>:<slug>`
  URN, the legacy `hrn:memory:<org>::<slug>`, or the short `<org>::<slug>` /
  `<org>:<slug>` forms (all resolve to the same memory) across
  `memory get|set|attach|rm|member|share|export`.
- App references — the persistent `--app`, every local `--app`,
  `--install-into`, and the `app` group's positionals — accept the App id, the
  canonical `hrn:app:<root>:<slug>`, or the short `<root>:<slug>` form (the
  legacy `<root>::<slug>` separator stays accepted). The ref is shape-checked
  client-side and sent in canonical form: a bare slug (`dev-team`) or anything
  else that cannot name an App is refused (exit 2) before any round trip, in a
  message that names the flag — the server is never asked to reject it.
- Node references are fully-qualified URNs. Either the canonical flat v2 form
  `hrn:node:<root>:<slug>:<loc…>`, or the scheme-less
  `<org>::<memory>::<loc>` (double-colon between segments), e.g. the legacy
  `hadronmemory.com::dev::start-here` — still accepted, and it may also carry
  the `hrn:node:` prefix (legacy `urn:node:` too). The prefix is REQUIRED for
  the flat v2 form. A scheme-less single-colon `<org>:<memory>:<loc>` is invalid
  — **not** a full URN, because a loc itself contains single colons
  (`services:secureid:user-reporting`), so it's ambiguous; the `hrn:node:` prefix
  is what removes that ambiguity for the flat v2 form. A bare loc is rejected
  (exit 2) *unless* you pass `-m/--memory hrn:mem:<root>:<slug>` (the short
  `<org>:<slug>` / `<org>::<slug>` forms also accepted) to name the memory — then
  `node get|update|move|clone|rm|export` and `edge add|list` take a bare `<loc>`
  (e.g. `node get start-here -m hrn:mem:hadronmemory.com:dev`; `edge add -m
  hrn:mem:a:m --from x --to y …` applies the memory to both endpoints). For `node move|clone`, `-m`
  scopes only the source `<loc>`; the destination is always the explicit
  `--to-urn`/`--to-memory`.
  Without `-m`, the URN must name the memory — the same loc can exist in
  several memories.
- Edges are directed, first-class entities (spec 037): each carries an
  optional `name` (the relationship — was `label`) and a `loc` that is its
  identity (`hrn:edge:<root>:<memory>:<loc>`). `edge add --from <node> --to
  <node> --name <rel>` creates one (plus optional `--loc`/`--description`/
  `--runnable`); `edge update`/`edge rm` address it by its edge ID (shown by
  `edge list` and in `node get --json`). A nameless edge prints its loc instead.
  Cross-memory edges are allowed. `edge rm` is permanent and removes only the
  link, never a node; `edge update --name` keeps the loc, and `--condition null`
  / `--data null` clear those fields.
- Destructive / bulk-write commands (`memory rm`, `node rm`, `node merge`,
  `user merge`, `user set-roles`, `edge rm`, `app delete`/`app rm`, `app uninstall`, a real `replace` /
  `spec replace`, `memory link-user`, `memory config rule rm`, `memory config template rm`,
  and `memory encrypt`) prompt on a terminal and REQUIRE `--yes` when run
  non-interactively (agents must always pass `--yes`, or `--dry-run` to preview a
  `replace`). Without it they exit 2.
- `memory link-user <memoryRef> --external-user <id>` is the anonymous →
  registered promotion an App runs when an end user signs up. **App key only** —
  the server gates on the calling App, so a user PAT is refused; the memory must
  be a user/anonymous memory in that App's org. `--external-user` is the App's
  OWN identifier for the end user, not a Hadron user id; the server resolves it
  to the app-scoped user or provisions one (unless the App sets
  `createUserPermission=DENY`). Two effects to plan for: the memory's URN is
  **re-minted** (the anonymous one stops resolving — `--json` returns
  `{memory, previousUrn, encrypted}` so both are visible), and its anonymous TTL
  is cleared so it is no longer garbage-collected. Adding `--data-key-file <file|->`
  (mode 600; `-` reads stdin; also `--data-key -`; a literal `--data-key <key>` is
  DEPRECATED: one stderr warning that never echoes the key, refused in a later
  release, cli#1657) encrypts every node in the same transaction and lands the memory `private` and
  owned by that user — ONE-WAY, like `memory encrypt`. The key must be canonical
  base64 for exactly 32 bytes (43 characters plus `=`) and is sent once in the
  `x-hadron-operation-key` request header, never in the request body or its
  variables; a malformed or empty value is refused locally (exit 2) before any
  request, without echoing the value. The key channel needs a server with the
  operation-key header (hadron-server#1817, server main `1cedfa2e` on); on an
  older server a supplied key makes the command fail loudly (exit 1) with the
  memory's id and new URN rather than complete a plaintext promotion silently.
  Gated: pass `--yes`
  non-interactively.
- `hadron app get <app-ref>` reads only id, urn, name, appType and nullable
  description. No settings, nested keys or AI configuration is requested
  (the App's properties are `hadron app properties get`).
  `hadron app key list <app-ref>` uses top-level appKeysPage and
  prints only id, keyPreview, label, createdAt, lastUsedAt and revokedAt.
  It lists active keys, not revoked-key history; the server enforces its
  App-manager gate. No raw key or host-use records are requested. JSON is an
  explicit metadata object for get and an array for key list ([] when empty).
  Nullable description/label/timestamps stay null, and text uses - without
  claiming the value was withheld. Both require an explicit App ID or URN
  (existing short/legacy forms accepted); neither falls back to --app.
  `app key list` text cells (#1065) replace control and invisible characters
  (C0/C1, DEL, ESC, newline, tab, bidi/zero-width/line-separator marks) with a
  space and cut a cell at 256 characters ending in "…"; `--json` keeps the
  original values. The inventoried node, edge, spec, search (table view), and
  replace result tables apply this rendering to server-derived string cells
  (#1103), including formatted and joined cells. This is not a guarantee for
  every text mode: static counts and labels, local citation-scan fields, JSON,
  document/block output, and non-table diagnostics (including renumber moves
  and mint stale-abstract/open-question lines) keep their existing behavior.
  The remaining command groups' table cells are tracked in #1093.

- `hadron asset` is the file surface (spec 006 / cor:dmo:060:10). An asset is
  bytes held by a memory, as opposed to a node's text; listing and download are
  gated on the holding memory's READ access, so you see every uploader's assets,
  not just your own (`--mine` narrows to yours). Assets are addressed by id OR by
  URN `hrn:asset:<root>:<memory>:assets:<id>` — the URN carries its memory, which
  is why `asset url` accepts a URN alone but needs `-m` with a bare id.
  `asset list` REQUIRES `-m` unless `--all`: `-m` lists one memory's assets;
  `--all` lists every asset held by a memory you can read through the server's
  own `assets` query (not a client fan-out across memories), narrowed by
  repeatable `-m`, `--org` or `--scan-status` (cli#1341). It pages to
  exhaustion by default; `--limit` fetches one explicit page. `asset get <ref>` mints a
  short-TTL presigned URL and streams the bytes to the asset's own filename,
  `-o <path>`, or `-o -` for stdout; it refuses to clobber an existing file
  without `--force` (the default name comes from server metadata, not from you)
  and deletes a partial file on failure. Downloads are gated on virus scanning
  (server #896): a PENDING asset is refused because the verdict has not settled
  — that is "not yet", NOT a dead end, since the server's sweep retries on a
  backoff, so try again shortly — while a BLOCKED asset is refused permanently,
  its bytes deleted and the row kept as an audit tombstone. `asset list` shows
  the scan status, and for a BLOCKED row the engine signature that matched
  (`scanSignature` in `--json`, null on every other row). An upload whose bytes
  fail the scan fails on the LAST step, after the transfer, with a BLOCKED
  record left behind on purpose; re-uploading the same file always fails the
  same way. All three exit 1.
  `asset public-link <ref> --on|--off [--instruction <kind>:<ref>]` records the anonymous-download switch.
  The optional instruction uses the shared `team-chat-message:`, `task:` or
  `live-chat:` syntax (the reference after the first colon is preserved). It
  applies to a worker session on an App memory; the server decides admission.
  Malformed instructions exit 2 before confirmation or any call. Without the
  flag the person call and JSON are unchanged. Requires server 2026-11-03 or later.
  ON confirms “anyone with the link” (`--yes` for non-interactive callers);
  OFF needs no confirmation. The server decides management authority (today:
  admin on the asset's memory; a refusal exits 8); no local role calculation or
  retry. After OFF, or after `asset rm`, the hotlink 404s; ON again or
  `asset restore` brings back the same URL. The result and `asset list --json` expose
  `publicLinkEnabled` (stored opt-in), `canManagePublicLink` (server capability),
  `publicLinkAvailable` (deployment gate), and nullable `publicUrl` independently.
  An ON flag does not prove anonymous serving: deployment gates and asset
  eligibility still apply. Null URL never proves OFF. `asset url <ref>` prints
  the server's **UNAUTHENTICATED** hotlink, or exit 5 with the stored-off,
  deployment-disabled or no-URL state and scan/eligibility guidance. Its JSON
  adds the same three booleans without changing its existing keys. Never
  construct a public URL from the asset ID yourself.
  `asset upload <file> -m <memory>` is three steps behind one command: the
  server reserves the asset and returns a presigned PUT, the bytes go straight
  to object storage, and a final call marks it usable. Size and MIME are
  declared UP FRONT, so the size cap and MIME allowlist reject a bad upload
  before any bytes move — one round-trip, not a wasted transfer (exit 1). The
  platform-wide limits: 25 MB (26,214,400 bytes); PDF, PNG, JPEG, WebP,
  text/plain, text/markdown, text/csv, application/json, .docx, .xlsx, .pptx.
  Success prints `uploaded <name> (<size>, <mime>)` then the asset URN. The MIME type
  comes from the extension, else content sniffing; `--mime` overrides when the
  extension lies. A failure of the PUT leaves the asset reserved but not
  completed: it does not appear in `asset list` and can be re-uploaded. A
  failure of the FINAL call is ambiguous — the server may have committed the
  asset before the reply was lost — so check `asset list` before retrying;
  there is no idempotency key, and a blind retry can create a duplicate.
  Fresh uploads are `scanStatus: PENDING` and NOT downloadable until CLEAN.
  `asset rm` is a SOFT delete — restorable via `asset restore` within the
  24-hour retention window (then purged; restore keeps the public-link setting), and visible meanwhile with `asset list --include-deleted` —
  so it prompts on a TTY and needs `--yes` non-interactively, but says
  "restorable" rather than "permanent". `asset link <ref> --node <new-node-urn>`
  creates a `nodeType: reference` node carrying the asset in `data.asset`, the
  same shape `hadron_store_file` writes. **`--node` is the URN of the node to
  CREATE, not a parent to append under** — `hrn:node:<root>:<memory>:<loc>`,
  where the loc's colon prefix places it in the tree (`:designs:logo-v3` lands
  under `:designs`); naming an existing live node is refused with a loc
  conflict. The asset and the node need not share a memory (READ on the
  asset's, WRITE on the target's).
  The pointer is a SOFT reference — deleting the asset leaves the node with
  `asset` resolving to null, which is usually the right audit trail.
- `memory validate <memoryRef>` runs the server's memory health audit
  (`validateMemory`) and reports its findings: `broken-ref` (an edge pointing at
  a missing/soft-deleted node), `embed-failed` (a node absent from the vector
  index — from the outside indistinguishable from "no hits matched"), `sparse`
  (no description, content, or abstract), `schema` (objectType/properties
  violating the memory's declared schema), and `stale-abstract`. Read
  `stale-abstract` NARROWLY: it compares the abstract's origin hash against the
  current content hash, so it fires when the body is not the version the abstract
  was written against, including when the difference changed nothing the abstract
  says — it means "the body moved under this abstract", not "this abstract is
  wrong" (measured on the
  live specs corpus it carries essentially no signal about whether the abstract
  still describes the body; hadron-cli#352). Two counts, deliberately:
  `totalFindings` is the true PROBLEM count across every check BEFORE truncation,
  while `matchedFindings` counts the problems listed after `--limit` and
  `--check`. Informational `INDEX_TRUNCATED` declarations (the memory declares a
  chunk cap) are not problems: `totalInformational` / `matchedInformational`
  count them apart (cli#1501), they stay in `findings` (`kind: index-truncated`)
  and never affect `ok`. Gate on `ok`, never on `findings.length`. `ok` is false whenever a check was
  SKIPPED even with zero findings (health can't be claimed for a check that
  didn't run); `skippedChecks` says which and why (stale-abstract is skipped on
  an encrypted memory — the validator doesn't decrypt). `--check <kind>` filters
  CLIENT-side, and the server caps `findings` before that filter runs, so
  `--check` requests the server maximum (1000) unless you pin `--limit`; if the
  result is still truncated the report says the filtered view may be missing
  matches. Exits 0 whatever the findings unless `--fail-on-findings` (then exit
  5 when any LISTED problem finding matches; informational rows never do), because a check that fires on most of a
  never-audited corpus on day one just teaches people to ignore the report.
- `memory reembed-failed <memoryRef>` re-drives failed embeddings
  (`retryFailedEmbeddings`): the server re-stamps each live node whose embedding
  failed so the worker tries it again. `--json` is `{memory, memoryId,
  restamped}` — `restamped` counts nodes RE-STAMPED, NEVER nodes now indexed
  (a deterministic provider failure fails again; 0 is legitimate and also means
  a memory that is not vector-indexed). Confirm with `memory validate
  <memoryRef> --check embed-failed`. A mutation kept apart from the read-only
  `validate`; running it is the request, so no prompt. Sent once, never
  retried: a transport error leaves the outcome unknown — re-run it yourself.
  A response with a missing/null count (`data:null`, absent field) is the same
  unknown outcome: exit 7, NO success output, never a fabricated `restamped: 0`
  (cli#1008; only a returned integer, including 0, is success).
  Server gate (memory owner / org ADMIN) and its encrypted-memory limits apply.
- `memory encrypt <memory> --data-key-file <file|->` converts a plaintext memory to
  encrypted-at-rest: you provide the data key in a file only you can read (a file
  that group or others can access is refused before any request, exit 2, the
  `readPassphraseFile` rule; `-` reads stdin, as `--data-key -` still does, with
  the confirmation answered before stdin is read; `--data-key-file` is exclusive
  with `--data-key` and `--passphrase-file`; a literal `--data-key <key>` still
  works but is DEPRECATED, printing one stderr warning that never echoes the key,
  `--json` stdout unchanged, and will be refused in a later release: cli#1657,
  ruling 0148) so it stays out of `ps` and shell history and the server rewrites all node content as
  ciphertext in one transaction. It is ONE-WAY — there is no decrypt command —
  so keep the key. Afterwards a read prints the stored ciphertext (`enc:v1:…`)
  unless the invocation supplies the passphrase with the global `--unlock`
  (see "Reading an encrypted memory" below); v1 unlocks passphrase-encrypted
  memories only, not `--data-key` ones.
- `memory config get <memoryRef>` shows a memory's CONFIG: its node-role rules
  (hadron-server#1325), one per role or dotted sub-role, role-ascending.
  `memory config rule add|update|rm <memoryRef> <role>` changes them. **Only the
  memory's managers** can read or change a config (the owner of a personal or
  private memory, otherwise its user owner or an org ADMIN/OWNER); anyone else
  gets exit 4, EXACTLY as for a memory that does not exist. So exit 4 never
  means "no rules": a manager's memory with no rules is a success, `id: null`
  and `rules: []`.
  - **Skipped org defaults (#1716, server#3426):** `--json` carries
    `defaultSkips: []` (never `null`): each `{role, field, reason, ref,
    refState, createdAt, sourceTemplate}` is an org-default rule left out when the
    memory was created (a reference deleted, unreadable to the organization or not
    a task). `ref` and `refState` are as YOU see the reference now (`ref` null,
    `refState` BROKEN once the template or rule is gone; the CLI never builds a
    URN). Text prints a `Skipped org defaults` table only when there are some.
    Managers only, like the config: a non-manager gets exit 4 with no skip output.
  - `--json` for `get` is `{id, memoryId, rules[], createdAt, createdBy,
    updatedAt, updatedBy}`; each rule is `{id, role, revision, enabled,
    strictSubRoles, writers, validateBy, authorTask, authorTaskId,
    authorTaskState, validationTask, validationTaskId, validationTaskState,
    descriptionNode, descriptionNodeId, descriptionNodeState, locked, sourceTemplateId, sourceTemplate{id,name,deleted}, createdAt,
    createdBy, updatedAt, updatedBy}`. `add`/`update` print `{rule, warnings[]}`;
    `rm` prints `{memoryId, role, id, revision, deleted}`.
  - **A reference is a URN, an id and a STATE**: `OK` (the node is live and
    readable), `NONE` (not configured), `BROKEN` (the node was deleted; a
    manager must repoint it), `UNREADABLE` (it exists, you may not read it).
    The id and URN are `null` unless the state is `OK`, and even an `OK`
    reference can have a `null` URN (its memory's legacy URN cannot address
    it, hadron-server#697) — then the `*Id` is the ref to pass back. Branch on
    the STATE, never on the URN's presence: BROKEN and UNREADABLE are
    different facts with different remedies, and a null URN on OK is neither.
  - **Only the flags you give change.** An omitted flag leaves the field alone;
    on `update`, an EMPTY `--author-task` / `--validation-task` /
    `--description-node` / `--validate-by` CLEARS it (on `add` an empty value is
    refused, exit 2: there is nothing to clear). `--strict-sub-roles=false` and
    `--enabled=false` turn those off. `update` with no flag at all is exit 2.
  - **`update` and `rm` read the rule's revision and send it with the write**, so
    a rule someone else changed in between exits 5 (`CONFLICT`) instead of being
    overwritten or removed unseen — re-run to act on the current rule. Clearing
    `--validate-by` is part of the SAME single update as any other flag, never
    a second save. The role is the rule's identity: to rename one, `rm` and
    `add`.
  - Refs are node ids (32-hex or CUID) or fully-qualified URNs; the SERVER
    resolves them, and a node you cannot read is refused exactly like a missing
    one (exit 4). A bare loc is refused locally (exit 2): these flags take no
    `-m`. A readable node that is not a runnable task is exit 2 (`NOT_A_TASK`,
    `extensions.field` names which reference).
  - **The role on `add`** must fit the grammar (lower-case letter/digit/dash
    segments joined by dots, 1–64 characters), else exit 2
    (`INVALID_NODE_ROLE`, `extensions.reason` is `EMPTY | TOO_LONG |
    BAD_SEGMENT`); nothing is trimmed or lower-cased for you. An existing role
    on `add` is exit 5 (`NODE_ROLE_RULE_EXISTS`: use `update`). **The role on
    `update`/`rm`** is matched EXACTLY against the rules that exist, so a role
    with no rule — including a malformed one, which cannot have a rule — is
    exit 4, and the message lists the roles that do. `--validate-by` without a
    validation task is exit 2 (`BAD_USER_INPUT`, `reason:
    VALIDATION_TASK_REQUIRED`).
  - `warnings[]` are non-fatal findings about a saved rule and never change the
    exit code (on the terminal they go to stderr). Today the server returns
    none; visibility warnings arrive with hadron-server#1327.
  - `locked` and `sourceTemplate` are set by `template apply` (below): a rule
    a REQUIRED template wrote is locked, and a locked rule refuses EVERY
    caller's `rule update`/`rule rm` — exit 5 (`RULE_LOCKED`,
    hadron-server#1334), the message naming the remedy. `rule unlock
    <memoryRef> <role> [--yes]` is the deliberate step: it needs LOCK
    AUTHORITY (org ADMIN/OWNER, or the manager of a personal/private/org-less
    memory) — exit 8 without it — prompts on a terminal (`--yes` otherwise),
    sends the rule's revision (exit 5 if it changed), and returns the rule
    (`{rule,warnings}`, `locked: false`). Unlocking an unlocked rule changes
    nothing (a stderr note, no prompt).
- `memory config template list|get|create|update|rm|apply` manages TEMPLATES
  (hadron-server#1325 part c): a reusable rulebook with exactly ONE owner — the
  server (`--owner-server`, platform admins), an organization (`--owner-org`),
  you (`--owner-me`) or an App (`--owner-app`) — that is COPIED into a memory's
  config when applied, never live-linked. Only the owner's managers can see or
  change one; for anyone else it does not exist (exit 4, and `list` simply
  omits it). Templates are addressed by id.
  - `list` pages to exhaustion (`{items[], total}`, name-ascending); at most one
    owner flag narrows it. `get`, `create` and `update` print a template
    `{id, name, description, ownerType, ownerId, required, orgDefault, revision,
    rules[], createdAt, createdBy, updatedAt, updatedBy}` (`create`/`update` wrap it as
    `{template, warnings[]}`); each rule is a config rule without the
    config-only fields (no id, revision, locked or provenance), with the same
    URN/id/STATE references. `rm` prints `{id, name, revision, deleted}`.
  - **`orgDefault` (#1716, server#3426):** a boolean in the template file, like
    `required`. `true` designates an ORGANIZATION-owned template as an org default:
    applied once, as the org, to every new org knowledge or group memory at
    creation (`createMemory` only; never clone, extract, App, install, agent or
    chat creation), never retroactively. It is a starting configuration, NOT a
    boundary. A key the file has is applied, an omitted key is unchanged, and
    `"orgDefault": false` clears it (never dropped as zero); `null` is refused
    locally. The server refuses `true` on a user-, App- or server-owned template
    (`BAD_USER_INPUT`, reason `ORG_DEFAULT_ORG_ONLY`) and an eleventh live default
    in an org (`ORG_DEFAULT_LIMIT`, cap 10), both exit 2 with the server's message
    verbatim; `NODE_NOT_FOUND`, `NODE_ROLE_RULE_REF_BROKEN` and `CONFLICT` come
    from the save-time reference gate. A broken REQUIRED default is planted locked
    and refuses that role's writes until an admin repoints it. No dedicated
    set/clear verb: edit the file key.
  - **Bulk apply (#1701):** two or more refs, or --memories-file (one per
    line, - reads stdin once; trim lines, skip blanks and # comments), uses
    one bulk preview then one real call pinned to its successful entries'
    templateRevision. Arguments precede file refs, duplicates stay; the server
    owns the cap, with no client cap/chunking/retry. One ref without a file
    retains the single-memory operation and output. Different successful
    preview revisions refuse with exit 5; no successful preview means no real
    call. Replacement or ANY failed preview entry asks once, naming each memory and
    its replaced roles plus the failed refs that may now succeed/replace rules
    not previewed (--yes non-interactively). With --memories-file -, stdin
    cannot also confirm: use --yes when asking would be needed. --dry-run stops
    after preview.
    Bulk JSON is {dryRun,succeeded,failed,results:[{index,memoryRef,ok,result|error}]};
    result is the single-apply report, error is {code,message,currentRevision?,requestId?}.
    JSON echoes the sent canonical ref; text shows the typed ref by index.
    Any failed entry prints the full report then exits 1, even CONFLICT;
    all ok (including skips) exits 0. Whole-call refusals: UNAUTHENTICATED (3),
    FORBIDDEN (8), BAD_USER_INPUT/BULK_TARGETS_TOO_MANY (2) (server names cap/count),
    MEMORY_CONFIG_TEMPLATE_NOT_FOUND (4), CONFLICT (5). A lost or malformed real
    apply answer can follow partial writes: some memories may already have
    changed; verify memory configs before retrying. Missing and unmanaged targets
    both report MEMORY_NOT_FOUND. Retry failed targets only after checking
    errors; do not retry DUPLICATE_TARGET
    positions (the memory was already attempted earlier). Resending the
    whole --memories-file is not idempotent and may replace successful rules
    again. Requires server#3504 (573f3cf4); no fallback to repeated single calls.
  - **`--file` is `get --json`'s shape**, so a template round-trips: read it,
    edit it, send it back, or feed it to `create` to copy it. The server-owned
    keys it carries (`id`, `ownerType`, `ownerId`, `revision` on create,
    `created*`/`updated*`) are accepted and never sent; any OTHER unknown key is
    exit 2, so a typo cannot silently do nothing. A reference is taken from its
    URN, else its id. Reference STATES are checked strictly, because the state
    is never sent and a state with nothing behind it would silently drop the
    reference: a `*State` must be `NONE`, `OK`, `BROKEN` or `UNREADABLE`
    (exact: case, no padding, never `""`), an `OK` needs its URN or id, and a `BROKEN`/`UNREADABLE` one —
    the server withheld which node it is — needs a NEW reference set beside it
    (or its state key removed, to drop it on purpose). Each is exit 2
    otherwise. The file must hold exactly one JSON object: trailing content is
    exit 2. An explicit `null` is exit 2 on any key `get --json` never prints
    as null (`name`, `required`, `rules`, `revision`; per rule `role`,
    `enabled`, `strictSubRoles`, `writers`, every `*State`), because a null
    would be read as ABSENT: unchanged at the top, and inside a rule, which is
    replaced whole, the server's default. Give the value, or remove the key.
  - **`update` applies only the keys the file HAS**: an omitted key is
    unchanged, `"description": null` clears it, and `"rules"` REPLACES every
    rule (`"rules": []` removes them all). It is guarded by the revision the
    FILE was read at — its `"revision"`, or `--expected-revision` — never by a
    fresh read, so a template changed since you read it exits 5 (`CONFLICT`)
    instead of being overwritten by your stale copy. No revision at all, a flag
    that disagrees with the file, or a file whose `id` names ANOTHER template
    are exit 2.
  - `create` needs exactly one owner flag (exit 2 otherwise). A name taken by
    a live template of the same owner is exit 5 (`MEMORY_CONFIG_TEMPLATE_EXISTS`);
    a role listed twice is exit 5 (`NODE_ROLE_RULE_EXISTS`); a bad name is exit
    2. A server-owned template on a deployment with no server row is exit 1
    (`HADRON_SERVER_NOT_CONFIGURED`: an operator's fix). Naming an owner you
    do not manage is refused by the server.
  - `rm` deletes softly — configs it was applied to keep their copies — but no
    command restores a template; it prompts, needs `--yes` non-interactively,
    and sends the revision it read (exit 5 if the template changed meanwhile).
  - **`apply <templateId> <memoryRef>`** copies a template's rules into ONE
    memory's config (hadron-server#1334; cli#716 slice 3). It is a one-off
    copy: later template edits never reach the memory. You must manage both
    (exit 4 otherwise). `--json` is `{memoryId, template{id,name,deleted},
    templateRevision, required, dryRun, entries[], warnings[]}`, and each entry
    is `{role, outcome, rule}`:
    - `outcome` is `APPLIED` (no rule for the role; copied in),
      `SKIPPED_CONFLICT` (a non-required template never overrides),
      `REPLACED` (a required template overwrote the rule, now locked) or
      `SKIPPED_LOCKED` (a locked rule you cannot move stands);
    - `rule` is the rule in force afterwards (the `config get` rule shape), or
      on a dry run the existing one, `null` for a would-be `APPLIED`.
    It **previews first** (a server-side dry run). When the preview would
    `REPLACE` a rule it prompts, or needs `--yes` non-interactively (exit 2
    without). It then applies exactly the previewed `templateRevision`, so a
    template changed in between exits 5 (`CONFLICT`) instead of being applied
    unseen. That guards the template; the memory's own rules are re-read by the
    apply, and the reported result is authoritative. `--dry-run` writes nothing.
    `--expected-revision <n>` pins the revision yourself (e.g. one approved from
    a dry run); a non-positive value is exit 2. A template rule whose task was
    deleted refuses the whole apply, exit 5 (`NODE_ROLE_RULE_REF_BROKEN`: fix
    the template). Skipped entries are part of the report, not failures (exit
    0).
- `memory set` creates when called without a positional argument
  and updates when given one. Free-standing create requires `--org` and
  `--name`. `--owner-me --name <name>` instead creates a user-owned memory with
  no org — owned by you in your own `@handle` namespace, so its URN roots on
  your handle (`hrn:mem:<handle>:<slug>`). That path is owner-only: `--class
  personal|private` (defaults to personal); knowledge/group still need `--org`.
  The server derives the handle and bare URN — never construct it client-side.
  `--owner-me` with `--slug` is refused (exit 2, cli#1240) before anything is
  created: the slug is derived from `--name` and the server cannot rename a
  user-owned memory.
  App-scoped create requires `--app <ref> --agent <ref> --class
  app|personal|private --name <name>`; both refs accept an ID or URN, and the
  Agent must be installed in the App. Only fields passed as flags change. The
  free-standing URN slug is kebab-derived from
  `--name` on create (`"Project KB"` → `project-kb`) unless you pass
  `--slug <bare-slug>` to set it explicitly; on update, `--slug` renames
  the memory (its URN — and every node URN under it — changes). Because
  `createMemory` has no slug input, `--slug` on create is a create plus a
  rename: if the rename fails the memory still exists under its derived
  slug and the command exits non-zero (a partial write). App-scoped create
  rejects `--slug`: App-class URNs are name-derived by the server, while
  personal/private URNs use a per-owner opaque id.
- `memory set --app <ref> --class app --name <name>` with NO `--agent` adds a
  new free-standing app memory to an App that has an installed Agent
  (`createAppMemory`, cli#1176): only an admin/owner of the App's org (or, for
  a user-owned App, its owner), as themselves (not an App key, a run,
  impersonation or a worker session). It takes only `--name`,
  `--short`, `--description` and `--tag`; `--max-rev-count`, `--visibility`,
  `--slug`, `--org`, `--owner-me`, `--draft-corpus`, `--schema`/`--schema-file`
  and `--listed-on-marketplace` are refused (exit 2, nothing sent), as are a
  missing/non-`app` `--class`, a missing `--name` and a positional memory.
  `--agent` without `--app` is still exit 2. Server refusals keep their
  server sentence and mapped exit code (FORBIDDEN 8; BAD_USER_INPUT and
  AGENT_NOT_INSTALLED 2).
- `memory attach <memory> --app <ref> --agent <ref>` binds an existing
  free-standing personal/private memory to that App/installed Agent. The
  memory must be caller-owned and keeps its URN, class, and owner; server typed
  errors report already-scoped, cross-org, membership, and install failures.
- Successful node writes can carry server advice (including `include.pin.unkept`
  for a readable plain revision pin with no recorded approval or signature).
  The writes that display it are exactly those that send one of these
  mutations: `createNode`, `createSpecNode`, `createTaskNode`, `createReviewNode`,
  `createAssetReferenceNode`, `updateNode`, `updateSpecNode`, `updateTaskNode`, `updateReviewNode`,
  `importNode`, `mergeNodes`, `cloneNode`, `moveNode`, `updateNodeData`,
  `restoreNode`, `restoreNodeRevision`, `searchReplaceInNodes` and
  `searchReplaceInSpecNodes` (generic and kind add/update, import, merge, clone,
  move, data writes, restore, revision restore, text replacement and `asset link`).
  `asset link` embeds the file name in the reference node, so a name containing
  include syntax draws advice and an ordinary one none. `updateNodeRevision` (a
  label edit) and every other write are outside the set. The writes in the set
  display it as `warning: [<code>] <text>` on stderr in BOTH text and `--json` modes;
  stdout/JSON and the successful exit code stay unchanged. Unknown codes are
  displayed too; malformed entries and warnings for other response keys are
  ignored. Failed commands print no response advice. This is advisory, never a
  reason to retry a successful write or a promise that an include is retained.
- `node add` / `node update --seq <n>` (cli#479) set an ordinary node's position among
  its siblings (lower first). It is sent only when you pass it: `--seq 0` is a real
  position and leaving the flag off preserves the stored one. There is no way to clear
  a seq from the CLI. The CLI checks nothing about the node: the server refuses the
  write on `chat-message`/`record` nodes (`SEQ_PROTECTED`) and on nodes under a Channel
  (`LOC_PROTECTED`); other node kinds follow their own write rules (a runnable task
  takes a seq through its own door), and a refusal is printed as is. Both refusals exit 2 (usage). Reordering a
  legacy chat is not offered here.
- `node add` fails if the loc already exists; `node update` modifies
  an existing node and preserves unset fields. Content comes from
  `--content "<text>"`, `--content -` (stdin), or `--content-file` (`-`
  reads stdin, like `--content -`). **Two write guards on a new body
  (#873, `node update` and `spec edit`):** a body that starts with the header
  a text-mode `node get` / `spec get` prints is REFUSED (exit 2, nothing
  written; the message names `--body-only`) unless `--force`; a body that
  drops a `{{…}}` tag the stored body has (a placeholder, or a `{{=…=}}`
  delimiter change) is written with a `warning:` on stderr, and `spec edit
  --json` lists it under `warnings`. `node update --dry-run` writes nothing:
  it reads the node, runs every client-side check a write would, and prints the fields
  that would change with a unified diff of the stored body against the new
  one (`--json`: `{dryRun, id, urn, loc, fields[], content: {before, after,
  diff} | null, warnings[]}`); `--dry-run --data-merge` is refused (exit 2).
  The dry run covers the client-side checks only; the server's validation
  (a schema-governed memory, the door's refusals) runs on the write.
  **`--content -` is for a PIPE and is REFUSED (exit 2) when stdin is an
  interactive terminal** (#643): a terminal's line discipline can truncate or
  reorder large input BEFORE the CLI reads it, so the write would succeed and
  store a corrupted node — observed on a ~10 KB document with sections missing
  and others joined. If you drive this CLI through a PTY, use `--content-file
  <path>`; `cat file | hadron ...` is a pipe, not a terminal, and still works.
  The same refusal covers `node update --abstract -` / `--data-merge -` (use
  the `-file` form) and `node import -` (pass the path) (#648).
  **A non-empty pipe that no flag reads is REFUSED too (exit 2, #1450).**
  `cat body.md | hadron node create …` with no `--content -` used to exit 0
  and store an empty body; now `node create` (neither `--content -` nor
  `--content-file -`, so also an inline `--content "<text>"` or a
  `--content-file <path>` beside a live pipe) and `node update` (none of `--content -`,
  `--content-file -`, `--abstract -`, `--data-merge -`) refuse before any
  request, naming the flag that reads a pipe. An empty pipe, `</dev/null`, no
  pipe at all are unchanged; `--allow-empty` does not bypass it (it covers a deliberately empty body, not
  dropped bytes). The check peeks one byte for at most 250 ms, so a pipe whose
  writer stays open and silent never hangs the command.
  The refusal is on DOCUMENT reads only — `--data-key -` and other secret
  reads from a terminal are deliberate and unaffected;
  the abstract likewise from `--abstract`, `--abstract -`, or
  `--abstract-file` (paragraph abstracts dodge shell quoting this way).
  Machine-readable JSON `data` comes from `--data '<json>'` or
  `--data-file <path>` (validated client-side); it **replaces** the
  node's whole data object, so omit it to preserve and pass `--data null`
  to clear. To **merge** instead — overwrite a few top-level keys while
  preserving the rest (a shallow merge; nested object values are replaced
  wholesale) — pass `--data-merge '<json>'` (`-` reads stdin) or
  `--data-merge-file <path>`; the patch must be an object, and merge is
  mutually exclusive with the `--data` replace. `node get`/`spec get` show
  `data` in the text view (and the `data` field in `--json`).
- `node move` relocates a node and `node clone` copies it. Both name the source
  by URN (or a bare `<loc>` with `-m`) and take **exactly one** destination:
  `--to-urn hrn:node:<root>:<slug>:<loc>` (a full destination URN — new memory
  and/or loc) or `--to-memory hrn:mem:<root>:<slug>` (keep the loc, change the memory). `move`
  relocates the node **together with its whole subtree** (descendants come
  along) and keeps each node's id so edges stay valid; `clone` returns a **new**
  node (fresh id) and copies only the outgoing edges that resolve at the
  destination (incoming edges are never copied). A **cross-memory** `move` is
  refused (usage error, exit 2, with the reason) for a safe subset — an
  encrypted source/destination memory, a git-backed source, a subtree containing
  a chat-root, or a node that would violate the destination's collection schema.
  Any move (same- or cross-memory) is a **conflict** (exit 5) if a live node
  already occupies the destination, or if the source changes concurrently
  mid-move. The confirmation identifies the node by its destination URN (carried
  in `--json`
  as `urn`).
- `node move … --dry-run` asks the server to plan the move without writing
  (`planNodeMove`, cor:api:190:03). The server runs the move's own checks; the
  CLI predicts nothing. The plan lists each node that would move with its
  current and new URN, in loc order, and the content-link warnings a
  cross-memory move would report, or says they are withheld when you can't read
  the destination memory. A move that would be refused fails the same way under
  `--dry-run`: the move's error and exit code (for example exit 5 for an
  occupied destination, exit 2 for a move into its own subtree), and no plan is
  printed. The plan is true when answered and reserves nothing: a change before
  the move can still end it with a conflict (exit 5). The server lists at most
  `--limit` nodes (its default 100; it bounds the value; `--limit` is refused
  without `--dry-run`, below 1, and above 2147483647, the GraphQL Int range,
  each with exit 2 before any request). A plan that does not list every node prints
  `TRUNCATED` with the shown and total counts and exits 1; a complete plan exits
  0. When the server lists fewer nodes than `--limit` asked for, that is its
  bound: the error says a plan cannot list this subtree in full, rather than
  suggesting a larger `--limit` (a subtree beyond the bound can never plan
  COMPLETE).
  `--json`: `{status: COMPLETE|TRUNCATED, complete, totalCount, truncated,
  pointInTime, nodes: [{id, currentUrn, newUrn}], linkWarnings: [{nodeId,
  nodeLoc, raw, status: MISSING|RETARGETED|AMBIGUOUS, target}] | null,
  linkWarningsWithheld}`; `linkWarnings` is null when withheld and `[]` when
  there are none. `status`, `complete` and `truncated` always agree:
  `truncated` is the CLI's verdict (a plan is complete only when the server
  does not flag it and the nodes it lists match its count), always `!complete`, not a copy of the server's
  flag (cli#2320). Sends no `moveNode`.
- `node merge <source> --into <target>` folds the source node into the target
  (the survivor) and returns the target. Name each by URN or a bare `<loc>` with
  `-m` (which scopes both). By default every mergeable field folds in; restrict
  with repeated `--field`: `CONTENT`/`ABSTRACT`/`DESCRIPTION` concatenate
  (target first), `TAGS` unions, `DATA`/`PROPERTIES` shallow-merge (target wins
  on key collisions), `EDGES` re-points the source's edges onto the target. The
  source stays in place unless `--delete-source` hard-removes it after the merge.
  Merging mutates the target, so it's gated — pass `--yes` non-interactively.
- `user set-roles <userRef> --role <r>...` sets a user's PLATFORM roles (`owner`,
  `admin`, `contributor`, `reader`; case-insensitive). Platform ADMIN only. The
  flags REPLACE the whole role set — they are not additive, so every role the
  user keeps must be passed, and any omitted role is removed. `--json` returns
  `{user, previousRoles, roles, changed}` so the displaced set is visible without
  a second read; `changed` compares set MEMBERSHIP (role order is meaningless)
  and is **null** when the prior roles could not be read, in which case
  `previousRoles` is `[]` because it is unknown, not because it was empty.
  `<userRef>` must match a user EXACTLY (id, handle, email) — unlike
  `--grantee` elsewhere, a partial match is refused rather than silently
  retargeting another account. Platform roles are global standing, NOT org or memory
  membership — for those use `org member set-role` / `memory member set-role`.
  Gated: pass `--yes` non-interactively.
- `user merge <source> --into <target>` globally consolidates a duplicate source
  user into the surviving target and returns the target. `<source>` is
  soft-deleted; `--into <target>` survives. Each ref is a user id, bare handle, or
  `hrn:user:<handle>` URN, passed through verbatim (the server resolves and
  authorizes — platform ADMIN/OWNER, or an org ADMIN/OWNER over both members). No
  server dry-run; the confirmation is the last local safety boundary, so it's gated
  — pass `--yes` non-interactively. Data is safe (memberships, owned memories,
  grants and connections transfer atomically, and role collisions keep the
  strongest **live** entitlement), but **logins are not**: a source field fills the
  target only where the target's is empty, so a populated collision is cleared from
  the source and that credential stops authenticating. Compare both accounts'
  `identityProvider`/`email`/`githubId` via `user search` first, and pick the
  target for the handle you want to keep — there is no admin handle rename.
- `server-info` reports the hadron-server this invocation targets:
  `{url, version, baseUrl, authenticated}`. It works **signed out** (the field
  is public), so it doubles as a reachability probe — a failure REACHING the
  server is the server or the network, not a missing login (a credential store
  that cannot be read is still reported: it fails loud rather than silently
  querying anonymously). `version` is the server's
  API-SURFACE CONTRACT version, bumped when the query/tool surface changes in a
  caller-visible way; it is NOT a release version, so use it to decide whether a
  surface exists rather than comparing it to a release tag. `url` is where the
  query was sent and `baseUrl` is what the server calls itself — a mismatch
  means it sits behind a proxy without its public base URL configured, so every
  absolute URL it emits points somewhere wrong. `hadron version` reports the CLI
  build instead and needs no network.
- **`node get` accepts an action URN** (#1138, hadron-server#2610): a register
  entry has a second address, `hrn:action:<root>:<agent-slug>:<code>`, and
  `node get hrn:action:…` reads that entry's node (no `-m`; `-m` is ignored for
  it). `--json` and the text read carry `actionUrn` (text: `action urn:`) when the
  server computes one and omit it otherwise; the node `urn` is unchanged. A
  foreign, missing or malformed action URN is one answer, exit 4 and no output
  (the CLI adds no parse error of its own); the not-found hint names both URN
  forms. In a multi-ref read an action URN is resolved to its node first (the
  server's batch read refuses that kind), and one that names nothing is listed
  in `unavailable` under your spelling (exit 4).
- **`node get --json` carries `abstractOriginHash`** (#306, spec 032) — the
  content fingerprint recorded when the abstract was written, on the batched
  read as well as the single-ref one. The key is **always present**: `null` is
  a real answer (no abstract, or one never fingerprinted — server #1128's
  *unverified* state), so do not treat a missing key and a null value as the
  same thing. Reading the hash alone does NOT tell you the abstract is stale:
  staleness is the hash DISAGREEING with the current content's hash, which is
  `sha256(content)` truncated to 8 hex chars — the server's own definition, so
  the comparison is exact wherever you make it.
- **`node get` carries `revision`** (#715, hadron-server#1339): the node's
  live revision, where creation is 1 and each committed authoring change
  advances it. The same value MCP's node read prints as `Revision: N`. The
  key is **always present**: `null` means the server predates revisions, and
  the human output then says `revision: unknown`; it is never guessed as 0 or
  "current". It is read in a separate small query, so everything else keeps
  working against an older server. It is read BEFORE and AFTER the content
  and kept only when both agree, since a revision advances on every
  authoring change: if the node changed in between (or became unavailable),
  the whole read repeats, and after 3 attempts `node get` exits 5 rather
  than print content beside a revision from another moment. A revision versions the NODE only: it
  does not version partials or template data the node includes, and it is not
  proof that the content complies with any task.
- **`node get` carries the server-authored stamps of that same revision**
  (cli#752, hadron-server#1326), read in the same before/after bracket, so a
  printed stamp always describes the printed revision:
  - `authorship` — `{kind: HUMAN|TASK, authoredAt, taskRef, taskRevision,
    human: {handle, urn}}`. A TASK the server won't let you read is REDACTED:
    `kind: TASK` with `taskRef: null` ("a task you can't read").
  - `contentValidation` — `{state, latestReport}`. `state` is for THIS
    revision: `PASS|FAIL|ERROR` from a report made for it, `STALE` when the
    latest report was for an older revision (`latestReport.nodeRevision` says
    which — an old PASS is never shown as current), `UNVALIDATED` when there
    is none. Advisory only: it blocks nothing and proves no compliance.
  - **Null convention:** on a server with stamps `contentValidation` is never
    null, so `contentValidation: null` means the server PREDATES the stamps
    (human output: `stamps: unknown`) and `authorship: null` then says
    nothing. On a stamping server `authorship: null` is the server's answer:
    legacy content, or not recorded.
  - `node revision list|get` add each SNAPSHOT's own `authorship` and
    `contentValidation` (the validation at that historical revision, as the
    server snapshotted it — never reconstructed from the live node), with the
    same null convention; the list table gains a VALIDATION column.
  - `approvalStatus` reports APPROVED, NOT_APPROVED or SUPERSEDED for the
    node's current text, with the latest approval record. APPROVED means the
    approval still covers the title, abstract and content, so a metadata-only
    revision (tags, properties, role, description, alias, loc, edges) keeps it
    and `approval.revision` — where it was recorded — can be older than the
    node's `revision`. SUPERSEDED means that text changed after approval;
    `mintStatus` reports
    whether that node is minted and, when known, the minted revision, hash,
    time and principal. A null status means the server predates that feature.
    `node approve <ref> [--revision N]` stamps the current revision, with the
    optional expected revision refusing a changed node (REVISION_MISMATCH,
    exit 5). `memory approve-all -m <memory> --yes` stamps eligible unminted
    nodes in bulk. `node verify <ref>` returns INTACT, NOT_APPROVED,
    SUPERSEDED or TAMPERED (content changed outside the write path, never
    reported as merely superseded); non-INTACT exits 5. `spec get` and `spec list`
    expose the same statuses, `spec describe` counts them, and `spec list`
    accepts `--approved|--unapproved` or `--minted|--unminted` filters.
  - **Passkey signatures** (hadron-server#1632; cli#818). The CLI asks for a
    signature and reads the outcome; it never signs and never takes an
    assertion or key material. `node sign <ref> [--revision N] [--signer
    <user-id>]` creates a signing request for the current revision and prints
    it (`--json`: id, nodeId, nodeUrn, revision, approvalHash, payloadVersion,
    state, ceremonyUrl, createdAt, expiresAt, endedAt, namedSigner,
    requestedBy {userId, appId, workerId, sessionId}, signature). A person
    completes it with their passkey on the server's signing page, whose link
    the server returns (ceremonyUrl, hadron-server#3120): text prints
    `ceremony: <url>` — copied verbatim, never composed; a value that is not
    a clean absolute web address is neutralised rather than echoed raw —
    while the request is OPEN only (an ended request's link only explains
    that it ended; its URL stays in --json, which carries the field for
    every state); `list` prints each OPEN row's `id ceremony: <url>` line
    under the table. It ends exactly once:
    COMPLETED, DECLINED, WITHDRAWN, EXPIRED or STALE (the title, abstract or
    content changed). `node signing-request get <id>` reads one (exit 4 when
    unknown or unreadable), `list <ref> [--state s] [--limit N] [--after
    <cursor>]` lists ONE page of a node's requests newest first ({nodeId, urn,
    loc, requests: [], hasMore, nextCursor}; default 20, the server clamps
    --limit to 100; pass the printed next cursor to --after, opaque and exactly
    as printed; each page reflects the list at that call), and
    `withdraw <id>` withdraws an open one you made. `node signatures <ref>`
    prints the server's re-verified status ({nodeId, urn, loc, revision,
    state, integrityProblems, signatures: []}); `state` is null with no
    signatures, and a passkey is shown by its label only. `--check` also
    rebuilds each record's v3 payload and challenge locally (cli#879) and adds
    a per-record `localCheck` (only with `--check`): PAYLOAD_CONSISTENT,
    HASH_MISMATCH, UNSUPPORTED_VERSION (any record that is not
    hadron-signature-v3, v1 included, never locally valid) or UNAVAILABLE
    (inputs unreadable to you; reported, never dropped). The server's `state`
    stays authoritative and is printed unchanged. Rebuild only: the passkey
    assertion is verified by the server, not by this CLI, and no assertion or
    key material is read. `--check` exits 5 unless the state is CURRENT with
    no integrity problems, or if any record is HASH_MISMATCH or
    UNSUPPORTED_VERSION; UNAVAILABLE never lifts a server failure. Refusals:
    NODE_NOT_FOUND, SIGNER_NOT_FOUND and SIGNING_REQUEST_NOT_FOUND exit 4;
    REVISION_MISMATCH, NODE_IS_PLACEHOLDER, ENCRYPTED_MEMORY_NOT_APPLICABLE and
    SIGNING_REQUEST_ENDED exit 5; COMMENT_NOT_SIGNABLE exits 2;
    SIGNING_REQUEST_NOT_REQUESTER exits 8. A server that does not offer
    signing refuses every one of these commands with exit 2, "this server
    does not offer node signing". A server that offers signing but predates
    ceremony pages (older than hadron-server#3120) refuses the ceremonyUrl
    read with exit 2, "this server does not offer signing-request ceremony
    pages; upgrade the server (hadron-server#3120)"; the two refusals are
    told apart by the node-signing capability token, never by version
    sniffing, and a server whose token cannot be read keeps the
    signing-off answer.
  - **For one node you already hold**, compare the two yourself — that is what
    `spec citations --stale-abstracts` does, and exposing the field is what
    makes it possible from `node get` at all. **Compare against RAW content**:
    read it with `--raw`, or the batched form (several refs, or `--prefix`),
    which return content uncompiled. A plain single-ref `node get` COMPILES Mustache templates,
    and the hash is defined over the raw plaintext — so on a template node the
    two disagree for that reason alone and the node reads as stale when it is
    not. Identical for a node with no templates.
  - **For a whole memory**, `hadron memory validate <memory> --check
    stale-abstract` reports it server-side. Note it **caps its findings**
    (default 200, max 1000), so on a large memory the stale set can come back
    incomplete and a node reads as fresh when it is not — which is exactly why
    the citation check computes its own rather than delegating (#355).
  - Either way, a comparison says the body CHANGED since the abstract was
    written, not that the abstract is wrong. Do not report it as "the abstract
    is incorrect".
- **`node get` prints the node's `urn:` and, under it, a `URL:` line — the
  SERVER-BUILT portal link that opens it** (#515, `cor:api:230:01`), on the
  BATCHED read as well as the single-ref one; #520 pins both queries to the
  same field set precisely so the two do not print different shapes for the
  same node. That is the line to COPY when citing a node to a human,
  rather than assembling `<origin>/app/u/<urn>` yourself: the canonical
  spelling, the resolver route and the deployment origin are all the
  platform's, and a composed link resolves to nothing while looking right.
  The link is **nullable for ONE reason here** — the deployment has no usable
  web origin — and renders as NOTHING: no line, no placeholder, and never a
  locally-composed fallback. **Do not carry the WORKER's second reason across
  to a node.** `Node.urn` is `String!`, so "no canonical identifier to build
  from" cannot occur; `Worker.urn` is nullable (an App URN predating the arity
  a worker URN needs), which is why the worker section below says two and this
  one says one. The
  `urn` is unaffected by the link's absence and still addresses the node. Under
  `--json` both are `omitempty`, so a surface that does not select them leaves
  the keys out rather than asserting null.
  **The NODE and WORKER `--json` shapes differ here, and a consumer must branch
  on it.** A node's `portalUrl` is `omitempty` — the key VANISHES when there is
  no link — while a worker's is a nullable pointer, so the key is PRESENT as
  `null`. Both are stated correctly in their own sections, three hundred lines
  apart, and neither used to say "unlike the other one": that is exactly how a
  consumer writes `if (d.portalUrl === null)` and has it silently never fire on
  nodes. Test for ABSENCE on a node and for `null` on a worker, or test for
  falsiness and cover both.
- `node get` reads MANY nodes at once. With one ref the output is the node
  object, unchanged. With several refs, or with `--prefix <loc> -m <memory>`,
  it is `{nodes, unavailable}`. Several refs are sent as one batched read —
  `nodeBatch` takes a PK or a fully-qualified URN, so nothing is resolved first.
  Up to the server's 200-node cap that is a single request; a larger set is
  chunked (and response-size spillover re-queued), never silently truncated. `--prefix` is the form for a whole branch: also one call, and unlike
  `node list` it returns full content and edges. An empty `--prefix` means the
  whole memory; the server caps the node count and fails loudly rather than
  truncating.
  `unavailable` lists refs that are missing OR not readable by you — the server
  reports those identically, so neither the CLI nor you can tell them apart —
  named as the ref that was sent (your ref, canonicalized), not as opaque ids.
  Any unavailable ref exits **4**, so a partial read is never mistaken for a
  complete one. A MALFORMED ref (not fully qualified, or a URN of the wrong
  entity) is a different case: it fails the whole call with exit **2**, before
  anything is read, so a typo never hides among the denials.
  **Rendered or raw (#736):** a single-ref read COMPILES the node's Mustache
  templates by default — every `{{…}}` is replaced by its value against the
  node's data, or by nothing. **`--raw` returns the STORED body**, placeholders
  intact; batched and `--prefix` reads are always raw (the server's bulk SOURCE
  read for lint/audit/migration). `--json` carries **`rendered`** on every node:
  `true` only for a compiled body. **To edit a node's content, read it with
  `--body-only`** (#873): it prints ONE node's STORED body and nothing else,
  byte for byte (no header; no newline added when piped), so `node get <ref>
  --body-only > body.md` → edit → `node update <ref> --content-file body.md`
  round-trips exactly; `--json` gives `{id, urn, loc, content, rendered:
  false}`. It implies `--raw`, takes one ref (several, or `--prefix`, exit 2),
  and `--content-only` is its alias. Both other reads corrupt a body written
  back: a rendered body has lost every `{{…}}` placeholder and `{{=…=}}`
  delimiter line, and text-mode `--raw` prints a metadata header ABOVE the
  body, which would be saved into it. The CLI's own
  read-modify-write paths (`coding preflight create|route` rewriting the router
  body) read raw.
- **`--where` reads ONE JSONB column, and the default is `properties` (#603).**
  A leaf reads `properties` unless it sets `"field":"data"`. A node's free-form
  envelope — `authorName`, `sessionId`, a chat message's whole payload — lives in
  `data`, so the obvious spelling
  `--where '{"path":["authorName"],"exists":true}'` asks the wrong column and
  matches nothing. **The failure is a plain `0`**: not an error, not an
  empty-with-caveat, so it reads as "the corpus has none" and gets published as
  a finding. Two people on this team did exactly that within an hour.
  `--sort-property` takes the same `field` key and the same default.
  When a predicate names no column AND **matched** nothing, both `node list` and
  `search` now print a note naming the default and the remedy — on **stderr**,
  in `--json` mode too, so the note never enters the payload you parse and never
  goes missing because you asked for JSON. Treat that note as "your instrument
  may be pointed at the wrong column", not as "no results".
  It keys off what the predicate MATCHED, not off the rows you were shown, so an
  empty page from `--offset` or `--seq-gt` does NOT trigger it — an empty screen
  and an empty match are different facts. Where the two cannot be told apart
  (`search --offset N`, or `node list --offset N` without `--seq-gt`/`--sort-seq`)
  the note stays silent rather than guessing, so **absence of the note is not
  evidence your predicate was right** — re-run it at offset 0 to be told.
  **Run a positive control** before concluding a zero is real: re-ask for a key
  you have already seen on a node with `node get`. A zero that survives a
  positive control is a finding; a zero that does not is an instrument fault.
- **`--with-properties` / `--with-data` project those columns** (#602), on
  BOTH `node list` and `search`, so the command that filtered on a field can
  show it. Opt-in, because a listing is a thin index and a large result with
  full envelopes is a much bigger payload; independent, so `--with-data` does
  not drag `properties` along. Without them the row shape is exactly what it has
  always been — the keys are ABSENT, not null. With them the key is always
  PRESENT, carrying `null` when the node's column is empty: **an absent key
  means "you did not ask", a null means "asked, and there is none"**, and those
  are different answers. In text output either flag replaces the table with a
  per-node block, since a column would have to truncate the JSON; on `search`
  that block omits abstracts unless you also pass `--long`. `node get` already
  returns both columns and is unchanged.
- `isRunnable` gates whether `hadron task run` will execute a node. Both
  `node add` and `node update` take `--runnable` to set it; on `update` it's
  tri-state — `--runnable` sets true, `--runnable=false` clears it, omitting it
  preserves the current value. `node get` shows `runnable:` in the text view,
  and both `node get` and `node list` surface `isRunnable` in `--json` (`list`
  also shows a `RUN` column with a ✓ for runnable nodes). `node list --runnable`
  filters server-side to runnable nodes (`--runnable=false` to the explicitly
  non-runnable; omit for all) — the listing counterpart to `hadron task run`'s
  gate.
- `skill` maintains the **skill surface** exported from runnable task nodes
  (#580). A node opts in by declaring `properties.exports.<host>` — an object
  keyed by skill host, each entry `{name, description, enable}` where `enable` must be `true` to publish (the retired
  top-level `properties.skill` and `properties.claudeSkill` are
  read as aliases for the `claudeSkill` host, so nothing needs migrating). The
  skill NAME is **stored** at `properties.exports.<host>.name`, prefix
  included — it is not derived, and there is no `--prefix` and no org prefix
  field. `enable` must be `true` to publish and DEFAULTS TO OFF — a declaration alone
  does not ship a skill, because the corpus holds many runnable automation nodes
  that were never meant to be skills.
  `skill lint` checks the corpus and touches no disk, for EVERY host
  (`claudeSkill`, `codexSkill`), each declaration against its own host's caps:
  name present, kebab-case and ≤64, and not one the host reserves
  (`skill-name-reserved`: `claudeSkill` reserves `synced`, in any
  capitalization — Claude Code's folder for claude.ai-synced skills);
  for `claudeSkill`, a description with no `<…>` markup
  (`skill-description-markup`: a `<` followed later by a `>` with at least one
  character between, as Cowork's upload validator refuses — measured on
  server#1341; `a < b`, `<>` and `<3` pass);
  description present and ≤1024 chars (the
  host TRUNCATES longer ones in its listing, so trigger phrases past the cut
  never fire); `isRunnable` set; body non-empty and frontmatter-free; no two
  nodes ENABLING one name for the same host (a declaration that is not enabled
  claims no name — cor:agt:030:06). Each `--json` row carries `hosts`:
  the host(s) whose judgment produced it. A finding every host reports
  identically is one row naming them all, and an unreadable node's row has
  `hosts: []`. A key under `exports` that names NO host (e.g. `codex`) is a
  `skill-unknown-host-key` WARNING, with one row per key the server lists and one
  aggregate row for any remainder, all with `hosts: []` — the keys
  belong to no host — never read as aliases, and never blocking a known host
  beside it, however malformed (cor:agt:030:06). Note what cannot be checked:
  with no prefix source, a name's PREFIX is unverifiable — `hadon-foo` lints
  clean. `--all` walks what the server lists for you — own-org, shared-with-you
  and other orgs' PUBLIC memories, every class; a per-user agent memory is
  never listed, so name it with `-m`. Errors exit 5;
  warnings alone exit 0 unless `--strict` promotes them.
  `skill status` reads BOTH sides and writes nothing: it walks
  `<root>/*/SKILL.md` (`--to user` → `~/.claude/skills`, `project` →
  `<git toplevel>/.claude/skills`, `plugin` →
  `<git toplevel>/plugins/hadron-cli/skills`, anything else a directory), pairs
  each file to its node by the **id** in its provenance header, and reports the
  drift CLASS per declared node. A header rendered by a server with revisions
  also carries `rev=N`, the source node's revision when the file was written
  (#715); status sends it as the file's `revision`, and the server reports the
  file `stale` when the node has moved on, even if the content hash still
  agrees. The value must be a positive integer of at most 2147483647; a
  malformed `rev=` voids the WHOLE header (as on the server), so that file
  reads as somebody else's. Export and plugin write the server's rendered
  header verbatim, so their files carry `rev=` with no client change. Against
  a server that predates the field (it refuses `revision` as an unknown input
  field), status and export ask once more without revisions instead of
  failing; any other refusal is still the run's error. **The class is the
  server's word** — the vocabulary is one place so it cannot drift between the CLI, MCP and the
  portal — and a file that exists but does not PARSE gets `class: null` with
  `parseFailure: true`, because a parse failure is a failure and not an
  eleventh class; read the two together and never `class` alone. In the table
  that row's CLASS cell is `—` (no class returned) with the reason in DETAIL;
  in `--json` the `class` key is present and **null**, never omitted, so
  "the server returned no class" stays distinguishable from "not asked for".
  A STATUS row also carries `outOfExportReason` (string or null) from the server.
  When present, DETAIL prints that reason verbatim even beside `current`;
  CLASS still describes the installed file's drift, not source eligibility.
  The CLI does not derive this reason from findings, an action, or a missing
  body. A file that PARSES and carries no Hadron header is somebody else's
  skill: never listed, moved or removed. A file that does NOT parse is reported
  either way — attributed to its node when
  the provenance header below the broken frontmatter is still readable, and
  otherwise listed locally under `unparseable` (never sent, since an
  unidentifiable file must not be claimed as an orphan). An unreadable file is
  listed under `unreadable` with its errno, likewise never sent. A `SKILL.md` that is not a regular file (a FIFO, a device) is listed
  under `unreadable` WITHOUT being read, so it cannot block the walk.
  There is deliberately no `--node` (the orphan and collision classes are
  properties of a SET). An error finding exits 5; drift and warnings alone
  exit 0, so a CI gate is an explicit `--strict`, which exits 5 on any drift,
  parse failure, orphan, empty scope, or — as in `skill lint` — any WARNING
  finding.
  The symbolic roots are **host-specific**, so a `--host` other than
  `claudeSkill` must name a directory (`--to <dir>`) rather than being resolved
  against Claude's. **`--to plugin` applies no visibility filter** — it covers
  every accessible task node, as the other targets do (D9, ruled 2026-09-22);
  curating which tasks a bundle carries is a later feature, expected to key on
  a named scope. A selection that resolves to NO memory sends nothing and
  says so — it is not an all-clear, because an empty `memories` list is omitted
  on the wire and the server reads an omitted one as *every* memory. That state
  sets `scopeEmpty: true`, lists any generated files it could not judge under
  `unchecked`, and **fails `--strict`**: a gate must not pass on a result
  nothing verified.
  `skill export` owns the shared writer of user-level skill files (also used
  by login's optional skill installation) (#621;
  `skill plugin` below builds bundles under an explicit `--out`). By default it
  exports every ENABLED declaration you can read (cor:agt:030:03; `memories`
  is omitted on the wire). Repeat `--node <id-or-fully-qualified-URN>` to
  export only named individual task files. The server checks readable name
  collisions beyond the selection, and the CLI submits all local file facts;
  unselected installed files are untouched and not assessed as orphans, so
  `--node` refuses `--prune` before disk or network I/O. Plugin bundles keep
  scope selection through `skill plugin` and never take node selectors. Both
  export modes write for EVERY known host, into user-level roots —
  `claudeSkill` → `~/.claude/skills/<name>/SKILL.md`, `codexSkill` →
  `~/.agents/skills/<name>/SKILL.md` — creating an absent root, with no host
  detection and no prompt. The deprecated `~/.codex/skills` is never written,
  moved or cleaned. **The server decides every action and renders the whole
  file**; export writes that body byte for byte (atomically) and reports what
  actually happened on disk. `--json` is `{dryRun, hosts:[{host, root,
  failure, scanned, judged, written, moved, removed, skipped, refused, failed,
  orphaned, pruned, unreadable, unparseable}], unrecognized:[…],
  outOfExport:[{host, node, nodeId, name, reason}]}`; every list
  is `[]` when empty. An item is `{node, nodeId, name, reasons, kept}`, and
  `moved` items add `from`. Each reason is `{code, message, origin}`:
  `origin: "server"` is the planner's reason VERBATIM (a drift class or lint
  rule), `origin: "client"` an I/O fact about this machine (`root-is-link`,
  `skill-dir-is-link`, `io-error`, `directory-kept`, …). `unrecognized` lists
  nodes whose `exports` names no host, ONCE, attributed to no host.
  `outOfExport` is the server's source-level exclusion for each host plan,
  independent of its file action. The human report ends with "Out of export —
  not written (N)" and prints each name, node, host and verbatim reason;
  an existing file is left in place, and `--prune` still acts only on orphans.
  Selected runs add `selectedNodes` and `orphanAssessmentSkipped: true`, plus
  `selections` when named tasks have no host entries. An unavailable ref is
  reported once, host-free, with no node id; a known task with no declaration
  for a host is a skip for that host. These fields are omitted in the default
  unscoped report.
  **Links are refused:** if a host's root, or any directory between `$HOME`
  and it, is a symbolic link, nothing is written for that host — `failure`
  is set and every one of its skills is in `failed`, named; a skill directory
  that is a link is `refused` whatever the plan said. `$HOME` itself is
  resolved, not refused. A removal (a disabled declaration, a move's old
  directory, `--prune`) deletes `SKILL.md` and then the directory only if it is
  empty; anything else stays and is listed in `kept`. A hand-edited file is
  refused unless `--force` (also when disabled); `--force` also regenerates a
  file with no node id. Orphans are reported and removed only with `--prune`.
  `--dry-run` reports the same outcome and changes nothing, not even an absent
  root. **One failing item never stops the others.** Exit: 0 when nothing was
  refused or failed; **5 after the full report** when any item was refused or
  failed, or a host could not be written; a run that cannot start (auth,
  connection) exits with that error's code. Hosts load skills at session
  start.
  `skill plugin` is the PLUGIN producer (#653), separate from `export`
  (B8): it builds one installable unit per host under an explicit, required
  `--out`, never inferred from the working directory or a checkout, and
  never at, inside or above a host skills directory (`.claude/skills`,
  `.agents/skills`, `.codex/skills`, user- or project-level — exit 2 before
  any request). `claudeSkill` → `<out>/<name>/`, a Claude plugin
  (`.claude-plugin/plugin.json` + `skills/<skill>/SKILL.md`) that is ALSO a
  one-plugin marketplace, so `claude plugin marketplace add <out>/<name>` then
  `claude plugin install <name>@<name>` installs it with no git;
  `codexSkill` → `<out>/<name>-codex/`, skill folders for `~/.agents/skills`.
  `--zip` adds `<name>.zip` / `<name>-codex.zip` (plugin root at the zip
  root, the portal's layout; upload the Claude one to Cowork). `--name`
  defaults to `hadron` (lowercase words joined by hyphens, ≤ 64). The Claude
  manifest's `version` is derived from the bundled content (`0.0.0-h<hash>`),
  so it moves exactly when a skill does — Claude Code updates on any version
  CHANGE. Selection is every enabled declaration you can read unless
  `--scope` narrows it to that scope's readable memories; **a scope with no
  readable memory never plans** and exits 2. The CLI sends the persisted
  Scope ID, and the server resolves membership afresh for each host. If host
  plans report different scope fingerprints, the CLI writes neither artifact
  and exits 5. Plans carry no files. An
  existing artifact is replaced wholesale ONLY if this command wrote it (a
  `.hadron-plugin` marker; a zip's comment); anything else, or a link, is
  left alone and fails the host (`artifact-not-ours` / `artifact-is-link`),
  also when it appears mid-build; `--zip` needs a filesystem with hard links.
  `--json` is `{dryRun, name, out, scope, hosts:[{host, format, artifact,
  zip, version, failure, scanned, judged, included, skipped, refused, failed,
  notForHost, findings}], unrecognized}`; `scope` is null unscoped, else
  `{id, name, memoryCount, droppedCount, resolvedVia}` (the human render also names the App a scope NAME resolved in); items are `skill export`'s
  `{node, nodeId, name, reasons, kept}` (`kept` always `[]`), bucketed by
  the SERVER's planned action; `notForHost` names tasks declared only for
  the other host; `findings` (`{node, nodeId, name, rule, severity,
  message}`) is every finding except an error on a refused/failed entry,
  never affects the exit, and passes unknown severities through. `--dry-run`
  writes nothing and reports the same refusals. Exit: 0 when every item was
  included or skipped; **5 after the full report** on any refused or failed
  item or host failure; 2 for a bad flag, `--out` or an empty scope.
- `chat` is the low-friction surface for a **team chat** — a shared memory where
  several agents and humans coordinate, each message a `message` node whose
  payload is in `data`, ordered by a server-assigned `seq` (see the "Set up an
  agent team chat" how-to). The chat is named by the `--node <urn>` of the node
  whose direct children are the messages — one copyable URN packing the memory
  and the message location (or the two-field `-m <memory> --messages-loc <prefix>`
  the push channel uses). It saves an agent the per-turn plumbing: `chat read
  [--since <seq>]` returns the new messages in ONE call as a compact transcript
  (`--json`: `{messages:[{seq,loc,author,identity,role,timestamp,body,
  sessionId,mentions,authorWorkerId,authorUserId,authorAppId}], nextSince}`) —
  pass `nextSince` back as `--since` next turn. **`author` is the name the
  SERVER recorded** (`data.authorName`), not a handle parsed out of the loc
  (#630): the loc suffix is a lowercase mention token, so reading it rendered
  `jonas` where the server had written `Jonas`, and mis-attributed any handle
  containing a `-`. Precedence is `authorName` → the retired academy `author` →
  the loc, which now runs only for legacy rows that carry no envelope author.
  `authorWorkerId` / `authorUserId` / `authorAppId` are the envelope's identity
  fields, so a consumer can tell a WORKER post from a human one. `chat post`
  (`--body <text|->` inline or over stdin, or `--body-file <path>` for a composed
  multi-line message) writes through the **Channel** that chat is
  (`createChannelMessage`), creating it on first post: the server mints the loc,
  assigns the `seq` atomically, extracts `@mentions` and records the author, so
  the CLI builds none of them (#367). `--reply-to` takes the target's `seq`, or
  its loc/URN at the cost of one read. `--json` is
  `{loc,nodeId,seq,replyTo,author}` — **`loc` is always `null`** now that the
  server owns it, kept as an explicit null so it is distinguishable from a
  missing key; `nodeId` is the address that replaces it, and `author` is the
  name the server RECORDED, which is how an agent verifies it posted as whom it
  meant to.
  **Authorship comes from `--session <id>`** (the worker session to post as);
  without it the server records the human. `--handle`/`--identity`/`--role` are
  still accepted from flags, `HADRON_CHAT_*` and `.hadron/config.json` but no
  longer affect the post — they warn once on stderr (never on `--json`) and name
  `--session`. Reads still parse the stored envelope, so messages written before
  this keep their author. The chat coordinates still resolve from a flag, then
  `HADRON_CHAT_*`, then `.hadron/config.json` (`chat.node`, or
  `chat.memory`+`chat.messagesLoc`), so a configured agent's whole turn is
  `chat read --since <seq>` / `chat post --session <id> --body "…"`.
  **`chat config show` and `chat doctor` (#477) say which chat that is.** A stale
  `.hadron/config.json` posts into the wrong chat without any error, so `config
  show` reports, offline, the file found (nearest ancestor; `config.status`
  `loaded|malformed|absent`, plus unreadable nearer files under `skipped` —
  read and post walk past those and ignore a malformed one), every supplied
  coordinate with its `source` (`flag|env|config`), `name` and whether it was
  `used`, the `resolved` `{form,memory,messagesLoc}` (or `null` with `problem`),
  and any legacy `handle`/`identity`/`role` under `deprecated` with
  `effect:"none"`. Only those known keys are printed — never `appKey` or any
  other key in the file. `chat doctor` adds ONE read of the Channel at the chat
  root: `channel.status` `readable|unavailable|error|not-checked`, `ok`, and
  `problems[{check,detail}]`; it ALWAYS prints the report, then exits with the
  first failing check's code in the order config, coordinates, channel (2
  config/coordinates, 4 Channel unavailable; a read that fails outright —
  not signed in, a refusal, transport — is `error` with its own code, e.g. 3,
  8, 7). `unavailable` does not distinguish absent from unreadable
  (the server answers both identically), and `channel.lifecycle` is always
  `"not-provided"`: a Channel has no archived/live/superseded fact, so neither
  command infers one from its last message. Neither posts, creates a Channel or
  writes config.
  **`chat config set` (#477) is the one writer.** `chat config set --node REF`
  (or `-m MEMORY` and/or `--messages-loc LOC`; `--node` excludes the other two)
  writes `chat.node` / `chat.memory` / `chat.messagesLoc` into the nearest
  `.hadron/config.json` (creating `./.hadron/config.json`, mode 0600, only when
  none exists). It edits only the named keys: every other key, `appKey`
  included, keeps its raw bytes and order and is never printed. An existing file
  is copied to `config.json.bak` first and replaced atomically with its mode
  kept. Exit 2, nothing written, on invalid JSON, an empty or non-object file, a non-object `chat`,
  a duplicate key (case-insensitive, as the readers match), a symlinked file, an unreadable config anywhere on the search path (even beside a readable ancestor), a blank value, an unparseable node URN, or
  no flag. It takes no handle/identity/role/session flag, never posts and never
  contacts the server. `--json`: `{path,created,backup,set:[{key,value}]}`.
  A message body read from `-` (`chat post --body -`, `team chat post -`,
  `channel post <address> -`) is for a PIPE and is **refused (exit 2) when
  stdin is an interactive terminal** (#648), like `--content -`: use
  `--body-file <path>`, or for `channel post` a `< file` redirect.
- `--reason "<text>"` on `node update` and `replace text` records *why* a change
  was made in the node's version history (the same field MCP `hadron_update_node`'s
  `reason` populates). Optional; omit it and history falls back to the caller
  identity. It rides whichever mutation runs (replace, `--data` replace, or
  `--data-merge`). Only updates snapshot a prior version, so there is no
  `--reason` on `node create`.
- `node export <urn>` writes one node to a portable, self-describing file
  (frontmatter markdown, or `--format json`) — to stdout by default so it pipes
  into `node import`, or `-o <file>`. Rendered by the server (the one renderer
  shared with the portal and every other client, so the bytes are identical
  everywhere); needs a server with `nodeExport` (hadron-server #386). `node import
  [<file>|<dir>|-]` has THREE modes, chosen by the `mode` field in its `--json` output:
  - RESTORE (default) recreates an export file: a node already at the target loc
    is updated, else created. The target memory and loc come from the file's
    `memory:`/`loc:` keys; `-m`/`--loc` override them (re-homing a node into
    another memory). Outgoing edges are imported only with `--with-edges`
    (best-effort: targets resolve by loc then id, an edge that can't be wired is
    reported in `unwiredEdges` with a `reason`, never fatal, and re-import is
    idempotent); `--create-only` refuses to update; `--dry-run` classifies
    without mutating. Markdown removes exactly one framing LF at each end;
    indentation and authored boundary newlines remain. Empty/ASCII-whitespace-only
    bodies are omitted (preserving stored content on update); Unicode-only spaces
    remain content. Whole-file CRLF→LF normalization stays, including the ambiguity
    where a content-final CR joins the frame LF. Extra handwritten trailing blank
    lines are content. JSON content is unchanged. The server recomputes `contentHash`/`abstractOriginHash`,
    so a clean export→import round-trips losslessly, except for the node's
    governed kind and `objectType` from a `node export` file (below). The file's `role:` and
    `runnable:` keys (`role`/`isRunnable` in JSON; emitted when non-null, so an
    explicit `runnable: false` is kept) carry the node's governed kind, and the
    write goes through that kind's door, like `node add`/`node update` (cli#714).
    `objectType:` (#725) is carried the same way: right after `type:`, written
    only when set, and a file without it, or with a blank one, preserves the
    stored value — a file never clears it (`node update --object-type ""`
    does). On a memory that declares a property schema the server validates it,
    and an undeclared collection is exit 2 (cli#720).
    `memory export` and the server's git sync write these keys; `node export`
    (server-rendered) does not yet, so its files import with no opinion. `node
    import` reads them all back; the server's own git-sync IMPORT does not
    restore `objectType` yet (a restore from the git mirror drops it; routed to
    hadron-server).
    A file without them preserves the stored kind. A file that declares two
    governed kinds is refused, exit 2, before any request (`--dry-run`
    included); one whose kind meets a different stored kind is refused exit 2,
    by the CLI when the stored kind can be read, else by the server
    (`ROLE_GOVERNED`, also exit 2 since cli#721). Nothing is written either
    way. Landing on an EXISTING node
    overwrites it — like the destructive commands, that prompts on a terminal and
    requires `--yes` non-interactively (a prior version is kept); a create is
    never gated.
  - CONTENT ingests RAW external source and lets the server convert it to the
    node's Markdown body. Selected by `--url <url>` (fetched server-side,
    SSRF-guarded, WITHOUT your credentials — authenticated pages must be captured
    and passed inline), by a `.pdf`/`.html` file, or by `--as-content` to force an
    otherwise-ambiguous `.md`/`.json`/stdin source into this mode. The content
    type is inferred from the file extension (`.pdf` → `application/pdf`,
    `.html`/`.htm` → `text/html`, `.md`/`.markdown` → `text/markdown`) unless
    `--content-type` overrides it (required for a PDF over stdin, since a pipe
    has no extension); a PDF's text layer is extracted to Markdown and the CLI
    base64-encodes it for you (scanned/image-only PDFs error). Target with
    `--node <urn>` or `-m`/`--memory` + `--loc`; a node already there is updated
    in place, else created (nodeType defaults to `webpage`, or `info` for a PDF).
    `--name`/`--type` and `--properties`/`--properties-file` (provenance JSON)
    are optional. Flags belonging to the other mode are rejected loudly.
    `--task <ref>` runs a task node against the just-stored node (the server
    mints a MANUAL run and passes the imported node's URN as
    `eventData.importedNodeUrn`); the response is `status:"FETCH_PENDING"` +
    the run id in `jobId` (follow it with `run get <id>`). `--task-args <json>`
    adds template args, `--app <ref>` names the App (default: your active
    App) and `--agent <ref>` the installed Agent the run is served as; all three
    require `--task`.
  - RECURSIVE (`-r <dir>`) maps a local DIRECTORY TREE into the memory graph:
    each directory becomes a branch node, each text file a leaf node (loc =
    slugified path without the extension; name = filename), and the hierarchy
    becomes parent→child `contains` edges (materialized inline, bottom-up, no
    extra round-trips). A directory's `README.md`/`index.md` folds into that
    directory's node instead of a separate child. Binary files are skipped and
    listed under `skipped[]`; loc collisions are suffixed (`setup`, `setup-2`)
    and listed under `collisions[]`. Import is create-only: an existing loc is an
    error unless `--on-conflict skip` (which leaves it in place and lists it under
    `existing[]`). When that refusal leaves nothing created, stdout stays empty
    in both modes (exit 5, error on stderr); a failure after some node landed
    still prints the summary of what landed, then the error (#1231). `--under <loc>` roots the tree under a prefix; `--include`/
    `--exclude` globs, `--hidden` (dotfiles; `.git` always skipped), and
    `--max-file-size` filter the walk (globs are root-relative and validated up
    front; symlinks/special files are skipped); `--dry-run` prints the plan
    without writing. Under `--on-conflict skip` a pre-existing branch is still
    wired to the children created this run; a skipped node whose id can't be
    resolved is listed under `unresolved[]` (its parent edge is dropped). `--json`
    is `{mode:"tree", root, created[], existing[], unresolved[], skipped[],
    collisions[], edgesWired, nodesCreated}`.
- `memory clone <id-or-urn> --target-urn hrn:mem:<root>:<slug>` deep-copies a memory
  (nodes, edges, pending edges) into a new memory named by `--target-urn`
  (a fully-qualified `hrn:mem:<root>:<slug>` URN) and rewrites references to the source
  memory's URN inside node content and abstracts. The target org MAY differ
  from the source's, cloning into another org — you must be a non-reader
  member of that target org. Version history, shares/subscriptions, assets,
  and git-sync config are NOT copied. Encrypted memories and agent system /
  app memories cannot be cloned.
- `memory transfer <memoryRef> (--org <ref> | --user <ref>) [--class <c>]`
  previews a free-standing memory ownership transfer by default, including
  its old/new URN, dependent handling and typed blockers. It changes nothing
  until `--apply` (interactive confirmation) or `--yes` (scripted apply) is
  given. Apply sends the previewed `newUrn` as `expectedNewUrn`; if the
  destination changed, `STALE_TRANSFER_PREVIEW` exits 5 and nothing moves.
  Crossing a user/org boundary requires an explicit target class. Personal
  and private memories may move only to an org the owner administers; they
  cannot be transferred to another user. Group members refuse transfer until
  `--reset-group-members` is supplied, which revokes every old membership.
  `--json` is `{memoryId, oldUrn, newUrn, fromOwner, toOwner, currentClass,
  targetClass, dependents:[{kind,count,handling}], blockers:[{code,message}],
  canApply, applied}`. A blocked preview keeps all blocker codes in the output
  and exits by the first blocker: 2 for invalid arguments/class, 5 for state
  conflicts/dependents, 8 for forbidden; not found or concealed exits 4.
- `memory extract <parentRef> <targetUrn> [--move]` extracts a parent node and
  its loc-subtree into a NEW memory named by `<targetUrn>` (a fully-qualified
  `hrn:mem:<root>:<slug>` URN, root MAY differ), making the parent the memory's root — locs
  are rebased (`findings:auth`→`<slug>`, `findings:auth:oauth`→`<slug>:oauth`).
  `<parentRef>` is a node id or fully-qualified `hrn:node:<root>:<slug>:<loc>`
  URN; pass `-m hrn:mem:<root>:<slug>` with a bare loc instead. Default COPIES (source
  intact); `--move` relocates it, soft-deleting the source subtree (needs
  source write access, cannot target the source root). The new memory preserves
  the source's class. v1 limitation: node content is copied verbatim, so URN
  references among the moved nodes break (slug + locs change); unresolved
  pending edges in the subtree are dropped.
- `memory export <id-or-urn> [--out <dir>]` writes every node to a local
  directory as frontmatter
  markdown (`<out>/<loc>.md`, one self-contained file per node, colons in the
  loc become path segments) — the same layout the server's git sync produces,
  but on disk and without a remote. Each file carries the node's `role:` and
  `runnable:` when set, as the git sync does (cli#714).
  **`--out` defaults to `.`, BUT an omitted `--out` is REFUSED (exit 2, usage)
  when the current directory is inside a git work tree and is not empty** — a
  whole memory scattered among your files, overwriting same-named ones
  silently, is the hazard (#583). Pass `--out <dir>`, or `--out .` to state
  that you meant the current directory; an explicit `--out` is never refused,
  so a script that passes one is unaffected. Nodes
  are pulled in bulk; `data`-type nodes are skipped; nodes the read API
  cannot return come back under `unavailable` in the `--json` summary
  (a client-side export is bounded by per-node read access, unlike the
  server's full-DB git push). Outgoing edges whose target is in a memory you
  can't read (#781) can't be round-tripped, so they're omitted and counted in
  the summary's `hiddenEdges` (with a stderr warning) rather than dropped
  silently. Existing files are overwritten but files for
  removed nodes are never deleted. `--format markdown` is the default and
  only target today.
- `agent list --owned-by-me` and `app list --owned-by-me` are the same owner
  slice on the other two entities — `cor:api:120:03` names all three, and the
  contract a reader may rely on is *the entities this caller owns*. Do NOT
  infer one predicate: `Agent` and `App` carry a single owner column, while
  `Memory` carries two and splits per class. Org-less by definition, so
  neither consults your active organization and both give an App-key caller an
  empty list. `agent list --owned-by-me` still composes with `--type` and
  `--visibility`; it is rejected (exit 2) with `--org`, and with `--public`,
  whose `PublicAgentFilter` has no `ownedByMe` field at all because a PUBLIC
  agent is never user-owned. **`app list` requires `--org`, `--owned-by-me`, or `--agent`**. The
  Agent selector can stand alone across Apps you may read, or AND with one
  of the other two; `--org` and `--owned-by-me` remain mutually exclusive.
  The bare command still refuses (exit 2).
- `memory list --owned-by-me` NARROWS the default listing to the memories you
  own outright — org-less (`organizationId` null) and yours. The server answers
  it (hadron-server#1215); do not reconstruct it from a listing, because
  ownership is keyed on a different column per class and the `personal`/
  `private` class pair only LOOKS like an ownership test (#1176). A
  `knowledge`-class memory under your own handle is in this slice; an org-owned
  memory never is. Your active organization is not consulted — the slice is
  org-less by definition — and an App-key caller or impersonated session gets
  an empty list. Composes with `--include-agent-system`; rejected (exit 2)
  together with `--shared-with-me`, whose slice excludes owned memories, so the
  intersection is empty by construction.
- `memory list --shared-with-me` SWITCHES the listing to the memories other
  users shared with you (you are the grantee). It is a slice selection, not a
  narrowing: that set is disjoint from your own union, so the flag excludes
  every memory you own, and plain `memory list` is the only way to see those.
  This is how you discover the URN of a memory shared with you — it is
  otherwise absent from every listing even though you can read its nodes. Each
  row adds `shareRole` (`reader`/`writer`) and `sharedBy` (the grantor, as the
  same `{id,name,email,handle}` object `share list` emits — feed `id` back to
  `share rm --grantee`). Both keys are ABSENT on the ordinary listing, so their
  absence means "not a shared-with-me listing". Rejected (exit 2) together with
  `--include-agent-system`, which selects the other slice. An App-key caller
  gets an empty list — sharing is user-to-user.
- `channel register` is the Channel PARTICIPATION surface (spec 049 Phase 5) —
  which attendees take part in a Channel. **It NEVER grants access**: a row
  declares intent, not permission (D-2026-09-13-008), and what an attendee may
  read or post is the Channel's HOST MEMORY's decision. Adding a row confers
  nothing and removing one revokes nothing — to change who can reach a Channel,
  change access to the memory hosting it (`memory member` / `memory share`). A
  register entry carries an attendee (a Worker or an Agent), a `role` (`BOTH`
  takes part in reading and posting, `POST` posts, `WATCH` reads),
  `mentionOnly`, and an OWNER (an App or an organization) that is the context
  the attendee is named in. **Not the worker-name register**, which
  was removed in hadron-server#1050 and is unrelated — every `register` hit in
  this CLI's own GraphQL files is that retired one.
  `register add` needs **exactly one** of `--attendee <ref>` or
  `--all-attendees`; the latter is the WIDE entry covering every attendee in
  the owner's context. On the wire the wide entry is the ABSENCE of
  `attendeeRef`, and the server does read an omitted attendee as "everyone" —
  but the CLI refuses (exit 2) rather than letting a forgotten flag widen a
  declaration, so the wide form must be asked for by name. `register set` changes
  only `--role`, `--mention-only` and `--description`; the attendee and the
  Channel are immutable, so moving a registration is `rm` + `add`. An unset
  flag is omitted (preserve), so `--mention-only=false` is how you turn it off
  and a bare `set` with no field is rejected (exit 2) rather than reported as
  a change. `register rm` is a SOFT delete and needs `--yes` non-interactively;
  a `false` result is exit 4, not success; and an explicitly empty
  `--role=` is exit 2 rather than silently omitted. `register list`'s filters AND
  together and a ref that names nothing MATCHES NOTHING rather than failing, so
  an empty page is ambiguous — the command says so on **stderr**, leaving
  `--json` a clean `[]`.
- `memory member` and `memory share` control who can access a memory.
  `member list|add|set-role|rm <memory> --user <id> --role <owner|writer|reader>`
  manages team membership (rows exist only on group-class memories);
  `share list <memory>` and `share create|set-role <memory> --grantee <user-ref>
  --role <writer|reader>` grant individual users access (`--grantee` accepts a
  user id, email, handle, or `hrn:user:<handle>` — resolved to a user id
  client-side, among users you can see; for a grantee outside your org
  visibility, pass their id). `share rm <memory> [--grantee <user-ref>]` (alias
  `revoke`; no `--role`) removes a share: with `--grantee` the owner revokes
  that user, without it you remove YOUR OWN share of a memory someone shared
  with you — the leave path, which needs no ownership.
  `subscription` grants an entire organization
  access (the org-level counterpart, with the full Role set) — `subscription
  create|set-role <memory> --org <id> --role <owner|admin|contributor|reader>`,
  and `subscription list <memory>` / `subscription rm <memory> --org <id>`.
  The server withholds the subscribing organization from a caller who may
  manage the subscription but not read that org (e.g. the owning org's admin
  on `set-role`, cli#1246): the command still succeeds, `organization` is
  `{"id":"","name":"","urn":""}` (never filled in), `organizationHidden: true`
  is added, and `create`/`set-role` echo your own `--org` as `orgRef`; `list`
  keeps such rows (text shows `(hidden)`). For a visible org, `organization`
  carries its id/name/urn and `organizationHidden` and `orgRef` are omitted;
  `organization` itself is always present. The memory ref is an id or URN.
  `member add` and `share create` upsert (a repeat changes the role);
  `subscription create` does not: an existing subscription is refused, so
  change its role with `subscription set-role`. `member rm` / `share rm`
  / `subscription rm` require `--yes` non-interactively. Find user IDs via
  `org member list` or `auth whoami`.
- `spec` manages product-spec nodes. Governance requires a `spec` / `spec.*`
  role, not a tag alone (cor:acl:130:01). `list` marks tag-only candidates
  `[not governed]` and adds `governed` on every JSON row. `describe` counts
  only governed specs and reports tag-only candidates separately through
  `notGoverned` and `notGovernedNodes` (`citation`, `governed: false`).
  `lint` emits only warning `not-governed` for a tag-only candidate, without
  spec-shape checks; `--strict` retains its warning-to-error policy. Findings
  add `governed`: true/false for a known node, null for memory/corpus or
  unknown-node findings. Repair with `hadron node update <node-urn> --role spec`, which explicitly
  sets the role. A tag-only node that already has another governed role (`review`) is
  refused by that command (one governed kind at a time), so its `not-governed` message
  says so and that changing the role needs deliberate review, instead of promising the
  command; null-role and non-governed-role nodes keep the plain hint (#1779). `spec edit` alone preserves a missing role. Other legacy-marker users,
  including `link` and `edit`, remain unchanged. A spec's loc is its citation. **Any
  valid node loc works, at any depth and in any shape (#708)**: `get`, `list`,
  `edit`, `link`, `find`, `grep`, `replace` and `check-tools` check only the
  generic loc rule (colon-separated slug segments), and never drop a spec from
  a listing for its shape. Some corpora use a legacy numbering — flat
  (`<module>:<feature>:<rule>[:<flow>]`, e.g. `msg:010:02`) or product-rooted
  (`<product>:<module>:<feature>:<rule>[:<flow>]`, e.g. `cli:cha:010:01`), with
  reserved general-provisions contracts (feature `:00`, module `:000`, product
  `:gen`). That numbering is what `spec new`'s allocation and contract flags
  produce; it is a convention, not a rule other commands enforce.
  It takes `-m/--memory` and addresses specs by bare citation, not a full URN.
  `spec get` shows one citation, or `--prefix <prefix>` dumps every spec under
  any branch of the loc tree, at any depth, with the same per-node detail, paged to
  exhaustion (`--limit`/`--offset` fetch a single page); `--json` emits an array.
  `--body-only` prints one spec's STORED body, byte for byte (placeholders and
  `{{=…=}}` delimiter lines intact, no header) — the read to edit from, into
  `spec edit <citation> --content-file` (#873; it printed the rendered body
  before, which is how a delimiter line was lost).
  `spec describe` inventories a memory's spec corpus — `specs`, `roots`,
  `maxDepth`, `legacyNumbered`, `outsideNumbering` — and classifies nothing
  (#709): no flat/product scheme, no per-tier counts, and `--declare` is
  RETIRED (refused, exit 2, nothing written); a scheme still stored in the
  memory's data is reported as `retiredDeclaration` and never applied;
  **`spec new <loc> --title <title>`
  creates exactly that spec at any valid loc** (#708): nothing is derived from
  the loc's shape — no parent it must have, no contract it inherits, no number
  allocated — so its only edge is an explicit `--inherit <loc>`, a loc that
  already holds a node is refused, and it can't be combined with the tier
  flags below. For automatic citation-prefixed names, `--title` is only the human text after
  "<loc> — "; an already-matching prefix is refused with exit 2 before any
  write (also in --dry-run). Remove the prefix and retry: the refusal ends with
  the corrected `--title`. Explicit --role keeps its literal-title behavior.
  Without a `<loc>`, `spec new` allocates the next number in the
  legacy numbering (for `--new-feature` and `--feature` without `--rule`/`--flow` the SERVER picks it, cli#398: one
  `allocateSpecNode`, then one revision-guarded fill of that node; no retry, no local fallback; a failed fill leaves the
  number reserved and `--json` reports `{citation, allocated: true, fillStatus: "refused"|"unknown", reason, recovery: [commands]}` with exit 1/7 — read it, then resume with the `recovery` commands (title, abstract, body, tags, role, edges, the feature's contract), never rerun `spec new`; a filled feature whose contract fails reports `{citation, contractCitation, contractStatus: "refused"|"unknown", fillStatus: "filled", recovery}`; a blank body (an explicit empty `--content` too) is a usage error before any number is reserved; `recovery` commands are shell-quoted and keep the run's tags and `--no-edges`; a lost or unusable allocation answer exits 7 and a placeholder may exist; `--dry-run`
  reports `allocatesUnder` instead of a citation) and scaffolds a **tier-appropriate** skeleton — a `## Modules`/`## Features`
  index for a product/module root, a load-bearing-point + rule list for a
  feature root, a general-provisions skeleton for a contract, or the
  four-section rubric for a rule/flow
  (`--new-product`/`--new-module`/`--new-feature` create roots and, by
  default, **also scaffold that tier's general-provisions contract** — a
  product's `:gen`, a module's `:000`, a feature's `:00` — so children have an
  inheritance target immediately; `--no-contract` opts out, and the co-created
  node is reported under `also` in `--json`. `--contract`
  scaffolds the contract at the deepest tier named on its own;
  `<citation> --new-path` scaffolds a whole chain at once — it creates the given
  citation and **every missing ancestor** (each with its tier template and
  contract), so a fresh module+feature+rule is one call; the abstract can
  come from `--abstract-file`/`--abstract -`; `--dry-run` previews
  without writing); `spec edit <citation>` opens the spec's abstract **and** body
  together in $EDITOR pre-loaded (divided by sentinel lines) — or replaces either
  non-interactively from `--content -`/`--content-file` and/or
  `--abstract -`/`--abstract-file`. Use `--description`/`--description-file`
  to change the description shown in spec lists and search through the same
  guarded write. The description is not in the editor buffer. `--title <text>`
  (cli#810) retitles the spec through the same guarded write: it sets
  `Node.name` to the complete literal text for a typed `spec.<type>` role,
  as `spec new --role` does. Legacy specs keep `<citation> — <text>` (pass the text only; a value starting with
  the citation prefix, a blank one or a multi-line one is refused, exit 2), alone
  it opens no editor, and a title equal to the stored one writes nothing. The
  title is an approval-hash field (`cor:dmo:120:01`), so a retitle supersedes an
  existing approval: after a save, `--json` carries `titleChanged` and
  `approvalStatus` (`{state, approval}`) read back from the server (omitted on a
  dry run, where only a note says so, and when the status read fails or the server
  predates approvals); a returned name that differs from the one sent exits 1.
  Only changed fields are written; the rest are preserved. An abstract replacement is
  limited to 2000 UTF-16 code units, including whitespace and newlines;
  both preview and save reject an over-limit proposal. `spec edit` reads the
  body **raw** (as stored, `{{…}}` placeholders intact), so the editor, the preview and the
  write never see Mustache-rendered text (cli#737). So does every other `spec`
  command that writes back a body it read: `spec supersede` (the retirement
  note, and `--copy-body`) and `spec extract --strip-source` (cli#742). **`--dry-run` shows the
  change itself and writes nothing:** the terminal prints a unified diff per
  field, and `--json` carries `changes[]`, one entry per field written, each
  `{field: content|abstract|description|name, change: replaced|cleared|reaffirmed, before, after,
  diff}` (`before`/`after` byte-exact; `diff` is `""` for `reaffirmed`, which
  re-sends the same text; `cleared` also covers a whitespace-only abstract,
  which the server stores as null). A no-op is `changed: false` with
  `changes: []`, and a no-op dry run still closes with the not-an-approval line. The
  same `changes[]` is reported by a real edit, built from the one proposal the
  write sends: the changes a run reports are exactly what that run writes.
  Authoring requirements are still checked only at write time, pending the
  non-writing server check (#4168). Dry-run text says so; its JSON adds
  `authoringCheck: "write-only: ..."`. A missing or supplied authoring claim
  is not validated by the preview. No role-to-task map or probing write is used.
  A preview is **not an approval**: applying it is a separate run without
  `--dry-run`, which recomputes against the spec as stored then.
  **Every save is guarded (cli#738):** the write targets the immutable node ID
  read with the body and sends that node's revision as `expectedRevision`, so a
  concurrent change or a replacement at the same citation is never overwritten.
  `--json` carries **`nodeId`**, **`revision`**, and **`proposalHash`**; a dry
  run also prints all three flags to carry them. The hash covers the exact
  mutation input, so a changed file, stdin, or editor buffer cannot silently
  replace an approved proposal. To save a proposal approved in an EARLIER turn,
  pass **`--expected-node-id ID --expected-revision N --expected-proposal-hash HASH`**
  from the same dry run. If the node, revision, or proposal changed, the save
  refuses with exit **5** and does not apply the proposed edit. A pre-write
  refusal writes nothing. A server-reported write-time `NODE_WRITE_CONFLICT`
  also exits **5** and may leave a server-recorded revision for out-of-band
  drift, but does not apply the proposed edit. The CLI keeps
  the proposed text in a named file when the file can be created; a file-save
  failure is reported without claiming the proposal was kept.
  Either way: re-read, reconcile, and get renewed approval before saving. A
  server without revisions or guarded writes is refused (exit 2), never written
  to unguarded. In `spec new|edit|
  extract`, `--content -` and `--abstract -` are for a PIPE and are **refused
  (exit 2) from an interactive terminal** (#648); use the `-file` form. **A body-only edit
  ARMS `abstract-stale`** (the abstract was fingerprinted against the old
  content), and preserving an unchanged field by omission is also what makes the
  marker unclearable — re-running with the same abstract writes nothing.
  An explicit unchanged `--abstract`/`--abstract-file` with a body edit that
  would arm stale is refused (exit 2, ZERO mutations), including `--dry-run`.
  Drop that abstract flag and deliberately attest with
  `--abstract-still-accurate` after re-reading, or supply a changed abstract.
  An unchanged abstract alone remains a no-op.
  `--abstract-still-accurate` (#612) is the way out: it asserts you re-read the
  abstract and it still describes the spec, and re-sends it UNCHANGED so the
  server re-fingerprints it against the new body. Pass it with a body edit, or
  **alone** to settle a marker an earlier edit left behind — alone it still
  writes, and `--json` reports `abstractReaffirmed: true` with `changed: true`.
  `abstractReaffirmed` says a re-affirmation was REQUESTED; whether it LANDED
  is `abstractVerification` (cli#750), read from the write's own response —
  `refreshed` (the stored fingerprint matches the body the guarded write
  stored), `not-refreshed` (the write succeeded but the server left the
  abstract stale, hadron-server#1408 — exit 1, after the full result),
  `unverifiable` (the server returned no fingerprint; exit 0, and the text
  claims no refresh), or `null` when nothing was re-affirmed (not requested, or
  a `--dry-run`, which describes the proposal and verifies nothing).
  It is refused alongside `--abstract`/`--abstract-file` (a replacement is
  re-fingerprinted anyway); on a spec with no abstract, where re-sending an
  empty value would CLEAR the field rather than re-affirm it; and on a LEGACY
  abstract past the 2000-char cap, where re-affirming turns a preservable value
  into a replacement the server rejects. There is no
  `abstractStillAccurate` argument on GraphQL — that exists only on the MCP
  `hadron_update_node` — so this is the CLI doing client-side what MCP does with
  a flag; `spec extract <source> --to-feature <fff> [--rule <rr>]` (legacy
  numbering only: it allocates under the source's module, so a source outside
  it is refused with a pointer to `spec new <loc>`)
  splits a sub-rule out of a fat parent into its own citation under another
  feature, piping the moved chunk in via `--content -`/`--content-file`,
  auto-wiring the cross-ref edge new→source (`--ref-label`), and reminding you
  to refresh both abstracts (`--strip-source` also trims the chunk out of the
  source body when it matches its STORED text verbatim: a chunk copied from
  `spec get`, which renders `{{…}}` placeholders, will not match a templated
  source, and the miss says so; trim by hand with `spec edit`, never
  `spec get | node update`, which deletes the placeholders); `spec link <from> <to>`
  cross-references one spec from another by their bare citations — a
  convention-aware `edge add` that validates both endpoints are specs in the
  same corpus and synthesizes the field→entity label when `--label` is omitted
  (`--dry-run` previews), and is safe to rerun: an edge already there under
  the same label is reported as `already linked` (`alreadyLinked: true`,
  its `edgeId`) and nothing is written; an end with only the `spec` tag (no spec role)
  is not governed: a node is governed when the base kind of its `role` (the part
  before the first dot) is `spec`, so `spec.rule` and `spec.rule.flow` count,
  `special` does not, and the tag alone never does (cor:acl:130:01). The edge is
  still made, and each such end gets one warning, on every path (created,
  `--dry-run` and `already linked` alike): text prints, after the result line,
  `warning: <loc> is not governed (the spec tag alone is not law); use hadron node update <urn> --role spec`
  (for an end with another governed role, `review`, the tail instead says that role is
  already set, the command is refused, and changing it needs deliberate review)
  (the `<urn>` shell-quoted; text neutralises control characters and cuts a warning
  at 256 characters), and `--json` carries the full text without the `warning: `
  prefix in `warnings`, a list of plain strings (not objects), one per ungoverned
  end, `[]` when both ends are governed (cli#1781); `spec find` is semantic by default (`--match-exactly`
  forces literal regex matching); `spec list --role <role>` forwards
  the value verbatim to the server role-family filter, with no local role matching,
  legacy-tag stream or fallback on a server without that filter. A supplied blank
  role is a usage error (exit 2), before any request. With no --role, the existing
  paged legacy-spec-tag / spec-role union is unchanged. --role retains memory,
  citation-prefix, approval/mint filters and the display page; --json shape is
  unchanged (cli#684 Slice A). `spec list`/`spec find` run unscoped when no
  memory is given (`-m`, `HADRON_SPEC_MEMORY`, `spec use`, then the active
  memory), and since citations and names collide across corpora, results
  spanning more than one memory add a `MEMORY` column — `--json` always carries
  both `memoryId` and the readable `memoryUrn`; `spec grep <pattern>` searches every spec's
  **body + abstract** across the whole corpus (one bulk read, not a per-spec
  loop) and prints each hit as `citation:line: text`, exhaustively — literal by
  default, `--regex` for RE2, `-i` to fold case, `--field content|abstract` to
  narrow; use it to discover where a token lives (it's deliberately broad — no
  word boundary); `spec replace <pattern> <replacement>` is the citation-aware
  bulk find/replace over bodies + abstracts, **word-boundary-aware by default**
  (whole-token only, so `h-read-node` never hits `h-read-nodes`;
  `--word-boundary=false` for substring, `--regex` for a pattern with `$1`
  backrefs), gated like other bulk writes (`--dry-run` previews per-citation
  counts, `--yes` non-interactively, `--max-specs N` caps blast radius) and
  **re-lints the changed specs** afterward. In a DRAFT corpus it uses the
  spec-only server door to rewrite governed specs and binds apply to the exact
  preview `plan`; a stale plan exits 5 and writes nothing. Draft JSON also
  reports `specsSkipped` and node-addressed `skips`. In a MINTED corpus the
  generic bulk door skips governed specs (cor:acl:130:02), so the report
  carries `specsInScope` / `specsGoverned` / `specsScanned` and a zero never
  reads as "not found" (#659) — change those with `spec grep` + `spec edit`;
  `spec supersede` prints its "update the register" reminder on stderr only when the
  corpus HAS a register node (a failed lookup after the write prints a note and never
  fails or retries the supersede); `spec register` labels a module's `:000` and a
  product's `:gen` as general provisions (`--json` adds `generalProvisions` per feature
  and top-level `productGeneral[]`; every existing key unchanged);
  `spec register` is advisory/read-only (`--check` reports
  ledger drift, exit 5; the ledger is the legacy numbering, and specs at any
  other loc are NAMED in `outsideNumbering`, never dropped); a spec `spec
  supersede` retired reads `(replaced by <loc>)` (or bare `(replaced)` when its
  `superseded-by` edge is missing, unreadable, in another memory or ambiguous;
  an identity is never guessed) in `spec list` and in the `rules:` line of
  `spec register`, where `--json` adds `replaced`/`replacedBy` per spec in
  `spec list` and `replacedRules[]` per feature in `spec register` (`[]` when
  none; every existing key unchanged). It is not the approval state SUPERSEDED,
  and no "withdrawn" state exists; `spec lint` is
  STRUCTURAL: since #708 its generic checks enforce no old content rubric (no missing
  abstract, "what invalidates", `data.version`, scaffold-body or
  placeholder-contract finding), because the sections a spec needs depend on
  its type — each corpus's own authoring framework (its review checks and
  review tasks) checks those. The platform-frame checks below additionally
  enforce that framework's structure and abstract bounds. It takes `--product`/`--module`/`--all`,
  warns (rule `abstract-length`) when a rule-tier abstract
  runs past ~1600 characters — a ceiling, not a target: retrieval is flat
  across ~700-1700 chars, and off-topic sentences dilute the embedding far
  more than length does. That finding always reports the HEADROOM left before
  the server's 2000-character hard cap (#539), and **escalates to an error with
  fewer than 150 chars of headroom left**. What is rejected there depends on the
  length: below the cap, an edit that grows the abstract past the remaining
  headroom; at exactly the cap, any edit that lengthens it (an equal-length
  rewrite still succeeds); past it (legacy data only), any update that REPLACES
  the abstract without bringing it to 2000 or fewer — an update that leaves the
  abstract alone, such as a body-only edit, still succeeds. Supersede now
  replaces the abstract too; use a shorter --summary for its retirement lead. Distilling is the wrong remedy in all three — on a
  spec whose sentences are all on-subject, cutting one drops a contract, so the
  fix is a supersede-level split. `spec lint --unwired` (opt-in, #687) is
  the repair report for specs whose edges were never written: for each
  legacy-grammar citation it reports, as `unwired` info (never failing
  `--strict`), each edge legacy `spec new` plans that the spec lacks (the
  table-of-contents edge to its parent, the inheritance edge to its contract,
  when that target exists), and the message carries the exact `spec link`
  that adds it (`edge create` when the target is not a spec); lint writes
  nothing. For `hadronmemory.com:specs`, `spec lint` additionally checks the
  platform frame; `--frame` opts another memory (or a memory id) into the same
  framework. It reads raw type templates from
  `hrn:mem:hadronmemory.com:specs-engineer-system` at invocation time. Template
  section order, optionality, defining caps and Built lines are not copied into
  the CLI. Missing, denied or malformed templates fail instead of reporting a
  clean partial check. Frame names/tags/sections/caps, abstracts and corpus
  wikilink labels produce errors (exit 5). Retired and pre-frame specs are
  exempt from structure checks only; corpus link checks still apply. Invalid
  type names are per-node findings and do not abort checks of the other nodes.
  This checks mechanics, not factual
  correctness or approval. `spec lint` also
  warns (rule `vector-index`) when the memory has no
  vector index so spec abstracts aren't embedded for semantic `find`
  (`--strict` promotes warnings to errors, exit 5). A corpus-scope lint also
  warns (rule `product-code`, once per code, #1147) when a product-rooted corpus
  uses a product code that is not 3 lowercase letters: automatic numbering
  (`spec new` without an explicit loc) cannot serve it, explicit locs still
  work. Two more warnings check
  the abstract against its body (#335, spec 032), which matters because the
  abstract is the RAG retrieval surface — a drifted one answers a semantic
  query authoritatively and wrongly: `abstract-stale` when the stored
  fingerprint disagrees with `sha256(content)[:8]`, meaning the body MOVED
  since the abstract was written (not that the abstract is wrong), and
  `abstract-unverified` when a node has an abstract AND a body but NO
  fingerprint — that abstract has never been checked against that body, so it
  reads as unverified rather than verified (server #1128). Both clear by
  re-saving the abstract. A null fingerprint is clean only when there is no
  abstract, or no content for one to describe. The legacy tier obligations —
  a missing parent (`parent-exists`), table-of-contents or inheritance edge, or
  an index body that doesn't cite its children (`index-incomplete`) — are
  REMOVED (#708): a spec at any loc, legacy-shaped or not, owes no parent or
  contract; `spec check-tools` scans the
  corpus for `hadron_*` tool references and flags any that aren't a real
  registered tool (checked against a manifest baked into the binary — the union
  of the MCP + runner tool registries — with a small ignore-list for known
  non-tools like the `hadron_token` cookie), exit 5 on findings so CI can gate on
  tool-name drift; `spec ledger` (#662) is a READ-ONLY cross-check of the
  hadron-concept decision registry file (`--registry`, default
  `design-discussions/_registry/decisions.md`) against the corpus: (1) decided
  entries from 2026-09-22 (by the date in the ID) with no `Corpus:` line, (2)
  `Codified` citations that do not resolve or are superseded (a `Retired` line
  whose every resolvable target is live; an unanchored relative `:NN`), (3)
  `D-YYYY-MM-DD-NNN|X` IDs the corpus cites that no entry has, with slash lists
  and `…`/`...`/`–` ranges expanded, (4) `Pending codification` entries as the
  work queue. Only registry IDs count: `DA`, `DU-1`, `rulings:NNNN` and R-codes
  never do. Anything it cannot read is a category-0 report line (unknown status or
  disposition, unexpandable shorthand, malformed or repeated heading, unreadable
  spec), never a guess. `--json` is an array of findings
  (`category`, `rule`, `severity`, `id`, `citation`, `locations`, `message`);
  exit 5 on any error-level line, 0 when only Pending remains, 4 for a memory
  with no visible specs, 2 for an unreadable registry file; `spec supersede` retires a
  spec (never renumbers), REQUIRES `--yes`, works on ANY spec (#708) —
  `--to <loc>` names the replacement's loc (free, any shape, no derived edges);
  without it only a legacy rule/flow gets an allocated number, and anything
  else is refused with a pointer to `--to` — and exits 5 if the old spec ends
  up with another successor (see "partial write" above); `spec import` is not yet
  implemented (exit 2).
- **A node's KIND decides which door writes it** (#606 → hadron-server#1201,
  shipped in #1203). Protection used to be by ADDRESS — `Memory.protectedLocs`,
  a per-memory list of loc patterns, with one generic `authorProtectedNode`
  exempt from all of them. **That column and that door are both GONE.** A write
  needs the door of EVERY governed kind it touches: what the node WILL BE, and
  for an update also what it IS now (before ∪ after). So REMOVING a kind
  (`isRunnable: false` on a task, a role change away from `spec`/`spec.*` or
  `review`/`review.*`) needs
  that kind's door too, and a write touching two kinds has no door at all (the
  CLI refuses it, exit 2):

  **A zero-byte `--content-file` (or empty piped `--content-file -`) is refused
  (exit 2) by `node add` / `node update`** (#1035): a failed `generate >
  body.md` leaves exactly that file, and an empty body is a real clear on the
  wire. The refusal names the file and is sent before any request. Pass
  `--allow-empty` when an empty body is deliberate (one empty-body mutation).
  Whitespace-only files, `--content ""` and `--content -` are unchanged;
  `--allow-empty` beside a non-empty file is a no-op.
  `spec edit` has the same refusal (#1092) for a zero-byte `--content-file`
  and empty piped `--content -`, raised before any request; `--allow-empty`
  opts in. `--abstract-file`/`--description-file` are not guarded.

  | the write touches | door |
  | --- | --- |
  | `role: "spec"` or dotted `spec.*` | `createSpecNode` / `updateSpecNode` |
  | `role: "review"` or dotted `review.*` | `createReviewNode` / `updateReviewNode` |
  | `isRunnable: true` | `createTaskNode` / `updateTaskNode` |

  A generic `createNode` / `updateNode` / `hadron_create_node` that would
  produce one is REFUSED and told which door to use — and the UPDATE half is
  not cosmetic: editing a node that already carries a governed role still
  produces one, so a governed node is not rewritable through the generic
  surface at all. **Use `hadron spec new` / `hadron coding review create`; do
  not hand-roll the node.**

  **The three are NOT equally strong**, and treating them alike is the mistake
  to avoid. **Spec is DILIGENCE** — it attests the spec tool authored the node,
  not that nobody else could; the label is free to omit and a citation resolves
  without it, so do not read it as making a corpus tamper-evident. **Review is
  SECURITY**: any work can be reviewed and a review may be a safety check, so
  removing one is the attack. **Task is CAPABILITY** and the only gate that
  cannot be dodged by omission, because it keys on `isRunnable` rather than on a
  label — drop the capability and the node does not run.

  **`node add --role` / `node update --role` set `Node.role`** and the command
  routes the write through that kind's door for you — so `hadron node update
  <ref> --role spec` is how an existing node becomes governed. Omit the flag to
  PRESERVE the stored role; an empty or whitespace-only `--role` clears it.
  Updates send literal null through the current ∪ resulting kind's door;
  creates omit the blank role. This also avoids writing empty strings on older
  servers. Governed refusals stay fail-closed, with no generic retry. The clear
  and other field edits share one mutation; --data-merge is refused with a role
  clear (use --data for atomic replacement). Nonblank roles remain verbatim.
  The flag is NOT
  `--type` (the platform kind) and NOT the `--role` on `memory member` /
  `memory share`, which is a MEMBERSHIP role on a person. `node get` and
  `node list` PROJECT it, and `--json` renders `"role": null` for an ungoverned
  node rather than dropping the key — null is the answer "ungoverned", not the
  absence of one.

  **`coding preflight create` writes through the GENERIC surface**, and that is
  correct rather than an exemption: its route targets are non-runnable
  orientation nodes carrying no role, so they are of no governed kind. Nothing
  is protected by address any more.

  **These commands do not upsert.** A create against a loc that already holds a
  live node is REFUSED, not overwritten — a written citation is never
  overwritten. The per-kind
  doors do not even offer an `upsert` argument, so this is now the server's
  decision rather than a convention each call site keeps. Re-running is not a
  repair: edit with `spec edit`, or supersede. The refusal is a
  NodeLocConflictError, exit 5.
  **Draft corpora and per-node minting (hadron-server#1447/#1591).** Draft
  rights are PER NODE since hadron-server#1591: every new node starts unminted,
  with draft rights, in any memory, until its first mint; `corpusState` is
  derived (DRAFT while no live node is minted). `memory set --draft-corpus` is
  accepted for compatibility and ignored by the server (still create-only and
  free-standing only; exit 2 on an update or with `--app/--agent`, nothing
  written); a create echoes the `corpusState` it read back.
  `spec describe` reports `corpusState` (`DRAFT` | `MINTED`, plus `mintedAt`
  for a former draft; a present `null` when the server predates draft
  corpora and cannot say). It also lists reserved placeholder citations and
  `placeholderCount` separately from the total `specs`; if the older server
  lacks the placeholder marker, those fields are omitted. In any memory:
  `spec reserve <citation> [--name]` creates a server-marked placeholder spec
  (`{memory,id,loc,name,role,placeholder}`) — write it with `spec edit`;
  `spec renumber <from> <to> [--dry-run]` moves an unminted spec and its
  subtree (a minted node in it refuses `NODE_MINTED`, exit 5) and
  rewrites every node URN in the corpus naming them
  (`{memory,from,to,dryRun,moved[],rewrites[],textCitations[],failed}`) —
  a citation written only as TEXT (a link label, a name) is reported in
  `textCitations`, NEVER rewritten, and any `FAILED` rewrite leaves the move
  standing and exits 1; `spec backlinks <citation>` lists what refers to a
  spec (`references[]` of `{kind: URN|EDGE, field, sourceLoc, sourceNodeId,
  targetLoc, text}`, exact citation only); `spec unresolved` lists references
  that reach no written spec (`reason: MISSING|PLACEHOLDER`), exit 0 either
  way. Backlinks/unresolved and draft-lint reference enrichment drain 20-row
  pages by served count/hasMore before rendering or deciding; provider order
  and duplicate rows survive. A failed/malformed/later-refused page fails with
  no partial output or all-clear. Only an exact first-request unknown-page-field
  validation permits one full legacy read; never on auth/transport/5xx or a later
  page. If the legacy unresolved report is also unsupported, draft lint keeps
  ordinary rules only. Paging bounds responses, not whole-corpus scan work;
  totals defend against some changes, not same-count edits or a snapshot.
  `spec document <citation> [-m] [-o <file>]` prints a feature's specs as
  ONE document the SERVER assembles (cor:spc:030:02; read-only, no MCP tool):
  stdout is the server's Markdown byte for byte (a newline added only on a
  terminal); `-o <file>` writes exactly those bytes atomically after the fetch
  and prints one `✓ wrote <citation> to <file> (<n> bytes)` line to stderr;
  `--json` is `{loc,memoryId,renderedAt,markdown,entries[{loc,urn,name,
  revision,content,isPlaceholder,approvalStatus{state,approval},mintStatus}]}`
  with entries in the server's document order; `--json` with `-o <file>` is
  exit 2 before any request. A missing or unreadable memory is one answer
  (exit 4), no live spec at the citation is exit 4, an encrypted memory exit
  5, a feature over the server's cap exit 2 (never a partial document), and a
  server without the query exit 7 with no fallback to `spec get --prefix`.
  `spec mint [--dry-run] [--yes]` mints every approved, unminted node of the memory, not only specs (cor:dmo:120:06). It always checks first and prints the report
  (`{memory,dryRun,minted,mintedCount,mintLocs[],blocked,blockers[],staleAbstracts[],staleAbstractsBlock,openQuestions[{decider,questions[{loc,question}]}]}`,
  `decider` "" = unassigned). Blockers include placeholders, unresolved
  references, structural lint errors, NOT_APPROVED current revisions,
  TAMPERED approvals, and CITES_UNMINTED cross-memory citations. Stale abstracts block only when
  `staleAbstractsBlock` is true; open questions never block. Blocked → exit 5
  and the real mint is never sent; `--dry-run` stops after the report (exit 5
  if blocked); otherwise it asks on a TTY, and non-interactively REQUIRES
  `--yes` (exit 2); an explicitly blank `-m` is refused (exit 2) rather than
  falling back to the ambient memory. With `--json` on a terminal the report
  goes to stderr before the prompt and stdout carries only the final
  document. `node mint -m <memory>` uses the same per-node server gate and
  report. Minting records state but currently enforces no citation permanence.
  A minted node cannot be renumbered (NODE_MINTED, exit 5); draft-only
  reserve and reference operations can refuse SPEC_CORPUS_NOT_DRAFT. An encrypted corpus refuses the
  scanning ones (SPEC_CORPUS_ENCRYPTED_UNSUPPORTED, exit 5). In a draft the
  READ commands know it too: `spec list` / `spec get` mark a reserved spec
  `"placeholder": true` (omitted otherwise; `[placeholder]` in the table), a
  placeholder's lint is the single `placeholder` warning, and `spec lint`
  reports each `placeholder` and each in-scope `unresolved-reference` as a
  WARNING at the citing spec (`--strict` escalates them; `spec mint` is the
  gate) — a minted corpus or an older server costs one state read and is
  otherwise untouched; a mint racing a new blocker
  (SPEC_CORPUS_MINT_BLOCKED) or a write in flight (SPEC_CORPUS_BUSY,
  retryable) is exit 5 too.
- `ai-config list --owner-type <type> --owner-id <ref>` reaches the uniform
  masked owner list, including disabled configs; `--all-owners` asks for the
  server's platform ADMIN/OWNER cross-owner view. These are explicit modes:
  global `--app` never supplies the owner filter. JSON is `{items:[],total:N}`,
  one name-ascending page (id tiebreak); `--limit` defaults server-side to 20,
  cap 200, 0 counts only; `--offset` defaults to 0. Increment offset by returned
  item count to continue. Server permissions are authoritative.
  `ai-config test <id>` makes one server-side saved-config test; the stored key
  stays on the server. `--override-file <path|->` reads edited provider/model/
  apiKey/endpoint/apiStyle/params, never saves or echoes them. Omitted fields
  use saved values; null endpoint/apiStyle restores the provider default, null
  params clears edited params. `-` is piped JSON (TTY refused, exit 2).
  Provider-reaching tests are metered and may cost money; NO_API_KEY and
  KEY_UNREADABLE make no provider call. The completed diagnostic exits 0 even
  for `ok:false`; inspect `ok`/`error.code` in JSON (with effective values,
  latency, value sources and knobs). Request refusals use normal CLI exits.
  `--chat-probe` (cli#1951, hadron-server#4005; off by default) adds ONE second,
  separately metered provider call: its closed status (PASS, FAIL_TEXT_ONLY,
  FAIL_OTHER_TOOL, FAIL_MULTIPLE_CALLS, FAIL_MALFORMED,
  FAIL_PROVIDER_UNAVAILABLE, UNSUPPORTED_PROVIDER; an unknown value passes
  through verbatim) prints on its own `chat:` line and is the `chat` key in
  `--json` (always present; null when not requested). It is a separate outcome
  from `ok`, and neither stands for the other. A completed test exits 0
  whatever `ok` and the chat status say (cli#1959). Request refusals, invalid
  server responses, argument, override-file and transport failures use normal
  CLI error exits. Default requests omit `chatProbe` and select no `chat`, so
  older servers work unchanged; with the flag an older server is refused
  (GRAPHQL_VALIDATION_FAILED, exit 2) and nothing is retried.
  The submitted override key is redacted locally from request errors and every
  completed-test string value (including reply, knob detail and value sources),
  in literal and string-escaped spellings. This does not change JSON keys/nulls,
  the sent override, the chat-probe wire, or a request refusal's exit code.
  Masking is by exact value in every rendered string, so an unusually short or
  common override key can also mask legitimate substrings of the provider,
  model, value-source, knob and status values. That is a presentation tradeoff
  of the any-field guarantee, not a defect: there is no length or word-boundary
  exemption, because one would let the key through on some surface.
  The server must still keep keys out of its responses; this local redaction is
  defence in depth for the submitted override, not a substitute for that rule.
  When a nonempty override key is supplied, every mapped request error is
  detached from its original chain, including errors with a clean message but
  key-bearing metadata. Numeric code, message and supported structured
  metadata (such as internal-fault requestId/serverCode) are retained with
  string values redacted. With no nonempty override key, errors are unchanged.
  `create --api-style` (also file apiStyle) and `update --api-style` set
  CHAT_COMPLETIONS or RESPONSES; `update --clear-api-style` sends null to use
  the provider default; omit both update flags to preserve the stored style.
  Only the server decides which providers accept styles.
- `ai-config list` lists the masked AI configs *resolvable* in an App's chat
  context (App→Agent→Org→HadronServer, innermost wins, enabled-only) — never
  key material, only a preview. `ai-config create|update|rm` manage the
  underlying provider configs: `create` needs an owner (exactly one of
  `--app`/`--agent`/`--org`, ID or URN) plus `--name`/`--provider`/`--model`;
  the API key is a secret read via `--api-key -` (stdin) and never echoed back.
  App, Agent, and Organization configs require a stored key on current servers.
  `--file <path>` (or `--file -` for stdin) reads the whole config — key
  included — from a JSON object (keys mirror the flags: app/agent/org, name,
  provider, model, endpoint, apiStyle, apiKey, params, enabled), keeping the secret out of argv;
  an explicit flag overrides the matching file field. `--file -` is for a PIPE
  and is refused (exit 2) from an interactive terminal (#648); `--api-key -` is
  a secret, not a document, and deliberately still reads from a terminal.
  `update <id>` changes only the fields you pass — `--api-key ""` clears the
  key, omitting it keeps it; `--param k=v` (repeatable) replaces the params
  object. `--endpoint <url>` stores a provider base URL; on update,
  `--endpoint ""` clears the override and omission preserves it. `get <id>`
  and `list --with-endpoints` show both the stored `endpoint` and the server's
  `effectiveEndpoint`; null stored means the provider default. `endpoints
  <provider>` returns the server-owned suggestions with billing and terms
  notes. These endpoint-aware commands require a newer server, while the
  original list/create/update paths remain compatible with older servers.
  `rm <id>` requires `--yes` non-interactively.
- `secret create|list|rm` manages the general owner-scoped secret store. Values
  are write-only: `create` reads the secret material from stdin, a file, or an
  interactive no-echo prompt (never argv), and `list` prints only the inspectable
  half (`name`, `kind`, `metadata`, audit fields). `--scope user` may omit
  `--owner` to mean the caller; org scope requires `--owner`.
  `webfetch-auth` secrets use `--type bearer|basic|header` plus `--url-prefix`;
  the server derives `metadata.type`. `rm <id>` requires `--yes`
  non-interactively.
  App secrets (cli#2013, server#3997): `--scope app --owner <app ref>` on
  `create|list`; `secret set <ref> --value-file -|@file` is the value path (the
  server overwrites an org/user secret and ADDS a version to an App secret;
  prints `currentVersion`, `-`/null for org and user secrets; `update
  --value-file` is the same operation). `secret versions <ref>` lists the KEPT
  versions newest first (metadata only; `--json` {id,name,currentVersion,
  protected,total,hasMore,versions[]}); `secret events <ref>` the audit events
  (`--json` {total,events[]}); both pass `--limit/--offset` through, `--all`
  walks pages, and an unset flag is omitted. An excluded caller's events page
  is empty, printed as empty (never "forbidden"). `protected` is display only: a
  change to a protected secret is staged for a passkey (cli#2738, see the
  `secret protect` / `secret change` entry above). No CLI-side size/count checks.
  A run never resolves an App secret.
- `org` manages organizations, their members, and invitations. `org list`
  lists organizations (`--mine` restricts to your memberships; unscoped spans
  every org you can see); `org create --name --urn`, `org get <id>`,
  `org update <id> [--name|--urn|--marketplace]` (`--marketplace` toggles the
  org's marketplace-catalogue listing — the field formerly called `isVisible`),
  `org rm <id>` (requires `--yes`). `org public <org-ref>` fetches the sanitized,
  member-free **discoverable** view of an org — its `publicMemories` /
  `publicAgents` (each a `{id,name,urn,description}` ref) plus
  `listedOnMarketplace` — or a not-found when the org isn't discoverable (the
  server collapses not-found and not-discoverable for anti-enumeration).
  Each public list is one page: `--limit` defaults to 20 (server cap 200),
  `--offset` defaults to 0; both flags apply to both lists. Zero limit returns
  counts only; negative values count as zero. Pages are not auto-drained.
  Text labels a list that is not whole `showing N of TOTAL` (plus `(offset O)` whenever `--offset` was given, with the offset sent: 0 for an explicit 0 or a negative one): more rows follow, a short last page, an offset past the end, or a zero limit (cli#2248); `(none)` only when the list holds nothing (total 0). JSON preserves the two arrays
  and adds `publicMemoriesTotal`, `publicMemoriesHasMore`, `publicAgentsTotal`
  and `publicAgentsHasMore`, including when counts are zero or hasMore is false.
  `org member list <org-id>` lists members; `member add|set-role <org-id> --user
  <id> --role <OWNER|ADMIN|CONTRIBUTOR|READER>` and `member rm <org-id> --user
  <id>` manage them. `org invite create <email> --org <id> --role <r>` mints an
  invitation whose returned `slug` is the acceptance token — the invitee redeems
  it with `org invite accept <slug>`; `org invite show <slug>` shows only
  public, slug-holder details: role, activation count/limit, expiry, sender
  display name/GitHub username, and organization name. It does not return the
  invitee's email or other identity fields. Its `--json` shape is
  `{slug, memberRole, maxActivations, activationCount, expiresAt,
  sender:{name,githubUsername}|null, organization:{name}|null}`; `slug` comes
  from the argument so it remains usable with `org invite accept`. The create
  result remains the full invitation for its authorized creator. For creation,
  `--expires-days` takes 1-10 (server default 10 days) and `--max-activations`
  takes 2-1000 (server default 2 activations). Unset bounds are omitted; explicit
  out-of-range values are passed through for BAD_USER_INPUT (exit 2).
- `agent` manages agents (user- or org-owned; an App runs an agent). `agent list
  [--org <id> | --owned-by-me] [--type ASSISTANT|CHATBOT] [--visibility
  ORGANIZATION|PERSONAL|PUBLIC]` is the member-scoped view (agents in your
  orgs); `--owned-by-me` narrows it to your own org-less agents (see the owner
  slice above — rejected with `--org`, and with `--public`). `agent list
  --public [--type <t>]` is the separate cross-org marketplace slice — every
  live PUBLIC agent, readable without org membership, so you can grab a foreign
  agent's URN to subscribe/install (`--org`/`--visibility` don't apply to it).
  `agent get <ref>` (ID or URN); `agent create --name <n>` creates a
  user-owned agent — pass `--owner-me` to say so explicitly, or `--org <id>`
  for an org-owned agent (the two are mutually exclusive). A user-owned agent is
  PERSONAL/owner-only in v1 (spec 047; the server derives the `@handle:<slug>`
  URN and rejects a non-PERSONAL visibility). A new agent is in NO App's cast
  pool, so it cannot be cast as a worker until it is installed somewhere —
  `agent create --install-into <app>` does that second step in the same run
  (#535), and without it the create notes on **stderr** that the agent is not
  yet installed. It is deliberately not spelled `--app`: that is the persistent
  App-CONTEXT flag, which never acts on an App. The two calls are NOT atomic —
  if the install fails the agent still exists, and the error says so and prints
  the `app agent add` that finishes it rather than rolling the agent back. A
  LOST ANSWER (exit 7, #394) is reported as `unknown`, not `failed` — the
  install may have committed, so the remedy is to check `app agent list` before
  retrying rather than to retry into a duplicate. In `--json` the create gains
  an `install` object **only** when `--install-into` is passed, so the existing
  shape is unchanged otherwise: `appRef` (what you passed, an ID or a URN),
  `status` (`installed`/`failed`/`unknown`), `error` on the latter two, and the
  server-resolved `appId`/`appUrn` on success only. Optional
  `--type`/`--visibility`/`--description`/`--system-memory`/`--surface`
  (repeatable) and `--persona-role` configure metadata. A new Agent starts
  without a prompt: write `boot:source` (role `boot-prompt`) in its system
  memory, then run `hadron agent compile` and `hadron node approve`.
  Retired prompt flags do not write prompts. Agent JSON omits the retired
  literal prompt fields. `agent update <ref> [<field flags>]`
  changes only the fields you pass (`--surface` replaces the set); `agent rm <ref>`
  requires `--yes`. `<ref>` is an agent ID **or** a fully-qualified URN
  (`hrn:agent:acme.com:support-bot`) — no need to resolve an ID first.
  `agent create|update --member-roles <role>[,<role>…]` (cli#1226, R211,
  `cor:dmo:050:04`) sets the roles the Agent offers an App's members: omitted
  sends NO `memberRoles` key (create: the server default `owner,member`; update:
  unchanged), an explicit empty value exits 2 with nothing sent, and role
  validity (format, duplicates, case clashes, count) is the server's — its
  `BAD_USER_INPUT` text is printed unchanged. `agent get` shows them as
  `member roles:` and `--json` `memberRoles` (`[]` when none returned; the same
  key now appears on every agent DTO). Memory-attach, AI-config wiring, and app-wiring land next.
- `app user create --external-id <id> [--handle <h>] [--name <n>]` (cli#1627,
  `createAppUser`): vouches for a user — the server creates or returns the user
  the App knows by that external id (same id, same user id). **App key callers
  only**: with a personal token the server's refusal is passed through typed
  (nonzero exit, the server's code; nothing is checked first). The response does
  not say created-vs-existing, so neither text nor `--json` claims it; `--json`
  is `{id, handle, name, urn, externalId}` (null = not returned). `--handle` and
  `--name` are unsent when omitted (an explicitly empty one, or an
  `--external-id` with surrounding whitespace, is a usage error); there is no `--email` (the server accepts
  only empty values). The credential is never printed. Session start/end for the
  user is out of scope.
- `app member add --user <ref> --role <role>` / `remove --user <ref>` (both take
  `[--instruction <kind>:<ref>]`, see Instructed staffing and member acts) / `list`
  (`ensureAppMember` / `removeAppMember` / `App.members`, `cor:dmo:050:04`)
  manage an App's HUMAN members. The App is the root `--app` flag, else the
  active App; there is no positional App, and an explicit blank `--app ""`
  refuses (exit 2) rather than falling back to the active App. `--user` is an
  id, email, handle or `hrn:user:` URN passed to the server UNCHANGED — never
  resolved or trimmed client-side. `--role` is required on `add` (no default)
  and is a server-owned string, never checked locally: `app member roles`
  prints the roles the App offers (the union of its installed Agents' member
  roles, `cor:dmo:050:04`; change one with `agent update --member-roles`), and
  an invalid role is refused with the server's own list. `add` is an idempotent upsert (the same
  role repeats; a different role changes the membership) and does no pre-read,
  so EVERY `add` and `remove` is confirmed (the prompt names the App and whether
  it came from `--app` or the active App; remove is reversible, so it says nothing
  of "cannot be undone"): `--yes` skips the prompt, a
  non-interactive call without it exits 2 with ZERO requests sent, and a
  declined/EOF prompt exits 6. `remove` succeeds whether or not a row existed,
  so `--json` reports `status: "absent"` (the state now), never "removed"; a
  non-manager may remove only themselves, and any other target gets the
  server's `Forbidden:` text as-is (no "user not found" — that would be an
  existence oracle). A definite refusal keeps the server text and exits by code:
  `InvalidRoleError` 2, `MaxMembersExceededError` and `OrphanAppError` 5,
  not-found 4, auth 3, and the plain-text `Forbidden:` 1. ANY other failure of a write, or any result returned next to an error,
  — transport error, `data` plus `errors`, a masked internal error, an empty
  payload — exits 7 with "outcome unknown", prints no success DTO, and is never
  retried: `ensureAppMember` re-reads the row after commit, so the write may
  have landed; check with `app member list --app <the App you targeted>`. `list --json` is
  `{appId, appUrn, members: [{user: {id, handle, urn}, role, createdAt}]}`
  with `members: []` (never null) when empty. The server returns `[]` when the
  member list is unreadable by you, so an empty list means "no members visible
  to you", NOT "no members"; a null App is exit 4, a malformed row is an error.
  User names and emails are withheld by the server and not selected.
- `app join <app-ref>` / `app leave <app-ref> [--yes] [--instruction <kind>:<ref>]`
  (`joinApp` / `leaveApp`, cli#1625) are the signed-in user's OWN membership doors;
  the App is positional (ID or URN, passed unchanged), unlike `app member`. Self-join
  is closed unless the App's primary Agent declares `selfJoinRole` (#1714); the CLI
  decides no policy. `join` prints the member row (`--json`: `app`, `appId`, `appUrn`,
  `appName`, `user`, `role`, `createdAt`, `isOrgExternal`) and exits 0 on a repeat with
  the same row: `JoinAppPayload` does not say whether the call created it, so the
  command makes no "already a member" claim and infers none from `createdAt`. `leave` is
  confirmed: the personal memory in the App is kept and re-attaches if you can join
  again. Rejoining is subject to self-join policy and capacity. `--yes` skips the
  prompt; a non-interactive call without it exits 2 with zero
  requests sent. It is idempotent server-side, so success means "not a member now"
  (`--json` `status: "absent"`), and `--instruction` takes the same parser and
  document switch as `app member remove`. Every refusal is the server's code and
  sentence, unchanged, nonzero, never "outcome unknown": `SelfJoinNotAllowedError`,
  `MaxMembersExceededError`, `AppNotFoundError`, `AppUninstalledError`,
  `OrphanAppError`, `InvalidRoleError`, `UNAUTHENTICATED`, and on `leave` the
  FORBIDDEN for a worker session with neither an instruction nor its own
  `app.members` grant (hadron-server#3342) and ruling 0082 (A)'s impersonation,
  App-run and unattributable-session refusals.
- `automation list|approval|approve|decline` (`automationApprovals` /
  `automationApproval` / `approveAutomation` / `declineAutomation`,
  `cor:agt:010:01`, hadron-server#2441) let an App owner review and approve the
  schedules and webhooks of an App with no row id and no hash. The App is `--app`,
  else the active App (an explicit blank `--app ""` refuses, exit 2). A trigger is
  named `schedule/<name>`, `webhook/<name>`, or a bare `<name>` that exactly one
  trigger of the App has; nothing is ever picked for you: no match exits 4 (one
  line, no dump of other automations), several exit 2 listing only the matches.
  A MANUAL origin is `--run <run-id>` (the id the refused run printed), exclusive
  with a name, never guessed or listed. **`list`** follows `nextCursor` to the end
  in the server's order; rows are `UNADOPTED`/`ADOPTED` only, printed "not
  approved"/"approved" — **never** "up to date": CHANGED exists on `approval`
  alone. `--json` is `{app, rows: [{triggerKind, triggerRef, name, state,
  headline}], omitted: [{triggerKind, reason}]}`, both arrays `[]` never null;
  MANUAL is always `omitted` ("Manual runs are approved from the run that needs
  them"). **`approval`** prints the server's plain summary; `--depth` adds the
  graph and the hash; `--json` is `{app, trigger, state, hash, summary: {name,
  schedule, reads, writes, tools, limits, cannotDo, flows}, depth?}`. **`approve`**
  fetches ONE projection, shows it on stderr (also under `--yes`: `--yes` skips the
  prompt, never the display), confirms, and sends the hash it just showed;
  `AUTOMATION_CHANGED` exits 5 and is never retried — run `approval` again.
  **`decline`** sends kind and ref only (no hash: it only removes authority) and
  never reads the projection, so an unreadable automation can still be declined;
  the prompt says an approved automation is switched off ("its next run will fail
  until it is approved again", including a newer approval someone else just gave)
  and that an unapproved one only records the decline. Display and prompt go to
  stderr; stdout carries only the result (`{app, trigger, status:
  "approved"|"declined", hash?}` under `--json`). A "no"/EOF exits 6 and a
  non-interactive call without `--yes` exits 2, both with ZERO mutations. Server
  text is stripped of terminal escapes, control and bidi/zero-width characters and
  bounded in text mode; `--json` passes it through unchanged. An empty list in the
  projection prints "none listed" (what the server listed, not a guarantee).
  `approve` refuses (exit 7, nothing sent) when the summary cannot be shown in
  full (a fact over 1000 characters or a list over 200 entries): read all of it
  with `approval --json`. A failed write of the display or of the confirmation
  prompt (stderr closed or full) stops before any mutation, `--yes` or not. A `--run` prompt says the run is
  found by run id only: the server takes no App for it, so the CLI does not claim
  one. Recovery hints never carry a name that sanitizing would change. Exit codes: `FORBIDDEN` (including the read-access
  refusals) 8, `NOT_FOUND` 4, `AUTOMATION_CHANGED` and `AUTOMATION_UNADOPTED` 5,
  bad selection 2, cancelled 6, and 7 for a malformed projection (a null required
  list, an empty hash that is not the stale-entry shape below, unknown state — never an empty success), a server without these
  operations ("this server does not support automation approval", before any
  prompt), or a write that is not confirmed (outcome unknown: check with
  `automation approval`). `previewAutomation`/`reAdoptAutomation` stay unbound.
  **Stale entry (cli#1202).** When the automation's entry node no longer resolves
  the server answers hash `""` with ONE limits sentence (hadron-server#2503). The
  CLI admits that answer by STRUCTURE only (state UNADOPTED, non-empty name, flows/
  reads/writes/tools/cannotDo present and empty, no schedule, exactly one non-empty
  limit; with `--depth`, depth exactly `{}`) — never by the sentence's wording.
  `approval` shows it (exit 0; `--json` is the usual DTO with `hash:""` and the one
  limit); `approve` (with or without `--yes`) shows it on stderr, never prompts,
  sends no `approveAutomation`, and exits 5 with "nothing was approved: this
  automation's entry node no longer resolves; set the trigger to the entry node's
  current URN, then run `hadron automation approval <ref>` again". `decline` is
  unchanged. Any other empty-hash answer stays exit 7.
- `app list` and both `app install` receipts keep `memberCount` present:
  JSON null means withheld, 0 means known-empty; no count column is added.
- `app agent list [<app-ref>]` returns JSON null (exit 0) when either the
  installation roster or Agent details list is withheld; human output says
  "roster withheld (not visible to you)". Known-empty stays [] and
  "0 readable agents". When appAgents is known, count its install entries
  (including derived), and count null joined Agents as withheld installs:
  partial text says "N installed (M not visible to you)". The JSON array
  still contains readable details only, never a complete-install count.
  `team role list` suppresses roster-based divergence warnings for withheld
  or partial rosters. This consumes provider nulls, not client authorization.
  The command lists readable installed Agents (including
  direct-import derived installs). JSON adds nullable `installMethod` (`ROW` or
  `DERIVED`) and nullable `importer` (`{id,urn,name}`) to each existing row.
  Human INSTALL shows `installed` or `derived`, with IMPORTER name and URN for
  the latter. When any installed Agent is unreadable, the server withholds
  both fields on every entry: explicit JSON nulls, no human install mark (? = not available).
  Never infer a row install from null. Requires server #3446's fields; older
  providers refuse the operation. Casting and install mutations are unchanged.
- `app agent add <app> <agent>` / `remove <app> <agent>` wrap
  `installAgentIntoApp` / `uninstallAgentFromApp` — the AppAgent join
  (`cor:dmo:050:03`), and the only way to attach an Agent to an App you
  ALREADY have (`app install` creates a new App from an Agent). `add` is what
  makes the retention promise reachable: an uninstalled Agent's
  per-(App, Agent) memories persist as orphans and become readable again on
  reinstall, so `remove` is reversible. `remove` is idempotent server-side and
  still prompts (`--yes` non-interactively), because it changes who is on a
  roster. `--training-mode` on `add` is PER-APP (spec 023 FR-001) — it is
  omitted unless passed, since sending it would re-set the flag for every
  installed Agent. Authorization is CONTRIBUTOR+ on the App's owning org (or
  ownership of a user-owned App), deliberately NOT plain App membership, since
  attaching an Agent grants read access to its design; a FORBIDDEN from these
  two commands says so rather than printing one word.
- `team` (#369, Worker model since #428 / hadron-server#974) coordinates a
  team of humans and AI agents. A team IS an App (`cor:agt:020:01`): the
  installed agents are its cast pool, and the staff are **workers** — a
  worker is the NAMED CASTING of an installed agent into the App
  (`cor:dmo:050:11`): "Iris" is the backend-engineer agent cast into the
  eng-team App, re-driven across many sessions by the same or a different
  human. The agent carries `personaRole` metadata and a compiled boot-prompt
  pointer; the name lives on the Worker. Commands take the worker's
  name (resolved within the App from `--app`, the App context, or the binding), its URN, or its
  id. A NAME with no App scope at all — outside a worktree, no `--app`, no
  App context — refuses **exit 4** naming the remedy (`#464`); before that it
  leaked the raw wire error, which named neither what you typed nor the fix.
  The URN — `hrn:worker:<root>:<app-slug>:<slug>` (hadron-server#991) —
  keys on a permanent DERIVED slug, never on the name (two names may legally
  slugify to one token, so a name-keyed URN would not resolve to exactly one
  casting — `cor:agt:020:02`); it needs no App context, which makes it the
  portable ref for scripts, and it is null only when the App's URN predates
  the flat grammar-v2 arity. `--json` carries `urn` and `slug` alongside the
  worker's id.
  **`portalUrl` is the link that opens the worker in the portal** (#510,
  `cor:api:230:01`) — SERVER-BUILT, and the one to COPY when signing anything
  published outside the team rather than assembling `<origin>/app/u/<urn>`
  yourself, which is the composition the field exists to remove: the canonical
  spelling, the resolver route and the deployment origin are all the
  platform's. `worker get` prints it as a `URL:` line under `urn:` — the
  framing the MCP node read already uses — and `session start`'s receipt
  carries it at the bind, matching `hadron_start_session`. `hadron node get`
  carries the same `urn:`/`URL:` pair since #515, so the copy-the-URL-line
  instruction is satisfiable on every read that prints a URN. It is **nullable, for two unrelated
  reasons** (the deployment has no usable web origin, or there is no URN to
  build one from) and both render as NOTHING: no line, no placeholder, and
  never a locally-composed fallback, since a link to a guessed origin fails
  silently for whoever clicks it. Under `--json` the key is present as `null`
  rather than omitted, so "this deployment emits no links" is
  distinguishable from an older CLI that does not know the key — **unlike a
  NODE's `portalUrl`, which is `omitempty` and whose key vanishes instead. Do
  not reuse one null-check across the two.** The `urn`
  beside it is unaffected — losing the link never costs you the ref. Carried
  on BOTH `worker get --json` and `worker list --json`; the roster TABLE
  deliberately has no URL column, since a full URL per row is wide.
  **`repos` is the ROLE's repo affinity, resolved onto the worker** (#456,
  hadron-server#1024) — a SOFT signal, carried on both worker surfaces. At
  `session start`, a `--repo` outside it prints a warning to **stderr** (after
  the boot briefing, where it can still be read) naming the mismatch and, when
  exactly one non-retired teammate claims that repo, suggesting them. It is
  **never a refusal**: the exit code is unchanged, the session starts and the
  binding is written, because cross-repo work is legitimate — a coordinator
  does it by definition and sibling repos share code. The warning prints under
  `--json` too, on stderr, so it never enters the parsed document.
  **EMPTY MEANS "NEVER WARN", and it is the answer to every uncertainty**: no
  role, a role with no definition, an unreadable system memory, and a denied
  read all return `[]`. So there is no unconfigured-vs-spans-everything
  distinction to draw — both mean do-not-warn, and telling them apart would
  make an unconfigured team look misconfigured. Renders as `[]`, never `null`.
  **`worker list --json` does NOT carry `prompt`** (#459): the
  resolved briefing is multi-KB per worker and a roster read is not where you
  want it, so the key is OMITTED — not nulled, because a null would hand a
  reader who wanted the briefing nothing while looking like an answer.
  `worker get <ref>` is the prompt surface and is unchanged. This is a
  deliberate breaking change to a documented `--json` key, taken while the
  surface is young. The retired prompt override key is also omitted.
  `team worker cast` mints one in ONE platform call (`castWorker`): the
  server resolves the agent (`--agent`, which must name an agent installed
  in THIS App — `WORKER_AGENT_NOT_INSTALLED` **exit 2, not 4**: the ref
  resolves and it is the install that is missing, so the caller fixes it by
  passing an installed ref; or `--role` picks the single
  installed agent whose persona role matches — `WORKER_AGENT_NOT_FOUND`
  exit 4 / `WORKER_AGENT_AMBIGUOUS` exit 2, never a guess), takes the name,
  binds the template, provisions the worker's working memory, and returns
  the resolved boot briefing (`prompt`), printed on success.
  **`--name` IS REQUIRED** (hadron-server#1050): a name is permanent for the
  App (`cor:agt:020:02`), so it is chosen and never derived — the server
  will not invent a permanent identifier nobody picked. The CLI refuses a
  nameless cast with exit 2 and the remedy; the server's own
  `WORKER_NAME_REQUIRED` also maps to exit 2. The claim is ONE attempt and
  `WORKER_NAME_TAKEN` (exit 5) is the answer, not a retry — `worker list
  --include-retired` shows what is taken, and the `--include-retired` is
  load-bearing: retired workers keep their names forever, so the default
  listing under-reports it. A bare `--role` used to allocate from the role's
  cast-list register; **that register is gone**, so it no longer casts.
  Two consequences of the same removal: `--team-agent` is gone from `cast`
  (casting reads no system memory now, so the flag had become
  accepted-and-ignored), and `--role` is NO LONGER VALIDATED against defined
  roles (`cor:agt:020:00` §1 — cast lists are ergonomics, never a gate). An
  unrecognized role resolves no agent (`WORKER_AGENT_NOT_FOUND`).
  **`--role` and `--agent` cannot be combined** (`cor:dmo:050:11`, R222, cli#1087):
  the role follows the agent and is never typed in beside it. The pair is exit 2
  before any call (`--dry-run` included); the server's own refusal,
  `WORKER_ROLE_OVERRIDE_REFUSED`, is also exit 2 and its message names the
  agent's role. `--role` alone and `--agent` alone are unchanged.
  Per-worker sources use `boot:workers:<id>:source` (role `boot-prompt`)
  in the App install memory; use `hadron team worker compile <worker>`, then
  `hadron node approve <compiled-node-ref>` before serving.
  Because `--role` resolves an AGENT and consults no role DEFINITION, a
  successful `--role` cast whose role has no `team role` definition prints a
  note on **stderr** (#542) — the divergence is created here, and stderr keeps
  the `--json` receipt on stdout clean. Best-effort: an unreadable roles
  branch stays silent rather than risk a note the operator cannot trust.
  **Reporting choice** (cli#933): `--reports-to <worker>` names the worker the
  new one reports to; `--no-team` records that it has none. One or the
  other, never both (usage, exit 2, nothing sent), and `--no-team=false` or a
  blank `--reports-to` is not a choice. Leaving both out sends neither
  argument: choose deliberately, because a cast without a choice will be
  refused once enforcement is on, and until then it records no choice
  (`UNSET`, never read as no-team and never a guessed coordinator). The
  server decides authority and an unknown head; its refusals
  (`WORKER_TEAM_CHOICE_CONFLICT|INVALID|REQUIRED`, exit 2; `WORKER_NOT_FOUND`,
  exit 4) surface verbatim and the cast is never retried. The recorded
  `teamPlacement` reads back as `REPORTS_TO`, `NO_TEAM` or `UNSET` on the cast
  receipt, `--dry-run`, `worker get` and `worker list` (`--json`:
  `teamPlacement`); `null` means withheld from this caller, never `UNSET`.
  Server compatibility: every `team worker` command selects `teamPlacement`
  even without the flags, so they need a server with hadron-server#2468; an
  older one rejects them as schema skew (exit 2): upgrade that provider first.
  **`worker cast --dry-run`** (#404, `castWorkerPreview`) runs the cast's
  EXACT resolution — same arguments, same typed refusals — up to but not
  including the writes, and shows what would be created: the
  name, the resolved agent (ids in `--json`), the composed boot
  prompt. A refusal
  on the dry run IS the answer that the real cast would refuse the same
  way. The preview RESERVES NOTHING (no lease exists): a previewed name may
  be gone at cast time — the cast's uniqueness constraint stays the only
  allocation primitive.
  **`team role list|get`** (#403, `teamRoles`) reads the App's role
  definitions: the role, its loc/nodeId, the description, the resolved role
  agent. **The register projection is
  GONE** (hadron-server#1050): `register`, `freeCount`, `exhausted`,
  `nameRange` and `nameConvention` are removed from the `--json` shape
  rather than emitted empty, because a permanent `[]`/0 would keep promising
  an allocation surface the server no longer has. "Which names are free" is
  no longer a question this answers — `worker list` is the roster, and a
  cast names its worker explicitly. The prompt
  TEMPLATE lives on the role AGENT (`agent get`), not the role node;
  `--team-agent` disambiguates when several
  installed agents carry a `roles:` branch (`TEAM_AGENT_AMBIGUOUS`, exit 2).
  The whole group's team App resolves ambiently (`--app` → App context →
  the worktree binding), so every command says which App it acted on and
  **which of those answered** (#468): the reads open with it, the writes
  carry it in the receipt, `role rm` puts it in the confirmation.
  Render-only: `--json` shapes are unaffected by the scope line and the
  decorating read is not issued under `--json`.
  **`role list` also warns on role↔agent divergence (#542), both human-only.**
  A role name and an agent's `personaRole` are coupled by string equality
  alone and nothing gates the match (`cor:agt:020:00` §1), so a definition
  no installed agent carries (its AGENT cell is blank — `roleAgentName` null
  in `--json`), and an installed agent whose persona role no definition names,
  both pass unremarked. The reverse half reads the install roster, so it is
  skipped under `--json` (the null `roleAgentName` already carries the forward
  case, and the reverse is `app agent list`'s data) and under `--team-agent`
  (a narrowed listing sees only one branch's definitions). Warnings, never
  gates.
  **`team role create/update`** are thin over `createTeamRole`/`updateTeamRole`.
  A role definition now carries a role and a description, and nothing else a
  client can set, so `create <role> [--description]` and `update <role>
  --description` are the whole write surface. An existing role refuses
  `create` (`TEAM_ROLE_EXISTS`, exit 5 — `update` is the edit path); an
  `update` naming no field is refused exit 2 rather than sent, since omitted
  is "preserve" and the write would be a no-op reporting success.
  **REMOVED with the name register** (hadron-server#1050, `cor:agt:020:07`
  WITHDRAWN — nothing replaces the mechanism): the whole `team role names
  set|add|rm|mv` group, `--names`, `--name-range`, `--name-convention`,
  `--clear-name-range`, `--clear-name-convention`, `--allow-out-of-range`,
  and the `expectedNames` CAS behind the sugar. Their refusals
  (`TEAM_ROLE_NAME_MINTED`, `TEAM_ROLE_NAME_DUPLICATE`,
  `TEAM_ROLE_NAME_OUT_OF_RANGE`, `TEAM_ROLE_STALE`, `TEAM_ROLE_IN_USE`) are
  unreachable and no longer mapped — the server cannot produce them.
  **Retired role/preview name probe (#916 C6).** Role reads and cast
  previews no longer request the retired `hasNamePlaceholder` probe or infer
  nameless briefings from it. Existing JSON keeps that key as null for
  compatibility. Other role fields, preview prompt and `reserved:false`
  remain unchanged. This does not remove legacy platform-variable handling or
  alter the prompt a bind/run is served.
  **`team role rm`** (`deleteTeamRole`) retires a definition, and is now
  UNCONDITIONAL: the minted-name gate and `--transfer-register-to` both went
  with the register, so there is no allocation ledger left to preserve and
  nothing for a bare retirement to refuse over. `transferredNames` and
  `transferredTo` are gone from the `--json` payload for the same reason.
  The delete is SOFT (the `roles:<role>` node and its sub-nodes are
  tombstoned, recoverable) and `--yes` is required off a TTY.
  **Retiring never frees a name for re-casting**: names are permanent per
  App against the whole roster (`cor:agt:020:02`), and that was as true with
  the register as without it — which is the evidence the register was
  bookkeeping about ALLOCATION rather than identity. The role AGENT is NOT
  touched: `app agent remove <app> <agent> --yes`, then `agent rm <agent>
  --yes`.
  Names bind **forever** per App (`cor:agt:020:02`, case-insensitive):
  **`worker update <name-or-id>`** edits signature and trailer templates.
  Its receipt carries `signatureLint` (cli#2007, cor:agt:040:13/:04): the CURRENT
  check for a missing worker link, read after the write with the write's own
  session; `{items,total,nextCursor}` or `null` (unavailable or masked, never "no
  warnings"). `worker get` always carries `signatureLint` and `promptLintWarnings`
  (the lint of the compiled revision recorded for the open session, named by its
  provenance revision): `items: []` with `total: 0` is the one clean answer; null
  is unavailable, masked, or (prompt) nothing served. 20 per request to the end,
  one 5 s / 1000-request budget started after the main read, no exit-code effect,
  one stderr line naming the unavailable lint fields. Text labels use `signature lint:`
  on both get and update; update names the current check after save. Compiled
  warning locators say `occurrence #n (0-based)`; JSON `position` is unchanged.
  For boot customisation use the worker source and compile workflow above.
  **`--signature` / `--trailer`** (cli#916 C4, `cor:agt:040:04`) replace the
  worker's raw signature and commit-trailer TEMPLATES (`{{worker.name}}`-style
  variables, rendered into the next briefing; nothing already delivered
  changes). Omitted keeps the stored template; `""` stores an empty one, which
  delivers nothing (D8); there is no reset — pass the default text to restore
  it. App managers only; the length limit is the server's (exit 2). From a
  bound worktree these edits carry the worktree session, and a refusal exits 8
  with the server's sentence plus a hedged note to retry from an unbound shell.
  **`--instruction <kind>:<ref>`** (cli#1592, hadron-server#3307, ruling 0096,
  `cor:dmo:050:12`; server 2026-11-03 or later), on `worker update` and `worker
  change-agent`: a bound worker's claim, for this one call, that its holder
  told it to make the change; admitted when the holder manages the App, and
  recorded with the worker, holder and reference (the holder answers for the
  claim; the server checks the reference, never that an instruction
  happened; a host-launched session is refused regardless). kind is
  `team-chat-message` | `task` | `live-chat` (→ `TEAM_CHAT_MESSAGE` / `TASK` /
  `LIVE_CHAT`); the ref is everything after the FIRST colon, byte for byte, a
  reference and never instruction text. Bad kind or blank ref: exit 2, zero
  requests (before change-agent's confirmation). Without the flag the old
  documents go out unchanged (no `instruction` argument at all), so older
  servers keep working; with it, a separate instruction document, never
  retried or downgraded. Attribution with the flag is checked against the
  target worker's own App, not `--app`, on a binding snapshot taken before the
  first lookup: `update` refuses (exit 2, nothing written) an incomplete,
  foreign-server, other-App or mid-command-rebound binding (including one that
  appears during the lookup in a worktree that was unbound) and never falls
  back to the person; `change-agent` sends those headerless as the person
  (stderr note). Unbound throughout (or outside any worktree): the person's
  call, the ref still checked. Lookups never carry
  the session. Refusals keep their codes (`--json` `serverCode`), one write.
  `worker get` and every worker `--json` gain `signature` / `trailer`: a string
  (`""` = empty template) or `null` = WITHHELD (masked), never "empty".
  **`team session recover [--mentions-limit N]` (cli#2062)** is the one
  read-only call a fresh run makes instead of `session whoami`, `worker get`,
  the state note, `chat read --mentions-me --since <watermark>` and `task mine`.
  It sends NO `X-Hadron-Session` header, so it records no heartbeat, never
  acknowledges, never advances the watermark (`team chat mark-read` does) and
  never makes a session live. `--json` shape (keys always present):
  `{worker:{id,urn,name,role,appId}, session:{id,startedAt,server,open,endedAt},
  tasks:[{id,handle,status,priority,title,blocked,waitingOnChildren,parentTaskId}],
  tasksMore, stateNote:{urn,updatedAt,content}|null, standingDirections:string|null,
  mentions:{watermark,head,items:[chat message DTO, oldest first],more},
  unavailable:[{section,reason}]}`. `tasks` = your PENDING and STARTED tasks
  (up to 200). `stateNote` is the node `state` in your own working memory
  (the worker's `memoryId`), `null` when there is none or no working memory is provisioned; the
  `## Standing directions` section of it comes back verbatim in
  `standingDirections`. `watermark` is the server read cursor, `null` when the
  server reports none: null means unknown, not zero. A section that could not be read is listed in
  `unavailable`, is null/empty, and the command exits 7 after printing the
  rest: an unreadable section never means "nothing to do". No binding: exit 2.
  **Rendered values (cli#1525, hadron-server#2969, cor:agt:040:04):** `worker
  get` also prints the signature and trailer as the SERVER fills them, under
  `Signature:` / `Trailer:` ahead of the templates, and `--json` adds
  `renderedSignature` / `renderedTrailer` (always present: a string, or `null`
  when withheld from you or the server predates #2969, which prints no line).
  `session whoami --signature` / `--trailer` print ONLY that value for the
  worker whoami answers for, plus a newline, so `$(hadron team session whoami
  --trailer)` is the trailer to append; `--json` is `{"renderedSignature": …}` /
  `{"renderedTrailer": …}`. The CLI substitutes nothing. The read goes out as
  that worker session, so the server fills the values it was bound with (`worker
  get` of your own bound worker does the same). No binding and no open
  session refuses exactly as `whoami --check` (exit 4); several open sessions
  and no binding exit 4 rather than picking; a binding made against another
  server exits 2; a withheld value exits 8 and
  prints nothing; an older server exits 1. The flags exclude each other and
  `--check` (exit 2). Plain `whoami` is unchanged.
  `worker retire` (requires `--yes`, idempotent) stops the worker and keeps
  its name reserved — PR trailers and chat history reference it — and there
  is no rename. Before the confirmation it prints a handoff advisory on
  STDERR (also under `--yes`/`--json`): stored continuity facts only
  (CURRENT / FIRST_STINT / MISSING, the visible handoff URN, and that an open
  session may still write); never a claim that a handoff is unconsumed or
  lost; warning only, no new refusal or prompt, and an unreadable advisory
  continues through the same confirmation. `--json` keeps
  `id/name/retiredAt/status` and adds `advisory`
  `{continuityStatus, hasLiveSession, handoffURN}`, where null means
  unavailable to you, not none; `worker rm` is the ONE removal escape, hard-deleting a
  NEVER-USED miscast and freeing its name. Persisted references or an
  unreleased stint refuse with `WORKER_IN_USE` (exit 5); an ended empty
  stint alone is not attributed work. Before removing one, verify that no
  signed commit, trailer, PR, comment, review or other published artifact
  attributes work to it, then pass `--confirm-no-external-attribution`.
  Database checks cannot prove external absence. Known or uncertain history
  means retire instead. `--yes` skips the deletion prompt only, never attests;
  omission of the separate flag refuses with
  `WORKER_EXTERNAL_ATTRIBUTION_UNCONFIRMED` (exit 2). Concealed/not-found workers
  remain exit 4. Old-server schema refusal never retries an unattested delete.
  `worker rm --dry-run` (cli#1735, `deleteWorkerPreview`, server 2026-11-06) is ONE
  Query after the worker lookup: advisory, never a reservation, never the
  attestation. It never calls `deleteWorker`, never prompts, never retries and
  never falls back (an older server's schema refusal ends it). Combining it with
  `--confirm-no-external-attribution` is exit 2 before any request. Refusals keep
  their codes (`WORKER_NOT_FOUND`/`WORKER_REFERENCE_NOT_FOUND` exit 4,
  `WORKER_IN_USE` 5, `FORBIDDEN` 8). `--json` is
  `{id, name, attestationRequired, status: "preview"}` (never `deleted`); a null
  reply or one naming another worker fails. With `--instruction` it is NOT
  write-free: one claim event and a fence bump, disclosed in the text.
  **`team worker release <name-or-id>`** clears the HOLD (`releaseWorker`,
  `cor:agt:020:09`) and is the ONLY thing that frees one — not `session end`,
  not an idle window, not an expiry, not a reap, not a closed chat session.
  **Release is not retire**: the worker keeps working, keeps its name and its
  history, and the name is never freed for a different casting; only who may
  BIND it changes. The worker's working memory and handoff history follow the
  NAME, so a release hands them to whoever takes it next — the intended
  transfer, and the reason nothing private belongs in a worker memory. For a
  RETIRED worker there is no next holder (`session start` refuses one), so
  releasing clears the hold and nothing else and the history stays with the
  name; the receipt and confirmation say so for the worker in hand. TWO
  ACTS, gated server-side: releasing your OWN name notifies nobody and does
  not prompt; releasing SOMEONE ELSE'S is an admin force-release, which
  **posts to the team chat** naming both parties and prompts unless `--yes`.
  That post is BEST-EFFORT server-side (an unreachable chat never blocks the
  release), and since hadron-server#1073 the payload REPORTS whether it
  happened (`notified`), so the receipt states the outcome instead of hedging
  about it — including the case where the notice was owed and FAILED, which the
  caller must be told because they are then the only person who knows the team
  was not informed. A force-release that is refused names the holder and who
  can lift the hold — "only the holder, or an App or org admin, can release
  it" (in a user-owned App the owner is that admin) — both for the non-TTY
  refusal without `--yes` (still exit 2, costs one user read) and for the
  server's not-authorized refusal (still exit 8, the server's sentence kept);
  a self-release is never decorated, and `--json` keeps its error envelope.
  The CLI reads the hold only to say which act it is, never to decide who may
  perform it. Idempotent, and it never claims the name was free: `heldByUserId`
  is masked to null on deny, so a nil hold read ON ITS OWN means "unheld OR held
  and invisible to you". **`release` does not disambiguate it, and since #522 it
  does not need to.** It asserts what it classified — see the precondition
  below — so a nil hold that is actually somebody's is refused by the server and
  turned into an informed offer, rather than resolved by a client-side guess.
  (A signal that WOULD tell the two apart exists since #487 — `hasLiveSession`,
  masked by the same gate, below — and this command still does not use it:
  making the ambiguity HARMLESS is a better answer than resolving it, and the
  two designs PR #504 retracted were both attempts at resolving it.) It does not prompt on a
  nil hold, since every idempotent no-op would otherwise.
  **THE RECEIPT IS THE SERVER'S ANSWER, NOT THE CLIENT'S PREDICTION**
  (hadron-server#1073), and this is a **breaking `--json` change**. The payload
  now carries `releasedFrom` — the holder the WRITE ended — plus a
  server-computed `forced` and a `notified`, so the two hedges this command used
  to publish are RETIRED rather than tolerated:
  `wasHeld` was TRUE-or-NULL and is now a definite **bool** (`releasedFrom` is
  non-null exactly when a hold ended), and `forced` was nullable when the
  caller's own identity could not be read and is now a definite **bool**
  computed against the authenticated principal inside the write. **A consumer
  branching on `null` for either will no longer see one** — that branch detected
  an ambiguity that no longer exists. `status` **keeps its published literal `"no-visible-hold"`** even though its
  meaning is now stronger (the write ended nothing, rather than nothing being
  visible): a status is a branch key, and renaming one is the change that fails
  silently for an agent matching it. The improved evidence is in the new keys.
  `--json` carries `wasHeld`/`releasedFromUserId`/`forced`/`notified` plus
  `releasedFrom { id, handle, urn, name }`. `releasedFromUserId` keeps its name
  and mirrors `releasedFrom.id` — only its provenance moved, from a client
  pre-read to the server's answer, which is strictly more correct on the retry
  path. The receipt's "what happens next" clause is gated on **visibility**, not just on
  a null hold: `heldByUserId` masks to null on deny, so availability is claimed
  only when `hasLiveSession` is non-null on the returned worker, and a re-hold
  taken between the write and the re-read is reported rather than papered over.
  `releasedFrom.name` is **gated** and null for a viewer outside it (an
  admin releasing a colleague who has LEFT the org is the ordinary case, since
  `leaveApp` deletes the membership while the hold survives) — fall back to
  `handle`, and then to `id`, never to a dash. **`handle` and `urn` are
  nullable too** (a handle-less user), so `id` is the only rung that cannot be
  absent; the CLI's receipt walks all three. It is a PROJECTION rather than a `User`, so do not
  expect to select further.
  **`notified` is three-valued and the third state is the point**: `null` = no
  notice was owed (a self-release, or nothing released), `true` = owed and
  posted, `false` = **owed and FAILED**, with the release still done. An
  unreachable chat must never block an admin recovering a departed colleague's
  names, so a client that collapses `null` and `false` cannot tell its user the
  team may not have been informed.
  **THE RELEASE STATES THE HOLD IT CLASSIFIED AGAINST** (#522,
  hadron-server#1084). Exactly one assertion travels with every call —
  `expectedHolderUserId: <the holder>` when one was visible, `expectUnheld:
  true` when none was — and the server refuses **`WORKER_HOLD_STALE`, exit 5**
  if that is not the hold it is about to write. It is enforced BY THE GUARDED
  WRITE, so there is no check-then-write window: the act performed is the act
  described. This replaced a client-side re-read that could only NARROW the
  race. The unheld assertion is the one that matters most — before it, a nil
  hold meant the CLI asserted nothing and released unconditionally, so a hold
  taken in the interval or merely masked from the caller was force-released in
  silence and announced in the team chat for an act nobody was asked about.
  **A refused assertion becomes an informed retry, ONCE.** The refusal carries
  the holder found now (`heldByUserId` in extensions; `null` there means the
  name is unheld now), which is information the caller could not otherwise
  have — so instead of failing, the command re-asks against the truth: it
  proceeds silently if that holder turns out to be YOU (a self-release owes no
  confirmation), prompts if it is somebody else, and refuses **exit 5** when
  there is no TTY and no `--yes`. A second stale answer stops rather than
  retrying — one retry, never a loop. Nothing is changed before you answer, and
  the receipt then reports what the SERVER saw — `wasHeld`,
  `releasedFromUserId`, `releasedFrom` and now `forced` too. **`forced` used to
  be the exception here**, staying null on this path because the retry learns
  WHO holds the name and never who you are; since #1073 the server classifies
  it, so the one path where this client could never answer is the one where the
  server always can. When the
  refusal says the name is unheld NOW, the command refuses rather than claiming
  a release it did not perform. Retirement is still re-read before the call —
  the precondition covers the hold, not whether the worker is still working,
  and a retirement mid-flight changes what releasing MEANS.
  `worker get` shows the holder (and `--json` gains
  `heldByUserId`/`heldAt`, additive); both are masked to null on deny, so a
  bare absence means "unheld OR not visible to you" — there is deliberately no
  `held` boolean, which would answer "no" to a caller who merely cannot see.
  Since #487 that read also prints `held by: nobody — binding it holds it`
  and a `driven:` line, but **only when `hasLiveSession` is non-null** (below);
  a caller who cannot see the working state gets NEITHER line rather than a
  dashed one, because a dash there would answer a question the server refused.

**`worker get` profile read (cli#1355, hadron-server#1149).** By id or worker
URN, a signed-in non-participant reads the worker's PROFILE; the command goes
straight to the worker read with no App or roster lookup (a name still needs
an App you participate in). `--json` adds `appName` and `ownerName`, always
present and nullable: null is UNAVAILABLE (masked, an older server, or an App
URN of the old shape), never proof there is no App or owner. Every
working-state key (`prompt`, `memoryId`, `hasLiveSession`, `checkStatus`,
`createdBy` ...) stays a present null on a profile read, and the human output
says `working state: not visible to you` and omits the holder, driven, check,
reporting and boot-pointer lines. The two names come from a separate
operation, so a server that predates them still serves `worker get` as before
(the fallback is only for that documented schema absence; an auth, transport or
server error fails the command).
  **`worker list` now carries the hold too, alongside liveness** (#487,
  hadron-server#1086): the table gains `HELD BY` and `LAST RECORDED ACTIVITY` after
  `ROLE`, and `--json` gains `heldByUserId`/`heldAt`/`hasLiveSession`/
  `lastActiveAt` on every row, additive. **The two columns answer different
  questions and must not be collapsed** (`cor:agt:020:09`). `HELD BY` is whose
  name it is — what decides whether you may bind, freed only by an explicit
  `worker release`, and `--force` does not help against someone else's hold.
  `LAST RECORDED ACTIVITY` shows `live` when the server reports a live
  session; otherwise the age of the server's last recorded activity (`3d ago`),
  or `never` for a name nobody has ever bound. A recent age does not imply a
  live session. This is not a session-end timestamp: normal ending may advance
  recorded activity to approximately the end time. **Since hadron-server#1114
  liveness is DERIVED at read time** — not ended AND driven recently — so it is no longer the same
  question as "is a session row open": an abandoned session stays open
  indefinitely and stops reading as live. **`live` is still not a claim that
  anyone is at the keyboard**: the window is generous, and a worker session
  outlives the chat session that started it. There is deliberately no age
  beside `live`, because an age there reads as presence.
  **`hasLiveSession` is also the VISIBILITY SIGNAL for this row's working
  state.** It is `null` only when the read gate masked it — the same gate that
  masks `heldByUserId`/`heldAt`/`memoryId` — and otherwise
  `true`/`false`. So `hasLiveSession: null` means every other working-state
  null on that row is a mask and both columns render **`?`** — "not available
  to you", never "nobody" and never "idle". `?` rather than the `—` this table
  uses elsewhere, deliberately: the adjacent `RETIRED` cell renders `—` for a
  DEFINITE no, so a masked row spelled with dashes would read as three settled
  facts, two of which the server refused to state. With `hasLiveSession`
  non-null, `heldByUserId: null` genuinely means unheld and `lastActiveAt:
  null` genuinely means never driven. Both keys are always PRESENT under
  `--json`, never omitted, because an absent key cannot carry that
  distinction. **An age can under-report on a
  reaped session**: the reaper overwrites the last heartbeat, so the
  derivation falls back to the session's start and the worker reads as MORE
  idle than it was — never less.
  **`team worker list` is the "who is on this team?" read** — the
  App's staff (retired hidden unless `--include-retired`); `hadron app agent
  list` is the install roster (the cast pool). Its team App resolves
  ambiently (`--app` → App context → the worktree binding), so the human
  render opens with the App it landed on and **which of those answered**
  (#458) — two worktrees bound to different teams otherwise print different
  staff identically. That line is render-only: `--json` stays the bare array
  and already carries `appId` (and `urn`) on every row. `agent list` answers a
  DIFFERENT question — every agent you can read — and `--app` does **not**
  scope it (that flag sets the App context and is not a filter; passing it
  prints a stderr note saying so). **Two things are called a session and
  ending one does not end the other** (hadron-server#1034): a **worker
  session** is the Hadron binding below, and a **chat session** is the
  conversation the human is in — the Desktop window, the Claude Code session.
  Closing a chat session does NOT end the worker session, and since
  hadron-server#1114 no idle window does either. `session end` does (the only
  path with a handoff), and so does the same person's next bind of that worker
  (not a resume of the same session), which supersedes their old open sessions
  on it without a handoff; a session given a hard deadline at start also
  auto-expires. Being TAKEN is separate and DOES clear on its own,
  because the server derives it from recent driving rather than from the row
  being open.
  And `session end` frees the SESSION, never the NAME: a person who binds a
  worker holds its name until `worker release` (`cor:agt:020:09`).
  Note "taken", not "released": since hadron-server#1050 **release** is a term
  of art for clearing the HOLD, and no session-lifecycle event clears one —
  see `worker release` (`cor:agt:020:09`). "Taken" covering both states is
  hadron-cli#487; this sentence is precise about which it means, which is not
  the same as fixing that.
  Never shorten "chat session" to "chat" — in this team the chat is the TEAM
  chat. A **worker session** binds the current git
  worktree to a worker: `session start --as <worker>` records provenance
  (repo/branch/host/tool/transcript path/model) server-side, prints the
  worker's boot briefing, and writes a local binding under the worktree's
  git dir (`git rev-parse --git-dir`), which `session whoami` reads back
  after a context compaction. At bind, `session start` also delivers
  `Worker.continuity` above the briefing: `CURRENT` includes the previous
  stint's handoff URN and content; `MISSING` names the missing current
  handoff before any older one; `FIRST_STINT` says there is no previous
  handoff. `--json` adds `continuity` with status, handoff and previousSession;
  `null` means unavailable, not no handoff. An older server without the field
  still binds and omits the text.
  **A call through a binding whose session ENDED warns (cli#914, Holger R1/R2,
  `cor:dmo:070:05`).** The first time a command consumes the worktree binding
  (any team command that reads it, or an authored write that attaches the
  session; a plain read sends none and checks nothing), it asks the server
  once. If the session ended, **stderr** says `warning: this worktree's worker
  session <id> (worker <name>) ended at <time> (cause: ended|superseded|expired|unknown)`
  and names the rebind: `hadron team session start --as <worker id>` (a plain
  start replaces an ended binding; **no --force**). The cause is the server's
  `Session.endReason`; a legacy end or an older server reads **unknown**, never
  guessed, and no actor is shown. **The call itself runs unchanged**: stdout,
  `--json` and the exit status are untouched, nothing ends or rebinds, and the
  binding file is not rewritten. A check that cannot complete (unreachable
  server, a binding for another server or one that recorded none, a session
  you cannot read) prints `note: could not check …` and never claims an end.
  `session start`, `session end` and `whoami --check` answer for the session
  themselves and add no notice.
  **Recovering after a context reset: resume claims (cli#842, server #1956,
  `cor:agt:020:03`).** Before a deliberate reset, write a claim:
  `session resume-claim` for a SHED (the session stays open) or
  `session end --resume-claim` for a ROTATION (written with the handoff,
  before the end; `--json` gains `resumeClaim: true`). Then, within **60
  seconds**, the fresh context runs `session start` with **no worker named**
  (and no launch grant). The server judges it only by your token: your one
  unused claim binds (`RESUMED` to the open session, or `STARTED` as the
  worker's next session); with no claim, your one LIVE worker session is
  `RESUMED`. The binding is written and the briefing printed as for any bind,
  and `--json` carries `resumeOutcome` (`RESUMED` | `STARTED`; **null** on an
  ordinary or launched bind). Several candidates bind nothing: they are listed
  (`--json`: `{"resumeOutcome":"CHOOSE","candidates":[{sessionId, workerId,
  workerName, description, source: CLAIM|LIVE_SESSION, expiresAt}]}`), **exit
  2**, and `start --session <id>` picks one. `NOTHING_TO_RESUME` is **exit 4**
  and nothing is posted or recorded; `RESUME_REFUSED` (the lifecycle gate) is
  exit 5 and has spent the claim, except reason `WORKER_MISMATCH`, which
  spends nothing; `RESUME_AMBIGUOUS` is exit 2 (name the worker). A worktree
  with a binding file (lapsed or ended) sends that worker as `expectedWorkerId`
  (needs hadron-server#3822, never with `--as`; an older server rejects the field and the resume refuses, exit 2, consuming nothing and not retrying without it: use `start --as <worker>`): if nothing of that worker's
  waits but another worker's claim or live session does, the resume is refused
  `WORKER_MISMATCH` (exit 5), naming both workers, that worker's item is
  untouched and no binding is written (run `start` in that worker's own
  worktree, or `start --as <worker>`); `--json`'s `error.extensions` carries
  `code`, `reason`, `picked`, `expectedWorkerId/Name`, `foundWorkerId/Name`
  and `foundSource` (CLAIM|LIVE_SESSION), exactly as the server sent them. A resume refuses `--repo`, `--branch`, `--reports-to`, `-m` and
  `--force` (exit 2), refuses over a worktree still bound to a live session
  (exit 5) and where git fails for any reason other than "not a git
  repository" (exit 2), all BEFORE any claim is spent. A plain directory
  (not a git checkout, cli#1391) resumes, starts and sheds: the binding is
  cached in `.hadron-team/hadron-team-session.json` inside that directory
  (owner-only, same lock), only the current directory is consulted, and
  another worker's start or resume there is refused (exit 5),
  `--force` included, so one directory never shares a binding. It never ends the session it recovered: on a later failure, run
  `start` again. The binding caches the bound session's own tool, model and
  repo; a flag fills only a field it lacks, and a flag a RESUMED session
  ignores gets a stderr note. A claim carries no secret and no id — there is nothing to
  store or pass on. It is for a context with NO binding: in a worktree still
  bound to a live session the binding already carries the worker (`whoami`),
  and a resume there refuses (exit 5). `tookOver` is **null** on `RESUMED`
  (another context may still drive that session) and false on `STARTED`. An
  empty `--as` is refused (exit 2), never read as a resume.
  `resume-claim` on an ended session is `SESSION_ENDED`, exit 5; its `--json`
  is `{sessionId, workerId, workerName, expiresAt}` (`workerName` null with
  `--session`).
  **`whoami` falls back to the SERVER when there is no binding — and when
  there is no worktree at all (#623)**, so a lost binding
  is a cache miss rather than an orphaned session, and a caller with no worktree
  (a non-coding worker, Cowork) can still answer "what am I driving?". `--json`
  carries `"source"`: `worktree` or `server`, plus `candidates[]` (always an
  array). The fallback lists only sessions that are **yours, worker-bound and
  still open** — the server's list is deliberately wider, including sessions you
  may merely SEE — and with several open it reports them all and leaves
  `sessionId` empty rather than guessing. **`git worktree remove` deletes the
  binding and does NOT end the session**; recover the id with `whoami` and end it
  with `session end --session <id>`. **`whoami --check` asks the server about
  the bound session (#484, state since cli#815)**. Binding identity stays available
  locally for compaction recovery. The default now also makes a best-effort
  worker read for its CURRENT reporting relation; a failed, masked or unavailable
  read reports `reportingKnown: false` / `? (unavailable)`, preserving recovery
  without pretending the worker reports to nobody. Use it
  when the binding may have outlived what it describes: `session end --session
  <id>` ends a session from ANYWHERE and clears only the binding of the worktree
  it ran in, so every other worktree keeps describing a session that is gone.
  When the server reports the session ENDED (`endedAt` set, however it ended) the answer is printed
  as usual and the command exits **14** (`SessionEnded`, #1835) — open, quiet or dormant stays 0 and a
  check that could not be answered keeps its own non-zero code (not-found 4, unavailable 7). Never 3.
  `--json` gains `checked`, `active`, `endedAt` and `autoExpiredAt`; `active` is
  **null** unless `checked` is true, because "not asked" is not "dead". It is
  `active`, not "live": the predicate is `endedAt IS NULL` and nothing more — the
  same one `session list` calls active — so it never means the worker is present.
  `--check` is honoured on the server-fallback path too, where the openness is
  already the server's word. `endedAt`
  answers WHETHER it ended and `autoExpiredAt` answers HOW — non-null only when
  the server reaped it rather than a person ending it. **`--check` also shows
  the server's own session state (cli#815, `Session.state`)**: text `server:`
  reads `live, lapses at <instant>`, `LAPSED at <instant>` (open but not live;
  rebind), `DORMANT` (no agent act resumes it; its human holder must rebind) or
  `ENDED` (also when a session hard-expired and is not yet reaped, where
  `active` is still true). `--json` adds `live`, `lapsesAt`, `state`
  (`live|lapsed|dormant|ended|unknown`) and `candidateStates` (`[]` unless the
  server fallback ran under `--check`; one `{sessionId, live, lapsesAt, state}`
  per candidate, also a STATE column in the table). `live`/`lapsesAt`/`state`
  are **null** without `--check`; a server that predates `Session.state`
  answers `state: "unknown"` (never guessed) and a transport/auth failure on
  the state read is an error, not "unknown". The CLI derives none of it — no
  clock, no idle or dormancy policy. Plain `whoami` makes no session-state
  read (it still reads the worker's reporting line). **`active` is unchanged and is not liveness**: since
  hadron-server#1114 a developer session has no inactivity deadline, so use
  `state` for "is it driven". **Most of the
  time, do not end a session at all**: they are meant to be long-lived
  (hadron-server#1114 removed the idle reaper), so ending is for when the WORK
  ends, not when a chat session, branch
  or worktree does. The session binds the
  WORKER (`SessionInput.workerRef`, the worker's id); the server stamps the
  role-agent AND the worker's App itself, so every worker session is
  App-bound; a retired worker refuses (`WORKER_RETIRED`). A worker with a
  LIVE session is *taken*: `start` refuses (exit 5) showing who is
  driving it since when, and `--force` takes over — informed and deliberate,
  never silent (`cor:agt:020:03`). **A forced bind that MAY displace somebody
  fails closed when it cannot name them** (#553): the session-detail read is
  narration everywhere else and degrades to a stderr note, but a takeover that
  cannot see its driver has nothing left to be informed by, so a failed read
  there refuses with the read's own exit code (a lost answer is exit 7, not 1)
  and the bind is not attempted. It takes BOTH `--force` and a worker row that
  does not prove the name is free: `--force` over a name whose
  `hasLiveSession` is **false** displaces nobody and still degrades — it is
  also the flag that replaces an abandoned worktree binding, so failing there
  would strand a caller who is taking nothing from anyone. A masked liveness
  does fail closed, because *"the server did not tell me somebody is there"* is
  not *"nobody is there"*. A read that ANSWERS and returns nothing does not
  refuse — it is the expected reply for a caller outside the session read
  scope, and the takeover names it as a driver *you are not permitted to see*
  rather than as an unknown one. **`--force` also stops riding to the server
  once the client has PROVEN the name free** (`hasLiveSession: false`): the
  override waives the server's atomic `WORKER_TAKEN` gate, and waiving it for a
  caller who was told there is nothing to waive would take the name from a
  driver who bound in the meantime — silently, and without naming them. The
  refusal is surfaced instead, with the driver, so the retry is an informed
  override. A masked liveness still forwards it, because absence of evidence is
  not evidence the name is free. **Live, not merely open** (#550): binding a
  name whose session is open but no longer live is not a takeover, needs no
  `--force`, and does not end that session — the note on stderr says so, and
  `--json` reports `tookOver: false`. **Live is DERIVED, not stored**
  (hadron-server#1114): the server computes it at read time from when the
  session was last driven, so a name whose driver stopped stops being taken on
  its own — and nothing ends the session to achieve that, so the CHANCE to
  write a handoff is still there for its driver to take. There is no handoff
  until somebody writes one: leaving the row open preserves the opportunity the
  old reaper foreclosed, not a record that does not exist. An OPEN session
  therefore tells you nothing about whether the name is taken; liveness does,
  and it means DRIVEN RECENTLY rather than that somebody is at the keyboard.
  **When liveness is not visible to you, the server decides** (#550): it rides
  on the worker's gated working-state fields, so a caller outside that gate
  reads `null` — WITHHELD, not "no" — and `start` binds rather than refusing,
  leaving the same rule to the server's atomic gate. `tookOver` is `null` in
  that case, distinct from `false`; like `continuity`, it is a nullable
  `--json` key on this command. A state the server declines to assert is
  different from one it asserts negatively.
  (`worker release`'s `wasHeld`/`forced` used to be the example here and are
  no longer nullable at all: #1073 gave them definite answers.)
  A `--force` that replaces this
  worktree's own binding first ends the session it named, best-effort.
  **HELD is not TAKEN, and only TAKEN is forceable** (`cor:agt:020:09`, #487).
  A name is held by the PERSON who binds it, and nothing about a session
  frees one — not an end, an idle window, an expiry or a reap, only
  `worker release`. Binding a name held by somebody else refuses
  `WORKER_HELD` (exit 5) whether or not `--force` rides along, and the
  remedy is to **cast your own worker** for the role, or to ask the holder
  (or an App/org admin) to release it — never to retry with the flag. `start`
  refuses this before it reaches the server when the hold is visibly
  another's AND the name is live — under `--force` too (#1859): a live worker
  is classified before any `taking over worker …` line, which prints only when
  the hold is yours or none is visible; if your own identity cannot be read it
  prints a hedged line instead ("the server decides") and still binds, and a
  name that is not live pays no extra read. Otherwise the server refuses it and the CLI
  renders that — same sentence, one round trip later. The server is the
  authority on a hold either way, which is also what closes the race the
  pre-flight cannot: the hold can be claimed between the worker read and
  the bind. Two server paths raise the code and
  they carry different payloads (`workerId`+`heldBy` always; `heldByName`
  and `heldAt` only on the non-race path), so the holder degrades to a raw
  user id rather than going blank. Casting does NOT hold: a roster staffed
  for other people is unheld until each of them binds, and an App-key
  session holds nothing at all.
  **Authored writes (#822):** node/edge/object and governed node mutations
  carry the worktree worker session only with recorded matching deployment
  and unchanged worker/session/App binding. An ambient App context, if present,
  must resolve to that App. Destination-memory access remains server-authorized;
  writing another authorized memory does not imply another author. Queries,
  approval/mint maintenance and raw `api` calls keep their existing contexts.
  Rebinds or missing provenance leave subsequent writes headerless. The server
  validates principal ownership and ignores ended/expired session headers.
  No write is replayed for attribution. Worker/agent stamping requires server
  #1658; older servers already accept the header without those new fields.

  **One worktree per live worker session** (#472, #791): binding a worktree
  that is already bound to a live session refuses (exit 5) and points at
  `git worktree add -b <new-branch> ../<name>`
  (the `-b` matters: the bare `<commit-ish>` form takes an EXISTING ref — a
  fresh name fails `invalid reference`, and a tag or sha silently gives a
  detached HEAD), because
  `--force` replaces the binding without separating two agents from one index
  and one working tree. An ended or lapsed binding can be replaced by plain
  `session start` when the server confirms it is not live. An open lapsed
  session is not ended by that replacement, leaving its own driver able to
  write a handoff. If exact liveness is unavailable, the CLI refuses with an
  unknown-state message rather than calling an open session live. Two agents
  in one checkout is the hazard: `git add -A` sweeps the other's in-flight
  edits and `Session.branch` is captured once at bind and never revisited, so the
  provenance a merged PR traces back through goes false silently. The guard
  catches a second BINDING only — a second agent working unbound in the same
  checkout does identical damage and nothing fires.
  `session log` additionally prints a **stderr** note counting team-chat
  messages since you last ran `chat read`, and how many mention you (#474) —
  the moment before you publish something durable is the last point a missed
  decision can still change what you do. `chat read` records that watermark on
  the binding, but ONLY for an unfiltered read of the binding's own App that
  rendered successfully: a `--mentions`/`--mentions-me` read skips the
  messages in between, a `--app <other>` read is not this binding's cursor
  (compared by canonical App id, so the same App named as a URN still counts —
  paired with a server check, since App ids are unique within a deployment and
  not across them),
  and output that never reached you — a closed pipe, a full disk, at any point
  in the render — was not read. It is also NOT `nextSince`
  — that is a paging cursor, answering "where do I resume"; the watermark is a
  claim about what you have SEEN. It advances only when the read was
  CONTIGUOUS with what the binding already holds (starting at or before the
  current watermark, or at 0 when there is none) and only to a seq the server
  actually returned. So `--since` ahead of the watermark reads a window rather
  than a prefix and records nothing — it would otherwise bury the gap it
  skipped while reporting you as caught up. Never having read is
  reported as its own state rather than as a count, since it is the louder one
  — and phrased as what THIS WORKTREE knows, because the watermark is
  binding-local and a read made in the portal or under another binding never reaches it (a nudge
  that is sometimes wrong trains people to ignore the one that is right). For
  the same reason the note omits the mentions clause when that query fails —
  or returns a count EXCEEDING the total, which two round trips against an
  append-only sequence can produce and which is impossible rather than merely
  stale — rather than reporting the unknown as "none". Best-effort: the milestone is
  already recorded when it runs, so a failed or unreadable chat never fails the
  log, and `session log --json` is untouched. `whoami --json` gains one optional
  key, `chatSeenSeq` — additive and `omitempty`, so it is absent on a binding
  that has never read, and `0` on one that read a chat with nothing in it.
  `session end` ends the bound worker session — the SESSION, never the name
  hold (`worker release` does that) — unless another active worker session
  still has it (check `session list --active`).
  **`--handoff <text>` is the #1029 continuity record and the thing the next
  driver actually reads**: prose about what landed, what is open, what is
  blocked, what not to repeat. The server files it in the worker's own memory
  and the next bind hands it over — and since handoffs follow the NAME
  (`cor:agt:020:09`), that may be a colleague. `--handoff-file <path>` reads it
  from a file and `--handoff -` from stdin, because a paragraph through shell
  quoting is its own hazard (`--handoff -` is for a PIPE and is **refused, exit
  2, from an interactive terminal**, which can truncate it: #648). It is written BEFORE the session ends and a failed
  write REFUSES the end (`HANDOFF_WRITE_FAILED`, exit 1) rather than ending
  anyway: a still-bound worker is recoverable, an ended session whose handoff
  evaporated is not. **That ordering is a platform guarantee**
  (`cor:agt:020:10`), not this client's choice. After a successful end that sent a
  handoff, the saved node's URN is read back and printed (`handoff saved: <urn>`;
  `--json` adds `handoff`) only when the worker's continuity is `CURRENT` and names
  this session; otherwise exit stays 0 with one line `handoff saved; URN not read
  back (<reason>)` and no `handoff` key (the end is committed; nothing is retried).
  **One compatibility exception:** a server without `Worker.continuity` (it
  refuses the read-back query as a GraphQL validation error) prints no handoff
  line at all and no `handoff` key, exactly the older output. That does not mean
  the handoff was lost: it was written before the end, as above, and is only not
  read back.
  Passing it on a session with no
  worker is refused — there is no sequence to write to. An EXPLICIT empty
  handoff is refused exit 2 (somebody meant to write something); omitting it
  entirely is the normal way to end without a record, and the next bind is told
  a stint ended without one rather than being left to read silence.
  **RETRY IS SAFE and cannot double-write.** One stint records one handoff, and
  that is keyed on the STINT rather than on whether a write already happened —
  so a crash between the handoff and the close does not leave a second attempt
  appending a duplicate (`cor:agt:020:10`).
  **IF THE END FAILS WHILE CARRYING A HANDOFF, THE CLI SAVES THE PROSE** to a
  temp file and reports the path with the retry that recovers it (ready-to-run
  on POSIX; raw argument values on Windows — see below). **Under `--json` those
  details are folded into the error's `message`**, not printed beside it: in
  JSON mode the `{"error":{…}}` envelope is written to STDERR, so a plain-text
  notice on the same stream would leave stderr unparseable on exactly the
  recovery path an agent needs to read. Without `--json` it goes to stderr as
  human text. The guarantee above protects the SESSION;
  it cannot protect text that only ever existed in this process, which is
  exactly the case for `--handoff -` once the pipe has been drained. This holds
  for EVERY failure that can happen once the handoff has been taken — no
  binding, an unreadable one, a server mismatch, missing credentials, the
  mutation itself — not only for `HANDOFF_WRITE_FAILED`. The retry reproduces
  the original invocation, carrying `--server` and `--summary` so a pasted
  command targets the same deployment and asks for the same outcome; on a
  binding/server mismatch it names the BINDING's server, since the retry's
  explicit `--session` bypasses the check that refused. **On POSIX the retry is
  a pasteable command line** (single-quoted, so every argument is literal). **On
  Windows the arguments are listed as raw values instead** — no quoting a
  process can apply survives both `cmd.exe`'s `%VAR%` and PowerShell's `$var`
  expansion, and it cannot tell which it is talking to, so presenting a command
  line there would advertise a guarantee that does not hold.
  **The wording distinguishes what the CLI KNOWS, and it knows less than you
  might expect.** It says `was NOT recorded` for exactly ONE outcome:
  `HANDOFF_WRITE_FAILED`, because `cor:agt:020:10` guarantees a failed handoff
  write refuses the end. **Every other failure says `may not have been
  recorded`** — including other GraphQL errors, because an answered refusal does
  NOT prove nothing committed: a resolver can fail AFTER the write, GraphQL can
  return data alongside errors, and null-bubbling can empty the payload on a run
  that succeeded. A transport failure is unknowable for the same reason. So do
  not read a generic error as proof the handoff was lost. The remedy is
  identical either way, because one stint records one handoff — the asymmetry is
  deliberate: overstating certainty here would make a later retry failure look
  like confirmation of data loss.
  **If the local save ALSO fails**, what happens depends on WHERE the handoff
  came from, because printing it is the last option rather than the first — a
  handoff is a stint's working notes and stderr is retained in CI logs and agent
  transcripts. `--handoff-file` is pointed back at its own file (checked, not
  assumed: the spill may have failed for a reason that took the source too, and
  then it IS printed). An inline `--handoff <text>` is not reprinted, since the
  shell history or the calling process still holds it. **A consumed `--handoff -`
  IS printed**, between `----- handoff begins/ends -----` markers, because it
  went from the pipe into memory and was never displayed: there is no scrollback
  holding it, and telling you to copy it from your terminal would be an
  instruction you could not follow.
  **`--summary <s>` is a different field and the next driver never sees it** —
  a display-only label on the session row. Not a quirk of today's build:
  `cor:agt:020:10` makes `--handoff` the ONE field carrying continuity and any
  other free-text field on a session display-only unless the corpus says
  otherwise, so **writing continuity into `--summary` is a contract violation
  rather than a naming preference — and it fails silently, producing no error
  and no record.** Both are kept deliberately; if you are writing one thing for
  whoever comes next, write `--handoff`. `end --session <id>` is the recovery path when the binding is
  gone or unusable (including one written by a pre-Worker CLI, which whoami
  reports as a degraded read); `end` also refuses (exit 2) when the binding
  was started against a different `--server` than the current one.
  `session list` lists sessions newest first, worker names joined in.
  **It is NOT a presence view**: `--active` narrows to sessions that never
  ENDED, and since hadron-server#1114 no idle window ends a developer session:
  only `session end`, the same person's next bind of that worker (it
  supersedes their old open sessions without a handoff) or a hard deadline set
  at start does, so an abandoned one stays listed until then. For
  "was this name driven recently, and does the server still count it as
  taken", read `worker list`'s LAST RECORDED ACTIVITY. Neither view answers "is somebody
  at the keyboard" — nothing here does. `--as` narrows SERVER-side
  (`sessions(workerRef:)`), `--active` client-side. **Both its tables lead with `WORKER  ROLE …` and carry
  `SESSION` last** (#486): the role says what a worker IS and was already on
  the wire, while the id is the one value a human cannot act on. The id is
  never truncated — it is the handle `session end --session <id>` takes, so a
  shortened one would break copy-paste. `--json` carries `workerRole`
  alongside `workerName`/`workerId`, present as `null` on a session with no
  readable worker rather than omitted, so the shape stays uniform across rows.
  **`--json` also carries `role`** (cli#1087, `Session.role`): the role the
  session was BOUND under, which does not follow a later change of the worker's
  agent (`cor:dmo:050:11`), where `workerRole` is the worker's current role. The
  `ROLE` column shows the bind-time role and falls back to the worker's role for a
  session the server stamped none on; this needs a server with `Session.role`
  (hadron-server#2481), which refuses the session reads otherwise; `role` is `null` for a session without a worker. **The worklog** is the provenance record, and it needs NO
  flag (#399): the binding records the bound worker's App — the worklog
  home — so `session log` just works, and the provenance query defaults to
  the binding's App (unbound checkouts pass `--app <ref>` or `-m
  <team-memory>`; explicit `-m` outranks `--app`). `team init` is **not** a
  precondition for worklog writes (#414). `-m` everywhere is an explicit
  OVERRIDE: `SESSION_NOT_IN_APP` on a worklog write means it named a
  DIFFERENT App's memory than the bound worker's (drop `-m`, or pass the
  worker's own team memory). Bindings from older CLIs degrade honestly:
  a pre-#399 worker binding falls back to its recorded team memory; a
  pre-Worker binding gets the end-and-restart diagnostics. Separately,
  `team init [--app <ref>]` asks the server to converge the collections it
  owns onto their canonical definitions — the App resolves its own team
  shared memory (`App.sharedMemory`, hadron-server#965), so no memory needs
  naming (#400). Because it WRITES and its receipt names the target memory
  only afterwards, the human render prints the resolved App and where the
  scope came from **before** converging (#469) — render-only, so `--json`
  is unchanged and issues no extra call. The `-m` path prints no source: it
  resolves from the named memory, not the ambient chain. `-m` stays as an explicit override (an `app`-class memory
  is required and any other class is refused with exit 2, since a system
  memory is read-only from every App that runs it; naming some other
  app-class memory still declares on the team memory, reported honestly;
  idempotent, preserves other collections, and repairs a declaration
  written by an older CLI), and
  `session log (--pr | --issue | --commit | --branch) <ref>` appends a
  milestone — refs normalize to one canonical string per artifact
  (`owner/repo#371`, `owner/repo@sha`, `owner/repo:branch`; URLs, short
  forms, and bare numbers/shas/branch-names qualified by the session's
  `--repo` or the git remote are all accepted; a bare branch value is always
  a branch name, never owner/repo), with `--action` (default `worked-on`)
  and an optional `--detail` JSON bag. The worklog row is recorded FIRST and the
  session's PR/branch pointer moves only after it (#1278): a refused record
  (exit code unchanged, e.g. 4) leaves the pointer untouched, and if the pointer
  update is refused after a recorded row, stderr says the row WAS recorded and
  the pointer was NOT updated, but only when the server answered with a definite
  refusal (a GraphQL error that is not an internal fault, maps to exit 2, 3 or 4,
  and did not arrive inside a 5xx response: a gateway 502/504 carrying a JSON
  `errors` list is not proof of a refusal). For a lost or
  undecodable answer (exit 7, or exit 1 for an unreadable body), an internal
  fault, or such a 5xx-wrapped envelope, stderr says the pointer update could NOT
  be confirmed and may have been applied: do not re-run the log, it would
  duplicate the row. The same holds when the answer to the worklog RECORD call
  itself is lost or is not a definite refusal: stderr says the row may or may
  not have been recorded (the pointer was not touched), names the
  `session list --<kind> <ref>` check, and warns that a re-run may duplicate it;
  a definite refusal of the record writes nothing and carries no such note. Exit
  codes are unchanged.
  `session log --model <m>` explicitly
  reports the LLM that produced THIS milestone, for model switches or late
  records; omission leaves fallback to the server's session model. The CLI
  never sends the binding's possibly stale model as an override. The server's
  event-time value appears as `model` in the log receipt and each provenance
  `worklog[]` row (null means unknown); the human provenance view separates
  REPORTED MODEL from SESSION MODEL and ARTIFACT TOOL. Attribution is reported,
  not cryptographically verified. `session start --model` still records the
  session's initial model, and an old server can log without the new flag but
  refuses an explicit model rather than dropping it. `--pr` and `--branch` additionally
  denormalize onto `Session.prNumber`/`Session.branch` (latest wins —
  display convenience only); every logged milestone — issue and commit
  included — is a heartbeat feeding the server's liveness derivation; without a
  team memory they degrade to that denormalization alone (`"recorded":
  "session"` instead of `"worklog"`), while `--issue`/`--commit` refuse. The
  receipt names the bound WORKER (`✓ logged … as Vera`), and `--json` carries
  `workerName`/`workerId` (#559) — the worklog is append-only, so a record filed
  under the wrong worker (a shell that wandered into a differently-bound
  worktree) is permanent, and the name is what makes that catchable on sight.
  `session list (--pr | --issue | --commit | --branch) <ref>` is THE
  provenance query: worklog lookup by canonical (ref, kind) → the sessions
  that produced the artifact (several rows expected; a recorded session you
  cannot read lists as an id-only stub rather than disappearing, still
  carrying the worklog's worker name). **`team chat`** is the team App's
  group chat as a THIN wrapper over the platform operations
  (hadron-server#939, Worker envelope #974, spec cor:agt:020:04): the server
  owns placement (`chats:team` in the Team Agent's shared app memory,
  bootstrapped on the first post — no init step), atomic seq allocation,
  author derivation, and write-time mention extraction. The App resolves
  from `--app`/the App context, else from the binding's team memory. With a
  binding, `chat post` is authored by the bound WORKER through that session
  (the server verifies it is yours, active, and bound to a non-retired
  worker OF this App — `SESSION_NOT_WORKER_BOUND` /
  `SESSION_WORKER_NOT_IN_APP` / `WORKER_RETIRED` — and records it: a worker
  message always traces to the DRIVING human); with `--as-me`, by you.
  **Without a binding (no binding file, no worktree, or an App from `--app`
  or the context alone) and without `--as-me`, `chat post` REFUSES, exit 4,
  and sends nothing** (cli#842, `cor:agt:020:03`): it used to post as the
  human driving the agent. Recover with `session start` (no worker named),
  bind with `--as <worker>`, or say `--as-me`. `session log` refuses the same
  way (exit 4, unchanged). Mention teammates as `@worker-name`/`@handle` (a
  multiword name by its slug, `@mary-jane`); mention tokens carry **no
  uniqueness** (hadron-server#979) — a token may match several workers, and
  the filter simply returns every match. `--reply-to` takes the seq readers
  see (the server wires the reply edge; a missing seq is the typed
  TEAM_CHAT_REPLY_NOT_FOUND). `chat read [--since <seq>] [--before <seq>]
  [--limit <n>] [--all] [--mentions-me | --mentions <ref>]` filters on the
  SERVER-stored mentions (never re-parsed); `--mentions` passes through raw (a
  worker name or id of this App, or a user handle/id — the server resolves it
  against the App's own staff and members only); `nextSince` is the seq
  watermark to pass next turn — with a mentions filter it advances only past
  the returned messages (skipped ones re-scan server-side, free, and are never
  re-delivered).
  **ONE PAGE BY DEFAULT** (server #1536, CLI #787): with no cursor, the server
  returns its newest page (ascending within that page). The CLI omits
  `sinceSeq` on that request. **`--since 0` is explicit** and starts at the
  oldest page; `--since N` continues forward one page. Pass `nextSince` to
  continue. **`--before <seq>` walks BACK**, returning the newest page before
  that seq, still ascending; `prevBefore` is the lowest returned seq to pass
  as the next `--before`. The two cursors compose for a bounded slice.
  `--limit` sizes one page (1–200). Only **`--all`** walks forward to exhaustion,
  from seq 0 unless `--since` is given; it cannot combine with `--before` or
  `--limit`.
  **Freshness (#801):** a separate unfiltered head probe runs before the page.
  An empty/short unfiltered forward page or a newest page that ends below that
  baseline is suspected incomplete/stale delivery: exit 5, messages still rendered, nothing marked
  read; retry from the original cursor. Full forward pages, mentions filters
  and backward/bounded windows are not required to reach the head. A `--before`
  bound above the sampled head selects a newest page when the head also exceeds
  `--since`.
  Concurrent
  posts after the probe are allowed. Equal responses can both be stale: this
  is a discrepancy detector, not a freshness guarantee. JSON adds `readState`
  (`head`, `allocatedHead`, `readCursor`, `suspectedStale`); text shows the same
  pre-read cursor/head. `head` is the latest surviving message, `allocatedHead`
  the allocator watermark (deletions can leave gaps), and `readCursor` is the
  bound worker's default-Channel server cursor before this read. Null values mean
  unavailable or inapplicable. Metadata is a separate best-effort App query:
  its stricter access gate never denies readable chat. A parsed old-schema
  `beforeSeq` validation refusal explicitly disables comparison (`head: null`);
  other head-probe failures fail the read. Cursor lookup failures leave null,
  and so does a worker with no read state on the default Channel (not
  registered or not readable, #1272); a registered never-read worker reads 0.
  Shared connections or edge/backend pools can return two equally stale pages.
  **Session attribution (#817):** bound reads of the same App/deployment carry
  the session with `advanceReadState:false`, including the independent head.
  This retains attribution/liveness while acknowledgement stays after delivery.
  Metadata diagnostics stay headerless; a changed binding stops subsequent
  attribution. Older servers retry headerless only for the precise unknown
  `advanceReadState` argument on `Query.teamChatMessages`, with a note that
  attribution is unavailable. Other failures are not retried.
  A cursor that could only return nothing is **refused** (exit 2) rather than
  answered: `--limit 0` (the SDL gives it a meaning — count only — so it
  SUCCEEDS and returns an empty page), a negative limit, a limit above the
  server's 200 cap, and `--before` below 1. Each would otherwise produce the
  end-of-history signal below for a caller with the whole chat ahead of them.
  `--before 1` stays legal: it means "nothing older", which is the honest end of
  a walk.
  **`prevBefore` is null when the page came back EMPTY, and that is the only
  end-of-history signal.** Do NOT derive "is there more" from a count: the
  server's `total` is scoped to the CURSOR, not the collection, whenever
  `beforeSeq` is given (hadron-server#1121 — measured here: no cursor → 435,
  `beforeSeq: 400` → 399, `beforeSeq: 394` → 393), so a reader trusting it stops
  early with messages still unread. `chat read` therefore neither publishes nor
  decides anything from it.
  **A `--before` read never advances the watermark**, and cannot: it returns the
  newest messages before a cursor, so everything between `--since` and that page
  is unread by construction — the gap is in the MIDDLE, where the
  start-of-read contiguity check cannot see it. A cursorless newest-page read
  also **never advances it**, even if the page looks complete: the server's
  count and fetch can race. An explicit bounded `--since` forward read from
  the watermark is a genuine prefix. The
  commit trailer carries the **app-qualified compound** — `Persona:
  eng-team/Iris` (`cor:agt:020:02/:03`) — because worker names are App-scoped
  and org-ambiguous bare. That App resolution is ambient, so **`chat read`
  opens with the App and which branch answered** — unconditionally,
  including at zero messages, since an empty incremental read is the normal
  steady state and was otherwise byte-identical to reading another team
  (#470). `chat post` names the team chat in its receipt, and writes a `note:` to
  **stderr before posting** when the scope is ambient AND no session goes on
  the wire (`--as-me`; without a binding nothing else reaches the write) — with a sessionRef the server checks
  session against App and refuses a mismatch, so only the sessionless case
  can land silently, and a post can neither be recalled nor its mentions
  unfired. Warning, never refusal; NOT suppressed by `--json`, since stderr
  leaves the stdout contract untouched. The `--json` shapes are unchanged
  and the reads issue no extra call under `--json`.
  **Team attention is retired** (cli#935; hadron-server#2027, decision
  issuecomment-6567317725). `team attention [--since <token>]`,
  `team attention switchover preview|apply` and `team router poll|loop|status`
  remain as discoverable stubs with no expiry. Each exits 2 (Usage) with a fixed
  retirement message (the standard error envelope under `--json`), makes no
  request, reads no config, credentials, binding or state directory, types into
  no terminal, and never echoes a `--since` or `--proof` value. Old flags are
  accepted and ignored so the message is reached; an unknown flag is an ordinary
  usage error. There is no replacement unread or token view: read your own chat
  with `team chat read`. Wakes come from tickers (hadron-server#2138).
  **A bound worker's `chat read` now marks its messages read on the server**.
  It happens only
  AFTER the messages were printed, for exactly the reads that record the
  binding's watermark (unfiltered, contiguous, not `--before`, own App, and a
  binding that records this server — a `--limit` page included), through the
  highest seq shown: the read itself carries no session, so a read that fails
  partway marks nothing (a
  repeated unread, never a lost message). An older server that refuses the
  mark skips it silently; other mark failures produce a stderr note, never a
  failed read. For the
  reads that don't count, **`chat mark-read --through <seq> [--channel <ref>]`**
  advances the bound worker's cursor explicitly
  (`--channel` defaults to the App's team chat; an EMPTY `--channel` is refused,
  exit 2, rather than falling back to it). It is monotonic — a lower seq
  changes nothing and says so — and a seq past the Channel's head exits 2
  (`SEQ_BEYOND_WATERMARK`). It needs a binding that records this server (exit 2
  without one), and the server accepts only your own live session. On the App's
  team chat (no `--channel`) a successful mark also moves the binding's local
  read hint, the watermark `session log` compares against, up to `--through`
  (cli#1854): never backwards, never for `--through 0`, and not when the server
  refuses the mark; a named `--channel` leaves it alone. `--json`: `{workerId, channelId,
  through, lastSeenSeq}` (unchanged).
- `user search <query>` finds users (enumeration-safe: substring on handle /
  GitHub username, exact on email) — the way to resolve a user ID for `org
  member`/`memory member`/`memory share`. **Omit the query** (also spelled `user
  list`) to list every user, the unfiltered listing a platform ADMIN/OWNER may
  run; a caller without platform authority gets an empty list, not an error.
  Without an explicit `--limit`/`--offset` it pages to exhaustion, so the
  population is never truncated at one server page; either flag selects a single
  explicit page. Each user carries `identityProvider`, `githubId`, `externalId`,
  `externalAppId`, and `linkedAt` alongside the profile fields — read those before
  `user merge`, since a login only survives a merge if the target's corresponding
  field is empty. `profile set [--name|--email|--handle]` updates YOUR own
  profile; only the fields you pass change.
- `access check <user> <resource>` answers "what access does this user have to
  this resource?" — the authoritative, server-computed effective access plus the
  grants that confer it (no client-side re-derivation). `<user>` is an id, email,
  or handle (resolved via `searchUsers`); `<resource>` is a fully-qualified URN
  — `hrn:mem:…`, `hrn:node:…`, `hrn:app:…`, `hrn:agent:…` (the legacy
  `hrn:memory:<org>::<slug>` is also accepted) — or a bare
  AiServiceConfig id. Output carries `canRead/canWrite/canManage/canDelete`, a
  `role` label, and a `grants[]` array (each `{source, role, via}`); an empty
  `grants[]` is the first-class "no access" answer. Reading it requires audit
  rights on the resource (platform admin, the owning org's ADMIN/OWNER, or a
  strict-owner memory's principal) — otherwise the server's `FORBIDDEN` surfaces
  as exit 8 (#619; it was exit 1 before the permission-denied class existed). An unresolvable resource is exit 4; an under-qualified resource ref
  (e.g. `acme.com:kb` with no `hrn:` prefix) is a usage error (exit 2).
- **Headless runs** (spec-040, `cor:agt:010`) drive an App off any interactive
  session — the open-source counterpart to the portal's run surface. A *run*
  executes an entry (prompt) node under an App's identity; `run`, `schedule`, and
  `webhook` are three triggers into the same kernel, and `ticket` is the
  action-budget ledger.
  - `run trigger --app <ref> --entry <node-urn>` fires a MANUAL run now and prints
    its id; `--arg k=v` (repeatable, value parsed as JSON or string) sets template
    args, `--ai-config <n>` picks a named config, `--wait` polls to a terminal
    status (`--wait-timeout`, default 5m; a timeout is exit 6). `run list` is the
    audit surface (scope `--app` XOR `--org`, filter `--status`, paged to
    exhaustion); `run get <id>` is the full record (budgets, policy, the run
    envelope `data` — fields extracted by flow nodes as the walk advances — and
    the failure payload); `run cancel <id>` is the kill switch (requires `--yes`).
    `run get`/`run list` also carry (additive `--json` keys) `failureReason`
    `{kind, summary, whoCanFix, nextStep}` (server text, verbatim; null when no
    failure), `costMicroCents` (exact decimal string, 1e-8 USD; `"0"` is a real
    recorded cost) and `budgetActionsInitial`/`budgetTokensInitial` (int). For
    all three, null means unknown; it must not be read as zero. Human `run list`
    adds COST and REASON (kind) columns; REASON is `unknown` for a FAILED,
    TIMED_OUT or CANCELLED run the server gave no reason for, `—` for a run that
    did not fail or is still in flight.
  - `schedule create --app <ref> --name <n> --cron '<expr>' --entry <node-urn>`
    registers a recurring trigger (5-field cron, evaluated in `--tz`, default UTC;
    one-time `--at` is not yet a server capability). `--policy '<json>'` is a
    trigger-layer allow-list (`{"allow":[…]}`, `cor:acl:040`). `schedule list
    --app`, `update <id>` (only the fields you pass change; `--enabled=false`
    disables, unset fields preserved), `rm <id>` (`--yes`).
  - `webhook create --app <ref> --name <n> --entry <node-urn>` mints an inbound
    trigger and prints the URL path + platform token **once** — they are never
    queryable again, so capture them (in `--json` they are `path`/`token`).
    `webhook rotate <id>` reissues the secret (old URL dies immediately; `--yes`).
    Rotation is person-only: worker sessions are refused (FORBIDDEN, exit 8),
    with or without an instruction; no flag authorizes it.
    `webhook list` never shows the secret; `webhook rm <id>` (`--yes`).
  - `task run <task>` renders a task node's prompt by default; `--app <ref>`
    instead EXECUTES it server-side — minting a MANUAL run under that App (a real
    LLM run) and printing the run id (`--json`: `mode:"execute"`, `runId`). Follow
    it with `run get <id>`. It's the fourth trigger into the same kernel.
    The default render records content reads and an action-run usage event.
  - `task render <node-ref>` calls only the `renderRunNode` Query, even with App
    context. Accepts a node ID/fully-qualified URN or a bare loc with `-m`. A
    qualified reference supplies its own memory; `-m` composes only a bare loc
    (a conflicting `-m` is ignored, not refused).
    `--args` is one JSON object inline or from `@file`, parsed before requests;
    omitted args stay omitted, `{}` stays an empty object. Raw stdout is the
    returned string verbatim (no added newline), including empty success;
    `--json` is `{"rendered":"…"}`. A successful reply with no `data` key
    (`{}`), `{"data":null}`, empty data (`{"data":{}}`) or a null `renderRunNode`
    breaks the server's `String!` contract: it exits 1, is not retried, and
    renders nothing. The refusal is the message
    `the server's reply had no renderRunNode (missing or null), so nothing was
    rendered`, shown like any other error: on stderr in text mode (stdout stays
    empty), and as the standard error envelope on stdout with `--json` (never a
    `rendered` document). A valid `""` is still a
    success. The server records content reads for the
    source and actually inlined dependencies, but no action-run or run row.
    No LLM call, local assembly/filtering, retry or legacy-renderer fallback.
    Refusals retain shared exit/envelope mapping (concealed missing 4,
    Forbidden 8, bad input/unsupported field 2). Provider assembly and admission
    remain server-owned; a render does not promise a later execution's prompt.
  - Per-trigger run limits (cli#1194, server#2479): `--budget-tokens N` /
    `--budget-actions N` (positive whole numbers) on `run trigger`, `schedule
    create|update`, `webhook create`. Omitted = platform default (create, trigger) or
    preserved (update); `schedule update --clear-budget-tokens|--clear-budget-actions`
    sends an explicit null (back to the default); a value with its clear flag, or a
    value <= 0, is exit 2 before any call. Above-default values are NOT checked
    client-side: the server's BAD_USER_INPUT refusal passes through (exit 2).
    `schedule list` / `webhook list` show TOKENS/ACTIONS (`default` when none) and
    `--json` carries `budgetTokens` / `budgetActions` (null = platform default).
    Needs a server with trigger budgets (hadron-server#2780).
  - `--agent <ref>` (cli#1268, hadron-server#2849; on `run trigger`, `task run
    --app`, `node import --task`) names the installed Agent (ID or URN) a manual
    run is served as. Omitted, no `agentRef` is sent and the App's single
    installed Agent serves it; an App with several refuses `RUN_AGENT_REQUIRED`
    (exit 2, the message says to pass `--agent`; `hadron app agent list <app>`
    lists the App's installed Agents). An Agent that is unknown or not
    installed in the App is the server's BAD_USER_INPUT, its message shown (exit
    2). A blank `--agent`, or `--agent` without `--app` (task run) or `--task`
    (node import), is exit 2 before any call.
  - `--as-self` (on `run trigger`, `schedule create`, `webhook create`,
    `task run --app`) makes the
    run act on behalf of YOU — required to reach your personal memories, and only
    usable by an authenticated user; an App-key caller gets `UNAUTHENTICATED`
    (exit 1). v1 never delegates a third party (`cor:agt:010:01`).
  - `ticket mint --org <ref> --action comm.outbound --count <n>` mints consumable
    action tickets into the org ledger (org ADMIN; `cor:acl:050:04`; `--app`
    scopes to one App, `--note` records why, `--expires` sets an ISO expiry);
    `ticket list --org <ref>` is the ledger — minted / consumed-by-which-run /
    expiries, paged to exhaustion.
  - `grant create --org <ref> --user <ref> --action memory.clone[,...]` hands one
    org member extra management actions on top of their role bundle (org ADMIN,
    interactive-only; the grantee must be a live member — a grant dies with the
    membership). Actions use the matcher grammar: exact (`memory.clone`),
    prefix (`memory.*`), or `*`. `grant list` defaults to YOUR OWN grants
    (self-audit is never gated); org ADMINs pass `--org` for the whole org,
    optionally `--user` to narrow. `grant revoke <id> --yes` soft-deletes;
    takes effect at the next gate check.
  - Every entry node is a fully-qualified node URN (`hrn:node:<root>:<slug>:<loc>`,
    or the legacy `<org>::<memory>::<loc>`) — a bare loc is rejected (exit 2). `--app`
    defaults to the App context (`hadron app set-active` / `--app`) when omitted.


### Content-validation reports (cli#1706)

`node validation record` records an advisory result of a task already run; it
never runs that task or edits the node. It reads the current node/task revisions,
so record before either changes. The server derives the reporter and checks the
write, task and author gates. CHECKED only compares recorded authors; it is not
proof of independence. AUTHOR_UNKNOWN means the server could not compare them.
`node get` shows this comparison in the existing validation line and JSON report;
older servers fall back with an unknown (`null`) comparison.

Discovery uses the server's `Node.validationTasks` projection, available to
writers. It never reads manager configuration or infers tasks from past reports.
One distinct READY task is automatic; multiple tasks require `--task` from the
listed refs/revisions. NOT_CONFIGURED returns the existing local exit 5 without a
mutation. BROKEN blocks the entire set, even alongside healthy tasks (exit 5).
UNAVAILABLE or a null projection refuses generically with exit 8 and no mutation;
it does not identify whether access or an unreadable task caused the answer.
Malformed or unknown states fail closed (exit 1). An older server lacking this
field returns the existing schema-skew exit 2, without legacy discovery fallback.

Exit 2: the input or the choice is invalid (an unknown --outcome, a --task outside the listed set, an invalid revision or a wrong task).
Exit 5: no task is configured or the task is broken, the node changed during the call, or the --run-id conflicts.
An explicit --task never overrides a refusal of the task set itself (an empty set is
still exit 2 once --task is named; a BROKEN set is exit 5 and an UNAVAILABLE one exit 8, each without a write).
An explicit task outside the set exits 2. No client reporter field is sent;
a READY selection does not waive the mutation's author or access checks.
An HTTP 5xx answer (any status from 500 to 599) to the record request exits 7 even when its body is a JSON error: the outcome is unknown, so read the node's latest validation report before recording again (`hadron node get`). A repeat with the same `--run-id` is answered as a replay if the first attempt was recorded and the node and task revisions and your identity are unchanged, otherwise refused as a conflict; without `--run-id` it may record a second report. The message shows only the HTTP status and, when the envelope says INTERNAL_SERVER_ERROR, that code and its request id; nothing is retried.

`--run-id` is passed through, including on a deliberate retry. JSON is
`{nodeId,replayed,report:{nodeRevision,validationTaskRef,validationTaskRevision,
outcome,reportedAt,authorCheck},state}`. `replayed:true` is success and writes
nothing. No automatic retry occurs. Exit 4 = missing/unreadable node; 8 = access,
reporter or author refusal; 2 = invalid task/revision/input; 5 = configuration,
node revision or run-ID conflict. Exit 7 is an uncertain outcome: inspect before
retrying. Reports are advisory, not approvals, and history is outside this command.


## The escape hatch: hadron api

Anything the curated commands don't cover is reachable through raw
GraphQL against the Hadron API:

```
hadron api 'query { me { id email } }'
hadron api 'query($ref: ID!) { memory(ref: $ref) { urn name } }' -F ref=hrn:mem:acme.com:kb
cat op.graphql | hadron api -
```

`-F key=value` sets variables (values that parse as JSON are sent as
JSON, otherwise as strings). When the server answers HTTP 200, its
GraphQL response envelope, `errors` included, is printed to stdout
(indented) and the errors set the exit code. A null result with no errors
(an unreadable or absent record, `me` signed out) is exit 0, so check
`data`. When the server refuses with an HTTP error status (400 or above),
the server's envelope is not printed, and the exit comes from the body's
`extensions.code` where it has one. In text mode, stdout stays empty and
the status and the server's body go to stderr. With `--json`, the CLI's
`{"error": …}` envelope goes to stdout and stderr stays empty. A document
the server rejects as invalid (HTTP 400, e.g. an unknown field) is exit 2.
`hadron api` sends no App context (`--app` has no effect). A server answer
on stdout is JSON with or without `--json`.
`hadron api -` is for a PIPE: like `--content -`, it is **refused (exit 2)
when stdin is an interactive terminal** (#648), whose line discipline can
truncate a large document before the CLI reads it. Through a PTY, use
`--input <path>`.

## App context (optional)

Some Hadron deployments scope requests to an App. By default the CLI
sends no App context, which the server treats as fine. Set a default
with `hadron app set-active <ref>` or override per-invocation with
`--app <ref>` (an App id, `hrn:app:<root>:<slug>`, or the `<root>:<slug>`
short form).

## Reading an encrypted memory (`--unlock`, cli#1234)

`--unlock <memory-ref>` is a GLOBAL flag (any command, `hadron api` included),
repeatable up to 8. It supplies an encrypted memory's passphrase for THIS
invocation only: the CLI sends `{memory, passphrase}` entries in the
`x-hadron-memory-keys` header of every GraphQL request the invocation makes
(cor:sec:040:04; a key is never a GraphQL field). Ruling 0129: nothing is
cached between invocations, and the passphrase is NEVER an argument or an
environment variable; there is no flag that takes it. Sources, one per
invocation:

- a terminal on stdin and no source flag: a no-echo prompt per memory
  (`Passphrase for <memory>:` on stderr);
- `--unlock-passphrase-file <path>`, one per `--unlock`, paired in order. The
  file must be a regular file only you can access (no group/other permission
  bits: `chmod 600`, the rule ssh applies to a key); otherwise exit 2. Trailing
  CR/LF is trimmed, exactly as `memory encrypt --passphrase-file` does. Not
  available on Windows (permissions cannot be checked): use the prompt or stdin.
- `--unlock-passphrase-stdin`: reads ALL of stdin as the passphrase for a
  single `--unlock` (two or more is exit 2); the command then cannot also read
  stdin.

Every local refusal is exit 2 with NOTHING sent: a 9th `--unlock`, a duplicate
memory, an empty ref or passphrase, a source flag without `--unlock`, stdin
with a file, a file count that differs from the `--unlock` count, an
accessible or unreadable file, no source on a non-terminal, or a header over
the server's 16 KiB. Memory refs are canonicalized (`<root>:<slug>` →
`hrn:mem:<root>:<slug>`); an id passes through. Only the memory's OWNER can
unlock it.

The server checks the header before running the operation. Its refusal is
typed and never retried; the message is fixed per code, never the server's
text, and `--json` carries `serverCode`:

| serverCode | exit | meaning |
|---|---|---|
| `WRONG_KEY` | 2 | a passphrase does not match its memory |
| `BAD_UNLOCK_INPUT` | 2 | an entry was unusable (e.g. a raw-data-key memory) |
| `NOT_ENCRYPTED` | 2 | an `--unlock` memory is not encrypted |
| `NOT_FOUND` | 4 | not found, or not yours (uniform) |
| `UNAUTHENTICATED` | 3 | not signed in |

The server does not say WHICH entry failed. The passphrase and the encoded
header are scrubbed from every error-bearing answer before it can be printed
(a non-200 body whole, a 200's `errors` list); successful data is never
rewritten. A key-bearing request is never sent over cleartext http (loopback
excepted) and never follows a redirect to another host or to http. The CLI has
no verbose/debug request dump. Without `--unlock`, requests are unchanged.

## `coding review run` — the checklist against a diff (#551)

Returns the review checks a diff could fire, **with their bodies**, in one
response. `--diff <path|->` supplies a unified diff (stdin with `-`); otherwise
the change set comes from git (`--base`/`--head`, defaulting to the merge base
with the default branch, and untracked files are included when the head is the
working tree). `--diff -` is for a PIPE and is **refused (exit 2) from an
interactive terminal** (#648): a truncated diff silently drops changed files, so
the run would select checks for less than the real change. The same refusal
covers `coding review create` / `preflight create --content -` (use
`--content-file`).

Applicability is decided STRUCTURALLY — path patterns the check names, never an
interpretation of its prose — into three buckets:

- `matched` — a named path was changed; `matchedOn` carries the (pattern, file)
  pair that fired, so the decision is auditable rather than asserted
- `undecided` — the check names no paths, so nothing structural speaks to it.
  **Returned in full**: "cannot tell" is not "does not apply"
- `excluded` — the check names paths and the diff touches none. The only bucket
  that removes anything, reported in `excluded` with its patterns, never
  silently dropped. `--all` returns these too

Most checks in a mature checklist land in `undecided` (measured: 83–100% across
the four corpora that have one), because checks describe code shapes more often
than file locations. **The filtering is a convenience; the one-response delivery
of bodies is the guarantee.**

`--json` is an OBJECT (unlike `review list`'s array): `memory`, `memorySource`,
`base`/`head`/`diffSource`, `changedFiles`, `total`/`returned`/`nextOffset`,
`checks[]` (with `content`, `patterns`, `matchedOn`, and `portalUrl` when the
deployment has a portal), `excluded[]` and `unavailable[]`. `nextOffset` is
non-null ONLY when `--limit`/`--offset` withheld checks, so a truncated read is
never silent.

## Repository-scoped memory for `hadron coding` (#551)

`coding`'s `-m/--memory` is optional. Run from a checkout and the memory is
resolved in this order, first hit winning:

1. `-m/--memory`
2. `memory` (or `coding.memory`) in `.hadron/config.json`, searched upward from
   the working directory — the same file `chat` reads
3. the configured memory (`hadron config set memory …`)
4. the git remote's repository NAME, matched against the memories you can read

Step 4 is an EXACT slug match, never fuzzy: `mm-app` resolves
`hrn:mem:micromentor.org:mm-app`, and `widget` never resolves `widget-app`. Two
matches refuse with the candidates listed rather than picking one, so an
ambiguous repo costs one `-m`, not a full `memory list`.

Steps 1–3 are local and never touch the network. Step 4 needs a read, and its
failure is NOT the answer: if the client cannot be built or the query does not
come back, you get the ordinary usage refusal naming every source that was
tried — not `AuthRequired` or exit 7 for a question about your arguments.

**Whichever ambient branch answers, the resolved memory and its source are
printed to stderr before the payload** (`using memory <ref> (from …)`), including
when the result is empty. It is on stderr precisely so `--json` does not move:
`coding review list --json` is a top-level array and stays one.

## Recipes

```
# Am I authenticated, and as whom?
hadron auth whoami --json

# List memories, machine-readable
hadron memory list --json

# List memories OTHER users shared with you (a separate slice — your own
# memories are never in it), with the role you were granted and who shared it
hadron memory list --shared-with-me --json

# Inspect one memory by URN
hadron memory get acme.com:project-memory --json

# Export a whole memory to local markdown files (one .md per node)
hadron memory export acme.com:project-memory --out ./kb --json

# List nodes in a memory
hadron node list --memory hrn:mem:acme.com:kb --json

# Read one node's content and edges
hadron node get hrn:node:acme.com:kb:findings:flaky-ci --json
hadron node validation record <node-ref> [-m <memory>] --outcome PASS|FAIL|ERROR [--task <ID-or-listed-URN>] [--run-id <key>] [--json]

# Create a node from stdin
cat finding.md | hadron node add -m hrn:mem:acme.com:kb --loc findings:flaky-ci \
  --name "Flaky CI" --content -

# Update just the name (other fields preserved)
hadron node update hrn:node:acme.com:kb:findings:flaky-ci --name "Flaky CI (resolved)"

# Author structured storage (#725, the write half of the --where/--object-type
# query surface). Define a memory's property schema (declared collections +
# typed fields), then write conforming records — the server validates
# objectType + properties against the schema. `node get` reads them back.
hadron memory set hrn:mem:acme.com:research --schema-file schema.json
hadron node add -m hrn:mem:acme.com:research --loc competitors:acme --name Acme \
  --object-type competitor --properties '{"tier":"enterprise","seats":50}'
hadron node update hrn:node:acme.com:research:competitors:acme --properties '{"tier":"pro","seats":12}'
# --properties REPLACES the bag (like --data, not --data-merge); pass "null" to
# clear it, --object-type "" to make a node ordinary again. There is no
# --properties-merge yet (blocked on the server-side updateNodeProperties
# mutation, hadron-server#742). Schema + properties work on encrypted memories
# too (both are plaintext).

# The `object` group is the legible, collection-oriented sugar over the same
# structured storage (#745). An object IS a node with an objectType, presented
# as a flat record { id, type, ...fields } — id/type reserved, loc/name hidden.
# create validates against the schema; update is an atomic shallow MERGE (unlike
# node --properties, which replaces); find desugars --match/--sort to the
# where/sort-property grammar. Use it when you want records, not graph nodes.
# find returns 20 per page by default (--limit up to 200). "shown" is THIS
# page's count, never cumulative: advance --offset by each page's shown and
# stop when offset + shown >= total (or a page shows 0; total can be null).
# {"f":null} keeps the key with a null value (drop a key by replacing the bag:
# node update <ref> --properties). A soft delete is
# undone by `node restore <id|urn>`; --hard is final.
hadron object create -m hrn:mem:acme.com:research --type competitor \
  --fields '{"name":"Letta","stage":"series-a","fundingUsd":12000000}' --key letta
hadron object update hrn:node:acme.com:research:competitor:letta --fields '{"stage":"series-b"}'
hadron object find -m hrn:mem:acme.com:research --type competitor \
  --match '{"stage":"series-b"}' --sort '{"fundingUsd":"desc"}' --json
hadron object get hrn:node:acme.com:research:competitor:letta   # flat { id, type, ...fields }

# Move a node (keeps its id + edges); clone it to a new memory (new id)
hadron node move hrn:node:acme.com:kb:findings:flaky-ci --to-urn hrn:node:acme.com:kb:archive:flaky-ci
hadron node clone hrn:node:acme.com:kb:templates:base --to-memory hrn:mem:acme.com:sandbox --json

# Fold a duplicate node into the canonical one (agents must pass --yes)
hadron node merge hrn:node:acme.com:kb:findings:dup --into hrn:node:acme.com:kb:findings:canonical \
  --field CONTENT --field EDGES --delete-source --yes

# Bulk search-and-replace across a memory. A real run previews + prompts;
# agents pass --dry-run to preview or --yes to apply non-interactively. Even
# with --yes the affected node count is printed to stderr before writing;
# --max-nodes N refuses the write if more than N nodes would change (a guard
# against a whole-memory rewrite from a wrong -m URN or a forgotten --prefix).
# Each changed node first gets a revision holding its previous text (labelled
# with --reason): undo with `node revision restore <that revision-id>`.
# Comments, governed nodes (specs, reviews), compiled boot prompts and protected
# addresses are skipped silently: not scanned, not counted, no skip count shown.
hadron replace text "old-url.com" "new-url.com" \
  -m hrn:mem:acme.com:kb --field content --field description --dry-run

# Connect two nodes
hadron edge add --from hrn:node:acme.com:kb:findings:flaky-ci \
  --to hrn:node:acme.com:kb:start-here --name routes-to

# List a node's edges, delete one (agents must pass --yes)
hadron edge list hrn:node:acme.com:kb:findings:flaky-ci --json
hadron edge rm <edge-id> --yes

# Delete a node (agents must pass --yes). Soft by default (recoverable from
# version history); --hard removes the row + its edges + version history
# irreversibly. Deleting a node that HAS descendants is refused (exit 2,
# NODE_HAS_DESCENDANTS naming the count) unless you pass --recursive/-r, which
# deletes the whole subtree under its loc.
hadron node rm hrn:node:acme.com:kb:findings:flaky-ci --yes
hadron node rm hrn:node:acme.com:kb:data:stale --hard --yes
hadron node rm hrn:node:acme.com:kb:findings --recursive --yes    # the branch + all children

# Revision history: list a node's snapshots, then inspect, label, or restore one.
# restore is undoable by default; --truncate discards newer history (needs --yes).
# Pass a bare node id to reach a soft-deleted node's history for cleanup.
hadron node revision list hrn:node:acme.com:kb:findings:flaky-ci --json
hadron node revision get <revision-id>
hadron node revision label <revision-id> --label "before the auth refactor"
hadron node revision restore <revision-id>
hadron node revision restore <revision-id> --truncate --yes
hadron node revision clear hrn:node:acme.com:kb:findings:flaky-ci --yes

# Hard-delete edge audit (hadron-server#2517, built in #2562): one entry per edge a HARD delete
# removed from this node, newest first. A nil edgeLoc/targetLoc in --json
# (rendered "(withheld)" in text) is CONCEALMENT — the target's memory is not
# readable — not missing data; targetId always names the deleted target.
# Pages like revision list (default 20, no upper bound; --limit 0 is an empty
# page). node revision list now has --offset and the real total (cli#1339).
hadron node edge-removals hrn:node:acme.com:kb:findings:flaky-ci --json

# Ranked search (hybrid semantic+keyword by default; scores + abstracts in --json)
hadron search "how do users report a bad actor" -m hrn:mem:micromentor.org:mmdata --json

# Structured property/attribute queries (parity with the server #719 `where`
# predicate). --where is a raw-JSON predicate over the node's properties/data
# JSONB: a LEAF is {"path":[...],"<op>":<value>} with one operator of
# eq|ne|in|lt|lte|gt|gte|between|exists|contains, optional "field":properties|data
# (default properties) and "as":text|number|datetime|boolean (default text); a
# BRANCH is {"and"|"or":[...]} or {"not":{...}}. --object-type filters the
# objectType collection facet; --sort-property orders by a JSON path (overrides
# relevance/loc sort). Available on both `node list` (browse) and `search` (ranked).
hadron node list -m hrn:mem:acme.com:kb --object-type insight \
  --where '{"and":[{"path":["source"],"eq":"substack"},{"path":["capturedAt"],"as":"datetime","gte":"2026-07-04"}]}' \
  --sort-property '{"path":["capturedAt"],"as":"datetime","direction":"desc"}' --json
hadron search "pricing" --object-type competitor --where '{"path":["tier"],"eq":"enterprise"}' --json

# Arbitrary query with a variable
hadron api 'query($q: String!) { findNodes(query: $q) { hits { node { loc name } } } }' -F q="auth flow"

# Drive a headless run: fire an entry node now and wait for the result
hadron run trigger --app acme.com:ops \
  --entry hrn:node:acme.com:ops:tasks:nightly-digest --arg topic=security --wait --json

# Schedule it nightly (on behalf of you, so it can reach your personal memories)
hadron schedule create --app acme.com:ops --name nightly-digest \
  --cron '0 6 * * *' --tz America/New_York \
  --entry hrn:node:acme.com:ops:tasks:nightly-digest --as-self

# Inspect what ran and why (the audit surface)
hadron run list --app acme.com:ops --status FAILED --json
hadron run get <run-id> --json

# Mint an outbound-comms budget the runs consume
hadron ticket mint --org acme.com --action comm.outbound --count 100 --note 'digest sends'
```


## Governed comments (`hadron comment`)

```
hadron comment create <target-ref> --body <text|->|--body-file <path> [-m <memory>] [--quote <text>] [--anchor-revision N]
hadron comment add <target-ref> --body <text|->|--body-file <path> [-m <memory>] [--quote <text>] [--anchor-revision N]
hadron comment reply <comment-ref> --body <text|->|--body-file <path> [-m <memory>]
hadron comment edit <comment-ref> --expected-revision N [-m <memory>] [--body <text|->|--body-file <path>] [--quote <text>]
hadron comment delete <comment-ref> --expected-revision N [-m <memory>] [--yes]
hadron comment delete <comment-ref> --thread --reason <text> [-m <memory>] [--yes]
hadron comment resolve <root-ref> --expected-revision N [-m <memory>] [--reopen]
hadron comment get <comment-ref> [-m <memory>]
hadron comment list <target-ref> [-m <memory>] [--state OPEN,RESOLVED] [--limit N] [--offset N]
```

**Leaving feedback at the end of a task: use `comment add`, not `create`.** `add` is
one call that works whether or not you already hold an open thread on the node
(`create` is refused with `COMMENT_OPEN_THREAD_EXISTS`, exit 5, when you do).
It sends one `createComment`; if the server answers with that typed conflict and
the thread's `threadId`, it sends exactly one `replyToComment` to that thread.
Any other failure is returned unchanged (same exit code) with no reply and no
retry. Exit 0 on both outcomes. `--json`:
`{"outcome":"started"|"replied","threadId":"<id>","composedQuote":<bool>,"comment":{…}}`
(`threadId` is the new comment's id when started, your existing thread's id when
replied). A reply has no quote or anchor field, so on the reply path `--quote`
and `--anchor-revision` are written as a Markdown blockquote above the text
(`composedQuote: true`). If the reply itself is refused (thread deleted, access
lost) the exit code and server code are the reply's, the message names the
thread, and the JSON error carries `extensions.threadId`. If your thread was
resolved in the instant between the two calls the reply is recorded and the
thread stays resolved: `add` guarantees the feedback is on the node, not that it
is listed as unresolved. A conflict whose `threadId` contains control, format or
separator characters (or is over 256 runes) is not a usable thread: the original
refusal is returned and no reply is sent; the human refusal line renders any
`currentRevision`/`threadId`/`class` detail with such characters replaced by spaces
(`--json` keeps the original value, JSON-escaped). A success body with no usable id (for
example `{}`) is not a recorded outcome: `add` exits 7 (outcome unknown, read
before retrying) and prints nothing; after a conflict the message names the
thread. A lost reply answer, timeout, 5xx, missing result or ambiguous
post-write refusal says the outcome is unknown: read the thread before
retrying, since the reply may already be recorded. Only established pre-write
refusals say "failed". Exit codes, server codes and JSON fields are unchanged;
there is no automatic retry. `comment create` is unchanged and no longer has an
`add` alias.

Refs are node IDs, qualified node URNs, or bare locs with `-m`. Create anchors
to the current target revision unless `--anchor-revision` chooses a retained
one. A reply to a reply joins the same (one-level) thread; replies to resolved
threads remain resolved; a deleted root takes no replies (exit 5). Each author
may hold one open thread per target node. Editing preserves omitted
body/quote; `--quote ""` clears the quote on edit and is omitted on create. `--body -` requires piped stdin;
use `--body-file` interactively. Guarded writes require the comment's own
observed write counter (`revSeq`), not its target or anchor revision; conflicts
exit 5 and are never retried automatically. Delete is available to the author
or a memory manager. Deletion prompts on a terminal and requires `--yes`
non-interactively. A deleted root remains a text-less stub only while live
replies exist; the server removes it with its last live reply. There are no
hide/retract aliases and comments keep no revision history.

`comment delete --thread` deletes the whole thread of the comment it names and
prints `{"thread":"<ref>","deleted":true}` (`--json`) with exit 0; thread is the
comment ref you named. Two outcomes print no receipt on stdout (with `--json`,
the standard error envelope) and are not successes: an answer that carries no result is
exit 1, outcome unknown, so read the thread (`comment get`) before retrying and
never retry blind; an explicit `deleted:false` is exit 5, not deleted (before
cli#2543 that answer exited 0 with `{"thread":…,"deleted":false}`). Missing
`--reason` or `--yes` exit 2, no permission 8, a comment that is missing or unreadable 4.
Authorship is server-derived; bound comment writes use the #822 attribution
hook. A generic comment node/edge write exits 2 (`ROLE_GOVERNED`) and points to
this group. Permission/author/admin refusals exit 8; missing or unreadable
comments/targets exit 4; invalid inputs exit 2; changed thread state, existing
open thread and unavailable historical anchor exit 5.

`anchorApprovalHash` is a string or JSON null: encrypted targets have no hash;
plaintext hashes are preserved exactly. The key is always present. This replaces
the previous empty-string representation of an absent hash. Text output continues
to show the anchor revision and currentness without a fingerprint.

JSON uses explicit comment fields including `revSeq` (the current write counter), public `author`,
`provenanceUser`, target/anchor metadata, state, nullable body/quote ; list emits `{items, total}` with each thread's root, replies
array and server replyCount. `deleted`/`deletedAt`/`deletedBy` describe deletion;
`editedAt`/`editedBy` describe the last body or quote edit (resolve/reopen do
not set them). Human output labels old anchors, stubs and the last edit.
`node get` adds an advisory `commentSummary` cue (open/resolved threads and
comment count); it is null/unavailable on older servers or failed optional
reads. Feedback changes independently of the target revision.
`search <query> --comments-only` searches feedback via the server's
`NodeFilter.contentScope=COMMENTS`, before ranking and pagination in every mode.
Other filters still narrow the results; a non-comment `--type` with this flag
is refused (exit 2) before connecting. Without the flag, contentScope is omitted
and the server default applies (CONTENT, or COMMENTS for explicit `--type comment`).
Search JSON keeps its existing hit DTO, including `nodeType: "comment"`; human
comments-only output labels the results as feedback, not verified target content.
Older servers without this filter refuse the opt-in; ordinary searches omit it.

Comment refusals expose their bounded recovery details on curated output:
`error.extensions` in JSON retains `code` and any supplied `currentRevision`,
`threadId` or `class`; human errors label those values. Other extension fields
are not forwarded. `COMMENT_MERGE_FOLDS_THREADS` exits 2: the caller must
resolve the anchored-thread conflict before merging, rather than retry blind.


## Orca terminal status (`hadron orca terminal status`, cli#1187)

**An Orca integration command.** It reads terminals of the Orca app, so it works only on the host that
runs Orca and needs the `orca` CLI on PATH (or `delivery.orca_bin` with `--config`). Off that host it exits **10**
(no output). It only lists and reads terminals: it never types and never acknowledges a wake.
Its monitoring due-wake read is a preview: no wake reservation, no budget withholding and no budget
notice. It delivers no wake and cannot be acknowledged. The server must support dueWakes(preview)
(since hadron-server#3755); rejection fails closed with a schema-version hint, without a spending retry.
An App-key dueWakes read is recorded when the server answers it (which key, how many workers, when);
a refused one is not recorded. With `--config`, the ticker and subscription reads are each recorded as a host-key use, refusals included; the
`workers` read is not recorded. Reach check without guessing a name: `hadron orca terminal status no-such-worker` lists Orca first once the
arguments are accepted, so exit 10 = Orca unreachable (`orca` not on PATH, app not running, no answer in 15s) and
exit 4 = Orca answered; exit 2 or 3 is refused before Orca is listed and says nothing about Orca. `--all` is one JSON array only with `--json`; without it, one text line per worker.

`hadron orca terminal status <worker|terminal-id> [--config <host.yaml>] [--json]` or `--all` (with `--json` one JSON array, one entry per
active worker of the App, in the App's worker order). The terminal is the Orca terminal whose worktree's
session binding names the worker, or one named directly by id. Text mode prints one line per worker:
`Joshua idle`, `Joshua locked (menu_or_question)`, `Joshua locked (rate_limited, until 08:45Z)` (a usage-limit screen; ", read as the host's local time" is added when the screen's time has no UTC offset); a `dark` line also carries the local detail.

```json
{"contract":"terminal-status@1","worker":"Joshua","terminal":"term_50a6…","status":"idle",
 "prompt":"empty","reason":null,"lastTurnAt":"2026-10-29T16:12:03Z","checkedAt":"2026-10-29T16:31:40Z","resetAt":null}
```

| key | values |
|-----|--------|
| `contract` | always `terminal-status@1`; a breaking change ships as `@2` |
| `worker` | the worker's name; `null` for a terminal bound to no worker |
| `terminal` | the Orca terminal handle; `null` when the worker has none or has several agent terminals |
| `status` | `busy` a turn is running · `idle` empty prompt, safe to push one line · `locked` cannot take work · `dark` broken |
| `prompt` | `empty` · `not_empty` · `menu` · `unknown`: the terminal-level fact, separate from `status` |
| `reason` | `null` unless `locked`/`dark`. `locked`: `menu_or_question` (a selection menu, permission dialog or pending question: never send), `rate_limited` (a usage-limit screen: waiting on a reset, not stopped; never send; `resetAt` says when), `no_screen` (Orca gave no rendered screen, a read whose source is `screen-unavailable`, so nothing could be matched; never idle, never send; cli#2677), `multiple_terminals` (more than one agent terminal is bound to the worker, so `terminal` is `null`; cli#2677), `unrecognized` (blocked, or a real screen no rule matches: fail closed, never idle; a consumer that keyed on `unrecognized` for the two cases above sees the new values instead, and both are `locked` too). `dark`: `no_turn_for_due_ticker`, `no_terminal`, `bare_shell`, `no_agent` |
| `lastTurnAt` | when the harness's screen shows its last finished turn (Claude Code, Codex adapters); `null` when not shown, never guessed. It does not influence `status` |
| `checkedAt` | when the status was produced, UTC |
| `resetAt` | an additive key inside `@1` (cli#2676): when a `rate_limited` screen says the limit resets, RFC 3339 UTC; `null` otherwise, and for a usage-limit screen with no parsable time. A time with no UTC offset is read as the host's local time. Consumers that read only the other keys keep working |

Precedence: `locked`, then `busy`, then `dark`, then `idle`. **Push only when `prompt` is `empty` and
`status` is `idle` or `dark`.** `dark` means a ticker owed the worker a turn and none came: a tick of a
ticker the worker is subscribed to has been due and unanswered for at least the interval of **the worker's
fastest subscribed ticker** (each tick reason's own `tickDueAt` in `dueWakes`, that ticker's oldest unacknowledged tick; never `anchorAt`, which is the oldest reason of any kind, and never `Ticker.lastTickAt`).
The monitoring due-wake read always sends preview:true (cli#1984): it reserves no wake budget,
withholds no due wakes and posts no budget notice. It does not acknowledge or deliver wakes.
The host delivery poller keeps its spending read; the public team wakes due preview flag is separate.
Task wakes include CHILD_CANCELLED beside CHILD_FINISHED. The due list keeps
the TASK <handle> <id> <event> format; delivery says "child cancelled". JSON
keeps the server event, and unknown events print as returned by the server.
An ANSWERED task wake (a question that blocked the task was answered, hadron-server#4431) is
delivered as `[hadron task] <handle> a question this task asked was answered: <code>`: the task
handle, fixed words and the question code (`DueTaskReason.questionCode`; the sentence ends after
"answered" when there is none), never the title or the answer. `team wakes due` is unchanged.
A merged wake (a tick plus a mention, a task or another ticker's tick) is judged per ticker by those times. A tick reason without `tickDueAt` (a server before hadron-server#2790, or withheld), with a malformed one (anything outside strict RFC 3339 with `Z` or an offset up to ±23:59), or without its `ticker` has no age evidence, so an otherwise-idle worker fails closed to `locked`/`unrecognized`. A worker with nothing due is never dark, however old its last turn. Also `dark`: no terminal, a bare
shell, an agent that is not connected.

Fail closed: a terminal Orca reports as not writable, both agents' chrome at once, a shell that shows no shell prompt, missing subscription data, and a screen the classifier does not positively recognize is `locked`/`unrecognized`, and so is
an otherwise-idle worker whose due-wake evidence could not be read or was refused for this caller (a
coordinator asking about another worker is gated by the server). A busy or locked worker keeps its
status. A terminal named by id that no worker's binding claims has no ticker evidence, so it is never `dark` by the ticker rule. Text mode prints `scope: App <ref> (from --app | the App context | a worker-session binding)` on stderr whenever it read the server; JSON mode prints nothing extra. If the wake budget is spent (`WAKE_BUDGET_EXHAUSTED`) due wakes are withheld, which also fails an otherwise-idle worker closed. The reason codes `model_at_capacity`, `rate_limited`, `token_budget_exhausted`,
`guardrail_triggered` and `shed_staged_not_relaunched` are deferred until a captured screen exists for
each: such a screen is `locked`/`unrecognized` (or `idle`/`busy` where the existing completion and
running-turn rules already say so). A fresh Codex launch whose banner has scrolled out of the
idle-proof window is `locked`/`unrecognized` too, as delivery treats it.

**`--config <host.yaml>` (cli#1378)** is how the sweep runs unattended under cron or systemd, with a path
and nothing else. The file is the one `team host run --config` reads. Orca is `delivery.orca_bin` when set
(it must be absolute, even with delivery off: exit 2) and it resolves, else `orca` on PATH (cli#2166: a path that is gone, a directory or not executable falls back). The server is read with the
host's `host:operate` App key, read only from `key_file` (mode 0600 or tighter) and sent as the bearer on a
client that carries no session and never `HADRON_TOKEN` or the stored token. The key is never in argv, the
environment or a file descriptor of the Orca subprocess, nor in any output. The App is the file's: `--app`
with `--config` is exit 2 with nothing read; the `scope:` line reads `App <ref> (from --config <path>)`, a
terminal bound to another App is judged against the file's App and fails closed, and `--all` lists only the
App's active workers the file configures (name, URN or id, the delivery matcher). Exit 2: empty path, bad
file, relative `orca_bin`. Exit 3: no `key_file`, missing, empty or too-open key file, all before Orca is
listed. Without `--config` nothing changes. **What a host key reads:** the dark rule needs two reads from it and both must pass. (1) Tickers and
their subscribers (`host:operate`), read-only, for the workers its own host's registration lists (a server
before hadron-server#2931 refuses it, `APP_NOT_FOUND`). (2) `dueWakes` (`attention:deliver`, a refusal is
`APP_KEY_SCOPE_REQUIRED`), again only for the workers its own host registers; a key bound to a session is
refused. The ticker and subscription reads are each recorded as a host-key use, refusals included; an answered
`dueWakes` read is recorded in the wake-read record, and a refused one is not recorded; the `workers` read is not
recorded. So the due-wake read is not the only record. If either read is refused an otherwise-idle worker fails closed to `locked`/`unrecognized`, exit 0,
and so does a configured worker the registration does not list.

The output carries codes only. The local `detail` (the ticker and due time behind a `dark`, or why a read
was refused) is never in the JSON, so a sweep can never send screen text anywhere. The team health sweep
(#1181) consumes this JSON and never imports the classifier.

## Orca worker onboarding

`team worker onboard` is Orca-hosted workers only. It defaults to dry-run,
submitting nothing and changing no files. It requires an already cast, held
worker, live host registration and configured launch argv/repo mapping, and
the worker's mention-only Channel register row. Those are operator prerequisites.
The router config must belong to the worker's App. `--repo` is the queue's repo
token; `--worktrees-root` must match the target host's root. Outside reachable
Orca it refuses with exit 2 before any queue submission.

`--apply` submits only typed `worktree.create` then `worker.start`, with
server-side authority checks (starting requires the App manager; `--as-person`
uses your own authority). It reuses identical operations on retry, and reports
failed operations instead of reissuing them. Only two host-local steps follow:
roster entry and Orca display name. `--timeout` (1s–30m) bounds each queue
operation. Routing is unverified: team router is retired (hadron-server#2027), so
nothing confirms this roster entry is read. This command starts no replacement
router (cli#935); `verified` never claims routing, and `humanRemainder` ends with
that sentence. Onboard never casts, binds a session or writes a register row.

`--json` returns `appId`, `appRef`, `dryRun`, `workerId`, `worktree`, `operations` (type/args),
`queue` (known id/type/status), `verified`, `humanRemainder`, and nullable `error`.
After preflight a failure prints this partial record before nonzero exit;
timeouts leave submitted work running. Inspect `team host ops` (its BY cell reads PLATFORM (DESIRED_RUNNING) for a no-task start of a long-lived worker; --json adds issuedReason only then; against a server older than #4257 the list omits it and says so once, a stderr note or a `warnings` entry in --json) after ambiguous
submission errors before retrying. Onboarding separately verifies the worktree
path/branch, a current worker session, an Orca agent terminal, and display
metadata. The human tail asks for the worker's pickup and mention
delivery to be checked.

## Host up

Sweep recording (C1/S10, #2056): a completed host-up engine pass in OBSERVE or
APPLY writes one run through a separate host-key client from `--config`'s 0600
`key_file`; reconciliation keeps its normal manager credential. `--run-id`
replays a token (default fresh UUID). S10 receives times, worker counts and at
most 50 token/outcome/reason-code lines, never free text. OFF records nothing.
No config prints `sweep run not recorded: no host key (pass --config)`; JSON
adds `sweepRun` with `recorded`, `reason` and `runId`. Record failures (including
S10_UNAVAILABLE for an old server) do not change reconciliation's exit. The
server owns summary posts and stale-sweep alarms; the CLI posts nothing.
`team host sweep-runs --host <id>` reads newest-first history (limit 1..100).

The one-minute `team host sweep --config` uses the poller's saved registration
and the config's App/server, and shares host-up's run lock. `sweep install`
requires systemd user units: 1m timer, failure restart every 10s, at most three
starts in 60s, plus a fixed-code OnFailure journal logger. It preserves the
existing health and report-stops timers and never writes the key to argv/unit.
The App's mode gates actions; installing does not change it. C5 owns recovery
inside the poller, never a new direct host-up launch. C5 now reports closed recovery outcomes through S10; no direct sweep relaunch
is added. Typed Orca-launch hosts share C5's managed-daemon readiness gate;
exec-launch hosts use ordinary host-up readiness. Both select the same Orca
binary. Expected unavailable observations exit0 and wait for the next timer
tick; lock conflicts exit5 without service restart (RestartPreventExitStatus=2 5).
Exec-host preflight Usage exit2 records nothing and waits for the next tick
instead of service-restarting; missing records remain visible to the stale
alarm. The failure logger is resolved from PATH at install time. Missing scope/unreachable
list is ORCA_DOWN; unreadable or ambiguous proof is RELAUNCH_UNPROVEN. The sweep
queues nothing in either case and records skipped metadata. The poller owns the
first failed outage report and persistent replay token. Only applied/failed
C5 passes write S10; empty, OBSERVE and skip-only passes write nothing. The
one-minute command records quiet outage skips. Thus an idle poller cannot mask
a stopped sweep timer from the host-level five-minute stale-record alarm. Default recovery target assumes 30s poll
cadence, two absence observations and available launch prerequisites.


`hadron team host up` (cli#1389, epic hadron-server#4229) reconciles one host:
for each worker the host should run it checks the host registration, the
worktree under `--worktrees-root`, the Orca agent terminals in it and the
worker session, and plans only what is missing. The default (`--dry-run`) issues
no worker operation: the request log shows reads plus the optional S10 report
write described above. `--apply` carries
the plan out through the typed host-operation queue (cor:agt:070:05):
`worktree.create` when the worktree is missing, then `worker.start`. It never
calls a direct Orca create or launch, never casts, binds, rebinds or ends a
worker, and never removes anything. `--worker <ref>` narrows the run to one
worker: an exact id, or a name (any case) only one worker has; a name two
workers share exits 2 and nothing is planned. An agent terminal counts whether
or not Orca reports it connected, so no second agent is started beside one.
A terminal whose `agentIdentity` is null or empty initially counts as a possible
agent (cli#2191). On Linux, the managed-host strict process census can prove
that it adds no agent beyond the identified terminals already counted
(cli#2193). An unreadable or ambiguous census, unavailable managed-host proof,
and non-Linux hosts keep it present. The item's `detail` names unknown
terminals that still count; a leftover shell without proof still holds a start
(`TERMINAL_WITHOUT_SESSION`) or reads `DUPLICATE_AGENTS` beside an agent.

The desired state comes from the server by default: `hostDesiredState(appRef,
hostId)` (S3, hadron-server#4224), read without the worker session. A worker
whose `desired` is null (`LEAVE_ALONE`, `SWEEP_OFF`) is reported under that
reason and never acted on, whatever `wouldBe` says; a null `desired` under any
other reason is `UNKNOWN_DESIRED`. The App's switch decides where a worker's
setup comes from (cli#2085; `hostSetup(appRef, hostId)`, hadron-server#4241,
read without the worker session, one read per run). Under `HOST`, or on a
server that predates `hostSetup` (a validation refusal naming only that missing
field; `--json` `hostSetupSource` is then null), the registration names it: exactly one registered worktree is the
one acted on, while none, several or a value that is not a host name token is
`SETUP_INVALID` (reported before the host is read for that worker). Under
`SERVER` the platform's assignment names the workdir and the family
(`SETUP_INVALID` if either is not a host name token), whatever the registration
lists; a worker in the desired state with no assignment on this host is
`NOT_ASSIGNED` and never observed or acted on, and an assigned worker the
desired state does not list is `NOT_REGISTERED`. Any other failure to read
`hostSetup`, or a `source` that is neither `HOST` nor `SERVER`, stops the run
before anything is planned (the rules to plan under are unknown; the server's
refusal keeps its code, a null answer exits 1). Two assignments for one worker
on this host, identical or not and even for a worker the run would otherwise
skip, abort the whole run: exit 1, nothing planned or issued, because the
source is malformed and a partial assignment map is not trusted (rows for
another host are ignored).
Under `SERVER` a missing worktree is created only from a repo the server
names: `Worker.repos` (the worker's role's repos; read without the worker
session, and only for a worker whose worktree is missing) must hold exactly
one entry, then `worktree.create` (base `main`, branch `<worker name>/<label>`,
label the workdir's last segment; the repo's last segment must equal the
workdir's first, else `SETUP_INVALID`) and `worker.start` are planned. Zero or
several repos, a malformed entry, or a worker the caller cannot read (the field
is masked to `[]`) is `NO_WORKTREE_SOURCE`: a repo is never guessed. Under
`HOST` no repo is named, so a missing worktree is `NO_WORKTREE_SOURCE`, never
created. `--json` `hostSetupSource` is `"HOST"`, `"SERVER"` or null. A server answer for another host exits 1;
the server's refusal keeps its code. `--desired-state-file <file>` reads the
engine's own shape from a file instead (tests, offline dry runs), including
the repo a `worktree.create` needs. The file is strict (unknown keys,
duplicate workers and non-token setup values exit 2) and its `hostId` must
equal `--host`:
`{"hostId","sweepMode","workers":[{"workerId","name","desired":"RUNNING"|"STOPPED","reason","wouldBe","sessionId","setup":{"worktree","repo","baseBranch","branch","family"}}]}`.

Nothing is planned unless `sweepMode` is `APPLY`. A worker whose reason is
`LEAVE_ALONE`, `HALTED`, `PAUSED`, `TEAM_PAUSED`, `RESETTING`, `RETIRED` or
`SWEEP_OFF` is reported with that reason and never touched, and a RUNNING
worker is started only for `LONG_LIVED` or `HOLDS_TASK`. A worker with an open
session is never started again: with its agent terminal gone it is reported
`RELAUNCH_UNPROVEN`. The current CLI has no adapter proving a live poller
association, so it does not report `RELAUNCH_PENDING_POLLER` or trigger a
recovery. Only the periodic poller relaunches an agent; reconciliation reports.

AGENTLESS SESSION END (hadron-cli#2201 C10; server `endAgentlessSession`,
hadron-server#4292, spec cor:agt:050:10 Proposed). A SHORT_LIVED worker that
finishes its task without ending its session leaves the session open with no
agent. Two paths close it, both only on the strict process census (the one
the relaunch above uses): complete, own-uid-visible and empty. Nothing weaker
counts: a census error, an unproven observation, an agent found, no
registered worktree, or no session named is never "absent".
- `worker.end` for a worker with `stop_on_end: always` (the host's transitional
  short-lived marker) and no live agent this host launched: when the census
  proves the worker's worktrees empty the report is FAILED with a reason that
  LEADS with `AGENT_ABSENT` (the server reads the leading code, ends the
  session and reads the operation SUCCEEDED, writing no handoff). Otherwise
  the report is as before, with the census result in the reason, and never
  leads with `AGENT_ABSENT`. A worker without `stop_on_end: always` is never
  reported absent.
- the periodic pass of `host run` (typed `launch_via: orca`, APPLY only): a row
  that reads desired STOPPED, reason NO_OPEN_TASK, with an open session and
  exactly one worktree, is censused; empty, and a fresh desired-state read
  that still matches (APPLY, same worker and session), it calls
  `endAgentlessSession` with reason `AGENT_ABSENT` under the host key (no
  worker session). Its S10 line is APPLIED `AGENT_ABSENT`; SKIPPED
  `AGENT_PRESENT`, `CENSUS_UNPROVEN`, `STATE_CHANGED` or
  `SESSION_ALREADY_ENDED`, or the server's closed refusal as its own code
  (`WORKER_HAS_OPEN_TASK`, `WORKER_NOT_SHORT_LIVED`, `HOST_DOES_NOT_RUN_WORKER`,
  `SESSION_NOT_WORKERS`; `END_UNSUPPORTED` for a server that predates S15), all
  skipped so a standing refusal writes no S10 record each pass; FAILED only
  for an unexpected error, `END_FAILED`. The census detail goes to the local
  `agentless-end` event, not to the server. A leave-alone, paused, halted,
  resetting or retired worker, a long-lived one, and OBSERVE/OFF never end.
`hadron team host up` does not end anything (it runs under the manager's
credential, and the proof and the host-key call are the poller's): it reports
such a worker as skipped `AGENTLESS_SESSION` (action `session.end`) instead of
`IN_SYNC`.

With typed `launch_via: orca` profiles, the Linux poller's periodic pass may
recover that same open session after two consecutive proven-absent sweeps.
It requires matching App/server/worker/session/worktree bindings, fresh
RUNNING desired state under APPLY, a strict process census, and exactly one
running own-user `orca-daemon-*.scope` plus a successful terminal list.
Missing systemd proof, a failed list, or zero/multiple scopes refuses
`RELAUNCH_UNPROVEN`, spending no launch attempt. The daemon is checked again
before each attempt. At most three attempts per worker per rolling hour are
persisted before launch, including failed attempts; restart preserves that
budget and requires two fresh absence observations. An unavailable strict census
is retried as a complete fresh scan up to three times, with 20ms between failed
scans, a 1s cooperative deadline per probe and 3s total per proof. Unknown processes never prove
absence. Exhaustion reports local `RELAUNCH_CENSUS_UNAVAILABLE`; exhaustion at
the final guard after the journal claim still charges exactly one attempt, with
no refund. This local reason has no typed server repair equivalent. Parent
cancellation stops the retry. The launch uses a fresh
recover prompt, never a new bind or `--resume`, then verifies its own pinned
process and rechecks desired state and binding. Failed verification stops
only that launch. Plain worktree bindings need no Git.

Dead-tab cleanup is opt-in: `orca_launch.cleanup_dead_tabs: true` (default
false; keep disabled until live host validation). After a verified C5 relaunch
and its outcome notice, cleanup reacquires admission and rechecks binding and
desired state, then may close a leftover tab only when every pane is
disconnected, unidentified and in this worktree. A connected shell may hold a
human's build, ssh or editor and stays open. The tracked handle must still be
in a separate tab. A fresh strict census must show only the pinned relaunched
agent; a nested agent match in its own tree leaves the old tab open (known
conservative limit). Disconnected alone is not proof. Non-Linux closes no
unidentified tab. Local `relaunch-terminal-cleanup` events carry existing
worker/session/terminal fields, outcome applied|skipped|failed and reason
`DEAD_TAB_CLOSED`, `TRACKED_TERMINAL`, `IDENTIFIED_TERMINAL`,
`CONNECTED_TERMINAL`, `TAB_UNPROVEN`, `CENSUS_UNPROVEN`,
`TERMINAL_LIST_UNAVAILABLE`, `TERMINAL_CLOSE_FAILED`, `CLEANUP_DISABLED`,
`CLEANUP_UNAVAILABLE`, `CLEANUP_ADMISSION_UNAVAILABLE`,
`CLEANUP_BINDING_UNPROVEN`, `CLEANUP_TARGET_UNPROVEN` or `CLEANUP_PANIC`.
No extra server event is sent; cleanup runs after the notice and cannot
suppress it. The pass has a cooperative 30-second deadline and catches cleanup
panics without exposing their text. Unproven tabs have no autonomous retry;
they wait for a later successful relaunch or manual operations.

Poller `--json` relaunch events identify `workerId`, `sessionId`, `reason`,
`attempt` and the absence observation's `checkedAt`, plus a terminal handle
when known. Optional `foreignUidSkipped` is the maximum number of unreadable-cwd
agent candidates excluded by a completed census during that execution because
all four Linux UIDs were foreign and non-root; it is local evidence, never a
server report field. The text event prints the same count when positive.
`noticeFailed` with `localCause` or `noticeCause` means a local
failure could not be represented or delivered as a typed server repair.
`SETUP_FAILED` means setup/disk failure before the launcher ran;
`VERIFY_FAILED` means the launch ran but its process proof failed. An actual
expired launch/proof wait remains `TIMEOUT`. A cancellation, unknown transport
failure or fresh server-state refusal is not relabeled as either new reason.
Older servers reject the new reason through schema skew: the failure stays
`FAILED` with its local cause and `noticeFailed`, without a retry or a borrowed
code. No daemon controls or separate relaunch RPC are added.

An identical operation still in flight is adopted, never issued twice, and
followed to its outcome; a finished one is not reused; a conflicting one in
flight fails that worker's step (`ACTION_FAILED`). Every operation is issued
as the person running the command, never as a worker bound to the worktree:
starting a worker is the App manager's. No request this command makes, read or
write, carries `X-Hadron-Session`.

`--apply` takes a per-host lock (`<config dir>/host-up.<hash>.lock`, an OS
lock released when the process ends): a second applying run exits 5 at once and
issues nothing. A credential refusal (exit 3 or 8) stops the run: on the registration read
before anything is issued, or on an operation, where the refused step is
`REFUSED` and every later step `NOT_ATTEMPTED`. Exit 1 when any step
failed; exit 4 when `--worker` names a worker the host is not meant to run
(reported `NOT_ON_HOST`); exit 2 outside reachable Orca.

`--json` returns `{"appRef","hostId","dryRun","sweepMode","planned":[],"applied":[],"skipped":[],"failed":[],"error","source"}`;
`source` is `"server"` or `"file"`.
`sweepMode` and `error` are nullable. Each item is
`{"workerId","worker","action","reason","args":{},"operationId","detail"}`
(`workerId` null only for `NOT_ON_HOST`; `action` null for a skip with no step;
`operationId` set on applied and failed steps that reached the queue).
Reason codes, which are added to and never repurposed:
- planned / applied: `WORKTREE_MISSING` (worktree.create), `NO_SESSION` (worker.start)
- skipped: `IN_SYNC`, `SWEEP_NOT_APPLY`, `NOT_ON_HOST`, `NOT_REGISTERED`, `STOP_PENDING`,
  `AGENTLESS_SESSION`, `RELAUNCH_UNPROVEN`, `SESSION_WITHOUT_WORKTREE`, `TERMINAL_WITHOUT_SESSION`,
  `DUPLICATE_AGENTS`, `NO_WORKTREE_SOURCE`, `SETUP_INVALID`, `UNKNOWN_DESIRED`,
  `UNKNOWN_REASON`, `ACTION_UNAVAILABLE`, or the platform's own stop reason
- failed: `OBSERVE_FAILED`, `ACTION_FAILED`, `REFUSED`, `NOT_ATTEMPTED`

## Worker reporting relationships

`hadron team session start --as <worker> [--reports-to <target>]` can set the
worker's reporting relation in the bind. The target is a worker name, URN or id.
Omitting the flag omits reportsTo (preserves the relation); an empty value exits 2.
The relation lives on the worker and survives session ending and rebinding.

`hadron team worker set-reports-to <worker> <target>` sets or hands it over;
`hadron team worker set-reports-to <worker> --clear` sends explicit null.
The server decides who may set, hand over or clear it; this CLI does not infer
permission or retry refusals. Bound setter requests carry the worker session.
WORKER_REPORTS_TO_SELF/CYCLE/OTHER_APP exit 2; RETIRED/HELD exit 5;
FORBIDDEN exits 8; missing/unreadable workers exit 4. The server's own refusal
message is retained, with the WORKER_REPORTS_TO_* code in error.extensions.code
under --json and a server code label in human errors.

Worker get/list JSON adds `reportsTo: {id, name, urn} | null` and
`reportsToRetired: bool`. Null can be masked; hasLiveSession distinguishes
unavailable working state from a permitted unset relation. Human output shows
the target name, `(retired)` when flagged, `-` when unset, and `? (unavailable)`
when masked. Roster rows are grouped by target id, keeping unbound workers.
Session whoami and session rows add the same fields plus `reportingKnown`;
whoami reads the current worker relation, never a cached bind-time value.
With several fallback candidates each row shows its relation; there is no single
selected relation. No client enforcement, alarm routing or human target is added.

### Ticker commands

`ticker create` and `list` use the explicit/configured `--app`. Ticker and worker
refs are IDs or fully-qualified URNs; names are not addresses. No owner flag:
the server determines the caller and authority. Create/update accept `--name`,
`--cadence-seconds`, `--anchor-at` (RFC3339), `--quiet-hours` (JSON object),
`--channel-ids` (comma-separated/repeated IDs), `--relevance all|mentions`,
`--reasons` and `--enabled=false`. Update also has `--clear-quiet-hours` and
`--clear-channels`, mutually exclusive with their set flags. Omitted update
fields are preserved; false and clear values are transmitted explicitly. A no-field
update refuses with exit 2 before a request. Ticker/worker URNs use the v2 parser;
malformed or wrong-type refs refuse before a request.

Reasons form a nonempty closed set: `read-team-channel`, `every-tick`,
`live-check` (any-of). Duplicate reasons collapse to canonical order. Empty and unknown
reasons refuse before a write. The current server defaults omitted create
reasons to read-team-channel, relevance to all, anchor to now, enabled to true.
These defaults and the management/read rules include Proposed corpus clauses;
this describes shipped behavior, not a promotion to settled law. The server
checks cadence, consent and authority; no local wake/budget simulation occurs.
Adding every-tick can refuse the entire edit with TICKER_CADENCE_NEEDS_CONSENT.
Subscribing a short-lived worker refuses with TICKER_ON_SHORT_LIVED (exit 5):
a short-lived worker is woken by its tasks, not by tickers. Unsubscribe stays allowed.
The JSON error envelope also carries `serverCode: "TICKER_ON_SHORT_LIVED"`.
The CLI does not retry an uncertain write or attempt a narrower edit.

JSON ticker objects use id, nullable urn, name, slug, appId, cadenceSeconds,
anchorAt, quietHours, readableChannels, relevance, reasons, enabled, nullable
owner/nextTickAt/lastTickAt/updatedAt, and createdAt. `tickCode` is the server's
stored tick code (`tick.<slug>`, derived, never set by a flag) and
`registerTaskLoc` the register entry's loc exactly as returned: null means no
entry or one you may not read, so it never means "unmapped". `ticker create`
may succeed with warnings (`ticker.code.unmapped` when no register entry maps
the new code): in text mode one `warning: [code] text` line per warning on
stderr after the receipt, in `--json` an always-present `warnings` array
(`code`, `text`, `register`) beside the ticker; the exit code is unchanged and
`update` has none. Reasons use lowercase hyphenated names. Get/list add subscribers, paged to exhaustion; empty arrays
are []. Readable channels are only the reader's view. Missing and unreadable
get both report not found. `list` reads tickersPage to the server's own total
(cli#1339), so it is the whole set the caller may read; a stalled or repeating
page fails with exit 7 instead of a partial list.

`ticker apply-templates --worker <id-or-URN>` (cli#1291, hadron-server#2096)
creates the tickers its Agent's templates declare that ONE worker is missing,
each owned by and subscribed to it; nothing existing is edited. Run it as the
App's manager acting as yourself: it never sends a worker session, which the
server refuses. A missing, empty or bare-name `--worker` is exit 2 before a
request. Text prints each created ticker with its effective cadence, labelled
"the App's floor" only when it equals `appTickerSettings.minCadenceSeconds`
(read only when something was created; unreadable drops the label), then the
templates already present; a second run prints "nothing to create", exit 0.
`--json` is `{created:[{ticker,template:{id,name},effectiveCadenceSeconds}],
alreadyPresent:[{id,name}]}`, arrays always present, no client label.
Refusals keep their code as the message prefix: `FORBIDDEN` 8,
`WORKER_NOT_FOUND` 4, `WORKER_AGENT_NOT_INSTALLED` 2, `TICKER_LIMIT_REACHED` and
`TICKER_NAME_TAKEN` 5 (all-or-nothing: the message says nothing was created).

Delete requires TTY confirmation or --yes and ends live subscriptions; there is
no restore. A false delete response is an error, never a success receipt. Recreating a
name gets a new slug/URN. Subscribe/unsubscribe return subscription and nullable
announcement. Null is normal; POSTED reports grant announcement success. FAILED
leaves the subscription change standing and prints a warning; never rerun to
retry the announcement. Ticker/wake settings and actual wake transport are
separate surfaces; these commands do not advance chat/read/tick marks.

C2 host-due delivery opt-in: `team chat post --orca-host-config PATH` validates a
same-App/server registered host with its separate session-free file key before
posting. After confirmed output it attempts already-due host batches, including
older unrelated ones, with C1 locks/confirmation/receipts. Absent flag means zero
host/Orca access. Delivery holds/failures preserve post stdout/JSON and exit 0;
recover with hadron team host run --config <host.yaml>, never repost. Held
deliveries appear in local counts but are not reported to the server yet
(no server alarm/escalation).

`--push` (cli#954) requires `--orca-host-config` (exit 2 before posting without
it) and delivers to the terminals of the workers the post MENTIONS, only: the
recipients are the mentions the server stored on the message (cor:agt:020:04;
the CLI parses nothing, no quote exclusion; `@all`/`@team`/`@team.*` push to no
one). Each gets its whole due batch through the same delivery transaction
(cor:agt:080:16, :22), so nothing is typed over a draft and a held recipient's
marks do not move. The receipt gains `push: [{recipient, state, reason?, batch, acked}]` with
state `pushed` (acknowledged, cor:agt:080:16) | `typed` (the text reached the terminal but
the acknowledgement did not land, reason `ack-pending`: no mark moved, still due, never typed
again by the CLI, cor:agt:080:12) | `held` | `not-pushed` | `unconfirmed`. `acked` = batches
whose acknowledgement the server accepted. `batch` = the span
(`<channel>#<incarnation>:<from>-<through>`, the host log's handle; never the batch id, a
signed proof) of the batch that decided the state, or `none`. `not-pushed` means nothing
typed, with a closed cause (cli#1900): `not-on-host` (the mention matches no worker
registered on this host with a configured worktree) or `no-due-batch` (it matches one, and
the server offered no due batch for it; why none was due is not known to the CLI), both
`batch: none`; a failed or pending offered batch keeps the delivery's own reason.
`unconfirmed` means the pass failed before this recipient had an outcome, so typed-or-not is
not established (reason `push-failed`, `batch: none`). Every state exits
0, so test the state, not the exit. A mention token addresses every host worker with that
server mention token (mentionTokenOf; two names may share one) and reports the worst outcome;
a pass error keeps the confirmed `pushed`/`typed`/`held` results. Text prints
`push <name>: <state> (<reason>) batch <span|none>`. Known limit: an ack that never lands is handed out again and the host
poller may retype it (platform gap, cor:agt:080:12 doubt; not this command's defect).
A failed push never undoes the post or changes exit 0: do not repost. Without
`--push` the receipt has no `push` key.


### Team-channel checks (experiment #2138)

`team roster` adds a CHECK column alongside its existing work STATE. `team worker
get` shows the server's check status and last team-channel check. JSON carries
`checkStatus: {status, noSessionReason, pendingReason, lastCheckedAt} | null`: null is unavailable,
not PENDING or OK. A known result with lastCheckedAt null means never checked.
NO_SESSION, PENDING (with the server's reason), OK and OVERDUE are rendered as
returned; the CLI does not infer them from local time, task counts or liveness.
For NO_SESSION, the server's nullable noSessionReason distinguishes IDLE
(open, quiet), ENDED (ended) and NEVER_BOUND (never bound). A missing reason
stays bare NO_SESSION; the CLI never derives IDLE from an age. In worker get
and roster CHECK, IDLE adds the age since lastActiveAt, such as
`NO_SESSION (open, quiet 30h)`; the age truncates to minutes, hours, then whole
days from 48h (`quiet 3d`). That age uses this computer's clock for display
only: it changes no state, availability or exit code. Future times display
"just now"; missing or invalid times have no age. JSON retains the absolute
lastActiveAt and raw nullable noSessionReason. This selection needs a server
with hadron-server#3084; a server that rejects the field fails normally,
with no fallback or guessed reason.
This experiment supplies status only, with no overdue alarm.

`ticker create|update --reasons live-check` selects the server's live-check rule.
The server decides task expectation, freshness, pause and the remaining wake exclusions;
the CLI never computes due wakes. Changing a check period and the corresponding
team ticker cadence is the App manager's coordinated operation.

`team delivery-settings set --check-period-seconds 600 --failed-checks-to-overdue 2`
changes only the supplied settings; omitted fields preserve, no null/default
reset. Values are whole GraphQL Ints; limits and relations are server-owned.
Batch window, busy delay and stuck window remain readable and settable as
legacy deprecated push settings; they are not used to derive check status.

`agent update <ref> --uses-tasks=false` makes that Agent's workers expected to
check without an assigned task. Explicit true/false is sent; omission preserves.
`agent get` and Agent JSON read back usesTasks. The server owns edit authority.
No task is created or finished by this setting, and this CLI slice does not
start a ticker, service, terminal push or fleet activation.

`agent update <ref> --reset-context-after-task[=false]` records whether the
Agent's workers get a fresh context after each task (cor:agt:050:27). Explicit
true/false is sent; omission preserves. `agent get` and Agent JSON read back
resetContextAfterTask. The flag only records the setting: nothing resets
unless a host with a `worker.reset-context` handler acts on it (#1043), and
`team host run` enables that handler on Linux only when its config explicitly
declares the capability and supplies an absolute `reset_context.orca_bin`.
This driver cannot yet prove the terminal belongs to the target process;
all current targets are unbound and new resets refuse NOT_IDLE/TARGET_UNBOUND
before claim or stop. A same-worktree idle screen is insufficient. C3 (#2058)
owns a terminal-aware success path. Installing the CLI activates nothing. Same edit authority as `--uses-tasks`.

`agent create|update --context-cap <tokens>` (cli#2162, hadron-server#4258) sets the
Agent's context cap, a whole number from 1: a long-lived worker whose context
grows past it, and whose host reports it idle at an empty prompt, is reset (at
most once an hour per worker; a busy worker never is). `agent update
--no-context-cap` removes it (an explicit null on the wire); omitted preserves
it on update and means no cap on create; both flags together, `--context-cap` below
1 and `--no-context-cap=false` are usage errors before any request. A write that
sends a cap flag uses its own document and the receipt prints `context cap: N
tokens` or `none` (JSON `contextCap`, a number or null, present only on such a
write); every other agent command is unchanged, so an older server keeps
working and rejects only the cap write (exit 2). `agent get` shows `context
cap:` the same way (JSON `contextCap`, absent and "unknown (server before
#4258)" when the server has no such field). The reset the cap triggers still
needs a host whose reset driver is activated (C3, #2088).

`agent create --uses-tasks[=false] --reset-context-after-task[=false]` (cli#58
slice 2) set the same two settings at creation. Pass a flag only to choose a
value: left out, the server default stands (uses tasks true, reset false) and
the key is absent from the request; an explicit `=false` is sent as false.
Server compatibility: every `agent create` now sends a document that takes
`usesTasks` and `resetContextAfterTask`, even without the flags, so it needs a
server whose `createAgent` has them (schema snapshot from hadron-server
53e12896e, 2026-10-26); an older one rejects it as schema skew (exit 2).

`agent update <ref> --listed-on-marketplace[=false]` lists (or unlists) the Agent
in the marketplace catalogue (cor:acl:080:04). It governs discovery only: it does
not change visibility or who can reach the Agent. Explicit true/false is sent;
omission preserves; null is never sent.

`agent update <ref> --register-mention-only[=false]` sets the Agent's register
default `mentionOnly` (sent as `registerDefaults: {mentionOnly: <bool>}`);
`--clear-register-defaults` returns the register defaults to the platform
defaults (sent as an explicit `registerDefaults: null`); neither leaves them
unchanged (the key is omitted, never null). Both together, or
`--clear-register-defaults=false`, are usage errors (exit 2) with no request.
`agent get` and Agent JSON read back `listedOnMarketplace` and
`registerDefaults.mentionOnly` as the server returned them; authority and
refusals are the server's and surface unchanged.

## Boot-prompt pointers

A pointer names the compiled boot prompt an Agent, an installed Agent (one
App) or a customised worker uses (#916, hadron-server S8). GraphQL only; there
is no MCP write.

- `hadron agent set-boot-prompt <agent> <node-ref>`: the node must be in the
  Agent's own system memory.
- `hadron app agent set-boot-prompt <app> <agent> <node-ref>`: in that App's
  install memory for that Agent.
- `hadron team worker set-boot-prompt <worker> <node-ref>`: in the worker's
  App's install memory for its Agent.

`<node-ref>` is a node id, a fully-qualified node URN, or a bare loc with
`-m <memory>`. It is normalized locally and never looked up first. `--clear`
instead of a node ref sends an explicit `nodeRef: null`, the server's clear.
Passing both or neither exits 2 before any request. Each command sends exactly
one mutation and never retries. Setting the pointer does not approve the
prompt; approval is checked when the prompt is served.

**As yourself.** Only the manager may set a pointer, as themselves (R23). These
three commands never send a bound worktree's worker session, even from a bound
worktree. A request that does carry one is refused
`BOOT_PROMPT_POINTER_SESSION_REFUSED`, exit 8.

**Exit codes.**
- `BOOT_PROMPT_POINTER_NOT_COMPILED`, `_WRONG_ROLE` and `_WRONG_MEMORY` exit 2.
- `BOOT_PROMPT_POINTER_NOT_FOUND` exits 4. It covers a missing, deleted or
  unreadable node alike.
- `FORBIDDEN` (not the manager, an App key, an impersonation, a run) exits 8.

The server checks authority before it looks at the target, and its message is
kept whole.

**Reads.** The JSON from `agent get` / `team worker get` / `app agent list`
(and every command that prints the same Agent or worker shape) carries
`bootPromptRef` (the URN, which follows a moved node) and
`bootPromptTargetDeleted`. **Both null means unset OR not visible to you**:
never proof that no pointer exists. Human output says
`— (unset or not visible to you)`. A deleted target shows its URN with
`(deleted: clear or repoint)`; nothing is served from it.

## Canonical node portal pointer (cli#1015, Refs #29)

`hadron node open <node-ref> [-m <memory>] [--print]` reads only the node's
returned id/urn/portalUrl. It never constructs a portal route. Interactive
text output requests the platform launcher using argv; no shell is used.
JSON, non-terminal stdout and --print only print the pointer, with no launcher.
JSON is {id,urn,portalUrl}; text is the exact returned URL. Launcher success
means the launch request was accepted, not that a page loaded. Failure is an
error. Missing/unreadable node is exit 4; unavailable/null/invalid URL is exit 1,
a deterministic answered failure, with no guessed fallback or blind retry.
The query selects identity/URL only; the server still records a node read and
may decrypt internally. URL validation requires http(s), a host, no userinfo.
Memory/Agent/App open are not delivered; their canonical URL provider gap is
hadron-server#2309. No real navigation is required for automated tests.

## Usage reporting (read-only #65)

```text
hadron usage activity [--from <iso>] [--to <iso>] [--bucket DAY|WEEK|MONTH] [--memory <ref>]... [--time-zone <IANA-zone>]
```
Activity preserves omitted flags and server defaults. No client count cap,
zero-fill, totals or fallback from an empty resource scope (cor:acl:090:01).
Unreadable/unknown resource refs are silently excluded by the server; it does
not report which refs contributed. Text names the requested scope and flags,
not the resolved window. JSON is [{date,reads,writes}] in returned order.

```text
hadron usage events [--memory <ref>] [--loc <loc> | --node <PK-or-URN>] [--type <type>] [--since <iso>] [--until <iso>] [--app-key <id>] [--limit N] [--offset N]
```
The events command returns one newest-first page (default20, local0-200 where 0 counts only, offset>=0), with access
owned by the server. JSON is an array; empty is []. Filter flags are optional;
explicit empty is Usage2. Loc is an exact verbatim loc, never shape-classified
as nodeId. `--since`/`--until` (cli#1205) bound the window by creation time:
since is INCLUSIVE, until EXCLUSIVE (since <= createdAt < until; `team events
--since` is the opposite, exclusive); either may be omitted. Each is an ISO
date-time WITH `Z` or an explicit offset; a date alone, an offset-less time, an
empty value, or since >= until is Usage2 with nothing sent. Windowing does not
change which memories are readable. Stderr says `showing a-b of N` with the
windowed real total (usageEventsPage, cli#1339) and `more: --offset b` when more
exist; stdout/--json stay the bare array. No automatic drain or cost aggregation.
A malformed null event row fails with Error1 before any successful rows or page
note; JSON uses the normal error envelope. Valid empty results remain [].
`costMicroCents` is a nullable base-10 integer STRING, units1e-8USD; keep every
digit, never convert to float. Null is unpriced (human: unpriced), not zero.
Token nulls are unknown. Action arguments/caller payloads are not selected.

`hadron memory usage <memoryRef>` returns the provider's per-loc/type counts
(JSON array of {nodeLoc,type,count}, empty []). These are not billing balances
or client sums over event pages. Historical unattributable events can be absent.
No recordUsage, deprecated nodeUsage or accounting write is exposed.

## Ordered next sibling (cli#1009)

`hadron node next <parent> [--after <current>] [-m <memory>] [--raw]`
reads the first child, or the sibling after the named direct child. The server
orders by seq ascending (null seq last), then loc. This is stateless, with no
session start/end. Node ids and qualified/legacy URNs go directly to nextNode;
`-m` composes bare locs locally, including compound memory refs.

End, missing/denied parent and a current ref outside its direct children share
one exit-0 result: `end of sibling chain` (JSON null). It proves no parent
existence or permission. Malformed refs and provider errors remain errors.
A returned node uses ordinary node-detail JSON/text; rendered is true by
default, false with --raw. Separate advisory revision/stamp/comment/signature
snapshots are not fetched and remain null; content/edges come from the one
nextNode read. No ordinary get/list behaviour changes.

- `agent get` alone adds nullable `clonedFromAgentRef`: the server-returned source
  current URN, copied verbatim. Text prints `Cloned from:` only when non-null.
  Null does not distinguish non-clone, withheld, deleted or unrecorded origin.
  No extra source lookup; clone/list output and selections are unchanged.


`hadron spec label-report [-m MEMORY] [--prefix CITATION] [--json]` is a read-only
inventory, not lint. JSON is `{memoryRef,prefix,items:[]}` with
`{parentId,parentLoc,edgeId,label,childId,childLoc,childName,status}` items.
`available` rows carry a current readable child name; differing editorial labels
are not defects.
A visible same-memory direct citation source establishes the relationship even when its current name cannot be read; in that case unavailable describes the current name.
Every incoming edge satisfying that source condition is inventoried, regardless of its editorial label; no special edge kind is selected. `unavailable` with
edgeId present and null childId is an unclassified source-unavailable candidate;
null edgeId is an unavailable-parent marker, not an invented edge. Null labels
and names remain null. No hidden endpoint/history read, heuristic, rewrite or
lint exit change; transport/protocol failures fail rather than return partial
inventory. The report covers caller-visible data only.
- `agent subscription list --org <id|urn>` reads one explicitly
  selected organization's visible Agent licenses. It preserves all provider rows,
  server `isActive`, and nullable lifecycle timestamps. The unpaginated field has
  no client completeness guarantee: empty `[]` can be a concealed projection,
  never proof that no grants exist globally. Caller org ref passes through without
  a prelookup. This differs from principal-action `hadron grant` and per-user
  Agent subscriptions.
- `agent subscription list --agent <ref>` / `--mine` (cli#2788) read the paged
  user subscriptions (agentSubscriptionsPage needs Agent ADMIN; `--mine` is the
  authenticated user, and a run or impersonated caller gets an empty page: empty is
  never proof of none). Exactly one of `--org|--agent|--mine`; `--limit`/`--offset`
  apply to the paged two (unset is omitted), `--all` follows pages to the end and
  conflicts with them (exit 2). One page prints "NOT the whole set"; `--json` is
  `{"items":[{agentId,userId,isActive,createdAt,activatedAt,expiresAt,revokedAt}],
  "total","hasMore","complete"}` (`complete:false` for a truncated page; a server that
  claims more yet returns an empty page under `--all` is exit 1, never a complete
  list). `--org` keeps its bare-array shape.
- `agent subscription add --org <ref> --agent <ref>` (createAgentOrgGrant) sends ONE
  write and never replays. A coded refusal keeps its normal exit (nothing changed);
  a lost answer, INTERNAL error or null payload is exit 7 "outcome unknown" with a
  check-first `agent subscription list --org <org>`. `--json` is the grant
  `{agentId,orgId,isActive,createdAt,activatedAt,expiresAt,revokedAt}`. A returned grant
  that is inactive or revoked is exit 1 (the server never returns one).
- `agent subscription revoke <agent> --org <ref> [--yes]` (cli#2788, revokeAgentOrgGrant)
  is the ORGANIZATION form of the one revoke command below (exactly one of `--user` /
  `--org`; neither, both or a blank value is exit 2 with nothing sent). Confirms on a TTY,
  `--yes` required otherwise; ONE write, never replayed; same exit 7 / check-first as add;
  a revoked subscription is not reactivated by subscribing again (server refuses); an
  answer that does not come back revoked and inactive is exit 1. `--json` is the grant
  shape above. The old `revoke --org <ref> --agent <ref>` and `unsubscribe` spellings are gone.
- `agent subscription revoke <agent> --user <ref> [--yes]` (cli#2789) revokes ONE USER's
  subscription to an Agent (not an organization license) with one `revokeAgentSubscription`
  mutation, sent once and never resent. **It can delete a memory:** the server removes
  the user's personal memory for that Agent if it has no nodes and keeps it if it has any;
  the server decides when the revoke runs, the CLI never looks first and the answer does
  not say which happened, so check with `hadron memory list`. On a terminal it asks, naming
  that; off one it needs `--yes` and sends nothing without it. `<agent>` is an ID or URN,
  `--user` an ID, email, handle or `hrn:user:` URN. Only an Agent admin/owner may; an
  Agent you cannot read answers like an absent one (exit 4). Exit 0: the answer shows the
  subscription ended (`--json` is `{agentId,userId,isActive,createdAt,activatedAt,
  expiresAt,revokedAt,revokedBy}`); 3 not signed in; 8 not permitted; 2 usage; 7 the
  outcome is unknown (a lost answer, an error beneath the mutation, or an answer that does
  not show an ended subscription: the revoke and the empty-memory deletion after it may
  or may not have happened; check `hadron agent subscription list --agent <agent>` (shows whether it is revoked) and `hadron memory list`) before running it again.


`hadron email connections --org REF` reads connection metadata through the
server's org-admin list gate. Explicit organization only; the server resolves
the id or URN. This does not authorize mailbox reads, reconnects or sends.
The existing unpaginated exchangeConnections list is used once; no page total
or client completeness guarantee is invented. Empty JSON is []. Each row has
{id,organizationId,userId,mailboxEmail,provider,displayName,syncEnabled,syncStatus,
lastSyncAt,createdAt,updatedAt}; displayName/lastSyncAt preserve null. No credentials
or lastError are selected. Text quotes external strings. Error mapping remains
unchanged, including unknown tool failures; no guessed reconnect remedy.

`hadron email connections --org REF --limit N --offset N` (either flag) makes ONE
paged request over exchangeConnectionsPage: an unset flag is omitted (the
server's default applies), the reply carries the real `total` and `hasMore`,
`--json` is then {items,total,hasMore} (the item shape above), and text ends
with the total and, when more follow, the `--offset` that continues. One call is
one page: the CLI never fetches the next page and never calls a list complete.
The `--offset` hint counts from the effective offset (a negative one counts as
0, as the server clamps it) and is withheld on an empty page. The bare form is
unchanged.

`--top`, `--limit` and `--offset` are checked only against the wire type (GraphQL
Int, signed 32-bit; a value outside it exits 2 with no request); inside that
range the server decides. `--unread=false` is sent as an explicit
`unreadOnly:false`; an unset `--unread` is omitted.

`hadron email messages --connection ID [--top N] [--unread]` reads one bounded
list of recent messages (exchangeMessages, once). `--top` is parsed as an integer
and sent unchecked (the server clamps and has the default); unset `--top` and
`--unread` are omitted, and the deprecated raw `filter` is never sent. `--json`
is an array of {id,subject,from,receivedDateTime,bodyPreview,body,hasAttachments,
isRead}; absent values are null, an empty result is []. The text table shows the
preview, never the body, with control and escape sequences made visible; message
text is external data and is never acted on or logged. `hadron email folders
--connection ID` reads exchangeFolders once: [{id,displayName,totalItemCount,
unreadItemCount}], counts null when the provider omits them. Both are owner-only
(an App needs a covering grant; an org admin who is not the owner, and an
impersonated session, are refused by the server): FORBIDDEN exit 8 passes
through unchanged, unknown tool failures keep the existing fallback with no
reconnect advice. A missing or blank `--connection` exits 2 with no request.

`hadron email test CONNECTION-ID` calls testExchangeConnection ONCE and never
retries; the server records the result as the connection's sync status, so it is
not a pure read. It is gated like reading messages (owner, or an App with a
covering `mail.read` grant; an org admin who is not the owner, and an
impersonated session, are refused by the server). `--json` is {connectionId,ok};
true exits 0, an explicit false exits 1 ("the server's probe did not pass": no reason, no
reconnect advice). An absent or null answer is exit 7 with no ok field, never a failed probe.
Connecting again is `hadron connection connect`.

`hadron email disconnect CONNECTION-ID [--yes]` calls deleteExchangeConnection
ONCE and never retries. It deletes the connection record of ANY provider and is
open only to an ADMIN of the owning organization (owner or grant holder does not
open it; impersonation is refused). The server's single Forbidden (missing,
deleted or not-admin alike) is shown as it comes, never as "not found" or
"already disconnected". It asks y/N on a terminal; without one it exits 2 with
ZERO requests unless `--yes`. If the server returns `false` (or no answer), the command exits 7 ("did not confirm"). `--json`
is {connectionId,disconnected}. "Disconnected" means Hadron's record: the
provider-side teardown is the server's best effort and is not reported. A missing
or blank id exits 2 with no request. Both commands: unknown tool failures keep the
existing fallback with no reconnect advice.

**Mailbox effects (cli#198 Slice C)** — each is one GraphQL request and is never retried; the
server decides ownership (owner, or an App with a covering grant; impersonated sessions and an org
admin who is not the owner are refused) and its refusals pass through (FORBIDDEN exit 8), unknown
tool failures keep the existing fallback with no reconnect advice, and a false answer exits 7 "not
confirmed". A missing or blank `--connection` or id exits 2 with no request; ids and string flags are sent exactly as given (not trimmed), so invalid UTF-8 in one exits 2 with no request.
- `email reply MESSAGE-ID --connection ID --body-file PATH|-` calls exchangeReply and replies to
  the SENDER only (the server takes no recipient: no `--all`, no `--to`). It SENDS MAIL: asks y/N on
  a terminal, else exit 2 with ZERO requests unless `--yes` (refused before stdin is read). The body
  (file, or `-` for stdin; required, nonempty, valid UTF-8, 1 MiB max; `--body-file -` on an
  interactive terminal exits 2) exits 2 with no request when invalid. Error text and server codes
  from FAILED writes are never printed: a failure of the commands that SUBMIT a body (reply, draft
  save, draft update) does not show the server's error message, an HTTP body or a decode error (any
  of them can quote the body); it shows the CLI's own words plus the HTTP status, or the server's
  error CODE when it is on a fixed allowlist of protocol codes (an unknown or body-derived code is
  dropped, in the message and in the --json serverCode), and keeps the exit class. Ids in a
  SUCCESS are shown as the server returned them.
  For the SEND (reply, and draft send): only a pre-send refusal (FORBIDDEN, UNAUTHENTICATED,
  BAD_USER_INPUT, GraphQL validation) is definitive; any other GraphQL error, and any answer with no
  usable GraphQL error at all (a dropped connection, a timeout, a bad HTTP answer), carries "outcome
  unknown ... check the Sent folder" (never "failed": the mail may have been sent) and the CLI does
  not retry. Draft send has no body and keeps the server's words. If the SERVER confirmed an effect but writing the result then fails (a closed stdout), the error says the server confirmed it and where to check (Drafts or Sent folder) before any retry. There is no idempotency key on reply.
- `email draft save` calls emailSaveDraft: a reply-draft with `--reply-to` ALONE, or a fresh one with
  `--to` (repeatable) AND `--subject` (any other combination exits 2 with no request and before the
  body is read); the body is HTML sent as written. It prints {draftId,conversationId}
  (conversationId may be null), printed as the server returned them: a draft that was created is never
  refused or altered on a guess about its id. An unclear save (no usable answer, or a success with no
  usable draft id) is exit 7 "outcome unknown: a draft may have been created; check the Drafts folder
  before trying again". A retry is protected from a second draft only when the ORIGINAL request carried
  the same valid `--idempotency-key` too (within the server's deduplication window); the message says
  which case applies, and says a key added only on the retry does not protect. An explicitly empty or
  whitespace-only key is sent unchanged but counts as no key for this guidance (CLI policy, deliberately
  conservative: the server dedups only on a real key, and the CLI cannot tell whether it accepted one).
  `--idempotency-key` is passed unchanged (an empty value too) and omitted when unset: the CLI never
  invents or replays one, and retry protection needs the same valid key reused within the server's
  deduplication window. `draft update DRAFT-ID` calls
  emailUpdateDraft and replaces the body only; it has no idempotency key because the server has
  none. `draft send DRAFT-ID` calls emailSendDraft: SENDS MAIL (confirmation or `--yes`),
  `--idempotency-key` passed unchanged, an ambiguous result is "outcome unknown, check the Sent
  folder".
- `email mark-read` (emailMarkRead; `--unread` sends isRead:false, unset omits it), `email flag`
  (emailFlagMessage; `--flag FLAGGED|NONE|COMPLETE`, accepted in any case with surrounding space ignored, unset omits it,
  anything else exits 2 with no request), `email categorize` (emailCategorizeMessage; repeatable `--category`, or `--clear` for an
  explicit empty list; neither or both exits 2), `email move --folder FOLDER-ID` (exchangeMoveMessage;
  the folder id is the one `email folders` prints), `email delete` (emailDeleteMessage, provider
  trash semantics; asks y/N, else `--yes` or exit 2 with no request). `--json` for these is
  {connectionId,messageId|draftId,action,ok}.


`hadron connection connect <provider> --org REF` starts the OAuth flow that
connects a mailbox, calendar or Drive to an org (startConnection, one call), with
no portal. Only a signed-in user acting as themselves who is an org ADMIN may run
it; an App key, bound worker session, impersonated request, MCP-only OAuth
credential or a non-admin is refused by the server (FORBIDDEN / UNAUTHENTICATED /
CONNECTION_PROVIDER_NOT_CONFIGURED), and --json errors carry that as
error.serverCode. The authorize URL is a single-use credential: it is shown
exactly once. The browser is opened and only a notice is printed; stdout gets the
URL only under --no-browser or when the browser cannot be opened. With --json the
URL is the `authorizeUrl` field of the one result document and nowhere else, so
--json with --no-browser requires --no-wait (usage error, exit 2, before any
start). After the start the command polls connectionFlow(flowId) with the
start's flow id (--wait, default 5m, never past the link's expiresAt; --no-wait
skips it), so two flows started at once each end with their own connection and a
reconnect ends with the existing connection's id. PENDING is an answer: only the
deadline ends the wait. CONNECTED: one metadata-only EmailConnections read fills
mailboxEmail/displayName (if it fails the result still carries the id). FAILED:
exit 1 (Error) with the flow's stable failureCode (e.g. access_denied) as the
result, not a transport error. A refused flow read (unknown, expired or another
user's flow id) is one message and exit 8 (Forbidden) in every case, ends the wait
and is not retried. With --json a browser that cannot be opened ends the
command at once with status "started" (no wait). Connected: exit 0,
{provider,orgId,authorizeUrl,expiresAt,flowId,browserOpened,
status:"connected",failureCode:null,connection:{id,provider,mailboxEmail,
displayName}}. If the follow-up connections read failed or did not list the new connection, the
connect still succeeds with mailboxEmail and displayName empty, one note on stderr
says which of the two happened, and the document adds degraded:true and
degradedFields:["mailboxEmail","displayName"] (absent otherwise). The marker means
that failed or missing lookup, not empty fields: a listed row whose fields are
themselves empty gets no marker and no note. Failed: the same document with status "failed", failureCode set
and connection null, then exit 1. --no-wait: status "started", flowId set, connection null. Not
finished in time: exit 13 (WaitTimeout), "not found yet", after the record is
printed with status "pending" and the flowId - the consent may still
be completing, so check `hadron connection flow FLOW_ID` or
`hadron email connections --org REF` before starting again. Errors never contain the URL or its nonce; nothing is
written to disk.


`hadron connection flow <flow-id>` reads connectionFlow once (read-only, safe to
repeat): {flowId,status:"PENDING"|"CONNECTED"|"FAILED",connectionId,failureCode},
unused fields null. Every resolved state exits 0 (a failed flow is a successful
read reporting a failure; check status); only the refusal (exit 8, the same for an
unknown, expired or another user's flow id) or a transport error is nonzero.

`hadron calendar` reads with list/events/event get/freebusy and (owner-only) changes
with event create/update/delete/respond: explicit --connection, no discovery fallback or
org-admin mailbox entitlement. `events` sends top=25 by default (1..100), one
bounded bare list with no total/continuation. Absent optional calendar/query/time
filters are omitted; explicit empty values are refused before requests. Bounds
are RFC3339 instants with offsets; both supplied bounds must increase. Freebusy
requires both and returns schedules/windows/error only, never event contents.
CalendarInfo DTO: {id,name,description,timeZone,isPrimary,canEdit}. Event DTO:
{id,calendarId,title,description,location,start,end,isAllDay,status,organizer,
attendees,webLink,seriesId,isOrganizer,myResponse,createdAt,updatedAt}. Boundaries
are {date,dateTime,timeZone}; date-only YYYY-MM-DD strings stay dates. Organizer
is null or {address,name}, attendees is [] or {address,name,optional,response}
rows; enum strings including NONE and nullable myResponse stay as returned.
Freebusy DTO: [{id,error,busy:[{start,end}]}]. Arrays are [], nullable fields are
null, and supplied offsets/zones are preserved. Human mode labels pretty JSON
as external data and identifies the connection source; --json is the DTO only.
Writes (owner-only; a grant never allows them) print the Event DTO, except
delete, which prints {eventId,deleted:true} only when the server returns true
(false or null is an error, exit 7, never a success). Timed events take RFC3339
with an offset, or a zone-less date-time plus --tz; --all-day takes YYYY-MM-DD
dates (end is exclusive); a zone is never guessed. update sends only the flags
given (nothing given is refused, exit 2); a non-empty --attendee list replaces
the whole set, --clear-attendees empties it (exclusive with --attendee); an empty
--title/--description/--location is sent, not omitted. --no-notify sends
notifyAttendees=false (default: omitted, the provider notifies);
--idempotency-key is passed through on create/update/delete. Nothing is retried:
after a transport error or timeout the change may have been made, so read the
event back before repeating, or repeat with the same key. respond takes
accepted|declined|tentative, never NONE. delete needs --yes when not on a
terminal. Existing error mapping/refusal sentences remain; unknown errors do not
imply reconnect/retry.


`hadron drive list/get/export` are read-only with explicit --connection and the server's existing
owner/drive.read grant gate; org administration grants no mailbox access. List
returns one bounded {files,nextPageToken} page, never --all or a completeness
claim. Explicit top defaults to50 browse/25 search (query/nameContains/mimeType/
modifiedAfter), range1..100; folder/token alone remains browse. Absent optional
filters are omitted, blank supplied filters refused. Opaque page tokens are
forwarded unchanged; nextPageToken null ends pagination. File DTO:
{id,name,kind,mimeType,parents,size,createdAt,modifiedAt,webLink}; parents stays[],
nullable Float size remains bytes/null with no integer guarantee. Modified-after
requires RFC3339 with offset; no timezone guessed. Get/list text labels escaped
JSON metadata as data and names the connection source. Export text writes only
the original content bytes (no banner/newline/truncation/evaluation); JSON is
{file,format,mimeType,content}. Format MARKDOWN/TEXT/HTML is case-normalized; absent
format delegates the provider default. Export has no local output-file or remote
document creation option. Server delegates format/binary/size guarantees to its tool;
file_too_large and unknown failures retain existing error mapping/refusal text,
with no retry/reconnect guess. No hidden discovery, writes or uploads.

`drive create-doc` is the separate, confirmed owner-only remote creation
command: `hadron drive create-doc <node-ref> --connection <id> [--folder <id>] [--title <text>] [--yes]`. It never changes the read/export commands into writes.

## Default team channel location (#489)

`hadron team channel [--app <ref>]` answers where the default team channel lives.
It uses the existing team App resolution (--app, configured context, binding),
reports that source, and copies the server-built URL verbatim. The destination
is the channel root node's own page and channel view, not an App conversation
selector. No route is composed. JSON is {app,appSource,channelId,portalUrl};
portalUrl is present and nullable. Text prints URL: only when supplied, otherwise
says no channel URL is available. One metadata read and one best-effort authorized
root node read, no transcript fetch or cursor/binding write. The root read records
a read and may decrypt internally. A concealed/missing/failed root read leaves
the URL null; initial lookup failures remain errors. Existing channel get/list
commands are unchanged.

## Guarded node field updates (#820)

`hadron node update <ref> --expected-revision N <field-flags>` forwards exactly
your positive 32-bit baseline (1..2147483647) to the current kind's update door,
including role clears. Omit it for last-write-wins. The flag alone is not an
edit. Guarded data merges (--data-merge / --data-merge-file), including mixed
field+merge calls, refuse before any request or input read; --data replacement
uses the guarded door. Dry-run previews the supplied expectedRevision (nullable
JSON key) and explicitly says it is not validated by the server. A mismatch
keeps NODE_WRITE_CONFLICT/exit5 and provider currentRevision metadata; re-read
and retry deliberately. Drift can advance a revision without another user's
edit. No automatic refresh/retry or selector-identity guarantee is added.

## Team table text (#1101)

Server-supplied team table cells and composition role/family headcount labels
use `output.SafeCell`: C0/C1/DEL controls and Unicode Cf/Zl/Zp become single
spaces, and cells longer than 256 runes are shortened to 255 runes plus an
ellipsis. This also bounds long clean values. Clean cells up to 256 runes keep
their original text. `--json` keeps the original server strings; use it for a
complete long value. This table policy does not describe free-form prose,
chat bodies or task trees.


## Team metadata text (#1112)

The same SafeCell/256-rune policy covers role warning names, host-status
host/app labels, worker/family/launch/worktree cells, registration metadata
and read errors, composition/roster as-of values, host-operation cursors, and
stop-lift receipt ids. JSON retains the original values. Free-form task
details, task trees and kept-worktree diagnostics are outside this metadata
policy.

A displayed value that was replaced or shortened is never reused in a
copyable command or flag suggestion. Read the original token/cursor/proof
from --json; for an unmatched installed persona role, use
`hadron app agent list <app> --json`. Unchanged POSIX command arguments are
shell-quoted independently. Windows output gives argument guidance rather
than a shell command. None of this changes DTOs or the arguments sent to
the server.

## Recorded served boot prompts (#916 C3b)

`hadron team worker served-prompt <worker-id-or-qualified-URN>` and `hadron run served-prompt <run-ref>` read the recorded revision/hash/variables and scalar-only provenance. `--limit 0..200` (default 50), `--offset N`, or `--all` (all provenance pages, buffered before output). Explicit `--offset` excludes `--all`; `--all --limit 0` is refused. JSON is `{servedPrompt: record|null}`; null is unavailable (unrecorded, unreadable or serving off), never proof of which, and exits 0 (unlike `node provenance`, whose unavailable revision exits 4). With `--all`, `provenance.items` aggregates every page while `provenance.limit`/`offset` stay the request paging (page size, offset 0). No current prompt/source text is fetched. Pruned revisions remain recorded. Recorded variables may contain sensitive values. Ordinary worker/run get remains unchanged.
Served-record lint (cli#2007, cor:agt:040:13): `team worker served-prompt` always adds `currentLint`; `run served-prompt`, `team session served-prompts` and `chat served-prompts` add it with `--lint-warnings` (key absent without it). It is the current lint of that record's provenance revision (never the newest compile), `{items,total,nextCursor}` or `null`; null is unavailable for that record, never "no warnings". A history record is whole or null (record-id and total checked), 20 per request, one shared 5 s / 1000-request budget; an unavailable read prints one stderr line naming current lint and changes neither the result nor the exit code. Retry only for a page that changed while reading or a spent budget; rerunning does not resolve an older server or unreadable record. Fix source templates for future prompts: recompiling may pick up changed sources but cannot repair a recorded revision.

`hadron team session served-prompts <session-id>` reads one explicit session’s history in server order (newest served time, then ordinal). Find ids with `team session list`; no ambient binding/App selects the session. `--limit 0..200` (default 50) and `--offset N` page records; `--limit 0` counts only. `--provenance-limit 0..200` (default 50) and `--provenance-offset N` independently page each record’s provenance. JSON is `{servedPrompts: {items: [], total, limit, offset}}`, or `{servedPrompts: null}` for a missing/unreadable session. Empty records can also mean concealment or serving off. Actual provider errors retain their mapped exit code. No source bodies are read; variables may contain sensitive values. For chatbot chat records, use `chat served-prompts <chat-id>` (below); this is separate from team-chat Channels.

`hadron team session outcome <session-id> <0..1> [--ref <text>] [--meta <json>]` (cli#1351, server#2896) records a session's outcome through `recordSessionOutcome`: a number from 0 to 1, an optional opaque `--ref` and optional `--meta` JSON. It works on any session the caller owns, not only the bound one. `--ref`/`--meta` are sent only when given, so leaving one out keeps the stored value; `--ref ""` stores an empty reference and `--meta null` clears the metadata. A negative outcome reads as a flag: pass it after `--` (`outcome <id> -- -0.1`), otherwise exit 2 with nothing sent. A malformed `--meta` or an outcome that is not a finite number exits 2 with nothing sent. The range, the owner gate (the attributed person, or an App key for its own App's person-less session; cor:dmo:070:05, cor:acl:010:02) and the MCP-only OAuth refusal are the server's: out of range or an unknown session exits 2 (`BAD_USER_INPUT`), not the owner exits 8 (`FORBIDDEN`), an MCP-only OAuth credential exits 3. `--json` errors keep `serverCode`. `--json` success is `{sessionId, outcome, outcomeRef, outcomeMeta}` (null when unset). Recording an outcome does not revive a lapsed session.

## Compiled prompt provenance (916 C1b)

```text
hadron node provenance <compiled-node-ref> --revision <n> [--limit 0..100] [--offset N | --all] [-m <memory>]
```

`node provenance` reads recorded sources for exactly the required positive
compiled revision. It never substitutes the latest include edges or fetches
source bodies. JSON is `{nodeId,revNo,contentHash,version,pruned,items,total,hasMore,limit,offset}`;
occurrences preserve recorded order/depth/parent,
kind, source nodeId/revNo/hash, replacesNodeId, nullable position, effective
selection `{rev,approved,signed}` and nullable `pruned`. Source `pruned: null` is
not visible, never a guessed retained/pruned state. Recorded ids/hashes/settings
are admitted by the compiled node's existing read/history gate.

Default one offset page, limit 20/cap 100; limit 0 counts only. `--all` buffers
every page and excludes `--offset` and counts-only. Errors, inconsistent counts/identity
or compiled pruning state, nonprogress or duplicate occurrence order fail the
whole all-page read with no partial payload. Independent pages are not a shared
snapshot; source availability may change between them. Null provider result is
exit 4 (missing/deleted/noncompiled/unreadable concealed together); server
integrity failures retain normal API errors, never become an empty result.
No write, export, approval, activation or automatic compile is added. Requires
a provider with merged server#2685; older servers refuse the new read.

## App install memory (916 C7)

`hadron app agent customize <app> <agent>` only reads the install-memory
record; it creates nothing. Refs are ids or fully-qualified URNs. JSON is
`{memory:{id,urn,name}}` or `{memory:null}`. Null is unavailable: not created
yet, missing or not shown to this caller; it is not a claim of absence.

`node add|update|get|list --install <app>/<agent>` targets that App's separate
customisation memory for the Agent. Both refs must be ids or fully-qualified
URNs. `--install` excludes `--memory`, including an explicitly empty memory
flag. Get/update take bare locs with --install, not a separately addressed
node id or URN. Prefix/batch reads and listing retain their existing shapes.

The first add sends native `input.install`, omitting memoryId. The server
creates the memory in the write's transaction, and a refused write creates
nothing. Updates read the memory without creating it and use install+loc for
the native field update. With --install, --data-merge/file is refused before
any request; use --data/file replacement to keep the install selector.
Get/list use the returned memory URN. An unavailable memory
exits 4 before any node query or mutation; it never becomes an unscoped list.
Read access to memory metadata is not permission to read its nodes or write:
all content and manager/live-App/installed-Agent checks remain server-owned.
Existing node output, omission/clear, kind routing and error mapping remain.
No automatic retry or client-side memory creation is added.
## Compile a boot prompt

- `hadron agent compile <agent> [--no-set-pointer] [--as-worker]`
- `hadron app agent compile <app> <agent> [--as-worker]` (by default always sets its pointer; with `--as-worker` the server never does)
- `hadron team worker compile <worker> [--no-set-pointer] [--as-worker]`

GraphQL-only operations. By default they act as yourself (a manager): none
sends the bound worktree's worker-session header. With `--as-worker` (cli#1872,
here and on `team worker compile` batch, `node approve` and `team worker
approve`) the compile/approve MUTATIONS carry the bound worker session instead;
reads stay yours. The server then admits only a new revision of an existing
compiled node that a signed `bootprompt.compile.approve` grant names (any worker
in the App, not only the bound one), forces `pointerSet` false, and never
creates a node; the CLI sends no `setPointer` it did not send before and
checks no grant. `--as-worker` never falls back to you. It refuses (exit 2)
for no worktree, a missing/incomplete binding, a binding for another server,
or an App mismatch through `--app` or the positional `<app>` of
`app agent compile` (cli#1887). With
`team worker compile --as-worker`, the resolved worker's App must match the
original binding, including explicit worker ids/URNs (cli#1896). App comparison
uses resolved ids.

An App you cannot read is refused exactly like one that does not exist:
byte-identical text and exit 2, so the refusal is no existence oracle (V58,
hrn:node:hadronmemory.com:hadron-dev-team-shared:decisions:2026-11-10-vera-rulings-app-refusals;
specs cor:agt:050:21 and cor:dmo:050:11, both Proposed, not Decided). The same
shared guard serves signature/trailer edits, `team grant list|get|request` and
`team task mine --app`: for those, a pure permission refusal on the App read
now exits 2 with the missing-App usage text, where it was exit 8 before
(cli#1896, cli#2239). Only a PURE permission refusal is normalized. A
credential or token-scope refusal and a MIXED answer (FORBIDDEN together with
UNAUTHENTICATED) keep the server's own messages in the order sent and exit by
the FIRST error's code: a mixed answer led by UNAUTHENTICATED exits 3, one led
by FORBIDDEN exits 8. A failed lookup keeps its ordinary mapped diagnostic, not
the server's text: an internal error is masked to "the server hit an internal
error" and exits 1 (cli#898, cli#2297). None of them reads as a missing App. Optional authored-write attribution (`node create` and the like)
silently leaves the worker session off for a missing OR unreadable App and
still sends your own write; a credential, mixed or failed lookup adds one note.

Initial binding faults refuse before any request; App/worker identity reads can precede
an App mismatch refusal, but no compile/approve write is sent. A binding that
changes during the run also refuses with exit 2; a mid-batch change files the
remaining workers under `failed` with exit 2 and sends nothing more. Server
refusals (`BOOT_PROMPT_COMPILE_NOT_PERMITTED`, `BOOT_PROMPT_GRANT_REQUIRED`,
`BOOT_PROMPT_WORKER_SESSION_REFUSED`, `BOOT_PROMPT_APPROVAL_FORBIDDEN`) print
with their codes, exit 8. The grant does not cover signing: where an organisation has switched on signing of compiled boot prompts (its own setting, not the passkey switch) a person still signs each compiled
prompt. Ask for the grant with `hadron team grant request`. The server selects the source and output
address. Agent/App references are ids or fully-qualified URNs; a worker may
also be a name in the selected App. Each command sends one compile mutation
and never retries. Default pointer behavior is omitted on the wire (server
default true); `--no-set-pointer` sends false, and is not available for App-Agent.

Success JSON is `{compiledNodeRef,revision,contentHash,pointerSet,approvalIssued}`.
The compile writes a revision even for unchanged text and issues no approval.
`approvalIssued:false` is not the revision's overall approval status; read
that with `node get`. Compile, approval and serving are separate operations.

Compile refusal exit codes: permission/grant required 8; unavailable include
4 (missing and unreadable concealed together); retired target, missing/invalid
source, retired/unavailable include revision or CONFLICT 5; malformed,
cyclic, too-deep, disallowed include or compile limit exceeded 2. Other errors
retain the normal API mapping. A lost response is 7 and has an unknown outcome:
check the compiled node with `node get` before compiling again. The message
names the cause class in parentheses (a dropped connection, a timeout, an
unreachable server or a gateway's HTTP status; cli#1157), never the response
body. No automatic retry is made, including after a conflict.

hadron team worker compile <worker> [--no-set-pointer] [--as-worker]

### Batch: every changed worker in an App (cli#1463)

hadron team worker compile [--all] [--only <names>] [--apply] [--no-set-pointer] [--as-worker] [--app <ref>]

With no worker argument, the command compiles the App's live workers in one
run. It is a dry run unless `--apply` (no mutation). By default only workers
whose sources changed are selected: the server's `RECOMPILE_AVAILABLE` lint on
the compiled node (cor:agt:040:13) decides, read in batched `nodeBatch` pages.
A worker with no compiled prompt gets its first compile. `--all` selects every
live worker. `--only a,b` (names, ids or URNs) limits the run; a name that is
not a live worker exits 4 before anything is written. Reads: one slim roster
read per 200 workers and one lint read per 200 compiled prompts; no per-worker
name resolution and no include-target reads.

Each selected worker is compiled once, by id. JSON:
`{app,applied,all,toCompile,compiled,unchanged,skipped,failed,unknown}`, every
list an array, never null. `toCompile`/`unchanged` rows:
`{name,id,urn,compiledNodeRef,reason}`. `compiled`:
`{name,id,urn,compiledNodeRef,revision,contentHash,pointerSet,approvalIssued,approveCommand}`.
`skipped` (BOOT_PROMPT_NO_SOURCE, BOOT_PROMPT_TARGET_RETIRED) and `failed`
(any other refusal): `{name,id,urn,code,exitCode,message}`. `unknown`:
`{name,id,urn,compiledNodeRef,reason,outcome,revisionBefore,revisionAfter,approveCommand}`;
`outcome` is "" for a worker the plan could not decide (lint or node
unavailable: not compiled; `--all` compiles it — a lint read that fails as a
whole leaves every compiled worker here with that read's exit code, e.g. 7,
and `--all` still compiles them with `revisionBefore` null), and `landed` / `not-landed` /
`unknown` after a lost response, decided by ONE revision (or, for a first
compile, pointer) read against the baseline. The compile is never re-sent.

Never approves: `approveCommand` is the `hadron node approve <ref> --revision N`
line to run after reading the revision (with ` --as-worker` appended when the
batch itself ran with `--as-worker`). Blind spot: a new install-memory
override at an address the last compile read from the Agent is not flagged;
use `--all`. Exit: 0 when all are compiled/unchanged/skipped; otherwise the
highest of 7 (lost response), 1 (undecided worker) and each failure's code.
`--all/--only/--apply` with a worker argument exit 2.

### Batch approve: every unapproved compiled worker prompt (cli#1466)

hadron team worker approve [--only <names>] [--apply] [--as-worker] [--app <ref>]

Approval only (hadron-server#2973 ruling (a)): no signing request, no step-up,
no register grant read. With `--as-worker` (cli#1872) the approvals go out as
the bound worker and the server admits each only under a live
`bootprompt.compile.approve` grant naming that compiled node; `authority`
then carries a second constant naming that path (same JSON keys). A read-only preview unless `--apply`. Selection: live
workers with their own compiled prompt (`bootPromptRef`), target not deleted,
approval state `NOT_APPROVED` or `SUPERSEDED`; install- and Agent-level prompts
are out. Reads: one roster read per 200 workers, one `nodeBatch` approval-state
read per 200 compiled prompts (a failed read aborts before anything is sent),
and `verifyNode` on each `SUPERSEDED` row. `--only a,b` (names, ids or URNs,
duplicates collapse) narrows; a name that is not a live worker exits 4 and an
empty `--only` exits 2, before anything is sent; a worker argument exits 2.

Apply sends ONE `approveNode(ref, revision)` per selected worker, in roster
order, pinned to the preview's revision (`REVISION_MISMATCH` exit 5: preview
again). Never `approveMemoryNodes`. The server decides authority per item;
the CLI checks none, and a refusal does not stop the others. Each approval is
its own transaction, no rollback.

Tamper is only the server's `verifyNode` verdict: `SUPERSEDED` there means a
legitimate edit (`toApprove`); `TAMPERED` goes to `tampered`, is never sent and
makes the run exit 5 (route: investigate, then `node approve <ref> --revision N`).
Any other verify answer (refused, empty, `INTACT`, `NOT_APPROVED`) leaves the
row in `unknown`, never sent (exit 1). Residual: a tamper between preview and
apply is not caught.

After each approval, ONE `Worker.bootPromptServe` read reports what a serve
would give now, as a snapshot, never "now live": `serve{servedRevSeq,refusal,unread}`
(human: "a serve now would give rev N" / "would still give rev M" / "would be
refused: CODE" / "serving state unread"). It never changes the exit code. A
lost approval response gets ONE approval-state read, outcome `landed` /
`not-landed` / `unknown`, exit 7, never resent and no serve read.

JSON: `{app,applied,authority,limits,toApprove,approved,alreadyApproved,tampered,skipped,failed,unknown}`,
every list an array. Target rows `{name,id,urn,compiledNodeRef,revision,state,reason}`;
`approved` `{name,id,urn,compiledNodeRef,revision,serve}`; `failed`
`{name,id,urn,compiledNodeRef,revision,code,exitCode,message}`; `unknown`
`{name,id,urn,compiledNodeRef,revision,outcome,reason}`. Exit: the maximum of
0, 1 (undecided row or an empty approval success, which gets the same one
status read), 4, 5 (server `REVISION_MISMATCH` / `NODE_IS_PLACEHOLDER`; an
encrypted-memory prompt, skipped and never sent; a tampered row), 7 (lost) and 8 (`BOOT_PROMPT_APPROVAL_FORBIDDEN`,
`BOOT_PROMPT_WORKER_SESSION_REFUSED`, which `node approve` also maps to 8).

## Memory merge, verify and settings (cli#1340)

`memory merge <source> --into <target>` sends mergeMemories once: every source node moves to the target, or folds into the target node at the same loc (the node merge strategy; `--field` repeatable, default all). Edges follow; the source is left empty and `--delete-source` deletes it. Confirms on a terminal, `--yes` required otherwise (exit 2, nothing sent). The server's refusals (encrypted, system/app class, write access, `MERGE_TARGET_LOC_TOMBSTONED`) map through the exit-code table. A refusal after some batches committed keeps those batches and never deletes the source; re-run the same merge to finish it. `--json`: the target memory `{id,urn,name,shortDescription,class,visibility,organizationId,isEncrypted,maxRevCount,updatedAt}`.

`memory verify <memory>` reads verifyMemoryNodes (#1591): `--json` `{memory,checked,intact,notApproved,superseded,tampered,problems[{nodeId,urn,loc,state,revision,approvedRevision,expectedHash,actualHash}]}` (problems = every non-INTACT node, by loc; `[]` when none). Exit 5 when `tampered > 0`, after printing; NOT_APPROVED/SUPERSEDED alone exit 0.

`memory set` settings: on a change, an omitted flag keeps the value and an empty one (`--license ""`, `--data null`) clears it; on create an empty value is exit 2 (nothing to clear). `--chunk-tokens/--chunk-overlap/--force-fixed-size/--acknowledge-vector-inversion-risk` are change-only (exit 2 on create). With `--app --agent` only `--data/--embedding-source/--vector-index` apply; `--app` alone takes none (exit 2). An explicit `=false` is a value: `--vector-index=false` sends false and `--no-vector-index=false` sends true; a pair that disagrees (`--vector-index --no-vector-index`, the fixed-size pair likewise) exits 2. An empty `--data-file` path exits 2 (use `--data ""` to clear); the file is read once. The server owns the rules (class defaults, GitHub-only `--git-source`, branch syntax, chunk arithmetic, the encrypted-vector acknowledgement). Moving a memory to another organization is `memory transfer`, not `memory set`. `memory list --org <org>` is that organization's view (empty for a non-member); `--visibility PUBLIC` lists the marketplace. `scope list --app-context <app>` lists the scopes a bare name resolves to in that App. `memory encrypt --passphrase-file <file|->`: the server derives the key; the passphrase is never printed. Files must be private (chmod 600); stdin is unchanged. Encryption key material travels in the mutation's request header only, never GraphQL arguments or lookup requests. The server-resolved USER principal sends a target-only memory-key entry; an owning APP completion uses a data key in the operation-key header, never a passphrase. No argument fallback or automatic retry. An older server or stripped header refuses loudly; a null answer or isEncrypted:false is not confirmed, so inspect before retrying. A confirmed isEncrypted:true answer stays exit 0 even if metadata repeats the secret: those string fields are blanked, detailsWithheld:true is added, and text prints a fixed successful receipt. Other metadata is unchanged. On Windows use --passphrase-file - (stdin), because private-file permission checks are unsupported.

## Opt-in node source-lint warnings (#916 C3c)

`node get|add|update|approve|move|clone|merge|restore --lint-warnings` adds a separate current advisory read, paged completely. JSON omits `lintWarnings` by default, returns null when the requested warning read is unavailable (including older/denied providers), or `{items,total,nextCursor:null}` when complete. Human warning text is terminal-safe; JSON preserves server text/nullable fields. An unavailable advisory read also prints exactly one fixed stderr class per command: `lint warnings: unavailable (advisory read did not complete)`. No underlying error or concealment reason is exposed. A failed warning read never retries or changes a successful mutation, stdout/JSON or exit; it reports no conclusion, not zero warnings. This is not a snapshot of the authored revision. Excludes actual --body-only/--dry-run. Advisory reads share a five-second budget across the command, including every batch-get node; timeout leaves the main receipt successful with warning state unavailable. Main response-extension warnings are unchanged; this flag controls only the extra source-lint read.

The worker and served-prompt advisory surfaces use `<class> [signature lint, prompt lint]`:
`<class>` is the fixed stderr prefix `lint warnings: unavailable (advisory read did not complete)`.
Only failed fields appear. The fixed labels are `signature lint`, `prompt lint` and
`current lint`; worker get uses the fixed order signature then prompt, with the
`, ` separator. Worker update names signature lint; served-prompt and history
name current lint. The bracketed field-label suffix is separate from the prefix;
node lint stays bare (no suffix). The first call's labels win, so callers with
several fields must aggregate before calling. The diagnostic names no cause.
Run the command again to retry the advisory read; recompiling can affect a
future served prompt but cannot repair its recorded revision.


## Skill lint server judgment (#1393)

`skill lint` reports the server's LINT-plan findings, including `skill-include-broken` warnings, using the explicit selected memories and filtering exact selected node IDs. The server judges the memory-wide corpus, so a selected node can carry a collision finding caused by an unselected node. Warnings preserve the server message and remain exit 0 (`--strict` promotes them to 5); error findings exit 5. Row order is documented, not sorted: hosted findings in host declaration order and then server entry/finding order (the Nth identical finding under another host merges into the Nth row with more hosts, so repeated neutral messages within one host still count separately), host-free unknown-key rows, node-scoped `skill-node-not-judged` warnings, then unavailable-node rows. Literal notice text alone is never interpreted as a broken include. A failed, unavailable, or malformed plan returns an error, never a clean result and never a local fallback; there is no offline mode. If a syntactically valid plan returns no judgment for a selected declaration to a host this CLI supports, the CLI preserves the other server findings and adds a `skill-node-not-judged` warning whose `hosts` names that host and whose `memory` is the selected memory URN, rather than reporting that declaration clean. An unknown-host warning does not cover a known declaration beside it. Retry once if the corpus may have changed between host plans; if the omission persists, report a server `skillPlan` defect with the node and host. Unknown exports keys use the structured `keys`/`keyCount` fields. When the list is untruncated, a node with any locally-unknown key absent from it produces ONE host-free, non-blocking `skill-node-not-judged` warning (per node, not per key; exit 0 unless `--strict`), so it is not reported clean. A missing entry or one with no findings produces the same warning; truncated lists retain their own aggregate warning and are not over-interpreted. Host-free consistency checks cover selected nodes only. Unknown-key and not-judged findings remain warnings under `cor:agt:030:06`; local/server declaration-scope disagreements never become blocking synthetic errors. `scanned` is also memory-wide and includes intentional `exports: {}` gaps, so it is not treated as selected-node proof. The server must provide `SkillPlan.unrecognized` with its `keys` and `keyCount` fields; an older server without them fails through normal GraphQL error mapping. No rendered body is fetched and no file is written. Skill export continues to use the provider-resolved body.

hadron team session served-prompts <session-id> [--lint-warnings] [--limit N] [--offset N] [--provenance-limit N] [--provenance-offset N]
hadron team session outcome <session-id> <0..1> [--ref <text>] [--meta <json>]
hadron run served-prompt <run-ref> [--lint-warnings] [--limit N] [--offset N | --all]
hadron team worker served-prompt <worker-id-or-qualified-URN> [--limit N] [--offset N | --all]

# Editing a task (cli#1137)
`hadron team task update <task>` edits a task after creation through one
`updateAgentTask` mutation; the task keeps its id and handle. Only named flags
are sent (the server reads the input as a strict patch): omitted means
unchanged, and with no flag at all the command exits 2 locally and sends
nothing. `--title` (cannot be emptied; the server refuses an empty one and its
refusal is printed), `--description` / `--clear-description`, repeatable `--tag`
(replaces the WHOLE list) / `--clear-tags`, `--require-role` / `--family-in` /
`--family-not-in` / repeatable `--exclude-worker` / `--min-tier low|mid|high` (any of them replaces the
WHOLE requirements) / `--clear-requirements`, `--start-by <RFC 3339>` /
`--clear-start-by` (re-anchors only the not-started clock; a raised alarm stays
raised), `--properties <json|@file>` / `--clear-properties` (the task's OWN JSON
object, replaced whole, never merged; see Task properties below), and the tri-state `--close-session` / `--no-close-session` (neither =
unchanged; `--close-session=false` and `--no-close-session=false` exit 2, say no with `--no-close-session`). A value flag with its `--clear-…` twin, both close-session flags, or
`--family-in` with `--family-not-in` exit 2 before any request. Not editable
here: source, owner, assignee, status, parent, priority, blockers. Text output
is `task get`'s; `--json` is the updated task. Refusals keep the server code and
mapped exit and print no task: TASK_ENDED (exit 5), TASK_NOT_OWNER (8; an
assignee alone may not edit), TASK_NOT_FOUND (4), BAD_USER_INPUT (2). Requires
a server containing hadron-server#2645 (76792bd9 or later); older servers reject
the unknown field as schema skew.

# Task properties (cli#1118, server#2420)
A task carries one optional JSON object of its own, `properties`, which the team
organises into namespaces (review schedule, complexity, a seat's reviewed head)
and the platform stores and never reads. `team task create|update --properties
<json|@file>` sets it (update replaces the WHOLE value; create sets it only when
the creator may write properties, else the task is made without it, no refusal),
`team task update --clear-properties` removes it (sends null), and an omitted
flag leaves it unchanged (the key is absent on the wire). `--properties` and
`--clear-properties` together, text that is not JSON, an empty value, a literal
`null` (use `--clear-properties`) or an unreadable `@file` exit 2 before any
request. `task get` and every task read carry `properties` in `--json` (null when
unset, never a missing key; a child shows its OWN value, nothing is inherited)
and print a `properties:` row in text when set. The server owns the rest, each
printed with its code: PROPERTIES_NOT_AN_OBJECT, PROPERTIES_TOO_LARGE (16 KiB of
JSON) and PROPERTIES_SCHEMA_VIOLATION exit 2; PROPERTIES_SCHEMA_UNUSABLE (the
team's task-properties schema cannot be used) and TASK_ENDED exit 5; an
assignee alone may edit properties and references (--properties/--clear-properties,
--ref/--clear-refs; references need a server at or after hadron-server#3718
2026-11-08), and naming any other field, even beside those, refuses the whole
edit TASK_NOT_OWNER (8). Needs a server at or after hadron-server
2026-10-30 (the #2420 contract, merged as hadron-server#2877).

## Task priority and dependencies (cli#1135)
`hadron team task create <title> --priority 2 --blocked-by AT1.2 --blocked-by <id>`
sets optional priority and blockers in the same create mutation. Omitted fields
stay omitted: the server supplies default priority 1. Priority ranges from 1 to
5 (1 = highest) and affects matching; ordinary list order stays server-returned
newest-first. `task set-priority <task> <priority>`, `task add-blocker <task>
<blocker-task>` and `task remove-blocker <task> <blocker-task>` each send one
mutation. Task refs are ids or server-supported handles, passed through trimmed.
The server decides assigner authority, same-App visibility, cycles, permitted
states and atomic refusal. Starting a blocked task is refused. Completed or
cancelled blockers and removed edges release blocking on the server.
Get/list and write JSON include priority, blocked, blockedBy and blocks. The two
relation fields are bounded pages {items:[{id,handle,title,status}],total,hasMore}
with the first 20 entries, not complete arrays. `task dependencies <task>
--limit 20 --offset 20 --json` reads later pages in both directions; follow each
hasMore separately. Text `list` displays priority, blocked and both dependency
summaries; `list --team` displays priority and blocked without the dependency
summaries (its --json still carries the pages); text get displays the bounded
pages. A page item that is null or has no id is a malformed answer: the command
refuses it (exit 1, "server returned a malformed blockedBy|blocks|clones page")
and prints nothing, instead of dropping the item. No local priority sort or
dependency policy. Refusals preserve the server code/message and mapped exit
code, print no task, and never retry the mutation. `set-priority`, `add-blocker`
and `remove-blocker` show no server warnings (as the simple task mutations);
create, assign, reassign and finish do.

hadron team task update <task> [--title <t>] [--description <d> | --clear-description] [--tag <t>… | --clear-tags] [--ref <kind>=<target>[|<label>]… | --clear-refs] [--require-role <r>] [--family-in <f,…> | --family-not-in <f,…>] [--model-group <name,...>] [--model-group-not <name,...>] [--exclude-worker <w>…] [--min-tier low|mid|high] [--clear-requirements] [--start-by <rfc3339> | --clear-start-by] [--finish-by <rfc3339> | --clear-finish-by] [--properties <json|@file> | --clear-properties] [--close-session | --no-close-session]
hadron team task clone <task> [--parent <task> | --no-parent] [--status pending|backlog] [--title <t>] [--description <d>] [--source <s>] [--priority 1..5] [--assignee <party>] [--start-by <RFC3339>] [--finish-by <RFC3339>] [--close-session | --no-close-session] [--tag <t>… | --clear-tags] [--ref <kind>=<target>[|<label>]… | --clear-refs] [--properties <json|@file> | --clear-properties] [requirement flags | --clear-requirements] [--owner <party>] [--project]
hadron team task create --batch-file <path|-> [--distinct-assignees]
hadron team task set-priority <task> <priority> | add-blocker|remove-blocker <task> <blocker-task> | dependencies <task> [--limit N] [--offset N]
  Instructed task writes (cli#2422, cor:dmo:050:12): `--instruction <kind>:<ref>` is accepted by `team task create`, `update`,
  `set-priority`, `add-blocker`, `remove-blocker`, `assign`, `reassign`, `cancel`, `park`, `ready`, `dispose-finding` and
  `release`. kind is `team-chat-message`, `task` or `live-chat` and the reference after the first colon is sent as written. It
  claims that the bound worker's holder told it to make this one change; the claim rides only the write (a lookup before it, such
  as `release`'s read of the outside item or `create`'s App lookup, carries none), and the worker session attaches as it does
  without the flag. A malformed instruction exits 2 before any request; `create --instruction` with `--batch-file` exits 2 (the
  batch write takes no claim). The server decides admission; its refusal is shown once and never retried without the flag. Without
  the flag, the requests and `--json` are unchanged. Needs server 2026-11-04 or later. `team stall-schedule set` and `team alarm ack` take it too, at the same server floor: see
  "Instructed alarm acknowledgement and stall schedule" below.

Task priority/blocker fields require server #2726 (2026-10-29 or later) on every
target deployment before releasing/installing this CLI; old servers reject the
shared task GraphQL documents. This PR does not authorize a release or deploy.
The task error mapper prefixes the known codes TASK_BLOCKED, TASK_BLOCKER_CYCLE,
TASK_NOT_FOUND, TASK_NOT_OWNER, TASK_INVALID_TRANSITION, TASK_HANDLE_QUALIFY,
TASK_ENDED, TASK_NOT_ASSIGNEE, TASK_CHILD_OWNER_MISMATCH, TASK_OWNER_INVALID,
TASK_ASSIGNEE_INVALID, TASK_STOPPED and BAD_USER_INPUT onto the mapped server message, including existing create/assign/reassign/start/
progress/cancel/park/ready/finish refusals. Exit classification is unchanged;
the underlying error chain is preserved. Task-ref commands do not use --app to
qualify a handle: use an id or hrn:agenttask:<root>:<app-slug>:AT1.2 when the bare
handle is ambiguous in the server-side caller context.
Dependency list cost: each row resolves both bounded pages independently on the
current server; --team scans all task pages. These reads are not batched yet
(server#2764). Prefer get/dependencies for a known task when you do not need a
whole-App or team view.

Dark-worker alarms: team delivery-settings get shows darkAlarmWindowSeconds;
team delivery-settings set --dark-alarm-window-seconds <seconds> changes only
that field (server bounds 300–86400, default 1800). Omitted preserves.
team events --alarms names a task-less dark-worker alarm's worker and known
reason (overdue or wake-unacked). A task-less alarm.lead-cadence (alarm.coordinator-cadence on events raised before the rename) shows
coordinator <name>, with no reason suffix or payload evidence; a missing
subject and other task-less types keep —; a task-less alarm.start-failing (wake handle START: the platform's starts of a long-lived worker keep failing) shows worker <name> the same way, its failure count and window only in --json. The same subject appears on alarm
ack receipts. Metadata stays in --json exactly as returned and may be withheld.
team alarm ack <eventId> acknowledges escalation; it does not end the episode.
A dark episode ends when checks recover and no wake is unacknowledged past
the window. These commands do not activate delivery or calculate check state.

# Task references (cli#1520, server#2649, cor:agt:050:01/:09): `create --ref <kind>=<target>[|<label>]` (repeatable, order kept; kind issue|pull-request|pull_request|node|task|url, case-insensitive; the label starts at the first `|`) and `update --ref …` (replaces the WHOLE list) / `--clear-refs` (sends `[]`); either one alone is a valid update, both together are exit 2, neither leaves the list alone. `list --ref <target> [--ref-kind <kind>]` matches a target exactly (never a prefix; the label is ignored); `--ref-kind` alone, an unknown kind, a `--ref` without `=`, or `list --team … --ref` is exit 2 with nothing sent. The CLI checks only the `kind=target` shape: form and caps (20 references, 512-character target, 120-character label) are the server's (BAD_USER_INPUT, exit 2, naming the item's position). The task `--json` always carries `references: [{kind, target, label}]` (`[]` when none, `label` null when absent); text prints a `references:` block of `<KIND> <target> (<label>)` lines. Targets and labels are printed as given (terminal-safe in text, verbatim in `--json`) and never resolved or fetched.
# `team task clone` and `team task create --batch-file` (cli#1565, server#2646/#2653, cor:agt:050:02/:06):
- `task clone <task> [--parent <t> | --no-parent] [--status pending|backlog] [--title] [--description] [--source] [--priority] [--assignee worker:|user:] [--start-by] [--finish-by] [--close-session|--no-close-session]` makes a NEW task from an existing one, in its App, linked to it (one `cloneAgentTask` call; the source is never changed). Only named flags are sent: omitted keeps the source's value or leaves the field unset; a given flag sends its value, an empty one included. `--no-parent` sends `parentTaskId: null` (a root under AT0), `--parent` names another, neither keeps the source's parent. `--parent` with `--no-parent`, `--no-parent=false`, an empty `--parent`, `--close-session` with `--no-close-session`, a bad `--status`/party/time are exit 2 with nothing sent. Refusals: unknown/unreadable/other-App source or parent exit 4 (TASK_NOT_FOUND), AT0 itself exit 2, TASK_NOT_ASSIGNEE/TASK_NOT_OWNER exit 8, TASK_ENDED exit 5. `--tag`/`--ref` replace the copied list and `--clear-tags`/`--clear-refs` send `[]`; `--properties` replaces and `--clear-properties` sends `null`; the requirement flags (`--require-role`, `--family-in`, `--family-not-in`, `--exclude-worker`, `--min-tier`, `--model-group`, `--model-group-not`) replace the WHOLE requirements and `--clear-requirements` sends `null`; `--owner worker:|user:` names the owner (never cleared); `--project` sends `project: true`. A value flag with its `--clear-…` twin, `--project` with `--parent`, `--project=false`, and an empty `--tag`/`--owner` are exit 2 with nothing sent; `--project --no-parent` is sent. `TASK_OWNER_INVALID` is exit 2. A `--properties` value from a caller who may not set properties is NOT a refusal: the clone is made without properties and the value is not judged (cor:agt:050:28); `PROPERTIES_*` refusals are only for a caller who may set them. `--json` is the created task plus `warnings`, as `task create`. Needs a provider with server#2646 (older: schema skew, exit 2).
- `task get` also prints/returns the clone links: `--json` adds `clonedFrom` (`{id,handle,title,status}` or `null`) and `clones` (`{items,total,hasMore}`, items `[]` when none; page with `--clones-limit` (default 20, cap 200) / `--clones-offset`). Text says `replaces:` when the source is FINISHED or CANCELLED, `cloned from:` otherwise, and lists clones. Other task commands' `--json` is unchanged. `get` needs server#2646.
- `task create --batch-file <path|->` creates 1 to 25 tasks in ONE `createAgentTasks` call (all or none; never retried, not idempotent: after a lost answer list the App's tasks first). The file is a JSON array of `{"task": <CreateAgentTaskInput>, "blockedByPositions": [<1-based position>…]}`; every task field the single create takes (matching included) is read straight from the file in the server's own field names, so nothing is batch-specific. Items go in file order; an omitted key is omitted on the wire (never null). Unknown, mis-cased or duplicate keys, a missing `task`, a wrong value type, 0 or more than 25 items, `--batch-file ""`, stdin `-` from a terminal, or any single-create flag/title with it (`--json`, `--app` do combine) are exit 2 with nothing sent. The App comes from `--app`/context/binding as for create. A refusal keeps its exit (TASK_BATCH_TOO_LARGE 2, TASK_BLOCKER_CYCLE 5, BAD_USER_INPUT 2, TASK_NOT_FOUND/APP_NOT_FOUND 4, TASK_NOT_OWNER/TASK_NOT_ASSIGNEE 8, TASK_ENDED 5) and, when the server names a task, `(task n of m)` in the text and an integer `position` in the `--json` error envelope (absent otherwise). `--json` is `{"tasks": [task…], "warnings": [{position, code, text, register}…]}` (tasks in list order; warnings `[]` when none; `position` null if the server names none); text prints one line per task and warnings on stderr.
- `--distinct-assignees` (with `--batch-file` only; cli#1567, server#3243, cor:agt:050:06 ruling 0084 A) sets the call-level `distinctAssignees` option on that one call: every task that asks for a match goes to a different worker (explicitly named assignees count toward the exclusion), and ANY failed match refuses the whole call, nothing created. Without the flag the argument is absent (not `false`); `--distinct-assignees=false` is sent as `false`. Without `--batch-file` it is exit 2 with nothing sent. `TASK_MATCH_FAILED` is exit 5; the text reads `(task n of m: <REASON>)` (e.g. `NOBODY_FREE`) and the `--json` error envelope carries integer `position` plus string `reason` (`reason` absent when the server names none or a non-code value). The same worker named twice is `BAD_USER_INPUT` (exit 2) at the second position.
# `team task list` filters (cli#1250): `--tag-any`/`--tag-all` (repeatable; a comma is part of a tag), `--source` (exact), `--external-id` (exact; cli#1524), `--overdue`/`--overdue=false` (tri-state; unset sends nothing; two arms on the server clock — start-by passed and not started, or finish-by passed and not ended; backlog, ended and a task with neither time are never overdue; the finish-by arm needs a provider containing hadron-server#2654; cli#2509), and inclusive `--created-/--started-/--finished-after|before` bounds taking RFC 3339 with Z or an offset (a date-only or offset-less value is exit 2, nothing sent; a task with no start/finish time never matches a range on it). Filters AND together; none combine with `--team`. With none set the request and `--json` output are unchanged.

# Chatbot served-prompt history (cli#1280, server#2865)
hadron chat tool-choice <chat-id> [--json]   # cli#2020 (hadron-server#4042/#4151, cor:dmo:050:10 Proposed, cor:acl:030:01): the server's tool-choice DECISION for the chat's next turn, read and never made here: AUTO, FORCED, or `tool choice: not resolved` (--json toolChoice null; a normal answer, no reason implied). --json = {chatId, toolChoice}. A chat you may not read answers exactly as a missing one (exit 4, same message, no hint). The decision only: no key, id, name, provider or model. An enum member this hadron does not know exits 1 naming it, never a default.
hadron ai-config tool-choice <provider> <model> [--json]   # cli#2020: the decision for a provider and model pair that need not be saved, passed verbatim (no trimming, case change or list here). An unknown pair answers FORCED, a normal result. Pure: no rows read, no usage metered; any signed-in caller, no --app/--org. --json = {provider, model, toolChoice}. Unknown enum member: exit 1.
hadron chat served-prompts <chat-id> [--limit N] [--offset N] [--provenance-limit N] [--provenance-offset N]
# Explicit chatbot chat id from the application, NOT a team-chat Channel id or node URN.
# Record-only chatServedPrompts query; no myChats scan, conversation, source bodies or live edges.
# No chat config, ambient App or worktree session. Read as whoever manages the first-record App;
# only that App's records are visible. No-App chats: platform admin, never impersonation, per current server rule.
# Server order (newest served time, then ordinal), one page: --limit 0..200 default 50, --offset >=0.
# Each record has an independent provenance page (--provenance-limit/offset, same bounds); 0 counts only.
# --json {servedPrompts:{items,total,limit,offset}}, items[] when empty; missing/denied/no-record/serving-off
# all conceal as an empty page, exit 0, never a manufactured refusal. Real provider errors retain mapped exits.
# Recorded variables may be sensitive. Pages are separate requests, not a snapshot. Requires server 2026-10-30.


### Team health sweep (cli#1181)

`hadron team health sweep --config <host.yaml>` runs on the registered Orca host.
It consumes only `hadron orca terminal status --all --json --config <host.yaml>`
(the separately gated C1 host-config dependency), without importing or duplicating
its classifier. That command uses delivery.orca_bin when set and it resolves, else PATH. Both
commands read only the host's 0600 key_file; no key value in argv, env or logs.

The sweep sends one setHostWorkerTerminals call with status, prompt, closed reason,
lastTurnAt and checkedAt for each registered worker. It never sends detail, screen
text or a terminal handle, never types and never skips paused workers. Missing or
malformed status reports, an unknown contract version, or more than 200 workers
refuse before writing. An empty registered host has no report to write.

The JSON receipt is {reports:[{worker,status,prompt,reason,lastTurnAt,checkedAt}],
refused:[{worker,code}],renames:[{worker(id),name,terminal,from,to,applied,error}],orcaVersion,dryRun}
(`renames` and `dryRun` are cli#2017 additions; `orcaVersion`, a string or null, is cli#2166's). Text is one safe Name/status/reason line per worker,
with an explicit refusal suffix for WORKER_NOT_FOUND or TERMINAL_REPORT_STALE.
No retries. A successful write exits 0 even with per-worker refusals or no
coordinator; no alarm/noCoordinator field is selected or used. Failed write or
malformed status exits 1; rejected key 3, server unreachable 7, forbidden 8, Orca
unreachable 10. No new exit 9. CLI input errors retain usage 2. Scheduling and
recovery are separate commands/slices, not installed by a sweep.

The status read is allowed 5 seconds per registered worker (never under 2 minutes);
`--status-timeout <duration>` overrides it. A read that runs out of time exits 1 with
`terminal status timed out after <duration>; nothing written` (cli#1838), distinct from
the generic `terminal status command failed` class kept for every other failure.

Tab titles (cli#2017). After the status batch is stored, the sweep lists the
host's Orca terminals and, for every tab whose worktree session binding names a
worker registered on this host, sets the tab title to that worker's bare name
(`team worker terminal-title`'s string) through `orca terminal rename` when it
differs; a second sweep renames nothing. Every tab of a worker's worktree is
covered (panes of one tab once); a tab with no registered worker (a plain shell,
another host's worktree) is never touched, and a tab is matched by binding, never
by its current title. `--dry-run` is on by default: a manual run changes nothing
and lists "would rename" lines (`renames[].applied` false, `dryRun` true). The
status batch is stored either way. The installed timer passes `--dry-run=false`
(re-run `hadron team health install` once on a host installed before cli#2017 to
pick it up). One tab failing to rename is `renames[].error: "rename_failed"` and
does not stop the rest; `renames[].orcaCode` (cli#2040) carries the error code Orca
answered with, an opaque string to pass through (null when the rename succeeded or
Orca gave no code; never branch on its values, `error` stays the constant), and the
text line reads `rename failed (rename_failed; orca: <code>)`; a failed rename or a failed terminal list exits 1 after
the receipt is printed. No chat post is made per rename.

The title pass needs Orca 1.4.232 or later (cli#2166: terminal list and rename with a
tab title; 1.4.230 made the host's tab name the one source, 1.4.232 added setting one
pane's title; live-verified on 1.4.247, nothing older tested). The receipt carries
`orcaVersion`, what `orca --version` printed when the title pass ran (a digit-led token
of at most 64 characters), else null: no registered worker, no answer, or something that
is not a version. The text receipt shows it as an `orca  <version>` line before the
titles. It is the operator's evidence only and is never sent to the platform. The pass
and the status read run `delivery.orca_bin` when that path resolves (exists, not a directory, executable),
else `orca` on PATH, so a stale host config no longer fails every sweep. A relative
`delivery.orca_bin` (a bare name or a path with a separator) is refused before any server
request, status read or Orca exec: exit 2, `delivery.orca_bin must be an absolute path; nothing written`,
as `orca status --config` refuses it (cli#2369); an unset `orca_bin` and an absolute one are unchanged.

A missing/non-live host registration is exit 8. Unresolved or ambiguous registered
workers, or a missing status row for any registered worker, refuse the whole
batch before writing (exit 1). Other hosts' status rows are ignored. JSON reports
are the sent reports, including any refused by the server; read refused too.

Context size (cli#2063, #2103): the worker's host-YAML `tui` selects the reader
(`claude` or `codex`); when absent, the worker's family supplies its native
interface. GLM/Kimi workers using Claude-format transcripts need `tui: claude`
and their own `claude_config_dir`. Provider identity stays unchanged; a custom
family without a supported TUI is not measured. Each report may carry `contextTokens` and
`contextMeasuredAt`, sent together or not at all and never 0. The host reads them
from what the worker's launcher wrote locally, for a worktree it configured for
that worker: reader `claude` = input + cache-read + cache-creation tokens of the
latest main-thread assistant turn in the newest transcript under
`$CLAUDE_CONFIG_DIR|~/.claude/projects`; reader `codex` = the latest token_count
event's last-turn `input_tokens` in the newest rollout under
`$CODEX_HOME|~/.codex/sessions`. `contextMeasuredAt` is that record's own time. Any
other reader, or a file that is missing, malformed, not attributable to the worktree
(the file's own cwd must equal it) or dated over 2 minutes ahead, sends neither
field and the row still reports. Only numbers are read; no transcript text is held
or sent. The JSON receipt shows both fields (omitted when not measured).
A server from before hadron-server#4238 refuses the whole batch for the unknown
fields; the sweep then retries once without them (only for that validation error),
so terminal health still stores and the receipt shows no context fields.
The sweep reads the sweep process's own `CODEX_HOME` / `CLAUDE_CONFIG_DIR` / `~`
unless the worker's host-YAML entry sets `codex_home` and/or `claude_config_dir`
(absolute paths; sessions are read from `<codex_home>/sessions` and
`<claude_config_dir>/projects`). Set them where the launcher gives a worker its own
account home (Orca does for Codex workers); otherwise those workers read as not measured.

### Team health install (cli#1374)

`hadron team health install --config <host.yaml> --interval <Nm>` writes one fixed
per-user entry that runs `<abs hadron> team health sweep --config <abs host.yaml> --dry-run=false`
(the flag makes the scheduled sweep apply the tab-title renames and the duplicate
fixes; a manual sweep only lists them): a systemd user timer (crontab line where
user units are unavailable) or a launchd agent. The entry carries the installing
shell's PATH; install refuses (exit 2) when neither delivery.orca_bin nor PATH
resolves `orca`. Installing twice with the same arguments changes nothing; a
changed interval or a changed command line (as cli#2017 added `--dry-run=false`)
rewrites the entry; a different --config refuses until `uninstall`, which removes
exactly what install wrote, also after a failed install. An entry an older CLI
wrote (without `--dry-run=false`) stays owned: reinstall replaces it in place —
and until it is reinstalled, the old entry runs the upgraded sweep DRY (the
flag's default): upgrading the binary never turns an existing timer into an
applying one by itself, only a fresh `install` does. `status` is read-only
(exit 0 even when not installed) and reads the last run from the scheduler;
under cron it prints `unknown`. The host key never enters a generated file,
argv or env. Do not install against production until hadron-server#2931 is
deployed.

### Team usage (cli#1472)

`hadron team usage` records how much of each model subscription account's 5-hour
and 7-day allowance is used, so "which account runs out first, and when" has data
behind it. It is NOT `hadron usage` (AI-call events the server recorded in your
memories); it never reads or writes those. It tracks every provider the team uses
(cli#1659): `family` is `claude`, `codex` or `glm`; a provider with no readable
source (`glm` today) is recorded as a finding (`sourceState: no-local-source`) and
listed by `show`, never silently dropped.

The operator sets up, per account: a status line script that writes the harness's
`rate_limits` to `$CLAUDE_CONFIG_DIR/hadron-usage/rate-limits.json` as
`{"observedAt":N,"five_hour":{"used_percentage":N,"resets_at":N}?,"seven_day":{...}?}`
(Unix seconds, atomic temp-file rename; a window the harness omits stays out; the
CLI never edits a harness settings.json); optionally a ccusage file made with
`CLAUDE_CONFIG_DIR=<dir> npx ccusage@<pinned> blocks --json --offline > <file>` (the CLI
never runs ccusage or any subprocess); and a host YAML `--config` with `memory`,
optional `host`, and `accounts: [{label, configDir (absolute), family: claude,
ccusageFile?, workers?: [names]}]`. A label is the operator's own name; one with
`@` is refused. `workers` is never derived from transcripts: absent means
`workersConfigured:false`.

`family: codex` needs no setup: `configDir` is the account's Codex home (a clean
absolute path) and `collect` reads the newest `sessions/YYYY/MM/DD/rollout-*.jsonl`
under it, taking only the latest `token_count` event's `rate_limits` (`used_percent`,
`window_minutes`, `resets_at` for `primary` and `secondary`). No network, no API key.
Nothing else from a rollout (conversation text, token counts, `credits.balance`,
`plan_type`) is stored, printed or logged; a malformed or oversized line is skipped,
and a symlink or non-regular file is never followed. A rollout with no `token_count`
yet is `sourceState: no-reading-yet`, not 0.

`collect` validates every account before writing; any problem (missing or malformed
config, unreadable/malformed/symlinked snapshot or ccusage file, `used_percentage`
outside 0-100, an `@` label) is exit 2 and writes no record for ANY account; an
absent snapshot file is "no data" (windows absent). Errors never echo file
contents. Records are objects in collection `account-usage` (schemaVersion 1, absent
values omitted, not recorded as 0): `label, family, host, observedAt, fiveHourPct,
fiveHourResetsAt, sevenDayPct, sevenDayResetsAt, tokensSinceLast,
estCostUsdSinceLast, ccusageBlocks, workers, workersConfigured, schemaVersion`; Codex
records add `primaryPct, primaryMinutes, primaryResetsAt, secondaryPct,
secondaryMinutes, secondaryResetsAt` (each window keeps its own length, never
relabelled as 5h/7d) and `sourceState` (`ok | no-reading-yet`; glm:
`no-local-source`); a Claude record is unchanged;
`ccusageBlocks` is the checkpoint (blocks that started within 6h of the
observation) and the since-last fields are the growth of those blocks since this
account's previous record on this host and family (absent when that cannot be
measured, e.g. a block's totals went down a checkpointed block left the file, or a block older than the previous observation appeared).
The shape is pinned by a golden test and declared in the CLI, not in `team init`
(a server-declared collection is a follow-up with the portal).

`show --json` is `{memory, accounts:[{label,family,host,observedAt,fiveHour,sevenDay,
primary?,secondary?,sourceState?,workers,workersConfigured,points}]}`, each window
`{usedPct,resetsAt,expired,minutes?,burnPctPerHour,exhaustsAt,exhaustsBeforeReset}`
(a null field means not known; `primary`/`secondary` appear only for codex accounts and
`sourceState` only for codex/glm; `accounts` is `[]` when nothing is recorded).
"Runs out first" ranks every window of every family by the earliest `exhaustsAt`
before its own reset; the minutes label a window, they never rank it. `install|uninstall|status` mirror `team health` as a
separate entry; each removes only its own entry.

### Agent ticker templates (cli#913; MCP cut C1)

`agent ticker-templates` (alias `ticker-template`) exposes per-template CRUD.
Reading requires Agent read access; creating/updating/deleting requires managing
the Agent: its user owner, its organization's OWNERs and ADMINs, or a platform
admin. Managing an App alone is not enough. The server decides authorization.
Templates are snapshots for future casts: editing or deleting never changes
existing workers or their tickers. An App manager can separately add the template
tickers a worker lacks with `hadron ticker apply-templates --worker <worker-ref>`;
it changes no existing ticker. Cadence is requested whole
seconds; casting raises it to the App's floor, never a client clamp.

List preserves the server's name/id order and returns a JSON array (`[]` empty).
Create/update return one object:
`{id,revision,shapeVersion,name,tickCode,cadenceSeconds,quietHours,relevance,reasons,createdAt,updatedAt,agent:{id,name}}`.
Reasons are lower-kebab strings and always an array; quietHours and updatedAt
are null when absent. Update/delete address templates by opaque id, never list
position; each name is unique within its Agent.
Delete returns `{id,deleted:true}`; it soft-deletes only the template and requires
--yes without a TTY. No runtime ticker/subscription/worker mutation is added.

Create requires --name and --cadence-seconds; absent optional values are omitted
for server defaults. Update sends only changed field flags, requires an explicit
--expected-revision and at least one field, and preserves omitted fields.
--quiet-hours takes a JSON object; --clear-quiet-hours removes it and conflicts
with --quiet-hours. --reasons must be nonempty; quiet-hours timezone/window and
cadence bounds are validated by the server. Delete also requires the revision
read from that template. A mismatch is sent once and never retried or re-fetched.
All three writes deliberately carry no bound worker-session attribution.

Exit 2: missing/invalid flags, BAD_USER_INPUT, TICKER_CADENCE_OUT_OF_RANGE or
TICKER_QUIET_HOURS_INVALID. Exit 4: AGENT_NOT_FOUND (also unreadable Agent) or
TICKER_TEMPLATE_NOT_FOUND. Exit 5: TICKER_TEMPLATE_NAME_TAKEN,
TICKER_TEMPLATE_LIMIT_REACHED or TICKER_TEMPLATE_REVISION_MISMATCH. Exit 8:
FORBIDDEN/caller cannot manage the Agent. Standard auth/transport errors retain exits 3/7; malformed
success payloads are errors, not fabricated success.


## App write grants and credential identity (#1338)

```text
hadron app memory-grant add <app-ref> --memory <ref> --prefix <loc> [--register-node <node-urn>] [--yes]
hadron app memory-grant list <app-ref> [--include-revoked] [--instruction <kind>:<ref>]
hadron app memory-grant rm <grant-id> [--yes] [--instruction <kind>:<ref>]
  # list/add/rm grant JSON adds server-derived notEffective (bool|null) and
  # notEffectiveReason (string|null). Text marks true rows as not-effective,
  # with the reason when returned; false/null rows have no mark. A concealed
  # memoryUrn has both fields null, and so do prefix and registerNodeUrn
  # (hadron-server#4734, cor:agt:010:01; cli#2868): JSON `prefix` is a string or
  # null, never "" (an empty prefix from the server is refused as an incomplete
  # grant, exit 1), and a null prefix never rejects the row. Text shows ? for
  # memory, prefix and register on a concealed row (a null register on a visible
  # row stays -); the CLI never infers a withheld value. A not-effective grant is still unrevoked:
  # it stays in the default list until a manager revokes it; the mark can go
  # away if its target qualifies again. Older providers lacking the fields
  # still support list/add/rm; both metadata fields are then null (unknown).
  # Only a typed pre-execution validation refusal for these exact fields on
  # AppMemoryWriteGrant permits one legacy-document attempt, same inputs/actor.
  # No transport/runtime/mixed/partial-result error is retried.

hadron app whoami
hadron run latest <app-ref> [--previous]
hadron app asset-upload enable|disable <agent-ref> [--yes]
```

Memory grants let an App's userless runs create/update under one loc prefix
and its colon-boundary descendants, never a whole memory. The server requires
a person managing the App; list/rm also admit their held worker with
--instruction (see Instructed credential acts below). Setters must also write the
memory. Personal/private targets and listing are owner-only, shared targets
must belong to the App's org/team space, encrypted/system targets are refused.
No read access or authorization bypass is created. The CLI asks once and
reimplements none of those checks. Add/rm confirm (scripts need `--yes`), execute at most one mutation,
and keep nullable metadata (including withheld `memoryUrn`) as null in
JSON. Text uses `?` for a concealed/null memory and `-` for other null metadata.
List is an array, `[]` when empty, oldest-first server order, revoked rows
only with `--include-revoked`. Explicit `--include-revoked=false` sends false;
when absent it is omitted. A register node is only a citation, not a lookup or
proof; it is omitted unless supplied. Refusals preserve mapped exit/serverCode.
If a write's answer is lost, list (including revoked rows) before retrying.

`app whoami` reads `myApp`, not `--app` or a configured App: JSON is
`{app: metadata|null}`; a null App is success and is never replaced by an
ambient lookup. Only identity metadata is requested, never keys/settings.
`run latest` asks for a concrete run (same detail/JSON as `run get`), optionally
the previous one. `--previous=false` sends false; absence omits it. No magic
run ID or fallback App is constructed; a null run is exit 4 (none readable).

Asset upload takes an Agent ID or qualified URN, not an arbitrary installed
App. The server checks Agent readability and org-admin status, rejects
user-owned Agents as unsupported, and toggles `asset:upload` on the Agent's
own backing App. This is the v1 tool allowlist, not Memory.acceptsUploads.
Both enable and disable confirm (`--yes` non-interactively); disabled is sent
as explicit false. JSON `{appId,appUrn,agentRef,enabled}` reflects the returned
App's allowlist, not an assumed outcome. One mutation, never retried. No key,
credential or file content is requested. No client authorization is added.

For these new commands, auth/transport refusals retain exits 3/7; FORBIDDEN and
AVAILABILITY_NOT_PERMITTED exit 8, NOT_FOUND exits 4, BAD_USER_INPUT exits 2.
Asset-upload setting providers containing server#2979 (dadc314a) send
PERMISSION_DENIED (shared exit 8) and concealing NOT_FOUND (exit 4 for missing or
unreadable Agent alike). Lookup INTERNAL_SERVER_ERROR stays exit 1 with requestId.
Older providers may still conceal domain errors as internal faults. Classify
only extensions.code, never prose; no scoped override or retry.
INTERNAL_SERVER_ERROR (exit 1) is an unknown outcome: it can be a concealed refusal or a real server fault, so do not assume nothing changed, and this command does not retry.

## Resend senders and platform invitations

`org resend-sender` manages the organization's stored Resend configuration;
it does not send email. The server checks membership for reads and org
ADMIN/OWNER for writes. Create takes an org ID, slug or URN via `--org`;
get/update/rm take a sender ID. List spans accessible orgs unless `--org-id`
sets a raw organization ID (not a slug/URN). Without pagination flags list
pages to exhaustion; either `--limit` or `--offset` requests one page.

The Resend API key is write-only. Never put it on the command line: use
`--api-key-file ./resend.key` or `--api-key-file -` with stdin. Create with no
file flag reads piped stdin or a masked terminal prompt. Update only rotates
when that flag is explicitly supplied; other omitted fields stay unchanged,
and explicit `--enabled=false` disables the sender. The server defaults create
to enabled. Text and JSON expose `hasApiKey`, never the key. A credential-write
failure preserves the mapped exit and serverCode, but withholds the backend
diagnostic to prevent a reflected key. Do not blindly repeat a write after
exit 7: check the sender first. Delete prompts on a terminal and requires
`--yes` non-interactively. Null/missing get exits 4. List JSON is an array
(empty: []); create/get/update return sender metadata objects; rm returns
{id,deleted}. The key is never selected in GraphQL.

`user invite` invokes the server's platform-invitation operation, with no
organization membership. Server authorization remains authoritative. No email
is sent: share the slug privately and accept with `hadron org invite accept
<slug>` while signed in. Email, GitHub username and name are descriptive, not
acceptance restrictions. Use `--expires-in-days` 1-10 (server default 10 days)
and `--max-activations` 2-1000 (server default 2 activations). Unset values
are omitted so server defaults apply; explicit values, including 0, are sent
unchanged. The server refuses out-of-range values with BAD_USER_INPUT (exit 2);
no invitation is created. `org invite create` uses the same ranges and server
defaults, with `--expires-days` for expiry and `--max-activations` for the cap. JSON includes the slug and
invitation metadata; treat the output as private.

`org member add|set-role|rm` forwards orgRef/userRef, replacing deprecated
orgId/userId arguments. Org ID/slug/URN and user ID/email/handle/URN are passed
verbatim for server resolution. Command spellings and output stay unchanged.

`org signing get|preview|on|off <org>` reads and sets an organization's
boot-prompt signing switch (hadron-server#2973). Only the org's current ADMIN
or OWNER, signed in as themselves, may call it: a worker session, an App key,
a run, impersonation (reads and previews), a non-member and an unknown org ALL
get the same FORBIDDEN (exit 8, one constant message); an impersonated `on`/`off`
is instead refused by the platform with HTTP 403 IMPERSONATION_READ_ONLY (exit 1,
the server's message, serverCode in the envelope) and nothing changes, so the CLI never looks the org up
first and never sends the worker-session header. `<org>` goes to the server as
typed and `--json` echoes it unchanged in `org`. `get --json`:
{org, required, enforced, deploymentSigns}; `enforced` is the switch AND the
deployment shipping signing, so required=true, enforced=false means this
deployment does not sign yet. `preview --json`: {org, enforced, deploymentSigns,
total, rows[], hasMore, endCursor}, each row {appId, appUrn, agentId, agentName,
workerId|null, workerName|null, compiledNodeId, compiledNodeUrn|null,
servedRevSeq, state SIGNATURE_PENDING|REVISION_CHANGES|UNSIGNED_STOPS,
changesToRevSeq|null, agentUrn|null, workerUrn|null}; agentUrn/workerUrn (cli#1949, server >= cac6c05c6, an older server refuses the preview request: exit 2, a sentence naming both fields and that commit and saying no signing change was sent, serverCode GRAPHQL_VALIDATION_FAILED, no retry or fallback) are the server's display URNs printed as served, never built here, and an absent one is an explicit null key (workerUrn is null on an install-level row); the text table adds AGENT URN and WORKER URN columns, empty when absent; the same rows embedded in `on`'s document carry both; rows is [] when empty; there is no previewToken. A plain
`preview` reads ONE server page (the server's default of 20) and, when hasMore,
says to use `--after <endCursor>` or `--all` (every page; not combinable with
--first/--after). `--first N` goes to the server unchanged (below 1 is the
server's BAD_USER_INPUT, exit 2; above 200 is served as 200); a non-integer is
exit 2 with no request. `on` always reads EVERY page and writes the full
preview to stderr (JSON under --json) BEFORE it confirms, then sends the FIRST
page's token; `--yes` skips only the prompt, and a non-interactive run without
it exits 2 (an empty preview still confirms). Its stdout is one document:
{org, required, enforced, deploymentSigns, preview:{total, rows}}. If the
preview changed or expired (10 minutes), BOOT_PROMPT_SIGNING_PREVIEW_STALE exits
5 with serverCode in the envelope and nothing changes: run `on` again. A preview
that moves while it is being read exits 1 with nothing printed. `off` needs no
token, confirms (`--yes` skips; non-interactive without it exits 2), sends no
previewToken, and prints {org, required, enforced, deploymentSigns}.
BOOT_PROMPT_SIGNING_NOT_CONFIGURED (the server has no token key) exits 1.
Older servers answer GRAPHQL_VALIDATION_FAILED (schema skew, exit 2).

### Team session inputs and host availability (#1337)

- `team session start --as <worker>` accepts `--agent`, `--customer`, `--family`, `--language`, `--parent-session`, `--previous-session`, `--plan`, `--pr-number`, and `--type` as explicit new-session inputs. Unset values are omitted. The server resolves references and authorizes the bind.
- `team session start` with no worker resumes: `--description` supplies a plan only if redemption starts a new session; `--family` also refreshes an open resumed session. Start-only flags are refused before redemption.
- `team session end` accepts `--input-tokens`, `--output-tokens`, `--turns`, `--errors`; explicit zero is preserved. These record counters, not permission. Ending remains an explicit session lifecycle action.
- `team session list --customer <id> --type <type>` filters server-side, including when `--active` scans all matching pages. These filters do not combine with artifact provenance flags.
- `team session work-items --worker <name|id|URN> --app <app> [--limit <n>] [--offset <n>]` reads one server page, JSON `{total, items}`. Retired names resolve server-side; a ref resolving to nothing readable gives an empty page, not an existence error. Default limit 50; zero counts. Refusals never retry without the worker filter.
- `team host run --config <file>` forwards the config's explicit `capabilities` list on each refresh; missing/empty declares none. This poller has no `worker.reset-context` handler: do not advertise it for this runner. A declaration adds no implementation; this runner warns on stderr before registering each unimplemented declaration.
- `team host availability <worker> --config <file> --reason OUT_OF_TOKENS|HOST_DOWN [--until <RFC3339>]` marks unavailable; `--clear` clears this host's own mark. Uses only that config's host key/App, no worker session or ambient token. Another setter's mark (including a stall) refuses `AVAILABILITY_NOT_PERMITTED`, exit 8. No automatic marking policy.

## Node, edge, asset and comment coverage (cli#1341)

`node set-properties` merges a JSON object into a node's properties server-side (updateNodeProperties; top-level keys overwrite, the rest stay; `exports` is merged as an RFC 7396 JSON Merge Patch, recursively, and a null there removes a host or field); `--json` is `{id, memoryId, loc, name, properties, updatedAt}`. `node add --edge` writes outgoing edges atomically with the node (`<name>=<target>` or a JSON object with `target`, `name`, `loc`, `description`, `runnable`; unknown keys exit 2); `--id` sets a client-chosen id. `node update --edge` REPLACES the whole outgoing set and needs `--replace-edges` (exit 2 otherwise, and with `--dry-run`). `node rm --cascade-cross-memory-edges` needs `--hard`. `node list` takes `--created-by`, `--role`, `--updated-after/--updated-before` (RFC 3339), `--include-deleted`, `--memory-class` (repeatable). `node export --section` is a presentation render (not round-trippable, md/json only). `edge get <id|urn>` reads one edge (missing and unreadable are both exit 4); **Attribution (cli#1513, server#3092):** `node get --json` (single, batch and `node next`) carries `createdByUser`; `edge get`, `edge list` with no node and `edge list <node>` (each row, both directions) carry `createdAt`, `updatedAt`, `createdByUser`, `updatedByUser`. Every key is always present. An author is the server's `{handle, urn}` (either half may be null) or `null`, passed through verbatim: the CLI never classifies the principal, never looks one up and never prints a raw id, and a null author means not shown to you or not known, never "nobody". `updatedAt` stays null when the server returns null. **Index truncation (cli#1481, server#887):** `node get --json` (single, batch and `node next`) always carries `indexTruncated`: `{omittedChunks}` when the node has more chunks than the search index holds, else `null` (fully indexed); text prints `index: truncated, N chunks not embedded` only for a truncated node. Text prints a `created by:` line (`node get`) or `CREATED`/`UPDATED`/`CREATED BY`/`UPDATED BY` rows (`edge get`) only for values that exist (`@handle`, else the URN; nothing printable prints nothing); list tables keep their columns. `edge create|update` output and `Node.updatedByUser` are unchanged and not shown. `edge list` with no node lists every readable edge (`--json` `{total, edges}`, each `{id, urn, loc, name, description, isRunnable, priority, sourceId, sourceLoc, targetId, targetLoc, createdAt, updatedAt, createdByUser, updatedByUser}`, a hidden endpoint as ""), paging to exhaustion unless `--limit/--offset`; `--source`/`--target` take a node id or node URN (a bare loc is exit 2); `--runnable=false` keeps only edges explicitly stored non-runnable (an edge with no runnable value matches neither `--runnable` nor `--runnable=false`); and a memory, node or org the server cannot resolve or you cannot read gives an empty list, not exit 4; the node-only filters need a node and the list filters need none (exit 2). `asset list --all` is the cross-memory list (`--json` `{total, assets}`); `--org`/`--scan-status` need `--all`; an unknown or unreadable `-m` contributes no assets (the other `-m` memories still list) and such an `--org` gives an empty list; neither is exit 4. `asset upload --agent-request <id>` sends upload intent AGENT_REQUEST with that id (the server keeps it with the upload ticket; it is not a retry key). `comment delete --thread --reason <text>` deletes a whole thread (memory admin, server-gated; `--json` `{thread, deleted}`; no `--expected-revision`). `search --granularity chunk` adds `passages` (`parentNodeId`, `parentNodeUrn`, `chunkIndex`, `charStart`, `charEnd`) to `--json`; the key is present (as `[]` when nothing matched) only on a chunk search.

# Task minimum model tier (cli#1479, server#2983)
`team task create|update --min-tier low|mid|high` sets the minimum model tier
in requirements. The server prefers the lowest sufficient tier; an unknown
tier is ineligible when a minimum is set (cor:agt:060:06 r21, cor:agt:060:04 r22).
Exact tokens only: empty or invalid values exit 2 before HTTP. Update replaces
the WHOLE requirements object, so repeat every requirement to keep.
`--clear-requirements` clears the minimum too and conflicts with `--min-tier`
(exit 2, no HTTP). No maximum-tier flag or client-side eligibility logic.
Get/list JSON includes requirements.minTier as a string or explicit null when
requirements exists. Human output shows a tier only when set. The shared task
fragment requires a server containing #2983 (2026-10-31); deploy it before
production use. Server BAD_USER_INPUT refusals retain their mapped exit 2 and
message. This CLI build does not claim a production deploy or production probe.

## Policy, quota, grant and secret coverage (cli#1343)

`policy explain --action <a> --app <ref> [--user <ref>]` explains the standing action-policy chain (org, then user only with `--user`, then app; cor:acl:040:02): org ADMIN on the App's org, else exit 8; unknown App or user exit 4. `--json` is `{allowed, layers}`, each layer `{layer, present, allowed, allow}`; `allow` is null when the layer has no link (no restriction) and `[]` when its link allows nothing, so never treat the two alike. Per-trigger links (schedule/webhook `--policy`) are not in this chain. `policy set --mine '<json>'` (or `@file`) writes the caller's own user layer, `{"allow":[...]}`; `--clear` sends an explicit null (no restriction); `--mine` is required, the JSON and `--clear` are mutually exclusive, and invalid JSON, or a JSON `null` (use `--clear`), exits 2 with nothing sent; the server checks the shape (exit 2). `--json` `{layer:"user", userId, handle, policy}`. `org update --policy '<json>'` sets the org layer; `--clear-policy` clears it (explicit null; mutually exclusive with `--policy`); a blank `--policy` (an unset variable) or `--policy null` exits 2 with nothing sent, never a silent clear; without either flag the link is untouched (omitted, never null). `quota list --org <ref> [--app <ref>]` shows the daily counters (`--json` array of `{id, scopeKind, scopeId, action, window, used, limit, resetAt}`; `[]` when empty; rows are created with platform defaults the first time a scope is shown). `quota set --org <ref> [--app <ref>] --action run.execute|llm.tokens|sms.send|email.send --limit <n>` sets the enforced limit (0 stops that action for the scope; for run.execute/llm.tokens every launch is refused; the server refuses other actions and out-of-range limits, exit 2); without `--app` the org-level limit (appRef omitted). Both org ADMIN (exit 8); an App outside the org exit 4. `grant get <id>` and `connection grant get <id>` read one grant (the same DTO as a list row); `secret get <id|urn>` reads one secret's metadata, never its value. All three answer exit 4 for both missing and not-visible, with one message (no existence oracle). `secret update <id|urn>` overwrites in place (no versions): `--value-file -|@file|path` replaces the value (`-` on a terminal prompts without echo; the value is never printed), `--meta`/`--meta-json` replace generic metadata, `--type` (with `--value-file`) builds a webfetch-auth value and `--url-prefix` replaces its metadata; an omitted half is preserved (not sent). Name, kind and owner are immutable (no flags: `--name`/`--kind` exit 2). Mixing generic and webfetch-auth flags, `--meta` with `--meta-json`, an empty `--meta-json` or `--value-file` argument, `--type` without `--value-file`, or no change at all exit 2 with nothing sent; a value or metadata that does not fit the secret's kind is refused by the server (exit 2).

## Cross-entity discovery and catalogue (cli#1345)

hadron find <query> [--entity-type organization,memory,node,agent,app,aiServiceConfig,user] [--field pk,urn,name,description,content] [--org <ref>] [--scope <id|name|app|global>] [--app <ref>] [--limit 1..200] [--offset N]
hadron marketplace agents|memories|orgs [--limit 0..200] [--offset N]

`find` is the cross-entity globalSearch surface; node-only vector-aware search
stays `search`. Query bytes pass through unchanged: the server sniffs PK (exact
id), URN, or name+URN; --field overrides the sniff, content is opt-in and skips
encrypted memories; --entity-type restricts kinds. No org, App or stored scope
is implicitly applied. --org accepts an id or fully qualified org URN and
narrows org-anchored slices, never the user slice;
missing/non-member orgs have the same empty answer. --scope narrows only memory
and node slices; a bare name or app needs explicit --app. The server decides
all visibility, matching, ranking and scope resolution. One request/page,
limit 20/offset 0 defaults, order unchanged. Total is the bounded candidate-list
count, not the full database count. JSON is {hits:[{id,entityType,name,urn,
description,score,matchedField,memoryId,organizationId}],total,degraded:[],scope};
nullable names/URNs remain null, and scope retains kind,label,source,ownerUrn,
memoryUrns,droppedCount. Degraded notes also go to stderr; no hit enrichment or
fallback query. A nullable URN is valid (AI config, user without legal handle).

Catalogue reads are public and work signed out (available stored credentials
are attached). Listing is advertisement, not access: unlisted public resources
remain usable by link but are absent from browse (cor:acl:080:04). The server
returns only opted-in, publicly accessible refs ordered (name,id); clients do
not infer eligibility. Exactly one requested resource-type query per page,
JSON {items:[{id,name,urn,description}],total}, empty items [], nullable description.
Default limit 20/offset 0; limit0 is count-only. Human output includes total.
No nested members, private data, credentials or follow-up resource fetches.
Both commands print a next-offset hint using rows actually served. Neither
walks every page automatically; null/malformed pages fail instead of claiming
an empty result. Refusals pass through MapError; no production grant is implied.

## Automation graph and trigger inputs (cli#1346)

`automation preview <SCHEDULE|WEBHOOK|MANUAL> <ref>` reads the raw graph and
server digest; `--json {trigger:{kind,ref},hash,graph}` preserves the server JSON; the
digest is graph.hash and repeated as hash. Human output labels it Hash;
DISPLAY INCOMPLETE means review the full --json graph before re-adopting.
Ref is a trigger row id, or the origin AppRun id for MANUAL, never a name.
No App lookup is made. Only the run-as user or manager of an App-only trigger
may preview/re-adopt; authorization is the server's. Review the graph before
`automation re-adopt <kind> <ref> --expected-hash <digest> --yes`: it sends that
exact digest once, never fetches a replacement and never retries. A graph edit
since preview is exit 5; false is exit 1. Confirmation is required (`--yes`
non-interactively). JSON is `{trigger,status:"re-adopted",hash}`. This updates
future consent, not an executed run's historical snapshot. Human preview is
sanitized/bounded; use `--json` for the full graph.

`automation approval <name> --limit N` sets the name-resolution page size;
all cursor pages are still read, preserving ambiguity checks. Positive N;
omitted uses the server default. The existing `--run` path needs no list.

`schedule preview` (cli#1678, server#3463) makes one preview query and no write:
no create, update, adoption, run or automatic follow-up. It accepts create's
App/entry/Agent/run-as/config/budget/args/timing flags, but not name, scope or
policy. Exactly one of --cron or --run-at; empty timing is usage 2 before traffic.
Unset optional fields are omitted (default enabled=true, timezone server UTC).
`preview does not validate policy or scope; create may refuse them even after an ADOPTABLE preview.`
This is a snapshot: no reservation and no authority to create; changed automation
may change the hash. Worker scheduling needs human authorization or the
applicable grant; the server decides admission. This does not settle worker
approval/manual-run policy or change schedule create.
JSON is `{outcome,hash,reason,summary}`. ADOPTABLE exits 0, with a 64-hex hash,
reason:null and summary `{name,schedule,reads,writes,cannotDo,tools,limits,flows}`;
each flow is `{name,urn,tools,writes}`. Lists remain arrays, including empty [].
CANNOT_BE_ADOPTED exits 5 with only reason MEMORY_UNREADABLE or OTHER in text,
null hash/summary in JSON, and no memory identity. Text shows every summary
section (cells sanitized/bounded); JSON preserves the values. The schedule line
is the server's string with raw cron/instant/timezone, no local timing rewrite.
Malformed/nil/unsupported-provider replies fail nonzero, never become empty
success and never fall back to a write. Refusals retain schedule create's
mapping (2 usage, 3 auth, 4 not found, 8 forbidden), transport 7; no retry.

`schedule create` requires exactly one of `--cron` or `--run-at <RFC3339>`;
`schedule update` accepts at most one. A run-at switches to one-shot and clears
cron; cron switches to recurring. `--scope <id|name>` on schedule create/update
and webhook create selects a readable scope resolved in the App context,
snapshotted by the server onto each new run. Omitted means App memories at
create and preserves at update; `schedule update --scope ""` restores App
memories. `schedule update --agent <id|urn>` selects an installed Agent.

`run list --statuses FAILED,CANCELLED` filters several statuses (not with the
existing single `--status`); `--parent <run-id>` filters children. `--sort`
is createdAt|startedAt|finishedAt|status and `--dir` asc|desc; omitted flags
use server defaults. Every list page uses the same filter/sort variables.

## Model groups (cli#1485 S1, cor:agt:060:12)

```
hadron agent model-group list <agent>
hadron agent model-group set <agent> <name> [--pattern <glob>]...
hadron agent model-group delete <agent> <name> [--yes]
hadron app model-group list <app-ref>
hadron app model-group set <app-ref> <name> [--pattern <glob>]... [--instruction <kind>:<ref>]
hadron app model-group clear <app-ref> <name> [--yes] [--instruction <kind>:<ref>]
```

A model group is a named list of model patterns (lowercase globs: `*` any run,
`?` one character) matched against a worker session's self-reported model. It
is a matching input only, never an authorization input. An Agent declares
groups; an App whose Team Agent it is may override one by name or add its own.
Groups resolve live.

- **Pass-through.** Names and patterns are sent exactly as given (no
  lowercasing, grammar or limit checks); the server normalises, validates and
  authorizes, and the output prints what it stored. App set/clear with
  --instruction may resolve the App first; no write is retried.
- **`set` replaces the whole list** (no merge). Repeat `--pattern` once per
  glob; a value is never split on commas. No `--pattern` sends `patterns: []`
  on both Agent and App set: a group that matches no one.
- **`agent model-group list`** is a management read of the Agent's own
  definition: `--json` is an array of `{name, patterns, updatedAt}` with no
  origin. `set` prints one such object.
- **`app model-group list`** is readable by any App participant: `--json` is
  an array of `{name, patterns, origin}`. `origin` is a CLI label for the
  server's `ModelGroupOrigin`: `AGENT` → `agent`, `APP_OVERRIDE` →
  `app-override`, `APP` → `app-only`. A value this CLI does not know is
  printed as the server sent it. `app model-group set` prints
  `{name, patterns, updatedAt}`.
- **`app model-group clear`** removes an App override (the Agent's group
  applies again) or an App-only group. It never deletes an Agent's group: for
  a name only the Agent declares, the server's `MODEL_GROUP_NOT_FOUND` is
  shown as it comes (exit 4). `--json` confirmation: `{app, name, cleared}`.
  `agent model-group delete` prints `{agent, name, deleted}`.
- Empty lists are `[]`, never `null`, for both rows and `patterns`.
- `delete` and `clear` prompt on a TTY and need `--yes` otherwise (exit 2,
  nothing sent).
- Refusals keep the usual mapping: `AGENT_NOT_FOUND` / `APP_NOT_FOUND` /
  `MODEL_GROUP_NOT_FOUND` exit 4; `FORBIDDEN` / `APP_MANAGER_REQUIRED` exit 8;
  `BAD_USER_INPUT` exit 2; `MODEL_GROUP_LIMIT_REACHED` (the Agent or App holds
  the most groups allowed) and `AGENT_OWNERSHIP_CHANGED` (the Agent's owner
  changed during the write) exit 5, a state conflict a same-call retry can't
  fix (cli#2428; they exited 1 before). The `--json` error envelope's
  `serverCode` and the message are the server's, unchanged.


## Task integrations: App properties and task externalId (cli#1524)

hadron-server#3125; plan `plans:1524-find-untracked`. Specs `cor:dmo:050:02`,
`cor:dmo:050:20`, `cor:agt:120:01`, `cor:agt:120:02`. A thin client: every
rule is the server's (`cor:api:240:01`), the CLI sends values as given and maps
refusals. Nothing here is on MCP.

```
hadron app properties get <app-ref>
hadron app properties set <app-ref> <json|@file|-> [--expected-revision <n>] [--instruction <kind>:<ref>]
hadron app properties clear <app-ref> [--expected-revision <n>] --yes
hadron team task create <title> --external-id <id> …
hadron team task list --external-id <id>
hadron team task find-untracked --integration <name> [--team <worker> | --no-team] [--ready only|exclude|any] [--repo <owner/name>] [--skip N] [--limit N] [--assign-to-me [--assignee <party>] [--owner <party>]]
hadron team task find-untracked --adapter <exe> [--for <worker>] [--track] [--assignee <party>] [--owner <party>] [--limit N] [--json]   # cli#1961 (cor:agt:120, cor:agt:120:04): the team's local INTAKE ADAPTER (an executable; --adapter, else $HADRON_INTAKE_ADAPTER; an explicit --integration is the server path above and cannot be combined with --adapter). Hadron makes no tracker call. Invocation: `<exe> <call>` with ONE argument (the call: describe, list-untracked, mark-tracked, untrack, report), never a shell; the request is one JSON object on stdin (always `contractVersion`), the answer one JSON object on stdout with exit 0, any other exit is a refusal; stdout is capped at 1 MiB, a call at 60 s, a page at 100 items; every HADRON_* environment variable (case-insensitive prefix) is removed before invoking the adapter. `describe` runs first, before the binding, the App or Hadron is read: another `contractVersion` exits 15 (nothing else is asked, Hadron untouched); its `calls` array lists every call the adapter answers, and a run needs `list-untracked` (and `mark-tracked` with --track) there, else exit 2 with nothing claimed (a list-only adapter still lists). describe answer {name, contractVersion, calls, filters, order}. `list-untracked` request {cursor?, for?, limit}, answer {items:[{externalId,title}], nextCursor?}: paged by the adapter's opaque cursor only (a repeated cursor, an endless list (50 pages) or a malformed/oversized answer exits 1; --skip and every other offset are refused, exit 2). Each item is checked with ONE batch read of the App's tasks by external id; an item is dropped only because a task holds it (Hadron is authoritative, the tracker's mark never decides). Without --track: lists up to --limit (1-100, default 20) unheld items. With --track: --limit (1-100, default 1) is how many tasks this call HOLDS; for each unheld item the CREATE is the claim (externalId and source = the adapter's URL exactly as written, no normalisation; title from the adapter, terminal-safe; nothing else from the tracker reaches Hadron); TASK_EXTERNAL_ID_TAKEN means another caller won: the item is listed under `lost`, never counts, is never looked up, adopted or marked, and the walk continues (exit stays 0). The winner then calls `mark-tracked` {externalId, task:{id,handle,portalUrl?}, worker:{id,name,role?,model?}}; portalUrl is the SERVER's `AgentTask.portalUrl`, omitted when null, never built. A claim's assignee: --assignee, else --for X, else the bound worker (none of these: exit 2). The server decides the caller's party and starts a caller-own claim atomically (server#4115); the CLI sends one create and reports its returned status, never a separate start. On an older server PENDING stays PENDING; the assignee starts it. Other-assignee claims stay PENDING. `startError` is retained as null for compatibility. Missing assignee names fall back to the server ID. A refused create stops the walk (what was claimed is printed first, then the refusal's exit code); a failed mark leaves the claim standing, is reported (`marked:false`, `markError`) and exits 1 after printing. --json list = {adapter, items:[{externalId,title}], complete, stoppedReason}; --track = {adapter, tasks:[{id,handle,title,status,externalId,portalUrl,assignee,marked,markError,startError}], lost:[], complete, stoppedReason}. `complete` means the list ran out without error; reaching --limit exits 0 with complete:false and stoppedReason:"limit_reached" (no extra page is fetched). Other stoppedReason values: check_failed, claim_failed, mark_failed, walk_failed, reconcile_failed; an error takes precedence over a limit stop, and exhaustion gives null. An explicitly empty --integration is exit 2. describe.filters/order are parsed metadata, currently unused. RECONCILE (cli#1962; cor:agt:120 D4, cor:agt:120:04), first in EVERY adapter run: it reads the server's `agentTaskExternalIdChanges` after the stored cursor (oldest first, 200 a page, at most 25 pages a run; the rest next run) and calls the adapter: REASSIGNED -> `mark-tracked` again {externalId, task:{id,handle,portalUrl?}, worker:{id,name} — no role or model, the new worker made no claim}; RELEASED -> `untrack` {externalId} with the RELEASED id the feed names (the task holds none); CANCELLED -> `report` {externalId, task, state:"CANCELLED"} only when describe.calls lists `report`; ASSIGNED, HANDED_BACK and FINISHED call nothing in v1. A call the adapter's describe does not list is skipped, not failed. T is the server's OPAQUE cursor, never a clock, kept in `intake-cursors.json` beside the config (0600) per server+App+adapter: the first run has none and reads from the oldest change, a second run starts after the last answered change, so nothing is told twice. One reconcile per feed at a time (cli#1975): a per-feed file lock (beside `intake-cursors.json`) covers the cursor read, the adapter's effects and the cursor write, so a ticker run and a hand run never overlap; a second run waits up to 2 minutes, then exits 5 (busy, nothing read or told). A page that does not advance the cursor (a non-empty page whose next cursor is the `after` just sent), or that says `hasMore` and serves nothing, is refused before any effect, persists nothing and ends the run exit 1 with `reconcile.error` set (the list walk's repeated-cursor rule). A REASSIGNED change whose task now has no assignee names no worker and is skipped. A refused adapter call stops the pass AT that change (the cursor stays before it, the next run retries it), the list and claim still run, and the run exits 1. Both outputs gain `reconcile: {read, marked, untracked, reported, skipped, complete, error}` (counts of changes; `error` null unless an adapter call failed; `complete` false when it stopped early); text mode puts one `note: reconcile: …` line on stderr when it did or said something. An older server without the feed: exit 2, the pass cannot run. Needs a server with hadron-server#4084 (an older one: exit 2, nothing claimed)
```

Adapter reconcile JSON (list and `--track`): `skipped` remains the total. Additive reason counters partition it: `skippedNull` (null feed entry, not counted in `read`), `skippedUndeclared` (adapter does not declare the call), `skippedKind` (no action for this change kind), `skippedNoAssignee` (reassignment without a task/assignee), and `skippedNoTask` (cancellation without a task), and `skippedHeldElsewhere` (a different ACTIVE task holds the item now, or for a release ANY task still holds its id, so the adapter is told nothing; a failed holder read, or a read that still lists a released task as its holder, ends the run with the cursor unmoved). Missing task/assignee takes precedence over an undeclared call. Every counter is present, including zero. A 25-page bound returns `reconcile.complete:false`, `reconcile.error:null` (exit 0); the next run resumes at the saved cursor.

Changed in cli#2250 (`team task find-untracked --adapter` JSON; first shipped
in the first release cut from main after v0.51.0; v0.51.0 does not have it): reaching `--limit` now
reports `complete:false` with `stoppedReason:"limit_reached"` where it reported
`complete:true` (still exit 0: a limit stop is a success, not a failure), and
the `--track` `stoppedReason` is `limit_reached`, or the failing stage
(`claim_failed`, `mark_failed`, …), where it was the literal `"stopped"`.
`limit_reached` comes only from `--limit`: the list walk's 50-page
endless-list bound is a refusal (exit 1), and the reconcile's 25-page bound
reports `reconcile.complete:false` with `reconcile.error:null` and exit 0 (the
top-level `complete` is the list walk's and is not changed by it), not a
`stoppedReason`. The list `--json`
gained `stoppedReason` (additive). A consumer matching
`"stopped"` or reading `complete:true` at a limit must switch to the stage
vocabulary and treat a limit stop as incomplete.

- **`app properties`** is the App's own JSON object; the platform reads only
  its `taskIntegrations` key. Anyone who may read the App reads it (null
  otherwise). The server checks App-manager authority. Without --instruction, writes
  use the ordinary authenticated caller; set also supports the held-worker
  instruction path below. clear has no instruction flag.
- **`get`** prints the object alone, indented. That text is valid JSON (or
  `null`), so `> props.json` round-trips; control and invisible characters
  inside strings are written as `\uXXXX` escapes (the same value). `--json`
  is `{appId, propertiesRevision, properties}`.
- **`set`** replaces the whole object in one `setAppProperties` call (no read
  first without --instruction, no merge, no retry): the value is a JSON object, `@file`, or `-` for
  stdin (an interactive terminal is refused). The CLI checks only that it is
  JSON and not `null` (exit 2 before any call); `{}` clears. It prints the
  stored object (`--json`: the same DTO).
- **`clear`** sends an explicit null. It prompts on a TTY and needs `--yes`
  otherwise (exit 2 before any call).
- **`--expected-revision <n>`** (set and clear) is sent only when given. A
  stale value is `APP_PROPERTIES_CONFLICT`, exit 5: re-read, then write.
- **Each changing write** is recorded as the team event
  `app.properties-changed`, naming the changed top-level keys, never values:
  `team events --type app.properties-changed`.
- **Refusals** (each with `serverCode` in the `--json` error envelope):

  | Code | Exit |
  |---|---|
  | `FORBIDDEN` | 8 |
  | `TASK_INTEGRATION_INVALID` (names the JSON path, never the value) | 2 |
  | `PROPERTIES_NOT_AN_OBJECT` | 2 |
  | `PROPERTIES_TOO_LARGE` (16 KiB, 64 levels) | 2 |
  | `PROPERTIES_KEY_RESERVED` | 2 |
  | `APP_PROPERTIES_CONFLICT` | 5 |
- **`task create --external-id <id>`** records the outside item the task
  claims (an issue's URL). At most one ACTIVE task (backlog, pending or started)
  per App holds a given id; finished and cancelled tasks keep it without
  blocking a new claim (cor:agt:120:02, cli#2264). It is set once and never
  edited, so there is no update flag.
  - A supplied blank is exit 2 before any call; every other check (length,
    characters) is the server's.
  - A duplicate is `TASK_EXTERNAL_ID_TAKEN`, exit 5, `serverCode` in the
    envelope, naming no task. The holder is the ACTIVE task:
    `task list --external-id <id> --status BACKLOG --status PENDING --status STARTED`
    finds it in one call, run against the same App as the refused create and
    with no other narrowing filter (an empty list then means no active task
    matched under these filters; any other filter, `--unresolved` among them,
    which lists finished tasks only, can hide the holder). Without the
    status filter the same list also returns the id's finished and cancelled
    tasks, newest first, so it can hold several (an exact match, refused with
    `--team`).
  - `task get` prints an `external id:` line, and every task `--json` carries
    `externalId` (null when none). Every task read selects it, so it needs a
    server with #3125 S2; no CLI release before that deployment.
- **`team task find-untracked`** (S3, `cor:agt:120:03`) is one call, never
  retried. It lists the open items of the App's task integration `--integration
  <name>` (the integration's NAME in `taskIntegrations`, not the provider) that
  no task of the App claims in any status. The server reads GitHub; the CLI
  never does.
  - **Flags.** `--team <worker>` names the team's lead (the worker its members
    report to) and is sent raw; a bare `--team` is exit 2. `--ready
    only|exclude|any` maps to `ReadyFilter`; another value is exit 2. Every
    omitted flag sends nothing, so the server's defaults apply. Bounds pass
    through: list `--limit` 1-100 (default 20), claim `--limit` 1-10 (default
    1); out of range is the server's `BAD_USER_INPUT`, exit 2. A missing or
    blank `--integration` is exit 2, and `--assign-to-me --skip` is exit 2,
    both before any call. There is no `--tag`: the claim takes none.
  - **`--no-team` (cli#1622, hadron-server#3422).** Keeps the items carrying
    none of the integration's team labels (compared without regard to case; an
    unrelated label does not count as a team, and with no teams mapped every
    item qualifies). It works for the list and for `--assign-to-me`, with or
    without `--assignee`/`--owner`: the server filters the list the claim walks,
    so a claim takes only no-team items. `--no-team` with `--team` (even an empty
    one) is exit 2 before any call; `--no-team=false` is the same as omitting
    it. It rides separate request documents (`…NoTeam`), so every call without
    it is byte for byte what it was and needs no new server. One request, never
    retried and never sent again without the filter. A server without the
    `noTeam` argument gets one plain statement, `this server does not support
    --no-team` (exit 1); any other validation failure is reported as before, and
    a server `BAD_USER_INPUT` keeps its code (exit 2). `--json` shapes do not
    change.
  - **List.** Paging is oldest first, then repository, then number. It is
    deterministic only while the tracker and the App's tasks are unchanged.
    `--json` is `{items:[{externalId, repository, number, title, url,
    labels:[], createdAt, updatedAt, linkedPullRequestMerged}], total,
    complete, incompleteReason}`. Text is a table with a CLOSE CHECK column
    reading `merged PR: close check` for a merged-linked item, and every
    tracker text is terminal-safe.
  - **Claim (`--assign-to-me`).** It creates STARTED root tasks owned by and
    assigned to the caller, in list order: `{tasks:[task], complete,
    incompleteReason, refusalCode}`. In a worktree bound for this server and
    App the claim carries the session, so the bound worker claims; with no
    binding the configured credential (a person) claims.
  - **Claim parties (cli#1596, hadron-server#3132 S4).** `--assignee <party>`
    and `--owner <party>` are valid ONLY with `--assign-to-me`; either one
    without it is exit 2 with zero requests (a list never becomes a write).
    Each changed flag takes `worker:<ref>` or `user:<ref>` (as on `task
    create`), sent raw and never resolved; an empty or kindless value is exit
    2 with zero requests, and an unset flag sends no key (never null). With a
    party the claim goes out as a separate document (`claimUntrackedTaskItems`
    with `assignee`/`owner`), so the no-party claim is the old document byte for
    byte; naming a party needs a server with #3132 and is never retried or
    downgraded without it. Attribution is unchanged. The server decides who
    may name parties: an App's own key must name both (`TASK_ASSIGNEE_REQUIRED`,
    `TASK_OWNER_REQUIRED`), each a worker of the App or a participating person
    (`TASK_ASSIGNEE_INVALID`, `TASK_OWNER_INVALID`); a person or worker claims
    for themselves and gets `BAD_USER_INPUT` for a named party; a key request
    carrying a bound worktree's session gets `APP_KEY_SESSION_BOUND`. All exit
    2 with `serverCode`; a stopped walk prints its claims first, as without
    parties.
  - **Incomplete results.** `claim_refused` prints the claimed tasks on stdout
    (one JSON document) and then exits with `refusalCode`'s mapping (below),
    with `serverCode` and a stderr message saying how many tasks were claimed
    and that they are committed. `claim_failed`, or `claim_refused` with a null
    code, exits 1 the same way. Any other `complete: false` (`rate_limited`,
    `github_error`, `page_limit`, `item_truncated`) exits 0 with a stderr
    note.
  - **Refusals** (thrown, nothing on stdout, `serverCode` in the envelope):

    | Code | Exit |
    |---|---|
    | `TASK_INTEGRATION_NOT_FOUND`, `APP_NOT_FOUND` | 4 |
    | `TASK_INTEGRATION_REPOSITORY_NOT_CONNECTED`, `TASK_INTEGRATION_TEAM_UNKNOWN`, `BAD_USER_INPUT`, `GITHUB_INVALID_ARGUMENT` | 2 |
    | `GITHUB_NOT_INSTALLED` (also an App with no organization), `GITHUB_TOKEN_SCOPE_MISMATCH` | 5 |
    | `GITHUB_PERMISSION_MISSING`, `GITHUB_REPOSITORY_NOT_ALLOWED`, `TASK_CLAIM_PRINCIPAL_UNSUPPORTED` | 8 |
    | `GITHUB_UNAVAILABLE` | 7 |

## nodeBatch selected-edge cap (cli#1210)

Edge-reading batch commands split an explicit ref chunk and retry the identical
query only for the structured BAD_USER_INPUT / limit=edges refusal. A 200-ref
chunk costs at most 399 requests, plus existing byte-cap omitted-ref requeues.
An indivisible ref or prefix exits 2 with its sanitized identity/cap and points
to `hadron edge list --source <node-ref>` and `hadron edge list --target <node-ref>`;
a prefix can instead be narrowed or read by explicit refs. No partial report or
export files are emitted on terminal pre-write refusal. Content-only skill,
coding and spec reads use an edge-free document. Other errors keep their mapping.

The exception is the re-lint AFTER a successful `spec replace`: the replacement
and exit status remain successful, with no mutation retry. After an applied replacement, JSON adds
`relintStatus: complete|incomplete` and `relintError` when incomplete. A failed or
unreadable re-lint discards partial findings, says incomplete rather than clean,
and keeps the completed replacement report. Ordinary re-lint errors use fixed
safe text; a terminal edge-cap refusal gives safe paged-read guidance.

# Task provider compatibility (cli#1485 S2, server#3029)
All task-record reads and mutation receipts require a provider containing
server#3029, EVEN WITHOUT either group flag. The shared task selection adds
modelGroupIn/modelGroupNotIn to get, list (plain/filtered/--team), mine, tree,
create/update, assign/reassign, start/progress/finish/cancel/park/ready and
priority/blocker edits. An older provider rejects these static documents as
schema skew (exit 2). Upgrade each provider first, including self-hosted and
staging; omitting input keys does not hide output fields. The separate
`task dependencies` paging query does not select these fields.

# Task model-group requirements (cli#1485 S2, server#3029)
`team task create|update --model-group <name,...>` requires the model to
match at least one named group; `--model-group-not <name,...>` requires it
to match none. Both lists may coexist, even with the same name. Names are
sent as supplied after comma splitting: the server alone normalizes and
validates names, caps and existence. Empty supplied lists (including comma-only)
exit 2 before HTTP. No local overlap check, lookup, matching or retry occurs.
Groups are live matching inputs, never authorization: editing a group can
change matches without a task edit.

On update, either flag replaces the WHOLE requirements together with role,
families, excluded workers and minimum tier: name EVERY requirement to keep.
A min-tier-only update drops prior group requirements. Each re-sent group
name is revalidated, so a deleted group can refuse the update. An unrelated
field edit leaves requirements omitted/unchanged; `--clear-requirements`
sends the deliberate null clear and conflicts with either group flag (exit 2,
no request). Absent group flags send no group key, including no accidental null.
The extra serverCode wrapper applies only when a group flag is named; ordinary
task writes retain their previous error-envelope shape.
Provider refusals `MODEL_GROUP_NOT_FOUND`, `BAD_USER_INPUT` and
`TEAM_AGENT_AMBIGUOUS` keep their message and serverCode through MapError.
`task get|list --json` requirements add `modelGroupIn` and `modelGroupNotIn`
arrays (absent/null lists become []; null requirements stay null). Human get
shows both lists when either is nonempty; plain list/mine add both group columns when
any row names groups. The specialized --team/tree human views keep their existing
projection; their JSON task records include the new arrays. Human names are terminal-safe, JSON names stay raw.
Requires deployed server#3029; no CLI release before that deployment.

## Questions (`hadron question`, cli#2465)

Question logs and questions (hadron-server #1641, slices S1-S4). This group replaces
`hadron decision` (cli#1335): the server renamed decisions to questions with no aliases, so
`hadron decision ...` (any arguments, any old flag) now prints `hadron decision was
renamed to hadron question (hadron-server#1641). Nothing was sent.`, exits 2 and sends
nothing (it reads no config and makes no request). Each verb is one server call; references
(ID or URN) go to the server as typed, and who may ask, amend, answer, retract, withdraw,
purge or change deciders is the server's rule, never re-checked by the CLI. The CLI refuses
only usage mistakes (exit 2) before any call.

- `ask` and `amend` read `--input-file` (`-` = piped stdin): one JSON object with the fields of
  the server's `AskQuestionInput` / `AmendQuestionInput`: `title`, `background`, `whyNow`,
  `priority` (`LOW|NORMAL|HIGH|URGENT`), `tier` (`ANSWERED|APPROVED|SIGNED`), `options`
  (`key,label,whyChoose,whyNot,risks`) or `yesno`, `recommendation`/`noRecommendation`,
  `deciders`, `assignee`, `neededBy`, `defaultAction`, `blocks`, `sources`, `specsTouched`,
  `specsMustChange` (+ `authorizes`, `grant` on ask). One question per node: no `parts`,
  `context`, `urgency`, `followUps`. Unknown, mis-cased and duplicate keys are refused (exit 2)
  before the call; values are judged by the server (`QUESTION_INVALID` names each bad field by
  JSON pointer). `--title`, `--background`, `--tier`, `--priority`, `--assignee` override the file.
  A field you leave out is unchanged (amend) or the server default (ask). Amend: `null` clears
  `background`, `whyNow`, `assignee`, `neededBy`, `defaultAction` (left out: unchanged);
  `blocks`, `sources`, `specsTouched`, `specsMustChange` are sent as `null` when left out
  ("unchanged") and an explicit `[]` clears; `"options": []` makes the question free text.
  A `null` on any other key changes nothing and is refused (exit 2, before the call) naming the key.
  Ask's `deciders` is `null` when left out (the log's defaults). `ask` requires `--input-file`;
  `amend` refuses a change set with no fields (a `[]` clear counts as a change).
- `answer` and `retract` are a decider's act: both send `via: CLI` themselves (not a flag) and
  require `--expected-revision`, the `revision` that `get` prints (one server call, no hidden
  read; a stale one is `NODE_WRITE_CONFLICT`, exit 5). `answer` needs `--option <key>` or a text
  (`--text` or `--text-file`, not both; neither is exit 2 before any call); a text beside an
  option is a note. Only a question's decider acting as themselves can: a worker session, an
  agent session and an App key are refused (`QUESTION_NOT_DECIDER`, exit 8): to answer from a
  worktree bound to a worker, use the portal or a shell whose session is bound to no worker.
  Text after an answer: `✓ answered <code>: <option key or (text)>` then the question;
  after a retract: `✓ answer taken back; <code> is pending again`; `--json` prints only the
  question.
  **Success is claimed only on evidence in the reply.** `answer`: the standing answer is an ANSWER
  entry (not a retraction) with the `--option` key you gave, on a revision that did not go back, and
  a NEWER revision when you gave a non-blank `--reason` (the same answer again without a reason is the
  server's no-op and may return unchanged). `retract`: no standing answer, a RETRACTION as the last
  `answers` entry, and a revision newer than `--expected-revision`. The text and the reason are NOT
  compared (the server normalises them): a difference prints a `note:` on stderr and never fails.
  A reply that does not show the outcome exits **1** with nothing on stdout (text mode) or the
  standard error envelope (`--json`), names `question get`, and is **not resent**. This is the one
  place exit 1 means "the server answered but the outcome is not shown", where `secret client
  revoke`, `agent subscription revoke` and `ha` use exit 7 for the doubt itself. Exit **7** ("answer|retraction
  not confirmed ... not resent; read the question with `hadron question get <ref>`") is the answer when the reply
  never arrived (a dropped connection), was unusable (a null question), was a 5xx or gateway page, 408 or 409 or any
  other 4xx, or showed the question or a coded error BELOW the mutation field (a resolver ran: the write may have
  been recorded). What the server refused before running the mutation keeps its ordinary exit: a coded refusal of the
  mutation itself (no path, or exactly the mutation field: 2, 4, 5, 8) and a plain HTTP 400, 401, 403, 404, 405,
  413, 415 or 429 (401 is 3, 404 is 4, the others 1 with the server's message); a connection that never opened is
  "could not reach the server" (7) with no "not confirmed" claim. In the unknown cases read the question with
  `question get` before trying again.
- `list` needs a scope: `--app` (every question in that App's Memories you can read),
  `--log` or `--awaiting-me` (exit 2, no request, when none is given, when `--awaiting-me=false` is the only one, or
  when `--app` and `--log` both are; `--awaiting-me` may combine with `--app` or `--log`). Every filter is sent only when its flag was given (`--answered` alone is `true`,
  `--answered=false` keeps the OPEN ones (no standing answer), `--authorizes=false` is sent as false); `--answered-since`
  takes an RFC 3339 time. One server page (`--limit`, server default 20; `--offset`); `total`
  counts every match. The table is `CODE STATE PRIORITY TIER ASSIGNEE REV TITLE`; "N of M" goes
  to stderr. `log list (--app | --memory)` finds the logs (`[]` when none).
- `log set-deciders`: without `--eligible` the other eligible deciders are kept (sent as
  `null`); `--eligible ""` clears them (sent as `[]`).
- `revision` in every JSON shape is the node's current revision: pass it as `--expected-revision`
  to `amend`, `answer`, `retract`, `withdraw` and `log set-deciders`.
- Refusal exits: `QUESTION_FORBIDDEN`, `QUESTION_NOT_DECIDER`, `QUESTION_NOT_ASKER`,
  `QUESTION_IMPERSONATION_REFUSED` exit 8; `QUESTION_INVALID`, `QUESTION_TIER_TOO_LOW`,
  `QUESTION_DECIDER_NOT_ELIGIBLE`, `QUESTION_RECOMMENDATION_MISSING`, `QUESTION_UNKNOWN_OPTION`,
  `QUESTION_ASSIGNEE_NOT_DECIDER`, `QUESTION_LOG_ENCRYPTED|MIRRORED|SYSTEM_MEMORY|ANONYMOUS_MEMORY`,
  `QUESTION_LOG_TOO_LARGE_FOR_FILTER` exit 2; `QUESTION_LOG_EXISTS`, `QUESTION_ANSWER_IMMUTABLE`,
  `QUESTION_WITHDRAWN`, `QUESTION_NOT_ANSWERED`, `QUESTION_ANSWERED`, `NODE_WRITE_CONFLICT` exit 5;
  `QUESTION_NOT_FOUND`, `QUESTION_LOG_NOT_FOUND` exit 4.
- `state` is derived by the server on every read. Rely on a question as a ruling only while it is
  `DECIDED`; `verify` returns the findings behind the state. Approval of a question node goes
  through `hadron node approve <question node> --revision <n>`.
- Deciders (`--decider`, `--eligible`, and `deciders` in the input file) are user IDs: the server
  accepts only a user who can write the log's Memory (`QUESTION_DECIDER_NOT_ELIGIBLE`). An empty
  `--input-file ""` is refused.
- `withdraw` (final, code stays used) and `purge` (platform admins; a never-answered question only: one left in the
  pre-hadron-server#4426 format counts only while its payload holds no answer or answer history, else the
  server refuses it) prompt on a terminal and need `--yes` otherwise; the purge prompt says so.
- JSON: a question is `{id,urn,code,n,logId,title,background,whyNow,state,tier,priority,answered,
  neededBy,defaultAction,authorizes,revision,portalUrl,viewerIsDecider,viewerMayAnswer,taskId,
  askedAt,answeredAt,askedBy,assigneeId,assignee,effectiveAssigneeId,effectiveAssignee,deciderIds,
  deciders,identityMerges,options[{key,label,whyChoose,whyNot,risks}],recommendation{key,reason,by},
  noRecommendationReason,answer,answers[{kind,key,text,reason,byUserId,by,at,via}],blocks,sources,
  specsTouched,specsMustChange,withdrawn,approval}` (`answer` is the standing answer or `null`;
  `answers` is every entry, oldest first, with `kind` `ANSWER` or `RETRACTION`; `viewerMayAnswer`
  is the server's word on whether you may submit a NEW answer right now: it is false once the answer
  history is full even though a slot is kept for retracting, and it does not say an answer stands to
  retract, so use `retract`'s own result (`QUESTION_NOT_ANSWERED`) for that); a log is
  `{id,urn,loc,name,prefix,lastCode,revision,portalUrl,memoryId,defaultDeciderIds,defaultDeciders,
  eligibleDeciderIds,eligibleDeciders,identityMerges,purges}`; `list` is `{total,items}`; `log list`
  is an array; `verify` is `{state,revision,approvedRevision,approvalIntegrity,approvalByDecider,
  codec,renderer,findings}`; `purge` is `{ref,purged}`. Lists are `[]`, absent values `null`.

## Retired boot-prompt flags (cli#1175)

`--system-prompt`, `--system-prompt-file`, `--clear-system-prompt` (on `agent create|update` and `app update`) and `--prompt-override`, `--clear-prompt-override` (on `team worker cast|update`) are removed and hidden from help. Passing one is a usage error (exit 2) that sends no request and names the new route: write the source node (role `boot-prompt`, `node update`), run the matching compile command (`agent compile`, `app agent compile`, `team worker compile`), then the Agent's/App's manager approves the compiled node with `node approve`. Nothing is sent and nothing is written; the flags never work again.

Task finish deadlines (cli#1514): create/update --finish-by <RFC 3339> sets
finishBy; update --clear-finish-by sends null to clear. Neither flag omits the
key (preserve); both together or a non-RFC-3339/empty value exit 2 before a call.
The server refuses past deadlines or finishBy earlier than startBy with
BAD_USER_INPUT (exit 2, server text); equal times are allowed and a refusal
changes nothing. Get/list JSON always carries finishBy (null when unset);
text detail shows a set deadline, and plain list adds FINISH BY when any row
has one. Shared task reads/receipts require provider #2648/#3088 even without
a finish flag. While set, ordinary stall windows do not apply; stall.finish-by
is raised for an unfinished task after the deadline (not in backlog; a blocked
task waits for release). Clearing restores ordinary windows from that edit.
The server owns all alarm timing and behavior; use team events --alarms to read
stall.not-started, stall.no-progress, stall.owner-unreactive, assignment.unassignable
and stall.finish-by, and team alarm ack to acknowledge.

## Task refs and --app (cli#1831)

Every `team task` argument that names a task (the positional task of every
verb, the second argument of `add-blocker`/`remove-blocker`, and
`create --parent`/`--blocked-by`, `clone --parent`, `list --parent`) goes
through one rule. With an explicit `--app`, a BARE handle (`^AT\d+(\.\d+)*$`,
after trimming) is sent as `hrn:agenttask:<root>:<slug>:<handle>`. `--app` must
then be `hrn:app:<root>:<slug>` or `<root>:<slug>`: an App id cannot be
composed without a read and is refused (exit 2, nothing sent). A qualified ref
(`hrn:agenttask:<root>:<slug>:…` or the server's
`hrn:agenttask:hrn:app:<root>:<slug>:…`) naming another App than `--app` is
refused (exit 2, nothing sent); naming the same App it is sent unchanged. A
task id or any other non-handle is sent unchanged. Without `--app` nothing
changes: the configured App context and the worktree binding never qualify a
handle, so a member of more than one App still gets the server's
`TASK_HANDLE_QUALIFY` for a bare handle. `task mine` keeps its own --app check; a pure permission refusal on its App read exits 2 as a missing App (cli#1896, #2239: see `--as-worker` above).

## Task single-shot matching (cli#1549)

`team task create --match` asks the server to run one blocking match for that
new PENDING task. Explicit false or omission leaves the old create input
unchanged. It cannot combine with an explicit assignee, backlog or an actually blocked task:
the server refuses BAD_USER_INPUT (exit 2) and creates nothing. A successful
match prints the chosen worker and exits 0. A failed match still CREATED the
unassigned task; stdout contains the normal task receipt plus reason/time,
and only then exits **5**. A writer failure takes precedence. Never retry
creation after this receipt: use its task id. No automatic retry is performed.
The automatic matcher skips a marked task; assignment, cancellation, or park
then ready clears the mark. Ready resumes regular matching, not match-now. Reasons currently include NO_ROLE, REQUIREMENTS_EXCLUDED,
NOBODY_FREE, STOP_SIGNAL and TEAM_POOL_EMPTY; unknown future reason strings
are retained. JSON is the existing bare task object, with optional
`matchFailure:{reason,failedAt}` only when present, never an error envelope
for a created task. Transport failure can leave creation uncertain; inspect
existing tasks before retrying.

Skip reasons (cli#1848, hadron-server#3816 V21–V23): the `--match` create
answer also carries `matchSkipped:[{workerId,name,reasons:[...]}]`, every
candidate the failed match skipped with every reason in the server's order
(RETIRED, USES_NO_TASKS, ENDING_SESSION, HOLDS_TASK, UNAVAILABLE,
FAMILY_UNAVAILABLE, STALLED, NO_FAMILY, NOT_HELD, FAMILY_EXCLUDED,
NAME_EXCLUDED, TIER_BELOW_MINIMUM, TIER_UNKNOWN, MODEL_GROUP_MISMATCH,
STOP_SIGNAL; the set is additive, so an unknown value is kept and printed as
itself). On the `--match` answer it is always an array, `[]` when nothing was
skipped or the match assigned; it is absent from a create without `--match`
and from every read (`get`, `list`, `tree`): the server returns it once and
never stores it. Text adds one `  skipped: <name> (<workerId>): <reason>, ...`
line per candidate under the match-failed line, or `  skipped: no candidates`.
Needs a provider at hadron-server be731afd (0.0.1879) or later: every create
selects the field.

Create `--require-role qa,cli --require-role server` accepts any listed role.
One role sends `requirements.role`; several send `requirements.roles`, never
both selector keys. The server checks 1–32 nonblank roles of at most 128
characters and matches case-insensitively. Empty supplied items exit 2 locally.
Create `--team <lead worker id, URN or name>` sends requirements.team: the
server restricts the pool to that same-App active lead, its reporting-chain
descendants and workers reporting to nobody; other requirements AND with it.
Unresolved, retired and foreign-App leads are BAD_USER_INPUT (exit 2).
This is distinct from the existing coordinator-view `task list --team`.
Read JSON adds requirements.roles/teamWorkerId only when present; old
unmarked/no-new-requirement output bytes remain unchanged, arrays stay arrays.

Plain `task list --match-failed` selects marked tasks before paging;
`--match-failed=false` selects unmarked, omission preserves the existing input.
Empty results are [] with exit 0. Combining either named form with list
--team exits 2 before any read. Human detail/list display reason/time with
terminal controls removed; JSON retains server values verbatim.
All shared task records now select these fields: requires deployed
server#3245 (schema pinned0356d7f12e or compatible successor), even with no
new flags. **No CLI release before that compatible provider deployment.**

### Read the action register

`hadron team register [--agent <id-or-URN>] [--task-loc <loc>] [--code <code>] [--limit <n>] [--offset <n>] [--json]` reads one page of the action register using `--app`, configured App or the worktree binding. It never writes or runs the mapped task. An unset `--agent` is omitted: the server uses the authenticated worker/run Agent, or refuses a person/App key with BAD_USER_INPUT (exit 2). A complete binding for the selected App on the same server supplies session attribution on the register data read; App aliases are checked through the existing App-identity read and the binding is rechecked after that lookup. A missing, other-App or other-server binding supplies none. An explicitly unusable Agent is AGENT_NOT_FOUND (exit 4); unreadable/unknown Apps are APP_NOT_FOUND (exit 4). No client pre-resolution or fallback. Requires server#3312 or later.

JSON is `{entries:[{code,state,actionUrn,entryUrn,definedBy,taskLoc,taskUrn}],total,hasMore}`; nullable values stay null and an empty entries list is `[]`. Server order and text paging summary are retained. UNRESOLVED says no runnable task found at its loc; UNMAPPED says the entry is malformed, with the code visible. An empty page gives no cause. Unset paging flags use server defaults; `--limit 0` counts, values above 200 are sent unchanged and capped by the server. Safe human cells replace terminal controls and bound long values; JSON preserves originals.

### Task finding dispositions (server#3258; cli#1556)

```sh
hadron team task finding-dispositions <task> [--limit N] [--offset N]
hadron team task dispose-finding <task> <finding-id> --disposition fixed|accepted|deferred|rejected [--ref <kind>=<target>[|<label>]]
hadron team task list --unresolved[=false]
```

Dispositions are beside a finished task's immutable result, never edits to it.
All four codes (including deferred) resolve a finding; the latest value wins,
with no clear operation. Omitted `--ref` means the NEW disposition has no
reference, even if the previous one did. Reference kinds are issue,
pull-request, node, task and url; stored target/label text is never fetched.
The server decides authority: task assigners only after finish, never an
assignee alone, an App key or impersonation. No local inference or retry.
`TASK_NOT_OWNER` exits 8, `TASK_NOT_FINISHED` exits 5,
`TASK_FINDING_NOT_FOUND` exits 4; server codes remain in the JSON error envelope
and human message. A transport or unreadable-response failure may follow a
committed write: read dispositions before deciding whether to write again.

### Link a post-merge defect to its PR (server#4280; cli#2189)

```sh
hadron team task link-defect <pr-build-task> --defect <issue URL> [--introduced-at-head <sha>] [--instruction <kind>:<ref>]
```

Records that a defect found after a PR merged traces back to that PR's build
task: one `review.defect-linked` event on the task (server #4280/#4284).
It sends exactly `prTaskRef`, `defectRef` and, when given, `introducedAtHead`
(a full 40-hex head frozen on that PR; omitted when unset), once, with the bound session as the actor.
With `--instruction <kind>:<ref>` (server 2026-11-16 or later), the holder's claim rides only this mutation; kind is team-chat-message, task or live-chat.
Malformed claims exit 2 before any request. The verified same-server session
attaches; an unavailable or changed binding sends as the person with the claim
checked but not recorded. A refusal is never retried on either path: no retry
after a plain refusal, and no plain retry after an instructed one.
Without the flag, requests and JSON remain unchanged. A link never changes a
verdict, a seat, readiness or the task. Linking the same pair again writes
nothing and exits 0 with the existing link (`already linked`, `created: false`).
Who may link is the server's: a bound `qa-engineer` or `coordinator` worker, or
a person who manages the App; or an instructed worker whose holder manages it;
never the App key or a run.
`DEFECT_LINK_FORBIDDEN` exits 8; `TASK_NOT_FOUND` (unknown, another App's or
unreadable, all answered the same) exits 4; `PR_REFERENCE_MISSING` (not a PR
build task) and `DEFECT_HEAD_NOT_FROZEN` exit 5; `BAD_USER_INPUT` (a defect URL
or head in the wrong form) exits 2, and an empty `--defect` or
`--introduced-at-head` is refused with exit 2 before any request. A response
with no link exits 1: the link may have been recorded, so read the events of
the task the message names (`hadron team events --task <ref>`) before
retrying. The message's last line is that task ref alone, in full (control
characters in the ref show as spaces, so reuse the ref you passed in when you
still hold it), so copy it as one argument, and quote the ref when you pass it
back to a POSIX shell: a ref can hold shell metacharacters. Single-quote it,
writing each embedded `'` as `'\''`; double quotes still expand `$(...)` and
backticks. No retry. `--json` is `{taskId, defect, introducedAtHead, eventId,
linkedAt, created}`; `introducedAtHead` is null when the link names no head.

`finding-dispositions --json` is `{items:[{findingId,disposition,reference}],
total,hasMore}` in finding order; empty items is `[]`. Default page 20, maximum
200, `--limit 0` counts only. Follow hasMore with offset, never infer the end
from page length. A write prints a bare server-returned receipt
`{taskId,findingId,disposition,reference}`. Reference is null when absent, else
`{kind,target,label}` (label nullable). Human text is bounded/terminal-safe;
JSON preserves original text. These commands do not read immutable findings
(the separate cli#1550 slice).

`list --unresolved` filters before paging to FINISHED tasks with at least one
undisposed finding; `=false` and omission do not narrow. Both explicit values
conflict with `list --team`, exit 2 before a read. Other filters AND together.
Omitted flag retains existing list documents and bare-array JSON. New commands
and the named unresolved flag require a provider containing server#3258; they
add no new selections to existing task commands. Task ids/handles work as on
get; an ambiguous handle needs its fully qualified App form.

## Instructed credential acts (cli#1595)

`--instruction <kind>:<ref>` is available on `app key list` (including --scopes
and --scopes --host-uses N), `app key revoke`, `app key scope revoke`,
`app memory-grant list` and `app memory-grant rm`. Needs server 2026-11-03 or
later. Kinds: team-chat-message, task, live-chat; sent as TEAM_CHAT_MESSAGE,
TASK, LIVE_CHAT. Split on the first colon, ref bytes unchanged; blank/unknown
kinds or blank refs exit 2 before any request or confirmation. A ref names the
instruction's location, never its text. The holder answers for the claim;
the server checks reference readability/App and holder authority, never that
an instruction happened. Host-launched sessions are refused whatever they
claim (FORBIDDEN, exit 8, no event), with no grant arm at these doors.

With the flag a complete, unchanged same-server worktree binding sends its
worker session explicitly. Unbound/corrupt/incomplete/foreign-server bindings
refuse locally (exit 2), never falling back to the person. Each request's
server gate decides the target App and whether the holder manages it; ambient
--app cannot lend authority. Without the flag, the ordinary document omits
instruction and the call remains headerless, acting as you even from a bound worktree (Xanthos
acceptance-2 ruling #71555). Separate instructed documents carry the claim;
a refusal is never retried/downgraded and preserves mapped exit and --json serverCode.

An admitted instructed list is not record-free: one app.credential-act event
per call/page, no dedupe. A changing instructed revoke records one; a no-op
revoke records none. Events name worker, holder, App, act kind and instruction,
never a key/scope/grant id. Minting, rotating and granting remain person-only:
app key create/rotate, app key scope grant and memory-grant add get no flag.
All three key list queries use TOP-LEVEL appKeysPage, never nested App.keys (which
still refuses a worker). Scopes and hostUses use holder management at server
2026-11-03; they are selected unchanged and a nested refusal fails the command,
never rendered as empty. No raw key is selected. Output DTOs stay unchanged.

## Instructed staffing and member acts (cli#1594)

`--instruction <kind>:<ref>` is available on `team worker cast` (and its
`--dry-run`, `castWorkerPreview`), `team worker retire`, `team worker rm`
(alias `delete`), `app member add` and `app member remove` (hadron-server#3308,
rulings 0096/0097, `cor:dmo:050:12`, `cor:agt:080:24`). Needs server 2026-11-03
or later. Kinds and parsing as above: team-chat-message, task, live-chat (sent
as TEAM_CHAT_MESSAGE, TASK, LIVE_CHAT), split on the first colon, ref bytes
unchanged; a bad kind or blank ref exits 2 before any prompt or request.

Without the flag these five are the signed-in person's writes, headerless even
from a bound worktree, with their documents and variables unchanged. With it, a
separate instructed document goes out, never retried or downgraded. Its
attribution follows `worker change-agent`'s row: the binding is snapshotted
before the command's first lookup, and the write carries its worker session
only when that binding is complete, for this server and for the TARGET App,
and unchanged at send time (one that appears meanwhile is a change too). The
target App is the resolved worker's own for retire/rm (an ambient `--app` is
not the target); for cast and the member doors it is `--app` or the active
App, resolved headerlessly (`app(ref:)`) when it is not the binding's own App
id. Otherwise the write goes out headerless as the person with a stderr note
(`note: this worktree's worker session is not used (...)`): the server checks
the person's reference but records no claim. No lookup ever carries the
session. A failed target lookup exits with its own code and sends nothing.

Server rule (worker session): the instruction arm or the worker's own live
grant, `workers.staff` (cast, preview, retire, delete) or `app.members` (member
add/remove, self-leave included); no grant is held by default, and the holder
still needs the right to make the change themselves. An admitted claim is
recorded, also for a preview and for a no-op (an already-retired worker, an
existing or absent member): an instructed `cast --dry-run` is NOT write-free,
and its text receipt says when it went out through the worker session (the
server records a claim it admits, never a host-launched session's). A target
App the caller can't read (`app(ref:)` is org-member-only) is noted as such,
and the write goes out as the person. A host-launched session's claim is
ignored (neither checked nor recorded): it acts on its own grant only, refused
without one. The MCP tools take no instruction. Refusals keep their codes:
FORBIDDEN exit 8, with `serverCode` in `--json` errors, which these five doors
now carry on the no-flag path too.


AUTOMATION READ GRANTS, --instruction (cli#2446, server 7907d93d or later):
`automation grant list` and `automation grant revoke` accept
--instruction <kind>:<ref> (the shared parser of the team-snapshot-grant
commands); `grant set` stays person-only. Without the flag the requests are
byte-identical to before and no worker session header is sent. With it, the
one call carries the instruction and this worktree's verified session. `list
--instruction` REQUIRES --app (usage error, exit 2, nothing sent): an instructed
list is scoped to the worker's own App, memory-only lists included, and each
admitted instructed list records one ids-only team event. A bad
<kind>:<ref> sends nothing. A worktree that cannot carry the call (no binding,
or one for another server) does not block it, as for the snapshot commands: it
goes out once as the signed-in person, with a stderr note when a binding did
not fit, and the server judges it. Revoke is one mutation, never retried; a
server refusal is shown once (FORBIDDEN exit 8, `serverCode` in `--json`).

AUTOMATION TEAM SNAPSHOT GRANTS (cli#1578, server#3363 ecdde802):
team-snapshot-grant set/list take an explicit App ID, URN or root:slug.
The group stands alone while automation read-grant (cli#1566) is not built.
Only a person managing the App may give a schedule/webhook read-only own-App
roster/task access; no default grant, no manual-run grant and no writes.
set sends appRef, triggerKind (SCHEDULE|WEBHOOK), triggerId; an identical repeat
returns the live row. It sends no worker session header even in a bound worktree.
list --trigger-kind/--trigger-id (cli#2775) keep only that trigger's grants: each is
sent as given only when set (the server validates triggerKind; the CLI pre-checks
nothing), on every page and with or without --instruction; without them the
request is unchanged.
list/revoke accept --instruction <kind>:<ref> (server7907d93d or later); set
stays person-only with no instruction flag. Shared parser accepts team-chat-message,
task or live-chat and preserves everything after the first colon. Bad instructions
exit2 before confirmation or requests. With the flag, the binding is snapshotted
before confirmation/client setup and rechecked before each page and the revoke;
a verified session is attached, otherwise the call is the person's with a note.
The SERVER owns App admission; no extra identity lookup or local App match.
A server refusal is returned once, never retried or downgraded to person.
A person fallback keeps the reference checked but records no worker claim.
Every admitted worker-instructed list page records ONE team event, empty page included:
default list-to-end records one per page; --limit bounds list to one page.
An instructed revoke pre-confirms the grant ID, target App and that page-event
consequence BEFORE any request. --yes skips it. After consent it walks the App's
entire history (one event PER PAGE), then sends one revoke if the live row is
found, without a second prompt. Missing row => no revoke; ended row => no repeat.
The revoke history walk has no --limit flag; do not assume it records one event
in total. The by-id read (#3381) has no worker arm, so the instructed revoke
stays on this history walk.
Without --instruction (a person), a non-TTY revoke without --yes exits 2 before
client setup or any lookup, even if the row would be ended or missing. Otherwise
revoke reads that ONE grant by ID with `automationTeamSnapshotGrant(id:)` (cli#1619):
no App history is paged, and no App is needed (the active App is not consulted).
Without `--app`, the confirmation names the App the server returned for the
grant. With an explicit `--app` (a malformed ref still exits 2 before any
request), the grant must belong to that App, else it is not found before any
confirmation and nothing is sent: an App given as an ID is compared directly; a
URN or root:slug costs one `app(ref:)` read to learn its ID. A TTY without --yes
confirms the grant's App ID + trigger kind/ID once. An unknown id, or one in
an App the caller may not manage, is the same server NOT_FOUND and the same CLI
error: uniformly not found (exit4) and no mutation; an ended row is shown without
another revoke. set/revoke require --yes without a terminal. Revocation is terminal.
No write is retried; a lost response is unknown outcome, so list with
--include-revoked before retrying. FORBIDDEN is exit8/serverCode FORBIDDEN.
list reads every page of20 unless --limit is supplied (1..200, exactly one page);
--offset starts at0, must be nonnegative. --include-revoked=false is sent as false.
Pages retain server order; concurrent changes mean this is not an atomic snapshot.
A page failure/nonprogress stops without partial output; continuation notes use stderr.
Set/revoke JSON: {id,appId,triggerKind,triggerId,grantedBy,createdAt,revokedAt,revokedBy}.
revokedAt/revokedBy are explicit null when unrevoked. List JSON: {items,total,hasMore};
items is [] when empty, total is the server count, hasMore is the last page value.
Human output names the App/trigger/grant; server-derived cells are terminal-safe.

# App-defined action receipt (cli#1669)
hadron app action record <code> [--task <task>] [--payload <json> | --payload-file <path>] [--json]
# Uses --app/active App; code is app.<current-slug>.<name> (regex + URN atom shape, <=64 bytes). Server checks slug/authority; after rename old codes refuse (interim reading). Payload must be a JSON object <=4KiB; every App participant and its key can read it, so never secrets. Omitted task/payload remain absent. One mutation/no retry; unknown outcome: inspect events before recording again. JSON {eventId,action:{code,state,taskLoc,taskUrn}}; register states MAPPED/UNMAPPED/UNRESOLVED/WITHHELD and nullable loc/URN are server answers, not authorization.
# team events --code-prefix <prefix> combines with existing filters; app. includes platform app.properties-changed/app.credential-act/app.manager-act, only app.<slug>. is App-only. Omitting the flag keeps the original query/variables.

## PR protocol commands (cli#1664)

`hadron team pr` consumes server#3448 (schema refreshed at06f4b59c).
Task references accept the same id/handle/URN forms as `team task get`.
The five writes return `{task:{id,handle,status},action:{code,state,taskLoc,taskUrn}}`.
The action is the server's register guidance: MAPPED, UNMAPPED, UNRESOLVED or
WITHHELD, with nullable loc/URN preserved. It is never authorization. The
server alone enforces owner-chain/assignee gates and negative-verdict rules.
Verified bound writes act as the worker, otherwise as the person; readiness
also admits the App's own key. A verdict records a fact, never finishes a task.

`readiness --head` returns `{ready,head,slots:[{slot,currentTaskId,state,verdict,head,counts}]}`
with an empty slot list as `[]` and absent seat/verdict/head as null. Text lists
every slot's facts and whether it counts, followed by no client-derived gate.
READY/NOT READY is the server's boolean (both query outcomes exit0). No head
inference, opaque-property mirror read, GitHub CI check or merge authorization.
Missing/short/nonhex heads, malformed colon-atom slots (max64 ASCII characters),
duplicate/empty schedules and invalid verdicts exit2 before HTTP. Optional
supersedes/node/receipt values are omitted when absent, blank values refuse.

Each command sends one protocol request, no retry/automatic fallback; existing
verified-attribution diagnostics may still run. Inspect task/events before
repeating a write with an unknown outcome. Protocol conflicts exit5, retain the
provider message and root JSON `serverCode`; PR_NOT_READY additionally exposes
`error.slots` with the same typed slot fields as readiness. Text names the code
and failing slots with terminal-safe cells; JSON retains raw strings. Common
bad-input/auth/not-found/forbidden refusals retain existing exits2/3/4/8. Null
readiness is exit4 (not found or not readable). Missing write receipts are exit7
with an inspect-before-repeating hint. The event codes are review.scheduled,
pr.frozen, review.seat.assigned, review.verdict and pr.ready; read with team events
--type. No team-process rewiring, model-family enforcement or automatic merge.

Node type union (cli#1528): `node list --types task,data --types Task` forwards exact values, order and duplicates; no trim/case-fold/dedupe. An explicit empty list, comment, both --type/--types or over 20 entries are sent for server BAD_USER_INPUT (exit2); unset omits nodeTypes for older-server compatibility. No schema-skew retry. Output unchanged.

## Complete key inventories (cli#1547)

`auth token list` (including --include-hidden) uses myUserApiKeysPage;
`app key list` (including --scopes/--host-uses and --instruction) uses
appKeysPage. Each reads pages of 20, advances by rows served, and follows
hasMore to false, never total or short-page length as a stop condition. Total
is selected only to detect a count change between pages; that or any repeated ID fails without
a partial inventory. Neither check establishes a snapshot: equal-count changes
without a repeated ID can escape detection. List after changes settle.
Existing --json arrays and row fields stay unchanged; no page envelope is printed. A later-page
error fails without printing a partial inventory. Requires server 2026-11-02
(hadron-server#3189) or later; no retry with an unpaged field. Instructed App
key listing keeps its session and instruction on every page and records one
app.credential-act event per admitted page. Offset paging is not a snapshot
under concurrent changes. Subscription-list commands remain unimplemented
(cli#890); this migration adds none.

App read flag edges (cli#1642): nonblank `app list --agent` values are forwarded verbatim, not trimmed; supply refs without surrounding whitespace. Empty/whitespace-only Agent refs refuse locally (exit2, zero requests). `app nodes --prefix=`, `--type=`, `--object-type=` forward empty strings (no narrowing on their own); `--type` still conflicts with `--types`. Repeated `--tag` requires all supplied tags; `--tag=` forwards a literal empty tag, not omission. Values remain untrimmed. Empty identity/date selectors (`--created-by`, `--role`, `--updated-after`, `--updated-before`) refuse locally. Absent filters and explicit false booleans keep their existing wire semantics. Agent-filtered App listing drains all pages, requesting at most20 per call under cor:api:120:04, not an Agent-specific server cap.

App node type unions: `app nodes --types` matches `node list --types`: repeatable/comma-separated CSV, parsed case/spaces/order/duplicates preserved, unset omitted, explicit empty sends []. The server judges empty/comment/--type conflicts/over-20 lists (BAD_USER_INPUT, exit 2); no local type policy. Other App node filter gaps are tracked in cli#1697.

### Pull a memory from its repository

`hadron memory sync pull <memory> --yes [--json]` mirrors the configured
repository into the memory. The repository wins over Hadron edits; nodes the
repository dropped are deleted. There is no revision check. Encrypted memories
are refused by the server. Without `--yes`, a terminal asks for confirmation
and a script exits 2 before any request. Only pull is offered.

After confirmation, one narrow read checks source presence and encryption;
neither value is printed or persisted. A readable non-encrypted row with no
source exits 2 without a mutation. A null read gives no local verdict and
delegates to SyncMemory; the server supplies success or refusal. Encrypted always reaches SyncMemory, even with no source,
so the server's writer gate and typed refusal remain authoritative. Otherwise
one SyncMemory follows. Configuration can change between read and mutation;
this is not a concurrency-proof execution receipt. Requests are never retried.

JSON is `{memory,operation:"pull",clean,syncStatus,syncError,lastSyncedAt,
syncReportStatus,syncReport}`. A non-OK status exits 1 with not-completed wording
and clean:false, and does not interpret historical reports as this invocation's
outcome. For OK status, a partial, unknown or unavailable report exits 0 with
clean:false and a stderr warning; inspect the report, not the exit code, to
decide whether the apply was clean. A stored failed outcome exits 1. URL
credentials in sync errors are masked. Exit 7 after sending the mutation means
no usable answer: the sync may have happened. Check `hadron memory get <memory>`
before retrying. A failed preflight instead says no sync mutation was sent.

### Worker terminal display label

`hadron team worker terminal-title <name-or-id> [--app <app>] [--json]`
reads one worker and prints its bare scrubbed `<Name>` plus newline (cli#2017;
it was `<Name> [<role>, <model>, <leader>]` under cli#1738). A scrub-empty worker
name becomes `worker unavailable`. Role, model, family and leader stay in the
`--json` `parts`/`worker`/`model_source` fields, raw and unchanged, but are no
longer in `title`. Same reference and retired-worker handling as `worker get`.
This is advisory display, never verified identity or authorization: nothing may
identify a worker by its tab title. Text replaces controls, bidi/format
characters, line/paragraph separators and brackets/commas with spaces, collapses
whitespace, and caps the title at 256 Unicode runes, the last being an ellipsis
when it was cut. The command itself takes no terminal action; the health sweep
applies the title (see "Team health sweep").

JSON keys: `title`, `worker{id,name,urn}`,
`parts{name,role,model,family,reports_to:{id,name,urn}|null}`, `model_source`
(`reported-by-session`, `family-fallback`, `none`), `teamPlacement` and
`reportsToRetired`. Parts preserve raw returned strings/nulls; only title is
sanitized. A masked/null value proves neither unbound nor withheld reporting.
Refusals use the common mapper (not-found 4, usage 2, gateway failure 7),
with no partial title or retry. The shared worker query now selects the existing
`Worker.model` field; older providers rejecting it require an upgrade. Other
worker command JSON DTOs and `worker get` text stay unchanged.

### Explicit authoring attestation

`node create` (`add`), `node update` (including `--data` and `--data-merge`),
`node set-properties`, `node merge`, `node import` (cli#2790), `replace text`, `spec new`, `spec edit`,
`spec replace`, `spec rescind`, `spec supersede` and `spec extract` accept
`--authored-with <task URN or id> --authored-revision <n>`. Pass both or neither;
one alone exits 2 before any request. Neither sends no `authoredWith` key.
For this slice the CLI never infers the task from a binding, role or content.
Multi-write commands carry the same claim on every node write that accepts it;
edge-only mutations have no such argument. `node import` sends the claim on its
content import (`importNode`) and on each node write of a restore (the update and
the create fallback). A node document cannot carry a claim: without the flags no
claim is sent. A half pair is refused before the target is looked up.

This is attested authorship, not verified compliance: a successful check does
not prove that the task was read or followed. The server judges the claim.
The four `AUTHORING_ATTESTATION_MISSING`, `AUTHORING_ATTESTATION_WRONG_TASK`,
`AUTHORING_ATTESTATION_STALE` and `AUTHOR_TASK_BROKEN` refusals retain their
existing exit codes. A `READABLE` refusal shows the required task, revision
and available `load.cli` route; only commands with the flags also offer
`Retry with: --authored-with ... --authored-revision ...`. Other commands show
the load route without promising a retry they cannot send. With `--json`,
`error.extensions` carries `taskAccess`, `task`, `taskId`, `taskRevision` and
`load` (`graphql`, `mcp`, `cli`, including their nulls). `UNAVAILABLE` shows only
that an owner must fix access or the rule, with no task identity, revision or
load route in either output mode, even if an inconsistent server sends them.
It never distinguishes an unreadable task from a nonexistent or unusable one.

The explicit revision must fit a GraphQL Int (-2147483648 through 2147483647);
an out-of-range value refuses before any request. Role clearing
(`node update --role ""`) carries the same explicit claim through its typed
clearing mutation; ordinary clearing keeps its existing document.

When extraction has succeeded but --strip-source is refused, do not rerun
spec extract. Load the required authoring task and finish the source edit
separately with spec edit; the warning gives task/load advice, not a retry
fragment. Supersede with an existing sole successor edge resumes retirement
without creating another replacement.

**Effective boot prompt preview (cli#1655).** `app agent list` and `team worker
get` show what the next bind would be served now, not the current session's
prompt: `pointerLevel`, `compiledNodeId`, `servedRevSeq`, or a typed
`bootPromptRefusal`. These nullable JSON keys are additive; `bootPromptUrn`
appears only from an already-visible own pointer at the matching served level.
No URN is constructed, content fetched, or approval inferred from the pointer.
`BOOT_PROMPT_INSTALL_LICENSE_INACTIVE` (the owner no longer qualifies for an
organization Agent; reachable from hadron-server cd0da474 on) previews as a
refusal like the others, and when a bind, cast or change-agent is refused with
it the command exits 8 (forbidden), not 1 (cli#1776).
Only `BOOT_PROMPT_NOT_SET` means none. A null/redacted preview is not visible; inconsistent or value-level malformed
decisions stay unknown. Structurally invalid JSON/typed values fail the command,
without output or fallback. A refusal carries no source level. An exact initial
missing-field validation may use the old label/listing once; auth, transport,
partial results and unrelated or mixed schema errors never select it. This is
not a generic old-stock version guarantee: the listing still requires its
existing installMethod/importer provider fields. A participant's ability to
see a served identity does not grant access to the compiled content.


### Immutable task result findings (cli#1550)

`team task finish <task> --outcome <word> --findings-file <path|->` records findings
in the same finish call. This opt-in path and `team task findings` require a
provider containing server#3248/#2662. Omitting the flag preserves the old finish
operation, receipt and warning behavior; it does not send a findings field.
A supplied `[]` is an explicit empty list. No extra confirmation is introduced.

The file is one UTF-8 JSON array, at most 4 MiB; `-` reads piped stdin once and
refuses interactive stdin. Each entry has severity `P1`, `P2`, `P3` or `note` and
string `text` (empty is allowed, whitespace is kept). At most 100 entries and
2000 Unicode code points per text. All entries either supply unique,
case-sensitive ASCII `[A-Za-z0-9_-]{1,32}` ids or leave every id absent/null
(the server assigns ids). Duplicate/unknown fields, trailing JSON, invalid UTF-8,
NUL and lone surrogate escapes refuse locally, exit 2, before any request;
errors never quote the input. Severity is a label, not an outcome or exit policy.

Opt-in finish retains the task/warnings envelope and adds `result.findings` as
`{items:[{id,severity,text}],total,hasMore}`. The receipt asks for 20 entries at
offset 0. `team task findings <task> [--limit N] [--offset N]` reads one page,
never drains or writes; default limit 20, explicit 0 counts only, positive
GraphQL Int values above 200 pass through for provider capping. Offsets are
non-negative GraphQL Int values. Continue using the actual served count and
hasMore, not the requested limit; an empty continuation page reports no progress.
JSON is `{taskId,findings}`; findings is null before a result exists, distinct
from an empty finished page. Human ids/severity use safe cells and text uses a
2000-code-point safe block; JSON retains the complete original valid text.

Missing/unreadable tasks exit 4; assignee/permission refusals 8, ended/transition
refusals 5, authentication 3, bad input/schema mismatch 2, transport/gateway
unavailability 7, other internal/invalid-answer errors 1. Server codes remain in
the JSON error envelope. After an uncertain finish answer, inspect the findings
before deciding what to do; do not resend automatically. Findings are immutable
and separate from finding dispositions. Existing task get/list/tree/mine shapes
and event reads do not change.

## Worker-own grants (cli#1832) and grant requests (cli#1868)

`hadron team grant list [--limit N] [--offset N]` reads one page (default20, maximum200; offset default0). `--limit 0` counts only. `hadron team grant get <id>` reads one row. These require a worktree worker binding on the targeted server; sign in and use `hadron team session start` first. `--app` may restate that App, never switch it (a pure permission refusal on that App read exits 2 as a missing App, no longer 8: cli#1896, #2239). The server serves only a person-driven worker-bound session in its own App; other callers receive empty/null, so an empty answer does not prove that you have no grants.

Scope as shipped: grants naming this worker directly as holder, plus its own requests that are neither activated nor revoked. Role-agent, other-worker, person and App holders are not listed. `list` and `get` only read; top-level `hadron grant list|get` remains the separate organization action-grants surface. The request verbs below ask for a grant and for its signing; no command here signs, activates or revokes one.

List JSON is `{items:[],total:N}` (empty items stays `[]`); count-only JSON is `{total:N}`. Get JSON and each item contain exactly id, action, values, holderKind, holderId, startsAt, expiresAt, maxUses, useCount, reason, createdAt, activatedAt, revokedAt, allows. Nullable values stay null, values keeps its JSON shape, and allows is the server's boolean fact about now. Human output uses yes/no for allows, raw timestamps, and no derived state label; terminal cells are sanitized, JSON retains original values. Get returns exit4 and the same not-found-or-not-visible line for null (hidden and missing indistinguishable). Transport/GraphQL errors use the standard mapping; malformed paging or a missing/cross-server binding exits2. New reads require a server containing hadron-server70bedd14bd; existing top-level grant reads use their old documents.

### Asking for a grant (cli#1868)

`hadron team grant actions [--limit N] [--offset N]` lists the registered grant actions of the App named by `--app` (or the App context): name, parameters, tool, `enforcement`, `issuerRule`, `source`. `hadron team grant action <name>` reads one by its exact dotted code. Anyone in the App reads: a person with their own credential (no worker binding needed) or a bound worker through its session. `--limit 0` counts only; `--json` is `{items,total}` (known fields, values unchanged); count-only text prints the `App:` header then `total:`. `enforcement` is the DECLARED level, not an active guarantee. A worker binding for this server takes precedence over the person credential: a bound worker reading another App gets the empty answer even if the person participates there. A blank or empty --app is absent: the App context applies and the `App:` header names it. The App ref is sent exactly as given (not even trimmed; blank counts as absent), never resolved or looked up first, so every refused caller (not in the App, unknown or malformed App, retired worker, App key, run) gets the same empty list (exit 0) or the same not-found line (exit 4): an empty answer proves nothing about the App.

`hadron team grant request --action <name> --values <json|@file> --reason <text> [--holder worker|agent] [--holder-id <id>] [--starts-at <time>] [--expires-at <time>] [--max-uses <n>]` asks for a grant for this worker (default) or its role agent; the holder id comes from the binding, `--holder-id` overrides it and is required when unbound. It prints the pending grant in the same fourteen fields as `team grant get` (JSON identical); the next-step line goes to stderr. `hadron team grant request-signature <grant-id>` asks for that pending grant to be signed and prints the signing request; `hadron team grant signing-request get <request-id>` reads one (to see whether the URL was signed) and `hadron team grant signing-request withdraw <request-id>` ends an open one (the request, not the grant; no confirmation prompt).

The signing request prints exactly id, grantId, version, termsHash, payloadVersion, state, requestedBy{userId,appId,workerId,sessionId}, createdAt, expiresAt, endedAt, lastRefusedAt, ceremonyUrl. `lastRefusedAt` is the latest time the server saw a verified signing attempt refused as ineligible (a timestamp only; null in `--json`, a dash in text, means none observed, never "never refused"; no identity or count; request-signature and signing-request get|withdraw need a server with hadron-server#4581 and an older one refuses them; `team grant request` does not read it). `state` is the server's own state for the request (OPEN, COMPLETED, DECLINED, EXPIRED, STALE, WITHDRAWN), printed as sent; the CLI derives no grant state label. `ceremonyUrl` is the page where a person signs: hand it over as printed. It is never composed, shortened or rewritten; text mode refuses (exit 1, nothing on stdout, pointing to `--json`) a URL carrying control or format characters, and `--json` always carries it byte for byte. Nothing here signs: a requested grant allows nothing until a person signs it.

These verbs make no local worker check. With no worker binding in the worktree they send the one operation without a worker session, print `no worker binding in this worktree; sent without a worker session` to stderr, and report the server's answer (`GRANT_REQUESTER_NOT_WORKER`, exit 8, for `request`). A binding for another server or another `--app` exits 2 with nothing sent. Empty optional flags, a blank id and malformed `--values` exit 2 before any call. Exit codes: `GRANT_REQUESTER_NOT_WORKER`, `GRANT_HOLDER_NOT_ALLOWED`, `SIGNING_REQUEST_NOT_REQUESTER`, `FORBIDDEN` 8; `GRANT_ACTION_UNKNOWN`, `GRANT_VALUES_INVALID`, `GRANT_TERMS_INVALID` 2; `GRANT_NOT_PENDING`, `GRANT_EXPIRED`, `GRANT_TERMS_UNREPRESENTABLE`, `SIGNING_REQUEST_ENDED` 5; `GRANT_NOT_FOUND`, `SIGNING_REQUEST_NOT_FOUND` 4. The verbs exist only where signing is switched on; a server without it answers `does not offer grant requests and grant signing` (exit 2, no upgrade advice). `--json` errors carry `serverCode`.

## Team App controls and read filters

`hadron team grant add <worker-id-or-URN> <name>` and
`team grant rm <worker-id-or-URN> <name> --yes` give/revoke named App grants,
separate from the existing worker action-grant list/request workflow. Ordinary
person authentication is used without --instruction; rm also supports the
held-worker instruction path below. add has no instruction flag. The server owns
manager permission. Names: TICKER_SUBSCRIBE_WORKERS (subscribe/unsubscribe other
workers at any cadence), WORKERS_STAFF (headless casting/retiring/deleting;
holder's staffing rights still apply), APP_MEMBERS (headless member add/remove;
holder's member rights still apply). The latter two are provisioned before the
separate server#3308 enforcement rollout; no gating effect is inferred here.
Giving is person-only/idempotent; revoking takes effect at the next grantee call.
The JSON receipt is {id,appId,workerId,name,grantedAt,revokedAt}; revokedAt may be null.
rm confirms on a terminal, or requires --yes. Refusals retain normal CLI exit codes.

`hadron team ticker settings get` reads the App's ticker settings (any App
participant; read-only, no flags beyond --app/--json). Text prints an `app:` line, then the four numbers and
marks the daily wake budget "derived" or "set" as the server says (the CLI never
computes it). JSON: {minCadenceSeconds,liveCap,selfSubscribeCadenceSeconds,
dailyWakeBudget,dailyWakeBudgetDerived}. Refusals map as everywhere (FORBIDDEN 8, not found 4).

`hadron team ticker settings set` changes only supplied flags:
--min-cadence-seconds, --live-cap, --self-subscribe-cadence-seconds,
--wake-daily-budget, --clear-wake-daily-budget. Omitted preserves; null
is refused. Clear=true returns to a derived budget; =false means "do not clear", the same as
omitting it, and is not sent. A set that names no field (a lone =false included) is refused
(usage, exit 2) before any request; no standby flag. --instruction alone
does not supply a settings field.
Server bounds: floor>=60s, self-subscription between floor and 86400s, live cap 1–100,
budget>=0; server also checks existing ticker/delivery conflicts. The CLI parses
GraphQL whole-number shape only. JSON: {minCadenceSeconds,liveCap,
selfSubscribeCadenceSeconds,dailyWakeBudget,dailyWakeBudgetDerived}.
App scope is --app, configured context or worktree binding for these App controls.

`team worker held --user <id|handle|URN|email> [--include-retired] [--limit N]
[--offset N]` reads across all readable Apps, oldest casting first. Default 20,
offset 0; limit 0 counts only. Empty output gives no cause: missing users, no holds
and unreadable holds are indistinguishable. --app does not narrow this query.
JSON is {items:[],total:N}; items retain nullable profile/live-session/hold fields.
No hold is released and no session is changed.

hadron team register candidates <code> [--limit N] [--offset N]
hadron team register pick <code> [--worker <id-or-URN>]
hadron team register attendee --agent <id-or-URN>  # or --worker (exactly one)

`candidates` and `pick` are experimental read-only projections. Never assign, route or deliver; never re-rank/filter the server's
answer. candidates JSON: {appId,code,depth,taskLoc,total,candidates}; each candidate
has depth,role,taskUrn,worker{id,kind,name,urn}; depth/taskLoc/role/name/urn may be
null. pick JSON: {appId,code,entries:[{id,kind}]}. Empty arrays stay [].

`attendee` (exactly one of --agent or --worker) reads the
resolved Channel register, separate from the action register index printed by
plain `team register`. JSON array rows retain attendeeUrn,namedBy,canRead,canPost,
lastSeenSeq and channel{id,name,loc}; nullable values are not interpreted as false.
App reads use --app, configured context or binding; none of these reads delivers.

`team role update <role> --repo owner/repo [--repo owner/another]` replaces repository
affinity; --clear-repos sends [], omission preserves (--clear-repos=false is omission). Can combine with --description;
--repo and --clear-repos conflict. Affinity is advisory, not an authorization gate.
The repo-update receipt additionally reports repos as an array; other role DTOs are unchanged.

`team task list --creator
worker:<ref>|user:<ref>` filters by creator server-side, combining with other plain
list filters. It cannot combine with --team. Without --creator the existing list
operation documents stay unchanged. No authorization is reimplemented locally.

## S3 worker lifecycle and sweep controls

`hadron agent create --name <n> --lifecycle LONG_LIVED|SHORT_LIVED` sets
the lifecycle at creation (omitted sends no key and uses the server default
LONG_LIVED). `hadron agent update <ref> --lifecycle LONG_LIVED|SHORT_LIVED` changes the
Agent's desired worker process lifetime, with the same server permission as
`--uses-tasks`. Omitted sends no lifecycle key and preserves the value.
`agent get` reads lifecycle separately. A server before #4242 gives text
`unknown (server before #4242)` and JSON `lifecycle: null`; other read errors
remain errors. Shared Agent fields and omitted-flag create/update documents
stay unchanged, so unrelated commands keep working on older servers.
Lifecycle write receipts include the returned value in JSON (`lifecycle`) and as a text line `  lifecycle: <VALUE>` under the `✓ created|updated agent` line (also with `--install-into`); a write that sent no `--lifecycle` prints no lifecycle text and omits the JSON key, while `agent get` always carries the key (a value, or null on a server before #4242). Unrelated Agent DTOs omit it.
LONG_LIVED is the server default; SHORT_LIVED runs while assigned a runnable
task. Recording the value launches or stops no process.

`hadron team worker leave-alone <worker> [--on|--off] [--reason <text>]`
marks a worker for the sweep to observe and report without starting, stopping
or repairing it, or clears that marker. With neither switch, it reads the
marker without changing it. A write requires exactly one true switch;
`=false` is refused. A reason is accepted only with `--on` (server limit:
500 characters). A repeat call succeeds. Names use `--app`, the App context
or the worktree binding; direct worker ids/URNs are resolved by the server
without ambient App scope. `--json` returns
`{"workerId":"…","leaveAlone":true,"reason":null,"by":"…","at":"…"}`.
`leaveAlone`, `reason`, `by`, `at` are nullable. All four are null for a
profile-only reader; human output calls that marker unknown (not visible to
you), never false. For a visible cleared marker, leaveAlone is false and
the other three fields are null.
Worker get/list keep their previous documents, DTOs and tables; use the new
leave-alone command to read the marker.

`hadron team review-stats --month YYYY-MM` (hadron-cli#2190; server #4281, spec
`cor:agt:130:06`, Proposed) reads an App's review-quality measures for one UTC
month. Read only; the numbers never decide readiness. `--month` is required
and a malformed value exits 2 before any request; the App is `--app`, else the
App context or worktree binding, and with neither the command exits 2 naming
`--app` before any request. Readers are the App's participants and its own key;
anyone else gets the server's missing-App answer, exit 4, byte-identical to a
missing App. `--json` is `{month, asOf, rows}`; `rows` is `[]` for a month with
no reviews. A row is keyed (family, model, modelReported) and carries
`recorded`, `family`, `model`, `modelReported`, `reviews`, `findings`,
`findingsPerReview`, `confirmedFindings`, `rejectedFindings`,
`undisposedFindings` (the three-way split, none folded into another),
`shareConfirmed` (null when `findings` is 0), `approvingReviews`,
`defectsMissed` (an integer, 0 with no links) and
`defectsMissedPerApprovingReview` (null only when `approvingReviews` is 0).
The one not-recorded row (`recorded:false`, family/model/modelReported null) is
last and never counted under a family. Nulls stay null in `--json` and print
empty in the table. `asOf` is when the numbers were worked out: a past month
can still change.

`hadron team app sweep-mode [OFF|OBSERVE|APPLY]` reads with no argument and
sets with one. OFF is the default and acts on no workers; OBSERVE reports
without repair; APPLY permits supported repairs. App scope is `--app`,
the App context or binding. `--json` returns
`{"appRef":"…","sweepMode":"OFF"}` on reads and writes alike.

Both controls write as the App manager. Optional `--instruction <kind>:<ref>`
claims a holder instruction, following the existing manager-write pattern:
kind is `task`, `team-chat-message` or `live-chat`, ref stays byte-for-byte,
never instruction text. A verified binding sends the worker session; the
server resolves the target App and checks its manager arm and reference.
Without the flag the write is the person's, with no worker session. Reads
never carry `X-Hadron-Session`. No refusal is retried without the instruction.
`--instruction` on either control read is refused; --reason also requires a
leave-alone write. Lifecycle flag writes and the two new controls require the
S3 server (hadron-server#4242), with schema skew exit 2. They do not raise the
minimum for unrelated Agent/Worker commands.

Refusals produce no partial result: `WORKER_LEAVE_ALONE_NOT_PERMITTED` and
`FORBIDDEN` exit 8, `WORKER_NOT_FOUND` exits 4, `WORKER_RETIRED` exits 5,
`BAD_USER_INPUT` exits 2. Invalid enums, switch combinations and instruction
syntax exit 2 before a request.


Argument coverage follow-up (cli#1833):
- `team chat read` and `channel read` accept repeatable
  `--team-head <stable-id>`. One occurrence sends legacy teamHeadRef; two or
  more send teamHeadRefs, preserving each raw occurrence and order (commas
  are literal), never both. The server owns the 20-entry cap and ID validation.
  Tag filters remain exclusive with --untagged/--tagged/--thread. Filtered
  reads do not move read state or binding watermarks.
- `team wakes due --preview` sends a non-spending read: no reservation,
  withholding or budget notice; with --accept-stop it declares no stop lane.
  Text labels preview and omits proof; JSON adds preview:true and keeps each
  proof empty, so rows cannot be acknowledged. Default output is unchanged.
  Older-server refusal of either new argument exits 2; no retry broadens the
  heads filter or turns a preview into a spending read. Monitoring preview
  from #1984 is unchanged; this is the public command.


## Instructed alarm acknowledgement and stall schedule (cli#2451)

`hadron team alarm ack <event-id> --instruction <kind>:<ref>` and
`hadron team stall-schedule set --assignment-minutes 30 --instruction <kind>:<ref>`
open the App-manager's held-worker path (server 2026-11-04 or later).
Without the flag, acknowledgement keeps its owner-in-chain path and ordinary
worker attribution; setting a stall schedule remains the signed-in person's
write. Only `set` accepts the flag; `stall-schedule get` stays a read.

Kinds are `team-chat-message`, `task` and `live-chat`. The reference is sent as
written, never fetched as instruction text. Malformed claims fail with exit 2
before requests. A complete, unchanged same-server binding supplies the session
only to the mutation; otherwise the instruction is checked as the person, with
no recorded worker claim and a diagnostic for an unusable binding. The server
owns admission and a refusal is never retried without the claim.
`ALARM_ALREADY_ACKNOWLEDGED` still refuses a second acknowledgement; the first
stands. Instructed acknowledgement JSON adds `warnings` ([] when absent), with
the task-create warning shape; text prints warning code/text on stderr.
No-flag request documents and JSON shapes remain unchanged.

## App-manager writes with an instruction (cli#2448)

These nine commands (and the five ticker writes at the end of this section)
accept `--instruction <kind>:<ref>`:
`app properties set`, `app model-group set`, `app model-group clear`,
`team delivery-settings set`, `team ticker settings set`, `team grant rm`,
`team host assign`, `team host unassign`, and `team host setup`. The first
six need server **2026-11-04** or later (delivery/ticker server#3398;
properties/model groups server#3376; revoke server#3340); the host trio
needs only the flag's base revision, **2026-11-03** or later. Other commands,
including properties clear and grant add, gain no flag.

Kinds are `team-chat-message`, `task`, `live-chat`; the reference is everything
after the first colon, sent unchanged, never instruction text. A bad kind or
blank reference is exit 2 before any prompt or request. The claim rides only
the mutation; lookups have neither claim nor worker session.

Without the flag, the existing document, variables and JSON output are
unchanged and no worker session is attached. With it, a complete binding for
the same server and target App, captured before the first lookup and unchanged
immediately before sending, supplies its worker session. Otherwise the same
claim goes as the signed-in person: an unbound shell is quiet, an unusable or
changed binding explains the fallback on stderr. There is no retry as a
person or without a claim after a server refusal.

The server owns admission: the holder must manage the App, host-launched
sessions are refused, and the reference must be readable and in scope. It
checks the reference, never whether the instruction really happened. An
admitted changing worker act records one ids-only instruction event; a no-op
records none. A person claim is checked but not recorded as an instruction.
The MCP tools take no instruction. `--expected-revision` is independent of
the claim. Model-group clear and grant rm still confirm (or require `--yes`);
confirmation precedes network requests on the instructed path.


For instructed **interactive** `team grant rm`, the target App is selected once
from --app, the configured App, or the binding's AppID. The prompt shows that
exact App reference and the mutation sends it unchanged, even if the binding
or configured App changes while answering. Attribution is still rechecked
separately and can fall back to the person with the claim retained. A legacy
binding carrying only teamMemory needs --app or a configured App on this one
path: exit 2 before any prompt or request, since resolving its App would need
a request before consent. Plain revocation and instructed --yes retain their
existing scope resolution. (cli#2448, rulings #116456/#116489.)

**Ticker writes (cli#2773, Refs #1617).** `ticker update`, `ticker delete`,
`ticker subscribe`, `ticker unsubscribe` and `ticker apply-templates` take the
same flag with the same meaning, help paragraph and exit-2 refusals; `ticker
create` has no instruction argument and gains none. A ticker or worker ref does
not name its App, so the CLI cannot match the binding to the target App: it
verifies the binding locally and the server resolves the App and owns the
admission (the holder must manage it). Without the flag the plain operation
goes out unchanged, and `ticker apply-templates` still never sends a worker
session; `ticker update` with only `--instruction` is still "nothing to update"
(exit 2). Needs a server carrying hadron-server#3321.
