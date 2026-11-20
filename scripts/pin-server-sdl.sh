#!/usr/bin/env bash
# Reproduce the hadron-server revision schema/schema.graphql was exported from
# (hadron-cli#2043; retire with #2658): the merge of the two commits named in
# schema/SOURCE (base= and head=). A checkout of "main" would not do: it carries
# S2, which the committed old-shape decision operations cannot typecheck against.
#
# usage: scripts/pin-server-sdl.sh <server-git-checkout> <out-dir> [--verify]
#   <server-git-checkout>  a git checkout of hadron-server (a sparse, blobless
#                          one is enough: only the SDL source is read)
#   <out-dir>              where to put the pinned worktree (must not exist)
#   --verify               after the merge, export the SDL from the worktree and
#                          check it against sdl_sha256/sdl_lines in schema/SOURCE
#                          and against the committed snapshot (cli#2679). Needs
#                          node+npm: typeDefs.ts is a self-contained SDL string,
#                          so a throwaway install of tsx + graphql@16 — the
#                          exporter's only runtime deps, same trick as the
#                          schema-drift job — is enough; no server pnpm install.
# env: HADRON_SERVER_FETCH_TOKEN  optional; when set, the two commits are
#      fetched from origin with it before the worktree is made (CI).
#
# Determinism: the merge runs with a fixed author and committer identity AND
# fixed author/committer dates, so parents, tree, identity and dates are all
# stable — the merge commit SHA is byte-identical on every run. (Before #2679
# the dates were left at "now", so the SHA changed whenever two runs did not
# land in the same clock second; only the merged tree was deterministic.)
set -euo pipefail
repo=${1:?usage: pin-server-sdl.sh <server-git-checkout> <out-dir> [--verify]}
out=${2:?usage: pin-server-sdl.sh <server-git-checkout> <out-dir> [--verify]}
verify=${3:-}
[ "$verify" = "--verify" ] || [ -z "$verify" ] || { echo "unknown option: $verify" >&2; exit 2; }
here=$(cd "$(dirname "$0")/.." && pwd)
base=$(sed -n 's/^base=//p' "$here/schema/SOURCE")
head=$(sed -n 's/^head=//p' "$here/schema/SOURCE")
[ -n "$base" ] && [ -n "$head" ] || { echo "schema/SOURCE names no base/head" >&2; exit 2; }
[ ! -e "$out" ] || { echo "$out already exists" >&2; exit 2; }

# A failed merge or sparse checkout used to leave the worktree registered at
# $out with partial state, and a rerun refused with "already exists" (#2679).
# Remove it on any non-zero exit; on success this is a no-op.
trap 'rc=$?; if [ $rc -ne 0 ] && [ -e "$out" ]; then git -C "$repo" worktree remove --force "$out" 2>/dev/null || git -C "$repo" worktree prune; fi' EXIT

# CI authenticates every git call below (the fetch and any lazy blob fetch the
# checkout and merge trigger in a blobless clone) with the same header.
if [ -n "${HADRON_SERVER_FETCH_TOKEN:-}" ]; then
  auth=$(printf 'x-access-token:%s' "$HADRON_SERVER_FETCH_TOKEN" | base64 | tr -d '\n')
  export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0="http.https://github.com/.extraheader" GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth"
  git -C "$repo" fetch --quiet --no-tags --filter=blob:none origin "$base" "$head"
fi
for c in "$base" "$head"; do
  git -C "$repo" cat-file -e "$c^{commit}" 2>/dev/null || { echo "commit $c is not in $repo (fetch it first)" >&2; exit 2; }
done
# The pin only works while both commits stay fetchable (#2679): they are on
# origin/main today, but a future re-pin against an unmerged branch would put
# that commit one branch-deletion away from unreachable. Warn, do not fail —
# the reproduction above still works from any checkout that has them.
for c in "$base" "$head"; do
  if ! git -C "$repo" merge-base --is-ancestor "$c" origin/main 2>/dev/null; then
    echo "warning: $c is not reachable from origin/main — if the ref carrying it is deleted, fetching it by SHA will stop working" >&2
  fi
done
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
  git -C "$out" merge --quiet --no-edit "$head" >&2

if [ "$verify" = "--verify" ]; then
  want_sha=$(sed -n 's/^sdl_sha256=//p' "$here/schema/SOURCE")
  want_lines=$(sed -n 's/^sdl_lines=//p' "$here/schema/SOURCE")
  ( cd "$out" && npm install --no-save --no-package-lock --silent tsx graphql@16 1>&2 && node_modules/.bin/tsx scripts/export-graphql-sdl.mjs ) > "$out/exported.graphql"
  got_sha=$(sha256sum "$out/exported.graphql" | cut -d' ' -f1)
  got_lines=$(wc -l < "$out/exported.graphql" | tr -d ' ')
  ok=1
  [ "$got_sha" = "$want_sha" ] || { echo "sdl_sha256 mismatch: SOURCE says $want_sha, export is $got_sha" >&2; ok=0; }
  [ "$got_lines" = "$want_lines" ] || { echo "sdl_lines mismatch: SOURCE says $want_lines, export is $got_lines" >&2; ok=0; }
  if ! diff -u "$here/schema/schema.graphql" "$out/exported.graphql" >&2; then
    echo "exported SDL differs from the committed snapshot (see diff above)" >&2; ok=0
  fi
  [ $ok = 1 ] || exit 1
  echo "verified: export matches sdl_sha256, sdl_lines and the committed snapshot" >&2
fi
echo "$out"
