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
| 2    | usage error (bad flags/arguments, not-yet-implemented command) |
| 3    | authentication required or rejected |
| 4    | not found (or not visible to this principal) |
| 5    | conflict (e.g. duplicate install) |
| 6    | cancelled / timed out waiting for the user |

## Command surface (v1)

```
hadron auth login | logout | whoami | status
hadron memory ls | get <id-or-urn> | set | rm        # set/rm pending
hadron node ls | get | add | update | rm             # pending
hadron app ls | install | uninstall | use <urn>      # use available
hadron config get | set | list
hadron api <query-or-mutation>                       # raw GraphQL
hadron version
hadron completion <shell>
hadron agentic-usage                                 # prints this doc
```

Commands marked pending are registered and documented but exit 2
with "not implemented yet" — check `hadron <group> --help` before
relying on one.

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
hadron memory get acme.com::project-memory --json

# Arbitrary query with a variable
hadron api 'query($q: String!) { nodeSearch(query: $q) { nodes { loc name } } }' -F q="auth flow"
```
