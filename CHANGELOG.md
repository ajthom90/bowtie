# Changelog

All notable changes to Bowtie are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/).

## [0.14.0] — 2026-10-04

### Added

- **Skip commercials.** When [Comskip](https://github.com/erikkaashoek/Comskip)
  is available (the Docker image includes it), Bowtie finds the commercial
  breaks in each finished recording in the background, and every recording
  player — web, iPhone/iPad, Apple TV, Mac, Android, Android TV/Fire TV and
  Roku — shows **Skip ad** while you're in one. Turn on **Skip ads
  automatically** to skip each break once. Older recordings are scanned too,
  newest first. Set `BOWTIE_COMSKIP_PATH=off` to turn it off. After tuning
  `comskip.ini`, admins can press **Find ads again** in the web recording
  player. See README → Commercial detection.
- **Sleep timer on Roku** (live TV and recordings), like the other apps.

### Changed

- **Roku:** Right in the live player now opens **Options** (quality and the
  sleep timer); Down in a recording opens the sleep timer; Settings can be
  moved through with Up/Down.

## [0.13.0] — 2026-10-04

### Added

- **Multiview (web).** Watch up to four live channels at once — 1, 2 or a
  2×2 grid (stacked on phones). One tile has sound: click a tile or press
  1–4 to move it. Each tile can change channel, go full screen or close, and
  shows its own error (tuners busy, stream limit, no signal) without stopping
  the others. Tiles without sound start at a lower quality to save bandwidth.
  "Restore last" brings back your last set (it never starts streams on its
  own). Open it from the Multiview button in the guide.
- **Sleep timer** on iPhone/iPad, Apple TV, Mac, Android and Android TV /
  Fire TV: 15 minutes to 2 hours, or "End of this program". A minute before,
  a "Still watching?" prompt lets you keep going; otherwise playback stops
  and the tuner is freed.
- **Recording storage and padding (Admin → Recordings).** A storage gauge
  (recordings, other files, free space), recording counts, a warning when the
  disk is almost full, and how early to start / how long to keep recording
  (were fixed at 1 and 3 minutes). End padding now gives way: if another
  recording needs the tuner when its show starts, a recording that is only
  in its end padding stops early.
- **Backup (Admin → Settings → Download backup).** A snapshot of accounts,
  channels, guide matches, series rules, the recording list and settings —
  see README → Backup and restore. Token-signing keys and sign-in sessions
  are left out, so restoring signs everyone out.
- **Roadmap** (`docs/roadmap.md`): what antenna viewers ask for most, what
  Bowtie covers, and what's next.

### Fixed

- **Apple apps:** playing a recording no longer stops live TV until the
  recording actually starts; if it can't, live TV keeps playing.

## [0.12.1] — 2026-10-04

### Fixed

- **Changing channels from an IPTV player at your stream limit.** Players
  like TiviMate and Kodi often open the next channel before closing the last;
  at an account's stream limit the new channel now replaces the old one
  instead of failing.
- **Deleting a recording while it converts** stops the conversion right away
  instead of letting FFmpeg finish into a deleted folder.
- **Turning off the free HDHomeRun guide** now removes its listings and the
  channel matches it made (matches you set by hand stay).
- **Guide search on restricted accounts** fills its results from allowed
  programs, and blocked programs no longer match on their descriptions.
- **TV sign-in codes** are limited to 10 per device every 10 minutes.

## [0.12.0] — 2026-10-04

### Added

- **Free TV guide from your HDHomeRun (no account needed).** Bowtie now
  fetches the guide SiliconDust gives every HDHomeRun owner (about 2-3 days
  ahead; 14 with an HDHomeRun DVR subscription) and matches it to your
  channels by number, so the guide fills in on its own. Channels you already
  mapped are left alone, and it refreshes once a day at a random time. It is
  on by default; turn it off with the `hdhomerun.enabled` admin setting.
  Episode and series IDs and new/repeat flags come along, so series
  recordings work with it too.

- **Mac app.** A native macOS app (sidebar with Favorites, Recent, Channels
  and Recordings; full screen, picture in picture; Space, L for live,
  ⌘↑/⌘↓ to change channel). Built in CI alongside iOS; see `ios/README.md`
  for adding it to App Store Connect or notarizing it.
- **Sign in with your phone on TVs.** Apple TV, Android TV / Fire TV and Roku
  open on a QR code: scan it, approve on your phone, and the TV is signed in —
  no typing a password with a remote. Or open `<server>/link` and enter the
  code. (Account → Enter a TV code in the web app.)
- **Record a series.** "Record series" next to Record (web, iPhone/iPad,
  Apple TV, Mac, Android, Fire TV, Roku) records every new episode on that
  channel (web: any channel, all episodes, keep the latest N). Shows you
  record are under Recordings → Shows. Skipping an upcoming episode keeps it
  skipped.
- **Search the guide.** Search titles, episodes and descriptions from the
  web, iPhone/iPad, Mac, Apple TV and Android, then watch or record (or
  record the series) from the results.
- **Parental controls.** In Admin → Users, limit an account to some channels,
  a maximum rating (TV-Y … TV-MA) and optionally block unrated programs.
  Blocked programs show 🔒 in the guide and recordings; starting one is
  refused, and a show that changes into a blocked rating stops. Ratings come
  from the guide (Schedules Direct has them; the free HDHomeRun guide
  doesn't). Admins are never limited.
- **Use Bowtie in other apps (Xbox via Kodi, VLC, TiviMate, Plex,
  Jellyfin).** Account → "Use Bowtie in other apps" gives you a personal M3U
  playlist and XMLTV guide link. Kodi's IPTV Simple Client on an Xbox, VLC,
  TiviMate, Plex and Jellyfin can then watch through Bowtie (with your
  account's limits and parental controls).
- **SharePlay on iPhone, iPad and Apple TV.** Watch a channel together on a
  FaceTime call: everyone joins the sharer's stream, and pause, rewind and
  Live stay in sync. Each person signs in to your server with their own
  account. (Needs the Group Activities capability on the App ID; test on
  real devices.)

### Fixed

- Clearing a channel's guide mapping now means "no guide" (automatic
  mapping won't fill it back in).
- Android: "Try again" on a recording gets a fresh link (it failed after the
  link expired).

### Changed

- **New logo.** The icon is now an old-school UHF bowtie TV antenna instead
  of a necktie bowtie, on every app: iPhone/iPad, Apple TV (layered icon and
  top shelf), Android (adaptive and themed icon, which replaces the stock
  Android icon), Android TV / Fire TV (icon and banner), Roku (channel poster
  and splash), and the web (favicon, home-screen icon, header and sign-in).
  Source SVGs are in `docs/brand/`.

## [0.11.0] — 2026-10-04

### Added

- **DVR: record shows.** Pick a program in the web guide (or long-press a
  channel on iPhone/iPad, Apple TV, Android and Fire TV) and choose
  **Record**. Recordings capture the broadcast from the antenna (a recording
  on a channel someone is watching shares their tuner), start a minute early
  and end three minutes late, and keep trying for the whole show if every
  tuner is busy. When the show ends Bowtie converts it for playback; then it
  appears under **Recordings** with seeking and resume where you left off.
  Missed recordings say why ("No tuner was free"). Scheduling more shows at
  once than you have tuners asks before going ahead. Mark a recording
  **Keep** so it's never deleted to free space. Recordings are stored in
  `/data/recordings` (`BOWTIE_RECORDINGS_DIR`); see the TrueNAS guide.
- **Windows app.** A native app for Windows 10 and 11 on Intel/AMD (x64)
  and ARM PCs (Surface Pro X and Copilot+ PCs). Sign in to your server once
  (it's remembered in the Windows Credential Locker), then watch live TV
  with favorites first and a Recent row, pause and rewind live TV (← and →
  skip 30 seconds), jump back with **Go Live**, pick quality, audio and
  captions, change channels with Page Up/Page Down, and go full screen with
  F11 or a double-click. **Recordings** play with resume and can be kept,
  stopped or deleted, with **Skip ad** over detected commercial breaks (and
  **Skip ads automatically** in the account menu). A **sleep timer** and the
  **All · Sports · Movies · News · Kids · New** guide filters work like the
  other apps. Each release attaches `bowtie-windows.msixbundle` (and
  the `.cer` to trust before installing) plus zips that run without
  installing; see `windows/README.md`.

### Fixed

- **Signed out in a second tab.** Two tabs (or an app waking up twice)
  refreshing at the same moment no longer signs you out.

## [0.10.0] — 2026-10-04

### Added

- **Favorites.** Star a channel (☆ in the web guide; swipe or long-press on
  iPhone/iPad; click-and-hold Select on Apple TV; the star button or
  long-press on Android; hold OK or ☰ on Fire TV / Android TV; `*` on Roku).
  Favorites sit at the top everywhere, and channel up/down on TV visits them
  first. Favorites follow your account across devices.
- **Recent.** A row of the channels you watched lately (after 30 seconds of
  watching, so zapping past a channel doesn't count) on every app.

## [0.9.0] — 2026-10-03

### Added

- **Per-account limits.** In **Admin → Users**, set how many **Streams** an
  account may watch at once and how many **Tuners** it may use, so someone you
  share the antenna with can't take every tuner. Watching a channel another
  account is already watching never uses a tuner, so it doesn't count. Admins
  are never limited. A viewer over a limit sees, for example, "Your account
  can use 1 tuner at a time. Stop another channel first." (HTTP 429,
  `code: "user_limit"`).

### Fixed

- **Android/Fire TV: quick channel changes no longer leave a stream running
  on the server.** Changing channel again before the last one started now
  cancels that request outright, so it can't count against an account's
  limit (or hold a tuner) until it times out.

## [0.8.0] — 2026-10-03

### Added

- **Closed captions.** The broadcast's captions (CEA-608) become a separate
  caption track that works with every encoder, including Intel QSV. Turn
  them on with **CC** in the web and Android players, the system Subtitles
  menu on iPhone/iPad/Apple TV, or `*` on Roku.
- **Other audio languages and 5.1.** Every broadcast audio track is offered
  (e.g. FOX 9's Español): **Audio** in the web, iPhone/iPad and Android
  players, the system audio panel on Apple TV, `*` on Roku. The original
  Dolby 5.1 is also offered; surround-capable devices pick it automatically.
- **Choices are remembered** per device (audio language, captions on/off).
- **Shared rewind across accounts.** Viewers of a channel at the same quality
  share one transcode and one rewind window, whatever their account or audio
  format (AC-3 and AAC clients no longer split).
- **Adaptive quality (admin switch, off by default).** Admin → Settings →
  Streaming → *Adaptive quality*: one transcode per channel at
  1080/720/480/360 (never above the broadcast) that **every** viewer shares;
  players pick the quality for their connection, and a viewer's quality
  setting or account limit caps it. Check it on your GPU first:
  `docs/deploy/adaptive-quality-check.md`.
- **No more upscaling:** "Original" on a 720p channel now stays 720p.
- Quality pickers read as "the most this player will use".
- `BOWTIE_MULTITRACK=off` turns off captions, extra audio and 5.1.
- If a channel can't start with all its tracks, it falls back to one quality
  and its main audio automatically.
- **iPhone/iPad: "Live" button.** The player's top controls show **Live** with
  a red dot while you're at the live point. When you've rewound (or a channel
  you just tuned starts behind), it shows how far behind you are
  (e.g. "Live −0:20"); tap it to jump back to live.
- **Easy Android and Fire TV installs.** Open `https://<your-server>/android`
  on a phone, or type `https://<your-server>/tv` into the free Downloader app
  on a Fire TV, to get the app that matches your server
  ([guide](docs/install/android.md)). APKs now carry the release version, so
  each release installs over the last.

### Fixed

- **Android phone: the Bowtie controls (Back, Quality, Stats) never came back**
  after their first auto-hide; they now show with the player controls and sit
  under the channel name, clear of the scrubber.
- **iPhone/iPad: live rewind controls were unreachable.** Bowtie's own player
  chrome caught every tap, so the system scrubber, ±10 s skip and jump-to-live
  never appeared. Taps now reach the system controls, and Bowtie's quality,
  stats and Done buttons sit in the top row (AirPlay uses the system button).

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
