# Implementation Plan: `hadron mcp-server …` — external MCP server registry

> **Status: implemented and verified**; this reflects the design as built. GH
> issue [#220](https://github.com/hadron-memory/hadron-cli/issues/220), against
> hadron-server #634.

## Context

hadron-server #634 added the external-MCP-server registry (the hadrontool-mcp
conduit): org-owned `McpServer` rows whose tools headless runs invoke as
`mcp__<slug>__<tool>` in a node's `data.tools`. Per the CLI-completeness rule (a
server surface isn't done until the CLI covers it), this adds the `hadron
mcp-server` group.

## Server surface consumed (#634)

| Op | Kind | Notes |
|---|---|---|
| `mcpServers(orgId, limit, offset)` | query | uniform `{ items, total }` page |
| `mcpServer(ref)` | query | one server; null → not found |
| `mcpServerTools(ref)` | query | **live** tools/list, filtered by the row's allowlist; each carries `runToolName` (`mcp__<slug>__<tool>`) |
| `createMcpServer(orgRef, slug, name, url, headers, toolAllowlist, enabled)` | mutation | slug immutable; `headers` write-only JSON |
| `updateMcpServer(ref, name, url, headers, clearHeaders, toolAllowlist, enabled)` | mutation | `headers` REPLACES; `clearHeaders` removes (one or the other) |
| `deleteMcpServer(ref)` | mutation | hard delete |

`McpServer` exposes `hasHeaders` (never the values), `toolAllowlist`, `enabled`.

## Command surface (as built)

```
hadron mcp-server ls [--org <ref>]                                       # mcpServers   (alias: list)
hadron mcp-server get <id>                                               # mcpServer    (alias: show)
hadron mcp-server tools <id>                                             # mcpServerTools
hadron mcp-server create --org <ref> --slug <s> --name <n> --url <u> \
    [--header 'Name: value']... [--allow <tool>]... [--disabled]         # createMcpServer
hadron mcp-server update <id> [--name] [--url] [--header ...] \
    [--clear-headers] [--allow ...] [--clear-allow] [--enabled|--disabled]  # updateMcpServer
hadron mcp-server delete <id> [--yes]                                    # deleteMcpServer (alias: rm)
```

`mcp-server` is a top-level hyphenated group, matching the existing `ai-config`.

## Design decisions

- **Write-only headers.** `--header 'Name: value'` (repeatable, curl-style) is
  parsed into a JSON object via `strings.Cut` on the FIRST colon (so a value may
  contain colons, e.g. `Bearer x:y`); a missing colon or empty name is a usage
  error. The values are never read back — reads only ever surface `hasHeaders`.
- **headers vs clearHeaders** on update are `MarkFlagsMutuallyExclusive`
  (the server rejects both); likewise `--allow` / `--clear-allow` and
  `--enabled` / `--disabled`. Update gates every field on `cmd.Flags().Changed`
  (not the parsed bool) so an explicit `--enabled=false` still registers.
- **Clearing the allowlist.** `--allow` replaces it; `--clear-allow` sends an
  explicit `[]` (= all tools) — the only CLI path to reset a restricted list,
  since omitting `--allow` preserves the stored one.
- **Null `tools`.** `mcpServerTools` is a nullable list: null means the server
  is missing / not visible to the caller (distinct from an empty tool set), so
  `tools` surfaces it as NotFound like `get`.
- **Allowlist & enabled** are `*[]string` / `*bool`, sent only when their flag
  changed (create `--disabled` → `enabled:false`; omit → server default). An
  empty allowlist means "all tools" per the server, matching the `all` label in
  list/get output.
- **Pass-through refs.** `--org` and the server id are opaque to the CLI and
  validated server-side (like the `grant` group); no client-side URN grammar.
- **Policy note in help.** Registration grants nothing — a run still needs
  `tool.mcp__<slug>__<tool>` allowed by the policy chain; called out in the group
  and `create` help.
- **`--json` DTOs** are package-local (`mcpServerDTO`, `mcpToolDTO`);
  `toolAllowlist` is initialized to `[]`. `delete` returns a `{id,status}` map.
  A `false` delete/update return is surfaced as NotFound.

## Files

- `internal/api/queries/mcpservers.graphql` — the six typed operations + the
  `McpServerFields` fragment.
- `internal/cmd/mcpserver/` — group + ls/get/tools/create/update/delete + shared
  DTO/`parseHeaders`. Wired into `NewRootCmd`.
- `schema/schema.graphql` — refreshed snapshot (the #634 surface).
- `internal/cmd/agentic/agentic-usage.md` — the `mcp-server` surface line.
- `internal/cmd/mcpserver_cmd_test.go` — command tests.

## Testing

Covers: ls (`--org` forwarding, page parse), get (+ null→NotFound), tools
(runToolName rendering), create (header→JSON object incl. colon-in-value,
`--allow`→allowlist, `--disabled`→enabled:false, unset inputs omitted), update
(`--clear-headers`/`--disabled` wire values, unset omitted, nothing-to-update
guard, header/clear-headers mutual exclusion), and delete (`--yes` gate,
`false`→NotFound). `TestAgenticUsageDocumentsEveryCommand` gates the surface line.
