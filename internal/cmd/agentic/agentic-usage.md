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
hadron memory ls | get <id-or-urn> | set [<id-or-urn>] | rm <id-or-urn> | clone <id-or-urn> --name <new-name>
hadron node ls [-m <memory>] | get <urn> | add | update <urn> | rm <urn>
hadron edge ls <node-urn> | add | update <edge-id> | rm <edge-id>
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
  optionally `urn:node:`-prefixed. Bare locs are rejected (exit 2) —
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
  `--content "<text>"`, `--content -` (stdin), or `--content-file`.
- `memory clone` deep-copies a memory (nodes + edges) into a new
  same-org memory and rewrites references to the source memory's URN
  inside node content. Version history, shares/subscriptions, assets,
  and git-sync config are NOT copied. Encrypted memories and agent
  system / app memories cannot be cloned.

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
