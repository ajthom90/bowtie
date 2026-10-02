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
go test -tags ffmpeg -run TestScenarios -parallel 10 -v ./internal/e2e/  # real FFmpeg, ~50s
go test -tags ffmpeg,soak -run TestScenarios ./internal/e2e/         # include soak scenarios
BOWTIE_FAKE_SOURCE=~/captures/fox9.ts go test -tags ffmpeg -run TestScenarios ./internal/e2e/
```

To match production's FFmpeg (Debian bookworm, 5.1):

```sh
docker run --rm -v "$PWD:/src" -w /src -e CGO_ENABLED=0 golang:1.22-bookworm bash -c \
  'apt-get update -qq && apt-get install -y -qq ffmpeg && go test -tags ffmpeg -run TestScenarios -v ./internal/e2e/'
```

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
expect:
  sequenceMonotonic: true   # playlist never goes backwards
  maxGapSeconds: 12         # longest wait for a new segment
  sessionSurvives: true
  maxFfmpegRestarts: 0
  tunersBusy: false
```

Faults: `signal` (strength/quality/symbol), `stall` (for, burst), `drop`
(mode clean|reset), `corrupt` (rate, type bitflip|tei|sync), `slow` (rate),
`timestamp-jump` (by), `pid-loss` (track audio|video, for), `source-switch`
(to), `busy`, `hang`. Scope: `channel` (default; everyone on the channel, now
and later), `connections` (only connections open when it fires), `device`.

**`xfail: "<reason>"`** marks expectations that fail today because of a known
Bowtie gap. The test then *requires* them to fail, so whoever fixes the gap
must delete the marker. Pipeline tests use the same idea via `xfail(t, …)`.

## Captures

Real broadcasts can't be committed. Keep local captures in `captures/`
(gitignored) and point `BOWTIE_FAKE_SOURCE` at them.
