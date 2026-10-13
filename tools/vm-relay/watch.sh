#!/bin/bash
# Wait for the next agent reply in outbox/, print it, and exit.
# Run in the background and re-run after each reply.
O="$(cd "$(dirname "$0")" && pwd)/outbox"
mkdir -p "$O"
last=$(ls "$O" | sort | tail -1)
while :; do
  if [ -n "$(ls "$O" | sort | awk -v l="$last" '$0>l')" ]; then
    sleep 3
    for f in $(ls "$O" | sort | awk -v l="$last" '$0>l'); do echo "== $f"; cat "$O/$f"; echo; done
    exit 0
  fi
  sleep 5
done
