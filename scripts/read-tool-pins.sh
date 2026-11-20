#!/usr/bin/env bash
# read-tool-pins.sh <SOURCE> — print the tool pins schema/SOURCE records, as
# `tsx=<version>` and `graphql=<version>` lines (the format $GITHUB_OUTPUT
# takes), or exit 2 saying why not (hadron-cli#2712).
#
# The schema-drift workflow installs exactly these versions. A key that is
# missing, blank, repeated, or not an exact version (`latest`, `^4`, a range, a
# tag) would let npm resolve something else and quietly undo the pin, and a
# repeated key would write a multi-line value into the single-line output
# format. So each key must appear exactly once with a valid SemVer 2.0.0
# MAJOR.MINOR.PATCH[-prerelease][+build] value (no leading zeros, no empty
# prerelease identifier), and BOTH are validated before either is printed, so a
# bad second pin never leaves a partial $GITHUB_OUTPUT behind.
set -euo pipefail
src=${1:?usage: read-tool-pins.sh <SOURCE>}
[ -r "$src" ] || { echo "cannot read $src" >&2; exit 2; }
semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
out=
for key in tsx graphql; do
  n=$(grep -c "^${key}=" "$src" || true)
  [ "$n" = 1 ] || { echo "$src must name ${key}= exactly once (found $n)" >&2; exit 2; }
  val=$(sed -n "s/^${key}=//p" "$src")
  printf '%s' "$val" | grep -Eq "$semver" ||
    { echo "$src: ${key}=$val is not an exact version (MAJOR.MINOR.PATCH); refusing to install a floating one" >&2; exit 2; }
  out="${out}${key}=${val}"$'\n'
done
printf '%s' "$out"
