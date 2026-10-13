---
name: vm-relay-host
description: Use when testing the Bowtie Windows or Xbox apps through a Claude Code agent running inside a Windows VM — starts the message relay in tools/vm-relay, sends instructions to the VM agent, and watches for its replies.
---

# VM relay: host side

`tools/vm-relay` (see its README) passes plain-text messages between you and
Claude Code agents inside Windows VMs. You queue instructions; each agent
polls for them, does the work, and posts replies.

## Start

1. Ask the user which VMs are involved, what agent name each uses (e.g.
   `arm`, `x64`), and which host address each VM can reach, unless your
   local notes already say. Never write those addresses into the repo.
2. Start the relay in the background with the longest allowed timeout:
   `cd tools/vm-relay && exec python3 -I relay.py 127.0.0.1 <addr> [<addr>…] 2>> relay.log`
   When a background limit kills it, restart it with `--keep-token` so
   polling agents keep working.
3. Ask the user to read the token (`cat tools/vm-relay/token`) and give it to
   each VM agent with: "Use your vm-relay-agent skill. Your agent name is
   `<name>`. The relay is `http://<addr>:8765` and the token is `<token>`."
   Don't print the token yourself.

## Send and receive

- Queue a message: write it to a scratch file, run
  `tools/vm-relay/send.sh <agent> <file>` (prints `queued`), delete the file.
- Keep `tools/vm-relay/watch.sh` armed in the background whenever an agent is
  working: it prints the next reply and exits. Read the reply, act on it, then
  start the watcher again. Unread replies are how test results get lost.
- An agent only polls between its own steps; `relay.log` shows when each last
  polled (`GET /next?agent=…`). Mark urgent messages PRIORITY.
- Don't send secrets. If a test needs a password, the person at the VM types it.

## Writing good instructions

- Number the steps and ask for PASS/FAIL per step with evidence: exact
  on-screen text, timings, screenshot names, log excerpts.
- Say what the agent may change (install a build, edit code, commit to a
  branch) and what it must not (system settings, other software).
- Things only a person can do (importing a certificate as admin, approving
  sign-in on a phone) go to the user through the agent; say so up front.

## Shutting down

Stop the relay when testing ends. The token, inbox, outbox and logs stay in
`tools/vm-relay` and are git-ignored.
