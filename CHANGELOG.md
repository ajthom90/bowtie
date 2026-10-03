# Changelog

All notable changes to Bowtie are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/).

## [0.7.0] — 2026-10-03

### Added

- **Channels the antenna can't receive are marked.** The server learns each
  channel's reception from real tunes (the HDHomeRun only reports signal for
  a channel it's tuned to): a "no signal" answer marks the channel, a working
  stream clears it. Web, iOS, Apple TV, Android and Fire TV dim those
  channels and label them "No signal"; they stay tappable since signal can
  return. API: `reception` and `receptionCheckedAt` on `/api/v1/channels`
  and `/api/v1/guide`.
- **"All tuners in use" says when another app holds them.** The 503 body
  has `otherInUse` (tuners the HDHomeRun reports busy minus the ones Bowtie
  holds), and every app adds e.g. "1 tuner is in use by another app (like
  Plex)."
- **Apple TV app is ready for TestFlight**: layered app icon, Top Shelf
  images, and the iOS bundle ID so it ships as a tvOS platform of the same
  App Store Connect app.

## [0.6.2] — 2026-10-03

### Fixed

- **Playback resumes within about 2 seconds of the signal coming back.**
  While the tuner keeps answering "no signal", Bowtie now retries at most
  every 2 seconds instead of backing off toward 30 seconds. After a
  40-second loss of signal, playback resumed 22 seconds sooner (45 s gap
  instead of 67 s).
- **A long loss of signal no longer restarts FFmpeg when the signal
  returns.** The 30-second "stuck transcoder" rule counted the quiet time
  with no input as FFmpeg being stuck, so the first data after an outage
  longer than 30 seconds cut FFmpeg off. It now counts only time that data
  actually waited.

## [0.6.1] — 2026-10-03

Roku release: first build verified on a real Roku TV against production.

### Fixed

- **The Roku channel installs.** A stray `bs_const=true` in the manifest made
  the firmware reject the whole package.
- **Roku screens receive API responses.** A hidden screen tearing down its
  listener removed every screen's listener, so Connect never saw the server
  answer. Listeners are now scoped per screen.
- **The Roku channel list loads.** It crashed on a misnamed date method.
- **Back on Roku always ends the session.** A request sent at the same moment
  as another one could be dropped, leaving the viewer on the server until the
  idle timeout.
- **Quality is reachable on Roku TVs.** Roku TVs keep `*` for their picture
  menu during playback; press **Right** to open the quality dialog.
- Roku request bodies use the API's camelCase keys; Connect and Login are
  centered; the on-device self-test passes 41/41.

## [0.6.0] — 2026-10-02

Reliability release: ordinary hiccups no longer freeze or end a stream.

### Fixed

- **Players no longer freeze when FFmpeg restarts.** A restarted FFmpeg
  continues the playlist's numbering with a discontinuity marker instead of
  starting over at segment 0, which players treated as going back in time.
- **A briefly slow FFmpeg is no longer restarted.** Each transcoder gets a
  16 MiB buffer (about 7 s of 1080i); if it falls further behind, the oldest
  data is dropped (with the stream tables re-sent so playback resyncs)
  instead of cutting FFmpeg off after 2 s. It is restarted only if it takes
  nothing for 30 s.
- **Dead device connections are detected.** If the tuner sends nothing for
  8 s, Bowtie reconnects (previously it waited forever).
- **Weak signal during playback is retried.** A reconnect answered with
  "no signal" (HDHomeRun 806/807) keeps retrying for up to 60 s instead of
  ending every viewer's session as if the tuner had been taken.
- **Device requests time out.** Connecting to the tuner gives up after 5 s
  and waiting for its answer after 12 s; a hung tuner no longer hangs
  session start.
- **The device stream outlives the start request**, so every new session no
  longer reconnects to the tuner one second in.
- **Restarts Bowtie triggers itself no longer end playback.** FFmpeg used to
  write "end of stream" to the playlist when Bowtie closed its input, so
  players stopped during the restart.
- **A restart waiting on a slow tuner no longer stalls every viewer** on
  every channel; the device wait now happens outside the session lock.
- **A truly hung FFmpeg is stopped and restarted**, not just cut off from
  its input.
- **The admin tuners view no longer blocks** (or blocks other channels'
  starts) while one channel is still connecting to the tuner.

### Added

- Server log lines for FFmpeg exits (session, channel, backend, uptime,
  FFmpeg's error), restarts, dropped data and reconnects.

## [0.5.5] — 2026-10-02

### Fixed

- QSV: sessions no longer fail with "ffmpeg exited before playlist ready".
  The `vpp_qsv` scaler was given `w=-1`, which FFmpeg 5.1 (the image's
  build) doesn't treat as "keep aspect", so it produced a 0-pixel-wide frame
  ("Picture size 0x1088 is invalid"). The width is now an explicit
  aspect-preserving expression.
- When FFmpeg exits during startup, the error now ends with FFmpeg's last
  error lines, so admins see the actual cause in the app.

### Added

- iOS / tvOS: saved servers. The Connect screen lists every server you've
  used; tap one to switch, long-press to remove it. Each server keeps its own
  login, and "Change server" no longer signs you out, so switching between
  servers doesn't require signing in again. Existing installs keep their
  server and login.

## [0.5.4] — 2026-10-02

### Fixed

- iOS / tvOS: live channels no longer fail with "Playback stalled" as soon as
  they start. AVPlayer begins every load waiting to buffer, which the apps
  treated as a stall; their retries then reloaded the stream before it could
  start. A stall now needs playback to have started and a wait longer than
  4 s (or no start within 20 s).
- VideoToolbox (Mac-hosted servers): H.264 streams play on Apple devices
  again. The encoder's closed-caption SEI was malformed and AVPlayer
  rejected the whole stream (CoreMediaErrorDomain -12971); captions are now
  off for that encoder.

### Added

- iOS UI test that plays a channel end to end against a live server
  (skipped unless `TEST_RUNNER_BOWTIE_UITEST_URL` is set).

## [0.5.3] — 2026-10-02

### Fixed

- "Failed to start session" now says why. The underlying cause is always
  logged, and admins see it in the error message (for example in the iOS
  app), so a failing channel can be diagnosed without digging through logs.
- A channel the tuner can't receive (HDHomeRun `806 Tune Failed` /
  `807 No Video Data`, weak or no signal) is reported as "no signal on this
  channel" (HTTP 502) instead of "all tuners in use".
- Any other non-2xx device response (for example an unknown channel) is
  now an error. Previously it was streamed into FFmpeg as if it were video,
  which surfaced as a generic "failed to start session".
- The ingest tail and stall timers start when their countdown begins, not
  when their goroutine is scheduled.

### Added

- `GET /api/v1/version` reports the running server version (public).

## [0.5.2] — 2026-10-02

### Fixed

- QSV streams no longer fail with "failed to start session". The `vpp_qsv`
  filter was given `scale_mode=hq`, which the image's FFmpeg 5.1 rejects, so
  every QSV session died on its first frame. Exposed by 0.5.1 making QSV
  available (and selected by `auto`) on Gen12+ iGPUs.
- Docker image now reports its release version in the startup log instead of
  `0.1.0-dev`.

## [0.5.1] — 2026-08-07

### Fixed

- Docker image now includes the Intel oneVPL GPU runtime (`libmfx-gen1.2` +
  `libvpl2`), enabling the `qsv` encoder path on Gen12+ iGPUs (12th/13th-gen
  Core, e.g. i3-13100). Previously only `vaapi` probed on those chips — same
  silicon, but now both API paths are available.

## [0.5.0] — 2026-08-07

### Added

- **Live pause / rewind (DVR buffer)** — settings-backed
  `streaming.bufferMinutes` (default **15**, range 2–60). HLS list size follows
  the buffer at session start. Web seek bar (LIVE badge, skip-back 30s,
  jump-to-live); native scrubbers on iOS/tvOS and Android/Fire TV (DPAD ±30s).
  Out-of-window positions clamp to live with notice:
  *"Jumped to live — paused longer than the buffer"*. Roku: pause/resume only
  this cycle (REW probe documented for a later seek UI).
- **Tuner reuse** — one HDHomeRun tuner per channel regardless of concurrent
  quality/profile variants. Per-channel ingest fan-out with single-flight dial,
  join buffer (PAT/PMT), 5s empty tail, and reconnect. **Verified on real
  hardware:** dual-profile sessions on one channel hold a single tuner.
- **Session heartbeats** — `POST /api/v1/sessions/{viewerId}/heartbeat` every
  15s from all clients while the player is open (playing or paused), authorized
  with the **stream token** query param (or Bearer). Web also beats on
  `visibilitychange` → hidden. Roku enqueues only when ApiTask `queueDepth` < 3.
- **Viewer idle timeout** 30s → **90s** so throttled background tabs and brief
  client hiccups survive between heartbeats. Session empty-grace remains 60s.
- Admin settings: **Streaming buffer (minutes)** with tmpfs sizing hint.
  Deploy docs: segment tmpfs guidance **2g → 4g** with buffer math
  (~60 MB/min/session at top profile).

### Changed

- FFmpeg ingest path uses stdin (`pipe:0`) with `+discardcorrupt` when the
  session is fed by the shared ingest (URL input retained as fallback).
- Admin tuners payload includes `ingestChannels` (active ingest channel IDs).

### Breaking

None — API is additive (`streaming` settings section optional on PUT;
heartbeat endpoint new; existing clients keep working).

## [0.4.0] — 2026-08-07

### Added

- **EPG-less watching** — channels are watchable with zero guide configured.
  Empty guide state splits by role (admin vs viewer copy). Program-less cells
  say "No guide data — press to watch". Admins can **▶ Preview** disabled
  channels from Admin → Channels (viewers still get 404 for disabled).
- **Admin → Settings** control plane (DB-backed, restart-free):
  - XMLTV source + refresh interval
  - Schedules Direct username/password + lineup picker (`Load lineups`)
  - Encoder selection (from probe `available` + `auto`) and Allow HEVC
  - Per-section Save with "Saved." feedback; EPG tab keeps status + Refresh only
- **Mobile-friendly web** — 640px breakpoint: admin tables card-collapse,
  scrollable nav pills, touch targets ≥44px, guide/player polish, player
  quality bottom sheet on narrow viewports.
- Settings API: `GET`/`PUT /api/v1/admin/settings`, `GET /api/v1/admin/epg/lineups`.

### Changed

- EPG supervisor always runs and re-reads settings each cycle (enable/disable
  sources and change intervals without restart).
- Stream manager and `/admin/transcode` `selected` read encoder/HEVC from the
  same settings provider per session.
- iOS/tvOS empty-now copy: "Nothing on now" → "No guide data".
- Deploy docs (README, Compose comments, TrueNAS) point product settings at
  Admin → Settings.

### Breaking-ish (ops)

**`BOWTIE_ENCODER` and yaml EPG/transcode keys are first-boot seeds, not live
overrides.** On first start after upgrade, absent DB keys are presence-seeded
from env/`config.yaml` (defaults: encoder `auto`, refreshHours `12`,
allowHevc `false`). After a key exists in the DB — including an intentional
empty string (e.g. XMLTV disabled) — Admin → Settings is the sole source of
truth; changing env/yaml for those product keys no longer overrides stored
values. Infra keys still apply every start: listen address, data dir, segment
dir, FFmpeg path, `devices` / `BOWTIE_DEVICES`.

Existing deployments: first boot after upgrade seeds from current config —
zero behavior change until you edit Admin → Settings.
