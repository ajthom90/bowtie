# VM relay

A tiny HTTP relay for testing the Windows and Xbox apps in virtual machines
with an AI agent at each end: Claude Code on the host that runs the relay
sends instructions, and a Claude Code agent inside each Windows VM carries
them out and reports back. Messages are plain text.

Two project skills describe each side:

- `.claude/skills/vm-relay-host`: start the relay, send, watch for replies.
- `.claude/skills/vm-relay-agent`: the VM side (poll, act, reply).

## Files

| File | Purpose |
|---|---|
| `relay.py` | The server (Python 3 standard library only). |
| `send.sh AGENT FILE` | Queue a message for an agent (host only, via 127.0.0.1). |
| `watch.sh` | Wait for the next reply in `outbox/`, print it, exit. |

At run time the relay creates `token`, `inbox/`, `outbox/`, `delivered-*`
and `relay.log` here; they are git-ignored. Never commit them.

## Start

```bash
cd tools/vm-relay
python3 -I relay.py 127.0.0.1 <address a VM can reach> [<another address>]
```

It listens on port 8765 on each address. Use the host's address on the
network each VM is on: for a Parallels VM that is the host's address on the
Parallels shared network (e.g. `10.211.55.2`); for a VM on another machine,
the host's LAN address (`ipconfig getifaddr en0` on macOS). Keep the
addresses to the networks you need, not `0.0.0.0`.

Every start writes a new random token to `token` (mode 600). Pass
`--keep-token` to reuse it, so agents that are already polling keep working
across a restart. Whoever runs a VM gives its agent the token by hand.

## Protocol

Every request needs the header `X-Relay-Token: <token>`.

| Request | Who | Result |
|---|---|---|
| `GET /next?agent=NAME&wait=S` | agent | 200 with the next message, or 204 after `S` seconds (max 600) |
| `POST /reply?agent=NAME` | agent | body saved as `outbox/<time>-NAME.txt` |
| `POST /send?agent=NAME` | host, from 127.0.0.1 only | body queued for NAME |

Agent names are 1–20 characters of `a-z`, `0-9` and `-`.

## Security

The relay is for a trusted home or lab network. Anyone with the token who
can reach the port can read the queued instructions and post replies, so
don't send secrets through it. If a test needs a password, have the person
at the VM type it there.
