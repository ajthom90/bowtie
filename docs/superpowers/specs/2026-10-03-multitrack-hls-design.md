# Multi-track HLS: captions and alternate audio — Design

Date: 2026-10-03. Requested: closed captions and second-language / described
audio in every app ("do them all" roadmap, Track B). This spec also lays the
foundation for adaptive bitrate (ABR), which is out of scope here.

## Goal

A viewer can turn on closed captions and pick another audio track (e.g. the
Spanish track FOX 9 broadcasts) in the web, iOS/iPadOS, Apple TV, Android,
Fire TV and Roku apps. Captions work on every encoder Bowtie uses, including
production's QSV.

## What the broadcast has (measured on a 9.1 FOX 9 capture)

- Video: MPEG-2 720p. Audio: AC-3 5.1 `eng` and AC-3 stereo `spa`.
- CEA-608 captions in the MPEG-2 user data (FFmpeg's `[out0+subcc]` extracts
  them).

## Constraints found in the spike (2026-10-03, image FFmpeg 5.1.9)

1. **In-band captions can't be relied on.** `h264_qsv`, `h264_nvenc` and
   libx264 in FFmpeg 5.1 have `-a53cc` (default on), but whether 608 data
   survives the `mpeg2_qsv` hardware decode is unknown and only testable on
   the production box; VideoToolbox's `a53cc` corrupts the stream (disabled
   in 0.5.4); VAAPI has none. So captions are a separate **WebVTT**
   rendition for every encoder, and `-a53cc 0` is set whenever that
   rendition exists so players don't list captions twice.
2. **Players can't switch audio tracks muxed into one stream**, so each
   extra audio track must be its own HLS audio rendition.
3. **One FFmpeg 5.1 process can do it.** Verified two ways:
   - all-lavfi: `-f lavfi -i "movie='pipe\:0':s=dv+da+da[out0+subcc][out1][out2]"`
     produced the renditions, but AVPlayer never became ready on `main`
     (audio muxed via the lavfi path stalls it — bisected: the same master
     became ready once English audio came from its own playlist);
   - **two inputs (chosen, §1)**: `-i pipe:0` plus
     `-f lavfi -i "movie='pipe\:3'[out0+subcc]"` with `-map 1:s` and
     `-var_stream_map "v:0,a:0,s:0,agroup:aud,sgroup:subs,name:main a:1,agroup:aud,name:spa"`.
     AVPlayer: status ready, audible ["English", "Spanish"], legible
     ["English"], on a Bowtie-written master.
4. **FFmpeg's own master playlist is wrong for us** (makes Spanish the
   default audio, adds an audio-only variant). AVPlayer, given a Bowtie-
   written master, lists audio "English"/"Spanish" and legible "English".

Both features therefore need Bowtie to serve an HLS **master playlist** with
audio and subtitle renditions instead of today's single media playlist.

## Design

### 1. Inputs: keep the video pipeline, add a caption tap
The video/audio pipeline stays as today (`-i pipe:0` with the backend's
hwaccel decode — QSV, VAAPI, NVENC, VideoToolbox, software). Captions come
from a **second input on fd 3**: Bowtie attaches a second ingest subscriber
for the same channel and passes it to FFmpeg via `exec.Cmd.ExtraFiles`;
FFmpeg reads it with `-f lavfi -i "movie='pipe\:3'[out0+subcc]"` and maps
only the `subcc` stream. Both subscribers receive identical bytes from the
fan-out, so caption and video timestamps share one origin.

Rejected: a single lavfi input for everything (loses hardware decode,
changes the QSV filter chain, and its muxed audio stalls AVPlayer — see
constraint 3).

### 2. Which tracks exist
Ingest already parses PAT/PMT for the join buffer. Extend the PMT parse to
record audio elementary streams (AC-3 / MPEG audio / AAC stream types) in
PID order with their ISO 639 language descriptor. A session starts FFmpeg
with one audio rendition per audio stream (cap: 3). Captions: always
included for ATSC sources (608 is near-universal; an empty rendition costs
nothing). Language names come from a small ISO 639 table (eng → English,
spa → Español, fra → Français; unknown → the code). A track with the
"visual impaired" audio type in its descriptor is labeled "Described video".

### 3. FFmpeg output
- `-var_stream_map` with variant `main` = video + first audio; each other
  audio stream as an audio-only rendition in group `aud`; the caption stream
  as subtitle group `subs` (`-c:s webvtt`).
- Extra audio is transcoded to AAC stereo (hls.js/Chrome can't play AC-3).
- Variant names `main`, `aud1`, `aud2`. FFmpeg derives the files from them:
  playlists `main.m3u8`, `aud1.m3u8`, `main_vtt.m3u8` (captions ride the
  `main` variant), segments `main_%05d.ts`, `aud1_%05d.ts`, `main%d.vtt`.
- `-master_pl_name` is not used; Bowtie writes the master (below).
- Restarts keep `append_list+discont_start+omit_endlist` (0.6.0), now per
  rendition — **must be verified** with `var_stream_map` (risk 3).

### 4. Bowtie-authored master playlist
`GET …/index.m3u8` (the existing `playlistUrl`) returns a master:
```
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Español",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,URI="aud1.m3u8?token=…"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="main_vtt.m3u8?token=…"
#EXT-X-STREAM-INF:BANDWIDTH=…,RESOLUTION=…,CODECS="…",AUDIO="aud",SUBTITLES="subs"
main.m3u8?token=…
```
Each media playlist is served through today's rewrite (token-signed segment
URLs, DVR window, `Touch` on fetch); the rewrite generalizes from
`live.m3u8` to any rendition playlist in the session dir. FFmpeg 5.1 writes
WebVTT segments without `X-TIMESTAMP-MAP`; cue times start at 0 while the
video's first PTS is 1.4 s (the mpegts muxer's start offset; measured
1.421 s vs first cue 0.022 s). When serving a `.vtt`, Bowtie inserts
`X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000` after the `WEBVTT` line.
Each FFmpeg restart begins a new discontinuity with the same offsets, so the
constant holds across restarts. On-screen sync is checked in the plan. Clients that can't use renditions keep working: they play `main`
with its muxed primary audio.

### 5. Apps
| App | Audio picker | Captions |
|---|---|---|
| Web (hls.js) | menu from `hls.audioTracks` | CC button → `hls.subtitleTrack` |
| iOS / iPadOS | `AVMediaSelectionGroup` (audible) in the player chrome | CC toggle (legible group) |
| Apple TV | system info panel (AVPlayerViewController) | system panel |
| Android / Fire TV | Media3 track selection (audio, text) | CC toggle |
| Roku | `availableAudioTracks` / `audioTrack` | `globalCaptionMode` / caption toggle |

The last choice (audio language, captions on/off) is remembered per device.

## Testing
- Unit: PMT audio-stream/language parse; master playlist builder; WebVTT
  header injection; FFmpeg args for 1–3 audio renditions + captions.
- Harness: extend the synthetic source generator with a second audio track
  and run the restart scenarios against the multi-rendition output
  (sequence monotonic per rendition).
- Real: the 9.1 capture and a short live 9.1 session — AVPlayer (iOS
  Simulator UI test switches to Español and enables CC), Chrome (hls.js
  lists tracks, captions render), Android emulator if available. QSV:
  user-run check on the TrueNAS box after release (I can't run QSV).

## Risks (resolved in the plan's first tasks)
1. **Live (not VOD) behavior** of the two-input output is only verified on
   a 20 s clip with `#EXT-X-ENDLIST`; the plan's first task runs it live on 9.1.
2. **Caption quality and sync**: mid-stream extraction produced control-code
   garbage in the first cue ("atH@Fox News Update"); needs cc_dec options
   (e.g. `real_time`, `data_field`) tested on a longer capture. Sync of the
   126000 constant is unconfirmed on screen.
3. **Restart continuity per rendition**: `append_list` with `var_stream_map`
   is unverified.
4. **CPU**: the caption tap decodes MPEG-2 in software (cheap; one extra
   decode per session) plus AAC for each extra audio track.
5. **Channels with no 608 data** get a caption playlist that never gains
   segments. Check what AVPlayer and hls.js do when CC is selected there;
   if either stalls, Bowtie omits the SUBTITLES rendition until the first
   cue file exists (the master is rebuilt on each fetch).

## Out of scope
ABR ladder (this master playlist is its foundation), CEA-708, burned-in
captions, DVR.
