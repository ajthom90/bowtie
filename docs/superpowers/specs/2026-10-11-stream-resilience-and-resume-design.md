# Stream resilience, same-moment recovery, and resume — Design

Date: 2026-10-11. Requested after live TV on the iPhone drifted out of A/V sync
and kept jumping back to the start of the saved window during weak antenna
signal: "if there's a slight outage in the antenna signal it [shouldn't] mess
everything up with the streams." The user then set two requirements:

1. **Recover to the same moment.** After a drop, apps resume as close as
   possible to where the viewer was. If they were 8m13s behind live, they
   resume about 8m13s behind live, at the same broadcast moment. Never jump to
   live, and never to the start of the window.
2. **Resume where you left off** (option A, chosen 2026-10-11). The server
   remembers each user's last moment per program. On return the app offers
   "Resume at 12:24" or "Watch live", but only when that footage still exists:
   a recording of the program, or a live session someone else kept open. Bowtie
   does not keep a channel running after its last viewer leaves (no extra tuner
   or disk). So the user's own example (start at noon, leave at 12:24, come
   back at 12:52) resumes only when the game is being recorded. The app should
   make recording from the player easy.

Standing approval: the user approved building this without further gates.
Evidence: `.superpowers/sdd/av-sync/investigation.md` (git-ignored) and
production logs from 2026-10-11 20:39–21:01, channel 14, QSV.

## Root cause (from production logs and reproduction)

- **Corrupt timestamps make FFmpeg invent video.**
  - With no `-fps_mode` set, a single video PES whose PTS is off by a few
    seconds makes FFmpeg duplicate frames to fill the gap: dup=270 for +9 s
    ("More than 1000 frames duplicated" in production).
  - FFmpeg then re-bases the whole input, which leaves an equal hole in the
    audio. Audio and video drift apart by that amount.
- **Audio holes are never filled.** The AC-3 decoder rejects bad frames
  (`-16976906`) and corrupt audio PES are dropped. There is no
  `aresample=async`, so audio loses time while video doesn't.
- **The duplicate bursts make FFmpeg fall behind.** Each duplicated frame is
  scaled and encoded on every QSV rung. The ingest queue overflows ("transcoder
  behind, dropped N KiB"), and every drop is a new hole.
- **The "stuck" detector then kills FFmpeg.** After 30 s with no reads,
  `forceClose` sends SIGKILL and restarts FFmpeg with `append_list`. In
  production this happened at 20:39:27, 20:45:12, 20:45:52 and 20:49:17.
  - Each restart leaves 30 s or more with no new segments and resets the PTS.
  - FFmpeg 5.1 adds a DISCONTINUITY at the top of the window and never writes
    `EXT-X-DISCONTINUITY-SEQUENCE`.
  - The iPhone stall recovery reloads the item, and AVPlayer or the
    live-window clamp picks the start or the end of the window: the jumps.
- **FFmpeg's PROGRAM-DATE-TIME (PDT) isn't wall clock.** PDT is the process
  start time plus the sum of segment durations. It runs ahead on invented
  frames, falls behind on collapsed gaps, and snaps back (even backwards) at a
  restart. So it can't anchor "the same moment".
- **Smaller problems:**
  - Restart join: the new FFmpeg joins at the last PAT, mid-GOP. That causes
    "Invalid frame dimensions 0x0" ×13–41 and slows start-up.
  - `thread_queue_size` is 8.
  - FFmpeg 5.1's WebVTT append bug reloads caption entries as `.`, so old
    `.vtt` files are never deleted: log spam and a small tmpfs leak.

## Goals

1. A brief antenna glitch (corruption, or a gap of a few seconds) costs at most
   a moment of frozen or blocky picture. Audio stays in sync, and FFmpeg keeps
   running.
2. When a restart is unavoidable (a long outage, a hung encoder), the playlist
   stays valid. The media sequence and the discontinuity sequence are
   monotonic, and PDT is real wall clock and monotonic. Every client resumes at
   the viewer's broadcast moment, or the nearest one that still exists.
3. Resume where you left off, across sessions and devices, whenever the
   footage still exists.

## Part 1 — Server resilience (ships first, on its own: Docker update)

### 1.1 FFmpeg input and audio hardening (`internal/transcode/ffmpeg.go`)
- Main pipe input:
  - `-fflags +discardcorrupt+genpts -err_detect ignore_err -thread_queue_size 1024 -probesize 20M -analyzeduration 10M`.
  - The same `-thread_queue_size`, probe and analyze options on the caption
    `movie` input.
- Timestamp handling: choose between `-dts_delta_threshold 1` and `-fps_mode vfr`
  (or `passthrough`) by test on **FFmpeg 5.1** in the production image (Docker
  on the dev Mac, software encode; QSV is checked by the user after release).
  - The chosen option must give no duplicate storms on the `ptsjump` fault.
  - It must give no output timeline inflation.
  - It must give zero false "timestamp discontinuity" re-bases on a clean
    real-broadcast soak that covers ad splices (see Testing).
  - Prefer the option that doesn't fire on splices; if both pass, prefer
    `fps_mode`, which has less magic.
- Every audio output: `aresample=async=1:min_hard_comp=0.02:first_pts=0`, so
  audio is sample-continuous and stays in sync with video timestamps.
- VOD and concat inputs keep their current flags. Only the live pipe changes.

### 1.2 Stuck handling (`internal/stream/ingest.go`, `manager.go`)
- When FFmpeg reads nothing for 30 s, first **drain**: drop the queued backlog
  and re-send PAT, PMT and the last sequence header, as an overflow drop does.
  Log `speed`/`dup` if available.
- Kill (SIGKILL plus append restart) only if FFmpeg still reads nothing after a
  further grace period (30 s). A wedged QSV encoder still gets restarted;
  slow-but-alive FFmpeg is no longer killed.

### 1.3 Join at a video sequence header (`ingest.go`)
A new sub (restart or caption tap) starts at the last MPEG-2 or H.264 sequence
header / IDR on the video PID, plus PAT and PMT, instead of the last PAT. This
fixes the 0x0 storm and makes restarts faster.

### 1.4 Bowtie-owned timeline in served playlists (`internal/api` playlist serving, `internal/stream`)
Bowtie rewrites media playlists on the way out (it already does for captions).
It adds:
- **PDT:** Bowtie records the wall-clock start of each segment when it first
  sees it (playlist poll or file mtime), keyed by session, rendition and media
  sequence.
  - The record is monotonic: never earlier than the previous segment's end.
    After a restart or outage it jumps forward by the real gap.
  - For the same broadcast moment it is identical across renditions: align by
    segment index across renditions cut at the same time, within one segment.
  - Served `#EXT-X-PROGRAM-DATE-TIME` uses Bowtie's values, never FFmpeg's.
- **Discontinuities:**
  - Bowtie tracks every discontinuity at or after an append restart and
    serves `#EXT-X-DISCONTINUITY-SEQUENCE`, incremented as tagged segments
    slide out of the window.
  - It drops 5.1's spurious DISCONTINUITY at the top of the window when it
    isn't a real boundary.
  - Discontinuity tags sit at the same broadcast moment in every rendition.
- The media sequence stays monotonic across restarts. FFmpeg already does
  this; it is asserted in tests.

### 1.5 Timeline endpoint for clients without PDT (Roku)
`GET /api/v1/sessions/{viewerId}/timeline` (signed in, the viewer's own
session) returns:
- `windowStart`, `liveEdge`: wall clock, RFC 3339 with milliseconds;
- `segments`: a compact list of `[startWallClock, durationSec]` for the
  window, or ranges with gaps marked.

With it a client can map "broadcast moment T" to an offset into the current
window.

### 1.6 Caption `.vtt` sweeper
The session maintenance loop deletes `.vtt` files in the session dir that are
older than the window plus one segment and aren't referenced by the current
caption playlist.

## Part 2 — Same-moment recovery in every app (app updates; web ships in the image)

Rule for every client:
1. Before any teardown, reload or reconnect, save `targetMoment`: the wall clock
   of the current position, from PDT or Roku's timeline.
2. Recover the stream.
3. Once the new item is ready, seek to `targetMoment`:
   - inside the window: there;
   - inside an outage gap: the start of the next segment after it;
   - before the window (aged out): window start + one segment, with a short
     "Resumed at the earliest available point";
   - after the live edge: live.
4. No live-edge clamp may run during recovery. The iOS out-of-window clamp
   clamps forward to window start + one segment, never to the live end.

| App | Read moment | Seek to moment | Change |
|---|---|---|---|
| iPhone, iPad, Apple TV, Mac | `AVPlayerItem.currentDate()` | `item.seek(to: Date)` after `.readyToPlay` | StallGate item reload (`PlayerView.swift`), `PlayerBridge` clamp, tvOS `TVPlayerView` clamp, Mac player |
| Android phone / TV | window `windowStartTimeMs` + `currentPosition` | `seekTo(target − windowStartTimeMs)` after prepare | `PlayerEngine.kt` network-error and BehindLiveWindow paths currently `seekToDefaultPosition()` (live) |
| Roku | timeline endpoint (1.5) + `video.position` | `video.seek = target − windowStart` | PlayerScene retry re-bind (`startPlayback`) |
| Web (hls.js and Safari native) | `hls.playingDate` / `video.getStartDate()` + `currentTime` | frag lookup by `programDateTime` | fatal-error path only shows an error today; add media/network recovery, then seek |

The moment logic (pick the seek target from a target moment and the window's
segment list) is a pure, unit-tested function on each platform. Apple keeps it
in BowtieKit.

## Part 3 — Resume where you left off (option A)

### 3.1 Server
- **Watch-position store** (SQLite, existing DB), per user. Key:
  - `(channelId, programStart)` when the guide has a program;
  - `(channelId, wall-clock hour bucket)` when there's no guide data, e.g. 2.3
    "No guide data".
  - Value: `moment` (wall clock), `programTitle`, `programEnd`, `updatedAt`.
- **Writes:** players report it with the existing heartbeat. The heartbeat
  carries `moment`; no new client timer is needed. The final moment is also
  reported on leave (session DELETE body or a last heartbeat).
- **Resume offer:** `GET /api/v1/channels/{id}/resume` returns
  `{ moment, programTitle, source: "live"|"recording", recordingId? }`, or 204.
  It returns an offer only when all of these hold:
  - the program is still the current program on that channel, or a recording
    of it exists;
  - the footage at `moment` still exists: a live session for that channel
    whose window covers `moment`, or a recording covering it;
  - `moment` is more than about 30 s behind live, otherwise it's just live.
- Recordings already store position (`PUT /recordings/{id}/position`).
  Opening a recording keeps that behaviour. The new offer is for opening the
  *channel* while the program is on.
- Positions expire after 24 h.

### 3.2 Apps
- Opening a channel: if the resume endpoint returns an offer, show
  **Resume at 12:24** (local time, with the program title) and **Watch live**,
  focus on Resume, auto-dismiss to live after 10 s.
  - `live`: start the session, then seek to the moment (Part 2 seek).
  - `recording`: open the recording at the matching offset, while it's still
    in progress.
- **Record from the player:** a "Record this program" action in the player's
  options (Roku Right → Options, Apple and Android player menus, web player
  controls), using the existing createRecording API for the current guide
  program. Hidden when there is no guide program or recording is unavailable.

## Not in scope
- Keeping a channel running after its last viewer leaves (options B/C).
- The idle-tuner fix (empty sessions holding tuners), Roku rewind/FF/back-to-live,
  the Roku pairing idle stall, and the web zap cancel.
- Moving captions to a separate FFmpeg process (A1c), unless tests show the
  caption input still gates the main loop after 1.1–1.3.

## Testing
- **Fault scenarios** (`server/internal/e2e/testdata/scenarios/`, fake HDHR
  faults):
  - `ptsjump` (new fault: +3/+6/+9 s on one video PES);
  - `corrupt-audio` (tei 1% for 10 s);
  - `brief-dropout` (stall 4 s);
  - `threshold-dropout` (9 s and 12 s);
  - `long-outage-restart` (65 s, forced append restart, window slides past it).
- **testplayer** loads every rendition and asserts:
  - a monotonic MEDIA-SEQUENCE;
  - DISCONTINUITY tags at the same moment in every rendition;
  - DISCONTINUITY-SEQUENCE correct as the window slides;
  - served PDT monotonic and aligned across renditions;
  - Σ EXTINF ≈ wall time minus outages;
  - no audio hole over 50 ms (ffprobe);
  - A/V first-PTS alignment within 100 ms per segment pair;
  - zero FFmpeg restarts for the short faults.
- **FFmpeg 5.1:** run the fault suite through the production image's ffmpeg
  (Docker, software encode) to choose 1.1's timestamp option.
- **Real-broadcast soak:** the hardened argv on a real OTA channel via the
  household HDHomeRun, for one hour with one tuner, off the production server.
  The local test server runs with `BOWTIE_HDHOMERUN_GUIDE=off`. Require zero
  false re-bases and Σ EXTINF ≈ wall time across ad splices.
- **Restart discriminator:** after Part 1, force scenario `long-outage-restart`
  and watch a real AVPlayer (iOS Simulator) and Chrome/hls.js. If the jump is
  already gone, Part 2 is still built (stalls without restarts still need it),
  but the result is recorded.
- **Clients:**
  - unit tests of the seek-target function on each platform;
  - an iOS Simulator UI test against the fake HDHR with `long-outage-restart`:
    the position never lands at the window start or the live end;
  - Playwright for the web;
  - Roku on the bedroom Roku (192.168.50.6) only.
- **Resume:** server API tests (offer only when the footage exists; guide and
  no-guide keys; expiry) and client tests for the offer UI.

## Rollout
- **Release 1 (0.24.0):** Part 1 (server, Docker update). The release notes say
  what to watch for on QSV: any "timestamp discontinuity" lines around ad
  breaks, and whether "transcoder stuck" still appears.
- **Release 2 (0.25.0):** Parts 2 and 3 (apps, server endpoints for resume).
  The web player and the resume endpoints ship in the image.
