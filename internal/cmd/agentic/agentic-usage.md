# hadron CLI — agentic usage

`hadron` is the command-line interface to the Hadron AI-memory
platform. This document is the single reference an agent needs to
drive the CLI. Read it once per session; everything below is stable,
documented contract.

## Setup and authentication

```
hadron auth status            # am I signed in? exit 0 yes / 3 no (--json: rejectedReason says why, when known)
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
`hadron auth whoami` / `status` announce the acting-as state loudly.

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
| 5    | conflict (e.g. duplicate install, a loc already taken) |
| 6    | cancelled / timed out waiting for the user |
| 7    | unavailable — the request never got an answer (gateway 5xx, reset, timeout) |
| 8    | forbidden — authenticated, but not permitted |
| 10   | Orca not reachable — `hadron orca …` run off the Orca host (command-specific; see below) |
| 13   | `team chat read --wait` timed out: every poll succeeded and no new message arrived (command-specific; not an error — wait again from the same `--since`) |

`hadron orca …` exits **10** when the Orca CLI/app does not answer (this is not the host that runs
Orca). A worker with no terminal is NOT that: it is a `dark`/`no_terminal` status and exit 0. The other
command-specific 9–12 below belong to `team push` / `team chat check` and are never emitted by `orca`; 13 belongs to `team chat read --wait`.

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

`team chat check` additionally uses **9** for an empty tagged range whose sampled
head is marked, (9 is not an error; handle it explicitly in `set -e` scripts), and **10** for an uncertain mark outcome. These are command-specific;
0 means messages were written and coverage confirmed, not that an agent read the file.

`team session sync-model` (without `--hook`) uses **11** when it could not prove a model
for the bound session: nothing was reported and nothing was refused. With `--hook` it
exits 0 in that case (and on any failure), prints nothing on stdout, and writes one fixed
sanitized line on stderr. Command-specific; 11 is not an error class of its own.

`team session set-model [<model>] [--family <token>]` (cli#887) needs at least one of the two;
only what you give is sent (an omitted model or family is preserved, never nulled). Zero values,
more than one model, or an empty/blank/over-long/multi-line model or family is a usage error
(exit 2, nothing sent); an empty `--family` does not clear. The family goes to the server as typed
and wins over its derivation; the server's refusal is surfaced and no receipt is printed. The
`--json` receipt names only what was sent: `{sessionId, model (null when family-only), family
(omitted unless sent), reported}`. `sync-model` (the hook) stays model-only and has no `--family`.

`team session shed [--auto] [--print-prompt] [--offline] [--cooldown <d>] [--transcript <path>]`
(cli#952) **exits 0 on every failure after the flags parse** and never blocks a reset: one fixed
sanitized line on stderr, and under `--json` `{status: written|skipped|failed|printed, node, claim:
written|failed|none, reason}`. With `--auto`, flag-parse errors also exit 0 (nothing on stdout
without `--json`); without it a bad flag exits 2 as usual. It writes `sheds:<stamp>` (or
`sheds:auto-<stamp>`) into the worker's own memory, reads it back, and only then writes the resume
claim: a failed node write never leaves a claim, a failed claim leaves the node (`claim: failed`).
`--auto` inside `--cooldown` (default 10m, stamp in the git dir beside the binding) is `skipped`
and retries only a failed claim. `--print-prompt` mutates nothing. No claim value is ever printed.
`--transcript` is opt-in and NOT scrubbed: its lines land in a node the team can read.

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
fix is a different credential, not somebody's permission, so commands exit
**3** on it. That includes `app agent add/remove` and `agent create
--install-into`, whose usual "needs CONTRIBUTOR+" guidance would be a false
remedy here. The latter still reports the agent as created, and puts
`app agent add` AFTER replacing the key. The message keeps the
server's sentence (without Apollo's `Context creation failed: ` prefix, which
the live `/graphql` refusal carries) and appends both recoveries:
`hadron auth logout && hadron auth login` (v0.15.0+ requests `account`), or a
key from the portal's API keys page (`/app/account/api-keys`) passed to
`hadron auth login --with-token`. If the key is in `HADRON_TOKEN`, replace
the variable: it outranks any stored login.
- `auth status --json` still prints its report for such a key:
  `authenticated: false` plus **`rejectedReason: "mcp-only-scope"`** (or
  `"unsupported-scope"`, below). It exits
  3 either way; before #681 it printed no report at all. `auth token validate
  --json` carries the same field beside `valid: false`.
- `rejectedReason` is set only alongside a rejection, and only when the CLI
  knows why. Absent means "rejected, reason unknown". Treat an unknown value as
  that.
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

`spec new`, `spec extract` and `spec supersede` cannot leave a spec without its
table-of-contents / inheritance edges unless told to: without `--no-edges`
(new/extract only; it deliberately creates the node edge-less), each spec node
is written together with those edges and (extract) its cross-ref, and a target
that does not resolve refuses the command before anything is written. A `spec new` that creates several nodes (a root with its
contract, or `--new-path`) writes each separately, so a failure part-way exits
non-zero naming the nodes it already created, each complete with its edges.

## Command surface (v1)

```
hadron auth login | logout | whoami | status | token create|list|validate|revoke <id>   # the capture example checks GNU ln -T before minting; after a non-zero exit following the mint, run `hadron auth token list`, identify the unexpected token by its label, then `hadron auth token revoke <id>` (add --yes when non-interactive)
hadron usage activity [--from <iso>] [--to <iso>] [--bucket DAY|WEEK|MONTH] [--memory <ref>]... [--time-zone <IANA-zone>]  # omitted flags stay omitted; no zero-fill, client resource-count cap or fallback from empty resource scope (cor:acl:090:01); JSON [{date,reads,writes}]
hadron usage events [--memory <ref>] [--loc <loc> | --node <PK-or-URN>] [--type <type>] [--since <iso>] [--until <iso>] [--limit 1-200] [--offset N]
hadron usage record --type <type> [--node-loc <loc>] [--session <id>] [--model <name>] [--tokens-in N] [--tokens-out N] [--action-args <json>]  # WRITE, cli#1409: one request, no retry or read-back; omitted flags are omitted, explicit 0 is sent; tokens are signed GraphQL Int sent unchanged; non-integer, out-of-range or invalid --action-args JSON exits 2 before any request; text "Usage event recorded", JSON {"recorded":true} identical whether or not the node or session was attributed (cor:dmo:080:02: an unreadable loc is recorded without attribution); --session on a live session you own counts as its activity, an App key can resume its own lapsed (never dormant) session, an owned ended/expired/idle session takes the record late without revival or activity; explicit empty strings are forwarded, only --type is required
hadron memory list [--shared-with-me | [--owned-by-me] [--include-agent-system]] | get <id-or-urn> (cli#1014: shows the stored last-sync outcome, Memory.lastSyncReport v1; `--json` adds `lastSyncedAt`, `syncError` (URL credentials masked), `syncReport` (the parsed v1 object {version,outcome,recordedAt,entries[{kind,loc,fromLoc?,code}],code?}, every entry, unknown kind/code passed through) and `syncReportStatus` recorded|none|unavailable; an unreadable report is `syncReport: null` + `unavailable`, never clean; text prints a `sync:` block only for a memory with a source or sync history, quoting server strings and capping at 50 entries) | set [<id-or-urn>] [--org <ref> | --owner-me | --app <ref> [--agent <ref>]] [--class <c>] [--max-rev-count <n>] [--draft-corpus] [--schema <json> | --schema-file <path>] [--listed-on-marketplace[=false]] (update only, cli#592: the opt-in marketplace catalogue flag, cor:acl:080:04, advertisement not access; omitted preserves it; `get` shows it as `marketplace: listed|not listed` and `--json` `listedOnMarketplace`, also on a `set` update's receipt) | attach <memory> --app <ref> --agent <ref> | set-active <id-or-urn> | rm <id-or-urn> | clone <id-or-urn> --target-urn hrn:mem:<root>:<slug> | transfer <memoryRef> (--org <ref> | --user <ref>) [--class <c>] [--reset-group-members] [--apply | --yes] | extract <parentRef> <targetUrn> [--move] | export <id-or-urn> [--out <dir>] | member list|add|set-role|rm <memory> --user <id> [--role <r>] | share list|create|set-role|revoke <memory> --grantee <user-ref> [--role <r>] | subscription list|create|set-role|rm <memory> --org <id> [--role <r>] | encrypt <memory> --data-key - | link-user <memoryRef> --external-user <id> [--data-key -] --yes | approve-all -m <memory> [--yes] | validate <memoryRef> [--check <kind>]... [--limit N] [--fail-on-findings] | reembed-failed <memoryRef> | config get <memoryRef> | config rule add|update <memoryRef> <role> [--author-task <ref>] [--validation-task <ref>] [--description-node <ref>] [--writers all|admin|owner] [--validate-by agent|platform] [--strict-sub-roles[=false]] [--enabled[=false]] | config rule rm <memoryRef> <role> --yes | config rule unlock <memoryRef> <role> [--yes] | config template list [--owner-server | --owner-org <ref> | --owner-me | --owner-app <ref>] | config template get <templateId> | config template create --file <path> (--owner-server | --owner-org <ref> | --owner-me | --owner-app <ref>) | config template update <templateId> --file <path> [--expected-revision <n>] | config template rm <templateId> --yes | config template apply <templateId> <memoryRef> [--dry-run] [--expected-revision <n>] [--yes] | usage <memoryRef>
hadron node list [-m <memory>] [--prefix <loc>] [--type <t>] [--object-type <t>] [--tag <t>]... [--where <json>] [--sort-property <json>] [--with-properties] [--with-data] [--sort-seq asc|desc] [--seq-gt N] | get <urn>... [--raw] | get <loc>... -m <memory> [--raw] | get <urn>|<loc> -m <memory> --body-only | get --prefix <loc> -m <memory> | add [--allow-empty] [--type <t>] [--object-type <t>] [--data <json>|--data-file <path>] [--properties <json>|--properties-file <path>] | update <urn> [--expected-revision N] [--content <text|-> | --content-file <path|-> [--allow-empty]] [--force] [--dry-run] [--type <t>] [--object-type <t>|""] [--data <json>|--data-file <path>|--data-merge <json>|--data-merge-file <path>] [--properties <json>|--properties-file <path>] | move <urn> (--to-urn <urn> | --to-memory <memory>) | clone <urn> (--to-urn <urn> | --to-memory <memory>) | merge <urn> --into <urn> [--field <f>]... [--delete-source] --yes | rm <urn> [--hard] [--recursive|-r] | export <urn> [-o <file>] [--format md|json|pdf] | import <file|-|--url <u>> [-m <memory>] [--with-edges] [--task <ref> [--task-args <json>] [--app <ref>] [--agent <ref>]] | approve <node-ref> [-m <memory>] [--revision N] | verify <node-ref> [-m <memory>] | mint -m <memory> [--dry-run] [--yes] | sign <node-ref> [-m <memory>] [--revision N] [--signer <user-id>] | signatures <node-ref> [-m <memory>] [--check] | signing-request get <request-id> | signing-request list <node-ref> [-m <memory>] [--state open|completed|declined|expired|stale|withdrawn] [--limit 1-100] | signing-request withdraw <request-id> | revision list <node-ref> [-m <memory>] [--limit N] | revision get <revision-id> | revision restore <revision-id> [--truncate [--yes]] | revision label <revision-id> --label <text> | revision delete <revision-id> [--yes] | revision clear <node-ref> [-m <memory>] [--yes] | recovery list -m <memory> [--limit 1-200] [--offset N | --all] | recovery get <node-urn>|<node-id>|<loc> [-m <memory>] [--content] | recovery graph -m <memory> [--edge <id>]... [--limit 1-200] [--offset N | --all] | restore <node-urn>|<node-id>|<loc> [-m <memory>] | next <parent> [--after <current>] [-m <memory>] [--raw] | open <node-ref> [-m <memory>] [--print] | edge-removals <node-ref> [-m <memory>] [--limit N]
hadron object create -m <memory> --type <t> --fields <json>|--fields-file <path> [--key <k>] [--name <n>] | get <ref> | update <ref> --fields <json>|--fields-file <path> [--reason <r>] | delete <ref> [--hard] --yes | find -m <memory> --type <t> [--match <json>] [--where <json>] [--sort <json>] [--limit N] [--offset N]
hadron asset list -m <memory> [--mine] [--mime <type>] [--include-deleted] [--limit N] [--offset N] | get <asset-ref> [-o <path>|-] [--force] | url <asset-ref> [-m <memory>] | upload <file> -m <memory> [--mime <t>] [--name <n>] [--description <d>] | rm <asset-ref> [--yes] | restore <asset-ref> | public-link <asset-ref> (--on | --off) [--yes] | link <asset-ref> --node <new-node-urn> [--name <n>] [--description <d>]
hadron task run <task-urn>|<loc> -m <memory> [--arg k=v]... [--app <ref> [--as-self] [--agent <ref>]]
hadron chat read [--since <seq>] [--node <urn> | -m <memory> --messages-loc <prefix>] | post (--body <text|-> | --body-file <path>) [--node <urn>] [--session <id>] [--reply-to <seq|loc>] | config show [--node <urn> | -m <memory> --messages-loc <prefix>] (offline, exit 0) | doctor [same coordinate flags] (read-only Channel check, report always printed; exit 0 ok, else first failing check: 2 bad config/coords, 4 Channel unavailable, or a failed read's own code)
hadron channel list [--owner-app <ref>] [-m <memory>] | get <id|address> | create <name> -m <memory> --loc <loc> [--description <d>] | update <id|address> [--name <n>] [--description <d>] | rm <id|address> [--yes] | read <id|address> [--since <seq>] [--before <seq>] [--limit N] [--offset N] [--mentions <ref>] [--thread <seq>] | post <id|address> <body|-> (--session <id> | --as-me) [--reply-to <seq>] | mark-read <id|address> --attendee <ref> --seq N [--owner-app <ref>] | read-state <id|address> --attendee <ref> [--owner-app <ref>] | register list [--channel <ref>] [--attendee <ref>] [--owner <ref>] [--org <id>] [--limit N] [--offset N] | register add --channel <ref> --owner <ref> (--attendee <ref> | --all-attendees) [--role both|post|watch] [--mention-only] [--description <d>] | register set <entry-id> (--role <r> | --mention-only[=false] | --description <d>)... | register rm <entry-id> [--yes] | search <id|address> <query> [--limit <n>] [--cursor <c>]   # post REQUIRES --session or --as-me (the server records the human silently otherwise); read --since is strictly-greater and the output reports nextSince; read --thread <seq> = the DIRECT replies to that message (server-filtered; unknown seq exit 4, reply-less message an empty page); a ref is the Channel id OR its address (chatRootUrn, printed by list); chatRootUrn is NULL for some Channels — the id always works; read-state exits 0 with lastSeenSeq 0 for a never-read attendee and 4 for an attendee that is unknown, unreadable or not registered on the Channel (one message for all three).
# channel get records a node read and may decrypt internally during its optional authorized root read; identity/URL selection does not imply no provider read effects. It also emits a best-effort chat-root node portalUrl (nullable JSON key; text URL: only when supplied). One authorized node read by chatRootNodeId; missing/concealed/failed pointer read gives null without failing the Channel read. List is unchanged; no transcript fetch. Portal-tab destination and default-team shortcut are not delivered by this pointer.

hadron search <query> [-m <memory>]... [--scope <name|id|app|global>] [--mode hybrid|keyword|vector|regex] [--prefix <loc>] [--type <type>] [--object-type <t>] [--tag <t>]... [--where <json>] [--sort-property <json>] [--with-properties] [--with-data] [--comments-only] [--limit N] [--offset N] [-l|--long] [--json]
hadron replace text <old> <new> --field <f> (--node <urn> | -m <memory>) [--prefix <loc>] [--regex] [-i] [--dry-run] [--yes] [--max-nodes N]
hadron edge list <node-urn> | <loc> -m <memory> | <node-id> [--direction incoming|outgoing] [--name <substr>] [--to <ref>] [--from <ref>] | add | update <edge-id> | rm <edge-id>
hadron spec label-report [-m <memory>] [--prefix <citation>] | list [-m <memory>] | get <citation> [--body-only] |--prefix <prefix> | describe | use [<memory>] | register [--check] | find <query> [--match-exactly] | grep <pattern> [--regex] [-i] [--field content|abstract] [--prefix <loc>] | replace <pattern> <replacement> [--regex] [--word-boundary=false] [--field content|abstract] [--dry-run] [--yes] [--max-specs N] | new [<loc>] ... | edit <citation> [--content-file <path> [--allow-empty]] [--title <title>] [--force] [--dry-run] [--expected-revision N --expected-node-id ID --expected-proposal-hash HASH] | extract <citation> --to-feature <fff> | link <from> <to> | lint [<citation>] | check-tools [--prefix <loc>] | citations [--src <path>]... [--exclude <glob>]... [--loose] [--stale-abstracts] [--strict] | supersede <citation> [--to <loc>] | import spec-kit|code | reserve <citation> [--name <name>] | renumber <from> <to> [--dry-run] | backlinks <citation> | unresolved | mint [--dry-run] [--yes]
hadron skill lint (-m <memory>... | --all | --node <ref>...) [--strict] [--json] | status (-m <memory>... | --all) [--host <host>] [--to user|project|plugin|<dir>] [--strict] [--json] | export [--node <ref>...] [--dry-run] [--force] [--prune] [--json] | plugin --out <dir> [--name <name>] [--scope <name|id>] [--zip] [--dry-run] [--json]
hadron coding review run [-m <memory>] [--base <ref>] [--head <ref>] [--diff <path|->] [--root <loc>] [--all] [--limit N] [--offset N] [--json] | review list [-m <memory>] [--root <loc>] [--broken] [--json] | review create <check-name> [-m <memory>] --trigger <cond> --description <d> [--scope <s>] [--tag <t>]... [--link <ref>[=<label>]]... [--seq N] [--content <text|-> | --content-file <path>] | review lint [-m <memory>] [--root <loc>] [--toolchain <t>|-] [--strict] [--suggest] [--fix [--yes]] [--json] | preflight list [-m <memory>] [--root <loc>] [--broken] [--route-model leaf|mirrored] [--json] | preflight create <loc> [-m <memory>] --route <action> --description <d> [--name <n>] [--symptom <s>] [--section <heading>] [--type <t>] [--tag <t>]... [--link <ref>[=<label>]]... [--seq N] [--content <text|-> | --content-file <path>] [--no-back-edge] [--no-body-line] [--route-model leaf|mirrored] [--attach-to <loc>] [--dry-run] | preflight route <node-ref> [-m <memory>] --route <action> [--description <d>] [--symptom <s>] [--section <heading>] [--no-back-edge] [--no-body-line] [--route-model leaf|mirrored] [--attach-to <loc>] [--dry-run] | preflight lint [-m <memory>] [--root <loc>] [--strict] [--route-model leaf|mirrored] [--json]
  Route model (cli#1025): `--route-model leaf|mirrored`, else `preflightRouteModel` in .hadron/config.json, else `mirrored` (the default, byte-identical to before; flag > config > default; a value other than the two, or an unparseable config, is exit 2). `leaf`: `create`/`route` write exactly ONE edge, the node -> the router (or `--attach-to <loc>`, an area node), labelled `to <action>`; nothing on the router's outgoing set, no back-edge (`--no-back-edge` and `--no-body-line` are no-ops), body line OFF unless `--section` is given (it then splices into the attach target; `--section` with `--no-body-line` is still exit 2); `--attach-to` under mirrored is exit 2; `--json` gains `model:"leaf"` and `router` is the attach target. `list`/`lint` under leaf read the router's INCOMING edges plus its outgoing (legacy) ones: list rows gain `model` `leaf|legacy` (one row per node, leaf wins; absent under mirrored); lint adds `legacy-outgoing-route` and `leaf-missing-edge` (a `[[link]]` in the router's OWN body with no edge to it; area bodies are not scanned) and carries label/retired/moved-memory to the incoming edges, and its human output ends with `N incoming edges visible to this caller` (never in --json). Every incoming edge counts as a route, so a non-`to` label is a `route-label-phrasing` warning.
hadron app agent customize <app> <agent> | member roles [--app <ref>] (cli#1226, R211 cor:dmo:050:04: App.availableMemberRoles, the roles `member add --role` accepts — the union of the App's installed Agents' member roles, server order; text one per line, `--json` a bare array, `[]` when none) | agent compile <app> <agent> | get <app-ref> | key list <app-ref> [--scopes [--host-uses <n>]] | key create <app-ref> --out <file> [--label <t>] [--force] [--print-key] | key revoke <key-id> [--yes] | key rotate <app-ref> --out <file> [--yes] [--force] [--print-key] | agent list [<app-ref>] (uses --app) | agent add <app> <agent> [--training-mode] | agent remove <app> <agent> --yes | agent set-boot-prompt <app> <agent> (<node-ref> [-m <memory>] | --clear) | list (--org <org> | --owned-by-me) | install (--org <id> | --owner-me) --agent <ref> --name <n> [--type <t>] [--urn <slug>] [--description <d>] | uninstall <ref> | set-active <ref> | key scope grant <key-id> <scope> [--host-id <host>] | key scope revoke <key-id> <scope>   # scopes: attention:deliver, host:operate; the App manager's, as themselves (never a worker session); host:operate names ONE host (exit 5 APP_KEY_SCOPE_HOST_CONFLICT: revoke first); scope-write output never carries key material; `key create` (cli#1223) writes the raw key to --out (new 0600 file, opened BEFORE the mint: an existing path/symlink is exit 2 and nothing is sent; --force = same-dir temp + rename, symlink replaced not followed; on windows 0600 only clears the read-only attribute and the file inherits its directory's ACL, so keep it under the user profile (cli#1324)), never to output unless --print-key (refused with --json, exit 2); an explicit empty or whitespace-only `--label` is exit 2 before the file is opened or anything is sent (omit --label for the server's default); the lost-answer message says "no file was kept" only when the cleanup removed it; --json {id,keyPreview,label,out}; a refused mint removes the file it created; createAppKey is sent once and never retried: a lost answer (exit 7/1) names the label and says to run `app key list <app-ref>` and revoke any unexpected key; write-after-mint failure names the key id and `app key revoke <key-id>` (exit 1); a new key holds no scope, so follow with `app key scope grant`; a person who manages the App mints, as themselves (App key / impersonation / worker session refused, exit 8); `key list --scopes` (cli#1333) is still ONE `appKeys` read, adding each key's `scopes` ([] when none) and, with `--host-uses <n>` (needs --scopes, n>=1, else exit 2), its newest n `hostUses` (absent from --json otherwise); the default list stays metadata-only (#1057); `key revoke <key-id>` (revokeAppKey, irreversible) and `key rotate` (regenerateAppKey: revokes EVERY active key of the App, the new key holds NO scopes so scope grants are redone) both confirm on a TTY and exit 2 with nothing sent when non-interactive without --yes; revoke --json {id,revoked}; revoke is never retried: a transport-class lost answer (exit 7) says the key may already be revoked and names `app key list <app-ref>`; server refusals keep their mapped exit and serverCode; rotate keeps `create`'s secret-file contract (--out 0600 opened first, raw key never in stdout/--json unless --print-key, never retried: a lost answer (exit 7/1) says every key may be revoked and to run `app key list <app-ref>`), --json {appId,appUrn,out,allOtherKeysRevoked}; same manager-as-themselves rule (exit 8)
hadron app memories [<app-ref>] [--limit N] [--offset N] (uses --app)   # cli#1209 over server#2190 appMemoryScope (cor:dmo:050:17): the memories App-wide reads (appNodes) cover for you, one row per memory with every source kind (AGENT_SYSTEM|AGENT_LINK|INHERITED_BASE|TEAM_SPACE) and the agent name only when you may read that Agent; one page ordered by memory URN, --limit 1..200 (default 20), next-page hint on stderr; WRITE (ADVISORY) yes|no|inherited, "inherited" = viewerCanWrite true but in scope only as an inherited base, so not a write target; --json {items[{memoryId,memoryUrn,name,class,sources[{kind,agentId,agentName}],viewerCanWrite,writeTarget}],total}; refusals are appNodes' (exit 8/4 by code)
hadron app update <app-ref> [--name <n>] [--description <d> | --clear-description] [--session-timeout-seconds <n>] [--yes]   # cli#1058: only the passed fields are sent (omitted = preserved; a clear is an explicit null, description only); no flags = usage error; confirms the target + field NAMES (never field values), --yes non-interactively; --json {app,id,urn,name,sessionTimeoutSeconds,status,changed}; a lost response is exit 7 "outcome unknown", sent once, never retried
hadron ai-config list [--app <ref>] [--agent <id>] [--with-endpoints] | get <id> | endpoints <provider> | create (--app|--agent|--org <ref>) --name <n> --provider <p> --model <m> [--endpoint <url>] [--api-key -] [--file <path>] | update <id> [--endpoint <url|"">] ... | rm <id>
hadron org list [--mine] | create --name <n> --urn <urn> | get <id> | public <org-ref> | update <id> | rm <id> | member list|add|set-role|rm <org-id> --user <id> [--role <r>] | invite create <email> --org <id> --role <r> | invite accept <slug> | invite show <slug> | set-active|use <orgRef> [--no-verify]   # "" clears; the active org is what --scope global resolves against
hadron scope list [--owner-org <ref> | --owner-app <ref> | --owner-agent <ref>] [--name <n>] | get <name|id> [--by-name] | create <name> (--owner-org|--owner-app|--owner-agent <ref>) -m <memory>… [--description <d>] | update <name|id> [--by-name] [--name <new>] [-m <memory>…] [--description <d>] | rm <name|id> [--by-name] [--yes] | explain <name|id> [--by-name] [--loc <address>] | set-active|use <name|id|app|global> [--no-verify]   # "" clears; applied when search omits --scope, and reported as scope.selectedBy="config". a NAME needs an App context (--app or the active App); an ID never does. --description cannot clear.
hadron agent compile <agent> [--no-set-pointer] | list [--org <id> | --owned-by-me] [--type ASSISTANT|CHATBOT] [--visibility ORGANIZATION|PERSONAL|PUBLIC] | list --public [--type <t>] [--limit N] [--offset N] | get <ref> | create --name <n> [--org <id> | --owner-me] [--type <t>] [--visibility <v>] [--description <d>] [--system-memory <id>] [--surface <s>]… [--persona-role <r>] | update <id> [<field flags>] [--tasks-index <node-ref> | --processes-index <node-ref>] (explicit empty clears) | rm <id> --yes | tasks list <agentRef> [--symptom <text> [--limit N]] (experimental, hadron-server#2421: up to 3 tasks matched on the abstract, adds MATCHED BY VECTOR/KEYWORD/BOTH and `matchedBy` in --json; --limit is ignored without --symptom, sent as given, server rules) | processes list <agentRef> | set-base <agent> (<base> | --clear) | set-boot-prompt <agent> (<node-ref> [-m <memory>] | --clear)
hadron agent ticker-templates list <agent-ref> | create <agent-ref> --name <n> --cadence-seconds <s> [--relevance all|mentions] [--reasons read-team-channel,every-tick,live-check] [--quiet-hours <json>] | update <template-id> --expected-revision <n> [--name <n>] [--cadence-seconds <s>] [--relevance all|mentions] [--reasons <csv>] [--quiet-hours <json> | --clear-quiet-hours] | delete <template-id> --expected-revision <n> [--yes]   # singular alias ticker-template; Agent managers write, future casts only, existing workers/tickers untouched; explicit revision, no fetch/retry; requested cadence (cast raises to App floor)

hadron agent clone <source> --to <org> [--name N] [--slug S]   # one server clone mutation, sent once and never retried, into an organization you administer; new PERSONAL draft with its own system-memory copy, links/imports retained; linked memories stay links. Source must be PUBLIC or managed in its own org. Text prints the returned NEW URN; --json prints the normal Agent DTO (no origin field). No install/subscription second step or manual-copy fallback. **Exit codes:** a typed refusal is a definite answer, nothing was created, so fix the cause and rerun: exit 8 for AGENT_NOT_PUBLIC and unreadable/unusable linked-memory, base or import; exit 2 for an unsupported target or encrypted content; a source-changed conflict (exit 5) carries retryable=true but is never retried for you, rerun by hand. Exit 3 is rejected credentials: nothing was created. Exit 1 with `(clone server error: …)` is a server fault whose effect on the clone is not known: verify as for exit 7 before rerunning. **UNKNOWN (exit 7, never success)** means the answer was lost, or a success reply carried no Agent (null/missing `cloneAgent`): the clone may exist. Verify before cloning again: `hadron agent list --org <org> --visibility PERSONAL` (`--org` takes the same id, slug or URN as `--to`; a new clone is a PERSONAL draft and is listed for org members); with `--name` look for that name, without it the server copies the SOURCE's name, so look for a PERSONAL agent bearing it. Refusal details are the first GraphQL error's own `code`/`reason`/`retryable` and blocker counts, positions and supplied names only (a hidden subject never gets a name); an empty-details first error yields none, never a later error's. The text suffix (stderr, human mode only) names the answer: `(clone refusal: …)`, `(clone authentication: …)` or `(clone server error: …)`, followed by the code, `reason <R>` and, when retryable is true, `retryable: rerun by hand`; each blocker then gets its own indented line, `blocker <CODE>  count <N>  "<name>" (position <P>), position <P>`, with positions as received and a quoted name only where the server supplied one. Server strings are made one-line and control-free; parse `--json`, not this text. Requires server cloneAgent S1; older providers reject as schema skew.
hadron team init [--app <ref> | -m <team-memory>] (uses --app, the context, or the binding)
hadron team worker cast --name <n> (--role <role> | --agent <ref>) [--dry-run] (uses --app) | list [--include-retired] (uses --app or the binding) | get <name-or-id> | update <name-or-id> [--signature <text>] [--trailer <text>] | set-reports-to <worker> (<target> | --clear) | set-boot-prompt <worker> (<node-ref> [-m <memory>] | --clear) | release <name-or-id> [--yes] | retire <name-or-id> --yes | rm <name-or-id> --yes
hadron team worker onboard --name <worker> --host <id> --repo <token> --branch <branch> --label <label> --family <family> --worktrees-root <absolute-path> --config <router.json> [--base main] [--display-name <text>] [--coordinator] [--apply] [--as-person] [--timeout 2m]   # Orca-hosted workers only; dry-run by default, submits nothing; --family is compared and queued as the server's canonical family (anthropic=claude, openai=codex, case/space folded; custom tokens stay themselves); prerequisites and partial-outcome contract below
hadron team role list [--team-agent <ref>] (uses --app or the binding) | get <role> [--team-agent <ref>] | create <role> [--description <d>] [--team-agent <ref>] | update <role> --description <d> | rm <role> [--yes]
hadron team session start (--as <worker> | --launch-grant <g>) [-m <team-memory>] [--repo <r>] [--branch <b>] [--transcript <path>] [--host <h>] [--tool <t>] [--model <m>] [--reports-to <worker>] [--force] | start [--session <id>] [--transcript <path>] [--host <h>] [--tool <t>] [--model <m>]   (no worker named: RESUME, cli#842) | whoami [--check] | log (--pr | --issue | --commit | --branch) <ref> [--action <a>] [--detail <json>] [--model <m>] [-m <team-memory>] | end [--handoff <text> | --handoff-file <path>] [--summary <text>] [--session <id>] [--resume-claim] | resume-claim [--session <id>] | list [--active] [--as <worker>] [--repo <r>] [--limit N] [--offset N] | list (--pr | --issue | --commit | --branch) <ref> [-m <team-memory>] | sync-model [--hook] | shed [--auto] [--print-prompt] [--offline] [--cooldown <d>] [--transcript <path>] | set-model [<model>] [--family <token>]
# cli#960: a bind whose resulting binding records no tool prints `warning: this binding has no tool recorded…` on STDERR (exit unchanged, --json stdout unchanged): `session log` then refuses, so pass --tool when you start a session that will log work; a resume of a session that already has a tool stays quiet without --tool
hadron team chat post <body|-> [--reply-to <seq>] [--as-me] [--orca-host-config PATH [--push]] (uses --app or the binding; with NO binding it REFUSES, exit 4, unless --as-me — cli#842) | read [--since <seq>] [--before <seq>] [--limit <n>] [--all] [--wait <dur> [--poll-interval <dur>]] [--mentions-me | --mentions <ref>] [--thread <seq>] (uses --app or the binding) | mark-read --through <seq> [--channel <ref>] (needs the binding)
# **WAIT (cli#1261):** `--since <seq> --wait <dur>` POLLS (team chat is never pushed): the same ordinary read every `--poll-interval` (default 5s, min 2s) until one returns messages (printed, exit 0) or `<dur>` passes (last empty read printed, exit **13**); a failing poll stops with its own code.
# **THREAD (cli#1262):** `--thread <seq>` returns the DIRECT replies to that message (never the message itself, never replies-to-replies; a reply's own thread is `--thread <its seq>`), filtered by the server, composing with `--since/--before/--limit/--all`. A seq naming no message is exit **4** (typed refusal, never an empty page); a message with no replies is a valid empty page, exit 0. A thread read is a filtered window: it never advances the watermark or server read state (use `team chat mark-read`).
# Team-chat post refusals (cli#1306): TEAM_CHAT_DUPLICATE and TEAM_CHAT_POST_RATE_LIMITED exit 5 (Conflict), retaining the server's message (earlier seq, or limit/reset time). --json error adds serverCode for these two refusals beside numeric code and message. A post or reply is sent once; the CLI never retries either refusal. TEAM_CHAT_BODY_TOO_LARGE remains exit 2. The node-addressed `hadron chat post --node …` door (cli#1319) maps the same three the same way (pinned): one CreateChannelMessage, no retry; only CHANNEL_NOT_FOUND creates the Channel and posts once more, and a refusal of that second post is not retried either.
# Chat-history search (cli#1327): `hadron team chat search <query> [--limit <n>] [--cursor <c>]` (uses --app or the binding) and `hadron channel search <id|address> <query> [--limit <n>] [--cursor <c>]` search ONE chat newest-first for a literal, case-insensitive substring. Results are REFERENCES only (`items[]`: seq, urn, nodeId, channelId, channelIncarnation; no body or snippet); read a hit with `team chat read [--app <ref>] --since <seq-1> --limit 1` (the text hint repeats the `--app` you searched with) / `channel read <id> --since <seq-1> --limit 1`. `--json` is the server page: `{channelId, channelIncarnation, complete, cursor, items}` (`items` is `[]` when empty, `cursor` null when complete). `complete:false` means a full page or the server scan bound stopped the search, NOT "no matches": rerun with the SAME query and `--cursor <cursor>`; only an empty page with `complete:true` means none. The server owns query length, default/max `--limit` and the scan bound; the CLI passes `--limit`/`--cursor` through unclamped. Refusals keep the server code in the `--json` error as `serverCode`: TEAM_CHAT_SEARCH_QUERY_LENGTH and TEAM_CHAT_SEARCH_CURSOR_INVALID exit 2, TEAM_CHAT_SEARCH_ENCRYPTED exit 5 (an encrypted chat is refused, never an empty page), `--limit` below 1 BAD_USER_INPUT exit 2, an unreadable/unknown Channel exit 4. A search changes no read position and marks nothing read.
hadron team attention [--since <token>] (uses --app or the binding)
hadron team attention switchover preview | apply --proof <proof> [--yes] (uses --app or the binding)
hadron team task create <title> [--priority 1..5] [--blocked-by <task>]… [--description <d>] [--assignee <party>] [--owner <party>] [--parent <id>] [--project] [--start-by <iso>] [--tag <t>]… [--source <s>] [--status backlog|pending] [--require-role <r>] [--family-in|--family-not-in <a,b>] [--exclude-worker <w>]… [--properties <json|@file>] [--close-session|--no-close-session] (uses --app or the binding) | assign|reassign <taskId> --assignee <party> [--close-session|--no-close-session] | start|progress|cancel <taskId> | park|ready <taskId> | finish <taskId> --outcome <word> [--summary <s>] [--evidence <url>] | get <taskId> | list [--status <s>]… [--text <words>] [--assignee <party>] [--owner <party>] [--tag <t>] [--queued] [--roots-only] [--parent <id>] [--limit N] [--offset N] (uses --app or the binding) | list --team[=worker:<ref>] [--status <s>]… [--tree] [--stale-over <duration>] (cli#1044: a coordinator's work in flight. Team = the coordinator (bare --team = the bound worker; the value must be attached with =) plus every worker whose roster reportsTo chain reaches them, in this App. Rows = the App's tasks a team member owns, is assigned or created (a task a person created, or a worker outside the team created, does not join through its creator; match stays party), plus all their descendants. It reads every page, never truncates, and refuses a non-advancing page (exit 1). --status applies after the join. --stale-over keeps PENDING/STARTED tasks idle longer than the duration, measured from updatedAt, else startedAt, else createdAt, and adds a stderr note that records lag. An unknown coordinator exits 4. No other App is read (server#2370). The only flags it combines with are --status/--tree/--stale-over, and --text (case-insensitive title/description substring, server-validated 1–200 characters after trimming); text uses a second paged server read and intersects after the full team join. Other plain filters are refused (exit 2). --json = {appId, coordinator, coordinatorSource, members, staleOverSeconds, tasks:[task + ownerTeam/assigneeTeam team|outside|unknown|user|null, match party|descendant, ageSeconds, ageFrom]}; --tree changes text only) | mine [--limit N] [--offset N] (needs the binding: the bound worker's PENDING+STARTED tasks, fixed filters) | tree <rootTaskId>   # a party is worker:<id|urn|name> or user:<id|handle>; close-when-done defaults to NO at create (the assigner opts in with --close-session; hadron-server#2249), while assign|reassign with neither flag keep the stored value; writes are the bound worker's when a session is bound; create|assign|reassign|finish|start|progress|cancel|park|ready may succeed WITH server warnings (cli#962, server#2198; the five simple verbs since cli#1283, e.g. task.cancel.open-children): text mode adds stderr lines `warning: [<code>] <text>` (+ ` → <taskUrn>` when the register maps it, ` (no task mapped)` when unmapped) after the unchanged stdout receipt; --json adds `warnings: [{code, text, register: {state, taskLoc, taskUrn} | null}]`, always present, [] when none; codes and text are the server's verbatim (unknown codes included); exit code unchanged, and a failed call prints no warnings; the create|assign|reassign receipts and `get` print `close session when done: yes|no` from the server-returned value (the same boolean as --json `closeSessionWhenDone`, cor:agt:050:10) — yes adds that finishing or cancelling asks the assignee to end its worker session and the platform may end it after a grace period (through a live host), no is the bare line; the command itself never ends one, and declining the request does not keep the session, so pass --no-close-session at create (or on the assign that first names a queued task's assignee) for a long-lived assignee (cli#958); the platform may end a flagged session through the worker's live host after a grace period (10 minutes by default, set per App), the choice has no in-place setter, and reassigning a task to its current assignee is refused (TASK_INVALID_TRANSITION, exit 5) with the choice unchanged (cli#959)
# Task handles (cli#1131, cor:agt:050:01): every task carries a server-derived handle (`AT3.405` = project AT3, then its number under each parent; `AT0.<n>` = no project) and `taskNumber`, which are additive: --json keeps `id` and every existing key and adds `handle` and `taskNumber`; text output prints the handle BEFORE the full id (list/mine/--team tables gain a leading HANDLE column; get, tree and the mutation receipts name `AT3.405 <id>`), and a server that returns no handle prints today's output (list/mine/--team then omit the HANDLE column); the task commands REQUIRE a server that serves `handle`/`taskNumber` (hadron-server#2579): an older server rejects every task query as a schema-skew refusal (exit 2, no fallback); task refusals print terminal-safe on stderr (controls neutralised, 1024-rune bound) while the --json error envelope keeps the server's raw text; every command that takes a task id (get|tree|start|progress|finish|cancel|assign|reassign|park|ready, `create --parent`, `list --parent`) also takes a handle, bare (`AT3.405`, case-insensitive) or qualified (`hrn:agenttask:<root>:<app-slug>:AT3.405`), passed to the server byte for byte (the CLI never parses, composes or allocates one); a bare handle held by several Apps is refused by the server with TASK_HANDLE_QUALIFY naming the qualified form, and an unknown handle or another App's handle answers as an unknown id; `create --project` makes a top-level project (AT1, AT2, …) instead of a task under the reserved no-project AT0: sent only when the flag is given (`--project=false` sends false), refused by the server with --parent or an AT0 parent, no client-side authority check; AT0 never appears in `list`/`--roots-only` output; event/roster/alarm lines still print bare ids (epic #2614 S6).
hadron team roster (uses --app or the binding)   # states are plan-derived, rendered as the server derives them; MODEL (self-reported) and TIER (server high/mid/low, null = unbound/unreported/unmapped) sit beside FAMILY, advisory only and never authorization; the table ends with a 'free for work by tier' line tallying the rows the server marked freeForWork (NOT state AVAILABLE), unrecognised/null tiers under unknown (table only; --json adds model, modelReportedAt, tier and keeps every earlier key). `team worker list` gains the same MODEL/TIER columns and --json keys, plus a FAMILY column and `family` key (cli#887; also on `worker get --json`): the server's recorded family as sent, null/dash = unknown, never inferred from a tool or host (`claude-code` is not a family). Needs a server that serves these fields (older servers reject the query, no fallback) ; HELD BY (name, else @handle) + SINCE show the holder of the worker name (heldBy {id,handle,urn,name}|null + heldAt in --json, additive; a name the server withheld is null and renders like an unset one, cli#1032) TERMINAL column (cli#1375, hadron-server#2771): the worker's latest host terminal report as `status (age)` / `status (reason, age)` (busy|idle|locked|dark; age from checkedAt on every reported row, display only), a dash when null; --json adds `terminal {host{id,label},status,prompt,reason,lastTurnAt,checkedAt,reportedAt} | null`, additive. Like model/tier it needs a server that serves the field: an older server's rejection surfaces as the schema-skew refusal. `team worker get` shows a `terminal:` line and the same `terminal` key from its own operation, so an older server falls back to null. REPORTS TO column (cli#1085): the server reportsTo party, "(retired)" marked, a dash for none; --json already carries reportsTo/reportsToRetired.
hadron team composition [--json] (uses --app or the binding)   # cli#1085: the server teamComposition read, rendered as returned: direct-report lanes in the server order (coordinator name, the unassigned lane last), a retired coordinator stays named and flagged, non-retired workers per lane with URN/role/family/state (the coordinator URN rides the lane heading; a missing URN is a dash), plain countsByRole and countsByRoleFamily where a null role or family is its own bucket (a dash in text, null in --json); --json {appId,asOf,lanes:[{coordinator|null,coordinatorRetired,countsByRole,countsByRoleFamily,workers}]} with [] never null. A read, not a capacity policy: no balance judgment, no second roster join. A refused read, a server without teamComposition, or a null result is a failure (mapped exit code, nothing rendered), never an empty team; only a server-confirmed empty team prints lanes [].
# Deleted-node recovery (cli#985; writers only): `node recovery list|get|graph` and `node restore` are the ONLY way to read or bring back a soft-deleted node — `node get|list|rm` stay live-only. A ref goes to the server unresolved (an ordinary resolve omits deleted nodes). `recovery list|graph` page 20 by default with an explicit `--limit` (1-200); when more exist, STDERR says `showing a-b of N; more: --offset b` (stdout/--json unchanged) and `--json` carries `{items,total,limit,offset,hasMore}`; `--all` reads every page and fails as a whole if any page fails (never a partial list; pages are not a snapshot, so concurrent restores can move rows). `recovery get` never reads the body unless `--content`, which writes the raw stored body byte for byte (text) or `content` + `contentIncluded:true` (--json); an unreadable body is an error, never an empty one. A `recovery get` miss is exit 4 with one fixed sentence whatever the cause (live, absent, not a writer). `node restore` restores ONE node (and the comment threads its own deletion removed), sends one request and never retries: refusals keep the server's message (`NODE_PARENT_DELETED` exit 5 names the ancestor to restore first; governed/comment/compiled-boot-prompt/protected exit 2; `ROLE_WRITER_LEVEL_REQUIRED`/`ROLE_WRITERS_UNSUPPORTED` exit 8); a lost response is exit 7 and means VERIFY with `node recovery get` before retrying. The receipt is the plain node (id, urn, memoryId, loc, name, nodeType, updatedAt, deletedAt) with no changed/idempotent flag.
# Paging (cli#967): `agent list`, `node list` and `team session list` list EVERYTHING unless you pass --limit/--offset (one page). `team task list|mine`, `team events`, `team host ops`, `comment list`, `node revision list`, `node edge-removals` and a bounded `team chat read` default to 20 per page; a full default page prints "more may exist" on STDERR (stdout and --json shapes are unchanged), and `team chat read --all` walks every page. A list with no total (`node revision list`, `node edge-removals`, `team task list`) cannot prove it ended, so treat a full default page as "there may be more". A scan that drains a list (`chat read`, `memory export`, `spec` scans, `node import` overwrite probes, …) stops only on an empty page; a server that keeps returning a page already read makes it FAIL with exit 7 (`Unavailable`: "the server returned a page already read … the list is not advancing"), never succeed with a partial list.
hadron orca terminal status (<worker|terminal-id> | --all) [--config <host.yaml>] [--json]   # ORCA INTEGRATION: reads Orca terminals, needs the Orca CLI/app on THIS host (exit 10 off-host); never types. One worker, or --all = ONE JSON array, one entry per active App worker. --config (cli#1378, the file of `team host run`) is the unattended form: Orca = delivery.orca_bin (absolute, exit 2 if relative) else PATH; server read with the host key from key_file only (0600; never HADRON_TOKEN/env/token store; no session); --app with --config exit 2; --all = the App workers the file configures; bad file exit 2, key file missing/too open exit 3 before Orca is listed; a host key cannot read tickers, so idle fails closed. Contract `terminal-status@1`, see "Orca terminal status" below.
hadron team push <worker> (--text <message> | --message-file <path>) [--seq N] [--wait-timeout 120s] [--draft-wait 30s] [--draft-stable-for 30s] [--confirm-timeout 10s] [--poll-interval 250ms]   # bound sender, one guarded console input; CPU-only busy/unknown/draft decisions and fresh on-screen turn confirmation. Command-only exits: 0 sent, 9 sent-with-draft (SUCCESS: NEVER RETRY), 10 held-busy, 11 held-unknown (zero composer input), 12 unconfirmed (mutation may have occurred: DO NOT RETRY). Draft wait and exact-byte stability overlap; stability cannot be below30s. JSON: outcome/phase/reason/draftIncluded/recipient/seq; no message/draft/screen text. Prefer --message-file (text argv is visible); no chat post or mark. Own-attempt lock only; ms read-to-input race remains and process death can lose a draft. Existing unguarded callers are not migrated by this command.
hadron team events [--alarms] [--since <iso>] [--after <eventId>] [--task <id>] [--type <t>]… [--limit N] (uses --app or the binding)   # oldest-first; --after pages forward
hadron team alarm ack <eventId>   # stops the alarm waking higher owners (cor:agt:050:20)
hadron team availability get | set (--worker <ref> | --family <token>) --state <available|unavailable> [--reason HOST_DOWN|OTHER|OUT_OF_TOKENS|STALLED] [--until <iso>] (uses --app or the binding)
hadron team chat check   # bound worker's team channel only; complete tagged range to private JSON artifact (stored C is trusted only for matching cursorIncarnation; otherwise scan from0 and disclose both values, possibly repeating messages), path on stderr BEFORE fenced H mark; includes server @all/team selection; mark covers untagged gap too; open artifact to read it, delete it yourself afterward; no inbox proof ack. Exit 0 written+marked, 9 empty+marked, 10 uncertain; errors1–8 retained. --json bounded receipt {error,outcome,sampledCursor,scanCursor,cursorIncarnation,sampledHead,messageCount,artifactPath,markAttempted,markStatus,markVerification}; no message bodies on stdout. Fenced mark response proves coverage, lost response uses strict stored/current incarnation readback then at most one identical fenced retry, never a mismatch/unguarded fallback. Page/file/path failure sends no mark; final receipt failure can follow confirmed mark (no rollback). Own-session reads/marks may count as checks even on command failure; no stamp/roster OK claim. Requires server#2194 and a private cache filesystem supporting file+directory sync; no production cursor experiment.
hadron team chat inbox [--after <endCursor> | --ack-file <path|->]   # own bound live worker session only, same canonical App/server; one rendered page, output+flush before ack; --json stable page/proof ids, no hidden journal; retain privately for ack-only recovery {"batchIds":["proof"]}; '-' requires piped input; ack recovery does not reread/redisplay; same-session proof/15-minute server boundary; no host-key fallback, no mark-read compensation; before ack a repull can redisplay
hadron team chat search <query> [--limit <n>] [--cursor <c>]   # search the team chat history; references only, see the cli#1327 note above (uses --app or the binding)
hadron team delivery-settings get | set [--check-period-seconds N] [--failed-checks-to-overdue N] [--batch-window-seconds N] [--busy-delay-seconds N] [--stuck-window-seconds N] [--standby=true|false] (uses --app or the binding)   # check period default 600s and failed checks default2 + 5min slack; server-derived check status; pair period with team ticker cadence; batch/busy/stuck settings deprecated; batch window IS ticker cadence floor (min 60s, default 120); existing tickers retain cadence, floor raise above a live ticker refuses; server defaults busy/stuck 600/900s; server enforces its current limits; omitted preserves, explicit false sends false, no null/reset; no-field set is manager-only current-values readback (not a database no-op); use get for a participant read; standby true currently refused (exit 5), never an activation claim; ordinary authenticated caller, server controls manager rights
hadron team pause | resume [--app <app>]   # App-manager setting; no worker-session header; regular wakes held while paused, PENDING(TEAM_PAUSED), no interruption; separate from stop signals and worker self-pause. Pause preserves the original timestamp; resume clears only the App pause and restarts OVERDUE clocks; resuming an unpaused team stamps nothing. JSON {app,paused,teamPausedAt}, timestamp nullable, returned state authoritative; FORBIDDEN 8, APP_NOT_FOUND 4; mutations sent once.
hadron team worker change-agent <name|id|URN> --agent <id|URN> [--yes]   # App-manager write, no worker-session header; name needs App scope (otherwise exit 2 before prompting); direct worker refs identify their own App, never the ambient one. Confirm before command requests (non-TTY needs --yes). Clears the custom boot-prompt selection and replaces agent-generated wake schedules including edits; hand-created tickers stay; next bind uses new agent/role, open session finishes under old casting (server containing #2504 required). Agent shape only checked locally, server resolves installation/role/grants. JSON worker DTO, text identity/agent/role. WORKER_AGENT_UNCHANGED/WORKER_CASTING_STALE/WORKER_AGENT_HAS_NO_ROLE exit 5; unchanged is a refusal, never success. No mutation retries; lost answer means inspect worker first.
hadron team stop list [--worker <ref>] [--include-ended] | set --scope worker|team|host --level pause-dispatch|pause|halt [--worker <ref> | --host <id>] [--until <RFC3339>] | lift <signal-id> (uses --app or the binding)   # #911 B1 over server #2022: stop signals, ids and times only; list pages to the end (200 a page; a non-advancing cursor refuses, exit 1, nothing shown); list worker filter = signals COVERING that worker now (a team signal on a head covers its reporting tree); set: worker/team scopes name a worker (a team by its head), host scope names a host (a host key may omit it), until is RFC 3339 with offset sent as UTC, omitted = until lifted; lift one id: changed=false is "already ended", never a lift, and lifting one never ends another; JSON list = array ([] when none), set = signal, lift = {changed,signal}; person credential, no worker session attached; notices recorded but NOT delivered (lane dark), no interrupt/resume/delivery claim; team rule: no set/lift before hadron-server#2169 is live; every set currently refused STOP_SIGNAL_UNAVAILABLE (exit 5); APP_MANAGER_REQUIRED/HOST_SCOPE_REQUIRED/STOP_SIGNAL_SCOPE_FORBIDDEN exit 8, STOP_SIGNAL_NOT_FOUND 4, BAD_USER_INPUT 2; stop refusal codes print fixed sentences, never the server text; an unrecognised error on set/lift says its outcome is unknown; a lost write answer exits 7, outcome unknown, sent once (check with list including ended)
hadron team wakes due [--worker <ref>]… [--first N] [--after <cursor>] [--accept-stop]   # cli#1279: the person's read of the wakes due (cor:agt:080:08); never a worker session (the server refuses one: due conceals the App, APP_NOT_FOUND exit 4). You see the workers you hold, or the whole App as its manager. --worker names resolve client-side and an unknown one exits 4 (the server answers an unresolvable ref with silence). A WAKE_BUDGET_EXHAUSTED page is printed and then exits 5 naming the status (its wakes stay due). --accept-stop sends accepts [MENTION,TICK,TASK,STOP] and declares the lane you read (cor:agt:090:04); omitted, no STOP notice is returned and nothing is declared. due --json = {status, withheld, wakes:[{workerId, workerName, proof, tickText, unread, unreadMentions, anchorAt, deliverAfter, busyDeadline, channels:[{channelId, loc, fromSeq, throughSeq, latestMentionSeq}], reasons:[{kind, tickDueAt, ticker:{id,name}|null, task:{id,handle,title,event,chainIndex,eventId}|null, stop:{eventId,signalId,level,notice,interrupt}|null}]}], dormant:[{workerId, workerName, mentions}], hasNextPage, endCursor}
hadron team wakes ack <proof>…   # cli#1279: record delivery of the proofs from `team wakes due` (cor:agt:080:16), as the person; never moves a read position. --json = {acknowledged, unchanged}; refusals lead with the server code (WAKE_PROOF_REQUESTER_MISMATCH 8, WAKE_PROOF_STALE 5, WAKE_PROOF_INVALID 2, WAKE_ACK_FORBIDDEN 8 for a worker's session, which this command never sends)
hadron team wake-settings get --worker <id-or-URN> | set --worker <id-or-URN> [--mentions immediate|off] [--paused-until <RFC3339>] [--clear-pause]   # cli#1292 over server #1747 (cor:agt:080:03, :21): one worker's wake settings; --worker takes an id or hrn:worker: URN (a name is exit 2: the calls take no App). get --json = {worker:{id,kind,name,urn}, mentions:"IMMEDIATE"|"OFF", pausedUntil, subscriptions:[{id,ticker:{id,urn,name,cadenceSeconds,enabled}|null,viaGrant,createdAt}] ([] when none), effectiveCadenceSeconds, dormant, isolated, isolatedAt, isolatedByHandle, isolatedUntil, canManageIsolation}, the server's defaults when nothing is stored; set sends ONLY the flags given (omitted keeps, nothing is sent as null), --clear-pause sends clearPause:true and no pausedUntil, no flag / --clear-pause with --paused-until / a bad --mentions or time is exit 2 with no request; set prints the returned settings in the same shape; who may set is the server's rule (the worker, its holder or the App manager, never the may-subscribe-workers grant); refusals keep their code as the prefix (WAKE_SETTINGS_FORBIDDEN 8, FORBIDDEN 8, WORKER_NOT_FOUND 4, BAD_USER_INPUT 2); isolating/clearing isolation is `team isolation` (cli#911 B2)
hadron team isolation set --worker <id-or-URN> [--until <RFC3339> | --for <duration>]   # cli#911 B2 (MCP cut C2, cor:agt:080:29): isolate a worker; --worker takes an id or hrn:worker: URN (a name is exit 2). Who may: whoever manages the App or the worker's holder, as a person acting as themselves; no worker may isolate (the server refuses a worker session WORKER_ISOLATION_FORBIDDEN), and these commands never send the bound worker session, so from a bound worktree they act as the person. set with neither time flag sends no until (the server's 2-hour default); --until (RFC 3339 with offset, whole seconds, later than now) or --for (positive whole-second duration) is sent once as UTC; both, a past, fractional (`.` or `,`), offset-less or out-of-range-offset (past +/-23:59) --until or --for <= 0 is exit 2 with no request; the 7-day cap is the server's. Until the server enables isolation (hadron-server#2130) every set is refused WORKER_ISOLATION_UNAVAILABLE (exit 5) and clear works; the server reports no isolation meanwhile, so a returned snapshot never shows the worker isolated. Both print the `team wake-settings get` --json shape (canManageIsolation = whether you are in the set, not whether isolation is available). Refusals keep their code as the prefix (WORKER_ISOLATION_UNAVAILABLE 5, WORKER_ISOLATION_FORBIDDEN 8, FORBIDDEN 8, WORKER_NOT_FOUND 4, BAD_USER_INPUT 2). Read isolation with `team wake-settings get` (no `team isolation get`)
hadron team isolation clear --worker <id-or-URN>   # cli#911 B2: clear a worker's isolation (a no-op when none is set); works while isolation is unavailable; same audience, refusals and --json shape as set
hadron team stall-schedule get | set [--assignment-minutes N|default] [--not-started-minutes N|default] [--no-progress-minutes N|default] [--owner-reaction-minutes N|default] [--host-lapse-check-ins N|default] [--host-report-timeout-minutes N|default] [--session-end-grace-minutes N|default] (uses --app or the binding)   # an omitted flag keeps its value, `default` restores the platform default; set is the App manager's, never the bound worker's (exit 8 APP_MANAGER_REQUIRED); host report timeout (5..1440, cor:agt:070:02) and session-end grace (1..1440, cor:agt:050:10) are range-checked before any call (exit 2, nothing sent, even alongside valid flags); get/--json show the server-returned values (sessionEndGraceMinutes, hostReportTimeoutMinutes; platform defaults 10 and 60 today), never client-computed (cli#983)
hadron team host list | ops [--host <id>] [--status issued|collected|succeeded|failed] [--worker <ref>] [--limit N] [--after <cursor>] | issue <type> --host <id> --worker <ref> [--arg k=v]… [--as-person] (uses --app or the binding)   # ops: newest first, --json {items, hasNextPage, endCursor}; issue: worktree.create|worktree.remove plus worker.start|worker.end issued by App managers (worker.deliver exit 2 HOST_OPERATION_NOT_ISSUABLE, other types exit 2 HOST_OPERATION_TYPE_UNKNOWN, bad args exit 2 BAD_HOST_OPERATION_ARGS, not permitted exit 8, unknown host exit 4); bound: issued as the worker (the target's reports-to), --as-person: as yourself (holder or App manager); arguments are ids and name tokens, never a credential
hadron team host run --config <host.yaml> [--once] | status --config <host.yaml>   # the host poller (cli#831, cor:agt:070): registers each poll, collects, runs ONLY worktree.create|worker.start|worker.deliver|worker.end|worktree.remove (anything else reported FAILED locally), reports every outcome; authenticates with the host:operate key from key_file only (mode 0600; NEVER HADRON_TOKEN or an env var); a launched agent gets HADRON_LAUNCH_GRANT and no host credential, and binds with session start reading it (the grant names the worker, so no worker argument; never forced; LAUNCH_GRANT_INVALID exit 8, LAUNCH_REFUSED exit 5); worker.end prompts and reports FAILED after end_wait (the server records an ended session as success; it never kills); worktree.remove refuses dirty, untracked, no-upstream, unpushed, or after a failed end; --json: one event object per line; no host key exit 3, bad config exit 2, APP_KEY_SCOPE_REQUIRED/HOST_KEY_REQUIRED exit 8; delivery.enabled (cli#907): each poll also reads the host's chat batches (key also needs attention:deliver) and types each only into an empty agent composer via orca_bin, then acks it; events delivery-delivered|reconciled|held|deferred|skipped|error (codes, ids, spans; never text or proof); a read APP_KEY_SCOPE_REQUIRED turns delivery off for the run (delivery-off event, exit unaffected); holds are UNREPORTED to the server until cli#940 (no server alarm); a message over 131,071 UTF-8 bytes fails delivery-error too-long-to-type (never truncated or acked, stays due) and, oldest first, blocks that worker's later chat until hadron-server#2121; tick/task wakes (cli#1178): each poll also reads dueWakes (no accepts, so never a STOP notice) and types each wake's TICK/TASK reasons as one own-prefixed line, recorded before acknowledgeWakes; a wake naming channels or a MENTION is typed once and by default held from ack (delivery.ack_mixed opts into deployed A′ as described below); a refused or unconfirmed send leaves no blocking record (the next pass sends fresh; our own unsubmitted line is Enter-submitted or cleared, never retyped); status --json delivery.workers[].unreported; per worker also lastAgeSeconds, heldSince (start of the current run of holds; only a delivered or reconciled event ends it; null when none) and heldOver30m (text row: "HELD OVER 30m: no delivery since <time>") and notRevisited (a held record whose newest event is older than three poll intervals: no newer local event, so the hold may be stale; the cause is not recorded: the server may list no wake for that worker, or the poller stopped or its reads failed; text: "NOT RE-EVALUATED") (cli#1178, cli#1322)
hadron team health sweep --config <host.yaml>   # code-only terminal health reporting (cli#1181); uses host key_file (0600) and the terminal-status@1 command JSON, no alarm/cadence/install or terminal typing; json {reports,refused}; exits 0/1/3/7/8/10 (usage 2), per-item refusals exit 0
hadron team health install --config <host.yaml> --interval <Nm> [--bin <abs hadron>]   # writes one user-level scheduler entry `hadron-team-health` running the sweep; see "Team health install" (cli#1374); exit 2 on no scheduler / orca unresolved / bad interval / different --config
hadron team health uninstall   # removes exactly what install wrote, also after a failed install
hadron team health status   # read-only; json {installed,scheduler,active,interval,binary,config,orca,files,lastRun{at,exitCode}}; lastRun unknown under cron
hadron team router poll | loop [--interval 60s] | status [--config <path>]   # the Orca attention router (cli#829), on YOUR login: types one nudge line into an idle, empty-prompt roster worker's Orca terminal; never reads/posts/marks chat. Config: --config, $HADRON_ROUTER_CONFIG, ~/.config/hadron/team-router/config.json, else the TS router's config (same state dir + writer lock). poll --json {at, ok, error, nudged, dropped, covered, held{name:reason}, adopted, liveness, livenessDropped}; a failed poll exits 1 with the token unchanged; no config exit 2; status reads local state only (never the token)
hadron user search [query] [--limit N] [--offset N] | set-roles <userRef> --role <r>... --yes | merge <source> --into <target> --yes
hadron profile set [--name <n>] [--handle <h>] [--email <e>]   # --email is accepted and REFUSED locally (exit 2, "Changing your email address is not supported here.") — the flag stays so scripts fail loudly (#844)
hadron server-info
hadron run trigger --app <ref> --entry <node-urn> [--as-self] [--agent <ref>] [--arg k=v]... [--ai-config <n>] [--budget-tokens N] [--budget-actions N] [--wait] | list [--app <ref> | --org <ref>] [--status <s>] | get <id> | cancel <id> --yes
hadron ticker list | get <id-or-URN> | create --name <n> --cadence-seconds <n> [--reasons read-team-channel,every-tick,live-check] | update <id-or-URN> [--reasons <list>] | delete <id-or-URN> --yes | subscribe <id-or-URN> --worker <id-or-URN> | unsubscribe <id-or-URN> --worker <id-or-URN> | apply-templates --worker <id-or-URN>
hadron schedule create --app <ref> --name <n> --cron '<expr>' [--tz <zone>] --entry <node-urn> [--as-self] [--policy <json>] [--ai-config <n>] [--arg k=v]... [--budget-tokens N] [--budget-actions N] | list --app <ref> | update <id> ... [--budget-tokens N|--clear-budget-tokens] [--budget-actions N|--clear-budget-actions] | rm <id> --yes
hadron automation list | approval (<name> | --run <run-id>) [--depth] | approve (<name> | --run <run-id>) [--yes] | decline (<name> | --run <run-id>) [--yes]   # cli#1071: an App owner reviews and approves schedules/webhooks without a row id or hash; name is schedule/<name>, webhook/<name> or a bare <name> that exactly one trigger has; --app on every subcommand
hadron webhook create --app <ref> --name <n> --entry <node-urn> [--as-self] [--policy <json>] [--args-schema <json>] [--ai-config <n>] [--budget-tokens N] [--budget-actions N] | rotate <id> --yes | list --app <ref> | rm <id> --yes
hadron ticket mint --org <ref> [--app <ref>] --action comm.outbound --count <n> [--note <why>] [--expires <iso>] | list --org <ref>
hadron billing prices [--provider <p>] [--model <m>] [--tier <n>] [--limit N] [--offset N] | prices get <id> | balance <org> | ledger <org> [--limit N] [--offset N]   # spec-046 metering READS (cli#215); <org> is an ID or hrn:org:<slug>. Money fields (inputMicroCentsPerMTok, outputMicroCentsPerMTok, balanceMicroCents, amountMicroCents) are base-10 integer STRINGS of micro-cents (1e-8 USD), signed on the ledger and unbounded, passed through byte for byte: parse with a bigint, never a float. Null markupBasisPoints = the server's default markup. prices get <id>: one row (the id is the ID column of the prices table, or "id" in prices --json), the same DTO as a prices row as one object; a null row exits 4 NotFound "not found or not visible" (the server does not say which); an empty id exits 2 with no request. prices and prices get: any signed-in caller; balance/ledger: org ADMIN, else FORBIDDEN exit 8. No --limit/--offset lists everything. No write commands (setting rates, granting credits) yet
hadron grant create --org <ref> --user <ref> --action <a>[,...] [--expires <iso>] | list [--org <ref>] [--user <ref>] | revoke <id> --yes
hadron calendar list --connection <ref> | events --connection <ref> [--calendar <id>] [--from <RFC3339>] [--to <RFC3339>] [--query <text>] [--top 1..100] | event get <event-id> --connection <ref> [--calendar <id>] | event create --connection <ref> --title <t> --from <t> --to <t> [--all-day] [--tz <iana>] [--attendee <email>]... [--description <t>] [--location <t>] [--calendar <id>] [--no-notify] [--idempotency-key <k>] | event update <event-id> --connection <ref> [same flags, all optional] [--clear-attendees] | event delete <event-id> --connection <ref> --yes | event respond <event-id> --connection <ref> --response accepted|declined|tentative | freebusy --connection <ref> --from <RFC3339> --to <RFC3339> [--schedule <id>]...
hadron drive list --connection <ref> [--folder <id>] [--query <text>] [--name-contains <text>] [--mime-type <type>] [--modified-after <RFC3339>] [--top 1..100] [--page-token <token>] | get <file-id> --connection <ref> | export <file-id> --connection <ref> [--format MARKDOWN|TEXT|HTML]
hadron email connections --org <id|urn> [--json]
hadron slack status [--org <id|urn>] [--limit 0..200] [--offset N] | get <connection-id>   # read-only Slack workspace connections; one page, stderr names the next --offset; tokens/lastError never requested; get on an invisible id exits 4
hadron connection connect <ms-exchange|gmail|google-cal|google-drive> --org <id|urn> [--no-browser] [--wait <duration>|--no-wait] [--json]
hadron connection grant create --connection <ref> --app <ref> --scopes <s>[,...] [--expires-at <iso>] | list [--connection <ref>] | revoke <grant-id> --yes
hadron mcp-server list [--org <ref>] | get <id> | tools <id> | create --org <ref> --slug <s> --name <n> --url <u> [--header 'Name: value']... [--allow <tool>]... [--disabled] | update <id> [--name <n>] [--url <u>] [--header ...]... [--clear-headers] [--allow <tool>]... [--clear-allow] [--enabled|--disabled] | delete <id> --yes
hadron ha list [--org <ref>] [--limit 1..200] [--offset N] | get <id> | ops <id>   # Home Assistant registry READS only (cli#231). list is ONE explicit page (default --limit 20, sent explicitly; the server default is 50), slug-ascending; --json is a bare array and, when more rows exist, stderr prints `showing N of TOTAL; next page: --offset M --limit L`. get/ops: null = absent OR not visible to you (one answer, exit 4; no existence proof). opAllowlist [] means EVERY op, not deny-all. runToolName/inputSchema are the provider's strings. A disabled instance's `ops` is refused by the server (HA_INSTANCE_DISABLED), message shown unchanged. The access token is write-only and never shown; no host/device is contacted.
hadron ha create --org <ref> --slug <s> --name <n> --url <u> [--token-file -|@path|path] [--allow-op <op>]... [--disabled]   # org ADMIN/OWNER (cli#231). The token is NEVER an argv flag: --token-file - reads stdin, @path/path a file; omitted, piped stdin is read (empty = exit 2, nothing sent); an explicitly empty --token-file= is exit 2, never a stdin fallback. No --allow-op = every catalog op; --disabled sends enabled:false. --json is the `ha get` shape (no token). Slug is permanent.
hadron ha update <id> [--name] [--url] [--token-file ...] [--allow-op <op>]... | [--clear-allow-ops] [--enabled|--disabled]   # only the named fields are sent; omitted = kept. No flags = exit 2, nothing sent. Without --token-file stdin is never read and the token stays. --clear-allow-ops sends [] = EVERY catalog op (not none; use --disabled to stop use). --allow-op/--clear-allow-ops and --enabled/--disabled are mutually exclusive. No slug flag.
hadron ha rm <id> [--yes]   # hard delete; TTY prompts, non-interactive needs --yes (else exit 2, nothing sent). --json {id, status:"deleted"}; false from the server is exit 1, never success. Nodes naming ha__<slug>__<op> then fail at run time; nothing is repaired.
  ha create/update/rm: server refusals keep their exit code and --json serverCode (not admin exit 8, missing or another org's instance exit 4 NOT_FOUND, invalid input exit 2). A lost response is exit 7 "outcome unknown": sent once, never retried; read back with `ha get`/`ha list` before retrying.
hadron secret create --name <n> --scope user|org [--owner <ref>] --kind generic|webfetch-auth [--value-file -|@file] | list --scope user|org [--owner <ref>] | rm <id> --yes
hadron config get <key> | set <key> <value> (an empty value removes the key; server then returns to its default) | list
hadron api <query-or-mutation>                       # raw GraphQL
hadron version
hadron completion <shell>
hadron agentic-usage                                 # prints this doc
```

Host mixed-wake acknowledgment (cli#1229): `host.yaml` `delivery.ack_mixed`
is a boolean, default false. Set true only after the operator verifies server
A′ (#2072, ad7f1c2) is deployed. It permits tick/task acknowledgment in a mixed
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
  `user merge`, `user set-roles`, `edge rm`, `app uninstall`, a real `replace` /
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
  is cleared so it is no longer garbage-collected. Adding `--data-key -`
  encrypts every node in the same transaction and lands the memory `private` and
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
  description. No settings, nested keys or AI configuration is requested.
  `hadron app key list <app-ref>` uses top-level appKeys and
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
  `asset list` REQUIRES `-m`: listing is memory-addressed server-side, there is
  no "every asset I can read" query, and the CLI deliberately does not fan out
  across memories to fake one (hadron-server#891). It pages to exhaustion by
  default; `--limit` fetches one explicit page. `asset get <ref>` mints a
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
  `asset public-link <ref> --on|--off` records the anonymous-download switch.
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
  `totalFindings` is the true count across every check BEFORE truncation — gate
  on that, never on `findings.length` — while `matchedFindings` counts what was
  listed after `--limit` and `--check`. `ok` is false whenever a check was
  SKIPPED even with zero findings (health can't be claimed for a check that
  didn't run); `skippedChecks` says which and why (stale-abstract is skipped on
  an encrypted memory — the validator doesn't decrypt). `--check <kind>` filters
  CLIENT-side, and the server caps `findings` before that filter runs, so
  `--check` requests the server maximum (1000) unless you pin `--limit`; if the
  result is still truncated the report says the filtered view may be missing
  matches. Exits 0 whatever the findings unless `--fail-on-findings` (then exit
  5 when any LISTED finding matches), because a check that fires on most of a
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
- `memory encrypt <memory> --data-key -` converts a plaintext memory to
  encrypted-at-rest: you provide the data key (read from stdin via `--data-key -`
  so it stays out of shell history) and the server rewrites all node content as
  ciphertext in one transaction. It is ONE-WAY — there is no decrypt command —
  so keep the key. Reads by authorized callers stay transparent afterward.
- `memory config get <memoryRef>` shows a memory's CONFIG: its node-role rules
  (hadron-server#1325), one per role or dotted sub-role, role-ascending.
  `memory config rule add|update|rm <memoryRef> <role>` changes them. **Only the
  memory's managers** can read or change a config (the owner of a personal or
  private memory, otherwise its user owner or an org ADMIN/OWNER); anyone else
  gets exit 4, EXACTLY as for a memory that does not exist. So exit 4 never
  means "no rules": a manager's memory with no rules is a success, `id: null`
  and `rules: []`.
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
    `{id, name, description, ownerType, ownerId, required, revision, rules[],
    createdAt, createdBy, updatedAt, updatedBy}` (`create`/`update` wrap it as
    `{template, warnings[]}`); each rule is a config rule without the
    config-only fields (no id, revision, locked or provenance), with the same
    URN/id/STATE references. `rm` prints `{id, name, revision, deleted}`.
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
  Generic/kind writes, import, replacement and restore display it as
  `warning: [<code>] <text>` on stderr in BOTH text and `--json` modes;
  stdout/JSON and the successful exit code stay unchanged. Unknown codes are
  displayed too; malformed entries and warnings for other response keys are
  ignored. Failed commands print no response advice. This is advisory, never a
  reason to retry a successful write or a promise that an include is retained.
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
    state, createdAt, expiresAt, endedAt, namedSigner, requestedBy
    {userId, appId, workerId, sessionId}, signature). A person completes it
    with their passkey on the server's signing page. It ends exactly once:
    COMPLETED, DECLINED, WITHDRAWN, EXPIRED or STALE (the title, abstract or
    content changed). `node signing-request get <id>` reads one (exit 4 when
    unknown or unreadable), `list <ref> [--state s] [--limit N]` lists a
    node's requests newest first ({nodeId, urn, loc, requests: []}), and
    `withdraw <id>` withdraws an open one you made. `node signatures <ref>`
    prints the server's re-verified status ({nodeId, urn, loc, revision,
    state, integrityProblems, signatures: []}); `state` is null with no
    signatures, and a passkey is shown by its label only. `--check` exits 5
    unless the state is CURRENT with no integrity problems. Refusals:
    NODE_NOT_FOUND, SIGNER_NOT_FOUND and SIGNING_REQUEST_NOT_FOUND exit 4;
    REVISION_MISMATCH, NODE_IS_PLACEHOLDER, ENCRYPTED_MEMORY_NOT_APPLICABLE and
    SIGNING_REQUEST_ENDED exit 5; COMMENT_NOT_SIGNABLE exits 2;
    SIGNING_REQUEST_NOT_REQUESTER exits 8. A server that does not offer
    signing refuses every one of these commands with exit 2, "this server
    does not offer node signing".
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
  `skill-unknown-host-key` WARNING, one row per key with `hosts: []` — it
  belongs to no host — never read as an alias, and never blocking a known host
  beside it, however malformed (cor:agt:030:06). Note
  what cannot be checked: with no prefix source, a name's PREFIX is
  unverifiable — `hadon-foo` lints clean. `--all` walks what the server lists for you — own-org,
  shared-with-you and other orgs' PUBLIC memories, every class; a per-user
  agent memory is never listed, so name it with `-m`. Errors exit 5;
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
  failing; any other refusal is still the run's error. **The class is the server's word** — the
  vocabulary is one place so it cannot drift between the CLI, MCP and the
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
  listed under `unreadable` with its errno, likewise never sent. A `SKILL.md` that is not a regular file (a FIFO, a device) is listed under `unreadable` WITHOUT being read, so it cannot block the walk.
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
  `skill export` is the ONLY writer of user-level skill files (#621;
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
  agent is never user-owned. **`app list` now requires exactly one of `--org`
  or `--owned-by-me`** — the bare command still refuses (exit 2), as it did
  when `--org` was simply required.
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
- `spec` manages product-spec nodes: a node is a spec when it carries the
  `spec` tag (or the governed spec role), and its loc is its citation. **Any
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
  legacy numbering and scaffolds a **tier-appropriate** skeleton — a `## Modules`/`## Features`
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
  `Node.name` to `<citation> — <text>` (pass the text only; a value starting with
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
  its `edgeId`) and nothing is written; `spec find` is semantic by default (`--match-exactly`
  forces literal regex matching); `spec list`/`spec find` run unscoped when no
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
  `spec register` is advisory/read-only (`--check` reports
  ledger drift, exit 5; the ledger is the legacy numbering, and specs at any
  other loc are NAMED in `outsideNumbering`, never dropped); `spec lint` is
  STRUCTURAL: since #708 it enforces no content rubric at any loc (no missing
  abstract, "what invalidates", `data.version`, scaffold-body or
  placeholder-contract finding), because the sections a spec needs depend on
  its type — each corpus's own authoring framework (its review checks and
  review tasks) checks those. It takes `--product`/`--module`/`--all`,
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
  abstract alone, such as a body-only edit or the one `spec supersede` writes,
  still succeeds. Distilling is the wrong remedy in all three — on a
  spec whose sentences are all on-subject, cutting one drops a contract, so the
  fix is a supersede-level split. `spec lint --unwired` (opt-in, #687) is
  the repair report for specs whose edges were never written: for each
  legacy-grammar citation it reports, as `unwired` info (never failing
  `--strict`), each edge legacy `spec new` plans that the spec lacks (the
  table-of-contents edge to its parent, the inheritance edge to its contract,
  when that target exists), and the message carries the exact `spec link`
  that adds it (`edge create` when the target is not a spec); lint writes
  nothing. `spec lint` also
  warns (rule `vector-index`) when the memory has no
  vector index so spec abstracts aren't embedded for semantic `find`
  (`--strict` promotes warnings to errors, exit 5). Two more warnings check
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
  tool-name drift; `spec supersede` retires a
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
  way. `spec mint [--dry-run] [--yes]` mints approved, unminted nodes in the corpus. It always checks first and prints the report
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
- `ai-config list` lists the masked AI configs *resolvable* in an App's chat
  context (App→Agent→Org→HadronServer, innermost wins, enabled-only) — never
  key material, only a preview. `ai-config create|update|rm` manage the
  underlying provider configs: `create` needs an owner (exactly one of
  `--app`/`--agent`/`--org`, ID or URN) plus `--name`/`--provider`/`--model`;
  the API key is a secret read via `--api-key -` (stdin) and never echoed back.
  App, Agent, and Organization configs require a stored key on current servers.
  `--file <path>` (or `--file -` for stdin) reads the whole config — key
  included — from a JSON object (keys mirror the flags: app/agent/org, name,
  provider, model, endpoint, apiKey, params, enabled), keeping the secret out of argv;
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
  result remains the full invitation for its authorized creator.
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
- `app member add --user <ref> --role <role>` / `remove --user <ref>` / `list`
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
  list, empty hash, unknown state — never an empty success), a server without these
  operations ("this server does not support automation approval", before any
  prompt), or a write that is not confirmed (outcome unknown: check with
  `automation approval`). `previewAutomation`/`reAdoptAutomation` stay unbound.
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
  For boot customisation use the worker source and compile workflow above.
  **`--signature` / `--trailer`** (cli#916 C4, `cor:agt:040:04`) replace the
  worker's raw signature and commit-trailer TEMPLATES (`{{worker.name}}`-style
  variables, rendered into the next briefing; nothing already delivered
  changes). Omitted keeps the stored template; `""` stores an empty one, which
  delivers nothing (D8); there is no reset — pass the default text to restore
  it. App managers only; the length limit is the server's (exit 2). From a
  bound worktree these edits carry the worktree session, and a refusal exits 8
  with the server's sentence plus a hedged note to retry from an unbound shell
  (the bound-session rule is provisional).
  `worker get` and every worker `--json` gain `signature` / `trailer`: a string
  (`""` = empty template) or `null` = WITHHELD (masked), never "empty".
  `worker retire` (requires `--yes`, idempotent) stops the worker and keeps
  its name reserved — PR trailers and chat history reference it — and there
  is no rename; `worker rm` is the ONE removal escape, hard-deleting a
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
  was not informed.
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
  exit 5 and has spent the claim; `RESUME_AMBIGUOUS` is exit 2 (name the
  worker). A resume refuses `--repo`, `--branch`, `--reports-to`, `-m` and
  `--force` (exit 2), refuses over a worktree still bound to a live session
  (exit 5) and outside a git worktree (exit 2), all BEFORE any claim is
  spent. It never ends the session it recovered: on a later failure, run
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
  with `session end --session <id>`. **`whoami --check` asks the server whether
  the bound session is still open (#484)**. Binding identity stays available
  locally for compaction recovery. The default now also makes a best-effort
  worker read for its CURRENT reporting relation; a failed, masked or unavailable
  read reports `reportingKnown: false` / `? (unavailable)`, preserving recovery
  without pretending the worker reports to nobody. Use it
  when the binding may have outlived what it describes: `session end --session
  <id>` ends a session from ANYWHERE and clears only the binding of the worktree
  it ran in, so every other worktree keeps describing a session that is gone.
  `--json` gains `checked`, `active`, `endedAt` and `autoExpiredAt`; `active` is
  **null** unless `checked` is true, because "not asked" is not "dead". It is
  `active`, not "live": the predicate is `endedAt IS NULL` and nothing more — the
  same one `session list` calls active — so it never means the worker is present.
  `--check` is honoured on the server-fallback path too, where the openness is
  already the server's word. `endedAt`
  answers WHETHER it ended and `autoExpiredAt` answers HOW — non-null only when
  the server reaped it rather than a person ending it. **`--check` says nothing
  about idleness**: since hadron-server#1114 a developer session has no
  inactivity deadline, so an open session undriven for months is correctly open,
  and `active` keeps its open/ended meaning even though the server now exposes
  per-session `isLive` for the `session start` binding guard. **Most of the
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
  another's AND the name is live; otherwise the server refuses it and the CLI
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
  (`cor:agt:020:10`), not this client's choice. Passing it on a session with no
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
  and an optional `--detail` JSON bag. `session log --model <m>` explicitly
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
  `--limit`. The Orca attention router polls `team attention`, not this command.
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
  **Team attention** (hadron-server#1353/#1384). `team attention [--since <token>]` is what a
  team-chat ROUTER polls: which of YOUR live workers have relevant unread chat,
  with no message bodies and no read-state change. It drains the server's
  bounded pages before printing any result; a partial scan yields no token
  and exits with an error. On an older server without the paged field, it
  uses the legacy query. `--json`:
  `{app, token, workers:[{worker, name, urn, live, channels:[{channel, name,
  unread, unreadMentions, firstUnreadSeq, lastSeq}]}]}` (`workers` is `[]` when
  idle). Pass the returned `token` as the next `--since` **only after every
  listed nudge was accepted**; on any failure keep the previous token — a retry
  may duplicate a nudge but cannot lose one. The CLI never stores the token. An
  empty `--since` is refused (exit 2 — usually an unset variable); `--since now`
  is refused by the server (exit 2): discarding a backlog is the explicit
  **`attention switchover preview`** (read-only; drains every preview page
  before printing the short-lived `proof`; reports each worker's first unread
  and through seqs) then **`attention switchover
  apply --proof <proof>`** (prompts on a TTY, `--yes` otherwise; atomic; a
  stale proof exits **5** — preview again). A token or proof the server did not
  sign for this operator+App exits 2; a token overtaken by a moved watermark
  exits 5 (poll again without `--since`). With no `--app` and no App context
  the App comes from the worktree binding, and a binding made against ANOTHER
  server is refused (exit 2): App ids are not unique across deployments. An
  explicit `--app` still works.
  **A bound worker's `chat read` now marks its messages read on the server**,
  which is what stops the router nudging it. It happens only
  AFTER the messages were printed, for exactly the reads that record the
  binding's watermark (unfiltered, contiguous, not `--before`, own App, and a
  binding that records this server — a `--limit` page included), through the
  highest seq shown: the read itself carries no session, so a read that fails
  partway marks nothing (a
  duplicate nudge, never a lost message). An older server that refuses the
  mark skips it silently; other mark failures produce a stderr note, never a
  failed read. For the
  reads that don't count, **`chat mark-read --through <seq> [--channel <ref>]`**
  advances the bound worker's cursor explicitly
  (`--channel` defaults to the App's team chat; an EMPTY `--channel` is refused,
  exit 2, rather than falling back to it). It is monotonic — a lower seq
  changes nothing and says so — and a seq past the Channel's head exits 2
  (`SEQ_BEYOND_WATERMARK`). It needs a binding that records this server (exit 2
  without one), and the server accepts only your own live session. `--json`: `{workerId, channelId,
  through, lastSeenSeq}`.
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
    `webhook rotate <id>` reissues the secret (old URL dies immediately; `--yes`);
    `webhook list` never shows the secret; `webhook rm <id>` (`--yes`).
  - `task run <task>` renders a task node's prompt by default; `--app <ref>`
    instead EXECUTES it server-side — minting a MANUAL run under that App (a real
    LLM run) and printing the run id (`--json`: `mode:"execute"`, `runId`). Follow
    it with `run get <id>`. It's the fourth trigger into the same kernel.
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
# Pages like revision list (default 20, no total, no upper bound; --limit 0 is
# an empty page; a full DEFAULT page says more may exist on stderr, an explicit
# --limit never does).
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
hadron comment create|add <target-ref> --body <text|->|--body-file <path> [-m <memory>] [--quote <text>] [--anchor-revision N]
hadron comment reply <comment-ref> --body <text|->|--body-file <path> [-m <memory>]
hadron comment edit <comment-ref> --expected-revision N [-m <memory>] [--body <text|->|--body-file <path>] [--quote <text>]
hadron comment delete <comment-ref> --expected-revision N [-m <memory>] [--yes]
hadron comment resolve <root-ref> --expected-revision N [-m <memory>] [--reopen]
hadron comment get <comment-ref> [-m <memory>]
hadron comment list <target-ref> [-m <memory>] [--state OPEN,RESOLVED] [--limit N] [--offset N]
```

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
(no output). It only lists and reads terminals: it never types, and it writes no data to the server (it reads
tickers, their subscribers and `dueWakes`, and never acknowledges a wake; a host key's `dueWakes` read leaves
an audit record on the server).

`hadron orca terminal status <worker|terminal-id> [--config <host.yaml>] [--json]` or `--all` (one JSON array, one entry per
active worker of the App, in the App's worker order). The terminal is the Orca terminal whose worktree's
session binding names the worker, or one named directly by id. Text mode prints one line per worker:
`Joshua idle`, `Joshua locked (menu_or_question)`; a `dark` line also carries the local detail.

```json
{"contract":"terminal-status@1","worker":"Joshua","terminal":"term_50a6…","status":"idle",
 "prompt":"empty","reason":null,"lastTurnAt":"2026-10-29T16:12:03Z","checkedAt":"2026-10-29T16:31:40Z"}
```

| key | values |
|-----|--------|
| `contract` | always `terminal-status@1`; a breaking change ships as `@2` |
| `worker` | the worker's name; `null` for a terminal bound to no worker |
| `terminal` | the Orca terminal handle; `null` when the worker has none or has several agent terminals |
| `status` | `busy` a turn is running · `idle` empty prompt, safe to push one line · `locked` cannot take work · `dark` broken |
| `prompt` | `empty` · `not_empty` · `menu` · `unknown`: the terminal-level fact, separate from `status` |
| `reason` | `null` unless `locked`/`dark`. `locked`: `menu_or_question` (a selection menu, permission dialog or pending question: never send), `unrecognized` (blocked, or a screen no rule matches: fail closed, never idle). `dark`: `no_turn_for_due_ticker`, `no_terminal`, `bare_shell`, `no_agent` |
| `lastTurnAt` | when the harness's screen shows its last finished turn (Claude Code, Codex adapters); `null` when not shown, never guessed. It does not influence `status` |
| `checkedAt` | when the status was produced, UTC |

Precedence: `locked`, then `busy`, then `dark`, then `idle`. **Push only when `prompt` is `empty` and
`status` is `idle` or `dark`.** `dark` means a ticker owed the worker a turn and none came: a tick of a
ticker the worker is subscribed to has been due and unanswered for at least the interval of **the worker's
fastest subscribed ticker** (each tick reason's own `tickDueAt` in `dueWakes`, that ticker's oldest unacknowledged tick; never `anchorAt`, which is the oldest reason of any kind, and never `Ticker.lastTickAt`).
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
(it must be absolute, even with delivery off: exit 2), else `orca` on PATH. The server is read with the
host's `host:operate` App key, read only from `key_file` (mode 0600 or tighter) and sent as the bearer on a
client that carries no session and never `HADRON_TOKEN` or the stored token. The key is never in argv, the
environment or a file descriptor of the Orca subprocess, nor in any output. The App is the file's: `--app`
with `--config` is exit 2 with nothing read; the `scope:` line reads `App <ref> (from --config <path>)`, a
terminal bound to another App is judged against the file's App and fails closed, and `--all` lists only the
App's active workers the file configures (name, URN or id, the delivery matcher). Exit 2: empty path, bad
file, relative `orca_bin`. Exit 3: no `key_file`, missing, empty or too-open key file, all before Orca is
listed. Without `--config` nothing changes. **Limits of a host key:** it cannot read tickers
(`APP_NOT_FOUND`, hadron-server#2931) and `dueWakes` needs `attention:deliver` as well (a refusal is
`APP_KEY_SCOPE_REQUIRED`), and the server records each key due-wake read. Either way an otherwise-idle worker
fails closed to `locked`/`unrecognized`, exit 0, so the dark rule cannot fire yet. Do not run the sweep against
production, or install it with `team health install`, until hadron-server#2931 is deployed.

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
failed operations instead of reissuing them. Only three host-local steps follow:
roster entry, exact-config reload confirmation from the current Go `team router
loop`, and Orca display name. Set `--timeout` (1s–30m, per operation/reload) to
allow the router's polling interval. A TS router has no reload receipt; upgrade
the loop before applying. Onboard never casts, binds a session, writes a register
row, or reads/writes the router's adopted token.

`--json` returns `appId`, `appRef`, `dryRun`, `workerId`, `worktree`, `operations` (type/args),
`queue` (known id/type/status), `verified`, `humanRemainder`, and nullable `error`.
After preflight a failure prints this partial record before nonzero exit;
timeouts leave submitted work running. Inspect `team host ops` after ambiguous
submission errors before retrying. Onboarding separately verifies the worktree
path/branch, a current worker session, an Orca agent terminal, and display
metadata. The human tail asks for the worker's pickup and mention
delivery to be checked. `team router loop` reloads validated configuration only
between complete polls, writing a fingerprint/time receipt in `config-loaded.json`.

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
get both report not found. The server's ticker list is currently unpaged: the
CLI reports that response without claiming an exhaustive census.

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
marks do not move. The receipt gains `push: [{recipient, state, reason?, acked}]` with
state `pushed` (acknowledged, cor:agt:080:16) | `typed` (the text reached the terminal but
the acknowledgement did not land, reason `ack-pending`: no mark moved, still due, never typed
again by the CLI, cor:agt:080:12) | `held` | `not-pushed` | `unconfirmed`. `acked` = batches
whose acknowledgement the server accepted. `not-pushed` means nothing typed: no batch came
back, which the server does not tell apart as off-host, unknown or nothing due. `unconfirmed`
means the pass failed before this recipient had an outcome, so typed-or-not is not
established (reason `push-failed`); `not-pushed` may also carry `no-batch`. Every state exits
0, so test the state, not the exit. A mention token addresses every host worker with that
server mention token (mentionTokenOf; two names may share one) and reports the worst outcome;
a pass error keeps the confirmed `pushed`/`typed`/`held` results. Text prints
`push <name>: <state>`. Known limit: an ack that never lands is handed out again and the host
poller may retype it (platform gap, cor:agt:080:12 doubt; not this command's defect).
A failed push never undoes the post or changes exit 0: do not repost. Without
`--push` the receipt has no `push` key.


### Team-channel checks (experiment #2138)

`team roster` adds a CHECK column alongside its existing work STATE. `team worker
get` shows the server's check status and last team-channel check. JSON carries
`checkStatus: {status, pendingReason, lastCheckedAt} | null`: null is unavailable,
not PENDING or OK. A known result with lastCheckedAt null means never checked.
NO_SESSION, PENDING (with the server's reason), OK and OVERDUE are rendered as
returned; the CLI does not infer them from local time, task counts or liveness.
This experiment supplies status only, with no overdue alarm.

`ticker create|update --reasons live-check` selects the server's live-check rule.
The server decides task expectation, freshness, pause and isolation exclusions;
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
hadron usage events [--memory <ref>] [--loc <loc> | --node <PK-or-URN>] [--type <type>] [--since <iso>] [--until <iso>] [--limit N] [--offset N]
```
The events command returns one newest-first page (default20, local1-200, offset>=0), with access
owned by the server. JSON is an array; empty is []. Filter flags are optional;
explicit empty is Usage2. Loc is an exact verbatim loc, never shape-classified
as nodeId. `--since`/`--until` (cli#1205) bound the window by creation time:
since is INCLUSIVE, until EXCLUSIVE (since <= createdAt < until; `team events
--since` is the opposite, exclusive); either may be omitted. Each is an ISO
date-time WITH `Z` or an explicit offset; a date alone, an offset-less time, an
empty value, or since >= until is Usage2 with nothing sent. Windowing does not
change which memories are readable. No total is available. Stderr labels the page offset and
limit; a full page says more may exist. No automatic drain or cost aggregation.
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
  Agent subscriptions. No subscribe/revoke/install/cleanup or implicit mine/agent
  scope is implemented by this read-only cut.


`hadron email connections --org REF` reads connection metadata through the
server's org-admin list gate. Explicit organization only; the server resolves
the id or URN. This does not authorize mailbox reads, reconnects or sends.
The existing unpaginated exchangeConnections list is used once; no page total
or client completeness guarantee is invented. Empty JSON is []. Each row has
{id,organizationId,userId,mailboxEmail,provider,displayName,syncEnabled,syncStatus,
lastSyncAt,createdAt,updatedAt}; displayName/lastSyncAt preserve null. No credentials
or lastError are selected. Text quotes external strings. Error mapping remains
unchanged, including unknown tool failures; no guessed reconnect remedy.


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
start). After the start the command polls the metadata-only EmailConnections read
(--wait, default 5m, never past the link's expiresAt; --no-wait skips it) for a
connection of that provider, owned by the caller, that was not there before the
start (the start carries no flow id, so another admin's connection or an
existing one never counts; two flows the same caller starts at once for one
provider cannot be told apart until hadron-server#2913, so run them one at a
time). With --json a browser that cannot be opened ends the
command at once with status "started" (no wait). Found: exit 0,
{provider,orgId,authorizeUrl,expiresAt,browserOpened,status:"connected",
connection:{id,provider,mailboxEmail,displayName}}. --no-wait: status "started",
connection null. Not found in time: exit 13 (WaitTimeout), "not found yet" - the
consent may still be completing, so check `hadron email connections --org REF`
before starting again. Errors never contain the URL or its nonce; nothing is
written to disk.


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


`hadron drive` is read-only with explicit --connection and the server's existing
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
format delegates the provider default. No local output-file or remote document
creation option. Server delegates format/binary/size guarantees to its tool;
file_too_large and unknown failures retain existing error mapping/refusal text,
with no retry/reconnect guess. No hidden discovery, writes or uploads.

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
and read errors, composition/roster as-of values, attention tokens and preview
expiry/proof, host-operation cursors, and stop-lift receipt ids. JSON retains
the original values. Free-form task details, task trees, kept-worktree
diagnostics and router-local rows are outside this metadata policy.

A displayed value that was replaced or shortened is never reused in a
copyable command or flag suggestion. Read the original token/cursor/proof
from --json; for an unmatched installed persona role, use
`hadron app agent list <app> --json`. Unchanged POSIX command arguments are
shell-quoted independently. Windows output gives argument guidance rather
than a shell command. None of this changes DTOs or the arguments sent to
the server.

## Recorded served boot prompts (#916 C3b)

`hadron team worker served-prompt <worker-id-or-qualified-URN>` and `hadron run served-prompt <run-ref>` read the recorded revision/hash/variables and scalar-only provenance. `--limit 0..200` (default 50), `--offset N`, or `--all` (all provenance pages, buffered before output). Explicit `--offset` excludes `--all`; `--all --limit 0` is refused. JSON is `{servedPrompt: record|null}`; null is unavailable (unrecorded, unreadable or serving off), never proof of which. No current prompt/source text is fetched. Pruned revisions remain recorded. Recorded variables may contain sensitive values. Ordinary worker/run get remains unchanged.

`hadron team session served-prompts <session-id>` reads one explicit session’s history in server order (newest served time, then ordinal). Find ids with `team session list`; no ambient binding/App selects the session. `--limit 0..200` (default 50) and `--offset N` page records; `--limit 0` counts only. `--provenance-limit 0..200` (default 50) and `--provenance-offset N` independently page each record’s provenance. JSON is `{servedPrompts: {items: [], total, limit, offset}}`, or `{servedPrompts: null}` for a missing/unreadable session. Empty records can also mean concealment or serving off. Actual provider errors retain their mapped exit code. No source bodies are read; variables may contain sensitive values. For chatbot chat records, use `chat served-prompts <chat-id>` (below); this is separate from team-chat Channels.

`hadron team session outcome <session-id> <0..1> [--ref <text>] [--meta <json>]` (cli#1351, server#2896) records a session's outcome through `recordSessionOutcome`: a number from 0 to 1, an optional opaque `--ref` and optional `--meta` JSON. It works on any session the caller owns, not only the bound one. `--ref`/`--meta` are sent only when given, so leaving one out keeps the stored value; `--ref ""` stores an empty reference and `--meta null` clears the metadata. A negative outcome reads as a flag: pass it after `--` (`outcome <id> -- -0.1`), otherwise exit 2 with nothing sent. A malformed `--meta` or an outcome that is not a finite number exits 2 with nothing sent. The range, the owner gate (the attributed person, or an App key for its own App's person-less session; cor:dmo:070:05, cor:acl:010:02) and the MCP-only OAuth refusal are the server's: out of range or an unknown session exits 2 (`BAD_USER_INPUT`), not the owner exits 8 (`FORBIDDEN`), an MCP-only OAuth credential exits 3. `--json` errors keep `serverCode`. `--json` success is `{sessionId, outcome, outcomeRef, outcomeMeta}` (null when unset). Recording an outcome does not revive a lapsed session.

hadron node provenance <compiled-node-ref> --revision <n> [--limit 0..100] [--offset N | --all] [-m <memory>]

## Compiled prompt provenance (916 C1b)

`node provenance` reads recorded sources for exactly the required positive
compiled revision. It never substitutes the latest include edges or fetches
source bodies. JSON is `{nodeId,revNo,contentHash,version,pruned,items,total,
hasMore,limit,offset}`; occurrences preserve recorded order/depth/parent,
kind, source nodeId/revNo/hash, replacesNodeId, nullable position, effective
selection `{rev,approved,signed}` and nullable `pruned`. Source pruned:null is
not visible, never a guessed retained/pruned state. Recorded ids/hashes/settings
are admitted by the compiled node's existing read/history gate.

Default one offset page, limit20/cap100; limit0 counts only. --all buffers every
page and excludes --offset and counts-only. Errors, inconsistent counts/identity
or compiled pruning state, nonprogress or duplicate occurrence order fail the
whole all-page read with no partial payload. Independent pages are not a shared
snapshot; source availability may change between them. Null provider result is
exit4 (missing/deleted/noncompiled/unreadable concealed together); server
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

- `hadron agent compile <agent> [--no-set-pointer]`
- `hadron app agent compile <app> <agent>` (always sets its pointer)
- `hadron team worker compile <worker> [--no-set-pointer]`

GraphQL-only manager operations, acting as yourself: none sends the bound
worktree's worker-session header. The server selects the source and output
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
check the compiled node with `node get` before compiling again. No automatic
retry is made, including after a conflict.

hadron team worker compile <worker> [--no-set-pointer]

## Opt-in node source-lint warnings (#916 C3c)

`node get|add|update|approve|move|clone|merge|restore --lint-warnings` adds a separate current advisory read, paged completely. JSON omits `lintWarnings` by default, returns null when the requested warning read is unavailable (including older/denied providers), or `{items,total,nextCursor:null}` when complete. Human warning text is terminal-safe; JSON preserves server text/nullable fields. A failed warning read never retries or changes a successful mutation; it reports no conclusion, not zero warnings. This is not a snapshot of the authored revision. Excludes actual --body-only/--dry-run. Advisory reads share a five-second budget across the command, including every batch-get node; timeout leaves the main receipt successful with warning state unavailable. Main response-extension warnings are unchanged; this flag controls only the extra source-lint read.

## Skill include lint (#916 C5)

`skill lint` also asks the S11d provider for `skill-include-broken` warnings, using LINT plans for the explicit selected memories and filtering exact selected node IDs. Warnings preserve the provider message and remain exit0 (`--strict` promotes to5). Literal notice text alone is never interpreted as a broken include. A failed/unavailable provider plan returns an error, not a clean lint. Existing local rules and selectors remain, no rendered body is fetched and no file is written. Skill export continues to use the provider-resolved body.

hadron team session served-prompts <session-id> [--limit N] [--offset N] [--provenance-limit N] [--provenance-offset N]
hadron team session outcome <session-id> <0..1> [--ref <text>] [--meta <json>]
hadron run served-prompt <run-ref> [--limit N] [--offset N | --all]
hadron team worker served-prompt <worker-id-or-qualified-URN> [--limit N] [--offset N | --all]

## Retired role/preview name probe (#916 C6)

Role reads and cast previews no longer request the retired `hasNamePlaceholder` probe or infer nameless briefings from it. Existing JSON keeps that key as null for compatibility. Other role fields, preview prompt and `reserved:false` remain unchanged. This does not remove legacy platform-variable handling or alter the prompt a bind/run is served.

# Editing a task (cli#1137)
`hadron team task update <task>` edits a task after creation through one
`updateAgentTask` mutation; the task keeps its id and handle. Only named flags
are sent (the server reads the input as a strict patch): omitted means
unchanged, and with no flag at all the command exits 2 locally and sends
nothing. `--title` (cannot be emptied; the server refuses an empty one and its
refusal is printed), `--description` / `--clear-description`, repeatable `--tag`
(replaces the WHOLE list) / `--clear-tags`, `--require-role` / `--family-in` /
`--family-not-in` / repeatable `--exclude-worker` (any of them replaces the
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
assignee may edit properties alone, and combined with any other field the whole
edit is refused TASK_NOT_OWNER (8). Needs a server at or after hadron-server
e09a1653 (the #2420 contract).

# Task priority and dependencies (cli#1135)
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
hasMore separately. Text list displays priority, blocked and both summaries;
text get displays the bounded pages. No local priority sort or dependency policy.
Refusals preserve the server code/message and mapped exit code, print no task,
and never retry the mutation.

hadron team task update <task> [--title <t>] [--description <d> | --clear-description] [--tag <t>… | --clear-tags] [--require-role <r>] [--family-in <f,…> | --family-not-in <f,…>] [--exclude-worker <w>…] [--clear-requirements] [--start-by <rfc3339> | --clear-start-by] [--properties <json|@file> | --clear-properties] [--close-session | --no-close-session]
hadron team task set-priority <task> <priority> | add-blocker|remove-blocker <task> <blocker-task> | dependencies <task> [--limit N] [--offset N]

Task priority/blocker fields require server #2726 (cf9bcfece) or later on every
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
team events --alarms names a task-less alarm's worker and known reason
(overdue or wake-unacked); other evidence stays in --json and may be withheld.
team alarm ack <eventId> acknowledges escalation; it does not end the episode.
A dark episode ends when checks recover and no wake is unacknowledged past
the window. These commands do not activate delivery or calculate check state.

# `team task list` filters (cli#1250): `--tag-any`/`--tag-all` (repeatable; a comma is part of a tag), `--source` (exact), `--overdue`/`--overdue=false` (tri-state; unset sends nothing; backlog is never overdue), and inclusive `--created-/--started-/--finished-after|before` bounds taking RFC 3339 with Z or an offset (a date-only or offset-less value is exit 2, nothing sent; a task with no start/finish time never matches a range on it). Filters AND together; none combine with `--team`. With none set the request and `--json` output are unchanged.

# Chatbot served-prompt history (cli#1280, server#2865)
hadron chat served-prompts <chat-id> [--limit N] [--offset N] [--provenance-limit N] [--provenance-offset N]
# Explicit chatbot chat id from the application, NOT a team-chat Channel id or node URN.
# Record-only chatServedPrompts query; no myChats scan, conversation, source bodies or live edges.
# No chat config, ambient App or worktree session. Read as whoever manages the first-record App;
# only that App's records are visible. No-App chats: platform admin, never impersonation, per current server rule.
# Server order (newest served time, then ordinal), one page: --limit 0..200 default50, --offset >=0.
# Each record has an independent provenance page (--provenance-limit/offset, same bounds); 0 counts only.
# --json {servedPrompts:{items,total,limit,offset}}, items[] when empty; missing/denied/no-record/serving-off
# all conceal as an empty page, exit0, never a manufactured refusal. Real provider errors retain mapped exits.
# Recorded variables may be sensitive. Pages are separate requests, not a snapshot. Requires server6b3f1e15.


### Team health sweep (cli#1181)

`hadron team health sweep --config <host.yaml>` runs on the registered Orca host.
It consumes only `hadron orca terminal status --all --json --config <host.yaml>`
(the separately gated C1 host-config dependency), without importing or duplicating
its classifier. That command uses delivery.orca_bin when set, else PATH. Both
commands read only the host's 0600 key_file; no key value in argv, env or logs.

The sweep sends one setHostWorkerTerminals call with status, prompt, closed reason,
lastTurnAt and checkedAt for each registered worker. It never sends detail, screen
text or a terminal handle, never types and never skips paused workers. Missing or
malformed status reports, an unknown contract version, or more than 200 workers
refuse before writing. An empty registered host has no report to write.

The JSON receipt is {reports:[{worker,status,prompt,reason,lastTurnAt,checkedAt}],
refused:[{worker,code}]}. Text is one safe Name/status/reason line per worker,
with an explicit refusal suffix for WORKER_NOT_FOUND or TERMINAL_REPORT_STALE.
No retries. A successful write exits 0 even with per-worker refusals or no
coordinator; no alarm/noCoordinator field is selected or used. Failed write or
malformed status exits 1; rejected key 3, server unreachable 7, forbidden 8, Orca
unreachable 10. No new exit 9. CLI input errors retain usage 2. Scheduling and
recovery are separate commands/slices, not installed by a sweep.

A missing/non-live host registration is exit 8. Unresolved or ambiguous registered
workers, or a missing status row for any registered worker, refuse the whole
batch before writing (exit 1). Other hosts' status rows are ignored. JSON reports
are the sent reports, including any refused by the server; read refused too.

### Team health install (cli#1374)

`hadron team health install --config <host.yaml> --interval <Nm>` writes one fixed
per-user entry that runs `<abs hadron> team health sweep --config <abs host.yaml>`:
a systemd user timer (crontab line where user units are unavailable) or a launchd
agent. The entry carries the installing shell's PATH; install refuses (exit 2) when
neither delivery.orca_bin nor PATH resolves `orca`. Installing twice changes nothing,
a changed interval updates the entry, a different --config refuses until
`uninstall`, which removes exactly what install wrote, also after a failed install.
`status` is read-only (exit 0 even when not installed) and reads the last run from
the scheduler; under cron it prints `unknown`. The host key never enters a generated
file, argv or env. Do not install against production until hadron-server#2931 is deployed.

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
