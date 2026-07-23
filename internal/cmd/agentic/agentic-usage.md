# hadron CLI — agentic usage

`hadron` is the command-line interface to the Hadron AI-memory
platform. This document is the single reference an agent needs to
drive the CLI. Read it once per session; everything below is stable,
documented contract.

## Setup and authentication

```
hadron auth status            # am I signed in? exit 0 yes / 3 no
hadron auth login             # interactive browser OAuth (human only)
echo $TOKEN | hadron auth login --with-token   # store a PAT
HADRON_TOKEN=hdr_user_...     # env var, overrides stored tokens (CI)
hadron auth token create      # mint a PAT (after login); ls | revoke <id> to manage
echo $TOKEN | hadron auth token validate   # check a PAT: exit 0 valid / 3 rejected
```

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

- Every command supports `--json`. JSON goes to stdout; progress and
  errors go to stderr. Without `--json`, output is plain aligned text.
- With `--json`, errors are emitted on stderr as
  `{"error":{"code":<exit-code>,"message":"..."}}`.
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
| 5    | conflict (e.g. duplicate install) |
| 6    | cancelled / timed out waiting for the user |

A **partial write** exits non-zero (generic failure, 1): a command that creates
the primary entity but cannot wire one or more of its edges — `node import
--with-edges` (see `unwiredEdges`), `spec new`, `spec extract`, and `spec
supersede` (see each edge's `status`) — reports the detail on stdout/stderr and
still exits 1, so a
caller branching on the exit code never reads a partial success as complete. The
node/spec exists but is under-linked; fix the target(s) and wire the edge(s).

## Command surface (v1)

```
hadron auth login | logout | whoami | status | token create|ls|validate|revoke <id>
hadron memory ls | get <id-or-urn> | set [<id-or-urn>] [--max-rev-count <n>] [--schema <json> | --schema-file <path>] [--app <ref> --agent <ref>] | attach <memory> --app <ref> --agent <ref> | set-active <id-or-urn> | rm <id-or-urn> | clone <id-or-urn> --target-urn <org::slug> | extract <parentRef> <targetUrn> [--move] | export <id-or-urn> [--out <dir>] | member ls|add|set-role|rm <memory> --user <id> [--role <r>] | share ls|create|set-role|revoke <memory> --grantee <id> [--role <r>] | subscription ls|create|set-role|rm <memory> --org <id> [--role <r>] | encrypt <memory> --data-key -
hadron node ls [-m <memory>] [--prefix <loc>] [--type <t>] [--object-type <t>] [--tag <t>]... [--where <json>] [--sort-property <json>] [--sort-seq asc|desc] [--seq-gt N] | get <urn> | add [--type <t>] [--object-type <t>] [--data <json>|--data-file <path>] [--properties <json>|--properties-file <path>] | update <urn> [--type <t>] [--object-type <t>|""] [--data <json>|--data-file <path>|--data-merge <json>|--data-merge-file <path>] [--properties <json>|--properties-file <path>] | move <urn> (--to-urn <urn> | --to-memory <memory>) | clone <urn> (--to-urn <urn> | --to-memory <memory>) | merge <urn> --into <urn> [--field <f>]... [--delete-source] --yes | rm <urn> [--hard] [--recursive|-r] | export <urn> [-o <file>] [--format md|json|pdf] | import <file|-|--url <u>> [-m <memory>] [--with-edges] [--task <ref> [--task-args <json>] [--app <ref>]] | revision list <node-ref> [-m <memory>] [--limit N] | revision get <revision-id> | revision restore <revision-id> [--truncate [--yes]] | revision label <revision-id> --label <text> | revision delete <revision-id> [--yes] | revision clear <node-ref> [-m <memory>] [--yes]
hadron object create -m <memory> --type <t> --fields <json>|--fields-file <path> [--key <k>] [--name <n>] | get <ref> | update <ref> --fields <json>|--fields-file <path> [--reason <r>] | delete <ref> [--hard] --yes | find -m <memory> --type <t> [--match <json>] [--where <json>] [--sort <json>] [--limit N] [--offset N]
hadron task run <task-urn>|<loc> -m <memory> [--arg k=v]... [--app <ref> [--as-self]]
hadron chat read [--since <seq>] [--node <urn> | -m <memory> --messages-loc <prefix>] | post (--body <text|-> | --body-file <path>) [--node <urn>] [--reply-to <loc>] [--handle <h>] [--identity <i>] [--role <r>]
hadron search <query> [-m <memory>]... [--mode hybrid|keyword|vector|regex] [--prefix <loc>] [--type <type>] [--object-type <t>] [--tag <t>]... [--where <json>] [--sort-property <json>] [--limit N] [--offset N] [-l|--long] [--json]
hadron replace text <old> <new> --field <f> (--node <urn> | -m <memory>) [--prefix <loc>] [--regex] [-i] [--dry-run] [--yes] [--max-nodes N]
hadron edge ls <node-urn> | add | update <edge-id> | rm <edge-id>
hadron spec ls [-m <memory>] | get <citation>|--prefix <prefix> | describe | use [<memory>] | register [--check] | find <query> [--match-exactly] | grep <pattern> [--regex] [-i] [--field content|abstract] [--prefix <loc>] | replace <pattern> <replacement> [--regex] [--word-boundary=false] [--field content|abstract] [--dry-run] [--yes] [--max-specs N] | new ... | edit <citation> | extract <citation> --to-feature <fff> | link <from> <to> | lint [<citation>] | check-tools [--prefix <loc>] | supersede <citation> | import spec-kit|code
hadron app ls --org <org> | install | uninstall <id> | use <urn>
hadron ai-config ls [--app <id>] [--agent <id>] | create (--app|--agent|--org <id>) --name <n> --provider <p> --model <m> [--api-key -] [--file <path>] | update <id> ... | rm <id>
hadron org ls [--mine] | create --name <n> --urn <urn> | get <id> | update <id> | rm <id> | member ls|add|set-role|rm <org-id> --user <id> [--role <r>] | invite create <email> --org <id> --role <r> | invite accept <slug> | invite show <slug>
hadron agent ls [--org <id>] [--type ASSISTANT|CHATBOT] [--visibility ORGANIZATION|PERSONAL|PUBLIC] | ls --public [--type <t>] [--limit N] [--offset N] | get <ref> | create --name <n> [--org <id>] [--type <t>] [--visibility <v>] [--description <d>] [--system-prompt <p>] [--system-memory <id>] [--surface <s>]… | update <id> [<field flags>] | rm <id> --yes
hadron user search <query> [--limit N] [--offset N] | merge <source> --into <target> --yes
hadron profile set [--name <n>] [--email <e>] [--handle <h>]
hadron run trigger --app <ref> --entry <node-urn> [--as-self] [--arg k=v]... [--ai-config <n>] [--wait] | ls [--app <ref> | --org <ref>] [--status <s>] | get <id> | cancel <id> --yes
hadron schedule create --app <ref> --name <n> --cron '<expr>' [--tz <zone>] --entry <node-urn> [--as-self] [--policy <json>] [--ai-config <n>] [--arg k=v]... | ls --app <ref> | update <id> ... | rm <id> --yes
hadron webhook create --app <ref> --name <n> --entry <node-urn> [--as-self] [--policy <json>] [--args-schema <json>] [--ai-config <n>] | rotate <id> --yes | ls --app <ref> | rm <id> --yes
hadron ticket mint --org <ref> [--app <id>] --action comm.outbound --count <n> [--note <why>] [--expires <iso>] | ls --org <ref>
hadron grant create --org <ref> --user <ref> --action <a>[,...] [--expires <iso>] | ls [--org <ref>] [--user <ref>] | revoke <id> --yes
hadron connection grant create --connection <ref> --app <ref> --scopes <s>[,...] [--expires-at <iso>] | ls [--connection <ref>] | revoke <grant-id> --yes
hadron mcp-server ls [--org <ref>] | get <id> | tools <id> | create --org <ref> --slug <s> --name <n> --url <u> [--header 'Name: value']... [--allow <tool>]... [--disabled] | update <id> [--name <n>] [--url <u>] [--header ...]... [--clear-headers] [--allow <tool>]... [--clear-allow] [--enabled|--disabled] | delete <id> --yes
hadron secret create --name <n> --scope user|org|app|memory [--owner <ref>] --kind generic|webfetch-auth [--value-file -|@file] | ls --scope <s> [--owner <ref>] | rm <id> --yes
hadron config get | set | list
hadron api <query-or-mutation>                       # raw GraphQL
hadron version
hadron completion <shell>
hadron agentic-usage                                 # prints this doc
```

Conventions:

- A new URN slug/path you supply — `org create/update --urn`, `app install
  --urn`, `node add --loc`, `agent update --urn`, `memory set --slug` — is
  validated client-side before the call: slug atoms must be 1–64 chars of
  `[A-Za-z0-9._-]`, starting and ending alphanumeric (so `--urn "Flow Lab"` is
  rejected, exit 2). `agent update --urn` also accepts the spec-047 user-author
  namespace form `@handle:slug`; node locs still reject `@`. This mirrors the
  server grammar; a bad slug fails fast with a usage error instead of a
  round-trip.
- Memory references accept the memory id, the full `hrn:memory:<org>::<slug>`
  URN, or the short `<org>::<slug>` / `<org>:<slug>` forms (all resolve to the
  same memory) across `memory get|set|attach|rm|member|share|export`.
- Node references are fully-qualified URNs
  `<org>::<memory>::<loc>` (double-colon between segments — e.g.
  `hadronmemory.com::dev::start-here`), optionally `hrn:node:`-prefixed (legacy
  `urn:node:` also accepted). Single-colon `<org>:<memory>:<loc>` is **not** a
  valid full URN — a loc itself contains single colons
  (`services:secureid:user-reporting`), so it's ambiguous. A bare loc is rejected
  (exit 2) *unless* you pass `-m/--memory <org::memory>` (single-colon
  `<org>:<memory>` also accepted) to name the memory — then
  `node get|update|move|clone|rm|export` and `edge add|ls` take a bare `<loc>`
  (e.g. `node get start-here -m hadronmemory.com::dev`; `edge add -m a::m --from
  x --to y …` applies the memory to both endpoints). For `node move|clone`, `-m`
  scopes only the source `<loc>`; the destination is always the explicit
  `--to-urn`/`--to-memory`.
  Without `-m`, the URN must name the memory — the same loc can exist in
  several memories.
- Edges are directed, first-class entities (spec 037): each carries an
  optional `name` (the relationship — was `label`) and a `loc` that is its
  identity (`hrn:edge:<org>::<memory>::<loc>`). `edge add --from <node> --to
  <node> --name <rel>` creates one (plus optional `--loc`/`--description`/
  `--runnable`); `edge update`/`edge rm` address it by its edge ID (shown by
  `edge ls` and in `node get --json`). A nameless edge prints its loc instead.
  Cross-memory edges are allowed.
- Destructive / bulk-write commands (`memory rm`, `node rm`, `node merge`,
  `user merge`, `edge rm`, `app uninstall`, a real `replace` / `spec replace`, and
  `memory encrypt`) prompt on a terminal and REQUIRE `--yes` when run
  non-interactively (agents must always pass `--yes`, or `--dry-run` to preview a
  `replace`). Without it they exit 2.
- `memory encrypt <memory> --data-key -` converts a plaintext memory to
  encrypted-at-rest: you provide the data key (read from stdin via `--data-key -`
  so it stays out of shell history) and the server rewrites all node content as
  ciphertext in one transaction. It is ONE-WAY — there is no decrypt command —
  so keep the key. Reads by authorized callers stay transparent afterward.
- `memory set` creates when called without a positional argument
  and updates when given one. Free-standing create requires `--org` and
  `--name`. App-scoped create requires `--app <ref> --agent <ref> --class
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
- `memory attach <memory> --app <ref> --agent <ref>` binds an existing
  free-standing personal/private memory to that App/installed Agent. The
  memory must be caller-owned and keeps its URN, class, and owner; server typed
  errors report already-scoped, cross-org, membership, and install failures.
- `node add` fails if the loc already exists; `node update` modifies
  an existing node and preserves unset fields. Content comes from
  `--content "<text>"`, `--content -` (stdin), or `--content-file`;
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
  `--to-urn <org>::<memory>::<loc>` (a full destination URN — new memory and/or
  loc) or `--to-memory <org::memory>` (keep the loc, change the memory). `move`
  keeps the node's id so its edges stay valid; `clone` returns a **new** node
  (fresh id) and copies only the outgoing edges that resolve at the destination
  (incoming edges are never copied). Both fail loudly if a live node already
  occupies the destination loc. The confirmation identifies the node by its
  destination URN (carried in `--json` as `urn`).
- `node merge <source> --into <target>` folds the source node into the target
  (the survivor) and returns the target. Name each by URN or a bare `<loc>` with
  `-m` (which scopes both). By default every mergeable field folds in; restrict
  with repeated `--field`: `CONTENT`/`ABSTRACT`/`DESCRIPTION` concatenate
  (target first), `TAGS` unions, `DATA`/`PROPERTIES` shallow-merge (target wins
  on key collisions), `EDGES` re-points the source's edges onto the target. The
  source stays in place unless `--delete-source` hard-removes it after the merge.
  Merging mutates the target, so it's gated — pass `--yes` non-interactively.
- `user merge <source> --into <target>` globally consolidates a duplicate source
  user into the surviving target and returns the target. `<source>` is
  soft-deleted; `--into <target>` survives. Each ref is a user id, bare handle, or
  `hrn:user:<handle>` URN, passed through verbatim (the server resolves and
  authorizes — platform ADMIN/OWNER, or an org ADMIN/OWNER over both members). No
  server dry-run; the confirmation is the last local safety boundary, so it's gated
  — pass `--yes` non-interactively.
- `isRunnable` gates whether `hadron task run` will execute a node. Both
  `node add` and `node update` take `--runnable` to set it; on `update` it's
  tri-state — `--runnable` sets true, `--runnable=false` clears it, omitting it
  preserves the current value. `node get` shows `runnable:` in the text view,
  and both `node get` and `node ls` surface `isRunnable` in `--json` (`ls`
  also shows a `RUN` column with a ✓ for runnable nodes). `node ls --runnable`
  filters server-side to runnable nodes (`--runnable=false` to the explicitly
  non-runnable; omit for all) — the listing counterpart to `hadron task run`'s
  gate.
- `chat` is the low-friction surface for a **team chat** — a shared memory where
  several agents and humans coordinate, each message a `message` node whose
  payload is in `data`, ordered by a server-assigned `seq` (see the "Set up an
  agent team chat" how-to). The chat is named by the `--node <urn>` of the node
  whose direct children are the messages — one copyable URN packing the memory
  and the message location (or the two-field `-m <memory> --messages-loc <prefix>`
  the push channel uses). It saves an agent the per-turn plumbing: `chat read
  [--since <seq>]` returns the new messages in ONE call as a compact transcript
  (`--json`: `{messages:[{seq,loc,author,identity,role,timestamp,body}],
  nextSince}`) — pass `nextSince` back as `--since` next turn. `chat post`
  (`--body <text|->` inline or over stdin, or `--body-file <path>` for a composed
  multi-line message) builds the colon-safe timestamped loc, assembles the `data`
  (author/body/timestamp, parsed `@mentions`, and identity/role when set), writes
  the `message` node (materializing the parent so the chat is a copyable node),
  and with `--reply-to <loc>` adds the reply edge — all in one call. The chat
  coordinates and this agent's `handle`/`identity`/`role` resolve from a flag,
  then `HADRON_CHAT_*`, then the project-local `.hadron/config.json` (the same
  file the push channel reads — top-level `handle`, `chat.node` or
  `chat.memory`+`chat.messagesLoc`, `chat.identity`/`chat.role`), so a configured
  agent's whole turn is `chat read --since <seq>` / `chat post --body "…"`.
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
    without mutating. The server recomputes `contentHash`/`abstractOriginHash`,
    so a clean export→import round-trips losslessly. Landing on an EXISTING node
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
    adds template args and `--app <ref>` names the App (default: your active
    App); both require `--task`.
  - RECURSIVE (`-r <dir>`) maps a local DIRECTORY TREE into the memory graph:
    each directory becomes a branch node, each text file a leaf node (loc =
    slugified path without the extension; name = filename), and the hierarchy
    becomes parent→child `contains` edges (materialized inline, bottom-up, no
    extra round-trips). A directory's `README.md`/`index.md` folds into that
    directory's node instead of a separate child. Binary files are skipped and
    listed under `skipped[]`; loc collisions are suffixed (`setup`, `setup-2`)
    and listed under `collisions[]`. Import is create-only: an existing loc is an
    error unless `--on-conflict skip` (which leaves it in place and lists it under
    `existing[]`). `--under <loc>` roots the tree under a prefix; `--include`/
    `--exclude` globs, `--hidden` (dotfiles; `.git` always skipped), and
    `--max-file-size` filter the walk; `--dry-run` prints the plan without
    writing. `--json` is `{mode:"tree", root, created[], existing[], skipped[],
    collisions[], edgesWired, nodesCreated}`.
- `memory clone <id-or-urn> --target-urn <org::slug>` deep-copies a memory
  (nodes, edges, pending edges) into a new memory named by `--target-urn`
  (a fully-qualified "org::slug" URN) and rewrites references to the source
  memory's URN inside node content and abstracts. The target org MAY differ
  from the source's, cloning into another org — you must be a non-reader
  member of that target org. Version history, shares/subscriptions, assets,
  and git-sync config are NOT copied. Encrypted memories and agent system /
  app memories cannot be cloned.
- `memory extract <parentRef> <targetUrn> [--move]` extracts a parent node and
  its loc-subtree into a NEW memory named by `<targetUrn>` (a fully-qualified
  "org::slug" URN, org MAY differ), making the parent the memory's root — locs
  are rebased (`findings:auth`→`<slug>`, `findings:auth:oauth`→`<slug>:oauth`).
  `<parentRef>` is a node id or fully-qualified `<org>::<memory>::<loc>` URN;
  pass `-m <org::memory>` with a bare loc instead. Default COPIES (source
  intact); `--move` relocates it, soft-deleting the source subtree (needs
  source write access, cannot target the source root). The new memory preserves
  the source's class. v1 limitation: node content is copied verbatim, so URN
  references among the moved nodes break (slug + locs change); unresolved
  pending edges in the subtree are dropped.
- `memory export <id-or-urn> [--out <dir>]` writes every node to a local
  directory (`--out` defaults to `.`, the current directory) as frontmatter
  markdown (`<out>/<loc>.md`, one self-contained file per node, colons in the
  loc become path segments) — the same layout the server's git sync produces,
  but on disk and without a remote. Nodes
  are pulled in bulk; `data`-type nodes are skipped; nodes the read API
  cannot return come back under `unavailable` in the `--json` summary
  (a client-side export is bounded by per-node read access, unlike the
  server's full-DB git push). Existing files are overwritten but files for
  removed nodes are never deleted. `--format markdown` is the default and
  only target today.
- `memory member` and `memory share` control who can access a memory.
  `member ls|add|set-role|rm <memory> --user <id> --role <owner|writer|reader>`
  manages team membership (rows exist only on group-class memories);
  `share ls|create|set-role|revoke <memory> --grantee <id> --role <writer|reader>`
  grants individual users access; `subscription` grants an entire organization
  access (the org-level counterpart, with the full Role set) — `subscription
  create|set-role <memory> --org <id> --role <owner|admin|contributor|reader>`,
  and `subscription ls <memory>` / `subscription rm <memory> --org <id>`. The
  memory ref is an id or URN; `add`/`create` upsert; `member rm` / `share revoke`
  / `subscription rm` require `--yes` non-interactively. Find user IDs via
  `org member ls` or `auth whoami`.
- `spec` manages product-spec nodes whose loc IS a citation number. A memory
  is either flat (`<module>:<feature>:<rule>[:<flow>]`, e.g. `msg:010:02`) or
  product-rooted (`<product>:<module>:<feature>:<rule>[:<flow>]`, e.g.
  `cli:cha:010:01`) — never both. Each tier has a reserved general-provisions
  contract its siblings inherit: feature `:00`, module `:000`, product `:gen`.
  It takes `-m/--memory` and addresses specs by bare citation, not a full URN.
  `spec get` shows one citation, or `--prefix <prefix>` dumps every spec under
  a branch (feature/module/product) with the same per-node detail, paged to
  exhaustion (`--limit`/`--offset` fetch a single page); `--json` emits an array.
  `--body-only` prints just one spec's raw markdown body for a clean
  `… | node update --content -` edit round-trip.
  `spec describe` reports a memory's scheme (flat/product), products, modules,
  and counts, reading any scheme declared in the memory's data
  (`--declare flat|product` writes it); `spec new` allocates the next number
  and scaffolds a **tier-appropriate** skeleton — a `## Modules`/`## Features`
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
  `--abstract -`/`--abstract-file` — writing only the field(s) that actually
  changed and preserving the rest (`--dry-run` previews); `spec extract <source> --to-feature <fff> [--rule <rr>]`
  splits a sub-rule out of a fat parent into its own citation under another
  feature, piping the moved chunk in via `--content -`/`--content-file`,
  auto-wiring the cross-ref edge new→source (`--ref-label`), and reminding you
  to refresh both abstracts (`--strip-source` also trims the chunk out of the
  source body when it matches verbatim); `spec link <from> <to>`
  cross-references one spec from another by their bare citations — a
  convention-aware `edge add` that validates both endpoints are specs in the
  same corpus and synthesizes the field→entity label when `--label` is omitted
  (`--dry-run` previews); `spec find` is semantic by default (`--match-exactly`
  forces literal regex matching); `spec grep <pattern>` searches every spec's
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
  **re-lints the changed specs** afterward; `spec register` is advisory/read-only (`--check` reports
  ledger drift, exit 5); `spec lint` takes `--product`/`--module`/`--all`,
  flags mixed-arity corpora, names the exact `edge add` remedy for a missing
  inheritance edge, and warns (rule `vector-index`) when the memory has no
  vector index so spec abstracts aren't embedded for semantic `find`
  (`--strict` promotes warnings to errors, exit 5); `spec check-tools` scans the
  corpus for `hadron_*` tool references and flags any that aren't a real
  registered tool (checked against a manifest baked into the binary — the union
  of the MCP + runner tool registries — with a small ignore-list for known
  non-tools like the `hadron_token` cookie), exit 5 on findings so CI can gate on
  tool-name drift; `spec supersede` retires a
  spec (never renumbers) and REQUIRES `--yes`; `spec import` is not yet
  implemented (exit 2).
- `ai-config ls` lists the masked AI configs *resolvable* in an App's chat
  context (App→Agent→Org→HadronServer, innermost wins, enabled-only) — never
  key material, only a preview. `ai-config create|update|rm` manage the
  underlying provider configs: `create` needs an owner (exactly one of
  `--app`/`--agent`/`--org`, ID or URN) plus `--name`/`--provider`/`--model`;
  the API key is a secret read via `--api-key -` (stdin) and never echoed back.
  `--file <path>` (or `--file -` for stdin) reads the whole config — key
  included — from a JSON object (keys mirror the flags: app/agent/org, name,
  provider, model, apiKey, params, enabled), keeping the secret out of argv;
  an explicit flag overrides the matching file field.
  `update <id>` changes only the fields you pass — `--api-key ""` clears the
  key, omitting it keeps it; `--param k=v` (repeatable) replaces the params
  object. `rm <id>` requires `--yes` non-interactively.
- `secret create|ls|rm` manages the general owner-scoped secret store. Values
  are write-only: `create` reads the secret material from stdin, a file, or an
  interactive no-echo prompt (never argv), and `ls` prints only the inspectable
  half (`name`, `kind`, `metadata`, audit fields). `--scope user` may omit
  `--owner` to mean the caller; org/app/memory scopes require `--owner`.
  `webfetch-auth` secrets use `--type bearer|basic|header` plus `--url-prefix`;
  the server derives `metadata.type`. `rm <id>` requires `--yes`
  non-interactively.
- `org` manages organizations, their members, and invitations. `org ls`
  lists organizations (`--mine` restricts to your memberships; unscoped spans
  every org you can see); `org create --name --urn`, `org get <id>`,
  `org update <id> [--name|--urn|--visible]`, `org rm <id>` (requires `--yes`).
  `org member ls <org-id>` lists members; `member add|set-role <org-id> --user
  <id> --role <OWNER|ADMIN|CONTRIBUTOR|READER>` and `member rm <org-id> --user
  <id>` manage them. `org invite create <email> --org <id> --role <r>` mints an
  invitation whose returned `slug` is the acceptance token — the invitee redeems
  it with `org invite accept <slug>`; `org invite show <slug>` inspects one.
- `agent` manages agents (user- or org-owned; an App runs an agent). `agent ls [--org
  <id>] [--type ASSISTANT|CHATBOT] [--visibility ORGANIZATION|PERSONAL|PUBLIC]`
  is the member-scoped view (agents in your orgs); `agent ls --public [--type
  <t>]` is the separate cross-org marketplace slice — every live PUBLIC agent,
  readable without org membership, so you can grab a foreign agent's URN to
  subscribe/install (`--org`/`--visibility` don't apply to it).
  `agent get <ref>` (ID or URN); `agent create --name <n>` creates a
  user-owned agent by default, or pass `--org <id>` for an org-owned agent,
  with optional `--type`/`--visibility`/`--description`/`--system-prompt`/
  `--system-memory`/`--surface` (repeatable); `agent update <id> [<field flags>]`
  changes only the fields you pass (`--surface` replaces the set); `agent rm <id>`
  requires `--yes`. Memory-attach, AI-config wiring, and app-wiring land next.
- `user search <query>` finds users (enumeration-safe: substring on handle /
  GitHub username, exact on email) — the way to resolve a user ID for `org
  member`/`memory member`/`memory share`. `profile set [--name|--email|--handle]`
  updates YOUR own profile; only the fields you pass change.
- `access check <user> <resource>` answers "what access does this user have to
  this resource?" — the authoritative, server-computed effective access plus the
  grants that confer it (no client-side re-derivation). `<user>` is an id, email,
  or handle (resolved via `searchUsers`); `<resource>` is a fully-qualified URN
  — `hrn:memory:…`, `hrn:node:…`, `hrn:app:…`, `hrn:agent:…` — or a bare
  AiServiceConfig id. Output carries `canRead/canWrite/canManage/canDelete`, a
  `role` label, and a `grants[]` array (each `{source, role, via}`); an empty
  `grants[]` is the first-class "no access" answer. Reading it requires audit
  rights on the resource (platform admin, the owning org's ADMIN/OWNER, or a
  strict-owner memory's principal) — otherwise the server's `FORBIDDEN` surfaces
  as exit 1. An unresolvable resource is exit 4; an under-qualified resource ref
  (e.g. `acme.com::kb` with no `hrn:` prefix) is a usage error (exit 2).
- **Headless runs** (spec-040, `cor:agt:010`) drive an App off any interactive
  session — the open-source counterpart to the portal's run surface. A *run*
  executes an entry (prompt) node under an App's identity; `run`, `schedule`, and
  `webhook` are three triggers into the same kernel, and `ticket` is the
  action-budget ledger.
  - `run trigger --app <ref> --entry <node-urn>` fires a MANUAL run now and prints
    its id; `--arg k=v` (repeatable, value parsed as JSON or string) sets template
    args, `--ai-config <n>` picks a named config, `--wait` polls to a terminal
    status (`--wait-timeout`, default 5m; a timeout is exit 6). `run ls` is the
    audit surface (scope `--app` XOR `--org`, filter `--status`, paged to
    exhaustion); `run get <id>` is the full record (budgets, policy, the run
    envelope `data` — fields extracted by flow nodes as the walk advances — and
    the failure payload); `run cancel <id>` is the kill switch (requires `--yes`).
  - `schedule create --app <ref> --name <n> --cron '<expr>' --entry <node-urn>`
    registers a recurring trigger (5-field cron, evaluated in `--tz`, default UTC;
    one-time `--at` is not yet a server capability). `--policy '<json>'` is a
    trigger-layer allow-list (`{"allow":[…]}`, `cor:acl:040`). `schedule ls
    --app`, `update <id>` (only the fields you pass change; `--enabled=false`
    disables, unset fields preserved), `rm <id>` (`--yes`).
  - `webhook create --app <ref> --name <n> --entry <node-urn>` mints an inbound
    trigger and prints the URL path + platform token **once** — they are never
    queryable again, so capture them (in `--json` they are `path`/`token`).
    `webhook rotate <id>` reissues the secret (old URL dies immediately; `--yes`);
    `webhook ls` never shows the secret; `webhook rm <id>` (`--yes`).
  - `task run <task>` renders a task node's prompt by default; `--app <ref>`
    instead EXECUTES it server-side — minting a MANUAL run under that App (a real
    LLM run) and printing the run id (`--json`: `mode:"execute"`, `runId`). Follow
    it with `run get <id>`. It's the fourth trigger into the same kernel.
  - `--as-self` (on `run trigger`, `schedule create`, `webhook create`,
    `task run --app`) makes the
    run act on behalf of YOU — required to reach your personal memories, and only
    usable by an authenticated user; an App-key caller gets `UNAUTHENTICATED`
    (exit 1). v1 never delegates a third party (`cor:agt:010:01`).
  - `ticket mint --org <ref> --action comm.outbound --count <n>` mints consumable
    action tickets into the org ledger (org ADMIN; `cor:acl:050:04`; `--app`
    scopes to one App, `--note` records why, `--expires` sets an ISO expiry);
    `ticket ls --org <ref>` is the ledger — minted / consumed-by-which-run /
    expiries, paged to exhaustion.
  - `grant create --org <ref> --user <ref> --action memory.clone[,...]` hands one
    org member extra management actions on top of their role bundle (org ADMIN,
    interactive-only; the grantee must be a live member — a grant dies with the
    membership). Actions use the matcher grammar: exact (`memory.clone`),
    prefix (`memory.*`), or `*`. `grant ls` defaults to YOUR OWN grants
    (self-audit is never gated); org ADMINs pass `--org` for the whole org,
    optionally `--user` to narrow. `grant revoke <id> --yes` soft-deletes;
    takes effect at the next gate check.
  - Every entry node is a fully-qualified node URN (`<org>::<memory>::<loc>`,
    optionally `hrn:node:`-prefixed) — a bare loc is rejected (exit 2). `--app`
    defaults to the App context (`hadron app use` / `--app`) when omitted.

## The escape hatch: hadron api

Anything the curated commands don't cover is reachable through raw
GraphQL against the Hadron API:

```
hadron api 'query { me { id email } }'
hadron api 'query($id: ID!) { memory(id: $id) { urn name } }' -F id=mem_123
cat op.graphql | hadron api -
```

`-F key=value` sets variables (values that parse as JSON are sent as
JSON, otherwise as strings). The verbatim GraphQL response envelope
is printed to stdout; GraphQL errors are reflected in the exit code.

## App context (optional)

Some Hadron deployments scope requests to an App. By default the CLI
sends no App context, which the server treats as fine. Set a default
with `hadron app use <urn>` or override per-invocation with
`--app <urn>`.

## Recipes

```
# Am I authenticated, and as whom?
hadron auth whoami --json

# List memories, machine-readable
hadron memory ls --json

# Inspect one memory by URN
hadron memory get acme.com:project-memory --json

# Export a whole memory to local markdown files (one .md per node)
hadron memory export acme.com:project-memory --out ./kb --json

# List nodes in a memory
hadron node ls --memory acme.com::kb --json

# Read one node's content and edges
hadron node get acme.com::kb::findings:flaky-ci --json

# Create a node from stdin
cat finding.md | hadron node add -m acme.com::kb --loc findings:flaky-ci \
  --name "Flaky CI" --content -

# Update just the name (other fields preserved)
hadron node update acme.com::kb::findings:flaky-ci --name "Flaky CI (resolved)"

# Author structured storage (#725, the write half of the --where/--object-type
# query surface). Define a memory's property schema (declared collections +
# typed fields), then write conforming records — the server validates
# objectType + properties against the schema. `node get` reads them back.
hadron memory set acme.com::research --schema-file schema.json
hadron node add -m acme.com::research --loc competitors:acme --name Acme \
  --object-type competitor --properties '{"tier":"enterprise","seats":50}'
hadron node update acme.com::research::competitors:acme --properties '{"tier":"pro","seats":12}'
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
hadron object create -m acme.com::research --type competitor \
  --fields '{"name":"Letta","stage":"series-a","fundingUsd":12000000}' --key letta
hadron object update acme.com::research::competitor:letta --fields '{"stage":"series-b"}'
hadron object find -m acme.com::research --type competitor \
  --match '{"stage":"series-b"}' --sort '{"fundingUsd":"desc"}' --json
hadron object get acme.com::research::competitor:letta   # flat { id, type, ...fields }

# Move a node (keeps its id + edges); clone it to a new memory (new id)
hadron node move acme.com::kb::findings:flaky-ci --to-urn acme.com::kb::archive:flaky-ci
hadron node clone acme.com::kb::templates:base --to-memory acme.com::sandbox --json

# Fold a duplicate node into the canonical one (agents must pass --yes)
hadron node merge acme.com::kb::findings:dup --into acme.com::kb::findings:canonical \
  --field CONTENT --field EDGES --delete-source --yes

# Bulk search-and-replace across a memory. A real run previews + prompts;
# agents pass --dry-run to preview or --yes to apply non-interactively. Even
# with --yes the affected node count is printed to stderr before writing;
# --max-nodes N refuses the write if more than N nodes would change (a guard
# against a whole-memory rewrite from a wrong -m URN or a forgotten --prefix).
hadron replace text "old-url.com" "new-url.com" \
  -m acme.com::kb --field content --field description --dry-run

# Connect two nodes
hadron edge add --from acme.com::kb::findings:flaky-ci \
  --to acme.com::kb::start-here --label routes-to

# List a node's edges, delete one (agents must pass --yes)
hadron edge ls acme.com::kb::findings:flaky-ci --json
hadron edge rm <edge-id> --yes

# Delete a node (agents must pass --yes). Soft by default (recoverable from
# version history); --hard removes the row + its edges + version history
# irreversibly. Deleting a node that HAS descendants is refused (exit 2,
# NODE_HAS_DESCENDANTS naming the count) unless you pass --recursive/-r, which
# deletes the whole subtree under its loc.
hadron node rm acme.com::kb::findings:flaky-ci --yes
hadron node rm acme.com::kb::data:stale --hard --yes
hadron node rm acme.com::kb::findings --recursive --yes    # the branch + all children

# Revision history: list a node's snapshots, then inspect, label, or restore one.
# restore is undoable by default; --truncate discards newer history (needs --yes).
# Pass a bare node id to reach a soft-deleted node's history for cleanup.
hadron node revision list acme.com::kb::findings:flaky-ci --json
hadron node revision get <revision-id>
hadron node revision label <revision-id> --label "before the auth refactor"
hadron node revision restore <revision-id>
hadron node revision restore <revision-id> --truncate --yes
hadron node revision clear acme.com::kb::findings:flaky-ci --yes

# Ranked search (hybrid semantic+keyword by default; scores + abstracts in --json)
hadron search "how do users report a bad actor" -m micromentor.org::mmdata --json

# Structured property/attribute queries (parity with the server #719 `where`
# predicate). --where is a raw-JSON predicate over the node's properties/data
# JSONB: a LEAF is {"path":[...],"<op>":<value>} with one operator of
# eq|ne|in|lt|lte|gt|gte|between|exists|contains, optional "field":properties|data
# (default properties) and "as":text|number|datetime|boolean (default text); a
# BRANCH is {"and"|"or":[...]} or {"not":{...}}. --object-type filters the
# objectType collection facet; --sort-property orders by a JSON path (overrides
# relevance/loc sort). Available on both `node ls` (browse) and `search` (ranked).
hadron node ls -m acme.com::kb --object-type insight \
  --where '{"and":[{"path":["source"],"eq":"substack"},{"path":["capturedAt"],"as":"datetime","gte":"2026-07-04"}]}' \
  --sort-property '{"path":["capturedAt"],"as":"datetime","direction":"desc"}' --json
hadron search "pricing" --object-type competitor --where '{"path":["tier"],"eq":"enterprise"}' --json

# Arbitrary query with a variable
hadron api 'query($q: String!) { findNodes(query: $q) { hits { node { loc name } } } }' -F q="auth flow"

# Drive a headless run: fire an entry node now and wait for the result
hadron run trigger --app acme.com:ops \
  --entry acme.com::ops::tasks:nightly-digest --arg topic=security --wait --json

# Schedule it nightly (on behalf of you, so it can reach your personal memories)
hadron schedule create --app acme.com:ops --name nightly-digest \
  --cron '0 6 * * *' --tz America/New_York \
  --entry acme.com::ops::tasks:nightly-digest --as-self

# Inspect what ran and why (the audit surface)
hadron run ls --app acme.com:ops --status FAILED --json
hadron run get <run-id> --json

# Mint an outbound-comms budget the runs consume
hadron ticket mint --org acme.com --action comm.outbound --count 100 --note 'digest sends'
```
