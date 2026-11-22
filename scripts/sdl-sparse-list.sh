#!/usr/bin/env bash
# sdl-sparse-list.sh <manifest> [<own-entry>...] — print the sparse-checkout list of the schema-drift job's
# hadron-server checkout: the entries of the server-owned manifest (scripts/sdl-export-files.txt, the files
# the SDL export imports, hadron-server#4916) followed by the CLI's own entries (the paths its other checks
# read), one per line, each listed once. Exit 2, saying why, when the manifest cannot be trusted (hadron-cli#3021).
#
# The manifest is another repository's file and becomes a sparse-checkout pattern list, so each line must be a
# plain repo-relative path: no absolute path, no `..`, no glob or negation (a pattern would widen the checkout
# beyond the files the export needs), no whitespace. A manifest with no entry at all is refused: an empty list
# would check out nothing and the export would then fail far from its cause.
set -euo pipefail
manifest=${1:?usage: sdl-sparse-list.sh <manifest> [<own-entry>...]}
shift
[ -r "$manifest" ] || { echo "cannot read the SDL-export manifest $manifest" >&2; exit 2; }
declare -A seen=()
out=()
add() {
  local p=$1
  case $p in
    /*) echo "$manifest: $p is an absolute path" >&2; exit 2 ;;
    *..*) echo "$manifest: $p has a parent segment" >&2; exit 2 ;;
    *[\*\?\[\!\ ]* | *$'\t'* | *$'\r'*) echo "$manifest: $p is not a plain repo-relative path (no glob, negation or whitespace)" >&2; exit 2 ;;
  esac
  [ -n "${seen[$p]:-}" ] && return 0
  seen[$p]=1
  out+=("$p")
}
count=0
while IFS= read -r line || [ -n "$line" ]; do
  case $line in '' | '#'*) continue ;; esac
  # a whitespace-only line is blank, not an entry
  [ -z "${line//[[:space:]]/}" ] && continue
  add "$line"
  count=$((count + 1))
done <"$manifest"
[ "$count" -gt 0 ] || { echo "$manifest names no file: refusing an empty sparse list" >&2; exit 2; }
for p in "$@"; do add "$p"; done
printf '%s\n' "${out[@]}"
