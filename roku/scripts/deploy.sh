#!/usr/bin/env bash
# Sideload out/bowtie-roku.zip to a Roku in developer mode.
#   ROKU_IP=192.168.x.y ROKU_DEV_PASSWORD=… npm run deploy
set -euo pipefail
: "${ROKU_IP:?set ROKU_IP}" "${ROKU_DEV_PASSWORD:?set ROKU_DEV_PASSWORD}"
cd "$(dirname "$0")/.."
# The upload's own failure (bad password, unreachable) stops here via set -e.
response=$(curl -sS --fail --digest -u "rokudev:${ROKU_DEV_PASSWORD}" \
  -F "mysubmit=Install" -F "archive=@out/bowtie-roku.zip" \
  "http://${ROKU_IP}/plugin_install")
printf '%s\n' "$response" | grep -oE 'Install Success|Identical to previous version|Install Failure[^<]*' || true
