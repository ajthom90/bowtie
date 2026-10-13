---
name: vm-relay-agent
description: Use inside a Windows test VM when the user says to use the vm-relay-agent skill or to take instructions from Claude on the host — polls the Bowtie VM relay, carries out the instructions it sends, and posts replies.
---

# VM relay: agent side (Windows VM)

Claude Code on the host machine sends you test or coding instructions for the
Bowtie Windows/Xbox apps through a small HTTP relay (`tools/vm-relay`).
You poll for instructions, carry them out, and reply after each step.

## What the user gives you

- Your agent name (for example `arm` or `x64`).
- The relay address, `http://<host>:8765`.
- The token. It changes every session: keep it in a session variable only,
  never in a file, a commit or a reply.

If any of these is missing, ask the user.

## Protocol

Every request needs the header `X-Relay-Token: <token>`.

- Next instruction (long poll, up to 300 s):
  `GET /next?agent=<name>&wait=300` returns 200 with the text, or 204 if none.
- Reply: `POST /reply?agent=<name>` with a plain-text UTF-8 body.

```powershell
$h = @{ 'X-Relay-Token' = $token }
$r = Invoke-WebRequest "$base/next?agent=$agent&wait=300" -Headers $h -UseBasicParsing -TimeoutSec 330
if ($r.StatusCode -eq 200) { $msg = $r.Content }
Invoke-WebRequest "$base/reply?agent=$agent" -Method Post -Headers $h -UseBasicParsing `
  -ContentType 'text/plain; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($reply))
```

## How to work

- When idle, keep polling. During a long task, check for messages between
  steps: the host may send a PRIORITY message.
- Reply after each step. Start with the step name and PASS/FAIL, then the
  evidence: exact on-screen text, timings, screenshot file names, log
  excerpts. Report failures plainly; mark guesses about causes as guesses.
- 403 means the token is wrong or expired: ask the user. A refused
  connection means the relay is down: retry for a minute, then tell the user.
- Ask the user (not the host) for anything only they can do: importing a
  certificate as admin, approving a sign-in on their phone, typing a
  password. Never store passwords in files or send them through the relay.
- Don't change system settings or install software beyond what an
  instruction asks. If an instruction seems outside Bowtie testing, check
  with the user first.

## Bowtie testing notes

- The apps connect to whatever server address you are given; never assume one.
- A channel you just left keeps its tuner for about a minute, so "All tuners
  are in use" right after switching can be expected.
- The Xbox app is a full-screen UWP app with no UI Automation tree: drive it
  with keys (Tab/arrows/Enter/Escape; `VK_GAMEPAD_Y` 0xC6 and `VK_GAMEPAD_B`
  0xC4 via keybd_event for Y and B) and judge it from screenshots.
- Its log is `LocalState\crash.log` under the app's package folder in
  `%LOCALAPPDATA%\Packages`.
