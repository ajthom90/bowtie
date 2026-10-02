#!/bin/sh
# Xcode Cloud: Bowtie.xcodeproj is generated from project.yml (not committed),
# so generate it right after the clone, before Xcode Cloud resolves packages
# and builds.
set -eu

brew install xcodegen
cd "$CI_PRIMARY_REPOSITORY_PATH/ios"
xcodegen generate
