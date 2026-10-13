#!/bin/sh
# Xcode Cloud: Bowtie.xcodeproj is generated from project.yml (not committed),
# so generate it right after the clone, before Xcode Cloud resolves packages
# and builds.
set -eu

repo="$CI_PRIMARY_REPOSITORY_PATH"

# Version every app as the release it ships with, so TestFlight shows the
# same version as the git tag. Xcode Cloud builds every push to main, and the
# release PR adds the new "## [X.Y.Z]" heading to CHANGELOG.md before the
# merge is tagged, so the newest released heading is that version. A build of
# a tag uses the tag itself.
version="${CI_TAG:-}"
version="${version#v}"
if [ -z "$version" ]; then
  version=$(sed -nE 's/^## \[([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/p' "$repo/CHANGELOG.md" | head -n 1)
fi
if ! printf '%s\n' "$version" | grep -Eqx '[0-9]+\.[0-9]+\.[0-9]+'; then
  echo "ci_post_clone: no release version found (got '$version')" >&2
  exit 1
fi
build="${CI_BUILD_NUMBER:-1}"
if ! printf '%s\n' "$build" | grep -Eqx '[0-9]+'; then
  echo "ci_post_clone: bad build number '$build'" >&2
  exit 1
fi
echo "ci_post_clone: MARKETING_VERSION $version, build $build"
sed -i '' -E \
  -e "s/^( *MARKETING_VERSION:).*/\1 \"$version\"/" \
  -e "s/^( *CURRENT_PROJECT_VERSION:).*/\1 \"$build\"/" \
  "$repo/ios/project.yml"

brew install xcodegen
cd "$repo/ios"
xcodegen generate
