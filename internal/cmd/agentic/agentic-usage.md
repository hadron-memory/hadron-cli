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

## Command surface (v1)

```
hadron auth login | logout | whoami | status | token create|ls|revoke <id>
hadron memory ls | get <id-or-urn> | set [<id-or-urn>] | rm <id-or-urn> | clone <id-or-urn> --name <new-name> | export <id-or-urn> [--out <dir>] | member ls|add|set-role|rm <memory> --user <id> [--role <r>] | share ls|create|set-role|revoke <memory> --grantee <id> [--role <r>]
hadron node ls [-m <memory>] | get <urn> | add | update <urn> | rm <urn> | export <urn> [-o <file>] [--format md|json] | import <file|-> [-m <memory>] [--with-edges]
hadron search <query> [-m <memory>]... [--mode hybrid|keyword|vector|regex] [--prefix <loc>] [--type <type>] [--tag <t>]... [--limit N] [--offset N] [-l|--long] [--json]
hadron replace <old> <new> --field <f> (--node <urn> | -m <memory>) [--prefix <loc>] [--regex] [-i] [--dry-run] [--yes]
hadron edge ls <node-urn> | add | update <edge-id> | rm <edge-id>
hadron spec ls [-m <memory>] | get <citation>|--prefix <prefix> | describe | register [--check] | find <query> [--match-exactly] | new ... | extract <citation> --to-feature <fff> | lint [<citation>] | supersede <citation> | import spec-kit|code
hadron app ls --org <org> | install | uninstall <id> | use <urn>
hadron ai-config ls [--app <id>] [--agent <id>] | create (--app|--agent|--org <id>) --name <n> --provider <p> --model <m> [--api-key -] | update <id> ... | rm <id>
hadron org create --name <n> --urn <urn> | get <id> | update <id> | rm <id> | member ls|add|set-role|rm <org-id> --user <id> [--role <r>]
hadron run trigger --app <ref> --entry <node-urn> [--as-self] [--arg k=v]... [--ai-config <n>] [--wait] | ls [--app <ref> | --org <ref>] [--status <s>] | get <id> | cancel <id> --yes
hadron schedule create --app <ref> --name <n> --cron '<expr>' [--tz <zone>] --entry <node-urn> [--as-self] [--policy <json>] [--ai-config <n>] [--arg k=v]... | ls --app <ref> | update <id> ... | rm <id> --yes
hadron webhook create --app <ref> --name <n> --entry <node-urn> [--as-self] [--policy <json>] [--args-schema <json>] [--ai-config <n>] | rotate <id> --yes | ls --app <ref> | rm <id> --yes
hadron ticket mint --org <ref> [--app <id>] --action comm.outbound --count <n> [--note <why>] [--expires <iso>] | ls --org <ref>
hadron config get | set | list
hadron api <query-or-mutation>                       # raw GraphQL
hadron version
hadron completion <shell>
hadron agentic-usage                                 # prints this doc
```

Conventions:

- Memory references accept the memory id, the full `hrn:memory:<org>::<slug>`
  URN, or the short `<org>::<slug>` / `<org>:<slug>` forms (all resolve to the
  same memory) across `memory get|set|rm|member|share|export`.
- Node references are fully-qualified URNs
  `<org>::<memory>::<loc>` (double-colon between segments — e.g.
  `hadronmemory.com::dev::start-here`), optionally `hrn:node:`-prefixed (legacy
  `urn:node:` also accepted). Single-colon `<org>:<memory>:<loc>` is **not** a
  valid full URN — a loc itself contains single colons
  (`services:secureid:user-reporting`), so it's ambiguous. A bare loc is rejected
  (exit 2) *unless* you pass `-m/--memory <org::memory>` (single-colon
  `<org>:<memory>` also accepted) to name the memory — then
  `node get|update|rm|export` and `edge add|ls` take a bare `<loc>` (e.g.
  `node get start-here -m hadronmemory.com::dev`; `edge add -m a::m --from x
  --to y …` applies the memory to both endpoints).
  Without `-m`, the URN must name the memory — the same loc can exist in
  several memories.
- Edges are directed, first-class entities (spec 037): each carries an
  optional `name` (the relationship — was `label`) and a `loc` that is its
  identity (`hrn:edge:<org>::<memory>::<loc>`). `edge add --from <node> --to
  <node> --name <rel>` creates one (plus optional `--loc`/`--description`/
  `--runnable`); `edge update`/`edge rm` address it by its edge ID (shown by
  `edge ls` and in `node get --json`). A nameless edge prints its loc instead.
  Cross-memory edges are allowed.
- Destructive / bulk-write commands (`memory rm`, `node rm`, `edge rm`,
  `app uninstall`, and a real `replace`) prompt on a terminal and REQUIRE
  `--yes` when run non-interactively (agents must always pass `--yes`, or
  `--dry-run` to preview `replace`). Without it they exit 2.
- `memory set` creates when called without a positional argument
  (requires `--org` and `--name`) and updates when given one. Only
  fields passed as flags change.
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
- `isRunnable` gates whether `hadron task run` will execute a node. Both
  `node add` and `node update` take `--runnable` to set it; on `update` it's
  tri-state — `--runnable` sets true, `--runnable=false` clears it, omitting it
  preserves the current value. `node get` shows `runnable:` in the text view,
  and both `node get` and `node ls` surface `isRunnable` in `--json` (`ls`
  also shows a `RUN` column with a ✓ for runnable nodes). `node ls --runnable`
  filters server-side to runnable nodes (`--runnable=false` to the explicitly
  non-runnable; omit for all) — the listing counterpart to `hadron task run`'s
  gate.
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
  [<file>|-]` has TWO modes, chosen by the `mode` field in its `--json` output:
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
- `memory clone` deep-copies a memory (nodes, edges, pending edges)
  into a new same-org memory and rewrites references to the source
  memory's URN inside node content and abstracts. Version history,
  shares/subscriptions, assets, and git-sync config are NOT copied.
  Encrypted memories and agent system / app memories cannot be cloned.
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
  grants individual users access. The memory ref is an id or URN; `add`/`create`
  upsert; `member rm` / `share revoke` require `--yes` non-interactively. Find
  user IDs via `org member ls` or `auth whoami`.
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
  forces literal regex matching); `spec register` is advisory/read-only (`--check` reports
  ledger drift, exit 5); `spec lint` takes `--product`/`--module`/`--all`,
  flags mixed-arity corpora, names the exact `edge add` remedy for a missing
  inheritance edge, and warns (rule `vector-index`) when the memory has no
  vector index so spec abstracts aren't embedded for semantic `find`
  (`--strict` promotes warnings to errors, exit 5); `spec supersede` retires a
  spec (never renumbers) and REQUIRES `--yes`; `spec import` is not yet
  implemented (exit 2).
- `ai-config ls` lists the masked AI configs *resolvable* in an App's chat
  context (App→Agent→Org→HadronServer, innermost wins, enabled-only) — never
  key material, only a preview. `ai-config create|update|rm` manage the
  underlying provider configs: `create` needs an owner (exactly one of
  `--app`/`--agent`/`--org`, ID or URN) plus `--name`/`--provider`/`--model`;
  the API key is a secret read via `--api-key -` (stdin) and never echoed back.
  `update <id>` changes only the fields you pass — `--api-key ""` clears the
  key, omitting it keeps it; `--param k=v` (repeatable) replaces the params
  object. `rm <id>` requires `--yes` non-interactively.
- `org` manages organizations and their members. `org create --name --urn`,
  `org get <id>`, `org update <id> [--name|--urn|--visible]`, `org rm <id>`
  (requires `--yes`). `org member ls <org-id>` lists members; `member
  add|set-role <org-id> --user <id> --role <OWNER|ADMIN|CONTRIBUTOR|READER>` and
  `member rm <org-id> --user <id>` manage them. There's no org-list query —
  address an org by id (the org behind a memory URN).
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
    exhaustion); `run get <id>` is the full record (budgets, policy, failure
    payload); `run cancel <id>` is the kill switch (requires `--yes`).
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
  - `--as-self` (on `run trigger`, `schedule create`, `webhook create`) makes the
    run act on behalf of YOU — required to reach your personal memories, and only
    usable by an authenticated user; an App-key caller gets `UNAUTHENTICATED`
    (exit 1). v1 never delegates a third party (`cor:agt:010:01`).
  - `ticket mint --org <ref> --action comm.outbound --count <n>` mints consumable
    action tickets into the org ledger (org ADMIN; `cor:acl:050:04`; `--app`
    scopes to one App, `--note` records why, `--expires` sets an ISO expiry);
    `ticket ls --org <ref>` is the ledger — minted / consumed-by-which-run /
    expiries, paged to exhaustion.
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
hadron node ls --memory acme.com:kb --json

# Read one node's content and edges
hadron node get acme.com:kb:findings:flaky-ci --json

# Create a node from stdin
cat finding.md | hadron node add -m acme.com:kb --loc findings:flaky-ci \
  --name "Flaky CI" --content -

# Update just the name (other fields preserved)
hadron node update acme.com:kb:findings:flaky-ci --name "Flaky CI (resolved)"

# Bulk search-and-replace across a memory. A real run previews + prompts;
# agents pass --dry-run to preview or --yes to apply non-interactively.
hadron replace "old-url.com" "new-url.com" \
  -m acme.com:kb --field content --field description --dry-run

# Connect two nodes
hadron edge add --from acme.com:kb:findings:flaky-ci \
  --to acme.com:kb:start-here --label routes-to

# List a node's edges, delete one (agents must pass --yes)
hadron edge ls acme.com:kb:findings:flaky-ci --json
hadron edge rm <edge-id> --yes

# Delete a node (agents must pass --yes)
hadron node rm acme.com:kb:findings:flaky-ci --yes

# Ranked search (hybrid semantic+keyword by default; scores + abstracts in --json)
hadron search "how do users report a bad actor" -m micromentor.org::mmdata --json

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
