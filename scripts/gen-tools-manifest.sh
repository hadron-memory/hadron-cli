#!/usr/bin/env bash
# Generate the manifest of registered hadron_* tool names from a hadron-server
# checkout — the UNION of the two registries a product spec may legitimately
# cite:
#   1. MCP tools:    server.tool('hadron_*', …) in EVERY non-test module under
#                    src/mcp/ — server.ts and the split modules it delegates to
#                    (agentDefinitionTools.ts, signingTools.ts, …). A tool
#                    registered behind a feature gate is still registered.
#                    MINUS the tools hadron-server DISABLES (see below).
#   2. Runner tools: RunToolDef { name: 'hadron_*' } in src/lib/runner/tools/*.ts
#
# The disabled-tool switch (hadron-server#3082, plan D1a, cli#1511): a disabled
# agent-team tool is registered through the helper DISABLE_HELPER, never
# server.tool(), so the SDK never lists it. The generator keys on that helper BY
# NAME and subtracts the disabled names from the MCP registry (only), so a
# disabled tool is skipped because it is disabled, not because its call text
# happens not to match the grep. The runner registry is unioned AFTER the
# subtraction: a runner tool that shares a disabled MCP name (hadron_task_create)
# stays. Two shapes of the helper are read, lexically and exhaustively:
#   (const|let) <alias> = <HELPER>(…)      then   <alias>(  'hadron_*', …
#   <HELPER>(…)(  'hadron_*', …                    (the direct form)
# The generator refuses (exit 2) when the helper is not found or disables
# nothing: a quiet empty disabled set would silently re-list every disabled tool.
# Prints one tool name per line, sorted and de-duplicated, to stdout. The
# Makefile `tools-manifest` target writes this to internal/cmd/spec/mcp-tools.txt
# (embedded into `hadron spec check-tools`); `tools-manifest-check` diffs it for
# drift.
set -euo pipefail

# The server's disabled-tool helper (hadron-server src/mcp/disabledTools.ts,
# #3082). A constant, not an env var: the manifest must be reproducible.
DISABLE_HELPER="offTeamTool"

DIR="${HADRON_SERVER_DIR:-../hadron-server}"
mcp="$DIR/src/mcp"
runner="$DIR/src/lib/runner/tools"

if [ ! -f "$mcp/server.ts" ]; then
  echo "gen-tools-manifest: $mcp/server.ts not found — set HADRON_SERVER_DIR to a hadron-server checkout" >&2
  exit 2
fi

# disabled_in_file prints the tool names a module registers through the
# disabled-tool helper: the first quoted hadron_* name at or after each call
# opener (same line, else the next line — the opener rule server.tool( uses,
# but each opener takes at most ONE name, so a kept tool on the next line is
# never read as disabled). The alias must start at a non-identifier character,
# and not after a '.', so turnoff( and x.off( are not the alias.
disabled_in_file() {
  local f="$1" aliases alt re
  aliases=$(grep -oE "(const|let)[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=[[:space:]]*${DISABLE_HELPER}\(" "$f" \
    | sed -E 's/^(const|let)[[:space:]]+([A-Za-z0-9_]+).*/\2/' | LC_ALL=C sort -u | paste -sd'|' - || :)
  re="${DISABLE_HELPER}\\([^()]*\\)\\("
  if [ -n "$aliases" ]; then
    re="${re}|(^|[^A-Za-z0-9_.])(${aliases})\\("
  fi
  # The regex travels in the environment: awk -v would process its backslash
  # escapes (gawk turns \( into a bare paren and fails to compile it).
  RE="$re" awk '
    function firstname(str,   m) {
      if (match(str, /["\047]hadron_[a-z0-9_]+["\047]/)) {
        m = substr(str, RSTART + 1, RLENGTH - 2); return m
      }
      return ""
    }
    {
      line = $0
      if (pending) {
        n = firstname(line); if (n != "") print n
        pending = 0
      }
      if (match(line, ENVIRON["RE"])) {
        rest = substr(line, RSTART + RLENGTH)
        n = firstname(rest)
        if (n != "") print n; else pending = 1
      }
    }
  ' "$f"
}

# The disabled set across every non-test MCP module. Aliases are per module.
disabled_set() {
  local f
  while IFS= read -r -d '' f; do
    disabled_in_file "$f"
  done < <(find "$mcp" -type f -name '*.ts' ! -name '*.test.ts' -print0) | LC_ALL=C sort -u
}

disabled="$(disabled_set)"
if [ -z "$disabled" ]; then
  echo "gen-tools-manifest: no tool is registered through ${DISABLE_HELPER} under $mcp — the disabled-tool switch (hadron-server#3082) was not found or disables nothing. Refusing to print a manifest that would list disabled tools as live; update DISABLE_HELPER if the server renamed it" >&2
  exit 2
fi
if [ -n "${GEN_TOOLS_PRINT_DISABLED:-}" ]; then
  printf '%s\n' "$disabled" >&2
fi

# Fixed header — kept in the generator (not the Makefile) so the committed file
# is byte-for-byte reproducible and `make tools-manifest-check` can diff it.
cat <<'HEADER'
# Registered hadron_* tool names — the union of hadron-server's two tool
# registries (MCP server.tool() across src/mcp/ + runner RunToolDef), minus the MCP
# tools the server disables through its offTeamTool switch (hadron-server#3082;
# cli#1511). GENERATED, do not edit
# by hand: run 'make tools-manifest'. Embedded into 'hadron spec check-tools',
# which flags any hadron_* token in a spec that is neither in this list nor in
# mcp-tools-ignore.txt. CI's tools-manifest-check fails if this drifts from
# hadron-server (the drift that let h-* shorthand rot silently — issue #240).
#
HEADER

# Byte collation is part of the generated artifact, independent of caller locale.
{
  # MCP tools: the name is the first argument to server.tool(, on the line that
  # immediately follows the call opener (or on the same line). Every non-test
  # module under src/mcp/ is read, nested ones included, because registration
  # is split across modules (hadron-cli#901). Test files register fixtures, not
  # tools. Accept single or double quotes and digits in the name; `|| :` so a
  # zero-match grep doesn't abort under `set -o pipefail` (an actually-empty
  # result is caught downstream — the embed-sanity test and the drift diff both
  # fail loudly on an empty manifest).
  find "$mcp" -type f -name '*.ts' ! -name '*.test.ts' -print0 \
    | xargs -0 grep -h -A1 -E 'server\.tool\(' \
    | grep -oE "['\"]hadron_[a-z0-9_]+['\"]" | tr -d "'\"" \
    | LC_ALL=C sort -u | LC_ALL=C comm -23 - <(printf '%s\n' "$disabled") || :

  # Runner tools: the RunToolDef.name field. (Non-hadron_ runner/integration
  # tools — e.g. ha__*, twilio_* — are out of scope: a spec can only drift on a
  # hadron_* token.)
  if [ -d "$runner" ]; then
    grep -rhoE "name:[[:space:]]*['\"]hadron_[a-z0-9_]+['\"]" "$runner" | grep -oE "hadron_[a-z0-9_]+" || :
  fi
} | LC_ALL=C sort -u
