#!/usr/bin/env bash
# Generate the manifest of registered hadron_* tool names from a hadron-server
# checkout — the UNION of the two registries a product spec may legitimately
# cite:
#   1. MCP tools:    server.tool('hadron_*', …) in src/mcp/server.ts
#   2. Runner tools: RunToolDef { name: 'hadron_*' } in src/lib/runner/tools/*.ts
# Prints one tool name per line, sorted and de-duplicated, to stdout. The
# Makefile `tools-manifest` target writes this to schema/mcp-tools.txt (embedded
# into `hadron spec check-tools`); `tools-manifest-check` diffs it for drift.
set -euo pipefail

DIR="${HADRON_SERVER_DIR:-../hadron-server}"
mcp="$DIR/src/mcp/server.ts"
runner="$DIR/src/lib/runner/tools"

if [ ! -f "$mcp" ]; then
  echo "gen-tools-manifest: $mcp not found — set HADRON_SERVER_DIR to a hadron-server checkout" >&2
  exit 2
fi

# Fixed header — kept in the generator (not the Makefile) so the committed file
# is byte-for-byte reproducible and `make tools-manifest-check` can diff it.
cat <<'HEADER'
# Registered hadron_* tool names — the union of hadron-server's two tool
# registries (MCP server.tool() + runner RunToolDef). GENERATED, do not edit
# by hand: run 'make tools-manifest'. Embedded into 'hadron spec check-tools',
# which flags any hadron_* token in a spec that is neither in this list nor in
# mcp-tools-ignore.txt. CI's tools-manifest-check fails if this drifts from
# hadron-server (the drift that let h-* shorthand rot silently — issue #240).
#
HEADER

{
  # MCP tools: the name is the first argument to server.tool(, on the line that
  # immediately follows the call opener.
  grep -A1 -E 'server\.tool\(' "$mcp" | grep -oE "'hadron_[a-z_]+'" | tr -d "'"

  # Runner tools: the RunToolDef.name field. (Non-hadron_ runner/integration
  # tools — e.g. ha__*, twilio_* — are out of scope: a spec can only drift on a
  # hadron_* token.)
  if [ -d "$runner" ]; then
    grep -rhoE "name: '(hadron_[a-z_]+)'" "$runner" | grep -oE "hadron_[a-z_]+"
  fi
} | sort -u
