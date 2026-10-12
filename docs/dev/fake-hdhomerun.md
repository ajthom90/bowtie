# Fake HDHomeRun and fault scenarios

Bowtie's tests can run against a fake HDHomeRun that broadcasts a continuous,
real-time MPEG-TS timeline and injects faults: weak signal, stalls, dropped
connections, corruption, audio/video loss, timestamp jumps, source switches,
busy and hung devices. Design: `docs/superpowers/specs/2026-10-02-fake-hdhomerun-harness-design.md`.

## Packages

| Package | What it does |
|---|---|
| `internal/hdhr/hdhrfake` | The fake device (`discover.json`, `lineup.json`, `status.json`, `/auto/v<ch>`). `New(t, opts)` in tests, `Start(opts)` elsewhere. `Apply`/`Remove` faults. |
| `internal/hdhr/hdhrfake/tsloop` | Loops a `.ts` clip forever, rewriting PCR/PTS/DTS and continuity counters so the timeline never jumps back. One clock per channel; connections join mid-stream like a real tuner. |
| `internal/hdhr/hdhrfake/faults` | Fault engine and signal model (quality ≥80 clean, 50–80 sporadic errors, <50 bursts, <30 loss of lock). |
| `internal/hdhr/hdhrfake/scenario` | YAML scenario files. |
| `internal/hdhr/hdhrfake/synth` | Generates 480i/720p/1080i MPEG-2 + AC-3 sources with FFmpeg (cached in the user cache dir). |
| `internal/testplayer` | Virtual HLS viewer: polls the playlist, fetches segments, reports stalls, sequence resets and discontinuities. |
| `internal/e2e` | Wires the real Bowtie stack to the fake. Pipeline tests (stub FFmpeg) run in the default build; `TestScenarios` (real FFmpeg) needs `-tags ffmpeg`. |

## Running

```sh
cd server
go test ./internal/e2e/                                              # pipeline tests, ~25s
go test -tags ffmpeg -run TestScenarios -parallel 3 -v ./internal/e2e/   # real FFmpeg, ~2 min
go test -tags ffmpeg,soak -run TestScenarios ./internal/e2e/         # include soak scenarios
BOWTIE_FAKE_SOURCE=~/captures/fox9.ts go test -tags ffmpeg -run TestScenarios ./internal/e2e/
```

To match production's FFmpeg (Debian bookworm, 5.1):

```sh
docker run --rm -v "$PWD:/src" -w /src -e CGO_ENABLED=0 golang:1.22-bookworm bash -c \
  'apt-get update -qq && apt-get install -y -qq ffmpeg && go test -tags ffmpeg -run TestScenarios -v ./internal/e2e/'
```

### On FFmpeg 5.1 (the production image's FFmpeg)

`server/scripts/ffmpeg51` runs Debian bookworm's FFmpeg 5.1 in Docker as a
drop-in for `BOWTIE_FFMPEG_PATH` (software encode):

```sh
cd server
BOWTIE_FFMPEG_PATH=$PWD/scripts/ffmpeg51 go test -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run TestScenarios ./internal/e2e/
```

`FF51_LOGLEVEL` changes the log level; `FF51_LOGDIR` collects each run's
argv and its re-base and duplicate lines with timestamps (scenarios then log
`ff: rebases=… dupFrames=…` and `rebase at +…`). To compare FFmpeg options,
commit each to its own scratch branch and run from it: the wrapper never
injects options, and the logged argv shows what ran. Captions (fd 3) can't
cross into the container, so scenarios with `bowtie: {layout: production}`
run inside the production image instead: build `deploy/Dockerfile`,
cross-compile the e2e test binary (`go test -c -tags ffmpeg`) and run it in
the image with `BOWTIE_FFMPEG_PATH` pointing at `scripts/ffmpeg-soak`
mounted into the container (see the stream resilience plan, Task 6).

### On real over-the-air clips

`TestOTAClips` plays captured `.ts` files through the production layout and
fails on a timestamp re-base that isn't near a signal dip in the capture's
tuner log. The clips stay out of the repo: point `BOWTIE_OTA_CLIPS` at their
folder and `BOWTIE_OTA_SIGNAL_DIR` at the `ch<N>.signal.tsv` logs
(optionally `BOWTIE_OTA_CLIP_FILES=ch2.2.ts,ch9.1@1200.ts` and
`BOWTIE_OTA_FOR=60m`). Clips are loaded into memory: cut clips over 1 GiB,
naming each piece `ch<N>@<start seconds>.ts`. `TestOTAClipsWithFaults`
injects timestamp faults into `BOWTIE_OTA_FAULT_CLIP` (a clip with captions)
and checks that the cues stay on the video.

## Writing a scenario

Drop a file in `server/internal/e2e/testdata/scenarios/`. With an `expect:`
block it is a test; no Go needed.

```yaml
name: device-drop
source: synthetic-480i      # or synthetic-720p / synthetic-1080i / a .ts path
duration: 35s
timeline:                   # times are from the moment playback starts;
  - {at: 10s, fault: drop, mode: reset, scope: connections}   # at: 0s happens before tuning
bowtie:
  slowConsumer: {at: 10s, for: 10s}   # pause FFmpeg's input reads
  killTranscoderAt: 15s                # kill FFmpeg; Bowtie restarts it
expect:
  sequenceMonotonic: true   # playlist never goes backwards
  maxGapSeconds: 12         # longest wait for a new segment
  sessionSurvives: true
  maxFfmpegRestarts: 0
  tunersBusy: false
```

Faults: `signal` (strength/quality/symbol), `stall` (for, burst), `drop`
(mode clean|reset), `corrupt` (rate, type bitflip|tei|sync), `slow` (rate),
`timestamp-jump` (by), `pts-jump` (by, ptsOnly), `pid-loss` (track
audio|video, for), `source-switch` (to), `busy`, `hang`. Scope: `channel` (default; everyone on the channel, now
and later), `connections` (only connections open when it fires), `device`.

`pts-jump` adds `by` to the PTS and DTS of one emitted video PES on each
connection (with `ptsOnly: true`, only the PTS of a PES that carries a DTS,
leaving the DTS valid). It fires once per connection within 10 s of starting,
wraps at 33 bits, and waits out a held lock, so it models a single corrupt
timestamp. Scenario steps: `{at: 8s, fault: pts-jump, by: 3s, ptsOnly: true}`.

While a channel's signal quality is below 30 (loss of lock), open
connections go silent and new tunes get `503` with `X-HDHomeRun-Error: 807
No Video Data`, as on a real HDHomeRun.

`drop` with `mode: reset` stops writing and waits up to 1s for the client to
take in-flight data before sending the RST. A RST that lands while data is
still in flight carries a sequence number the client hasn't reached, and the
kernel may silently drop it (macOS lost about a third in testing), leaving
the client half-open instead of reset. If the client isn't reading, the RST
can still be lost, as it can on a real network.

**`xfail: "<reason>"`** marks expectations that fail today because of a known
Bowtie gap. The test then *requires* them to fail, so whoever fixes the gap
must delete the marker.

**`flaky: "<reason>"`** is the non-strict form for a known gap that only shows
up some of the time (usually under CPU load): violations are logged but the
scenario never fails. Prefer `xfail` whenever the failure is deterministic.

## Captures

Real broadcasts can't be committed. Keep local captures in `captures/`
(gitignored) and point `BOWTIE_FAKE_SOURCE` at them.

## Reliability scenarios (Plan 2)

| Scenario | What it proves |
|---|---|
| `transcoder-restart` | FFmpeg killed mid-session; the restarted process continues the playlist (no backward jump, ≤ 14 s gap). |
| `slow-consumer-drops` | FFmpeg stops reading for 10 s; its queue drops old data and it is not restarted. |
| `half-open-stall` | The device goes silent without closing; Bowtie reconnects after 8 s. |
| `nosignal-reconnect` | 12 s loss of lock; reconnects get 807 and keep retrying until the signal returns. |
| `baseline-1080i` | No faults at 1080i: no restart even on a loaded machine. |
