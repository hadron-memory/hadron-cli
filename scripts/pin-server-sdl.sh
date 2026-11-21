#!/usr/bin/env bash
# Reproduce the hadron-server revision schema/schema.graphql was exported from
# (hadron-cli#2043; retire with #2658): the merge of the two commits named in
# schema/SOURCE (base= and head=). A checkout of "main" would not do: it carries
# S2, which the committed old-shape decision operations cannot typecheck against.
#
# An optional overlay (schema/SOURCE: overlay=, pre_overlay_sdl_sha256=) is a
# unified diff applied to the exported SDL before sdl_sha256/sdl_lines are
# checked, for a field whose server commit cannot be merged into the pin without
# S2 (cli#2727: GrantSigningRequest.lastRefusedAt, hadron-server#4581). It is
# retired with the pin (#2658). No overlay= key means no overlay.
#
# usage: scripts/pin-server-sdl.sh <server-git-checkout> <out-dir> [--verify]
#   <server-git-checkout>  a git checkout of hadron-server (a sparse, blobless
#                          one is enough: only the SDL source is read)
#   <out-dir>              where to put the pinned worktree (must not exist).
#                          Relative paths are normalised against the CWD once,
#                          up front: `git -C` resolves a relative worktree path
#                          against the repo, `[ -e ]` and the cleanup trap
#                          against the CWD, and the trap must not miss (#2696).
#   --verify               any argument position. After the merge: export the
#                          SDL from the worktree and check it against
#                          sdl_sha256/sdl_lines/merge_sha in schema/SOURCE and
#                          against the committed snapshot (cli#2679). Needs
#                          node+npm: typeDefs.ts is a self-contained SDL
#                          string, so a throwaway install of tsx + graphql —
#                          the exporter's only runtime deps, same trick as the
#                          schema-drift job — is enough; no server pnpm install.
#                          The install goes to a scratch dir OUTSIDE the
#                          worktree (npm --prefix), so the worktree stays the
#                          clean merged tree, and a failed verify keeps the
#                          exported SDL for inspection and prints its path.
# env: HADRON_SERVER_FETCH_TOKEN  optional; when set, the two commits are
#      fetched from origin with it before the worktree is made (CI). The
#      credential is scoped to the git calls: the derived git header AND the
#      variable itself are unset again right after the merge, before any npm
#      command runs (#2696 review: --ignore-scripts stops lifecycle scripts,
#      but tsx and graphql still execute, so they must not inherit the token
#      in their environment either).
#
# Determinism: the merge runs as --no-ff with hooks and commit signing off
# (--no-verify, commit.gpgsign=false) under a fixed author and committer
# identity AND fixed author/committer dates, so parents, tree, identity and
# dates are all stable — the merge commit SHA is byte-identical on every run.
# schema/SOURCE records it as merge_sha= and --verify asserts it. (Before
# #2679 the dates were left at "now", so the SHA changed whenever two runs did
# not land in the same clock second; only the merged tree was deterministic.)
set -euo pipefail

usage='usage: pin-server-sdl.sh <server-git-checkout> <out-dir> [--verify]'
repo= out= verify=
positional=()
for arg in "$@"; do
  case $arg in
    --verify) verify=1 ;;
    -*) echo "unknown option: $arg" >&2; echo "$usage" >&2; exit 2 ;;
    *) positional+=("$arg") ;;
  esac
done
[ "${#positional[@]}" -eq 2 ] || { echo "$usage" >&2; exit 2; }
repo=${positional[0]}
out=${positional[1]}
here=$(cd "$(dirname "$0")/.." && pwd)
base=$(sed -n 's/^base=//p' "$here/schema/SOURCE")
head=$(sed -n 's/^head=//p' "$here/schema/SOURCE")
# The exact tool versions sdl_sha256/sdl_lines in schema/SOURCE were produced
# with (#2696 review: a floating graphql minor can change printSchema bytes and
# trip the sha for a non-pin reason). They live in SOURCE as tsx=/graphql= —
# the single place: the schema-drift job's SDL_EXPORT interpolates the same
# keys, so a bump is one edit plus a re-run of --verify.
pin_tsx=$(sed -n 's/^tsx=//p' "$here/schema/SOURCE")
pin_graphql=$(sed -n 's/^graphql=//p' "$here/schema/SOURCE")
[ -n "$base" ] && [ -n "$head" ] || { echo "schema/SOURCE names no base/head" >&2; exit 2; }
[ -n "$pin_tsx" ] && [ -n "$pin_graphql" ] || { echo "schema/SOURCE names no tsx=/graphql= tool pins" >&2; exit 2; }
case $out in
  /*) ;;
  *) out=$PWD/$out ;;
esac
[ ! -e "$out" ] || { echo "$out already exists" >&2; exit 2; }

# A failed merge or sparse checkout used to leave the worktree registered at
# $out with partial state, and a rerun refused with "already exists" (#2679).
# Remove it on any non-zero exit; on success this is a no-op.
trap 'rc=$?; if [ $rc -ne 0 ] && [ -e "$out" ]; then git -C "$repo" worktree remove --force "$out" 2>/dev/null || git -C "$repo" worktree prune; fi' EXIT

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1 # macOS/BSD: sha256sum is GNU-only
  fi
}

# CI authenticates every git call below (the fetch and any lazy blob fetch the
# checkout and merge trigger in a blobless clone) with the same header. The
# credential — header and variable both — is unset again once no git call is
# left (see above); scripts/pinserverdl pins it.
scrub_auth=
if [ -n "${HADRON_SERVER_FETCH_TOKEN:-}" ]; then
  auth=$(printf 'x-access-token:%s' "$HADRON_SERVER_FETCH_TOKEN" | base64 | tr -d '\n')
  export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0="http.https://github.com/.extraheader" GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth"
  git -C "$repo" fetch --quiet --no-tags --filter=blob:none origin "$base" "$head"
  scrub_auth=1
fi
for c in "$base" "$head"; do
  git -C "$repo" cat-file -e "$c^{commit}" 2>/dev/null || { echo "commit $c is not in $repo (fetch it first)" >&2; exit 2; }
done
# The pin only works while both commits stay fetchable (#2679): they are on
# origin/main today, but a future re-pin against an unmerged branch would put
# that commit one branch-deletion away from unreachable. Warn, do not fail —
# the reproduction above still works from any checkout that has them. A missing
# origin/main ref (a clone fetched only by SHA) is its own warning, not a
# spurious "not an ancestor" one.
if git -C "$repo" rev-parse -q --verify origin/main >/dev/null 2>&1; then
  for c in "$base" "$head"; do
    if ! git -C "$repo" merge-base --is-ancestor "$c" origin/main 2>/dev/null; then
      echo "warning: $c is not reachable from origin/main — if the ref carrying it is deleted, fetching it by SHA will stop working" >&2
    fi
  done
else
  echo "warning: origin/main is not present in $repo — cannot check that the pinned commits stay reachable" >&2
fi
# Only the SDL source is read; materialise just that (a blobless clone would
# otherwise fetch the whole tree).
git -C "$repo" worktree add --quiet --detach --no-checkout "$out" "$base"
git -C "$out" sparse-checkout init --no-cone
git -C "$out" sparse-checkout set 'scripts/export-graphql-sdl.mjs' 'src/api/graphql/schema/typeDefs.ts'
git -C "$out" checkout --quiet "$base"
# The fixed date is arbitrary but must never change: it is part of the merge
# commit's identity, so changing it changes the SHA every reproduction prints.
pin_date='2026-11-19T00:00:00Z'
GIT_COMMITTER_NAME=schema-pin GIT_COMMITTER_EMAIL=schema-pin@example.invalid GIT_COMMITTER_DATE="$pin_date" \
GIT_AUTHOR_NAME=schema-pin GIT_AUTHOR_EMAIL=schema-pin@example.invalid GIT_AUTHOR_DATE="$pin_date" \
  git -C "$out" -c commit.gpgsign=false merge --quiet --no-edit --no-ff --no-verify "$head" >&2
merge_sha=$(git -C "$out" rev-parse HEAD)
# No git call needs the credential past the merge; npm must never see it —
# not the derived header, and not HADRON_SERVER_FETCH_TOKEN itself (unsetting
# only the header leaves the raw token in npm's, and so tsx's and graphql's,
# environment: #2696 review).
if [ -n "$scrub_auth" ]; then
  unset GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0 HADRON_SERVER_FETCH_TOKEN auth scrub_auth
fi
want_merge_sha=$(sed -n 's/^merge_sha=//p' "$here/schema/SOURCE")
if [ -n "$want_merge_sha" ] && [ "$merge_sha" != "$want_merge_sha" ] && [ -z "$verify" ]; then
  echo "warning: merge produced $merge_sha but schema/SOURCE says merge_sha=$want_merge_sha — the reproduction is not byte-identical (the tree may still be correct)" >&2
fi

if [ -n "$verify" ]; then
  want_sha=$(sed -n 's/^sdl_sha256=//p' "$here/schema/SOURCE")
  want_lines=$(sed -n 's/^sdl_lines=//p' "$here/schema/SOURCE")
  ok=1
  [ "$merge_sha" = "$want_merge_sha" ] || { echo "merge_sha mismatch: SOURCE says $want_merge_sha, the merge produced $merge_sha" >&2; ok=0; }
  # Outside the worktree: the install must not walk up into some ancestor
  # package.json (#2696), and the worktree must not end up with node_modules.
  # The exporter imports `graphql` (resolved from ITS own directory) and
  # `../src/...` (relative to itself), so it still runs from the worktree with
  # node_modules symlinked in for the duration of the export and removed right
  # after — a failed run takes the symlink down with the worktree (trap).
  vtmp=$(mktemp -d "${TMPDIR:-/tmp}/pin-sdl-verify.XXXXXX")
  # A failing install or export must say WHERE it kept its scratch dir, not
  # just die (#2696 review): set -e alone would exit silently with $vtmp
  # leaked. The worktree trap still fires on these exits.
  npm install --silent --no-save --no-package-lock --ignore-scripts --prefix "$vtmp" "tsx@$pin_tsx" "graphql@$pin_graphql" 1>&2 ||
    { echo "npm install failed — the scratch dir is kept for inspection at $vtmp" >&2; exit 1; }
  ln -s "$vtmp/node_modules" "$out/node_modules"
  ( cd "$out" && "$vtmp/node_modules/.bin/tsx" scripts/export-graphql-sdl.mjs ) > "$vtmp/exported.graphql" ||
    { echo "the exporter failed — its scratch dir is kept for inspection at $vtmp" >&2; exit 1; }
  rm "$out/node_modules"
  overlay=$(sed -n 's/^overlay=//p' "$here/schema/SOURCE")
  if [ -n "$overlay" ]; then
    want_pre=$(sed -n 's/^pre_overlay_sdl_sha256=//p' "$here/schema/SOURCE")
    pre_sha=$(sha256 "$vtmp/exported.graphql")
    [ "$pre_sha" = "$want_pre" ] || { echo "pre_overlay_sdl_sha256 mismatch: SOURCE says $want_pre, the merged export is $pre_sha" >&2; ok=0; }
    patch --quiet "$vtmp/exported.graphql" < "$here/$overlay" ||
      { echo "overlay $overlay does not apply to the export — kept at $vtmp" >&2; exit 1; }
  fi
  got_sha=$(sha256 "$vtmp/exported.graphql")
  got_lines=$(wc -l < "$vtmp/exported.graphql" | tr -d ' ')
  [ "$got_sha" = "$want_sha" ] || { echo "sdl_sha256 mismatch: SOURCE says $want_sha, export is $got_sha" >&2; ok=0; }
  [ "$got_lines" = "$want_lines" ] || { echo "sdl_lines mismatch: SOURCE says $want_lines, export is $got_lines" >&2; ok=0; }
  if ! diff -u "$here/schema/schema.graphql" "$vtmp/exported.graphql" >&2; then
    echo "exported SDL differs from the committed snapshot (see diff above)" >&2; ok=0
  fi
  if [ $ok = 1 ]; then
    rm -rf "$vtmp"
    echo "verified: merge_sha, sdl_sha256, sdl_lines and the committed snapshot all match" >&2
  else
    # Keep the export (and the failed diff's context) for inspection; the trap
    # removes only the worktree.
    echo "verify failed — the exported SDL is kept for inspection at $vtmp/exported.graphql" >&2
    exit 1
  fi
fi
echo "$out"
