# Shared channel stream: quality ladder, alternate audio, 5.1, captions — Design

Date: 2026-10-03. Supersedes `2026-10-03-multitrack-hls-design.md` (captions +
alternate audio, branch `docs/multitrack-hls`) and the draft
`2026-10-03-abr-design.md` (branch `docs/feature-designs`); their findings are
carried here.

## Goal

Everyone watching a channel shares one tuner, one rewind window and one
transcode, whatever their account, device or quality setting. Each viewer
gets the best quality their connection and cap allow (switching automatically),
can turn on closed captions, and can pick another broadcast audio track
(e.g. Español) or the original Dolby 5.1.

## Decisions (user, 2026-10-03)

1. 5.1 surround is offered as an audio choice inside the shared stream
   (not stereo-only, not a separate stream).
2. The multi-quality ladder ships behind an admin switch, **off** until the
   user verifies QSV on the TrueNAS box; then they turn it on.
3. Captions + alternate audio (approved multitrack design) are part of this.

## What exists today

- One tuner per channel already: `stream/ingest.go` fans one device stream out
  to every subscriber of a channel.
- Transcodes are shared only per `transcode.SessionKey` =
  `ch|codec|profile|audio` (`transcode/profile.go`). Two viewers with different
  profiles, codecs (HEVC) or audio modes (AC-3 copy vs AAC) get separate
  FFmpeg processes, and the one started later can rewind only to its own start.
- Profiles: original 1080p/8000k, high 720p/4000k, medium 720p/2500k, low
  480p/1500k; no source-height cap (9.1's 720p is upscaled to 1080 on
  "original"). Per-user cap `users.max_quality`; per-client `caps.maxHeight`.
- Quality pickers exist on web, iOS, tvOS, Android/Fire TV (Roku: none); a
  change re-creates the session.

## Evidence (spikes, 2026-10-03)

- FFmpeg 5.1.9 (image): captions come out of a second input,
  `-f lavfi -i "movie='pipe\:3'[out0+subcc]"` (fd 3 = a second copy of the
  channel TS), as a WebVTT rendition; AVPlayer lists audible
  ["English","Spanish"] and legible ["English"] on a Bowtie-written master and
  becomes ready. An all-lavfi input (no hardware decode) stalls AVPlayer.
- `h264_qsv`/`h264_nvenc`/libx264 have `-a53cc` (default on) but whether 608
  survives the `mpeg2_qsv` decode is unknown; VideoToolbox's is broken; so
  captions are always the WebVTT rendition and `-a53cc 0` is set with it.
- FFmpeg 8.0.1 (Mac), libx264: `split` + per-rung scale + `-var_stream_map`
  gives identical EXTINF lists per rung and keyframes at identical PTS; a
  `+append_list+discont_start` restart continues every rung.
- Image help text: `split`, `vpp_qsv`/`scale_qsv` (expression w/h),
  `-extra_hw_frames`, `-var_stream_map` exist; `h264_qsv` has `-forced_idr`
  (default false) and `-profile`. QSV running the split graph is unverified.

## Design

### 1. One structure, two modes

A session's output is always a **ladder of video rungs + audio renditions +
optional captions**, described by `transcode.Layout`. The admin switch only
changes how many rungs and how sessions are keyed:

| | Switch off (default) | Switch on (`streaming.adaptive`) |
|---|---|---|
| Rungs | 1 — the viewer's negotiated profile (as today) | every ladder rung ≤ source height |
| Session key | `ch|profile` | `ch` |
| Sharing | viewers on the same quality share | every viewer of the channel shares |

In both modes audio and captions are renditions, so AC-3 copy and the
codec no longer split sessions. Software encoding (libx264) never builds a
ladder (one rung). The ladder is H.264 only; when the switch is on the HEVC
setting is ignored (it is off today).

### 2. Inputs

Video/audio from `-i pipe:0` with the backend's hwaccel decode, unchanged.
Captions from a second ingest subscriber of the same channel, passed to FFmpeg
on fd 3 (`exec.Cmd.ExtraFiles`) and read by
`-f lavfi -i "movie='pipe\:3'[out0+subcc]"`. The caption subscriber has no
force-close hook: if it stalls, captions stop and video continues.

### 3. What the broadcast has

Ingest already keeps the latest PAT/PMT. New:
- **Audio tracks**: PMT audio streams (stream types 0x03/0x04/0x0F/0x11/0x81/0x87)
  in PMT order with ISO 639 language and the audio-description flag
  (`audio_type` 0x03). Cap 3 tracks.
- **Source height**: the 12-bit vertical size from the first MPEG-2 sequence
  header (`00 00 01 B3`) on the video PID. Unknown (H.264 subchannel, not seen
  in 3 s) → 720.
A session waits up to 3 s after attaching for the PMT; without one it assumes
one unnamed audio track.

### 4. Layout

- **Rungs** (switch on): 1080 @ 6000k, 720 @ 3000k, 480 @ 1400k, 360 @ 700k,
  those ≤ source height (720p source → 720/480/360; 1080i → all four). Switch
  off: one rung = the negotiated profile, capped at the source height (fixes
  the 720→1080 upscale). Rungs are video-only.
- **Audio, group `aac`**: one AAC stereo rendition per broadcast audio track
  (`eng` → English, `spa` → Español, `fra`/`fre` → Français, described →
  "Described video", else the code; duplicate names numbered).
- **Audio, group `ac3`**: each AC-3 broadcast track copied (no re-encode),
  `CHANNELS` from the stream (6 for 5.1).
- **Captions**: one WebVTT rendition, group `subs`, "English CC", carried with
  the top rung.

### 5. FFmpeg graph

`-filter_complex` decodes and deinterlaces once, `split`s into N, scales each
(top rung skips scaling when it equals the source):
- QSV: `[0:v]vpp_qsv=deinterlace=2,split=N[s0]…;[sK]vpp_qsv=w=trunc(iw*H/ih/2)*2:h=H[vK]`
  (5.1 constraints: explicit even width, no `scale_mode`); encoder adds
  `-forced_idr 1 -profile:v high -level 4.1`; `-extra_hw_frames` if surfaces
  run out (spike).
- VAAPI: `deinterlace_vaapi=rate=frame,split=N…;scale_vaapi=w=-2:h=H`.
- NVENC: `yadif_cuda=0:-1:0,split=N…;scale_cuda=-2:H`, `-forced-idr 1`.
- VideoToolbox / software: `yadif=0:-1:0,split=N…;scale=-2:H`.
Per rung `-b:v:K/-maxrate:v:K/-bufsize:v:K` (1.2×/2× as today); shared
`-g 120 -force_key_frames expr:gte(t,n_forced*4)` (split keeps PTS, so
keyframes align across rungs). Audio: `-c:a:i aac -ac 2` for the AAC group,
`-c:a:j copy` for the AC-3 group. `-c:s webvtt`, `-a53cc 0` when captions.
`-var_stream_map` names: rungs `v<height>`, audio `aac<i>`/`ac3<i>`, captions
ride `v<top>` with `sgroup:subs`. Files: `v720.m3u8`, `v720_%05d.ts`,
`v720_vtt.m3u8`, `v720N.vtt`, `aac0.m3u8`, `ac30.m3u8`. `-master_pl_name` is
not used. Restarts keep `append_list+discont_start+omit_endlist`.

### 6. Master playlist (Bowtie-written, per viewer)

`GET /api/v1/stream/{viewerId}/index.m3u8` (the existing `playlistUrl`)
returns a master built on each fetch:
- `#EXT-X-MEDIA` AUDIO entries for group `aac` (first track DEFAULT=YES) and
  group `ac3`; SUBTITLES "English CC" once `v<top>_vtt.m3u8` exists.
- One `#EXT-X-STREAM-INF` per rung **per audio group**: CODECS
  `avc1.640029,mp4a.40.2` + `AUDIO="aac"`, and `avc1.640029,ac-3` +
  `AUDIO="ac3"`; BANDWIDTH = (maxrate + audio) × 1.1; RESOLUTION; SUBTITLES
  when present; `#EXT-X-INDEPENDENT-SEGMENTS`. Players that can't decode
  AC-3 skip those variants; AVPlayer picks 5.1 on a surround route.
- **Per-viewer ceiling**: rungs above min(user `max_quality`,
  `caps.maxHeight`, picker choice) are omitted (360 always kept). Under the
  ladder, picking a quality re-creates the viewer but joins the running
  session — no new FFmpeg.
- Order: 720 (or the top rung if lower) first, then descending (AVPlayer starts
  on the first variant).
Rendition playlists are served through the existing token rewrite (now any
`*.m3u8` / `*_NNNNN.ts` / `*N.vtt` matching the layout's names) and every
playlist fetch `Touch`es the viewer. WebVTT segments get
`X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000` inserted (mpegts muxer
starts video at 1.4 s; cues start at 0).

### 7. Settings and safety

- `streaming.adaptive` (bool, default false) in Admin → Settings, help text
  with GPU/tmpfs cost (720p-source ladder ≈ 5.5 Mbps total, less than today's
  8 Mbps "original").
- `BOWTIE_MULTITRACK=off` / `disableMultitrack: true` turns off captions and
  extra audio (video + first audio as AAC).
- Fallback: if a session fails before its first playlist with the full
  layout, it retries once with one rung, the first audio track (AAC) and no
  captions; logged.

### 8. Apps

| App | Quality | Audio / 5.1 | Captions |
|---|---|---|---|
| Web (hls.js) | automatic; picker = ceiling | menu from `audioTracks` (AC-3 variants ignored by MSE) | CC button |
| iOS / iPadOS | automatic; picker = ceiling | system audio menu; 5.1 on AirPlay/AVR routes | system subtitles menu |
| Apple TV | automatic; info-panel picker = ceiling | system audio panel, 5.1 to the TV/AVR | system panel |
| Android / Fire TV | automatic (Media3) | Audio chip; AC-3 when the device passes it through | CC chip |
| Roku | automatic | `*` menu (verify on device, with permission) | `*` menu |

Each app remembers the last audio language and captions on/off per device.
Stats overlays show the playing rung.

## Testing

- Unit: PMT audio parse; sequence-header height; ladder from source height;
  golden argv per backend (1 rung / ladder, with and without captions and
  AC-3); master builder (groups, CODECS, order, ceiling filter for each
  `max_quality` × `maxHeight` × pick); session keys in both modes; WebVTT
  timestamp insertion; rendition names accepted/rejected by the handlers.
- Harness (fake HDHomeRun + software/VideoToolbox): per-rung identical EXTINF
  lists and media sequences, keyframe at the same PTS at each segment start,
  restart continuity per rendition, two viewers with different caps share one
  FFmpeg and one window.
- Real (this Mac, 9.1, short runs, one tuner): AVPlayer readiness, audio
  options incl. 5.1, captions sync, rung switching under Network Link
  Conditioner; Chrome/hls.js level switches.
- QSV: a user-run script on TrueNAS (one ladder session, `intel_gpu_top`, 3
  channels at once, keyframe alignment, forced restart) gates turning the
  switch on.

## Risks

1. QSV `split` into N `vpp_qsv`/`h264_qsv` on FFmpeg 5.1 may fail or exhaust
   surfaces (mitigations in §5; else one rung).
2. Demuxed (video-only) rungs with separate audio groups: verify AVPlayer,
   hls.js, Media3 and Roku start and switch cleanly live.
3. Captions: garbled first cues seen in the spike (`cc_dec` options to test);
   sync of the 126000 constant; channels without 608 data.
4. Restart continuity of every rendition with `append_list` on 5.1.
5. Source height unknown → conservative 720 ladder.

## Out of scope

HEVC/AV1 ladders, fMP4/CMAF, LL-HLS, admin-editable ladder, CEA-708,
burned-in captions, DVR, per-user stream/tuner limits (separate design),
SharePlay (separate design).
