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
```

Tokens are long-lived `hdr_user_*` personal access tokens, minted in
the Hadron portal or by the OAuth flow. The server defaults to
`https://srv.hadronmemory.com`; override per-invocation with `--server
<url>` or persistently with `hadron config set server <url>`.

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
hadron auth login | logout | whoami | status
hadron memory ls | get <id-or-urn> | set [<id-or-urn>] | rm <id-or-urn> | clone <id-or-urn> --name <new-name> | export <id-or-urn> [--out <dir>]
hadron node ls [-m <memory>] | get <urn> | add | update <urn> | rm <urn>
hadron edge ls <node-urn> | add | update <edge-id> | rm <edge-id>
hadron spec ls [-m <memory>] | get <citation>|--prefix <prefix> | describe | register [--check] | find <query> [--match-exactly] | new ... | extract <citation> --to-feature <fff> | lint [<citation>] | supersede <citation> | import spec-kit|code
hadron app ls --org <org> | install | uninstall <id> | use <urn>
hadron config get | set | list
hadron api <query-or-mutation>                       # raw GraphQL
hadron version
hadron completion <shell>
hadron agentic-usage                                 # prints this doc
```

Conventions:

- Memory URNs are `org:memory` (e.g. `hadronmemory.com:dev`). Where a
  command takes an ID it also accepts the URN.
- Node references are ALWAYS fully-qualified URNs:
  `<org>:<memory>:<loc>` (e.g. `hadronmemory.com:dev:start-here`),
  optionally `hrn:node:`-prefixed (legacy `urn:node:` also accepted). Bare
  locs are rejected (exit 2) —
  the same loc can exist in several memories.
- Edges are directed and labeled. Endpoints are node URNs; edges are
  addressed by their edge ID afterwards (shown by `edge ls` and in
  `node get --json`). Cross-memory edges are allowed.
- Destructive commands (`memory rm`, `node rm`, `edge rm`,
  `app uninstall`) prompt on a terminal and REQUIRE `--yes` when run
  non-interactively (agents must always pass `--yes`). Without it
  they exit 2.
- `memory set` creates when called without a positional argument
  (requires `--org` and `--name`) and updates when given one. Only
  fields passed as flags change.
- `node add` fails if the loc already exists; `node update` modifies
  an existing node and preserves unset fields. Content comes from
  `--content "<text>"`, `--content -` (stdin), or `--content-file`;
  the abstract likewise from `--abstract`, `--abstract -`, or
  `--abstract-file` (paragraph abstracts dodge shell quoting this way).
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
  and scaffolds the rubric
  (`--new-product`/`--new-module`/`--new-feature` create roots; `--contract`
  scaffolds the contract at the deepest tier named; the abstract can come from
  `--abstract-file`/`--abstract -`; `--dry-run` previews
  without writing); `spec edit <citation>` opens the spec's body in $EDITOR
  pre-loaded — or replaces it non-interactively from `--content -`/`--content-file`
  — writing a content-only update (the abstract is preserved) only when the body
  actually changed (`--dry-run` previews); `spec extract <source> --to-feature <fff> [--rule <rr>]`
  splits a sub-rule out of a fat parent into its own citation under another
  feature, piping the moved chunk in via `--content -`/`--content-file`,
  auto-wiring the cross-ref edge new→source (`--ref-label`), and reminding you
  to refresh both abstracts (`--strip-source` also trims the chunk out of the
  source body when it matches verbatim); `spec link <from> <to>`
  cross-references one spec from another by their bare citations — a
  convention-aware `edge add` that validates both endpoints are specs in the
  same corpus and synthesizes the field→entity label when `--label` is omitted
  (`--dry-run` previews); `spec find` is semantic by default (`--match-exactly`
  forces keyword); `spec register` is advisory/read-only (`--check` reports
  ledger drift, exit 5); `spec lint` takes `--product`/`--module`/`--all`,
  flags mixed-arity corpora, names the exact `edge add` remedy for a missing
  inheritance edge, and warns (rule `vector-index`) when the memory has no
  vector index so spec abstracts aren't embedded for semantic `find`
  (`--strict` promotes warnings to errors, exit 5); `spec supersede` retires a
  spec (never renumbers) and REQUIRES `--yes`; `spec import` is not yet
  implemented (exit 2).

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

# Connect two nodes
hadron edge add --from acme.com:kb:findings:flaky-ci \
  --to acme.com:kb:start-here --label routes-to

# List a node's edges, delete one (agents must pass --yes)
hadron edge ls acme.com:kb:findings:flaky-ci --json
hadron edge rm <edge-id> --yes

# Delete a node (agents must pass --yes)
hadron node rm acme.com:kb:findings:flaky-ci --yes

# Arbitrary query with a variable
hadron api 'query($q: String!) { nodeSearch(query: $q) { nodes { loc name } } }' -F q="auth flow"
```
