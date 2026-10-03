# Freeze Fix (Plan 2) — Design

Date: 2026-10-02. Inputs: fake-HDHomeRun harness spec §D (items 1–6),
approved in chat 2026-10-02 ("go with your recommendations for all").

## Goal

Family viewers never see a stream freeze or silently die because of an
ordinary hiccup: FFmpeg briefly slow, a weak-signal dropout, a dead device
connection, or a slow tuner. Success = every harness `xfail`/`flaky` marker
tied to these gaps is removed because its scenario now passes.

## Current behavior (what the harness pins)

| Gap | Today | Harness marker |
|---|---|---|
| Restart resets playlist | Restarted FFmpeg writes `seg00000` and a fresh `live.m3u8` into the same dir; players see the sequence go backwards and freeze | `slow-consumer-restart.yaml` xfail |
| Slow FFmpeg is killed | Sub channel (64 chunks ≈ 4 MiB, < 2 s at 19 Mbps) fills → chunks dropped → 2 s stall timer force-closes the sub → FFmpeg EOF → restart | `baseline-1080i.yaml` flaky |
| Silent device / lost RST | `pump()` reads with no deadline; a connection that stops sending is never redialed | `half-open-stall.yaml` xfail, `TestPipelineHalfOpenStallRedials` xfail |
| No dial timeout | `http.DefaultClient`, no timeouts | `TestPipelineDialHangFailsFast` xfail |
| First dial tied to request | Dial uses the `POST /sessions` context; when the handler returns, the body dies and ingest redials 1 s later | `TestPipelineStartKeepsFirstDial` xfail |
| No-signal on reconnect | Any 503 on reconnect (incl. 807) tears the session down as "tuner stolen" | (new scenario) |

## Design

### 1. Playlist continuity across restarts
Restarted FFmpeg (not the first start) gets `-hls_flags
delete_segments+temp_file+append_list+discont_start`. A new `JobSpec.Append`
field carries this: `restartSessionLocked` sets it, the first start leaves it
false, and the arg-builder tests pin both variants. `append_list` reads the
existing `live.m3u8` and continues segment numbering; `discont_start` marks
`#EXT-X-DISCONTINUITY` before the first new segment. A hand run on the
image's FFmpeg 5.1 (2026-10-02) continued numbering, but not with Bowtie's
own arguments; **the plan's first task re-verifies with the real pipeline**
(`-hls_segment_filename …/seg%05d.ts`, `-hls_list_size 225`,
`delete_segments`). If `append_list` does not continue from the last
segment on disk under those arguments, the fallback is for Bowtie to pass
`-start_number N` (last segment number + 1, read from the directory) plus a
discontinuity tag it inserts while rewriting the playlist. `rewritePlaylist` already passes
non-segment tags through. The first start keeps today's flags (no stale
playlist to append to). Player check: AVPlayer (iOS Simulator UI test),
Chrome/hls.js (Playwright on this Mac), ExoPlayer if an Android emulator is
available; Roku is noted as unverified.

### 2. Slow consumer: buffer more, drop instead of restart
- **Packet-aligned chunks.** `pump()` carries any partial 188-byte TS packet
  over to the next read, so every chunk handed to subscribers is a whole
  number of packets.
- **Byte-bounded per-sub queue, 16 MiB** (≈ 7 s at 19 Mbps) replacing the
  64-slot channel bound. Tracked in bytes, not slots.
- **Overflow drops the oldest queued chunks** (never the newest, never a
  partial packet) until the new chunk fits; counts dropped bytes.
- **After a drop, the next chunk is preceded by the current PAT and PMT**
  (ingest already tracks `lastPAT`/`lastPMT` for the join buffer), so the
  demuxer never waits for the next table cycle and the decoder resyncs at
  the next keyframe. FFmpeg (`-fflags +discardcorrupt`) sees a gap in time:
  a brief glitch instead of a restart and a 5–10 s outage.
- **Force-close only when truly stuck:** no bytes consumed by the sub for
  30 s (was: queue full for 2 s). That is the "FFmpeg hung" case, where a
  restart is the right answer.
- Fan-out stays non-blocking: a slow sub never delays other subs or the pump.

### 3. Dead-connection detection
An idle watchdog on the device body: if no bytes arrive for **8 s**, close
the body; the existing reconnect path (1 s backoff, 60 s give-up) redials.
Covers silent devices and RSTs the kernel dropped. 8 s is well above any
healthy gap (the device streams continuously; tuning happens before the
first byte), and leaves room in the half-open scenario's 15 s budget
(8 s detect + 1 s backoff + dial + first segment).

Intended interaction with §5: during loss of lock the HDHomeRun keeps the
connection open and sends nothing. After 8 s the watchdog redials, the
device may answer 807, and §5 keeps retrying. A dropout that would have
healed on its own at 9 s therefore costs about 2–3 s extra (backoff, redial,
re-lock). That is accepted: a longer timeout would slow the half-open case
for everyone.

### 4. Device dial: timeout and lifetime
- Dedicated `http.Client` for device streams: dial timeout 5 s,
  `ResponseHeaderTimeout` **12 s** (the HDHomeRun answers 806/807 after
  ~10 s, so 12 s never cuts off a legitimate "no signal" answer, and a hung
  device still fails inside the harness's 15 s fail-fast bound). No overall
  `Client.Timeout` (the body is a live stream).
- The device stream outlives the `POST /sessions` request. Contexts by
  phase:
  - **Dial and body:** `context.WithoutCancel(reqCtx)` (the stream may be
    shared by other sessions and must not die with one request).
  - **`waitPlaylist`:** the request context, so an abandoned request stops
    waiting. Its existing error path (`proc.Stop()`, `sub.Close()`, remove
    dir) must still run on cancellation; a test pins that no FFmpeg or sub
    outlives a cancelled start.

### 5. No-signal during playback
On a reconnect, `ErrNoSignal` (806/807) is transient: keep the reconnect
loop's backoff and 60 s give-up instead of closing all subs as
`ErrTunersBusy`. A genuine 805 / header-less 503 keeps today's behavior.

### 6. Logging
One line each, with session id, channel and backend where known:
- FFmpeg exit: exit error (already includes stderr tail since 0.5.5), uptime.
- Sub overflow: bytes dropped, queue size (rate-limited: first drop, then
  once per 10 s while dropping).
- Sub force-close (30 s stuck) and idle-watchdog redial.
- Restart attempt and result (append vs fresh).

## Testing (each fix test-first)

1. Make the scenario/xfail fail for the right reason, then fix, then remove
   the marker (the harness fails the test if a marker outlives its fix).
2. Scenarios: `slow-consumer-restart.yaml` becomes `slow-consumer-drops.yaml`
   (a slow consumer no longer restarts FFmpeg); the restart path is tested by
   a new `transcoder-restart.yaml` that kills FFmpeg mid-session
   (`bowtie.killTranscoderAt`), starting as an xfail that the fix removes; new `nosignal-reconnect.yaml` (807 for 5 s mid-stream →
   session survives) and `slow-consumer-drops.yaml` (10 s slowConsumer → no
   restart, sequence monotonic). The virtual test player does not decode,
   so the drop scenario also gets a real-player check: its HLS output plays
   in AVPlayer (the macOS AVPlayer probe) without error.
3. Unit tests: chunk alignment, byte-bounded drop-oldest queue, idle
   watchdog with the injected clock, dial timeouts against `httptest`.
4. Gates: `go test ./...`, `go test -tags ffmpeg -p 1 -parallel 2 ./...`,
   golangci-lint, iOS Simulator UI test and Chrome playback against the
   real HDHomeRun (channel 9.1) after a forced FFmpeg restart. Real-device
   checks hold the household's spare tuner, so run them one at a time
   (`-parallel 1`), keep them short, and avoid prime time.

## Out of scope

Adaptive bitrate, DVR, client-app changes (players already handle
`#EXT-X-DISCONTINUITY`), Roku on-device verification.

## Risks

- `append_list` with `delete_segments` and a 225-entry window: the restarted
  FFmpeg must not reuse deleted segment numbers. Covered by
  `restart-keeps-playlist` asserting monotonic sequence and unique URIs.
- Dropping data hides persistent slowness. Mitigated by the drop log and
  the 30 s stuck rule.
- 16 MiB × subs is memory: 3 sessions ≈ 48 MiB worst case. Acceptable on
  the TrueNAS box; constant is named and documented.
