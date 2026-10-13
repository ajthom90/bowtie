#!/bin/sh
# usage: send.sh AGENT FILE
R=$(dirname "$0"); curl -s -H "X-Relay-Token: $(cat $R/token)" -X POST --data-binary @"$2" "http://127.0.0.1:8765/send?agent=$1"; echo
