#!/usr/bin/env bash
# Report — and, for the default sibling, gate on — the REVISION a checkout is
# standing on, for any target that reads one (hadron-cli#503).
#
# The defect this exists for: `HADRON_SERVER_DIR` names a DIRECTORY, while the
# correctness of everything generated from it depends on a REVISION, and the
# gap between the two is invisible in the output. A sibling sitting on a
# colleague's unmerged branch bakes unreleased server fields into a committed
# snapshot — and it compiles, and the tests pass, and `schema-check` re-exports
# from the same checkout so it agrees with itself
# (findings:make-schema-follows-the-sibling-branch).
#
# @Tove's formulation, from the same defect in hadron-docs#242, is the one to
# keep: A CHECK THAT READS A SIBLING WORKING TREE IS MEASURING SOMEONE'S DESK.
# Her half produced a *changing measurement of an unchanged commit* — 15
# missing commands, then 12 an hour later, with nothing in her repo moving.
#
# THE RULE HERE: setting HADRON_SERVER_DIR yourself is the deliberate signal.
#
#   - default path (../hadron-server) — verified to be at origin/main, and
#     REFUSED otherwise. This is the shared checkout several agents branch in,
#     so it is the one nobody chose and the one that silently drifts.
#   - explicitly set — reported, never gated. CI points at a throwaway clone
#     with no full server install; a human points at a worktree of origin/main,
#     or deliberately at an unmerged branch to test against it. All three are
#     someone taking responsibility for the revision, which is exactly what the
#     default lacks.
#
# Deliberately does NOT fetch. A build target that reaches the network changes
# what `make` means, and a stale `origin/main` ref is reported rather than
# silently refreshed — the point is to make the revision visible, not to pick
# one for you.
set -euo pipefail

dir=${HADRON_SERVER_DIR:-../hadron-server}
explicit=${HADRON_SERVER_DIR+yes}   # set (even to the default) ⇒ deliberate
what=${1:-source}
shift || true                       # remaining args: paths this target reads

if [ ! -d "$dir" ]; then
  echo "✗ $what: no checkout at $dir — set HADRON_SERVER_DIR to one." >&2
  exit 1
fi

head_sha=$(git -C "$dir" rev-parse --short HEAD 2>/dev/null || echo unknown)
branch=$(git -C "$dir" rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
main_sha=$(git -C "$dir" rev-parse --short origin/main 2>/dev/null || echo "")

# Dirt is only reported for the paths this target actually reads. A shared
# checkout is dirty somewhere most of the time, and a guard that fires on the
# documented happy path is not caution — it just teaches people to set the
# override reflexively.
dirty=""
if [ "$#" -gt 0 ]; then
  dirty=$(git -C "$dir" status --porcelain -- "$@" 2>/dev/null | head -5 || true)
fi

state="$branch"
if [ -n "$main_sha" ] && [ "$head_sha" = "$main_sha" ]; then
  state="$branch, == origin/main"
elif [ -z "$main_sha" ]; then
  state="$branch, origin/main unknown"
else
  state="$branch, origin/main is $main_sha"
fi
echo "  $what: $dir @ $head_sha ($state)" >&2
[ -n "$dirty" ] && echo "$dirty" | sed 's/^/    uncommitted: /' >&2

[ -n "$explicit" ] && exit 0        # explicitly chosen ⇒ reported, not gated

fail() {
  echo "" >&2
  echo "✗ $what: refusing to read $dir at this revision." >&2
  echo "$1" >&2
  cat >&2 <<EOF

  A check that reads a sibling WORKING TREE measures someone's desk
  (hadron-cli#503). This checkout is shared, so it is regularly on an unmerged
  branch — and the artifact generated from it would be valid, would compile,
  and would pass the tests, while carrying a contract that is not on main.

  Either:
    git -C $dir fetch origin
    git -C $dir worktree add /tmp/hs-main origin/main
    (cd /tmp/hs-main && npm install --no-save tsx graphql)
    make ${MAKECMDGOALS_HINT:-<target>} HADRON_SERVER_DIR=/tmp/hs-main \\
      SDL_EXPORT='./node_modules/.bin/tsx scripts/export-graphql-sdl.mjs'

  ...or, to use this checkout as it stands — deliberately, e.g. to test against
  an unmerged server branch — name it yourself, which is the signal that you
  chose the revision:

    make ${MAKECMDGOALS_HINT:-<target>} HADRON_SERVER_DIR=$dir

  (Do NOT symlink the sibling's node_modules into a worktree: pnpm treats a
  foreign modules dir as one to purge. A throwaway npm install of tsx+graphql
  takes seconds and touches nothing shared.)
EOF
  exit 1
}

if [ -z "$main_sha" ]; then
  fail "  No origin/main ref — the revision cannot be verified, which is not the same as it being fine."
fi
if [ "$head_sha" != "$main_sha" ]; then
  fail "  HEAD is $head_sha ($branch); origin/main is $main_sha."
fi
if [ -n "$dirty" ]; then
  fail "  Uncommitted changes to the files this reads (listed above)."
fi
