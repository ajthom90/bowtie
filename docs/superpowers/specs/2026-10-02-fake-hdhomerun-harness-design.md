# Bowtie — Fake HDHomeRun Fault-Injection Harness

**Date:** 2026-10-02
**Status:** Draft — pending user review
**Motivation:** On 2026-10-02 a FOX 9 viewer froze on v0.5.2 (vaapi). The log
shows FFmpeg restarting mid-session (second process 6s after the first, same
session `startedAt`) with no error, and re-entering the channel recovered.
Two Bowtie defects were found by reading code, but neither can be proven or
regression-tested today:

1. **Playlist reset on restart.** `restartSessionLocked` starts a fresh FFmpeg
   into the same session dir with `-hls_flags delete_segments+temp_file`, so
   numbering restarts at `seg00000` / `#EXT-X-MEDIA-SEQUENCE:0` and overwrites
   the playlist players are following → players stall. (Reproduced with the
   v0.5.2 image's FFmpeg 5.1: second run overwrites from seg00000.)
2. **Silent subscriber force-close (suspected trigger).** `ingestChunk` force-
   closes an IngestSub whose channel (64 × 64 KiB ≈ 4 MiB ≈ <2s of ATSC) stays
   full for `ingestStallTimeout` (2s). FFmpeg sees EOF, exits 0, and is
   restarted — none of the three steps is logged.

The existing `hdhrfake` cannot inject faults, loops a 2s clip whose timestamps
jump back every loop, and is test-only. This spec builds a fault-injecting fake
HDHomeRun usable from Go tests (incl. CI) and as a standalone dev tool, then
uses it to drive the freeze fix test-first.

## Decisions of record

| Topic | Decision |
|---|---|
| Uses | **Both**: Go test harness (CI) and standalone `bowtie-fakehdhr` command, sharing one engine |
| Media | **Synthetic by default** (committed generator; CI-safe) **+ local captures** from the user's HDHomeRun (gitignored, never committed — broadcast copyright) |
| Fault control | **Both**: live control page + JSON API, and YAML scenario files; recorded sessions export as scenarios |
| Continuous stream | **Pure-Go TS loop with timestamp rewrite** (PCR/PTS/DTS/CC), PCR-paced; no FFmpeg inside the fake |
| Faults | Channel-scoped (antenna) vs connection-scoped (network); signal model drives packet errors |
| Regression tests | A scenario file with an `expect:` block **is** a test (no Go required) |
| CI | Server job installs FFmpeg and runs `-tags ffmpeg`; e2e budget ~3 min |
| Not included | UDP discovery, Docker image/release artifact, multi-device in one process |

## A. Components

All under `server/`. Existing `hdhrfake` tests keep passing unchanged.

| Package | Responsibility | Depends on |
|---|---|---|
| `internal/hdhr/hdhrfake/tsloop` | Endless, real-time TS source from a file | stdlib |
| `internal/hdhr/hdhrfake/faults` | Fault engine + signal model, applied per packet | stdlib |
| `internal/hdhr/hdhrfake/scenario` | YAML timeline parse/run; `expect:` schema | `faults`, `gopkg.in/yaml.v3` (already in go.mod) |
| `internal/hdhr/hdhrfake` (existing) | Device emulation (discover/lineup/status/stream, tuner accounting, 503) wired to the above | the three above |
| `cmd/bowtie-fakehdhr` | Standalone binary, control page, control API | `hdhrfake`, `scenario` |
| `internal/testplayer` | Virtual HLS player that records the viewer experience | stdlib |

### A1. `tsloop`

```go
type Source struct{ /* immutable after Load */ }
func Load(r io.Reader) (*Source, error)         // validates 188-byte alignment, finds PCR PID
type Loop struct{ /* per-connection cursor */ }
func (s *Source) NewLoop(clock Clock) *Loop
func (l *Loop) Next(ctx context.Context) ([]byte, error) // next 188-byte packet, paced
```

- **Timestamp continuity.** On each wrap, add `loopDuration` to PCR (27 MHz,
  adaptation field), PTS and DTS (90 kHz, PES header), modulo 2^33 / PCR
  wrap. `loopDuration = lastPCR − firstPCR + one PCR interval`, computed once
  in `Load`.
- **Continuity counters.** Rewrite each PID's CC so it stays continuous
  across the wrap (otherwise every loop is a CC error).
- **Pacing.** Emit a packet when the clock reaches its PCR time, interpolating
  between PCRs by byte position. `Clock` is injectable (real or fake).
- **Source switch.** `Loop.Switch(*Source)` continues the timeline from the
  current position (used by the `source-switch` fault).
- **Multiple connections** each get their own `Loop` and start at the head of
  the clip, like tuning in.

### A2. `faults`

Engine state is **per channel** (antenna faults hit every connection on the
channel) plus **per connection** (network faults hit one). Randomness uses a
seeded RNG; time uses the injected clock — deterministic in tests.

| Fault | Scope | Behavior |
|---|---|---|
| `signal{strength,quality,symbol}` | channel | Sets `status.json` values and drives packet errors: quality ≥80 clean; 80→50 linear 0→5% packets get TEI set or are dropped (CC gap); <50 error bursts (runs of 20–200 packets); <30 **loss of lock** → silence, connection stays open. Thresholds are tunable constants (they approximate tuner behavior; a scenario can bypass them with raw faults). |
| `stall{for, burst}` | channel or connection | Send nothing for `for`; connection stays open. `burst: true` delivers the withheld backlog at once afterwards (Wi-Fi); otherwise it is discarded (loss of lock). |
| `drop{mode}` | connection | Close mid-stream: `clean` (FIN) or `reset` (RST). |
| `corrupt{rate, kind}` | channel | `kind`: `bitflip` (payload), `tei` (error flag), `sync` (break 0x47 alignment). |
| `slow{rate}` | connection | Deliver at `rate` × real-time (e.g. 0.8). |
| `timestamp-jump{by}` | channel | Shift all subsequent PCR/PTS/DTS by `by` (±). |
| `pid-loss{track, for}` | channel | Drop all `audio` or `video` PES packets for `for`. |
| `source-switch{to}` | channel | Switch to another source (e.g. 1080i → 480i). |
| `busy` | device | New dials get `503 all tuners in use` regardless of free tuners. |
| `hang` | device | New dials accept TCP and never send headers. |

`status.json` reports `SignalStrengthPercent`, `SignalQualityPercent`,
`SymbolQualityPercent`, `VctNumber`, `VctName` and `TargetIP` per tuner, in the
same shape `hdhr.TunerStatus` decodes from the real device.

### A3. `hdhrfake` changes

- New `Start(opts Options) (*Fake, error)` + `Close()` for non-test use;
  `New(t, opts)` becomes a wrapper calling `Start` + `t.Cleanup`.
- `Options` gains `Channels []Channel{GuideNumber, Name, Source *tsloop.Source}`,
  `Clock`, `Seed`, `Listen` (default `127.0.0.1:0`). Existing `Lineup`-only
  callers keep the embedded 480i clip.
- `Faults() *faults.Engine` exposed for tests; `Events() []Event` (dial,
  close, fault start/end) for assertions and the control page log.

### A4. `scenario`

```yaml
name: mid-stream-ffmpeg-restart
source: synthetic-1080i          # or a path; BOWTIE_FAKE_SOURCE overrides
channel: "90.1"
duration: 45s
timeline:
  - {at: 15s, fault: drop, mode: reset}
  - {at: 25s, fault: signal, quality: 40, for: 8s}
bowtie:                          # optional, e2e only (§C3)
  slowConsumer: {at: 10s, for: 5s}   # pause FFmpeg's stdin reads
expect:                          # optional; makes the file a test (§C3)
  sequenceMonotonic: true
  maxGapSeconds: 12
  sessionSurvives: true
  maxFfmpegRestarts: 1
  tunersBusy: false
```

- Validation rejects unknown faults/fields, `at` beyond `duration`, and
  overlapping same-kind faults on one target.
- `scenario.Run(ctx, engine, clock)` applies the timeline; it returns when
  `duration` elapses.

## B. Standalone tool: `cmd/bowtie-fakehdhr`

```sh
go run ./cmd/bowtie-fakehdhr                     # defaults
go run ./cmd/bowtie-fakehdhr --listen 0.0.0.0:5080 --tuners 2 --seed 42 \
  --channel "9.1=FAKE FOX:~/captures/fox9.ts" --scenario freeze.yaml
```

- **Defaults:** `--listen 127.0.0.1:5080`, 2 tuners, channels `90.1 FAKE 1080i`,
  `90.2 FAKE 720p`, `90.3 FAKE 480i`. Synthetic sources are generated with
  FFmpeg on first run and cached in the user cache dir; without FFmpeg it
  falls back to the embedded 480i clip (and says so).
- **Adding to Bowtie:** Admin → Tuners → add `<host>:5080`. Verified:
  `hdhr.BaseURLFromManual` accepts `host:port`, and `tuner.Manager` reuses a
  non-standard stream port. Recommended target is a local dev Bowtie
  (`make dev-server`), not production, so FAKE channels never enter the real
  lineup.
- **Control page** `GET /control`: per-channel signal sliders and a button per
  fault (with duration/rate inputs); live connections (tuner, channel, client
  IP, bytes, active faults); event log; **Record → Save as scenario** downloads
  the recorded actions as scenario YAML.
- **Control API** (the page is a thin client over it):
  `GET /control/api/state`, `POST /control/api/channels/{num}/faults`,
  `POST /control/api/connections/{id}/faults`, `DELETE …/faults/{id}`,
  `POST /control/api/device/faults` (busy/hang). JSON bodies mirror the
  scenario timeline entries.
- **Safety:** no auth; binds loopback unless `--listen` says otherwise.
- **Capture:** `make capture CH=9.1 SECS=60 [HDHR=192.168.50.32]` →
  `captures/<ch>-<timestamp>.ts` at repo root (gitignored). Uses one real tuner
  for SECS; exits non-zero with a clear message on 503.

## C. Tests

### C1. Unit (default `go test`)

- `tsloop`: PCR/PTS/DTS strictly increase across ≥100 loops (incl. a 2^33
  wrap fixture); CC continuous per PID; paced output within ±2% of real time
  on the fake clock; `Switch` keeps timestamps monotonic.
- `faults`: each fault's packet-level effect, deterministic for a seed;
  signal thresholds; channel vs connection scoping.
- `scenario`: parse, validation errors, timeline firing order on fake clock.
- `hdhrfake`: existing tests unchanged; `status.json` reflects signal model;
  `busy`/`hang`.

### C2. Pipeline without FFmpeg (default `go test`, seconds)

Real `stream.Manager` + `IngestManager` dialing the fake, with stub `Runner`
processes (extending the pattern in `api/stream_handlers_test.go:710`). Adds a
**slow-consumer** stub that stops reading stdin on command. Covers: ingest
reconnect after `drop`/`stall`, subscriber force-close after >2s full queue,
restart scheduling/backoff, one-tuner-per-channel under faults, 503 mapping.

### C3. End-to-end scenarios (`-tags ffmpeg`)

- Wiring: fake (synthetic 1080i) → real `IngestManager` → real
  `FFmpegRunner` (software encoder) → real API handlers (`httptest`) →
  `testplayer`. The runner is wrapped to (a) count `Start` calls
  (`maxFfmpegRestarts`) and (b) interpose a **pausable stdin** reader so
  `bowtie.slowConsumer` stalls a real FFmpeg's reads — no production hooks.
- `testplayer` logs in, creates a session (low profile, e.g. 480p, to save CI
  CPU), heartbeats, polls the playlist at target-duration cadence, downloads
  new segments, and reports: media-sequence history, backward jumps,
  discontinuity tags seen, longest gap without a new segment, failed segment
  fetches, session-gone errors.
- **`TestScenarios`** runs every `testdata/scenarios/*.yaml` that has `expect:`
  as a parallel subtest. Scenarios tagged `soak: true` run only with
  `-tags ffmpeg,soak`.
- `BOWTIE_FAKE_SOURCE=<path.ts>` swaps the synthetic source for a capture.
- Budget ~3 min total in CI; timing assertions carry generous tolerances.

### C4. CI

`server` job: `sudo apt-get install -y ffmpeg`, then
`go test ./...` and `go test -tags ffmpeg ./...`. (The existing
`ffmpeg_e2e_test.go` has never run in CI; this enables it too.)

## D. First consumer: the freeze fix (test-first)

Implemented **after** §A–C, each change preceded by a failing scenario.

1. **Failing scenario** `testdata/scenarios/restart-keeps-playlist.yaml`:
   `drop` at 15s (forces an FFmpeg restart via EOF) with
   `sequenceMonotonic: true`, `sessionSurvives: true`. Expected to FAIL on
   current code (sequence resets to 0).
2. **Playlist continuity:** restarts continue numbering and mark a
   discontinuity. Candidate: add `append_list+discont_start` to `-hls_flags`
   (verified 2026-10-02 on the v0.5.2 image's FFmpeg 5.1: numbering continues
   `seg00004…` after a `#EXT-X-DISCONTINUITY`). The final flag set is whatever
   makes the scenario pass and plays in hls.js/AVPlayer/ExoPlayer/Roku;
   `rewritePlaylist` already passes non-segment tags through.
3. **Logging** (asserted via captured log output in C2): FFmpeg exit (session,
   channel, backend, exit error/code, uptime); IngestSub force-close (channel,
   queue state, stalled duration); restart attempt and result.
4. **Root cause of the restart:** a `slowConsumer` scenario reproduces the
   force-close path; production logs from (3) confirm or refute it on the
   user's box. Any change to the 2s/4 MiB policy is decided on that evidence —
   not in this spec.

## Out of scope

UDP discovery; Docker image or release artifact for the fake; simulating
multiple devices in one process (run a second instance on another port);
real-player automation (Playwright's Chromium cannot decode H.264 — real
players are exercised manually via the standalone tool).

## Risks

- **CI timing flakiness:** real-time scenarios on shared runners. Mitigation:
  generous tolerances, low profile, parallel subtests, soak tag for long runs.
- **TS rewrite edge cases** (PCR on non-video PID, PES without PTS, 33-bit
  wrap): covered by unit fixtures; captures from the user's device exercise
  real-world layouts.
- **Signal model realism:** thresholds are estimates; raw faults remain
  available, and the model is tunable once real captures under bad weather
  exist.
