# Shared Channel Stream Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every session's output becomes a Bowtie-written HLS master with video rungs, AAC + AC-3 audio renditions and WebVTT captions; with the admin switch on, one ladder transcode per channel serves every viewer.

**Architecture:** `transcode.Layout` describes rungs, audio renditions and captions; `BuildArgs` turns it into one FFmpeg (`-filter_complex` split/scale, `-var_stream_map`, caption tap on fd 3). The stream manager probes the channel (PMT audio, MPEG-2 height), picks the layout and session key per mode, and records each viewer's height ceiling. The API writes a per-viewer master and serves rendition playlists, segments and timestamped WebVTT through the existing token rewrite.

**Tech Stack:** Go server (`server/`), FFmpeg 5.1.9 (image) / 8.x (Mac), hls.js (`web/`), AVKit (`ios/`), Media3 (`android/`), Roku (`roku/`).

**Spec:** `docs/superpowers/specs/2026-10-03-channel-stream-design.md`

## Global Constraints

- Ladder rungs: 1080 @ 6000k, 720 @ 3000k, 480 @ 1400k, 360 @ 700k, only those ≤ source height; unknown source height → 720.
- Switch off (default): one rung = the negotiated profile capped at the source height; session key `ch|codec|profile`. Switch on (`streaming.adaptive`): ladder, key `ch` (H.264 only, HEVC setting ignored). Software encoder (libx264): always one rung.
- Rungs are video-only. Audio group `aac`: one AAC stereo rendition per broadcast audio track (max 3). Audio group `ac3`: each AC-3 broadcast track copied. Captions: one WebVTT rendition "English CC", group `subs`, on the top rung.
- Names: rungs `v<height>`, audio `aac<i>` / `ac3<i>`; files `v720.m3u8`, `v720_%05d.ts`, `v720_vtt.m3u8`, `v720N.vtt`, `aac0.m3u8`, `aac0_%05d.ts`, `ac30.m3u8`.
- Language names: eng → English, spa → Español, fra/fre → Français; audio description → "Described video"; else the code; duplicates numbered ("English 2").
- Caption tap arg exactly `movie='pipe\:3'[out0+subcc]` (Go `"movie='pipe\\:3'[out0+subcc]"`); `-a53cc 0` with captions on libx264, h264_qsv, h264_nvenc.
- Master per viewer: rungs above the viewer's ceiling omitted (lowest rung always kept); 720 (or top if lower) listed first, then descending; CODECS `avc1.640029,mp4a.40.2` / `avc1.640029,ac-3`; HEVC single rung `hvc1.1.6.L123.B0`; BANDWIDTH = (maxrate + audio kbps) × 1.1 × 1000.
- VTT insert: `X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000`.
- Fallback layout: one rung, first audio track as AAC, no AC-3, no captions.
- Kill switch: `BOWTIE_MULTITRACK=off` / `disableMultitrack: true` = video + first audio (AAC), no captions, no AC-3 group.
- Restarts keep `append_list+discont_start+omit_endlist`.
- Live tests on the real HDHomeRun: one tuner, 9.1, ≤2 min, stop sessions afterwards. **Never touch the living-room Roku (192.168.50.65) without asking the user first.**

## Review Focus

1. Two viewers with different `max_quality` on one channel with the switch on must share one FFmpeg and see different masters (ceiling). Pinned in Task 6.
2. A PMT listing an audio PID FFmpeg never sees (`-map 0:a:1` matches nothing) must fall back, not fail the start. Pinned in Task 6.
3. Viewers polling only rendition playlists must not be reaped (every `.m3u8` fetch Touches). Pinned in Task 8.
4. A stalled caption tap must not stop video (no force-close hook on the caption sub). Pinned in Task 6.
5. A saved audio preference must not be overwritten by the player's own initial selection (iOS ordering). Pinned in Task 12.

---

### Task 1: Live feasibility check (throwaway; ledger rulings)

**Files:** none in the repo; scratch `$S=/private/tmp/claude-501/-Users-ajthom90-projects-antenna-helper/bd71e177-69ab-5021-b7a6-90d4792a621f/scratchpad/exp/cs`.

- [ ] **Step 1: Ladder + demuxed audio + AC-3 copy + captions, live 9.1 for 60 s (VideoToolbox, Mac FFmpeg)**

```bash
S=/private/tmp/claude-501/-Users-ajthom90-projects-antenna-helper/bd71e177-69ab-5021-b7a6-90d4792a621f/scratchpad/exp/cs
mkdir -p $S/out && cd $S && rm -f out/* cap.fifo && mkfifo cap.fifo
timeout 60 curl -s http://192.168.50.32:5004/auto/v9.1 | tee cap.fifo | \
 ffmpeg -hide_banner -loglevel warning -nostats -fflags +discardcorrupt -i pipe:0 \
  -f lavfi -i "movie='pipe\:3'[out0+subcc]" \
  -filter_complex "[0:v]yadif=0:-1:0,split=3[s0][s1][s2];[s0]scale=-2:720[v0];[s1]scale=-2:480[v1];[s2]scale=-2:360[v2]" \
  -map "[v0]" -map "[v1]" -map "[v2]" -map 0:a:0 -map 0:a:1 -map 0:a:0 -map 1:s:0 \
  -c:v h264_videotoolbox -realtime 1 -profile:v high -level 4.1 -a53cc 0 \
  -b:v:0 3000k -maxrate:v:0 3600k -bufsize:v:0 6000k -b:v:1 1400k -maxrate:v:1 1680k -bufsize:v:1 2800k -b:v:2 700k -maxrate:v:2 840k -bufsize:v:2 1400k \
  -g 120 -force_key_frames 'expr:gte(t,n_forced*4)' \
  -c:a:0 aac -ac:a:0 2 -b:a:0 128k -c:a:1 aac -ac:a:1 2 -b:a:1 128k -c:a:2 copy -c:s webvtt \
  -f hls -hls_time 4 -hls_list_size 30 -hls_flags delete_segments+temp_file+omit_endlist -hls_segment_type mpegts \
  -hls_segment_filename out/%v_%05d.ts \
  -var_stream_map "v:0,s:0,sgroup:subs,name:v720 v:1,name:v480 v:2,name:v360 a:0,agroup:aac,name:aac0 a:1,agroup:aac,name:aac1 a:2,agroup:ac3,name:ac30" \
  out/%v.m3u8 3<cap.fifo
ls out | sed 's/_[0-9]*\.ts$//' | sort | uniq -c
```
Expected: playlists `v720 v480 v360 aac0 aac1 ac30 v720_vtt`, segments for each, `v720N.vtt` files. If any `-ac:a:0`/`-level` option errors, ledger the working spelling.

- [ ] **Step 2: Alignment** — `for v in v720 v480 v360; do grep -c EXTINF out/$v.m3u8; done` equal counts; `ffprobe -v error -select_streams v -show_entries packet=pts_time,flags -of csv=p=0 out/v480_00002.ts | head -1` and the same for v720/v360: identical first PTS with `K` flag. `ffprobe -v error -show_entries stream=profile,level -select_streams v out/v720_00002.ts` → High, 41 (else ledger the CODECS string to use).

- [ ] **Step 3: Restart continuity** — rerun Step 1 for 20 s with `+append_list+discont_start`; every playlist continues numbering after an `#EXT-X-DISCONTINUITY`.

- [ ] **Step 4: Players.** Write `out/index.m3u8`:

```
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aac",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="aac0.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aac",NAME="Español",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="aac1.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="ac3",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="6",URI="ac30.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="v720_vtt.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=4100000,RESOLUTION=1280x720,CODECS="avc1.640029,mp4a.40.2",AUDIO="aac",SUBTITLES="subs"
v720.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=854x480,CODECS="avc1.640029,mp4a.40.2",AUDIO="aac",SUBTITLES="subs"
v480.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1100000,RESOLUTION=640x360,CODECS="avc1.640029,mp4a.40.2",AUDIO="aac",SUBTITLES="subs"
v360.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=4400000,RESOLUTION=1280x720,CODECS="avc1.640029,ac-3",AUDIO="ac3",SUBTITLES="subs"
v720.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2300000,RESOLUTION=854x480,CODECS="avc1.640029,ac-3",AUDIO="ac3",SUBTITLES="subs"
v480.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1400000,RESOLUTION=640x360,CODECS="avc1.640029,ac-3",AUDIO="ac3",SUBTITLES="subs"
v360.m3u8
```
Serve `out/` with `python3 -m http.server 8713` (background) and run `$S/../../mediasel http://127.0.0.1:8713/index.m3u8` → expected `status=1`, audible English/Spanish (+ 5.1 English), legible English. Then load the same URL in Chrome via Playwright with hls.js (`https://cdn.jsdelivr.net/npm/hls.js@1`) on a scratch HTML page: `hls.levels.length === 3` (AC-3 variants filtered), `hls.audioTracks.length === 2`, `hls.subtitleTracks.length === 1`, playback starts. Ledger results; kill the http server (`pkill -f "http.server 871[3]"`).

- [ ] **Step 5: Captions** — `cat out/v7201.vtt out/v7202.vtt`: readable cues? If garbled, repeat Step 1 for 30 s with `-real_time 1` and then `-data_field first` placed before `-f lavfi`; ledger the option that gives clean text. First video PTS ≈ 1.4 s (`ffprobe … packet=pts_time out/v720_00000.ts | head -1`) → keep 126000, else ledger the measured offset.

- [ ] **Step 6: Ledger** all results as `Task 1: Ruling:` lines. No commit.

---

### Task 2: `transcode.Layout` — rungs, audio groups, names, master builder

**Files:**
- Create: `server/internal/transcode/layout.go`, `server/internal/transcode/layout_test.go`

**Interfaces:**
- Produces:
  - `type Rung struct { Height, VideoKbps int }`
  - `type AudioTrack struct { Lang string; Described, AC3 bool }`
  - `type Layout struct { Rungs []Rung; Audio []AudioTrack; AudioKbps int; Captions, AC3Copy bool; VideoCodec string }`
  - `func Ladder(sourceHeight int) []Rung`
  - `const MaxAudioTracks = 3`, `const UnknownSourceHeight = 720`
  - `func (l Layout) TopName() string` (e.g. `"v720"`), `func (l Layout) PlaylistReady() string` (`TopName()+".m3u8"`... see code), `func (l Layout) CaptionPlaylist() string`
  - `func (l Layout) AACTracks() []AudioTrack`, `func (l Layout) AC3Tracks() []AudioTrack`, `func (l Layout) AudioNames() []string`
  - `func (l Layout) VarStreamMap() string`
  - `func (l Layout) Safe() Layout`, `func (l Layout) IsSafe() bool`
  - `func MasterPlaylist(l Layout, ceiling int, query string, captionsReady bool) string`
  - `func (t AudioTrack) BCP47() string`

- [ ] **Step 1: Tests**

```go
package transcode_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestLadderFromSourceHeight(t *testing.T) {
	cases := map[int]string{720: "720,480,360", 1080: "1080,720,480,360", 480: "480,360", 0: "720,480,360"}
	for src, want := range cases {
		var got []string
		for _, r := range transcode.Ladder(src) {
			got = append(got, strconv.Itoa(r.Height))
		}
		if strings.Join(got, ",") != want {
			t.Errorf("Ladder(%d)=%v want %s", src, got, want)
		}
	}
	if r := transcode.Ladder(1080)[0]; r.VideoKbps != 6000 {
		t.Errorf("1080 rung kbps=%d", r.VideoKbps)
	}
}

func full() transcode.Layout {
	return transcode.Layout{
		Rungs:     transcode.Ladder(720),
		Audio:     []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}},
		AudioKbps: 128,
		Captions:  true,
		AC3Copy:   true,
		VideoCodec: "h264",
	}
}

func TestVarStreamMapFull(t *testing.T) {
	want := "v:0,s:0,sgroup:subs,name:v720 v:1,name:v480 v:2,name:v360 " +
		"a:0,agroup:aac,name:aac0 a:1,agroup:aac,name:aac1 " +
		"a:2,agroup:ac3,name:ac30 a:3,agroup:ac3,name:ac31"
	if got := full().VarStreamMap(); got != want {
		t.Fatalf("VarStreamMap=%q\nwant %q", got, want)
	}
}

func TestVarStreamMapSafe(t *testing.T) {
	s := full().Safe()
	if !s.IsSafe() || len(s.Rungs) != 1 || s.Rungs[0].Height != 720 || s.Captions || s.AC3Copy || len(s.Audio) != 1 {
		t.Fatalf("Safe()=%+v", s)
	}
	if got := s.VarStreamMap(); got != "v:0,name:v720 a:0,agroup:aac,name:aac0" {
		t.Fatalf("safe VarStreamMap=%q", got)
	}
}

func TestAC3OnlyForAC3Tracks(t *testing.T) {
	l := full()
	l.Audio = []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa"}}
	if n := len(l.AC3Tracks()); n != 1 {
		t.Fatalf("AC3Tracks=%d want 1", n)
	}
	l.AC3Copy = false
	if n := len(l.AC3Tracks()); n != 0 {
		t.Fatalf("AC3Copy off: AC3Tracks=%d", n)
	}
}

func TestAudioCapAndNames(t *testing.T) {
	l := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "eng"}, {Lang: "spa"}, {Lang: "fra"}}}
	if got := strings.Join(l.AudioNames(), "|"); got != "English|English 2|Español" {
		t.Fatalf("AudioNames=%q (cap 3, dedupe)", got)
	}
	l2 := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng", Described: true}, {Lang: "xyz"}, {}}}
	if got := strings.Join(l2.AudioNames(), "|"); got != "Described video|xyz|Audio" {
		t.Fatalf("AudioNames=%q", got)
	}
}

func TestNamesForTopRung(t *testing.T) {
	l := full()
	if l.TopName() != "v720" || l.CaptionPlaylist() != "v720_vtt.m3u8" || l.ReadyPlaylist() != "v360.m3u8" {
		t.Fatalf("names: %s %s %s", l.TopName(), l.CaptionPlaylist(), l.ReadyPlaylist())
	}
}

func TestMasterPlaylistFullLadder(t *testing.T) {
	got := transcode.MasterPlaylist(full(), 1080, "?token=T", true)
	want := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		"#EXT-X-INDEPENDENT-SEGMENTS",
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aac",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="aac0.m3u8?token=T"`,
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aac",NAME="Español",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="2",URI="aac1.m3u8?token=T"`,
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="ac3",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="6",URI="ac30.m3u8?token=T"`,
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="ac3",NAME="Español",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,CHANNELS="6",URI="ac31.m3u8?token=T"`,
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="v720_vtt.m3u8?token=T"`,
		`#EXT-X-STREAM-INF:BANDWIDTH=4100800,RESOLUTION=1280x720,CODECS="avc1.640029,mp4a.40.2",AUDIO="aac",SUBTITLES="subs"`,
		"v720.m3u8?token=T",
		`#EXT-X-STREAM-INF:BANDWIDTH=1988800,RESOLUTION=854x480,CODECS="avc1.640029,mp4a.40.2",AUDIO="aac",SUBTITLES="subs"`,
		"v480.m3u8?token=T",
		`#EXT-X-STREAM-INF:BANDWIDTH=1064800,RESOLUTION=640x360,CODECS="avc1.640029,mp4a.40.2",AUDIO="aac",SUBTITLES="subs"`,
		"v360.m3u8?token=T",
		`#EXT-X-STREAM-INF:BANDWIDTH=4382400,RESOLUTION=1280x720,CODECS="avc1.640029,ac-3",AUDIO="ac3",SUBTITLES="subs"`,
		"v720.m3u8?token=T",
		`#EXT-X-STREAM-INF:BANDWIDTH=2270400,RESOLUTION=854x480,CODECS="avc1.640029,ac-3",AUDIO="ac3",SUBTITLES="subs"`,
		"v480.m3u8?token=T",
		`#EXT-X-STREAM-INF:BANDWIDTH=1346400,RESOLUTION=640x360,CODECS="avc1.640029,ac-3",AUDIO="ac3",SUBTITLES="subs"`,
		"v360.m3u8?token=T",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("master:\n%s\nwant:\n%s", got, want)
	}
}

func TestMasterPlaylistCeilingAndOrder(t *testing.T) {
	l := full()
	l.Rungs = transcode.Ladder(1080)
	l.AC3Copy = false
	got := transcode.MasterPlaylist(l, 480, "", false)
	if strings.Contains(got, "v1080.m3u8") || strings.Contains(got, "v720.m3u8") || !strings.Contains(got, "v480.m3u8") {
		t.Fatalf("ceiling 480 not applied:\n%s", got)
	}
	got = transcode.MasterPlaylist(l, 100, "", false)
	if !strings.Contains(got, "v360.m3u8") {
		t.Fatalf("lowest rung must always be kept:\n%s", got)
	}
	got = transcode.MasterPlaylist(l, 1080, "", false)
	i720, i1080 := strings.Index(got, "\nv720.m3u8"), strings.Index(got, "\nv1080.m3u8")
	if i720 < 0 || i1080 < 0 || i720 > i1080 {
		t.Fatalf("720 must be listed first:\n%s", got)
	}
	if strings.Contains(got, "SUBTITLES") {
		t.Fatalf("captions listed before ready:\n%s", got)
	}
}

func TestMasterPlaylistHEVCSingleRung(t *testing.T) {
	l := transcode.Layout{Rungs: []transcode.Rung{{Height: 720, VideoKbps: 4000}}, AudioKbps: 160, VideoCodec: "hevc"}
	got := transcode.MasterPlaylist(l, 1080, "", false)
	if !strings.Contains(got, `CODECS="hvc1.1.6.L123.B0,mp4a.40.2"`) {
		t.Fatalf("hevc codecs:\n%s", got)
	}
}
```

- [ ] **Step 2: Run** `cd server && go test ./internal/transcode/ -run 'Ladder|VarStreamMap|AC3|AudioCap|Names|MasterPlaylist'` — Expected: FAIL (undefined `transcode.Ladder`).

- [ ] **Step 3: Implement `layout.go`**

```go
package transcode

import (
	"fmt"
	"strings"
)

const (
	// MaxAudioTracks caps broadcast audio tracks per session.
	MaxAudioTracks = 3
	// UnknownSourceHeight is assumed when the MPEG-2 sequence header was not seen.
	UnknownSourceHeight = 720
)

// Rung is one video rendition of a session's ladder.
type Rung struct {
	Height    int
	VideoKbps int
}

var ladder = []Rung{{1080, 6000}, {720, 3000}, {480, 1400}, {360, 700}}

// Ladder is every rung at or below the source height (unknown → 720).
func Ladder(sourceHeight int) []Rung {
	if sourceHeight <= 0 {
		sourceHeight = UnknownSourceHeight
	}
	var out []Rung
	for _, r := range ladder {
		if r.Height <= sourceHeight {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		out = []Rung{ladder[len(ladder)-1]}
	}
	return out
}

// AudioTrack is one broadcast audio stream, in PMT order (FFmpeg's 0:a:N).
type AudioTrack struct {
	Lang      string // ISO 639-2, e.g. "eng"; "" if absent
	Described bool   // audio description for the visually impaired
	AC3       bool   // AC-3 in the broadcast (eligible for the 5.1 copy group)
}

// Layout is a session's HLS output: video-only rungs (highest first), one AAC
// rendition per audio track, optional AC-3 copies, optional WebVTT captions.
type Layout struct {
	Rungs      []Rung
	Audio      []AudioTrack
	AudioKbps  int
	Captions   bool
	AC3Copy    bool
	VideoCodec string // "h264" or "hevc"
}

func (l Layout) rungs() []Rung {
	if len(l.Rungs) == 0 {
		return []Rung{{Height: UnknownSourceHeight, VideoKbps: 3000}}
	}
	return l.Rungs
}

// AACTracks are the tracks encoded to AAC stereo (at least one, at most 3).
func (l Layout) AACTracks() []AudioTrack {
	if len(l.Audio) == 0 {
		return []AudioTrack{{}}
	}
	if len(l.Audio) > MaxAudioTracks {
		return l.Audio[:MaxAudioTracks]
	}
	return l.Audio
}

// AC3Tracks are the broadcast AC-3 tracks copied into the 5.1 group.
func (l Layout) AC3Tracks() []AudioTrack {
	if !l.AC3Copy {
		return nil
	}
	var out []AudioTrack
	for _, t := range l.AACTracks() {
		if t.AC3 {
			out = append(out, t)
		}
	}
	return out
}

// AC3Indexes are the PMT-order indexes (0:a:N) of AC3Tracks.
func (l Layout) AC3Indexes() []int {
	if !l.AC3Copy {
		return nil
	}
	var out []int
	for i, t := range l.AACTracks() {
		if t.AC3 {
			out = append(out, i)
		}
	}
	return out
}

func rungName(r Rung) string { return fmt.Sprintf("v%d", r.Height) }

// TopName is the top rung's variant name (captions ride it).
func (l Layout) TopName() string { return rungName(l.rungs()[0]) }

// ReadyPlaylist is the playlist whose existence means the session can play
// (the lowest rung, written last by FFmpeg's variant loop).
func (l Layout) ReadyPlaylist() string {
	r := l.rungs()
	return rungName(r[len(r)-1]) + ".m3u8"
}

// CaptionPlaylist is FFmpeg's WebVTT playlist name for the top rung.
func (l Layout) CaptionPlaylist() string { return l.TopName() + "_vtt.m3u8" }

// Safe is the fallback: top rung, first audio as AAC, no AC-3, no captions.
func (l Layout) Safe() Layout {
	s := Layout{Rungs: l.rungs()[:1], AudioKbps: l.AudioKbps, VideoCodec: l.VideoCodec}
	if len(l.Audio) > 0 {
		s.Audio = []AudioTrack{{Lang: l.Audio[0].Lang}}
	}
	return s
}

// IsSafe reports whether l is already the fallback shape.
func (l Layout) IsSafe() bool {
	return len(l.rungs()) == 1 && len(l.AACTracks()) == 1 && !l.Captions && len(l.AC3Tracks()) == 0
}

// VarStreamMap is FFmpeg's -var_stream_map: rungs, then AAC, then AC-3.
func (l Layout) VarStreamMap() string {
	var parts []string
	for i, r := range l.rungs() {
		p := fmt.Sprintf("v:%d", i)
		if i == 0 && l.Captions {
			p += ",s:0,sgroup:subs"
		}
		parts = append(parts, p+",name:"+rungName(r))
	}
	a := 0
	for i := range l.AACTracks() {
		parts = append(parts, fmt.Sprintf("a:%d,agroup:aac,name:aac%d", a, i))
		a++
	}
	for i := range l.AC3Tracks() {
		parts = append(parts, fmt.Sprintf("a:%d,agroup:ac3,name:ac3%d", a, i))
		a++
	}
	return strings.Join(parts, " ")
}

var languageNames = map[string]string{"eng": "English", "spa": "Español", "fra": "Français", "fre": "Français"}
var bcp47 = map[string]string{"eng": "en", "spa": "es", "fra": "fr", "fre": "fr"}

// BCP47 is the HLS LANGUAGE value ("" if unknown).
func (t AudioTrack) BCP47() string {
	if v, ok := bcp47[t.Lang]; ok {
		return v
	}
	return t.Lang
}

func (t AudioTrack) name() string {
	switch {
	case t.Described:
		return "Described video"
	case languageNames[t.Lang] != "":
		return languageNames[t.Lang]
	case t.Lang != "":
		return t.Lang
	default:
		return "Audio"
	}
}

func uniqueNames(ts []AudioTrack) []string {
	out := make([]string, len(ts))
	seen := map[string]int{}
	for i, t := range ts {
		base := t.name()
		seen[base]++
		out[i] = base
		if seen[base] > 1 {
			out[i] = fmt.Sprintf("%s %d", base, seen[base])
		}
	}
	return out
}

// AudioNames are the AAC group's rendition NAMEs (unique, as HLS requires).
func (l Layout) AudioNames() []string { return uniqueNames(l.AACTracks()) }

func (l Layout) videoCodecs() string {
	if l.VideoCodec == "hevc" {
		return "hvc1.1.6.L123.B0"
	}
	return "avc1.640029"
}

// MasterPlaylist is the per-viewer master served as index.m3u8. Rungs above
// ceiling are omitted (the lowest rung is always kept); query (e.g.
// "?token=…") is appended to every URI.
func MasterPlaylist(l Layout, ceiling int, query string, captionsReady bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	writeGroup := func(group, channels, uriPrefix string, tracks []AudioTrack) {
		names := uniqueNames(tracks)
		for i, t := range tracks {
			fmt.Fprintf(&b, `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="%s",NAME="%s"`, group, names[i])
			if lang := t.BCP47(); lang != "" {
				fmt.Fprintf(&b, `,LANGUAGE="%s"`, lang)
			}
			def := "NO"
			if i == 0 {
				def = "YES"
			}
			fmt.Fprintf(&b, `,DEFAULT=%s,AUTOSELECT=YES,CHANNELS="%s",URI="%s%d.m3u8%s"`, def, channels, uriPrefix, i, query)
			if t.Described {
				b.WriteString(`,CHARACTERISTICS="public.accessibility.describes-video"`)
			}
			b.WriteByte('\n')
		}
	}
	aac, ac3 := l.AACTracks(), l.AC3Tracks()
	writeGroup("aac", "2", "aac", aac)
	writeGroup("ac3", "6", "ac3", ac3)
	if captionsReady {
		fmt.Fprintf(&b, `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="%s%s"`+"\n", l.CaptionPlaylist(), query)
	}

	rs := visibleRungs(l.rungs(), ceiling)
	variants := func(group, audioCodec string, audioKbps int) {
		for _, r := range rs {
			bw := (r.VideoKbps*12/10 + audioKbps) * 1100
			fmt.Fprintf(&b, `#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS="%s,%s",AUDIO="%s"`,
				bw, (r.Height*16/9+1)/2*2, r.Height, l.videoCodecs(), audioCodec, group)
			if captionsReady {
				b.WriteString(`,SUBTITLES="subs"`)
			}
			fmt.Fprintf(&b, "\n%s.m3u8%s\n", rungName(r), query)
		}
	}
	variants("aac", "mp4a.40.2", l.AudioKbps)
	if len(ac3) > 0 {
		variants("ac3", "ac-3", 384)
	}
	return b.String()
}

// visibleRungs drops rungs above ceiling (keeping the lowest) and orders them
// 720-or-lower-top first, then the rest descending: AVPlayer starts on the
// first listed variant.
func visibleRungs(all []Rung, ceiling int) []Rung {
	var kept []Rung
	for _, r := range all {
		if ceiling <= 0 || r.Height <= ceiling {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		kept = all[len(all)-1:]
	}
	first := 0
	for i, r := range kept {
		if r.Height <= 720 {
			first = i
			break
		}
	}
	out := []Rung{kept[first]}
	for i, r := range kept {
		if i != first {
			out = append(out, r)
		}
	}
	return out
}
```
Bandwidth check: AAC variants (maxrate + 128) × 1100 → 720: 4,100,800, 480: 1,988,800, 360: 1,064,800; AC-3 variants count 384 kbps → 4,382,400, 2,270,400, 1,346,400.

- [ ] **Step 4: Run** `go test ./internal/transcode/` — Expected: PASS.

- [ ] **Step 5: Commit** `git add server/internal/transcode/layout*.go && git commit -m "feat(transcode): session layout, ladder and per-viewer master playlist"`

---

### Task 3: `BuildArgs` — filter_complex ladder, audio groups, captions

**Files:**
- Modify: `server/internal/transcode/ffmpeg.go` (`JobSpec`, `BuildArgs`, `videoFilter` → `videoGraph`, `encoderExtras`)
- Modify: `server/internal/transcode/ffmpeg_test.go` (all expectations), `server/internal/transcode/ffmpeg_e2e_test.go`

**Interfaces:**
- Consumes: Task 2 `Layout`, `Rung`, `Ladder`.
- Produces: `JobSpec.Layout Layout`, `JobSpec.CaptionInput io.Reader`; `func videoGraph(b Backend, rungs []Rung) string`.

- [ ] **Step 1: Tests.** Replace the per-backend golden tests' tail so a one-rung session reads (QSV shown; others analogous with their filter):

```go
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-init_hw_device", "qsv=hw", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-c:v", "mpeg2_qsv",
		"-fflags", "+discardcorrupt", "-i", "pipe:0",
		"-filter_complex", "[0:v]vpp_qsv=deinterlace=2:w=trunc(iw*720/ih/2)*2:h=720[v0]",
		"-map", "[v0]", "-map", "0:a:0",
		"-c:v", "h264_qsv", "-profile:v", "high", "-level", "4.1",
		"-b:v:0", "4000k", "-maxrate:v:0", "4800k", "-bufsize:v:0", "8000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-preset", "veryfast",
		"-c:a:0", "aac", "-ac:a:0", "2", "-b:a:0", "160k",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file+omit_endlist",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "%v_%05d.ts"),
		"-var_stream_map", "v:0,name:v720 a:0,agroup:aac,name:aac0",
		filepath.Join(out, "%v.m3u8"),
	}
```
where the spec is `JobSpec{Stdin: nonNilStdin(), OutDir: out, D: <QSV 720p decision, AudioKbps 160>, Layout: transcode.Layout{Rungs: []transcode.Rung{{720, 4000}}, AudioKbps: 160, VideoCodec: "h264"}}`. Keep today's per-backend single-rung filters verbatim inside `[0:v]…[v0]`: software/VT `yadif=0:-1:0,scale=-2:H`, NVENC `yadif_cuda=0:-1:0,scale_cuda=-2:H`, VAAPI `deinterlace_vaapi=rate=frame,scale_vaapi=w=-2:h=H`. Add:

```go
func TestBuildArgsLadderQSVWithAudioAndCaptions(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		Stdin: nonNilStdin(), OutDir: out,
		D: transcode.Decision{VideoCodec: "h264", VideoEncoder: "h264_qsv", Backend: transcode.BackendQSV,
			Profile: transcode.Profile{Name: "original", Height: 1080, VideoKbps: 8000, AudioKbps: 160}},
		Layout: transcode.Layout{
			Rungs:     transcode.Ladder(720),
			Audio:     []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}},
			AudioKbps: 128, Captions: true, AC3Copy: true, VideoCodec: "h264",
		},
	}
	got := transcode.BuildArgs(s)
	mustContainSeq(t, got, "-f", "lavfi", "-i", "movie='pipe\\:3'[out0+subcc]")
	mustContainSeq(t, got, "-filter_complex",
		"[0:v]vpp_qsv=deinterlace=2,split=3[s0][s1][s2];"+
			"[s0]vpp_qsv=w=trunc(iw*720/ih/2)*2:h=720[v0];"+
			"[s1]vpp_qsv=w=trunc(iw*480/ih/2)*2:h=480[v1];"+
			"[s2]vpp_qsv=w=trunc(iw*360/ih/2)*2:h=360[v2]")
	mustContainSeq(t, got, "-map", "[v0]", "-map", "[v1]", "-map", "[v2]",
		"-map", "0:a:0", "-map", "0:a:1", "-map", "0:a:0", "-map", "0:a:1", "-map", "1:s:0")
	mustContainSeq(t, got, "-b:v:2", "700k", "-maxrate:v:2", "840k", "-bufsize:v:2", "1400k")
	mustContainSeq(t, got, "-forced_idr", "1")
	mustContainSeq(t, got, "-a53cc", "0")
	mustContainSeq(t, got, "-c:a:1", "aac", "-ac:a:1", "2", "-b:a:1", "128k", "-c:a:2", "copy", "-c:a:3", "copy")
	mustContainSeq(t, got, "-c:s", "webvtt")
	mustContainSeq(t, got, "-var_stream_map", s.Layout.VarStreamMap())
}

// mustContainSeq fails unless want appears contiguously in got.
func mustContainSeq(t *testing.T, got []string, want ...string) {
	t.Helper()
	for i := 0; i+len(want) <= len(got); i++ {
		ok := true
		for j := range want {
			if got[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return
		}
	}
	t.Fatalf("args missing %q\n%q", want, got)
}

func TestBuildArgsSingleRungQSVKeepsNoForcedIDR(t *testing.T) {
	// Production QSV single-rung path stays as proven; forced IDR only for ladders.
	s := transcode.JobSpec{Stdin: nonNilStdin(), OutDir: "/tmp/out",
		D:      transcode.Decision{VideoCodec: "h264", VideoEncoder: "h264_qsv", Backend: transcode.BackendQSV},
		Layout: transcode.Layout{Rungs: []transcode.Rung{{720, 4000}}, VideoCodec: "h264"}}
	if containsAdjacent(transcode.BuildArgs(s), "-forced_idr", "1") {
		t.Fatal("single rung must not change QSV IDR behavior")
	}
}
```
Update the HLS e2e test (`ffmpeg_e2e_test.go`) to wait for `Layout.ReadyPlaylist()` and glob `v*_*.ts`; add a ladder case (software, `Ladder(480)` on its test clip) asserting equal EXTINF counts across `v480.m3u8` and `v360.m3u8` and that the first packet of `v360_00001.ts` has the keyframe flag (ffprobe `-show_entries packet=flags`).

If Task 1 ruled cc_dec options (e.g. `-real_time 1`), they go immediately before `"-f", "lavfi"` in both tests and code.

- [ ] **Step 2: Run** `cd server && go test ./internal/transcode/` — Expected: FAIL (unknown field `Layout`).

- [ ] **Step 3: Implement.** `JobSpec` gains `Layout Layout` and `CaptionInput io.Reader` (doc: "fed to fd 3 by the runner when Layout.Captions"). In `BuildArgs` after the input block:

```go
	l := s.Layout
	if l.Captions {
		// Same TS again on fd 3, decoded in software only for CEA-608.
		args = append(args, "-f", "lavfi", "-i", "movie='pipe\\:3'[out0+subcc]")
	}
	rungs := l.Rungs
	if len(rungs) == 0 {
		rungs = []Rung{{Height: s.D.Profile.Height, VideoKbps: s.D.Profile.VideoKbps}}
	}
	args = append(args, "-filter_complex", videoGraph(s.D.Backend, rungs))
	for i := range rungs {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
	}
	aac := l.AACTracks()
	for i := range aac {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
	}
	for _, i := range l.AC3Indexes() {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
	}
	if l.Captions {
		args = append(args, "-map", "1:s:0")
	}
	args = append(args, "-c:v", s.D.VideoEncoder)
	if s.D.VideoCodec != "hevc" {
		// Pinned so the master's CODECS (avc1.640029) is true.
		args = append(args, "-profile:v", "high", "-level", "4.1")
	}
	for i, r := range rungs {
		args = append(args,
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", r.VideoKbps),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", r.VideoKbps*12/10),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", r.VideoKbps*2),
		)
	}
	args = append(args, "-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)")
	args = append(args, encoderExtras(s.D)...)
	if len(rungs) > 1 {
		switch s.D.VideoEncoder {
		case "h264_qsv":
			args = append(args, "-forced_idr", "1")
		case "h264_nvenc":
			args = append(args, "-forced-idr", "1")
		}
	}
	if l.Captions {
		switch s.D.VideoEncoder {
		case "libx264", "h264_qsv", "h264_nvenc":
			args = append(args, "-a53cc", "0")
		}
	}
	akbps := l.AudioKbps
	if akbps == 0 {
		akbps = s.D.Profile.AudioKbps
	}
	n := 0
	for range aac {
		args = append(args,
			fmt.Sprintf("-c:a:%d", n), "aac",
			fmt.Sprintf("-ac:a:%d", n), "2",
			fmt.Sprintf("-b:a:%d", n), fmt.Sprintf("%dk", akbps),
		)
		n++
	}
	for range l.AC3Indexes() {
		args = append(args, fmt.Sprintf("-c:a:%d", n), "copy")
		n++
	}
	if l.Captions {
		args = append(args, "-c:s", "webvtt")
	}
```
Remove the old `-vf`, single `-b:v/-maxrate/-bufsize`, and `-c:a` blocks; remove `"-profile:v", "high"` from `encoderExtras` for libx264/h264_videotoolbox (now emitted above; keep `-preset`, `-realtime`, `-a53cc 0` for VT). Output tail:

```go
		"-hls_segment_filename", filepath.Join(s.OutDir, "%v_%05d.ts"),
		"-var_stream_map", l.withRungs(rungs).VarStreamMap(),
		filepath.Join(s.OutDir, "%v.m3u8"),
```
with in `layout.go`: `func (l Layout) withRungs(r []Rung) Layout { l.Rungs = r; return l }`. Replace `videoFilter` with:

```go
// videoGraph decodes/deinterlaces once and scales each rung; labels [v0]….
// One rung keeps the single filter chain proven in production.
func videoGraph(b Backend, rungs []Rung) string {
	deint, scale := filterParts(b)
	if len(rungs) == 1 {
		return fmt.Sprintf("[0:v]%s[v0]", singleChain(b, deint, scale(rungs[0].Height)))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[0:v]%s,split=%d", deint, len(rungs))
	for i := range rungs {
		fmt.Fprintf(&sb, "[s%d]", i)
	}
	for i, r := range rungs {
		fmt.Fprintf(&sb, ";[s%d]%s[v%d]", i, scale(r.Height), i)
	}
	return sb.String()
}

func filterParts(b Backend) (string, func(int) string) {
	switch b {
	case BackendQSV:
		// FFmpeg 5.1 vpp_qsv: explicit even width (no w=-1), no scale_mode.
		return "vpp_qsv=deinterlace=2", func(h int) string { return fmt.Sprintf("vpp_qsv=w=trunc(iw*%d/ih/2)*2:h=%d", h, h) }
	case BackendNVENC:
		return "yadif_cuda=0:-1:0", func(h int) string { return fmt.Sprintf("scale_cuda=-2:%d", h) }
	case BackendVAAPI:
		return "deinterlace_vaapi=rate=frame", func(h int) string { return fmt.Sprintf("scale_vaapi=w=-2:h=%d", h) }
	default:
		return "yadif=0:-1:0", func(h int) string { return fmt.Sprintf("scale=-2:%d", h) }
	}
}

func singleChain(b Backend, deint, scale string) string {
	if b == BackendQSV {
		// One vpp_qsv does both, exactly as before this change.
		return "vpp_qsv=deinterlace=2:" + strings.TrimPrefix(scale, "vpp_qsv=")
	}
	return deint + "," + scale
}
```

- [ ] **Step 4: Run** `go test ./internal/transcode/ && go vet ./internal/transcode/` — Expected: PASS, including the FFmpeg e2e ladder case when `ffmpeg` is on PATH.

- [ ] **Step 5: Commit** `git commit -am "feat(transcode): ladder filter graph, audio groups and caption tap in BuildArgs"`

---

### Task 4: Runner wires the caption input to fd 3

Same as the approved multitrack plan's Task 4 (`git show origin/docs/multitrack-hls:docs/superpowers/plans/2026-10-03-multitrack-hls.md`) (fake ffmpeg `testdata/fd3cat.sh` that `cat <&3 > "$FD3_OUT"`; `TestRunnerFeedsCaptionInputOnFD3`; `os.Pipe` + `cmd.ExtraFiles` + copy goroutine closing the write end; parent closes the read end after `Start`).

**Files:** Modify `server/internal/stream/runner.go`; Create `server/internal/stream/testdata/fd3cat.sh`; Test `server/internal/stream/runner_test.go`.

- [ ] **Step 1: Test**

```go
func TestRunnerFeedsCaptionInputOnFD3(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fd3.out")
	t.Setenv("FD3_OUT", out)
	script, err := filepath.Abs("testdata/fd3cat.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &FFmpegRunner{Path: script}
	p, err := r.Start(context.Background(), transcode.JobSpec{
		Stdin: strings.NewReader(""), OutDir: t.TempDir(),
		Layout:       transcode.Layout{Captions: true},
		CaptionInput: strings.NewReader("caption-bytes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-p.Done():
		if err != nil {
			t.Fatalf("fake ffmpeg: %v", err)
		}
	case <-time.After(5 * time.Second):
		p.Stop()
		t.Fatal("fd 3 never reached EOF")
	}
	if got, _ := os.ReadFile(out); string(got) != "caption-bytes" {
		t.Fatalf("fd3 got %q", got)
	}
}
```
`testdata/fd3cat.sh` (chmod 0755):
```sh
#!/bin/sh
# Fake ffmpeg for runner tests: copies fd 3 to $FD3_OUT, ignores its args.
cat <&3 > "$FD3_OUT"
```
- [ ] **Step 2: Run** `go test ./internal/stream/ -run FD3` — Expected: FAIL (fd 3 not open).
- [ ] **Step 3: Implement** in `FFmpegRunner.Start` (replace the plain `cmd.Start()` block):

```go
	var capW *os.File
	if spec.CaptionInput != nil {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		cmd.ExtraFiles = []*os.File{pr} // fd 3 in the child
		defer pr.Close()                 // the child holds its own copy after Start
		capW = pw
	}
	if err := cmd.Start(); err != nil {
		if capW != nil {
			_ = capW.Close()
		}
		return nil, err
	}
	if capW != nil {
		// Ends when the caption sub closes (EOF) or FFmpeg exits (EPIPE).
		go func() {
			_, _ = io.Copy(capW, spec.CaptionInput)
			_ = capW.Close()
		}()
	}
```
- [ ] **Step 4: Run** `go test ./internal/stream/ -run Runner -count=3` — PASS.
- [ ] **Step 5: Commit** `git add server/internal/stream/runner*.go server/internal/stream/testdata/fd3cat.sh && git commit -m "feat(stream): feed the caption tap to FFmpeg on fd 3"`

---

### Task 5: Probe the channel — PMT audio, AC-3 flag, MPEG-2 height

**Files:**
- Create: `server/internal/stream/probe.go`, `server/internal/stream/probe_test.go`
- Modify: `server/internal/stream/ingest.go` (`channelIngest` records the video PID and source height; `ProgramInfo`)

**Interfaces:**
- Produces:
  - `type ProgramInfo struct { Audio []transcode.AudioTrack; SourceHeight int; VideoPID uint16 }`
  - `func parsePMT(pkt []byte) ProgramInfo` (Audio + VideoPID)
  - `func sequenceHeaderHeight(payload []byte) int` (0 if none)
  - `func (m *IngestManager) ProgramInfo(channelID int64, timeout time.Duration) (ProgramInfo, bool)`

- [ ] **Step 1: Tests** (`probe_test.go`):

```go
package stream

import (
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// pmtPacket builds a single-packet PMT: MPEG-2 video on 0x31 then es entries.
func pmtPacket(es ...[]byte) []byte {
	sec := []byte{0x02, 0, 0, 0x00, 0x03, 0xC1, 0x00, 0x00, 0xE0, 0x31, 0xF0, 0x00}
	sec = append(sec, 0x02, 0xE0, 0x31, 0xF0, 0x00)
	for _, e := range es {
		sec = append(sec, e...)
	}
	sec = append(sec, 0, 0, 0, 0)
	l := len(sec) - 3
	sec[1] = 0xB0 | byte(l>>8)
	sec[2] = byte(l)
	pkt := make([]byte, tsPacketSize)
	for i := range pkt {
		pkt[i] = 0xFF
	}
	copy(pkt, []byte{tsSyncByte, 0x40, 0x30, 0x10, 0x00})
	copy(pkt[5:], sec)
	return pkt
}

func audioES(streamType byte, pid uint16, lang string, audioType byte) []byte {
	var desc []byte
	if lang != "" {
		desc = append([]byte{0x0A, 4}, append([]byte(lang), audioType)...)
	}
	return append([]byte{streamType, 0xE0 | byte(pid>>8), byte(pid), 0xF0, byte(len(desc))}, desc...)
}

func TestParsePMT(t *testing.T) {
	got := parsePMT(pmtPacket(
		audioES(0x81, 0x34, "eng", 0),
		audioES(0x81, 0x35, "spa", 0),
		audioES(0x81, 0x36, "eng", 0x03),
		audioES(0x06, 0x37, "", 0),
		audioES(0x0F, 0x38, "", 0),
	))
	want := []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}, {Lang: "eng", Described: true, AC3: true}, {}}
	if got.VideoPID != 0x31 || len(got.Audio) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got.Audio[i] != want[i] {
			t.Fatalf("track %d = %+v want %+v", i, got.Audio[i], want[i])
		}
	}
}

func TestParsePMTGarbage(t *testing.T) {
	for _, pkt := range [][]byte{nil, make([]byte, 10), make([]byte, tsPacketSize)} {
		if got := parsePMT(pkt); len(got.Audio) != 0 || got.VideoPID != 0 {
			t.Fatalf("garbage parsed: %+v", got)
		}
	}
	trunc := pmtPacket(audioES(0x81, 0x34, "eng", 0))
	trunc[7] = 0xB0 | 0x0F
	_ = parsePMT(trunc) // no panic
}

func TestSequenceHeaderHeight(t *testing.T) {
	// 00 00 01 B3, width 1280 (0x500), height 720 (0x2D0): 12+12 bits = 50 02 D0.
	payload := []byte{0x47, 0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}
	if h := sequenceHeaderHeight(payload); h != 720 {
		t.Fatalf("height=%d", h)
	}
	if h := sequenceHeaderHeight([]byte{0, 0, 1, 0x00, 1, 2, 3}); h != 0 {
		t.Fatalf("no header → 0, got %d", h)
	}
	// 1920x1080: 0x780, 0x438 → 78 04 38.
	if h := sequenceHeaderHeight([]byte{0, 0, 1, 0xB3, 0x78, 0x04, 0x38}); h != 1080 {
		t.Fatalf("height=%d", h)
	}
}
```
Plus an ingest test `TestProgramInfoReadsPMTAndHeight`: dial body = PAT (PMT PID 0x30) + `pmtPacket(audioES(0x81,0x34,"eng",0))` + one TS packet on PID 0x31 with PUSI whose payload contains `00 00 01 B3 50 02 D0`; after `Attach`, `ProgramInfo(ch, 2*time.Second)` returns `SourceHeight 720` and one audio track; and `TestProgramInfoTimesOut` (no PMT → `ok=false` within ~100 ms timeout). Build the PAT packet as section `00 B0 0D 00 01 C1 00 00 00 03 E0 30` + 4 CRC bytes on PID 0 with PUSI; reuse the dial fakes in `ingest_test.go` (a `DialFunc` returning `io.NopCloser(io.MultiReader(bytes.NewReader(body), blockingReader{}))`).

- [ ] **Step 2: Run** `go test ./internal/stream/ -run 'ParsePMT|SequenceHeader|ProgramInfo'` — FAIL (undefined).

- [ ] **Step 3: Implement `probe.go`**

```go
package stream

import (
	"bytes"
	"strings"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// ProgramInfo is what a session needs to know about a channel's broadcast.
type ProgramInfo struct {
	Audio        []transcode.AudioTrack
	SourceHeight int    // MPEG-2 vertical size; 0 = unknown
	VideoPID     uint16 // 0 = none
}

// parsePMT reads a single-packet PMT (ATSC single-program): audio streams in
// PMT order (FFmpeg's 0:a:N) and the first video PID.
func parsePMT(pkt []byte) ProgramInfo {
	var info ProgramInfo
	if len(pkt) < tsPacketSize || pkt[0] != tsSyncByte || pkt[1]&0x40 == 0 {
		return info
	}
	i := 4
	if afc := (pkt[3] >> 4) & 0x3; afc == 2 || afc == 3 {
		i += 1 + int(pkt[4])
	}
	if i >= len(pkt) {
		return info
	}
	i += 1 + int(pkt[i]) // pointer_field
	if i+12 > len(pkt) || pkt[i] != 0x02 {
		return info
	}
	end := min(i+3+(int(pkt[i+1]&0x0F)<<8|int(pkt[i+2]))-4, len(pkt))
	j := i + 12 + (int(pkt[i+10]&0x0F)<<8 | int(pkt[i+11]))
	for j+5 <= end {
		st := pkt[j]
		pid := uint16(pkt[j+1]&0x1F)<<8 | uint16(pkt[j+2])
		n := int(pkt[j+3]&0x0F)<<8 | int(pkt[j+4])
		desc := pkt[j+5 : min(j+5+n, end)]
		switch {
		case isVideoStreamType(st):
			if info.VideoPID == 0 {
				info.VideoPID = pid
			}
		case isAudioStreamType(st):
			t := audioTrackFrom(desc)
			t.AC3 = st == 0x81
			info.Audio = append(info.Audio, t)
		}
		j += 5 + n
	}
	return info
}

func isVideoStreamType(st byte) bool { return st == 0x01 || st == 0x02 || st == 0x1B || st == 0x24 }

func isAudioStreamType(st byte) bool {
	switch st {
	case 0x03, 0x04, 0x0F, 0x11, 0x81, 0x87:
		return true
	}
	return false
}

// audioTrackFrom reads ISO_639_language_descriptor (0x0A); audio_type 0x03 is
// visual-impaired commentary.
func audioTrackFrom(desc []byte) transcode.AudioTrack {
	for k := 0; k+2 <= len(desc); {
		tag, l := desc[k], int(desc[k+1])
		body := desc[k+2 : min(k+2+l, len(desc))]
		if tag == 0x0A && len(body) >= 4 {
			return transcode.AudioTrack{Lang: strings.ToLower(string(body[:3])), Described: body[3] == 0x03}
		}
		k += 2 + l
	}
	return transcode.AudioTrack{}
}

var seqHeaderCode = []byte{0, 0, 1, 0xB3}

// sequenceHeaderHeight returns the 12-bit vertical_size of the first MPEG-2
// sequence header in payload, or 0.
func sequenceHeaderHeight(payload []byte) int {
	i := bytes.Index(payload, seqHeaderCode)
	if i < 0 || i+7 > len(payload) {
		return 0
	}
	b := payload[i+4:]
	return int(b[1]&0x0F)<<8 | int(b[2])
}
```
In `ingest.go`: `channelIngest` gets `videoPID uint16` and `sourceHeight int`. In `handleTSPacketLocked`, on the PMT branch set `c.videoPID = parsePMT(pkt).VideoPID`; in the default branch, when `c.sourceHeight == 0 && c.videoPID != 0 && tsPID(pkt) == c.videoPID`, set `c.sourceHeight = sequenceHeaderHeight(pkt[4:])` (payload start; ignore adaptation-field offset — a header split across packets only delays detection). Add:

```go
// ProgramInfo waits up to timeout for the channel's PMT and source height.
// ok is false when no PMT arrived (callers assume one unnamed audio track).
// Height may still be 0 (H.264 or not seen yet) when ok is true.
func (m *IngestManager) ProgramInfo(channelID int64, timeout time.Duration) (ProgramInfo, bool) {
	deadline := time.Now().Add(timeout)
	for {
		m.mu.Lock()
		c := m.channels[channelID]
		m.mu.Unlock()
		if c != nil {
			c.mu.Lock()
			pmt, h := c.lastPMT, c.sourceHeight
			c.mu.Unlock()
			if pmt != nil && (h > 0 || !time.Now().Before(deadline)) {
				info := parsePMT(pmt)
				info.SourceHeight = h
				return info, true
			}
		}
		if !time.Now().Before(deadline) {
			return ProgramInfo{}, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```
- [ ] **Step 4: Run** `go test ./internal/stream/ -run 'ParsePMT|SequenceHeader|ProgramInfo' -count=2` — PASS.
- [ ] **Step 5: Commit** `git add server/internal/stream/probe*.go server/internal/stream/ingest.go server/internal/stream/ingest_test.go && git commit -m "feat(stream): probe channel audio tracks and source height"`

---

### Task 6: Manager — layouts, session keys, caption sub, viewer ceiling, fallback

**Files:**
- Modify: `server/internal/stream/manager.go`, `server/internal/stream/session.go`, `server/internal/stream/manager_test.go`
- Modify: `server/internal/settings/settings.go` (+ test): `streaming.adaptive`
- Modify: `server/internal/config/config.go` (+ test): `DisableMultitrack`, `BOWTIE_MULTITRACK`
- Modify: `server/cmd/bowtie/main.go`, `server/internal/e2e/runners.go` (stub writes `transcode.Layout.ReadyPlaylist()` + `v720_%05d.ts`), `server/internal/e2e/harness.go`

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces:
  - `settings.KeyStreamingAdaptive = "streaming.adaptive"`; `settings.Streaming{BufferMinutes int; Adaptive bool}` (default false, seeded)
  - `config.Config.DisableMultitrack bool`
  - `ManagerDeps.Multitrack bool`, `ManagerDeps.TrackProbeTimeout time.Duration` (0 → 3 s)
  - `Viewer.MaxHeight int` (the negotiated profile height = per-viewer ceiling)
  - `type SessionMedia struct { Dir string; Layout transcode.Layout; MaxHeight int }`
  - `func (m *Manager) SessionMediaOf(viewerID string) (SessionMedia, bool)`
  - `func sessionKey(channelID int64, d transcode.Decision, adaptive bool) string` (`"ch%d|ladder"` / `"ch%d|%s|%s"`)

- [ ] **Step 1: Settings + config tests** — `settings_test.go`: seeded default `Adaptive == false`; `SetStreaming(Streaming{BufferMinutes: 15, Adaptive: true})` round-trips; raw key `streaming.adaptive` = `"true"`. `config_test.go`: `BOWTIE_MULTITRACK=off` → `DisableMultitrack`; `0`/`false` too; unset → false.

- [ ] **Step 2: Manager tests** (append to `manager_test.go`; helper `newMultitrackManager(t, adaptive bool, body ...[]byte)` builds `ManagerDeps{…, Multitrack: true, TrackProbeTimeout: time.Second, Settings: <provider with streaming.adaptive=adaptive>}` with a dial returning `body` then blocking; `probeBody()` = PAT + PMT(eng AC-3, spa AC-3) + a video packet with a 720 sequence header, built with Task 5's helpers):

```go
func TestLadderModeSharesOneSessionAcrossQualities(t *testing.T) {
	m, im, runner := newMultitrackManager(t, true, probeBody()...)
	full := userWithMaxQuality("alice", "")
	capped := userWithMaxQuality("bob", "low")
	h1, err := m.Start(context.Background(), full, testChannelID, transcode.ClientCaps{VideoCodecs: []string{"h264"}})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := m.Start(context.Background(), capped, testChannelID, transcode.ClientCaps{VideoCodecs: []string{"h264"}, Profile: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.Starts() != 1 || h1.SessionID != h2.SessionID {
		t.Fatalf("starts=%d sessions %s %s: one ladder per channel", runner.Starts(), h1.SessionID, h2.SessionID)
	}
	spec := runner.LastSpec()
	if len(spec.Layout.Rungs) != 3 || !spec.Layout.Captions || len(spec.Layout.AC3Tracks()) != 2 || spec.CaptionInput == nil {
		t.Fatalf("layout=%+v", spec.Layout)
	}
	m1, _ := m.SessionMediaOf(h1.ViewerID)
	m2, _ := m.SessionMediaOf(h2.ViewerID)
	if m1.MaxHeight != 1080 || m2.MaxHeight != 480 {
		t.Fatalf("ceilings %d %d", m1.MaxHeight, m2.MaxHeight)
	}
	if n := im.attachCalls.Load(); n != 2 {
		t.Fatalf("attach calls=%d want 2 (video + caption tap)", n)
	}
}

func TestSwitchOffKeysByProfile(t *testing.T) {
	m, _, runner := newMultitrackManager(t, false, probeBody()...)
	caps := transcode.ClientCaps{VideoCodecs: []string{"h264"}}
	if _, err := m.Start(context.Background(), userWithMaxQuality("a", ""), testChannelID, caps); err != nil {
		t.Fatal(err)
	}
	caps.Profile = "low"
	if _, err := m.Start(context.Background(), userWithMaxQuality("b", ""), testChannelID, caps); err != nil {
		t.Fatal(err)
	}
	if runner.Starts() != 2 {
		t.Fatalf("switch off: different profiles are separate sessions; starts=%d", runner.Starts())
	}
	if r := runner.Specs()[0].Layout.Rungs; len(r) != 1 || r[0].Height != 720 {
		t.Fatalf("original on a 720 source must not upscale: %+v", r)
	}
}

func TestAudioModeNoLongerSplitsSessions(t *testing.T) {
	m, _, runner := newMultitrackManager(t, false, probeBody()...)
	ac3 := transcode.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac", "ac3"}}
	aac := transcode.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}
	if _, err := m.Start(context.Background(), userWithMaxQuality("a", ""), testChannelID, ac3); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), userWithMaxQuality("b", ""), testChannelID, aac); err != nil {
		t.Fatal(err)
	}
	if runner.Starts() != 1 {
		t.Fatalf("AC-3 and AAC clients must share; starts=%d", runner.Starts())
	}
}

func TestFallbackToSafeLayout(t *testing.T) {
	m, _, runner := newMultitrackManager(t, true, probeBody()...)
	runner.onStart = func(spec transcode.JobSpec) {
		if !spec.Layout.IsSafe() {
			runner.failNext = true // exits before any playlist, like "-map 0:a:1 matches no streams"
		}
	}
	if _, err := m.Start(context.Background(), userWithMaxQuality("a", ""), testChannelID, transcode.ClientCaps{VideoCodecs: []string{"h264"}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	specs := runner.Specs()
	if len(specs) != 2 || specs[0].Layout.IsSafe() || !specs[1].Layout.IsSafe() || specs[1].CaptionInput != nil {
		t.Fatalf("specs=%+v", specs)
	}
}

func TestCaptionTapForceCloseKeepsFFmpeg(t *testing.T) {
	m, _, runner := newMultitrackManager(t, false, probeBody()...)
	if _, err := m.Start(context.Background(), userWithMaxQuality("a", ""), testChannelID, transcode.ClientCaps{VideoCodecs: []string{"h264"}}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	var tap *IngestSub
	for _, s := range m.sessions {
		tap = s.capSub
	}
	m.mu.Unlock()
	tap.forceClose()
	if runner.LastProc().Stopped() {
		t.Fatal("caption tap force-close stopped FFmpeg")
	}
}

func TestMultitrackOffIsVideoPlusFirstAudio(t *testing.T) {
	st := newTestStore(t)
	m, im, runner := newTestManagerWithDial(st, config.Config{SegmentDir: t.TempDir()}, newFakeClock(), &stubRunner{writeM3U: true}, okDial())
	if _, err := m.Start(context.Background(), viewerUser(), testChannelID, transcode.ClientCaps{VideoCodecs: []string{"h264"}}); err != nil {
		t.Fatal(err)
	}
	if n := im.attachCalls.Load(); n != 1 || !runner.LastSpec().Layout.IsSafe() {
		t.Fatalf("attach=%d layout=%+v", n, runner.LastSpec().Layout)
	}
}
```
Adapt helper names to `manager_test.go` (`userWithMaxQuality` = a `store.User{Username, Role: "viewer", MaxQuality}` seeded in the test store; `stubRunner` gains `failNext`, `Specs()`, `LastSpec()`, `stubProcess.Stopped()`; `writeM3U` writes `spec.Layout.ReadyPlaylist()`). Replace remaining `"live.m3u8"` with the ready playlist.

- [ ] **Step 3: Run** `go test ./internal/stream/ ./internal/settings/ ./internal/config/` — FAIL (unknown fields).

- [ ] **Step 4: Implement.**
  - **settings**: `KeyStreamingAdaptive = "streaming.adaptive"`; `Streaming` gains `Adaptive bool` (read with `strconv.ParseBool`, absent → false); `SetStreaming` writes both keys; seed `{KeyStreamingAdaptive, "false"}`.
  - **config**: `DisableMultitrack bool \`yaml:"disableMultitrack"\``; env `BOWTIE_MULTITRACK` in `off|0|false` sets it; document in `Load`'s comment.
  - **main.go**: `stream.ManagerDeps{…, Multitrack: !cfg.DisableMultitrack}`.
  - **session.go**: fields `layout transcode.Layout`, `capSub *IngestSub`; method `closeSubs()` closing both and nil-ing them; `Viewer.MaxHeight int`; `type SessionMedia struct { Dir string; Layout transcode.Layout; MaxHeight int }`; `ViewerHandle` comment "contains the session's playlists".
  - **manager.go**:

```go
var errPlaylistNotReady = errors.New("playlist not ready")

// playlistNotReadyError keeps FFmpeg's message and matches errPlaylistNotReady.
type playlistNotReadyError struct{ err error }

func (e playlistNotReadyError) Error() string   { return e.err.Error() }
func (e playlistNotReadyError) Unwrap() []error { return []error{e.err, errPlaylistNotReady} }

// sessionKey: one ladder per channel when adaptive; otherwise per codec+profile.
// Audio mode is not part of the key: AAC and AC-3 are renditions of one output.
func sessionKey(channelID int64, d transcode.Decision, adaptive bool) string {
	if adaptive {
		return fmt.Sprintf("ch%d|ladder", channelID)
	}
	return fmt.Sprintf("ch%d|%s|%s", channelID, d.VideoCodec, d.Profile.Name)
}

// layoutFor builds the session's output after the video sub is attached.
func (m *Manager) layoutFor(channelID int64, d transcode.Decision, adaptive, safe bool) transcode.Layout {
	info, ok := ProgramInfo{}, false
	if m.multitrack && !safe {
		info, ok = m.ingest.ProgramInfo(channelID, m.trackProbe)
	}
	src := info.SourceHeight
	l := transcode.Layout{VideoCodec: d.VideoCodec, AudioKbps: d.Profile.AudioKbps}
	if adaptive {
		l.Rungs = transcode.Ladder(src)
		l.AudioKbps = 128
	} else {
		h := d.Profile.Height
		if src > 0 && src < h {
			h = src
		}
		l.Rungs = []transcode.Rung{{Height: h, VideoKbps: d.Profile.VideoKbps}}
	}
	if ok && len(info.Audio) > 0 {
		l.Audio = info.Audio
	}
	if m.multitrack && !safe {
		l.Captions = true
		l.AC3Copy = true
		return l
	}
	return l.Safe()
}

// attachCaptions attaches the caption tap (no OnForceClose: a stalled tap
// stops captions, never video).
func (m *Manager) attachCaptions(ctx context.Context, channelID int64, inputURL string, l transcode.Layout) (*IngestSub, error) {
	if !l.Captions {
		return nil, nil
	}
	return m.ingest.Attach(ctx, channelID, inputURL)
}

// adaptiveForStart reports whether this start uses the shared ladder.
func (m *Manager) adaptiveForStart(d transcode.Decision) (bool, error) {
	if m.settings == nil || d.VideoEncoder == "libx264" {
		return false, nil
	}
	s, err := m.settings.Streaming()
	if err != nil {
		return false, fmt.Errorf("streaming settings: %w", err)
	}
	return s.Adaptive, nil
}
```
  - `Start`: `Negotiate` as today, then `adaptive, err := m.adaptiveForStart(decision)`; if adaptive and `decision.VideoCodec == "hevc"`, negotiate again with `allowHEVC=false` (the ladder is H.264 only); key = `sessionKey(ch.ID, decision, adaptive)`; loop with `safe := !m.multitrack` and the one-shot fallback:

```go
		if !safe && errors.Is(err, errPlaylistNotReady) {
			log.Printf("stream: channel %d: full layout failed (%v); retrying with one rung, first audio, no captions", ch.ID, err)
			safe = true
			attempt--
			continue
		}
```
  - `startAttempt(…, adaptive, safe bool)`: after the video `Attach`: `layout := m.layoutFor(ch.ID, decision, adaptive, safe)`; `capSub, err := m.attachCaptions(…)` (on error log and `layout.Captions = false`); spec gets `Layout: layout` and `CaptionInput: capSub.R` when non-nil; every failure path closes `capSub`; `session{…, layout: layout, capSub: capSub}`; `waitPlaylist(ctx, dir, layout.ReadyPlaylist(), proc)` wrapping failures in `playlistNotReadyError`.
  - `addViewerLocked(sess, username string, maxHeight int)` stores `Viewer.MaxHeight`; callers pass `decision.Profile.Height`.
  - `restartSession`: re-attach the caption tap when `sess.layout.Captions` (on failure run this process without captions); spec uses `sess.layout`; commit `sess.capSub`.
  - Replace the three `sess.sub` close blocks with `sess.closeSubs()`.
  - `SessionMediaOf` returns `SessionMedia{Dir: sess.dir, Layout: sess.layout, MaxHeight: v.MaxHeight}` for a live, non-terminated session.
  - Remove the now-unused `transcode.SessionKey` (and its test) or make it call nothing — delete it.

- [ ] **Step 5: Run** `cd server && go test ./internal/stream/ ./internal/settings/ ./internal/config/ ./internal/e2e/ -count=1 && go vet ./...` — PASS (e2e handler names may need Task 8; if so, ledger and finish there).

- [ ] **Step 6: Commit** `git add server/ && git commit -m "feat(stream): shared ladder sessions, caption tap, per-viewer ceilings, safe fallback"`

---

### Task 7: Admin switch for the shared ladder (API + web)

**Files:**
- Modify: `server/internal/api/settings_handlers.go` (+ test), `docs/api/openapi.yaml`
- Modify: `web/src/admin/settingsModel.ts` (+ test), `web/src/admin/Settings.tsx`

- [ ] **Step 1: Tests.** Server: `GET /api/v1/admin/settings` includes `"streaming":{"bufferMinutes":15,"adaptive":false}`; `PUT` with `{"streaming":{"bufferMinutes":15,"adaptive":true}}` persists (provider `Streaming().Adaptive == true`); omitting `adaptive` in a PUT leaves it unchanged. Web (`settingsModel.test.ts`): form round-trip carries `streaming.adaptive` (boolean) and `toPutBody` includes it.
- [ ] **Step 2: Run** `cd server && go test ./internal/api/ -run Settings` and `cd web && npx vitest run src/admin/settingsModel.test.ts` — FAIL.
- [ ] **Step 3: Implement.** `settingsStreamingJSON` / `putStreamingSection` gain `Adaptive *bool \`json:"adaptive,omitempty"\`` on PUT (nil = keep) and `Adaptive bool \`json:"adaptive"\`` on GET; apply via `settings.KeyStreamingAdaptive`. OpenAPI: add `adaptive: boolean` to the streaming settings schemas with description "One shared multi-quality transcode per channel (more GPU). Off: one stream per quality." Web: `SettingsStreaming.adaptive: boolean`; Settings → Streaming section adds a checkbox "Adaptive quality (one shared stream per channel)" with help text "Every viewer of a channel shares one transcode at 1080/720/480/360 (never above the broadcast), and players pick the quality for their connection. Uses about 1.7× the GPU of one stream. Applies to new sessions." 
- [ ] **Step 4: Run** server and web tests + `npm run lint` — PASS.
- [ ] **Step 5: Commit** `git commit -am "feat(admin): adaptive quality switch"`

---

### Task 8: API — per-viewer master, renditions, segments, WebVTT

**Files:**
- Modify: `server/internal/api/stream_handlers.go`, `server/internal/api/stream_handlers_test.go`
- Modify: `server/internal/testplayer/player.go` (+ test), `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: `MasterPlaylist`, `Layout.CaptionPlaylist/ReadyPlaylist` (Task 2); `SessionMediaOf` (Task 6).
- Produces: `StreamController.SessionMediaOf(viewerID string) (stream.SessionMedia, bool)`.

- [ ] **Step 1: Tests.** Fixture session dir: `v720.m3u8` (`v720_00000.ts`, `v720_00001.ts`), `v480.m3u8`, `v360.m3u8`, `aac0.m3u8` (`aac0_00000.ts`), `ac30.m3u8`, `v720_vtt.m3u8` (`v7200.vtt`), `v7200.vtt` = `"WEBVTT\n\n00:00.000 --> 00:02.000\nHello\n"`, and the `.ts` files. `stubStreams.register` stores `SessionMedia{Dir, Layout: <Ladder(720), eng+spa AC-3, captions, AC3Copy>, MaxHeight: 1080}`; add a `registerCapped(viewerID, dir, 480)`.

```go
func TestIndexIsPerViewerMaster(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeLadderFixture(t, dir)
	full, capped := "aabbccddeeff00112233445566778899", "bbbbccddeeff00112233445566778899"
	ss.register(full, dir)
	ss.registerCapped(capped, dir, 480)
	get := func(v string) string {
		tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
		rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/index.m3u8?token="+tok, nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
		}
		return rr.Body.String()
	}
	if body := get(full); !strings.Contains(body, "v720.m3u8?token=") || !strings.Contains(body, `GROUP-ID="ac3"`) || !strings.Contains(body, `SUBTITLES="subs"`) {
		t.Fatalf("full master:\n%s", body)
	}
	if body := get(capped); strings.Contains(body, "\nv720.m3u8") || !strings.Contains(body, "\nv480.m3u8") {
		t.Fatalf("capped master must omit 720:\n%s", body)
	}
	_ = os.Remove(filepath.Join(dir, "v720_vtt.m3u8"))
	if strings.Contains(get(full), "SUBTITLES") {
		t.Fatal("captions listed before the caption playlist exists")
	}
}

func TestRenditionPlaylistsTouchAndRewrite(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeLadderFixture(t, dir)
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	for name, seg := range map[string]string{"v720.m3u8": "v720_00000.ts", "aac0.m3u8": "aac0_00000.ts", "v720_vtt.m3u8": "v7200.vtt"} {
		rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/"+name+"?token="+tok, nil, nil)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/api/v1/stream/"+v+"/"+seg+"?token="+tok) {
			t.Fatalf("%s: %d\n%s", name, rr.Code, rr.Body.String())
		}
	}
	ss.mu.Lock()
	n := len(ss.touchCalls)
	ss.mu.Unlock()
	if n != 3 {
		t.Fatalf("every rendition playlist fetch must Touch; touches=%d", n)
	}
}

func TestVTTSegmentGetsTimestampMap(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeLadderFixture(t, dir)
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/v7200.vtt?token="+tok, nil, nil)
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/vtt") {
		t.Fatalf("status=%d ct=%q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(rr.Body.String(), "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

func TestStreamFileNamesValidated(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, t.TempDir())
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	for _, bad := range []string{"seg00000.ts", "live.m3u8", "x.m3u8", "v72_00000.ts", "aac9.m3u8", "v720.vtt", "..%2Fv720.m3u8"} {
		if rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/"+bad+"?token="+tok, nil, nil); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d want 400", bad, rr.Code)
		}
	}
}
```
Update `TestSegmentNameTraversal400` and the e2e stub runner in this file (`v720_00000.ts`, ready playlist). testplayer: `TestPlayerFollowsMasterPlaylist` — server serves `/s/index.m3u8` = `"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv720.m3u8?token=t\n"`, `/s/v720.m3u8` with one segment `/s/v720_00000.ts` (bytes `0x47,0,0,0`); two `poll`s → `SegmentsFetched == 1`.

- [ ] **Step 2: Run** `go test ./internal/api/ ./internal/testplayer/` — FAIL.

- [ ] **Step 3: Implement.**

```go
var (
	segmentNameRe   = regexp.MustCompile(`^(v\d{3,4}|aac[0-2]|ac3[0-2])_\d{5}\.ts$`)
	captionNameRe   = regexp.MustCompile(`^v\d{3,4}\d+\.vtt$`)
	renditionNameRe = regexp.MustCompile(`^(v\d{3,4}|v\d{3,4}_vtt|aac[0-2]|ac3[0-2])\.m3u8$`)
)

// vttTimestampMap aligns FFmpeg's WebVTT cue times (start at 0) with the
// video's MPEG-TS clock (the mpegts muxer starts at 1.4 s).
const vttTimestampMap = "X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000"

// withTimestampMap inserts vttTimestampMap after the WEBVTT line unless present.
func withTimestampMap(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("WEBVTT")) || bytes.Contains(b, []byte("X-TIMESTAMP-MAP")) {
		return b
	}
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return append(append(b, '\n'), vttTimestampMap+"\n"...)
	}
	out := make([]byte, 0, len(b)+len(vttTimestampMap)+1)
	out = append(out, b[:i+1]...)
	out = append(out, vttTimestampMap...)
	out = append(out, '\n')
	return append(out, b[i+1:]...)
}
```
(`captionNameRe` matches `v7200.vtt`, `v72013.vtt`; use the Task 1 constant if it ruled a different offset.)
  - `StreamController` gains `SessionMediaOf`.
  - `handlePlaylist` (index.m3u8): Touch → `SessionMediaOf` (404 if missing) → 404 "playlist not ready" if `Layout.ReadyPlaylist()` is missing → `captionsReady := Layout.Captions && exists(CaptionPlaylist())` → `transcode.MasterPlaylist(media.Layout, media.MaxHeight, "?token="+url.QueryEscape(token), captionsReady)`; headers as today.
  - `handleSegment`: validate the name against the three regexes first (400 otherwise), verify access, then dispatch: rendition → Touch + read + `rewritePlaylist` + playlist headers; caption → read + `withTimestampMap` + `Content-Type: text/vtt; charset=utf-8`, `Cache-Control: no-store`; segment → `ServeContent` as today.
  - `rewritePlaylist` rewrites lines matching `segmentNameRe` or `captionNameRe`.
  - testplayer `poll`: when the body contains `#EXT-X-STREAM-INF`, take the first non-comment line, resolve against the directory of `p.playlistURL` (unless absolute), set `p.playlistURL`, return false.
  - OpenAPI: `index.m3u8` = per-viewer master; `{segment}` documents `v<height>.m3u8`, `v<height>_vtt.m3u8`, `aac<i>.m3u8`, `ac3<i>.m3u8`, `*_NNNNN.ts`, `v<height>N.vtt`; `info.version: 0.8.0`.

- [ ] **Step 4: Run** `cd server && go test ./... -count=1 && go vet ./... && golangci-lint run ./...` (check each exit code; never pipe through `tail`) — PASS.
- [ ] **Step 5: Commit** `git add server/ docs/api/openapi.yaml && git commit -m "feat(api): per-viewer master, rendition playlists and timestamped WebVTT"`

---

### Task 9: Real end-to-end on this Mac (9.1, VideoToolbox)

**Files:** none (verification); any fix found gets a failing test first.

- [ ] **Step 1:** Build and run the branch server on :8400 (`run_in_background`, timeout 7200000): `cd server && go build -o $S/bowtie ./cmd/bowtie && BOWTIE_DEVICES=192.168.50.32 BOWTIE_LISTEN_ADDR=:8400 $S/bowtie -data-dir $S/bowtie-data`.
- [ ] **Step 2 (switch off):** log in as admin, `POST /api/v1/sessions {"channelId": <9.1>, "caps":{"videoCodecs":["h264"],"audioCodecs":["aac","ac3"]}}`; `mediasel http://127.0.0.1:8400<playlistUrl>` → status=1, audible English/Spanish (+5.1), legible English; one `v720*.vtt` via curl starts with the timestamp map and has readable text.
- [ ] **Step 3 (switch on):** `PUT /api/v1/admin/settings {"streaming":{"bufferMinutes":15,"adaptive":true}}`; start two sessions on 9.1 (admin with no cap; a viewer created with `maxQuality: "low"`); `GET /api/v1/admin/sessions` shows ONE session with two viewers; the viewer's master lists only 480/360; mediasel status=1 on both.
- [ ] **Step 4:** Restart continuity: `pkill -f 'ffmpeg.*v720'` once; within 10 s every playlist continues with one `#EXT-X-DISCONTINUITY`; mediasel still 1.
- [ ] **Step 5:** Delete sessions; confirm the tuner is released; set `adaptive` back to false. Ledger results.

---

### Task 10: Web — audio picker, CC toggle, playing quality

Carry over (read it with `git show origin/docs/multitrack-hls:docs/superpowers/plans/2026-10-03-multitrack-hls.md`) the approved multitrack plan's web task (`web/src/player/tracksModel.ts` with `TrackPrefs`, `loadTrackPrefs`, `saveTrackPrefs`, `pickAudioIndex`, `audioTrackLabel` and its tests verbatim; `Player.tsx` wiring for `AUDIO_TRACKS_UPDATED`, `AUDIO_TRACK_SWITCHED`, `SUBTITLE_TRACKS_UPDATED`, `hls.subtitleDisplay = true`, audio `<select>` and CC toggle button, prefs restore). Additionally the stats overlay shows the playing rung: on `Hls.Events.LEVEL_SWITCHED`, `setPlayingHeight(hls.levels[data.level]?.height ?? null)` and render "Quality: 720p" in the stats panel.

**Files:** Create `web/src/player/tracksModel.ts`, `web/src/player/tracksModel.test.ts`; Modify `web/src/player/Player.tsx`, `web/src/player/Player.module.css`.

- [ ] **Step 1: Tests** — `tracksModel.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { audioTrackLabel, loadTrackPrefs, pickAudioIndex, saveTrackPrefs } from './tracksModel'

function memStorage(): Storage {
  const m = new Map<string, string>()
  return {
    get length() { return m.size },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, v),
  }
}

describe('tracksModel', () => {
  it('picks the preferred language by primary subtag', () => {
    const tracks = [{ lang: 'en' }, { lang: 'es' }]
    expect(pickAudioIndex(tracks, 'es')).toBe(1)
    expect(pickAudioIndex(tracks, 'es-MX')).toBe(1)
    expect(pickAudioIndex(tracks, 'fr')).toBe(-1)
    expect(pickAudioIndex(tracks, null)).toBe(-1)
  })
  it('labels tracks by name, then language, then number', () => {
    expect(audioTrackLabel({ name: 'Español', lang: 'es' }, 1)).toBe('Español')
    expect(audioTrackLabel({ lang: 'es' }, 1)).toBe('es')
    expect(audioTrackLabel({}, 1)).toBe('Audio 2')
  })
  it('round-trips prefs and survives broken storage', () => {
    const s = memStorage()
    expect(loadTrackPrefs(s)).toEqual({ audioLang: null, captions: null })
    saveTrackPrefs({ audioLang: 'es', captions: true }, s)
    expect(loadTrackPrefs(s)).toEqual({ audioLang: 'es', captions: true })
    s.setItem('bowtie.trackPrefs', '{bad json')
    expect(loadTrackPrefs(s)).toEqual({ audioLang: null, captions: null })
    const throwing = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } } as unknown as Storage
    expect(loadTrackPrefs(throwing)).toEqual({ audioLang: null, captions: null })
    expect(() => saveTrackPrefs({ audioLang: 'en', captions: false }, throwing)).not.toThrow()
  })
})
```
- [ ] **Step 2: Run** `cd web && npx vitest run src/player/tracksModel.test.ts` — FAIL.
- [ ] **Step 3: Implement `tracksModel.ts`:**

```ts
export type TrackPrefs = { audioLang: string | null; captions: boolean | null }

const KEY = 'bowtie.trackPrefs'
const EMPTY: TrackPrefs = { audioLang: null, captions: null }

function defaultStorage(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

export function loadTrackPrefs(storage: Storage | undefined = defaultStorage()): TrackPrefs {
  try {
    const raw = storage?.getItem(KEY)
    if (!raw) return { ...EMPTY }
    const p = JSON.parse(raw) as Partial<TrackPrefs>
    return {
      audioLang: typeof p.audioLang === 'string' ? p.audioLang : null,
      captions: typeof p.captions === 'boolean' ? p.captions : null,
    }
  } catch {
    return { ...EMPTY }
  }
}

export function saveTrackPrefs(p: TrackPrefs, storage: Storage | undefined = defaultStorage()): void {
  try {
    storage?.setItem(KEY, JSON.stringify(p))
  } catch {
    /* private mode / quota: the preference just isn't remembered */
  }
}

const primary = (tag: string) => tag.toLowerCase().split('-')[0]

/** Index of the track matching the preferred language, or -1 to keep the default. */
export function pickAudioIndex(tracks: { lang?: string }[], preferred: string | null): number {
  if (!preferred) return -1
  const want = primary(preferred)
  return tracks.findIndex((t) => t.lang !== undefined && primary(t.lang) === want)
}

export function audioTrackLabel(t: { name?: string; lang?: string }, i: number): string {
  return t.name || t.lang || `Audio ${i + 1}`
}
```
- [ ] **Step 4: Wire `Player.tsx`** (hls.js branch after `attachMedia`):

```ts
      hls.subtitleDisplay = true
      hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, (_e, data) => {
        const tracks = data.audioTracks.map((t) => ({ name: t.name, lang: t.lang }))
        setAudioTracks(tracks)
        const want = pickAudioIndex(tracks, loadTrackPrefs().audioLang)
        if (want >= 0 && want !== hls.audioTrack) hls.audioTrack = want
        setAudioIdx(hls.audioTrack)
      })
      hls.on(Hls.Events.AUDIO_TRACK_SWITCHED, (_e, data) => setAudioIdx(data.id))
      hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, (_e, data) => {
        const any = data.subtitleTracks.length > 0
        setHasCaptions(any)
        const on = any && loadTrackPrefs().captions === true
        hls.subtitleTrack = on ? 0 : -1
        setCaptionsOn(on)
      })
      hls.on(Hls.Events.LEVEL_SWITCHED, (_e, data) => setPlayingHeight(hls.levels[data.level]?.height ?? null))
```
handlers `onPickAudio(i)` (set `hls.audioTrack`, save `audioLang`) and `onToggleCaptions()` (toggle `hls.subtitleTrack` 0/−1, save `captions`); reset state in `destroyHls`; controls next to Quality: audio `<select aria-label="Audio track">` when `audioTracks.length > 1`, `CC` button `aria-pressed` when `hasCaptions`; stats panel line `Quality: {playingHeight}p` when known.
- [ ] **Step 5: Run** `cd web && npm test && npm run lint && npm run build` — PASS.
- [ ] **Step 6: Browser check** (Task 9 server, switch on): Playwright plays 9.1 — audio select lists English/Español, choosing Español requests `aac1.m3u8`; CC requests `v720_vtt.m3u8` and a cue is visible in a screenshot; stats show a quality; reload restores choices.
- [ ] **Step 7: Commit** `git add web/src/player/ && git commit -m "feat(web): audio track picker, captions toggle, playing quality"`

---

### Task 11: Quality picker means "at most" — copy and session reuse

The server already treats the picker as a ceiling (Task 6). Update the picker copy so it reads right under the ladder, without changing behavior.

**Files:** `web/src/player/QualitySheet.tsx`, `ios/App/iOS/PlayerView.swift` (`qualityMenu`), `ios/App/tvOS/TVPlayerView.swift` (`TVQualityPanel`), `android/app/src/main/kotlin/app/bowtie/ui/PlayerScreen.kt` (quality sheet), Android TV drawer.

- [ ] **Step 1:** In each picker, rename "Auto" to "Auto (best for your connection)" and add the subtitle "Higher choices limit, not force, quality" under the list. Web test: `QualitySheet` renders the subtitle (`getByText(/limit, not force/)`).
- [ ] **Step 2:** `cd web && npm test`; iOS/tvOS build; Android `assembleDebug` — PASS.
- [ ] **Step 3: Commit** `git commit -am "feat(clients): quality picker reads as a ceiling"`

---

### Task 12: iOS / iPadOS / Apple TV — remember audio and captions

Carry over (read it with `git show origin/docs/multitrack-hls:docs/superpowers/plans/2026-10-03-multitrack-hls.md`) the approved multitrack plan's Task 9 verbatim: `ios/BowtieKit/Sources/BowtieKit/MediaPrefs.swift` (`MediaPrefs` Codable with optional `audioLanguage`/`captionsOn`, `load(from:)`, `save(to:)`, `pickIndex(languages:preferred:)`) with `MediaPrefsTests`; `PlayerModel` applies prefs on `.readyToPlay` via `loadMediaSelectionGroup(for: .audible/.legible)` and records changes from `AVPlayerItem.mediaSelectionDidChangeNotification` **only after its own apply ran** (`mediaPrefsApplied`). 5.1: no code — AVPlayer selects the AC-3 variants on surround-capable routes; verify in Task 9's style with the tvOS Simulator only by build (no Apple TV hardware).

- [ ] **Step 1: Tests** (`MediaPrefsTests.swift`):

```swift
import XCTest
@testable import BowtieKit

final class MediaPrefsTests: XCTestCase {
    func testPickIndexMatchesPrimarySubtag() {
        XCTAssertEqual(MediaPrefs.pickIndex(languages: ["en", "es"], preferred: "es"), 1)
        XCTAssertEqual(MediaPrefs.pickIndex(languages: ["en-US", "es-MX"], preferred: "es"), 1)
        XCTAssertNil(MediaPrefs.pickIndex(languages: ["en", nil], preferred: "fr"))
        XCTAssertNil(MediaPrefs.pickIndex(languages: ["en"], preferred: nil))
    }

    func testRoundTripAndDefaults() {
        let d = UserDefaults(suiteName: "MediaPrefsTests")!
        d.removePersistentDomain(forName: "MediaPrefsTests")
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs())
        MediaPrefs(audioLanguage: "es", captionsOn: true).save(to: d)
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs(audioLanguage: "es", captionsOn: true))
        d.set(Data("junk".utf8), forKey: MediaPrefs.defaultsKey)
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs())
    }
}
```
- [ ] **Step 2: Run** `cd ios/BowtieKit && swift test --filter MediaPrefsTests` — FAIL.
- [ ] **Step 3: Implement** `MediaPrefs.swift`:

```swift
import Foundation

/// Remembered audio language and captions choice; nil = never chosen (leave
/// the player's and the system accessibility defaults alone).
public struct MediaPrefs: Codable, Equatable, Sendable {
    public var audioLanguage: String?
    public var captionsOn: Bool?

    public init(audioLanguage: String? = nil, captionsOn: Bool? = nil) {
        self.audioLanguage = audioLanguage
        self.captionsOn = captionsOn
    }

    public static let defaultsKey = "bowtie.mediaPrefs"

    public static func load(from defaults: UserDefaults = .standard) -> MediaPrefs {
        guard let data = defaults.data(forKey: defaultsKey),
              let prefs = try? JSONDecoder().decode(MediaPrefs.self, from: data) else {
            return MediaPrefs()
        }
        return prefs
    }

    public func save(to defaults: UserDefaults = .standard) {
        if let data = try? JSONEncoder().encode(self) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }

    public static func pickIndex(languages: [String?], preferred: String?) -> Int? {
        guard let preferred else { return nil }
        let want = primary(preferred)
        return languages.firstIndex { $0.map(primary) == want }
    }

    private static func primary(_ tag: String) -> String {
        tag.lowercased().split(separator: "-").first.map(String.init) ?? tag.lowercased()
    }
}
```
- [ ] **Step 4: Wire `PlayerModel`/`PlayerBridge`** (iOS `PlayerView.swift` bridge and tvOS `TVPlayerView.swift` bridge — both own the `AVPlayerItem`): on `.readyToPlay` run `applyMediaPrefs(to:)`; observe `mediaSelectionDidChangeNotification` → `recordMediaSelection(_:)` guarded by `mediaPrefsApplied` (code as in the multitrack plan's Task 9 Step 5).
- [ ] **Step 5:** `swift test`; iOS + tvOS builds; extend `PlaybackUITests` with `testAudioAndCaptionChoicesPersist` (Task 9 server): open AVKit's audio/subtitles menu, pick Spanish + English CC, relaunch, play, assert the subtitle text is on screen (screenshot) — PASS.
- [ ] **Step 6: Commit** `git commit -am "feat(ios,tvos): remember audio language and captions choice"`

---

### Task 13: Android / Fire TV — audio (incl. 5.1) and captions

Carry over (read it with `git show origin/docs/multitrack-hls:docs/superpowers/plans/2026-10-03-multitrack-hls.md`) the approved multitrack plan's Task 10 verbatim (`TrackPrefs` encode/decode + `audioLabel` with `TrackPrefsTest`; `PlayerEngine.audioOptions/selectAudio/setCaptions/hasCaptions/captionsOn` via `TrackSelectionParameters`; phone chips "Audio" and "CC"; TV drawer rows; SharedPreferences `bowtie.player`/`trackPrefs`). Media3 picks the AC-3 variants only when the device reports AC-3 passthrough — no extra code.

- [ ] **Steps 1–6** exactly as in that task (test → fail → implement → pass → UI → `./gradlew :core:testDebugUnitTest :app:assembleDebug :tv:assembleDebug`), commit `feat(android,firetv): audio track and captions controls`.

---

### Task 14: Roku — ask, then verify

- [ ] **Step 1:** Grep `roku/components/PlayerScene.bs` `onKeyEvent` for `"options"` — today `*` opens the quality dialog; change it to return `false` (so the system overlay with Audio / Closed captioning opens) and keep Right for quality. Static test in `roku/test/player-keys.test.mjs`: `options` is not handled by the app.
- [ ] **Step 2:** `cd roku && npm test && npm run lint` — PASS. Commit `fix(roku): let * reach the system audio/captions menu`.
- [ ] **Step 3:** **Ask the user** before touching 192.168.50.65. With permission: sideload, play 9.1 ≤1 min, `*` → Audio lists English/Español; captions toggle; return the TV to what it was playing.

---

### Task 15: TrueNAS check script, docs, release

**Files:** Create `docs/deploy/adaptive-quality-check.md`; Modify `CHANGELOG.md`, `README.md`, `docs/dev/fake-hdhomerun.md` (names).

- [ ] **Step 1:** `adaptive-quality-check.md`: copy-paste steps for the user on TrueNAS — turn on Admin → Settings → Adaptive quality; start 9.1 on a phone; `sudo intel_gpu_top -l` for 30 s (expect Render/3D and Video busy < 80 %); start two more channels; `docker logs bowtie 2>&1 | grep -E 'full layout failed|ffmpeg exited'` must be empty; force a restart (`docker exec bowtie pkill -f ffmpeg`) and confirm playback resumes; turn it off again if any step fails and send the log lines.
- [ ] **Step 2:** CHANGELOG `## [0.8.0]`: captions, alternate audio, 5.1 option, adaptive quality switch (off by default), shared rewind across accounts on the same quality (all accounts when the switch is on), Live/rewind fixes already listed under Unreleased; README env `BOWTIE_MULTITRACK`.
- [ ] **Step 3:** All suites: `cd server && go test ./... && golangci-lint run ./...`; `cd web && npm test && npm run lint`; `cd ios/BowtieKit && swift test`; `cd android && ./gradlew :core:testDebugUnitTest`; `cd roku && npm test` — all exit 0.
- [ ] **Step 4:** Commit, push, PR, wait for green (`gh pr checks --json bucket`), merge, `git fetch --tags`, abort if `v0.8.0` exists or the merge SHA is empty, tag `v0.8.0`, push, confirm the ghcr image builds; tell the user to pull 0.8.0 and run the check script.
