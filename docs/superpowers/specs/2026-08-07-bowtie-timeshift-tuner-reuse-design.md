# Bowtie — v0.5.0 Design: Live Pause/Rewind + Tuner Reuse

**Date:** 2026-08-07
**Status:** Approved (pending implementation)
**Motivation:** (a) Live TV should pause and rewind like a DVR within a bounded
buffer; (b) one broadcast channel must consume ONE HDHomeRun tuner regardless of
how many transcode variants (quality/codec) are being watched.

## Decisions of record

| Topic | Decision |
|---|---|
| DVR buffer | Settings-backed `stream.bufferMinutes`, default **15**, range 2–60; `hls_list_size = bufferMinutes*60/4` |
| Tuner reuse | Per-channel **ingest fan-out**: one device HTTP stream per channel, teed to all sessions on that channel; FFmpeg reads `pipe:0` |
| Viewer liveness | New explicit heartbeat endpoint every 15s from all clients while the player is open (paused included); playlist fetches still count |
| Roku scope | Pause/resume only; seek-back pending user's on-device validation |
| Client scrubbers | AVPlayer native (free), ExoPlayer live window, web custom seek bar + LIVE badge + 30s skip-back, Fire TV DPAD left/right = ±30s |

## A. Per-channel ingest fan-out (`server/internal/stream/ingest.go`)

```go
type IngestManager struct{ /* owned by stream.Manager; keyed by channelID; per-channel mutex */ }
func (im *IngestManager) Attach(ctx context.Context, channelID int64, url string) (*IngestSub, error)
type IngestSub struct { R io.ReadCloser }
func (s *IngestSub) Close() error
```

### Lifecycle (BINDING — this is where the risk lives)

- **IngestSub lifetime = ONE FFmpeg process life.** `Attach` is called for EVERY
  process start: initial session start AND every crash-restart
  (`restartSessionLocked` re-Attaches and builds the new Cmd with the fresh
  sub). `Close` on process death, force-close, session teardown, admin
  Terminate, and the duplicate-key abandon path in `startAttempt`. The pipe
  NEVER survives an FFmpeg exit — a dead process's stdin reader is gone and
  cannot be revived.
- **Single-flight dial:** Attach holds a per-channel mutex; concurrent Attaches
  for one channel observe-or-create exactly one device connection. The
  tuner-reuse invariant (one device stream per channel) is enforced here, not
  hoped for.
- **Refcount:** attached subs. Last `Close` → **5-second tail** (absorbs
  restart churn only), then the device stream closes. There is NO second 60s
  grace at the ingest layer: the session's own empty-grace (60s, unchanged)
  already keeps its sub attached for zap-back. **End-to-end tuner-free budget:
  ≤65s after the last interested session ends — identical to v0.4.0 — and ~5s
  after an admin Terminate.** A test asserts the budget.
- **Pump algorithm (concrete):** the pump reads the device stream into 64 KB
  chunks. Each sub owns a buffered channel (cap 64 chunks ≈ 4 MB) drained by a
  per-sub goroutine writing to that sub's io.Pipe. The pump's send is
  non-blocking: a full sub channel marks the sub stalled; stalled >2s →
  force-Close THAT sub only (its session takes the normal crash path, whose
  restart re-Attaches a fresh sub). The pump and other subs are never blocked
  by one consumer. ATSC reality: ~2 MB/s per mux — 4 MB ≈ 2s of slack.
- **Mid-stream join:** the ingest keeps a rolling **join buffer**: the most
  recent PAT and PMT packets plus all TS packets since the last PAT (bounded
  1 MB). A new sub receives the join buffer first, then live chunks — FFmpeg
  demuxes promptly regardless of table cycle timing, on every backend
  (including `mpeg2_qsv` before `-i`). Tests: second Attach long after pump
  start produces a playlist within the 15s budget; crash re-Attach ditto.
- **Device 503 (FIELD FINDING from real-hardware testing 2026-08-07):** when
  the dial returns the device's 503 all-tuners-busy, Attach fails IMMEDIATELY
  with a typed tuners-busy error → Start surfaces the standard 503
  who's-watching payload. No blind open-retry loops (the pre-ingest code
  retried 13+ times against a busy device). Mid-stream reconnect hitting 503
  (tuner stolen by another household device — observed live: a family member
  was watching FOX 9 during testing) closes all subs; sessions surface
  tuners-busy.
- **Reconnect:** non-503 device errors/EOF → reconnect with backoff
  (1s,2s,4s..30s) WITHOUT closing subs (FFmpeg blocks on a quiet pipe);
  after 60s of failures, close subs → sessions take the stall/error path.
- **Input hardening (field finding):** real ATSC input contains corrupt
  packets even at good signal (observed: PES mismatches, AC-3 exponent
  errors at SEQ 100%). FFmpeg already tolerates them; add `-fflags
  +discardcorrupt` to input args to reduce artifact propagation. Not a
  correctness gate.
- `transcode.JobSpec` gains `Stdin io.Reader`; `BuildArgs` emits `-i pipe:0`
  when set (hw input flags unchanged); `Command` wires `cmd.Stdin`. Golden
  tests updated; a BuildArgs assertion + fake-HDHR dial count proves FFmpeg is
  NOT dialing the device itself.
- Admin Tuners/Sessions UI: tuners-in-use == distinct channels being ingested.

## B. DVR window (pause/rewind buffer)

- Settings: **`streaming.bufferMinutes`** (this exact store key; int, default
  15, validate 2–60). NEW top-level section `streaming` in the settings API:
  GET always returns it; PUT treats it as an optional section per the v0.4.0
  section-merge contract (existing sections unchanged; openapi delta: add
  `streaming` to the Settings schema, NOT to the required list — spell the
  schema out). Provider gains `Streaming()/SetStreaming`. Seeded (presence-
  based) with default 15. Admin → Settings UI adds the field with tmpfs sizing
  hint copy.
- FFmpeg: `-hls_list_size` computed from bufferMinutes at session start (window
  slides; `delete_segments` stays — memory is bounded to the buffer). Segment
  filenames/rotation unchanged.
- Read at session start (not live-mutable mid-session — documented; new
  sessions pick up changes, consistent with encoder semantics).
- tmpfs guidance: ~60 MB/min/session at top profile → docs suggest
  `size=4g` default in TrueNAS/compose examples with the math shown.
- Playlist serving: unchanged mechanically (225 lines at 15 min — trivial).
- **Out-of-window UX contract (all clients):** when the playback position
  falls out of the sliding window (paused/rewound longer than the buffer),
  the client CLAMPS to the live edge: force jump-to-live + a brief
  non-blocking notice ("Jumped to live — paused longer than the buffer").
  Never a hard error, never a stall loop. Web implements via seekable-range
  clamp; AVPlayer/ExoPlayer via seek-to-live on range-exceeded; Roku N/A this
  cycle (resume beyond buffer lands at live by nature of the window).

## C. Heartbeats

- `POST /api/v1/sessions/{viewerId}/heartbeat` — auth: stream token query param
  OR Bearer (mirrors DELETE); clients MUST send the stream token (it never
  rotates mid-session and avoids racing token refresh). 204; touches the
  viewer. openapi + Routes().
- **`viewerIdleTimeout` rises 30s → 90s** so throttled background browser tabs
  (timers clamped to ~1/min) and momentary client hiccups survive between
  heartbeats. Session empty-grace stays 60s. Reaper mechanics unchanged.
- Web additionally fires an immediate heartbeat on `visibilitychange` →
  hidden. A fully suspended tab may still eventually reap — accepted and
  documented (foreground/PiP/paused-visible are the supported cases; same
  acceptance for iOS fully-suspended background per existing contract).
- Roku: heartbeat enqueues to ApiTask ONLY when queue depth < 3 (never starves
  behind create/refresh bursts; the next 15s tick covers a skipped beat).
- Clients: while the player screen is open (playing OR paused): web `setInterval`
  15s; iOS/tvOS Timer on PlayerModel; Android/Fire TV coroutine ticker in
  PlayerViewModel (lifecycle-scoped, continues in PiP); Roku 15s Timer node in
  PlayerScene. Stops on real leave (existing stop paths).

## D. Client trick-play UX

- **Web:** seek bar under the video (input range styled with tokens): shows
  buffered live window (from hls.js `liveSyncPosition`/levelDetails), thumb at
  current position; LIVE badge (amber when at edge, dim "-mm:ss" when behind);
  buttons: skip-back 30s, jump-to-live; space/click pause as today. hls.js
  config: default live window behavior (no `liveMaxLatencyDuration` override);
  seeking clamped to seekable range.
- **iOS/iPadOS:** nothing — `AVPlayerViewController` shows its live-DVR
  scrubber automatically once the window exceeds ~2 min. Verify + remove any
  `requiresLinearPlayback` if set.
- **tvOS:** native transport already scrubs live-DVR streams; verify.
- **Android/Fire TV:** ExoPlayer `PlayerView`/controls expose the live window;
  enable seek controls (`setShowFastForwardButton/RewindButton` or default
  controller flags); Fire TV adds DPAD LEFT/RIGHT = seek ∓30s in the key
  handler (only while controls hidden; drawer interactions unchanged).
- **Roku:** OK toggles pause/resume (exists); no seek UI this cycle. Manifest
  of the validation doc gains a pause→resume-after-3-min step (proves
  heartbeat + window on device) and an EXPERIMENTAL seek probe step (REW button
  behavior recorded for a future cycle).

## Testing

- Ingest (heaviest): unit — two attaches one dial (fake HTTP TS source),
  fan-out delivers identical bytes to both subs, last-detach stops after grace,
  reconnect survives blip without closing subs, stuck-subscriber force-close;
  e2e (fake HDHR) — TWO sessions different profiles on ONE channel → fake's
  ActiveStreams()==1 (THE tuner-reuse assertion) and both playlists serve.
- BuildArgs: pipe:0 golden args (all backends); Command stdin wiring.
- Heartbeat: endpoint auth paths; reaper keeps heartbeating-paused viewer alive
  (fake clock).
- Settings: bufferMinutes validation/seed; list_size derivation unit test.
- Web: seek-bar math (position↔window mapping) vitest; skip-back clamp.
- iOS/Android: PlayerModel/ViewModel heartbeat timer tests (virtual time).
- Ingest lifecycle additions (each a named test): concurrent dual-profile
  Start → ONE dial (single-flight); crash ×2 within 60s → re-Attach each
  restart, playlist recovers; force-close of a stalled sub leaves co-watcher
  flowing; DELETE/Terminate of session A leaves session B's playlist
  advancing; quality-replace keeps device refcount ≥1 throughout (no redial);
  tuner-free budget ≤65s after last session ends (fake clock); dial-503 →
  immediate tuners-busy payload.
- Settings: `streaming` omitted-section PUT round-trip.
- Web: hidden-tab immediate heartbeat; out-of-window clamp + notice logic.
- User validation: pause 3 min → resume per platform; pause LONGER than a
  temporarily-2-min buffer → clamps to live with notice; rewind on
  web/mobile/TV; Roku pause step; TrueNAS tmpfs under 2 concurrent sessions;
  zap between channels under family tuner load.

## Out of scope

Persistent DVR/recordings, per-user buffer limits, catch-up beyond the live
window, Roku seek UI (pending validation), ATSC 3.0.

## Review history

- 2026-08-07: Initial draft (Claude), incorporating user requirements: 15-min
  default buffer, Roku pause-only, tuner reuse across quality variants.
- 2026-08-07: Grok review (10 findings, all incorporated) — ingest lifecycle
  REWRITTEN: sub-per-process-life with re-Attach on every restart (the
  pipe-survives-restart claim was false); single-flight dial; concrete pump
  algorithm; PAT/PMT join buffer for mid-stream Attach; dual-grace collapsed
  (5s ingest tail; tuner-free ≤65s, same as v0.4.0); out-of-window clamp UX;
  settings normalized to streaming.bufferMinutes with explicit openapi delta;
  heartbeat hardening (stream-token auth, 90s viewer timeout, web visibility
  beat, Roku queue-depth guard); ingest test bar expanded to full lifecycle.
- 2026-08-07: REAL-HARDWARE findings (first live test against the user's
  HDHomeRun CONNECT DUO, 192.168.50.32 — WCCO transcoded via VideoToolbox
  end-to-end): device-503 on dial → immediate tuners-busy (observed 13 blind
  retries pre-ingest); grace-held tuners cause real 503s under family load
  (tuner1 was live with household FOX 9 viewing mid-test); real ATSC input is
  dirty even at SEQ 100% (+discardcorrupt hardening).
