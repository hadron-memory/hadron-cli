#!/usr/bin/env bash
# Capture a generator's stdout to a committed file — and leave that file ALONE
# unless the generator actually produced something (hadron-cli#555).
#
# THE DEFECT THIS EXISTS FOR. `cmd > committed-file` opens the redirect BEFORE
# the command runs, so any failure leaves the file at ZERO BYTES:
#
#     cd $(HADRON_SERVER_DIR) && $(SDL_EXPORT) > schema/schema.graphql
#
# `make` reports `Error 1` and says nothing about the artifact it just emptied.
# The recovery is one `git checkout --` away, which is easy *once you know the
# file is gone* — and nothing tells you. It cost a real snapshot: the exporter
# exits 1 with NO output on either stream inside a git worktree of the server
# (`pnpm -s` swallows it), which is exactly the throwaway-checkout path #503
# pushes people onto, so the failure is likeliest where the guard is absent.
#
# A ZERO-EXIT generator that prints NOTHING is the same disaster wearing a green
# exit code, and that one is not hypothetical either — it is what a silenced
# runner does when its dependencies are missing. So emptiness is refused too,
# and refused LOUDLY: an empty schema snapshot passes genqlient's file read and
# fails somewhere far less obviously connected.
#
# `schema-check` already got this right with a mktemp backup and an EXIT trap.
# The targets that are SUPPOSED to write are the ones that had no guard at all.
#
# THE COMMAND IS A SHELL FRAGMENT, NOT AN ARGV LIST, and that is load-bearing
# (@codex P1). `SDL_EXPORT` has always been shell-expanded — the nightly
# schema-drift workflow passes
#
#     npm install --silent tsx graphql@16 1>&2 && node_modules/.bin/tsx ...
#
# where `1>&2` and `&&` are SHELL SYNTAX. The first draft of this script ran
# "$@" directly, which made them literal arguments to `npm`: the exporter never
# ran, the wrapper saw empty output, and every scheduled drift check would have
# failed for a reason unrelated to drift — on a `continue-on-error` job, i.e.
# quietly. Preserving an interface means preserving how its values are PARSED,
# not only what they are named.
#
# Usage:
#   write-if-produced.sh <destination> [-C <dir>] -- <shell command>
set -euo pipefail

usage() {
  echo "usage: write-if-produced.sh <destination> [-C <dir>] -- <shell command>" >&2
  exit 2
}

[ "$#" -ge 1 ] || usage
dest=$1
shift

workdir=""
if [ "${1:-}" = "-C" ]; then
  [ "$#" -ge 2 ] || usage
  workdir=$2
  shift 2
fi

[ "${1:-}" = "--" ] || usage
shift
[ "$#" -ge 1 ] || usage

# An explicit template rather than a bare `mktemp`. It works bare on this
# macOS (measured: /usr/bin/mktemp, BSD, returns a path) so @copilot's premise
# that it fails there is not true today — but the template costs nothing, and
# older BSD userlands did require one. A free guard against a question nobody
# should have to re-ask.
tmp=$(mktemp "${TMPDIR:-/tmp}/write-if-produced.XXXXXXXX")
# Cleans up on every exit path INCLUDING the success one, where the file has
# already been moved and `rm -f` is simply a no-op.
trap 'rm -f "$tmp"' EXIT

# Joined back into one string, so an UNQUOTED `$(SDL_EXPORT)` in a recipe still
# arrives whole — make word-splits it either way, and the old bare redirect
# re-parsed exactly this text through a shell.
cmd="$*"

# The subshell keeps the `cd` from leaking, and `set -e` is deliberately NOT
# relied on here: the point is to catch the failure rather than to inherit it.
if ! ( if [ -n "$workdir" ]; then cd "$workdir"; fi; eval "$cmd" ) > "$tmp"; then
  echo "✗ $dest: the generator failed — the file is UNCHANGED." >&2
  printf '  command: %s%s\n' "${workdir:+(in $workdir) }" "$cmd" >&2
  echo "  Run it by hand to see why; a silenced runner (pnpm -s) can exit non-zero with no output at all." >&2
  exit 1
fi

if [ ! -s "$tmp" ]; then
  echo "✗ $dest: the generator SUCCEEDED but produced nothing — the file is UNCHANGED." >&2
  printf '  command: %s%s\n' "${workdir:+(in $workdir) }" "$cmd" >&2
  echo "  An empty artifact is worse than a failed one: it is committable, and it breaks somewhere else." >&2
  exit 1
fi

mv "$tmp" "$dest"
# mktemp's 0600 is not what a committed file wants; the destination is read by
# everything and written by this script alone.
chmod 644 "$dest"
