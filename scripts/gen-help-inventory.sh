#!/usr/bin/env bash
# Generate docs/help-inventory.md (#1050): one row per command PAGE of the real
# command tree, found by walking `hadron <path> --help` from the root, so the
# inventory is what a user sees and not what the source says. Prints the file to
# stdout; the Makefile `help-inventory` target writes it through
# write-if-produced.sh and `help-inventory-check` diffs it for drift.
#
# Inputs (environment):
#   HADRON_BIN                 the binary to walk (default bin/hadron; `make` builds it)
#   HELP_INVENTORY_EXISTING    the committed inventory, whose HUMAN columns
#                              (evaluator, result, finding) are carried over by
#                              path while help is unchanged; the generator owns every other column
#   HELP_INVENTORY_GROUPS      the group map (default scripts/help-inventory-groups.txt)
#
# Page = one command or group, not one flag: a flag is audited inside its page's
# row. Aliases are a column of the target's row. help and completion are listed
# (a user reads them) marked `generated` and are not walked below the top level.
# Hidden and deprecated commands do not appear in --help, so they are not
# inventoried.
set -euo pipefail

BIN="${HADRON_BIN:-bin/hadron}"
EXISTING="${HELP_INVENTORY_EXISTING:-}"
GROUPS_FILE="${HELP_INVENTORY_GROUPS:-scripts/help-inventory-groups.txt}"

[ -x "$BIN" ] || { echo "gen-help-inventory: $BIN not found or not executable (run 'make build', or set HADRON_BIN)" >&2; exit 2; }
[ -f "$GROUPS_FILE" ] || { echo "gen-help-inventory: $GROUPS_FILE not found" >&2; exit 2; }

# Help must not depend on, or touch, the caller's configuration or credentials.
cfg=$(mktemp -d)
rows=$(mktemp)
trap 'rm -rf "$cfg" "$rows"' EXIT
export XDG_CONFIG_HOME="$cfg" HOME="$cfg"
# Help hashes must not inherit locale or terminal-width choices from the
# developer's shell. Capture every page in the C locale at 80 columns.
export LC_ALL=C COLUMNS=80
unset HADRON_TOKEN HADRON_SERVER || :

# Hash the captured help, not structural metadata or the binary's version.
# macOS provides shasum rather than sha256sum.
if command -v sha256sum >/dev/null 2>&1; then
  help_hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  help_hash_tool=shasum
else
  echo "gen-help-inventory: sha256sum or shasum is required" >&2
  exit 1
fi

hash_help() {
  if [ "$help_hash_tool" = sha256sum ]; then
    printf '%s' "$1" | sha256sum | cut -c1-12
  else
    printf '%s' "$1" | shasum -a 256 | cut -c1-12
  fi
}

help_of() { # $@ = command path words; empty = root
  "$BIN" "$@" --help 2>&1 || { echo "gen-help-inventory: '$BIN $* --help' failed" >&2; return 1; }
}

# walk <top-level> <path words...>: append one TSV row, then recurse.
walk() {
  local top=$1; shift
  local out name aliases examples generated="" child help_hash
  out=$(help_of "$@") || exit 1
  help_hash=$(hash_help "$out") || exit 1
  name="${*:-(root)}"
  aliases=$(printf '%s\n' "$out" | awk '/^Aliases:/ && !seen {getline; print; seen=1}' | sed 's/^ *//' | tr -d '\r')
  # The Aliases line lists the command's own name too; keep only the others.
  local self="${*: -1}" kept="" a
  IFS=',' read -ra parts <<<"$aliases"
  for a in "${parts[@]:-}"; do
    a=$(printf '%s' "$a" | sed 's/^ *//;s/ *$//')
    [ -n "$a" ] && [ "$a" != "$self" ] && kept="${kept:+$kept, }$a"
  done
  # Drain the producer: grep -q (and early awk exits) can cause printf to
  # receive SIGPIPE on large pages; pipefail would turn a match into a miss.
  if printf '%s\n' "$out" | grep '^Examples:$' >/dev/null; then examples=y; else examples=n; fi
  case "$name" in help|completion) generated=generated ;; esac
  # "-" stands for an empty cell: read with a tab IFS would collapse empty fields.
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$top" "$name" "${kept:--}" "${generated:--}" "$examples" "$help_hash" >>"$rows"
  # The root page is one row; its children are walked from their own top level.
  case "$name" in help|completion|"(root)") return 0 ;; esac
  for child in $(printf '%s\n' "$out" | awk '/^Available Commands:/ && !seen {f=1;seen=1;next} f&&/^$/{f=0} f&&/^  [^ ]/{print $1}'); do
    walk "$top" "$@" "$child"
  done
}

walk "(root)"
top_help=$(help_of)
for t in $(printf '%s\n' "$top_help" | awk '/^Available Commands:/ && !seen {f=1;seen=1;next} f&&/^$/{f=0} f&&/^  [^ ]/{print $1}'); do
  walk "$t" "$t"
done

# Human columns by path, from the committed file.
humans=$(mktemp)
trap 'rm -rf "$cfg" "$rows" "$humans"' EXIT
if [ -n "$EXISTING" ] && [ -f "$EXISTING" ]; then
  awk -F' \\| ' '/^\|[[:space:]]*`/ {
    # Normalize only Markdown padding OUTSIDE the backticked path. The human
    # cells below remain byte-for-byte text, including their own whitespace.
    p=$1; sub(/^\|[[:space:]]*`/,"",p); sub(/`[[:space:]]*$/,"",p)
    if (NF == 8) { h=""; e=$6; r=$7; f=$8 }
    else if (NF == 9) { h=$6; e=$7; r=$8; f=$9 }
    else { print "gen-help-inventory: unsupported inventory row shape: " $0 > "/dev/stderr"; exit 1 }
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", h)
    sub(/ \|$/,"",f)
    print p "\t" h "\t" e "\t" r "\t" f
  }' "$EXISTING" >"$humans"
fi

cat <<HEADER
# Help inventory

The command-tree inventory for the help audit (#826, #1050): one row per
command page, read from \`hadron <command> --help\`. GENERATED by
\`scripts/gen-help-inventory.sh\`; run \`make help-inventory\` to refresh it and
\`make help-inventory-check\` to see drift. The rubric every page is judged
against is [help-review-rubric.md](help-review-rubric.md).

Rules for what is a row:

- One row per command page (a command or a group), not per flag. A page's flags
  are audited inside its row.
- Aliases are a column of the target's row, not rows of their own.
- \`help\` and \`completion\` are listed and marked \`generated\`; only their
  top-level text is audited.
- Global flags (\`--app\`, \`--json\`, \`--server\`, \`-v\`) are one rubric item on
  the \`(root)\` row, not repeated per row.
- hidden and deprecated commands are not inventoried: they do not appear in
  \`--help\`.

Columns: \`path\`, \`aliases\`, \`generated\`, \`has-examples\`, \`child\` (the audit
issue that owns the page: the group section's issue for every row in that
section, including a command a later issue added to the group — the
implementing issue is cited in that row's finding link once assessed, not in
\`child\`), \`help-hash\` (the first 12 hex characters of the
sha256 of captured help text), then the three HUMAN columns \`evaluator\`, \`result\`
(\`unread\` | \`pass\` | \`fail\`; \`unread\` is never a pass; \`pass\` needs a named
evaluator who is not the author of the text) and \`finding\` (a link to the
assessment). The help-hash tracks the page's help text only, not the code
behind it: it is not a last-reviewed code state, and a row resets only when that
text changes.
The generator keeps these three by path only while the page's
help-hash is unchanged. A changed hash clears evaluator and finding and resets
both pass and fail to unread; a child that fixes help must re-assess the row.
For first adoption, a missing or empty hash is seeded from current help while
keeping the prior assessment; changes before adoption cannot be detected.
New pages start unread. Do not put a \`|\` in a finding cell. Each child edits only its own
section between the markers.
HEADER

emit_group() { # id title tops...
  local id=$1 title=$2; shift 2
  local child="#$id"; [ "$id" = unassigned ] && child="-"
  local heading="## Group: $title"; [ "$id" != unassigned ] && heading="$heading ($child)"
  local any=0 t
  local body
  body=$(
    for t in "$@"; do
      awk -F'\t' -v top="$t" '$1==top' "$rows"
    done | LC_ALL=C sort -t$'\t' -k2,2 | while IFS=$'\t' read -r top path aliases generated examples help_hash; do
      [ "$aliases" != "-" ] || aliases=""
      [ "$generated" != "-" ] || generated=""
      ev="" res="unread" fin=""
      h=$(awk -F'\t' -v p="$path" '$1==p {print; exit}' "$humans")
      if [ -n "$h" ]; then
        prior_hash=$(printf '%s' "$h" | cut -f2)
        # A legacy/empty hash seeds today's text without discarding prior
        # assessments. A present, changed hash invalidates pass AND fail.
        if [ -z "$prior_hash" ] || [ "$prior_hash" = "$help_hash" ]; then
          ev=$(printf '%s' "$h" | cut -f3); res=$(printf '%s' "$h" | cut -f4); fin=$(printf '%s' "$h" | cut -f5)
          [ -n "$res" ] || res=unread
        fi
      fi
      printf '| `%s` | %s | %s | %s | %s | %s | %s | %s | %s |\n' "$path" "$aliases" "$generated" "$examples" "$child" "$help_hash" "$ev" "$res" "$fin"
    done
  )
  [ -n "$body" ] || return 0
  printf '\n%s\n\n<!-- group:%s start -->\n| path | aliases | generated | has-examples | child | help-hash | evaluator | result | finding |\n|---|---|---|---|---|---|---|---|---|\n%s\n<!-- group:%s end -->\n' "$heading" "$id" "$body" "$id"
}

claimed=" "
while IFS='|' read -r id title tops; do
  case "$id" in ''|'#'*) continue ;; esac
  # shellcheck disable=SC2086
  emit_group "$id" "$title" $tops
  claimed="$claimed$tops "
done <"$GROUPS_FILE"

unassigned=""
for t in $(cut -f1 "$rows" | LC_ALL=C sort -u); do
  case "$claimed" in *" $t "*) ;; *) unassigned="$unassigned $t" ;; esac
done
# shellcheck disable=SC2086
[ -z "$unassigned" ] || emit_group unassigned "Unassigned (no audit child names these commands)" $unassigned
