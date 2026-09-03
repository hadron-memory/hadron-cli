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
# THE COMMAND ARRIVES IN THE ENVIRONMENT, NOT IN ARGV, and that is the whole
# interface (@codex P1 then P2, two rounds).
#
# `SDL_EXPORT` has always been a SHELL FRAGMENT. The nightly schema-drift
# workflow passes
#
#     npm install --silent tsx graphql@16 1>&2 && node_modules/.bin/tsx ...
#
# where `1>&2` and `&&` are shell syntax, and an exporter may legitimately
# contain quotes: `printf "%s\n" "type Query { … }"`.
#
# Two drafts got this wrong in two different ways, and both were the SAME
# mistake — changing how a value is PARSED while keeping its name, meaning and
# position:
#
#   1. `-- $cmd` run as "$@": `1>&2` and `&&` became literal arguments to npm,
#      so the exporter never ran and the drift job failed for a reason that had
#      nothing to do with drift — on a continue-on-error job, silently.
#   2. `-- "$(SDL_EXPORT)"` in the recipe: make expands, then the SHELL reparses,
#      and inner quotes are stripped before the script ever sees them. Measured
#      with GNU Make 3.81: `printf "%s\n" "type Query { quoted: String }"`
#      arrives as `printf %sn type Query { quoted: String }` and produces
#      `typenQueryn{nquoted:nStringn}n`.
#
# A target-specific `export` hands the fragment over UNTOUCHED by any shell, and
# `eval` then parses it exactly once — which is precisely what the original bare
# `cmd > file` redirect did. There is deliberately no argv form: a second way to
# pass the command is a second way to get it silently wrong.
#
# Usage:
#   WRITE_IF_PRODUCED_CMD='<shell fragment>' \
#     write-if-produced.sh <destination> [-C <dir>]
set -euo pipefail

usage() {
  echo "usage: WRITE_IF_PRODUCED_CMD='<shell fragment>' write-if-produced.sh <destination> [-C <dir>]" >&2
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
# Nothing else is accepted. A stray `--` or a command in argv is a caller still
# using the shape that lost the quotes, and answering it would be worse than
# refusing: it would produce a plausible wrong artifact.
[ "$#" -eq 0 ] || usage

cmd=${WRITE_IF_PRODUCED_CMD:-}
[ -n "$cmd" ] || usage

# STAGED BESIDE THE DESTINATION, not in $TMPDIR (@codex P2 and @copilot,
# independently) — and this one goes to the script's core promise.
#
# From $TMPDIR the final `mv` can be a CROSS-FILESYSTEM move: a bind-mounted
# workspace, a tmpfs /tmp, a container volume. Cross-device, `mv` is not a
# rename but a copy-then-unlink, and a copy that fails partway — ENOSPC, a size
# limit, an interrupt — leaves the destination TRUNCATED or half-written before
# returning non-zero.
#
# Which is to say: the guard against a zero-byte snapshot could have produced
# one itself, on exactly the machines least like a developer laptop. Same
# directory makes the `mv` a rename on one filesystem, which is atomic — the
# destination is either the old file or the whole new one, never a prefix.
#
# The dotted prefix keeps a crash-leftover visually out of the way; the EXIT
# trap removes it on every ordinary path.
#
# An explicit template rather than a bare `mktemp` as well. It works bare on
# this macOS (measured: /usr/bin/mktemp, BSD, returns a path), so @copilot's
# premise that it fails there is not true today — but the template costs
# nothing and older BSD userlands did require one.
tmp=$(mktemp "$(dirname "$dest")/.write-if-produced.XXXXXXXX")
# Cleans up on every exit path INCLUDING the success one, where the file has
# already been moved and `rm -f` is simply a no-op.
trap 'rm -f "$tmp"' EXIT

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
