#!/bin/sh
# Stamp the channel manifest with a release version, the way Android's
# build.gradle.kts reads BOWTIE_VERSION: "v0.24.1" or "0.24.1" becomes
# major_version=0, minor_version=24, build_version=1. The release workflow
# runs this before `npm run package`; the committed manifest keeps 0.1.0.
set -eu

version="${1:-${BOWTIE_VERSION:-}}"
version="${version#v}"
if ! printf '%s\n' "$version" | grep -Eqx '[0-9]+\.[0-9]+\.[0-9]+'; then
  echo "stamp-version: want X.Y.Z, got '${1:-${BOWTIE_VERSION:-}}'" >&2
  exit 1
fi
major="${version%%.*}"
rest="${version#*.}"
minor="${rest%%.*}"
patch="${rest#*.}"

manifest="$(dirname "$0")/../manifest"
sed -i.bak -E \
  -e "s/^major_version=.*/major_version=$major/" \
  -e "s/^minor_version=.*/minor_version=$minor/" \
  -e "s/^build_version=.*/build_version=$patch/" \
  "$manifest"
rm -f "$manifest.bak"
grep -E '^(major|minor|build)_version=' "$manifest"
