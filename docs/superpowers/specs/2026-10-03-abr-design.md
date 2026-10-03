# Adaptive bitrate (ABR) ladder — Design (DRAFT — awaiting decisions)

Date: 2026-10-03. Builds on `2026-10-03-multitrack-hls-design.md` (Bowtie-authored
master playlist, renditions, generalized playlist rewrite). Ship after it.

## Goal
A remote viewer (phone on cellular, over Tailscale) plays smoothly without
choosing a quality: the master playlist offers a ladder and the player switches
rungs as bandwidth changes. Production QSV must be able to afford it.

## What exists today
- `server/internal/transcode/profile.go`: `DefaultProfiles()` original 1080p/8000k,
  high 720p/4000k, medium 720p/2500k, low 480p/1500k. `Negotiate`/`pickProfile`
  pick ONE rung from `caps.Profile` ("" = original), clamped by the user's
  `MaxQuality` (admin per-user cap) and `caps.MaxHeight`. `SessionKey` =
  `ch|codec|profile|audio`.
- `server/internal/stream/manager.go`: viewers with the same key share one
  FFmpeg; different profiles on one channel = separate FFmpeg processes on the
  same ingest subscriber fan-out (one tuner). `waitPlaylist` polls `live.m3u8`;
  restarts use `Append` (`append_list+discont_start`).
- `server/internal/transcode/ffmpeg.go`: one `-vf`, one encoder, `-g 120`,
  `-force_key_frames expr:gte(t,n_forced*4)`, `seg%05d.ts` + `live.m3u8`. QSV
  filter comments: FFmpeg 5.1 `vpp_qsv` rejects `w=-1` and named `scale_mode`,
  hence `w=trunc(iw*H/ih/2)*2`. No source-height cap: a 720p source (9.1) on
  "original" is upscaled to 1080p.
- `server/internal/api/stream_handlers.go`: `rewritePlaylist` + `segmentNameRe
  ^seg\d{5}\.ts$` (multitrack generalizes both).
- `server/internal/settings/settings.go`: `Streaming{BufferMinutes}`,
  `Transcode{Encoder, AllowHEVC}`. `web/src/admin/settingsModel.ts:235` warns
  "~60 MB per minute per session at top profile" for tmpfs sizing.
- Manual quality picker ALREADY exists: web (`web/src/player/Player.tsx`
  `QUALITY_OPTIONS`), iOS (`ios/App/iOS/PlayerView.swift` `qualityMenu`), tvOS
  (`ios/App/tvOS/TVPlayerView.swift` info panel), Android/Fire TV
  (`android/.../ui/PlayerScreen.kt` quality sheet). Roku sends `profile: ""`
  only (`roku/source/lib/Caps.bs`). A change re-creates the session (playback
  restarts). "Auto" = "" = original = 1080p/8 Mbps — the worst choice on cellular.
- The 2026-08-04 server design deferred ABR "(GPU cost)".

## Evidence (2026-10-03 spike)
- Verified locally (FFmpeg 8.0.1, libx264, `yadif,split=3,scale` → 720/480/360,
  `-var_stream_map "v:0,a:0,name:v720 v:1,a:1,name:v480 v:2,a:2,name:v360"`,
  `-hls_segment_filename %v_%05d.ts`, playlist `%v.m3u8`): files `v720.m3u8`,
  `v720_00000.ts`…; identical EXTINF lists across rungs; segment N of every rung
  starts with a keyframe at the same PTS (5.437, 13.445); a second run with
  `+append_list+discont_start` continued each variant at `_00005` after
  `#EXT-X-DISCONTINUITY`.
- Verified in the production image (`ghcr.io/ajthom90/bowtie:0.7.0`, amd64,
  FFmpeg 5.1.9, help text only): `split`, `vpp_qsv` (w/h are expressions),
  `scale_qsv` (w/h expressions, `mode`), `-extra_hw_frames`, `-var_stream_map`
  all exist. `h264_qsv` has `-forced_idr` (default **false**) and `-profile`
  (default **unknown**).
- Unverified (needs the TrueNAS iGPU): `split` of QSV hw frames into several
  `vpp_qsv` + `h264_qsv` instances, hw frame-pool sizing, encoder throughput.

## Approaches
**A. Manual picker only (no ladder).** Make "Auto" sensible (client picks
"medium" on cellular/expensive networks), never upscale past source height,
add a Roku picker. Cheapest, no FFmpeg graph change. But: no mid-stream
adaptation (a Tailscale hiccup still stalls), each switch restarts playback,
and every distinct quality on a channel is another FFmpeg process.

**B. One shared ladder session per channel (recommended).** One FFmpeg: decode
once, deinterlace once, `split`, N scales + N encodes, `var_stream_map`. All
viewers of a channel (same codec/audio mode) share it; per-user caps and the
picker become per-viewer filters on the Bowtie-authored master. Players adapt
natively (AVPlayer, hls.js, Media3, Roku). Cost: ~1.7x the encode work of a
single top rung even with one viewer.

**C. Hybrid: ladder only for "Auto" viewers, fixed profile for explicit picks.**
Saves GPU when everyone picks a fixed quality, but a channel can then run a
ladder AND fixed sessions at once (more processes than B), with two code paths.

B wins: it is the only option that adapts mid-stream, and it caps FFmpeg at one
process per channel. Its cost is bounded by an admin toggle (Decision 1).

## Design (Approach B)
### 1. Ladder
Rungs by height: 1080 @ 6000k, 720 @ 3000k, 480 @ 1400k, 360 @ 700k; audio as
today (AAC 128k stereo, or copy). The top rung is the source height capped at 1080
(Decision 3): ingest records the video PID's height from the first MPEG-2
sequence header (`00 00 01 B3`, 12-bit vertical size) next to the PMT parse that
multitrack extends; the ladder is every rung below it. 720p source → 720/480/360;
1080i → 1080/720/480/360; unknown/H.264 source → 720/480/360. The same height
also caps fixed-profile sessions (fixes today's 720→1080 upscale).
Software backend (libx264): ABR not offered — sessions stay fixed-profile.

### 2. FFmpeg graph per backend (N rungs; K = rung index; first rung = top)
Inputs unchanged (`inputHWAccel` + `pipe:0`; multitrack's fd-3 caption input).
`-vf` becomes `-filter_complex`; top rung skips its scaler when it equals source.
- QSV (proposed; first plan task is a user-run spike on TrueNAS):
  `[0:v]vpp_qsv=deinterlace=2,split=N[s0]..;[sK]vpp_qsv=w=trunc(iw*H/ih/2)*2:h=H[vK]`
  (5.1 constraints carried: explicit even width, no `scale_mode`). Encoder adds
  `-forced_idr 1 -profile:v high`. If surfaces run out: `-extra_hw_frames`;
  if per-rung `vpp_qsv` is heavy: try `scale_qsv=w=…:h=H`.
- VAAPI: `[0:v]deinterlace_vaapi=rate=frame,split=N…;[sK]scale_vaapi=w=-2:h=H[vK]`.
- NVENC: `[0:v]yadif_cuda=0:-1:0,split=N…;[sK]scale_cuda=-2:H[vK]`; `-forced-idr 1`.
  Each rung is an NVENC session (GeForce cards cap concurrent sessions).
- VideoToolbox (dev Mac): `[0:v]yadif=0:-1:0,split=N…;[sK]scale=-2:H[vK]`.
Per rung: `-map [vK] -map 0:a:0` and `-b:v:K/-maxrate:v:K/-bufsize:v:K`
(1.2x/2x as today); shared `-g 120 -force_key_frames expr:gte(t,n_forced*4)`.
`split` keeps frame PTS, so forced keyframes land on identical timestamps in
every rung (verified in software). Primary audio is muxed into every rung (N AAC
encodes are cheap; `copy` is free), so rung switches never need an audio switch.

### 3. Output naming (composes with multitrack)
Variant names = `v<height>`; extra audio stays `aud1`/`aud2`; captions ride the
top rung only. Example (720p source, Spanish track, captions):
`-var_stream_map "v:0,a:0,s:0,agroup:aud,sgroup:subs,name:v720 v:1,a:1,agroup:aud,name:v480 v:2,a:2,agroup:aud,name:v360 a:3,agroup:aud,name:aud1"`
→ `v720.m3u8`, `v720_%05d.ts`, `v720_vtt.m3u8`, `aud1.m3u8`. Segment regex:
`^(v\d{3,4}|main|aud\d)_\d{5}\.ts$` (+ multitrack's `.vtt` rule). `waitPlaylist`
waits for the lowest rung's playlist. Fixed-profile sessions keep `main`.

### 4. Master playlist and per-viewer filtering
Multitrack's builder emits one `#EXT-X-STREAM-INF` per rung, all with
`AUDIO="aud",SUBTITLES="subs"`, plus `#EXT-X-INDEPENDENT-SEGMENTS`:
`BANDWIDTH` = (maxrate + audio) × 1.1 (TS overhead), `AVERAGE-BANDWIDTH` =
b:v + audio, `RESOLUTION` from source aspect (same width formula as the filter),
`CODECS` from the pinned profile/level (e.g. `avc1.640020,mp4a.40.2`; `ac-3`
when copying). AVPlayer starts on the FIRST listed variant, so the list starts
at 720p (or the top rung if lower), then the rest in descending order.
Each viewer gets a ceiling height = min(user `MaxQuality`, `caps.MaxHeight`,
picked profile) where original→1080, high/medium→720, low→480. The master
omits rungs above the ceiling (360 is always kept). The master is rebuilt on
every fetch, so this costs nothing; `Viewer` gains a `MaxHeight` field.

### 5. Session sharing
When ABR applies, `SessionKey` = `ch|codec|abr|audio`: the profile no longer
splits sessions, so a channel has one ladder FFmpeg regardless of picker choices.
A quality change still re-creates the viewer (as today) but joins the running
session — no new FFmpeg, near-instant restart. Restart/`Append` is unchanged
(per-variant `append_list` verified on FFmpeg 8, to be verified on 5.1).

### 6. Settings
`streaming.adaptive` (bool) in `settings.Streaming`; admin UI toggle in
`settingsModel.ts`, "applies to new sessions". Help text: GPU cost (~1.7x one
top-rung encode), tmpfs (720p-source ladder ≈ 5.5 Mbps total, less than today's
8 Mbps "original"; 1080 ladder ≈ 11.6 Mbps ≈ 1.3 GB per 15-min buffer).
Default off for the first release (Decision 1).

### 7. Clients
No playback code changes needed: all four players adapt from a master. Small
UX changes: session response gets `profile: "adaptive"` and `rungs`; stats
overlays show the live rung (hls.js `levels[currentLevel]`, AVPlayer access
log `indicatedBitrate`/`presentationSize`, Media3 `videoFormat`); admin Sessions
shows "adaptive (3 rungs)". hls.js: keep `startLevel: -1`. Pickers keep their
labels; under ABR they mean "at most" (Decision 2). Roku: nothing.

## Testing
- Unit: ladder from source height (720/1080/unknown); golden argv per backend
  (filter_complex, per-rung bitrates, `forced_idr`, var_stream_map with/without
  multitrack); master builder (BANDWIDTH/CODECS/order, ceiling filter for each
  MaxQuality × maxHeight × pick); `SessionKey` sharing; MPEG-2 sequence-header parse.
- Harness (fake HDHomeRun): run the ladder with software/VT; assert per rung
  identical EXTINF lists and media sequences, first packet of segment N is a
  keyframe at the same PTS across rungs (ffprobe), restart continuity per rung.
- Real: 9.1 live on the Mac (VideoToolbox) — iOS Simulator with Network Link
  Conditioner (3G profile) shows AVPlayer access-log rung changes without a
  stall; Chrome DevTools throttling shows hls.js level switches; Android emulator.
- QSV: only the user can verify on TrueNAS — script: one ladder session,
  `intel_gpu_top` load, 3 concurrent channel ladders, keyframe alignment check,
  forced restart. Gate for flipping the default (Decision 1).

## Risks
1. FFmpeg 5.1 QSV `split` of hw frames into N `vpp_qsv`/`h264_qsv` may fail or
   exhaust surfaces. Mitigations in §2; last resort is one process per rung
   writing into one dir (Bowtie writes the master), but keyframe alignment
   across processes is not guaranteed (each `t` origin depends on attach time;
   `-copyts` might fix it, unproven).
2. QSV I-frames without `forced_idr` are not IDR; rung switches need IDR
   segment starts. This may also affect today's single-rung QSV output — check.
3. iGPU throughput: N channels × ladder on one iGPU; measure before default-on.
4. Source height unknown (H.264 subchannels, missing sequence header in the
   join buffer) → conservative 720 ladder; wrong for a 1080 H.264 source.
5. Inherits multitrack risks (live behavior, per-rendition restart continuity).

## Out of scope
Demuxed audio-only video rungs, HEVC/AV1 ladders or mixed-codec masters,
fMP4/CMAF, LL-HLS, admin-editable ladder, picker relabeling to heights,
server-side bandwidth detection, software-encoder ladders.

## Decisions for the user
1. **Opt-in admin toggle or automatic?** Recommend: toggle `streaming.adaptive`,
   default off until you verify QSV on TrueNAS, then default on for hardware backends.
2. **Picker under ABR: server-side ceiling or client-side pin?** Recommend: ceiling
   (master filtered per viewer; no client code; covers Roku and per-user caps).
3. **Top rung: fixed 1080 or source height?** Recommend: source height (ingest reads
   the MPEG-2 sequence header; saves GPU on 720p channels and fixes today's upscale).
