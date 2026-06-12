---
name: use-hadron-cli
description: Read and write Hadron AI-memory (memories, nodes, edges, Apps) from the shell with the hadron CLI. Use when the user asks to query or edit a Hadron knowledge graph, mentions the hadron CLI or Hadron URNs (org:memory:loc), or when an MCP-less environment needs Hadron access. Covers auth, the stable JSON/exit-code contract, URN rules, destructive-command safety (--yes), and the raw GraphQL escape hatch.
---

# Using the hadron CLI

`hadron` is the command-line interface to the Hadron AI-memory platform.
**First step in every session**: run `hadron agentic-usage` — it prints the
complete, stable agent contract (commands, JSON output rules, exit codes,
recipes). Treat that output as the source of truth; this skill is the
orientation layer.

## Check before you act

```sh
hadron auth status --json   # exit 0 = signed in, 3 = not signed in
hadron auth whoami --json   # who am I?
```

If not signed in, do NOT run `hadron auth login` (interactive browser flow —
humans only). Ask the user to log in, or use a token:
`HADRON_TOKEN=hdr_user_...` env var, or `printf '%s\n' "$TOKEN" | hadron auth login --with-token`.

## Non-negotiable rules for agents

1. **Always pass `--json`** for machine-readable output. JSON goes to stdout,
   errors to stderr as `{"error":{"code":...,"message":"..."}}`. Field names
   are stable.
2. **Always pass `--yes` on destructive commands** (`memory rm`, `node rm`,
   `edge rm`, `app uninstall`). Without it they exit 2 when non-interactive.
   Confirm with the user before deleting anything you did not create.
3. **Node references are always fully-qualified URNs**: `<org>:<memory>:<loc>`
   (e.g. `acme.com:kb:findings:flaky-ci`), optionally `urn:node:`-prefixed.
   Bare locs are rejected (exit 2) — the same loc can exist in many memories.
4. **Exit codes are contract**: 0 success, 1 failure, 2 usage error,
   3 auth required, 4 not found, 5 conflict, 6 cancelled/timeout. Branch on
   them instead of parsing error text.

## Core verbs

```sh
hadron memory ls --json                       # list memories
hadron node ls -m acme.com:kb --json          # list nodes in a memory
hadron node get acme.com:kb:start-here --json # content + edges
hadron node add -m acme.com:kb --loc findings:x --name "X" --content-file note.md --json
hadron node update acme.com:kb:findings:x --name "X (resolved)" --json  # unset fields preserved
hadron edge add --from acme.com:kb:a --to acme.com:kb:b --label routes-to --json
hadron edge ls acme.com:kb:a --json
```

Inline or piped content also works: `--content "<text>"` or `--content -` (stdin).

`node add` fails if the loc exists (use `node update`); `node update` only
changes fields you pass. Edges are directed + labeled; cross-memory edges are
allowed; after creation, address an edge by the edge ID shown in `edge ls`.

## When the curated commands don't cover it

```sh
hadron api 'query($q: String!) { nodeSearch(query: $q) { nodes { loc name } } }' -F q="auth flow"
```

`hadron api` sends raw GraphQL; `-F key=value` binds variables (JSON-parsed
when possible). The response envelope prints verbatim; GraphQL errors are
reflected in the exit code.

## Server / App context

Default server is `https://srv.hadronmemory.com`; override with
`--server <url>` or `hadron config set server <url>`. Most calls need no App
context; set one with `hadron app use <urn>` or per-call `--app <urn>` only
when the deployment requires it.
