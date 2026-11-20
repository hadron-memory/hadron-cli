#!/usr/bin/env bash
# Reproduce the hadron-server revision schema/schema.graphql was exported from
# (hadron-cli#2043; retire with #2658): the merge of the two commits named in
# schema/SOURCE. A checkout of "main" would not do: it carries S2, which the
# committed old-shape decision operations cannot typecheck against.
#
# usage: scripts/pin-server-sdl.sh <server-git-checkout> <out-dir>
#   <server-git-checkout>  a git checkout of hadron-server (a sparse, blobless
#                          one is enough: only the SDL source is read)
#   <out-dir>              where to put the pinned worktree (must not exist)
# env: HADRON_SERVER_FETCH_TOKEN  optional; when set, the two commits are
#      fetched from origin with it before the worktree is made (CI).
# Prints the out-dir; the merge has a fixed committer, so it is deterministic.
set -euo pipefail
repo=${1:?usage: pin-server-sdl.sh <server-git-checkout> <out-dir>}
out=${2:?usage: pin-server-sdl.sh <server-git-checkout> <out-dir>}
here=$(cd "$(dirname "$0")/.." && pwd)
base=$(sed -n 's/^base=//p' "$here/schema/SOURCE")
merge=$(sed -n 's/^merge=//p' "$here/schema/SOURCE")
[ -n "$base" ] && [ -n "$merge" ] || { echo "schema/SOURCE names no base/merge" >&2; exit 2; }
[ ! -e "$out" ] || { echo "$out already exists" >&2; exit 2; }

# CI authenticates every git call below (the fetch and any lazy blob fetch the
# checkout and merge trigger in a blobless clone) with the same header.
if [ -n "${HADRON_SERVER_FETCH_TOKEN:-}" ]; then
  auth=$(printf 'x-access-token:%s' "$HADRON_SERVER_FETCH_TOKEN" | base64 -w0)
  export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0="http.https://github.com/.extraheader" GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth"
  git -C "$repo" fetch --quiet --no-tags --filter=blob:none origin "$base" "$merge"
fi
for c in "$base" "$merge"; do
  git -C "$repo" cat-file -e "$c^{commit}" 2>/dev/null || { echo "commit $c is not in $repo (fetch it first)" >&2; exit 2; }
done
# Only the SDL source is read; materialise just that (a blobless clone would
# otherwise fetch the whole tree).
git -C "$repo" worktree add --quiet --detach --no-checkout "$out" "$base"
git -C "$out" sparse-checkout init --no-cone
git -C "$out" sparse-checkout set 'scripts/export-graphql-sdl.mjs' 'src/api/graphql/schema/typeDefs.ts'
git -C "$out" checkout --quiet "$base"
GIT_COMMITTER_NAME=schema-pin GIT_COMMITTER_EMAIL=schema-pin@example.invalid \
GIT_AUTHOR_NAME=schema-pin GIT_AUTHOR_EMAIL=schema-pin@example.invalid \
  git -C "$out" merge --quiet --no-edit "$merge" >&2
echo "$out"
