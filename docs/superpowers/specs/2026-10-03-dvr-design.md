# DVR: scheduled recordings — Design (DRAFT — awaiting decisions)

Date: 2026-10-03. Requested: record from the guide (one-off, later series),
store on the NAS, list/play/delete in every app, with retention limits.

## Goal

A household member picks **Record** on a guide program. Bowtie records it
to disk, and it then appears under **Recordings** in the web, iOS/iPadOS,
Apple TV, Android, Fire TV and Roku apps. From there it plays with full
seeking and resume, or can be deleted. Recordings never interrupt live TV
without warning: Bowtie can't control Plex, and it cuts off a Bowtie viewer
only if the admin opts in, and then only after a notice. A recording that
can't get a tuner says so ("Missed: no tuner free").

## What exists today

- **Tuners.** Production is an HDHomeRun CONNECT DUO (2 tuners). Plex on
  the same TrueNAS box often holds one. `store.Device.TunerCount` stores the
  count; `tuner.Manager.Devices()` (`server/internal/tuner/manager.go`)
  reads live `status.json`. `StreamURL` builds `…:5004/auto/v<guide>`, which
  takes any free tuner. The device has no way to preempt a stream.
- **Ingest** (`stream/ingest.go`). There is one device dial per channel,
  fanned out to `IngestSub`s, each starting with a PAT/PMT join buffer.
  805, or a 503 with no reason, → `ErrTunersBusy`. An 805 on reconnect
  closes all subs. Each sub has a 16 MB drop-oldest queue and is closed
  after 30 s with no reads.
- **Sessions** (`stream/manager.go`, `transcode/ffmpeg.go`). FFmpeg writes
  HLS to `SegmentDir`, a 4 GB **tmpfs** in production
  (`deploy/docker-compose.yml`, `docs/deploy/truenas.md`). `Terminate`
  frees the tuner after the 5 s ingest tail.
- **Busy** (`api/stream_handlers.go`). The 503 response carries
  `{error, sessions, otherInUse}`. `otherTunersInUse()` = device-busy tuners
  − Bowtie-ingested channels, which is how Plex is detected.
- **EPG** (`epg/service.go`, `store/epg.go`). `programs` rows are deleted
  and re-inserted on every refresh, so their ids are unstable. SD
  `programID` (`epg/sd/client.go`) and XMLTV `episode-num`/`<new/>` are
  dropped.
- **Store and API.** SQLite with `store/migrations/000N_*.sql` (latest
  0002). DB-backed settings (`settings/settings.go`). Routes are in
  `api/server.go`; `docs/api/openapi.yaml` is enforced by `openapi_test.go`.

## Design

### 1. Scheduler (`internal/dvr/scheduler.go`)
A 5 s loop with an injectable clock (the same pattern as
`stream.Manager.maintain`). It starts the recorder at `start − padStart`
and stops it at `stop + padEnd`.
- **Sources:** a guide program (`channelId` + `programStart`; the server
  snapshots the metadata), or **manual** channel + start + stop (for
  EPG-less channels). Series rules come in phase 3.
- **EPG drift:** after each refresh, guide-sourced `scheduled` rows are
  re-matched by `program_id`, else by title + start within ±2 h, and their
  times updated. If nothing matches, the old times are kept and flagged.
- **Padding:** default 1 min early and 3 min late (`dvr.padStartSec`,
  `dvr.padEndSec`), editable per recording. In-progress recordings have
  **Extend +30 min**. Padding is soft: when the only overlap with another
  recording is padding, the scheduler trims it instead of reporting a
  conflict.

### 2. Recorder (`internal/dvr/recorder.go`)
The recorder takes **raw MPEG-TS from the ingest fan-out**: `Attach` →
copy `sub.R` to a file. There is no FFmpeg at record time.
- **Free sharing:** a recording on a channel someone is watching (or
  another recording uses) shares the device stream. Live viewers are
  unaffected.
- **Disk stalls:** the recorder drains the sub promptly into its own 64 MB
  ring and writes it to disk from another goroutine, so ZFS stalls don't
  trip the queue drop or the stuck rule. Drops are counted.
- **Gaps:** if the sub closes mid-recording (device lost, tuner taken), the
  recorder closes `part-001.ts`, re-acquires (§4), continues in
  `part-002.ts`, and adds to `gapSec`.
- **Restarts:** on SIGTERM it fsyncs and closes the part, and the row stays
  `recording`. At startup, rows still inside their window resume with a new
  part; rows whose window has passed are finalized (`partial` if short).
- **Disk space:** before starting it runs the retention sweep if free space
  is below `dvr.minFreeGB`. If space is still short, it fails with
  `diskFull`.

### 3. Storage, format, playback, retention
- **Location:** new infra config `recordingsDir` (`BOWTIE_RECORDINGS_DIR`,
  default `<DataDir>/recordings`). Compose and the TrueNAS doc mount a
  dedicated dataset at `/recordings`; never the tmpfs. Each recording gets
  `<id>-<slug>/` containing `part-NNN.ts`, `meta.json` (a sidecar for DB
  loss), `hls/index.m3u8`, `hls/seg_NNNNN.ts` and `thumb.jpg`.
- **Raw TS is the capture format:** zero CPU and lossless; about 6–8 GB/h
  for HD and 2–3 GB/h for SD subchannels.
- **Conversion after recording:** a queue (concurrency 1, `nice`)
  transcodes the TS to H.264/AAC **HLS VOD** using the configured encoder
  (QSV in production) and the live filters (`videoFilter`,
  `encoderExtras`). Arguments: `-f concat` over the parts,
  `-hls_playlist_type vod -hls_time 6`. The profile is `dvr.quality`
  (default `high`: 720p at 4 Mb/s, about 1.9 GB/h). The TS is deleted after
  success unless `dvr.keepOriginal` is set. Once multitrack lands,
  conversion reuses its argument builder, so recordings get captions and
  alternate audio.
- **Why not transcode on each playback:** static segments give every
  client native VOD seeking and resume, with no FFmpeg per viewer and
  nothing on the tmpfs. Plain remuxing doesn't work: AVPlayer and hls.js
  can't play MPEG-2 in HLS.
- **Retention:** a sweep runs hourly and before each start. Rules:
  `dvr.maxGB` (0 = none), `dvr.minFreeGB` (default 50),
  `dvr.deleteAfterDays` (0 = never), and a rule's `keepLatest`. The oldest
  unprotected `ready` recording goes first; `protected` recordings are
  never deleted automatically.

### 4. Tuner conflict policy
Conflicts are counted **per device**: overlapping recordings on *distinct*
channels versus `TunerCount`. Two recordings on the same channel cost one
tuner.

**At schedule time:**
- **More distinct channels than tuners:** **409** listing the conflicting
  recordings. "Record anyway" (`force`) sets a lower `priority`. When
  tuners are short, the earlier-created recording wins.
- **Uses the last free tuner** (on a DUO, any second overlapping channel):
  201 with the warning `usesAllTuners`: "If Plex or someone watching live
  TV is using a tuner then, this may not record."
- **Phase 3 forecast:** sample `otherTunersInUse()` every minute into a
  4-week weekday×hour histogram, so the warning can say "another app
  usually uses a tuner Tue 8 PM".

**At record time:**
1. `Attach` succeeds, or shares an existing ingest → `recording`.
2. `ErrTunersBusy` → `waiting`; retry every 15 s **for the whole window**.
   If a tuner frees later (someone changed channel, Plex stopped), recording
   starts late: `partial` with `missedSec`.
3. **Plex is never touched**; Bowtie cannot. Bowtie live viewers follow
   `dvr.conflictPolicy`:
   - `notify` (**default**). At T−2 min, if the device has no free tuner
     and the channel isn't already ingested, every Bowtie viewer on that
     device gets a notice: "'Jeopardy!' on 5.1 records at 7:00 and needs a
     tuner. [Watch 5.1] [Dismiss]". Watch shares the tuner and resolves the
     conflict. Nobody is cut off.
   - `preempt`. The notice says "Live TV on 9.1 stops at 7:00". At start,
     if the device is still full and Bowtie holds one of its tuners, Bowtie
     `Terminate`s its live session with the fewest viewers. A recording
     never preempts another recording.
4. **Never gets a tuner:** `failed`/`noTuner` with
   `failureDetail {otherInUse:1, bowtieChannels:["9.1 FOX 9"]}`. The app
   shows "Missed: no tuner free (another app used 1, live TV on 9.1
   used 1)".
- **Reverse case:** a live Start that 503s because recordings hold the
  tuners gets `"recordings":[{title,channelId,channelName,endsAt}]` in the
  busy payload ("Recording 'Jeopardy!' on 5.1 until 7:33"). Watching 5.1
  still works because it shares the tuner. Only users who can delete a
  recording can stop it.
- **Notice delivery:** updated clients send their heartbeat with
  `?notices=1` and get 200 `{"notices":[…]}` back (204 if none). Old
  clients, which expect 204 (`web/src/api/client.ts`), are unchanged.

### 5. Data model: `0003_dvr.sql`
```sql
ALTER TABLE programs ADD COLUMN program_id TEXT NOT NULL DEFAULT ''; -- SD programID / XMLTV dd_progid
ALTER TABLE programs ADD COLUMN series_id  TEXT NOT NULL DEFAULT ''; -- SD "SH"+programID[2:10]
ALTER TABLE programs ADD COLUMN is_new     INTEGER NOT NULL DEFAULT 0;
CREATE TABLE recording_rules (id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL,
  title TEXT NOT NULL, series_id TEXT NOT NULL DEFAULT '', channel_id INTEGER, -- NULL = any
  new_only INTEGER NOT NULL DEFAULT 1, keep_latest INTEGER NOT NULL DEFAULT 0,
  pad_start_sec INTEGER NOT NULL, pad_end_sec INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL);       -- used from phase 3
CREATE TABLE recordings (id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id INTEGER REFERENCES recording_rules(id) ON DELETE SET NULL, user_id INTEGER NOT NULL,
  channel_id INTEGER NOT NULL, channel_name TEXT NOT NULL,             -- snapshots, not FKs
  title TEXT NOT NULL, subtitle, description, category, icon_url,       -- TEXT NOT NULL DEFAULT ''
  program_id TEXT NOT NULL DEFAULT '', series_id TEXT NOT NULL DEFAULT '',
  start TEXT NOT NULL, stop TEXT NOT NULL, pad_start_sec INTEGER NOT NULL, pad_end_sec INTEGER NOT NULL,
  priority INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL,  -- scheduled|waiting|recording|converting|ready|failed|cancelled
  partial INTEGER NOT NULL DEFAULT 0, failure TEXT NOT NULL DEFAULT '', -- noTuner|noSignal|diskFull|error
  failure_detail TEXT NOT NULL DEFAULT '', actual_start TEXT, actual_stop TEXT,
  missed_sec INTEGER NOT NULL DEFAULT 0, dir TEXT NOT NULL DEFAULT '', size_bytes INTEGER NOT NULL DEFAULT 0,
  duration_sec INTEGER NOT NULL DEFAULT 0, protected INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE INDEX idx_recordings_state_start ON recordings (state, start);
CREATE TABLE recording_positions (recording_id INTEGER NOT NULL REFERENCES recordings(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL, position_sec INTEGER NOT NULL, updated_at TEXT NOT NULL,
  PRIMARY KEY (recording_id, user_id));
```
The SD and XMLTV parsers fill `program_id`, `series_id` and `is_new`.
Recordings never reference `programs.id`.

### 6. API (`/api/v1`, added to `openapi.yaml`)
- `GET /recordings?state=upcoming|recorded|failed` →
  `[{"id":7,"title":"Jeopardy!","subtitle":"…","channelId":3,"channelName":"5.1 KSTP","start":"…","stop":"…","state":"ready","partial":false,"failure":"","failureDetail":null,"durationSec":1980,"sizeBytes":3800000000,"protected":false,"positionSec":412,"thumbUrl":"…","scheduledBy":"andrew"}]`
- `POST /recordings` with `{"channelId":3,"programStart":"2026-10-05T00:00:00Z"}`,
  or `{"channelId":3,"start":"…","stop":"…","title":"Manual"}`, plus an
  optional `"force":true`. Returns 201
  `{"recording":{…},"warnings":[{"code":"usesAllTuners","message":"…"}]}`
  or 409 `{"error":"tuner conflict","tunerCount":2,"conflicts":[…]}`.
- `PATCH /recordings/{id}` with `{padStartSec,padEndSec,protected,extendSec}`.
  `POST /recordings/{id}/stop` stops and keeps what was recorded.
  `DELETE /recordings/{id}` cancels a scheduled recording or deletes the
  files.
- `POST /recordings/{id}/play` →
  `{"playlistUrl":"/api/v1/recordings/7/hls/index.m3u8?token=…","positionSec":412}`.
  The token is signed for `rec:<id>:<userId>`. `GET …/hls/{file}` checks
  the name against `^(index\.m3u8|seg_\d{5}\.ts)$` and rewrites the
  playlist with the token, as live does. `PUT /recordings/{id}/position`
  with `{"positionSec":…}`.
- `GET /guide`: `GuideProgram` gains `"recording":{"id":7,"state":"scheduled"}`.
- `GET /admin/dvr` →
  `{"dir","usedBytes","freeBytes","queue":[…],"active":[…]}`.
  `/admin/tuners` lists recordings. New settings: `dvr.*`.
- **Roles:** any user can schedule on enabled channels, see all recordings,
  and stop or delete their own. Admins can do everything.

### 7. Apps
| App | Schedule | Recordings | Playback |
|---|---|---|---|
| Web | Guide popover Record/Cancel, red dot, 409 dialog | Upcoming / Recorded / Missed tabs | hls.js VOD + resume |
| iOS / iPadOS | Guide context menu | Recordings tab | AVPlayerViewController scrubbing |
| Apple TV | Long-press in guide | Recordings row | same; top shelf later |
| Android / Fire TV | Long-press / menu key | Recordings screen | Media3 VOD + resume |
| Roku | `*` options on guide item | Recordings screen | Video node VOD (no BIF) |

Every app shows the tuner notice as a player banner with **Watch
<channel>**, and lists missed recordings with their reason. Admin web gets
DVR settings and a storage gauge.

## Testing
- **Unit (fake clock):** scheduler states; padding trim; EPG re-match;
  per-device distinct-channel conflict math (a same-channel overlap is
  free); retention order; restart reconciliation; migration on a 0002 DB;
  conversion args (golden); SD/XMLTV id parsing.
- **e2e** (`internal/e2e` + `hdhrfake`, `TunerCount: 2`):
  (a) a recording plus a viewer on the same channel → the fake logs 1
  dial;
  (b) **"Plex"**: a test client dials `/auto/v<ch>` on the fake directly
  and holds a tuner, while a Bowtie viewer is on a third channel →
  `waiting`, then `failed noTuner` with `otherInUse:1`. If the viewer
  leaves mid-window → `partial` with `missedSec>0`;
  (c) `notify` never terminates and `preempt` does, after the notice;
  (d) a `drop` fault → part 2 and a gap; `busy` on reconnect → re-acquire
  loop;
  (e) restart: rebuild the harness on the same store and dir → resumes in
  `part-002`;
  (f) `-tags ffmpeg`: convert, then `testplayer` plays the VOD (ENDLIST,
  duration ≈ window ± 6 s).
  Scenario YAML gains `bowtie.record: {channel, at, for}` and the
  expectations `recordingState` and `minRecordedSeconds`.
- **Real:** a 2-minute recording of 9.1 on the Mac against the DUO (one
  tuner, short). The user checks QSV conversion speed on TrueNAS.

## Risks
1. **2 tuners shared with Plex:** misses will be common. The policy makes
   them visible, and the phase-3 forecast explains them. Bowtie recordings
   can also make *Plex's* DVR miss.
2. **ZFS stalls longer than the 64 MB ring** (~30 s at HD rates) cause
   gaps. Use a dedicated dataset and show drops in admin.
3. **Without QSV,** libx264 (~1–2× realtime) lets the queue lag behind
   back-to-back recordings, and the TS doubles disk use until conversion
   finishes.
4. **A single 4 Mb/s rendition** may be too much for remote users with
   `maxQuality: low`. A lower second rendition (ABR) comes later.
5. **EPG times are wrong for overrunning sports.** Padding and Extend only
   partly help.
6. **`concat` across parts with PCR/PTS discontinuities** is unverified on
   FFmpeg 5.1. The plan's first task forces a reconnect and checks it.

## Out of scope
Commercial skip; "record what I'm watching" from the live rewind buffer
(those segments are viewer-profile H.264 on tmpfs, so it isn't trivial);
reading Plex's DVR schedule; mobile downloads; ABR; rebalancing tuners
across several HDHomeRuns.

## Phasing
- **Phase 1 (MVP: server + web):** one-off guide and manual recordings, TS
  capture, VOD conversion, the `notify` policy and 409 check, retention,
  restart reconciliation, web UI, and e2e scenarios.
- **Phase 2:** native apps (iOS/iPadOS, tvOS, Android/Fire TV, Roku):
  scheduling, Recordings screens, notices, and resume.
- **Phase 3:** series rules (new-only, keep latest N, de-dupe by
  `program_id`), the tuner forecast, the `preempt` option in the UI, chase
  play (convert while recording via FFmpeg `file` `follow=1`, to be
  verified), and thumbnails/top shelf.

## Decisions for the user
1. **Tuner conflicts with Plex and live TV.** *Recommend:* never touch
   Plex. By default never cut off Bowtie live TV: warn viewers 2 minutes
   ahead with one-tap "watch that channel", retry for the whole window, and
   end as Partial or "Missed: no tuner" saying who held the tuners. Admins
   can opt in to `preempt`.
2. **Format.** *Recommend:* raw TS capture, then background conversion to
   720p H.264 HLS VOD, then delete the TS (about 1.9 GB/h kept). The
   alternative, keeping TS and transcoding on each playback, means more
   disk, worse seeking, and FFmpeg per viewer.
3. **Who can record.** *Recommend:* every user can schedule and see all
   recordings and delete their own. Admins manage everything, including
   series rules.
4. **Storage and retention.** *Recommend:* a dedicated TrueNAS dataset at
   `/recordings`; no age limit; keep at least 50 GB free by deleting the
   oldest unprotected recordings first; a per-recording "Keep" flag.
5. **MVP scope.** *Recommend:* server and web with one-off and manual
   recordings first. Native apps in Phase 2, series in Phase 3.
