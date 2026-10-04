#!/bin/sh
# Fake ffmpeg for runner tests: copies fd 3 to $FD3_OUT, ignores its args.
cat <&3 > "$FD3_OUT"
