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
delete_segments+temp_file+append_list+discont_start`. `append_list` reads the
existing `live.m3u8` and continues segment numbering; `discont_start` marks
`#EXT-X-DISCONTINUITY` before the first new segment. Verified on the
image's FFmpeg 5.1 (2026-10-02). `rewritePlaylist` already passes
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
  partial packet) until the new chunk fits; counts dropped bytes. FFmpeg
  (`-fflags +discardcorrupt`) sees a gap in time and resyncs: a brief
  glitch instead of a restart and a 5–10 s outage.
- **Force-close only when truly stuck:** no bytes consumed by the sub for
  30 s (was: queue full for 2 s). That is the "FFmpeg hung" case, where a
  restart is the right answer.
- Fan-out stays non-blocking: a slow sub never delays other subs or the pump.

### 3. Dead-connection detection
An idle watchdog on the device body: if no bytes arrive for **10 s**, close
the body; the existing reconnect path (1 s backoff, 60 s give-up) redials.
Covers silent devices and RSTs the kernel dropped. 10 s is well above any
healthy gap (the device streams continuously; tuning happens before the
first byte).

### 4. Device dial: timeout and lifetime
- Dedicated `http.Client` for device streams: dial timeout 5 s,
  `ResponseHeaderTimeout` **15 s** (the HDHomeRun answers 806/807 after
  ~10 s, so 15 s never cuts off a legitimate "no signal" answer). No overall
  `Client.Timeout` (the body is a live stream).
- The device stream outlives the `POST /sessions` request:
  `Attach` dials with `context.WithoutCancel(ctx)`; cancelling the request
  still aborts the *wait* for the first playlist, but not an established
  device stream that other sessions may share.

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
2. New scenarios: `restart-keeps-playlist.yaml` (spec §D.1; slowConsumer
   long enough to force a restart via the 30 s rule, or an FFmpeg kill),
   `nosignal-reconnect.yaml` (807 for 5 s mid-stream → session survives),
   `slow-consumer-drops.yaml` (8 s slowConsumer → no restart, sequence
   monotonic).
3. Unit tests: chunk alignment, byte-bounded drop-oldest queue, idle
   watchdog with the injected clock, dial timeouts against `httptest`.
4. Gates: `go test ./...`, `go test -tags ffmpeg -p 1 -parallel 2 ./...`,
   golangci-lint, iOS Simulator UI test and Chrome playback against the
   real HDHomeRun (channel 9.1) after a forced FFmpeg restart.

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
