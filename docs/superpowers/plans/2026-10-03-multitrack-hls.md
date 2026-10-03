# Multi-track HLS (captions + alternate audio) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every Bowtie session serves an HLS master playlist with the broadcast's extra audio tracks (e.g. Spanish) as audio renditions and its CEA-608 captions as a WebVTT rendition, and each app lets the viewer pick audio and turn captions on.

**Architecture:** FFmpeg gets a second input on fd 3 (a second ingest subscriber of the same channel) read by `lavfi movie=…[out0+subcc]` for captions, and writes per-rendition playlists via `-var_stream_map`. Bowtie parses the channel's PMT to know the audio tracks, writes the master playlist itself, serves the rendition playlists/segments with the existing token rewrite, and inserts `X-TIMESTAMP-MAP` into WebVTT segments. A start that fails with the multi-track layout retries once with video + first audio only.

**Tech Stack:** Go 1.23 server (`server/`), FFmpeg 5.1.9 (image) / 8.x (Mac dev), hls.js web (`web/`), AVKit (`ios/`), Media3 (`android/`), Roku SceneGraph (`roku/`).

**Spec:** `docs/superpowers/specs/2026-10-03-multitrack-hls-design.md`

## Global Constraints

- Captions are a WebVTT rendition for every encoder; when it exists, in-band A53 captions are disabled (`-a53cc 0`) on libx264, h264_qsv and h264_nvenc (VideoToolbox already sets it).
- Each extra audio track is its own audio-only rendition in group `aud`; at most 3 audio tracks total (`MaxAudioTracks = 3`).
- Variant/file names: variants `main`, `aud1`, `aud2`; playlists `main.m3u8`, `aud1.m3u8`, `main_vtt.m3u8`; segments `main_%05d.ts`, `aud1_%05d.ts`, `main%d.vtt`.
- `GET /api/v1/stream/{viewerId}/index.m3u8` (the existing `playlistUrl`) returns the Bowtie-written master playlist; FFmpeg's `-master_pl_name` is never used.
- Caption tap input arg is exactly `movie='pipe\:3'[out0+subcc]` (Go string `"movie='pipe\\:3'[out0+subcc]"`).
- VTT header inserted when missing: `X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000`.
- Restarts keep `append_list+discont_start+omit_endlist`.
- Language names: eng → English, spa → Español, fra/fre → Français; unknown → the code; audio-description track → "Described video".
- Last audio language and captions on/off are remembered per device.
- Family TV etiquette for live tests: one tuner, channel 9.1, ≤2 minutes per run, stop sessions afterwards.

## Review Focus

1. A channel whose PMT lists an audio PID FFmpeg never sees (`-map 0:a:1` matches nothing) — the session must still start (fallback layout), not fail. Pinned in Task 5.
2. Viewers watching only via rendition playlists (players fetch `index.m3u8` once) must not be reaped as idle — rendition playlist fetches must `Touch`. Pinned in Task 6.
3. A caption tap that stalls or dies (fd 3 not read) must not stop video — the caption sub has no `OnForceClose` hook. Pinned in Task 5.
4. Saved audio preference applied before the player's own default selection fires must not be overwritten by that default (iOS notification ordering). Pinned in Task 9.
5. Duplicate rendition NAMEs in one group (two `eng` tracks) are invalid HLS — names are de-duplicated. Pinned in Task 2.

---

### Task 1: Live feasibility check (throwaway, no repo code)

Resolves spec risks 1–3 before building. Output: rulings in the ledger.

**Files:** none in the repo; scratch dir `$S=/private/tmp/claude-501/-Users-ajthom90-projects-antenna-helper/bd71e177-69ab-5021-b7a6-90d4792a621f/scratchpad/exp/live`.

- [ ] **Step 1: Run the two-input command live against 9.1 for 60 s** (the HDHomeRun HTTP stream on stdin and fd 3 via `tee` into a FIFO):

```bash
S=/private/tmp/claude-501/-Users-ajthom90-projects-antenna-helper/bd71e177-69ab-5021-b7a6-90d4792a621f/scratchpad/exp/live
mkdir -p $S/out && cd $S && rm -f out/* cap.fifo && mkfifo cap.fifo
timeout 60 curl -s http://192.168.50.32:5004/auto/v9.1 | tee cap.fifo | \
  ffmpeg -hide_banner -loglevel warning -nostats \
   -fflags +discardcorrupt -i pipe:0 -f lavfi -i "movie='pipe\:3'[out0+subcc]" \
   -map 0:v:0 -map 0:a:0 -map 0:a:1 -map 1:s:0 \
   -vf yadif=0:-1:0,scale=-2:720 -c:v libx264 -preset veryfast -a53cc 0 -g 120 -force_key_frames 'expr:gte(t,n_forced*4)' \
   -c:a aac -ac 2 -b:a 128k -c:s webvtt \
   -f hls -hls_time 4 -hls_list_size 30 -hls_flags delete_segments+temp_file+omit_endlist -hls_segment_type mpegts \
   -hls_segment_filename out/%v_%05d.ts -var_stream_map "v:0,a:0,s:0,agroup:aud,sgroup:subs,name:main a:1,agroup:aud,name:aud1" \
   out/%v.m3u8 3<cap.fifo
ls out | head; tail -3 out/main.m3u8 out/aud1.m3u8 out/main_vtt.m3u8
```
Expected: `main.m3u8`, `aud1.m3u8`, `main_vtt.m3u8`, ~14 `main_*.ts`, `main*.vtt` files. If FFmpeg stalls (no segments after 10 s) the fd-3 pipe is not being drained at the video's pace: ledger a ruling and stop to rethink (caption sub buffering).

- [ ] **Step 2: Restart continuity (spec risk 3).** Re-run Step 1 for 20 s with `-hls_flags delete_segments+temp_file+omit_endlist+append_list+discont_start` into the same `out/`, then:

```bash
for p in main aud1 main_vtt; do echo "== $p"; grep -E "MEDIA-SEQUENCE|DISCONTINUITY" out/$p.m3u8; grep -v '^#' out/$p.m3u8 | tail -2; done
```
Expected per playlist: segment numbers continue past the first run's last number and an `#EXT-X-DISCONTINUITY` precedes the first new segment. If `main_vtt.m3u8` restarts at 0, ledger `Ruling:` and in Task 6 serve the caption playlist only when its sequence is consistent (or drop captions after a restart) — decide with the evidence.

- [ ] **Step 3: Caption text quality (spec risk 2).** `cat out/main1.vtt out/main2.vtt`. If cues contain control garbage (`H@`, stray letters), repeat Step 1 for 30 s with each decoder option placed before the lavfi `-i`: `-real_time 1`, `-data_field first`. Keep the first variant that produces clean text. Ledger: `Task 1: Ruling: cc_dec options = <…>`. Task 3 adds them to `BuildArgs`.

- [ ] **Step 4: X-TIMESTAMP-MAP constant.**

```bash
ffprobe -v error -select_streams v -show_entries packet=pts -of csv=p=0 out/main_00000.ts | head -1
head -4 out/main0.vtt
```
Expected: first video PTS ≈ 126000–128000 (1.4 s + first frame). If it is not ~126000, ledger the measured offset and use it as the constant in Task 6.

- [ ] **Step 5: AVPlayer and hls.js on the live output.** Write `out/index.m3u8` (master per spec §4 without tokens and without `CODECS`), serve `out/` with `python3 -m http.server 8713` (background) and run the AVPlayer probe:

```bash
cat > $S/out/index.m3u8 <<'EOF'
#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Español",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,URI="aud1.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="main_vtt.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,AUDIO="aud",SUBTITLES="subs"
main.m3u8
EOF
/private/tmp/claude-501/-Users-ajthom90-projects-antenna-helper/bd71e177-69ab-5021-b7a6-90d4792a621f/scratchpad/mediasel http://127.0.0.1:8713/index.m3u8
```
Expected: `status=1`, audible `["English[en]", "Spanish[es]"]`, legible `["English[en]"]`. If status≠1 without `CODECS`, add `CODECS="avc1.640028,mp4a.40.2"` and retry; ledger whichever works (Task 2 writes it). Kill the http server afterwards (`pkill -f "http.server 8713"`).

- [ ] **Step 6: Ledger the five results** (`Task 1: Ruling: …` lines). No commit (nothing in the repo changed).

---

### Task 2: `transcode.Layout`, language names, master playlist builder

**Files:**
- Create: `server/internal/transcode/layout.go`
- Test: `server/internal/transcode/layout_test.go`

**Interfaces:**
- Produces:
  - `type AudioTrack struct { Lang string; Described bool }`
  - `type Layout struct { Audio []AudioTrack; Captions bool }`
  - `const MaxAudioTracks = 3`, `const MainPlaylist = "main.m3u8"`, `const CaptionPlaylist = "main_vtt.m3u8"`
  - `func (l Layout) AudioCount() int` (1..3; 1 when `Audio` is empty)
  - `func (l Layout) Safe() Layout` (first audio only, no captions)
  - `func (l Layout) IsSafe() bool`
  - `func (l Layout) VarStreamMap() string`
  - `func (l Layout) AudioNames() []string` (len == AudioCount, unique)
  - `func (t AudioTrack) BCP47() string`
  - `func MasterPlaylist(l Layout, d Decision, query string, captionsReady bool) string`

- [ ] **Step 1: Write the failing tests**

```go
package transcode_test

import (
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestLayoutAudioCountAndSafe(t *testing.T) {
	cases := []struct {
		l    transcode.Layout
		want int
	}{
		{transcode.Layout{}, 1},
		{transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}}}, 1},
		{transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "spa"}}}, 2},
		{transcode.Layout{Audio: make([]transcode.AudioTrack, 5)}, 3},
	}
	for _, c := range cases {
		if got := c.l.AudioCount(); got != c.want {
			t.Errorf("AudioCount(%+v)=%d want %d", c.l, got, c.want)
		}
	}
	full := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "spa"}}, Captions: true}
	if full.IsSafe() {
		t.Fatal("full layout reported safe")
	}
	s := full.Safe()
	if !s.IsSafe() || s.AudioCount() != 1 || s.Captions || s.Audio[0].Lang != "eng" {
		t.Fatalf("Safe()=%+v", s)
	}
	if !(transcode.Layout{}).IsSafe() {
		t.Fatal("zero layout not safe")
	}
}

func TestVarStreamMap(t *testing.T) {
	cases := []struct {
		l    transcode.Layout
		want string
	}{
		{transcode.Layout{}, "v:0,a:0,name:main"},
		{transcode.Layout{Captions: true}, "v:0,a:0,s:0,sgroup:subs,name:main"},
		{transcode.Layout{Audio: []transcode.AudioTrack{{}, {}}}, "v:0,a:0,agroup:aud,name:main a:1,agroup:aud,name:aud1"},
		{transcode.Layout{Audio: []transcode.AudioTrack{{}, {}, {}}, Captions: true},
			"v:0,a:0,s:0,agroup:aud,sgroup:subs,name:main a:1,agroup:aud,name:aud1 a:2,agroup:aud,name:aud2"},
	}
	for _, c := range cases {
		if got := c.l.VarStreamMap(); got != c.want {
			t.Errorf("VarStreamMap(%+v)=%q want %q", c.l, got, c.want)
		}
	}
}

func TestAudioNamesUniqueAndLocalized(t *testing.T) {
	l := transcode.Layout{Audio: []transcode.AudioTrack{
		{Lang: "eng"}, {Lang: "eng"}, {Lang: "spa"},
	}}
	got := l.AudioNames()
	want := []string{"English", "English 2", "Español"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("AudioNames=%q want %q", got, want)
	}
	l2 := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "eng", Described: true}, {Lang: "xyz"}}}
	if got := strings.Join(l2.AudioNames(), "|"); got != "English|Described video|xyz" {
		t.Fatalf("AudioNames=%q", got)
	}
	if got := strings.Join((transcode.Layout{}).AudioNames(), "|"); got != "Audio" {
		t.Fatalf("zero AudioNames=%q", got)
	}
}

func TestBCP47(t *testing.T) {
	for in, want := range map[string]string{"eng": "en", "spa": "es", "fre": "fr", "fra": "fr", "xyz": "xyz", "": ""} {
		if got := (transcode.AudioTrack{Lang: in}).BCP47(); got != want {
			t.Errorf("BCP47(%q)=%q want %q", in, got, want)
		}
	}
}

func testDecision() transcode.Decision {
	return transcode.Decision{
		VideoCodec: "h264", VideoEncoder: "h264_qsv",
		Profile: transcode.Profile{Name: "high", Height: 720, VideoKbps: 4000, AudioKbps: 128},
		Backend: transcode.BackendQSV,
	}
}

func TestMasterPlaylistFull(t *testing.T) {
	l := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "spa"}}, Captions: true}
	got := transcode.MasterPlaylist(l, testDecision(), "?token=T", true)
	want := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES`,
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Español",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,URI="aud1.m3u8?token=T"`,
		`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="main_vtt.m3u8?token=T"`,
		`#EXT-X-STREAM-INF:BANDWIDTH=5056000,RESOLUTION=1280x720,AUDIO="aud",SUBTITLES="subs"`,
		"main.m3u8?token=T",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("master:\n%s\nwant:\n%s", got, want)
	}
}

func TestMasterPlaylistSafeAndCaptionsNotReady(t *testing.T) {
	l := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}}, Captions: true}
	got := transcode.MasterPlaylist(l, testDecision(), "?token=T", false)
	if strings.Contains(got, "TYPE=AUDIO") || strings.Contains(got, "SUBTITLES") {
		t.Fatalf("unexpected renditions:\n%s", got)
	}
	if !strings.Contains(got, "#EXT-X-STREAM-INF:BANDWIDTH=4928000,RESOLUTION=1280x720\nmain.m3u8?token=T\n") {
		t.Fatalf("variant line wrong:\n%s", got)
	}
}

func TestMasterPlaylistDescribedCharacteristics(t *testing.T) {
	l := transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "eng", Described: true}}}
	got := transcode.MasterPlaylist(l, testDecision(), "", false)
	if !strings.Contains(got, `NAME="Described video",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="aud1.m3u8",CHARACTERISTICS="public.accessibility.describes-video"`) {
		t.Fatalf("described rendition wrong:\n%s", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd server && go test ./internal/transcode/ -run 'Layout|VarStreamMap|AudioNames|BCP47|MasterPlaylist'`
Expected: FAIL — `undefined: transcode.Layout` (build error).

- [ ] **Step 3: Implement `layout.go`**

```go
package transcode

import (
	"fmt"
	"strings"
)

// HLS file names Bowtie relies on (FFmpeg derives them from the variant names
// in Layout.VarStreamMap).
const (
	MainPlaylist    = "main.m3u8"
	CaptionPlaylist = "main_vtt.m3u8"
	// MaxAudioTracks caps renditions per session (each extra one is an AAC encode).
	MaxAudioTracks = 3
)

// AudioTrack is one broadcast audio stream, in PMT order (FFmpeg's 0:a:N).
type AudioTrack struct {
	Lang      string // ISO 639-2 code from the PMT, e.g. "eng"; "" if absent
	Described bool   // audio description for the visually impaired
}

// Layout is a session's rendition set: the main variant (video + first
// audio), one audio-only rendition per extra track, and optional WebVTT
// captions. The zero Layout is video + one audio, no captions.
type Layout struct {
	Audio    []AudioTrack
	Captions bool
}

// AudioCount is how many audio streams FFmpeg maps (1..MaxAudioTracks).
func (l Layout) AudioCount() int {
	n := len(l.Audio)
	if n < 1 {
		return 1
	}
	if n > MaxAudioTracks {
		return MaxAudioTracks
	}
	return n
}

// Safe is the fallback layout: video and the first audio track only.
func (l Layout) Safe() Layout {
	if len(l.Audio) == 0 {
		return Layout{}
	}
	return Layout{Audio: l.Audio[:1]}
}

// IsSafe reports whether l has no extra audio and no captions.
func (l Layout) IsSafe() bool { return !l.Captions && l.AudioCount() == 1 }

func (l Layout) track(i int) AudioTrack {
	if i < len(l.Audio) {
		return l.Audio[i]
	}
	return AudioTrack{}
}

// VarStreamMap is FFmpeg's -var_stream_map for l.
func (l Layout) VarStreamMap() string {
	n := l.AudioCount()
	main := "v:0,a:0"
	if l.Captions {
		main += ",s:0"
	}
	if n > 1 {
		main += ",agroup:aud"
	}
	if l.Captions {
		main += ",sgroup:subs"
	}
	parts := []string{main + ",name:main"}
	for i := 1; i < n; i++ {
		parts = append(parts, fmt.Sprintf("a:%d,agroup:aud,name:aud%d", i, i))
	}
	return strings.Join(parts, " ")
}

var languageNames = map[string]string{
	"eng": "English", "spa": "Español", "fra": "Français", "fre": "Français",
}

var bcp47 = map[string]string{"eng": "en", "spa": "es", "fra": "fr", "fre": "fr"}

// BCP47 is the HLS LANGUAGE value for t ("" if unknown).
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

// AudioNames are the rendition NAMEs, unique within the group as HLS requires.
func (l Layout) AudioNames() []string {
	n := l.AudioCount()
	out := make([]string, n)
	seen := map[string]int{}
	for i := 0; i < n; i++ {
		base := l.track(i).name()
		seen[base]++
		if seen[base] > 1 {
			out[i] = fmt.Sprintf("%s %d", base, seen[base])
		} else {
			out[i] = base
		}
	}
	return out
}

// MasterPlaylist is the HLS master playlist Bowtie serves as index.m3u8.
// query (e.g. "?token=…") is appended to every URI. Captions are listed only
// when captionsReady (the caption playlist exists).
func MasterPlaylist(l Layout, d Decision, query string, captionsReady bool) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n")
	n := l.AudioCount()
	if n > 1 {
		names := l.AudioNames()
		for i := 0; i < n; i++ {
			t := l.track(i)
			fmt.Fprintf(&b, `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="%s"`, names[i])
			if lang := t.BCP47(); lang != "" {
				fmt.Fprintf(&b, `,LANGUAGE="%s"`, lang)
			}
			if i == 0 {
				b.WriteString(",DEFAULT=YES,AUTOSELECT=YES")
			} else {
				fmt.Fprintf(&b, `,DEFAULT=NO,AUTOSELECT=YES,URI="aud%d.m3u8%s"`, i, query)
			}
			if t.Described {
				b.WriteString(`,CHARACTERISTICS="public.accessibility.describes-video"`)
			}
			b.WriteByte('\n')
		}
	}
	if captionsReady {
		fmt.Fprintf(&b, `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English CC",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,FORCED=NO,URI="%s%s"`+"\n", CaptionPlaylist, query)
	}
	bw := (d.Profile.VideoKbps*12/10 + d.Profile.AudioKbps*n) * 1000
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d", bw)
	if h := d.Profile.Height; h > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", (h*16/9+1)/2*2, h)
	}
	if n > 1 {
		b.WriteString(`,AUDIO="aud"`)
	}
	if captionsReady {
		b.WriteString(`,SUBTITLES="subs"`)
	}
	b.WriteString("\n" + MainPlaylist + query + "\n")
	return b.String()
}
```

(If Task 1 Step 5 ruled that `CODECS` is required, add it to the `EXT-X-STREAM-INF` line here and to the test expectations.)

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/transcode/`
Expected: PASS. (Bandwidth check: 4000·1.2 + 128·2 = 5056 kbps; single audio 4928.)

- [ ] **Step 5: Commit**

```bash
git add server/internal/transcode/layout.go server/internal/transcode/layout_test.go
git commit -m "feat(transcode): rendition layout and master playlist builder"
```

---

### Task 3: `BuildArgs` emits the multi-rendition FFmpeg command

**Files:**
- Modify: `server/internal/transcode/ffmpeg.go` (`JobSpec`, `BuildArgs`)
- Modify: `server/internal/transcode/ffmpeg_test.go` (all `seg%05d.ts` / `live.m3u8` expectations), `server/internal/transcode/ffmpeg_e2e_test.go`

**Interfaces:**
- Consumes: `Layout`, `MainPlaylist` (Task 2)
- Produces: `JobSpec.Layout Layout`, `JobSpec.CaptionInput io.Reader` (wired to fd 3 by the runner, Task 4)

- [ ] **Step 1: Update existing expectations and add new tests.** In `ffmpeg_test.go` replace every

```go
		"-hls_segment_filename", filepath.Join(out, "seg%05d.ts"),
		filepath.Join(out, "live.m3u8"),
```
with
```go
		"-hls_segment_filename", filepath.Join(out, "%v_%05d.ts"),
		"-var_stream_map", "v:0,a:0,name:main",
		filepath.Join(out, "%v.m3u8"),
```
and insert `"-map", "0:v:0", "-map", "0:a:0",` immediately after each expected input (`"-i", <input>`) block. Then add:

```go
func TestBuildArgsMultitrackQSV(t *testing.T) {
	out := "/tmp/out"
	s := transcode.JobSpec{
		Stdin:  nonNilStdin(),
		OutDir: out,
		D: transcode.Decision{
			VideoCodec: "h264", VideoEncoder: "h264_qsv",
			Profile: transcode.Profile{Name: "high", Height: 720, VideoKbps: 4000, AudioKbps: 128},
			Backend: transcode.BackendQSV,
		},
		Layout: transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "spa"}}, Captions: true},
	}
	got := transcode.BuildArgs(s)
	want := []string{
		"-hide_banner", "-loglevel", "warning", "-nostats",
		"-init_hw_device", "qsv=hw", "-hwaccel", "qsv", "-hwaccel_output_format", "qsv", "-c:v", "mpeg2_qsv",
		"-fflags", "+discardcorrupt", "-i", "pipe:0",
		"-f", "lavfi", "-i", "movie='pipe\\:3'[out0+subcc]",
		"-map", "0:v:0", "-map", "0:a:0", "-map", "0:a:1", "-map", "1:s:0",
		"-vf", "vpp_qsv=deinterlace=2:w=trunc(iw*720/ih/2)*2:h=720",
		"-c:v", "h264_qsv",
		"-b:v", "4000k", "-maxrate", "4800k", "-bufsize", "8000k",
		"-g", "120", "-force_key_frames", "expr:gte(t,n_forced*4)",
		"-preset", "veryfast",
		"-a53cc", "0",
		"-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-c:s", "webvtt",
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "30",
		"-hls_flags", "delete_segments+temp_file+omit_endlist",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(out, "%v_%05d.ts"),
		"-var_stream_map", "v:0,a:0,s:0,agroup:aud,sgroup:subs,name:main a:1,agroup:aud,name:aud1",
		filepath.Join(out, "%v.m3u8"),
	}
	assertArgs(t, got, want)
}

func TestBuildArgsCaptionsDisableInBandCC(t *testing.T) {
	s := transcode.JobSpec{
		Stdin: nonNilStdin(), OutDir: "/tmp/out",
		D: transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264",
			Profile: transcode.Profile{Height: 480, VideoKbps: 1500, AudioKbps: 96}, Backend: transcode.BackendSoftware},
		Layout: transcode.Layout{Captions: true},
	}
	got := transcode.BuildArgs(s)
	if !containsAdjacent(got, "-a53cc", "0") {
		t.Fatalf("libx264 with caption rendition must set -a53cc 0 (duplicate CC track): %v", got)
	}
	for _, enc := range []string{"h264_qsv", "h264_nvenc"} {
		s.D.VideoEncoder = enc
		if !containsAdjacent(transcode.BuildArgs(s), "-a53cc", "0") {
			t.Errorf("%s with caption rendition must set -a53cc 0", enc)
		}
	}
	s.D.VideoEncoder = "libx264"
	s.Layout.Captions = false
	if containsAdjacent(transcode.BuildArgs(s), "-a53cc", "0") {
		t.Fatal("-a53cc 0 without caption rendition removes the only captions")
	}
}
```
In `ffmpeg_e2e_test.go`, change the playlist it waits for from `live.m3u8` to `transcode.MainPlaylist` and segment globs from `seg*.ts` to `main_*.ts`.

If Task 1 Step 3 ruled cc_dec options (e.g. `-real_time 1`), they go immediately before `"-f", "lavfi"` in both the test and the code.

- [ ] **Step 2: Run to verify failure**

Run: `cd server && go test ./internal/transcode/`
Expected: FAIL — `unknown field Layout in struct literal` and the renamed expectations.

- [ ] **Step 3: Implement.** In `JobSpec` add:

```go
	// Layout is the session's rendition set (extra audio, captions). The zero
	// Layout is video + first audio only.
	Layout Layout
	// CaptionInput feeds the caption tap (fd 3) when Layout.Captions is set:
	// a second ingest subscriber of the same channel. The runner wires it.
	CaptionInput io.Reader
```
In `BuildArgs`, after the input block:

```go
	if s.Layout.Captions {
		// Same TS again on fd 3, decoded in software only to extract the
		// CEA-608 captions (h264_qsv cannot carry them in-band).
		args = append(args, "-f", "lavfi", "-i", "movie='pipe\\:3'[out0+subcc]")
	}
	args = append(args, "-map", "0:v:0")
	for i := 0; i < s.Layout.AudioCount(); i++ {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", i))
	}
	if s.Layout.Captions {
		args = append(args, "-map", "1:s:0")
	}
```
after `args = append(args, encoderExtras(s.D)...)`:

```go
	switch s.D.VideoEncoder {
	case "libx264", "h264_qsv", "h264_nvenc":
		if s.Layout.Captions {
			// The WebVTT rendition carries captions; in-band A53 would show twice.
			args = append(args, "-a53cc", "0")
		}
	}
```
after the audio block:

```go
	if s.Layout.Captions {
		args = append(args, "-c:s", "webvtt")
	}
```
and replace the output tail:

```go
	args = append(args,
		"-f", "hls",
		"-hls_time", "4",
		"-hls_list_size", fmt.Sprintf("%d", listSize),
		"-hls_flags", hlsFlags,
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", filepath.Join(s.OutDir, "%v_%05d.ts"),
		"-var_stream_map", s.Layout.VarStreamMap(),
		filepath.Join(s.OutDir, "%v.m3u8"),
	)
```
Delete the old `segPattern`/`playlist` variables.

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/transcode/ && go vet ./internal/transcode/`
Expected: PASS (the FFmpeg e2e test runs locally when `ffmpeg` is on PATH; it must pass too).

- [ ] **Step 5: Commit**

```bash
git add server/internal/transcode/
git commit -m "feat(transcode): var_stream_map renditions and caption tap input"
```

---

### Task 4: Runner wires the caption input to fd 3

**Files:**
- Modify: `server/internal/stream/runner.go` (`FFmpegRunner.Start`)
- Create: `server/internal/stream/testdata/fd3cat.sh`
- Test: `server/internal/stream/runner_test.go`

**Interfaces:**
- Consumes: `JobSpec.CaptionInput` (Task 3)

- [ ] **Step 1: Write the failing test.** `testdata/fd3cat.sh` (mode 0755):

```sh
#!/bin/sh
# Fake ffmpeg for runner tests: copies fd 3 to $FD3_OUT, ignores its args.
cat <&3 > "$FD3_OUT"
```
Test:

```go
func TestRunnerFeedsCaptionInputOnFD3(t *testing.T) {
	out := filepath.Join(t.TempDir(), "fd3.out")
	t.Setenv("FD3_OUT", out)
	script, err := filepath.Abs("testdata/fd3cat.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &FFmpegRunner{Path: script}
	spec := transcode.JobSpec{
		Stdin:        strings.NewReader(""),
		OutDir:       t.TempDir(),
		Layout:       transcode.Layout{Captions: true},
		CaptionInput: strings.NewReader("caption-bytes"),
	}
	p, err := r.Start(context.Background(), spec)
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
		t.Fatal("fake ffmpeg did not exit: fd 3 never reached EOF")
	}
	got, _ := os.ReadFile(out)
	if string(got) != "caption-bytes" {
		t.Fatalf("fd3 got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd server && go test ./internal/stream/ -run TestRunnerFeedsCaptionInputOnFD3`
Expected: FAIL — fake exits with `Bad file descriptor` (fd 3 not open) → `fake ffmpeg: exit status 2` (or empty output).

- [ ] **Step 3: Implement** in `FFmpegRunner.Start`, after `cmd.Stderr = …`:

```go
	var capW *os.File
	if spec.CaptionInput != nil {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		cmd.ExtraFiles = []*os.File{pr} // fd 3 in the child
		defer pr.Close()                 // the child has its own copy after Start
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
(replacing the existing `if err := cmd.Start(); err != nil { return nil, err }`; add `"os"` import).

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/stream/ -run 'Runner' -count=3`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/runner.go server/internal/stream/runner_test.go server/internal/stream/testdata/fd3cat.sh
git commit -m "feat(stream): feed the caption tap to FFmpeg on fd 3"
```

---

### Task 5: PMT audio parse, ingest track probe, manager wiring

**Files:**
- Create: `server/internal/stream/pmt.go`, `server/internal/stream/pmt_test.go`
- Modify: `server/internal/stream/ingest.go` (add `ProgramAudio`)
- Modify: `server/internal/stream/manager.go`, `server/internal/stream/session.go`
- Modify: `server/internal/stream/manager_test.go` (`live.m3u8` → `transcode.MainPlaylist`; new tests)
- Modify: `server/cmd/bowtie/main.go`, `server/internal/config/config.go` (+ test)
- Modify: `server/internal/e2e/runners.go` (`writeStubPlaylist` names), `server/internal/e2e/harness.go` (leave `Multitrack` false)

**Interfaces:**
- Consumes: `Layout`, `AudioTrack`, `MainPlaylist` (Task 2), `JobSpec.Layout/CaptionInput` (Task 3)
- Produces:
  - `func parsePMTAudio(pkt []byte) []transcode.AudioTrack`
  - `func (m *IngestManager) ProgramAudio(channelID int64, timeout time.Duration) ([]transcode.AudioTrack, bool)`
  - `ManagerDeps.Multitrack bool`, `ManagerDeps.TrackProbeTimeout time.Duration` (0 → 3s)
  - `type SessionMedia struct { Dir string; Layout transcode.Layout; Decision transcode.Decision }`
  - `func (m *Manager) SessionMediaOf(viewerID string) (SessionMedia, bool)`
  - `config.Config.DisableMultitrack bool` (yaml `disableMultitrack`, env `BOWTIE_MULTITRACK=off|0|false` sets it)

- [ ] **Step 1: PMT parse tests** (`pmt_test.go`):

```go
package stream

import (
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// pmtPacket builds a single-packet PMT: MPEG-2 video then the given ES entries.
func pmtPacket(es ...[]byte) []byte {
	sec := []byte{0x02, 0, 0, 0x00, 0x03, 0xC1, 0x00, 0x00, 0xE0, 0x31, 0xF0, 0x00}
	sec = append(sec, 0x02, 0xE0, 0x31, 0xF0, 0x00) // video PID 0x31, no descriptors
	for _, e := range es {
		sec = append(sec, e...)
	}
	sec = append(sec, 0, 0, 0, 0) // CRC (not checked)
	l := len(sec) - 3
	sec[1] = 0xB0 | byte(l>>8)
	sec[2] = byte(l)
	pkt := make([]byte, tsPacketSize)
	for i := range pkt {
		pkt[i] = 0xFF
	}
	copy(pkt, []byte{tsSyncByte, 0x40, 0x30, 0x10, 0x00}) // PUSI, PID 0x30, payload only, pointer 0
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

func TestParsePMTAudio(t *testing.T) {
	pkt := pmtPacket(
		audioES(0x81, 0x34, "eng", 0),
		audioES(0x81, 0x35, "spa", 0),
		audioES(0x81, 0x36, "eng", 0x03),
		audioES(0x06, 0x37, "", 0), // private data, not audio
		audioES(0x0F, 0x38, "", 0), // AAC without language
	)
	got := parsePMTAudio(pkt)
	want := []transcode.AudioTrack{{Lang: "eng"}, {Lang: "spa"}, {Lang: "eng", Described: true}, {}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("track %d = %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestParsePMTAudioRejectsGarbage(t *testing.T) {
	for _, pkt := range [][]byte{nil, make([]byte, 10), make([]byte, tsPacketSize)} {
		if got := parsePMTAudio(pkt); len(got) != 0 {
			t.Fatalf("garbage parsed as %+v", got)
		}
	}
	trunc := pmtPacket(audioES(0x81, 0x34, "eng", 0))
	trunc[7] = 0xB0 | 0x0F // section length far past the packet
	_ = parsePMTAudio(trunc) // must not panic
}
```

- [ ] **Step 2: Run** `cd server && go test ./internal/stream/ -run ParsePMT` — Expected: FAIL `undefined: parsePMTAudio`.

- [ ] **Step 3: Implement `pmt.go`**

```go
package stream

import (
	"strings"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// parsePMTAudio lists a PMT packet's audio streams in PMT order (the order
// FFmpeg numbers 0:a:N) with their ISO 639 language. Minimal PSI: the section
// must start in this packet, which holds for ATSC single-program PMTs.
func parsePMTAudio(pkt []byte) []transcode.AudioTrack {
	if len(pkt) < tsPacketSize || pkt[0] != tsSyncByte || pkt[1]&0x40 == 0 {
		return nil
	}
	i := 4
	if afc := (pkt[3] >> 4) & 0x3; afc == 2 || afc == 3 {
		i += 1 + int(pkt[4])
	}
	if i >= len(pkt) {
		return nil
	}
	i += 1 + int(pkt[i]) // pointer_field
	if i+12 > len(pkt) || pkt[i] != 0x02 {
		return nil
	}
	end := i + 3 + (int(pkt[i+1]&0x0F)<<8 | int(pkt[i+2])) - 4 // minus CRC
	if end > len(pkt) {
		end = len(pkt)
	}
	j := i + 12 + (int(pkt[i+10]&0x0F)<<8 | int(pkt[i+11]))
	var out []transcode.AudioTrack
	for j+5 <= end {
		st := pkt[j]
		n := int(pkt[j+3]&0x0F)<<8 | int(pkt[j+4])
		dEnd := min(j+5+n, end)
		if isAudioStreamType(st) {
			out = append(out, audioTrackFrom(pkt[j+5:dEnd]))
		}
		j += 5 + n
	}
	return out
}

func isAudioStreamType(st byte) bool {
	switch st {
	case 0x03, 0x04, 0x0F, 0x11, 0x81, 0x87: // MPEG-1/2, AAC, LATM, AC-3, E-AC-3
		return true
	}
	return false
}

// audioTrackFrom reads the ISO_639_language_descriptor (tag 0x0A); audio_type
// 0x03 is "visual impaired commentary".
func audioTrackFrom(desc []byte) transcode.AudioTrack {
	for k := 0; k+2 <= len(desc); {
		tag, l := desc[k], int(desc[k+1])
		body := desc[k+2 : min(k+2+l, len(desc))]
		if tag == 0x0A && len(body) >= 4 {
			return transcode.AudioTrack{
				Lang:      strings.ToLower(string(body[:3])),
				Described: body[3] == 0x03,
			}
		}
		k += 2 + l
	}
	return transcode.AudioTrack{}
}
```

- [ ] **Step 4: Run** `go test ./internal/stream/ -run ParsePMT` — Expected: PASS.

- [ ] **Step 5: `ProgramAudio` test** (in `ingest_test.go`, using the existing dial fakes; feed a body that starts with a PAT for PMT PID 0x30 then `pmtPacket(...)` from Step 1). Use the existing PAT builder in `ingest_test.go` if present (grep `func patPacket`); otherwise build a PAT with program 3 → PID 0x30:

```go
func TestProgramAudioReadsPMT(t *testing.T) {
	body := append(patFor(0x30), pmtPacket(audioES(0x81, 0x34, "eng", 0), audioES(0x81, 0x35, "spa", 0))...)
	im := newIngestWithBody(t, body) // helper: IngestManager whose dial returns body then blocks
	sub, err := im.Attach(context.Background(), 7, "http://dev/auto/v7")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	got, ok := im.ProgramAudio(7, 2*time.Second)
	if !ok || len(got) != 2 || got[1].Lang != "spa" {
		t.Fatalf("ProgramAudio=%+v ok=%v", got, ok)
	}
}

func TestProgramAudioTimesOutWithoutPMT(t *testing.T) {
	im := newIngestWithBody(t, nil)
	sub, err := im.Attach(context.Background(), 7, "http://dev/auto/v7")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	start := time.Now()
	if _, ok := im.ProgramAudio(7, 100*time.Millisecond); ok {
		t.Fatal("ok without PMT")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout not honored")
	}
}
```
If `patFor`/`newIngestWithBody` don't exist, add them to `pmt_test.go` (PAT: section `00 B0 0D 00 01 C1 00 00 00 03 E0 30` + CRC4, PUSI packet on PID 0; ingest: `NewIngestManager` with a `DialFunc` returning `io.NopCloser(io.MultiReader(bytes.NewReader(body), blockingReader{}))` — copy the shape of the dial fakes in `ingest_test.go`).

- [ ] **Step 6: Run** `go test ./internal/stream/ -run ProgramAudio` — Expected: FAIL `undefined: (*IngestManager).ProgramAudio`.

- [ ] **Step 7: Implement** in `ingest.go`:

```go
// ProgramAudio waits up to timeout for the channel's PMT and returns its audio
// tracks. ok is false when no PMT arrived; callers then assume one track.
func (m *IngestManager) ProgramAudio(channelID int64, timeout time.Duration) ([]transcode.AudioTrack, bool) {
	deadline := time.Now().Add(timeout)
	for {
		m.mu.Lock()
		c := m.channels[channelID]
		m.mu.Unlock()
		if c != nil {
			c.mu.Lock()
			pmt := c.lastPMT
			c.mu.Unlock()
			if pmt != nil {
				return parsePMTAudio(pmt), true
			}
		}
		if !time.Now().Before(deadline) {
			return nil, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```
Run again — Expected: PASS.

- [ ] **Step 8: Manager tests** (append to `manager_test.go`; set `Multitrack: true` in a local `ManagerDeps` built like `newTestManagerWithDial`, with a dial body carrying PAT+PMT (eng, spa)):

```go
func TestStartMultitrackLayoutAndCaptionSub(t *testing.T) {
	// dial body: PAT + PMT(eng, spa), then blocks.
	m, im, runner := newMultitrackManager(t, patFor(0x30), pmtPacket(audioES(0x81, 0x34, "eng", 0), audioES(0x81, 0x35, "spa", 0)))
	h, err := m.Start(context.Background(), viewerUser(), testChannelID, transcode.ClientCaps{})
	if err != nil {
		t.Fatal(err)
	}
	spec := runner.LastSpec()
	if spec.Layout.AudioCount() != 2 || !spec.Layout.Captions || spec.CaptionInput == nil {
		t.Fatalf("spec layout=%+v captionInput=%v", spec.Layout, spec.CaptionInput != nil)
	}
	media, ok := m.SessionMediaOf(h.ViewerID)
	if !ok || media.Layout.AudioCount() != 2 {
		t.Fatalf("SessionMediaOf=%+v ok=%v", media, ok)
	}
	if n := im.attachCalls.Load(); n != 2 {
		t.Fatalf("attach calls=%d want 2 (video + caption tap)", n)
	}
}

func TestStartFallsBackToSafeLayoutWhenPlaylistNeverAppears(t *testing.T) {
	m, _, runner := newMultitrackManager(t, patFor(0x30), pmtPacket(audioES(0x81, 0x34, "eng", 0), audioES(0x81, 0x35, "spa", 0)))
	// First process exits before writing a playlist (like "-map 0:a:1 matches no streams").
	runner.onStart = func(spec transcode.JobSpec) {
		if !spec.Layout.IsSafe() {
			runner.failNext = true
		}
	}
	_, err := m.Start(context.Background(), viewerUser(), testChannelID, transcode.ClientCaps{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	specs := runner.Specs()
	if len(specs) != 2 || specs[0].Layout.IsSafe() || !specs[1].Layout.IsSafe() || specs[1].CaptionInput != nil {
		t.Fatalf("specs=%+v", specs)
	}
}

func TestCaptionSubForceCloseDoesNotStopFFmpeg(t *testing.T) {
	m, _, runner := newMultitrackManager(t, patFor(0x30), pmtPacket(audioES(0x81, 0x34, "eng", 0)))
	if _, err := m.Start(context.Background(), viewerUser(), testChannelID, transcode.ClientCaps{}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	var cap *IngestSub
	for _, s := range m.sessions {
		cap = s.capSub
	}
	m.mu.Unlock()
	cap.forceClose()
	if runner.LastProc().Stopped() {
		t.Fatal("caption tap force-close stopped FFmpeg")
	}
}

func TestMultitrackOffKeepsSingleAttach(t *testing.T) {
	// existing newTestManagerWithDial (Multitrack false): one Attach, zero Layout.
	st := newTestStore(t)
	m, im, _ := newTestManagerWithDial(st, config.Config{SegmentDir: t.TempDir()}, newFakeClock(), &stubRunner{writeM3U: true}, okDial())
	if _, err := m.Start(context.Background(), viewerUser(), testChannelID, transcode.ClientCaps{}); err != nil {
		t.Fatal(err)
	}
	if n := im.attachCalls.Load(); n != 1 {
		t.Fatalf("attach calls=%d", n)
	}
}
```
Adapt helper names (`viewerUser`, `testChannelID`, `newTestStore`, `okDial`, `stubRunner.failNext/Specs/LastSpec`, `stubProcess.Stopped`) to what `manager_test.go` already has; add the missing ones to `stubRunner` (a `failNext` flag makes `Start` return a process that exits immediately without writing the playlist; `writeM3U` writes `transcode.MainPlaylist`). `newMultitrackManager` builds `ManagerDeps{…, Multitrack: true, TrackProbeTimeout: time.Second}` with a dial returning `body` then blocking. Also replace all other `"live.m3u8"` in `manager_test.go` with `transcode.MainPlaylist`.

- [ ] **Step 9: Run** `go test ./internal/stream/` — Expected: FAIL (unknown fields `Multitrack`, `capSub`, `SessionMediaOf`).

- [ ] **Step 10: Implement manager wiring.**

`session.go` — add fields and helper:

```go
	layout transcode.Layout // fixed at start; restarts reuse it
	capSub *IngestSub       // caption tap (fd 3); nil when layout has no captions
```
```go
// closeSubs closes the process-scoped ingest subscribers (video and caption tap).
func (s *session) closeSubs() {
	if s.sub != nil {
		_ = s.sub.Close()
		s.sub = nil
	}
	if s.capSub != nil {
		_ = s.capSub.Close()
		s.capSub = nil
	}
}
```
and `SessionMedia` type + `ViewerHandle` comment `// contains main.m3u8`.

`manager.go`:
- `ManagerDeps`: `Multitrack bool` (comment: serve extra audio + captions renditions; off = video + first audio) and `TrackProbeTimeout time.Duration` (0 → 3s); copy into `Manager` fields `multitrack`, `trackProbe`.
- Replace the three `if sess.sub != nil { … }` blocks (supervise, prepareRestartLocked, teardownSessionLocked) and the abandon/failed-start `sub.Close()` calls with `closeSubs` / closing both subs.
- Add:

```go
var errPlaylistNotReady = errors.New("playlist not ready")

// playlistNotReadyError keeps the FFmpeg message and matches errPlaylistNotReady.
type playlistNotReadyError struct{ err error }

func (e playlistNotReadyError) Error() string   { return e.err.Error() }
func (e playlistNotReadyError) Unwrap() []error { return []error{e.err, errPlaylistNotReady} }

// layoutFor probes the channel's audio tracks (ingest must be attached).
func (m *Manager) layoutFor(channelID int64, safe bool) transcode.Layout {
	if !m.multitrack || safe {
		return transcode.Layout{}
	}
	tracks, ok := m.ingest.ProgramAudio(channelID, m.trackProbe)
	if !ok || len(tracks) == 0 {
		tracks = []transcode.AudioTrack{{}}
	}
	return transcode.Layout{Audio: tracks, Captions: true}
}

// attachCaptions attaches the caption tap when layout needs it. The tap has
// no OnForceClose hook: if it stalls, captions stop but video continues.
func (m *Manager) attachCaptions(ctx context.Context, channelID int64, inputURL string, layout transcode.Layout) (*IngestSub, error) {
	if !layout.Captions {
		return nil, nil
	}
	return m.ingest.Attach(ctx, channelID, inputURL)
}
```
- `waitPlaylist`: path `filepath.Join(dir, transcode.MainPlaylist)`; wrap both failure returns: `return playlistNotReadyError{fmt.Errorf("ffmpeg exited before playlist ready: %w", err)}` (and the nil-err variant, and `"playlist timeout waiting for main.m3u8"`).
- `Start`: thread a `safe` flag:

```go
	safe := !m.multitrack
	var lastErr error
	for attempt := 0; attempt < startMaxAttempts; attempt++ {
		h, err, retry := m.startAttempt(ctx, user, ch, key, decision, inputURL, safe)
		if err == nil {
			return h, nil
		}
		if !safe && errors.Is(err, errPlaylistNotReady) {
			log.Printf("stream: channel %d: multi-track start failed (%v); retrying with video + first audio", ch.ID, err)
			safe = true
			attempt-- // the fallback is not a duplicate-key retry
			continue
		}
		…existing…
```
- `startAttempt(…, safe bool)`: after the video `Attach`:

```go
	layout := m.layoutFor(ch.ID, safe)
	capSub, err := m.attachCaptions(ctx, ch.ID, inputURL, layout)
	if err != nil {
		log.Printf("stream: channel %d: caption tap attach failed: %v (continuing without captions)", ch.ID, err)
		layout.Captions = false
	}
	spec := transcode.JobSpec{Stdin: sub.R, OutDir: dir, D: decision, HLSListSize: listSize, Layout: layout}
	if capSub != nil {
		spec.CaptionInput = capSub.R
	}
```
  Every failure path after this closes `capSub` too (nil-safe helper `closeSub(capSub)`); the `session` literal gets `layout: layout, capSub: capSub`.
- `restartSession`: after the video `Attach`, `capSub, err := m.attachCaptions(context.Background(), sess.channelID, sess.inputURL, sess.layout)`; on error log and run this process without captions (`layout := sess.layout; layout.Captions = false` for the spec only); spec gets `Layout`/`CaptionInput`; failure paths close `capSub`; commit sets `sess.capSub = capSub`.
- `SessionMediaOf`:

```go
// SessionMediaOf returns what the playlist handlers need for a viewer's session.
func (m *Manager) SessionMediaOf(viewerID string) (SessionMedia, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return SessionMedia{}, false
	}
	sess, ok := m.sessions[v.SessionID]
	if !ok || sess.terminated {
		return SessionMedia{}, false
	}
	return SessionMedia{Dir: sess.dir, Layout: sess.layout, Decision: sess.decision}, true
}
```
`config.go`: field `DisableMultitrack bool \`yaml:"disableMultitrack"\``; env: `if v := strings.ToLower(os.Getenv("BOWTIE_MULTITRACK")); v == "off" || v == "0" || v == "false" { cfg.DisableMultitrack = true }`; add the env var to the `Load` doc comment and a `config_test.go` case. `cmd/bowtie/main.go`: `Multitrack: !cfg.DisableMultitrack` in `stream.ManagerDeps`. `e2e/runners.go` `writeStubPlaylist`: names `main_%05d.ts` and `transcode.MainPlaylist`.

- [ ] **Step 11: Run** `cd server && go test ./internal/stream/ ./internal/config/ ./internal/e2e/ -count=1 && go vet ./...`
Expected: PASS (e2e may fail only on testplayer/handler names fixed in Task 6 — if so, ledger it and finish them there; the stream and config packages must pass here).

- [ ] **Step 12: Commit**

```bash
git add server/
git commit -m "feat(stream): probe audio tracks, caption tap sub, safe-layout fallback"
```

---

### Task 6: API serves the master, rendition playlists, segments and WebVTT

**Files:**
- Modify: `server/internal/api/stream_handlers.go`
- Modify: `server/internal/api/stream_handlers_test.go` (fixture names; `stubStreams.SessionMediaOf`)
- Modify: `server/internal/testplayer/player.go` (follow the master), `server/internal/testplayer/player_test.go`
- Modify: `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: `MasterPlaylist`, `MainPlaylist`, `CaptionPlaylist` (Task 2); `SessionMediaOf`, `SessionMedia` (Task 5)
- Produces: `StreamController.SessionMediaOf(viewerID string) (stream.SessionMedia, bool)`

- [ ] **Step 1: Tests.** Change `fixturePlaylist`/`writeFixtureSession` to write `main.m3u8` with `main_00000.ts`/`main_00001.ts`, plus `aud1.m3u8` (`aud1_00000.ts`), `main_vtt.m3u8` (`main0.vtt`) and `main0.vtt` (`"WEBVTT\n\n00:00.000 --> 00:02.000\nHello\n"`). `stubStreams.register` stores a `stream.SessionMedia{Dir: dir, Layout: transcode.Layout{Audio: []transcode.AudioTrack{{Lang: "eng"}, {Lang: "spa"}}, Captions: true}, Decision: <720p decision>}`; implement `SessionMediaOf` on `stubStreams`. Rename `TestPlaylistRewriteAndTouch` → `TestMediaPlaylistRewriteAndTouch` fetching `/api/v1/stream/<id>/main.m3u8?token=…` with expectations `main_00000.ts`. Add:

```go
func TestIndexIsMasterPlaylist(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	viewerID := "aabbccddeeff00112233445566778899"
	ss.register(viewerID, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))

	rr := doJSON(t, h, "GET", "/api/v1/stream/"+viewerID+"/index.m3u8?token="+tok, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`URI="aud1.m3u8?token=` + tok + `"`,
		`URI="main_vtt.m3u8?token=` + tok + `"`,
		"\nmain.m3u8?token=" + tok + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("master missing %q:\n%s", want, body)
		}
	}
	// Captions are omitted until the caption playlist exists.
	_ = os.Remove(filepath.Join(dir, "main_vtt.m3u8"))
	rr = doJSON(t, h, "GET", "/api/v1/stream/"+viewerID+"/index.m3u8?token="+tok, nil, nil)
	if strings.Contains(rr.Body.String(), "SUBTITLES") {
		t.Fatalf("captions listed before main_vtt.m3u8 exists:\n%s", rr.Body.String())
	}
}

func TestRenditionPlaylistsTouchAndRewrite(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	viewerID := "aabbccddeeff00112233445566778899"
	ss.register(viewerID, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	for name, seg := range map[string]string{"aud1.m3u8": "aud1_00000.ts", "main_vtt.m3u8": "main0.vtt"} {
		rr := doJSON(t, h, "GET", "/api/v1/stream/"+viewerID+"/"+name+"?token="+tok, nil, nil)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/api/v1/stream/"+viewerID+"/"+seg+"?token="+tok) {
			t.Fatalf("%s: %d\n%s", name, rr.Code, rr.Body.String())
		}
	}
	ss.mu.Lock()
	n := len(ss.touchCalls)
	ss.mu.Unlock()
	if n != 2 {
		t.Fatalf("rendition playlist fetches must Touch (viewer idle reaping); touches=%d", n)
	}
}

func TestVTTSegmentGetsTimestampMap(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	viewerID := "aabbccddeeff00112233445566778899"
	ss.register(viewerID, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "GET", "/api/v1/stream/"+viewerID+"/main0.vtt?token="+tok, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/vtt") {
		t.Errorf("Content-Type=%q", ct)
	}
	if !strings.HasPrefix(rr.Body.String(), "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

func TestStreamFileNamesRejected(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	viewerID := "aabbccddeeff00112233445566778899"
	ss.register(viewerID, t.TempDir())
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	for _, bad := range []string{"seg00000.ts", "aud0_00000.ts", "x.m3u8", "live.m3u8", "main.vtt", "..%2Fmain.m3u8"} {
		rr := doJSON(t, h, "GET", "/api/v1/stream/"+viewerID+"/"+bad+"?token="+tok, nil, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d want 400", bad, rr.Code)
		}
	}
}
```
Update `TestSegmentNameTraversal400` and the e2e stub runner in this file (`live.m3u8` → `transcode.MainPlaylist`, `seg00000.ts` → `main_00000.ts`). testplayer test:

```go
func TestPlayerFollowsMasterPlaylist(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/s/index.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nmain.m3u8?token=t\n")
		case "/s/main.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4,\n/s/main_00000.ts\n")
		case "/s/main_00000.ts":
			w.Write([]byte{0x47, 0, 0, 0})
		}
	}))
	defer srv.Close()
	p := &Player{cfg: Config{BaseURL: srv.URL, Client: srv.Client()}, playlistURL: "/s/index.m3u8?token=t", prevNews: -1, highest: -1}
	p.poll(context.Background())
	p.poll(context.Background())
	if p.Report().SegmentsFetched != 1 {
		t.Fatalf("report=%+v hits=%v", p.Report(), hits)
	}
}
```
(Adapt the `Player` literal to its real field names/initial values.)

- [ ] **Step 2: Run** `cd server && go test ./internal/api/ ./internal/testplayer/` — Expected: FAIL (`SessionMediaOf` missing on interface, names).

- [ ] **Step 3: Implement handlers.**

```go
var (
	segmentNameRe   = regexp.MustCompile(`^(main|aud[1-9])_\d{5}\.ts$`)
	captionNameRe   = regexp.MustCompile(`^main\d+\.vtt$`)
	renditionNameRe = regexp.MustCompile(`^(main|aud[1-9]|main_vtt)\.m3u8$`)
)

// vttTimestampMap aligns FFmpeg's WebVTT cue times (which start at 0) with the
// video's MPEG-TS clock, which FFmpeg's mpegts muxer starts at 1.4 s.
const vttTimestampMap = "X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000"
```
- Add `SessionMediaOf(viewerID string) (stream.SessionMedia, bool)` to `StreamController`.
- `handlePlaylist` (index.m3u8): after Touch, `media, ok := s.deps.Streams.SessionMediaOf(viewerID)` (404 if !ok); 404 "playlist not ready" if `transcode.MainPlaylist` is missing; `captionsReady := media.Layout.Captions && fileExists(filepath.Join(media.Dir, transcode.CaptionPlaylist))`; body `transcode.MasterPlaylist(media.Layout, media.Decision, "?token="+url.QueryEscape(token), captionsReady)`; same headers as today.
- `serveMediaPlaylist(w, r, viewerID, name)`: Touch (404 viewer not found), dir, read `name`, `rewritePlaylist`, headers as today.
- `handleSegment`: validate `name` against the three regexes first (400 otherwise); verify access; then dispatch: rendition → `serveMediaPlaylist`; caption → read file, `withTimestampMap`, `Content-Type: text/vtt; charset=utf-8`, `Cache-Control: no-store`; segment → existing ServeContent path.
- `rewritePlaylist`: rewrite lines matching `segmentNameRe` or `captionNameRe`.

```go
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
(Use the Task 1 Step 4 ruling's constant if it differs.)
- testplayer `poll`: after the 200 check, if `bytes.Contains(body, []byte("#EXT-X-STREAM-INF"))`, take the first non-comment line, resolve it against the directory of `p.playlistURL` (`p.playlistURL[:strings.LastIndex(p.playlistURL, "/")+1] + uri` unless absolute), set `p.playlistURL` to it, and `return false` (next poll reads the media playlist).
- `docs/api/openapi.yaml`: `index.m3u8` description → "HLS master playlist (video variant + optional audio/subtitle renditions)"; the `{segment}` path documents names `main.m3u8`, `aud1.m3u8`, `aud2.m3u8`, `main_vtt.m3u8` (media playlists, `application/vnd.apple.mpegurl`), `main_NNNNN.ts`/`audN_NNNNN.ts` (`video/mp2t`), `mainN.vtt` (`text/vtt`); bump `info.version` to `0.8.0`.

- [ ] **Step 4: Run** `cd server && go test ./... -count=1 && go vet ./... && golangci-lint run ./...` (check each exit code; no `| tail`).
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/ docs/api/openapi.yaml
git commit -m "feat(api): serve master playlist, renditions and timestamped WebVTT"
```

---

### Task 7: Real end-to-end check on this Mac (9.1)

**Files:** none (verification); fixes found here get their own failing test first.

- [ ] **Step 1:** Build and run the branch server on :8400 (background, `run_in_background`, timeout 7200000): `cd server && go build -o $S/bowtie ./cmd/bowtie && BOWTIE_DEVICES=192.168.50.32 BOWTIE_LISTEN_ADDR=:8400 $S/bowtie -data-dir $S/bowtie-data`.
- [ ] **Step 2:** Log in (admin creds from the earlier run), `POST /api/v1/sessions {"channelId": <9.1 id>, "caps": {}}`, then run `mediasel http://127.0.0.1:8400<playlistUrl>`. Expected: `status=1`, audible English + Spanish, legible English. `curl` one `main*.vtt` URL from `main_vtt.m3u8`: starts `WEBVTT\nX-TIMESTAMP-MAP=…`; cue text readable.
- [ ] **Step 3:** hls.js in Chrome via Playwright: open the web app at `http://127.0.0.1:8400`, play 9.1, `browser_evaluate` → `window.__hls?.audioTracks.length` (temporarily not available — instead check the network panel: `aud1.m3u8` and `main_vtt.m3u8` requested after switching in Task 8). For this task: confirm playback starts (no fatal hls.js error) with the master.
- [ ] **Step 4:** Restart continuity: `pkill -f 'ffmpeg.*main.m3u8'` once; within 10 s the session resumes; `curl` `main.m3u8`, `aud1.m3u8`, `main_vtt.m3u8` — sequence numbers continue, one `#EXT-X-DISCONTINUITY` each; mediasel still `status=1`.
- [ ] **Step 5:** `DELETE` the session; confirm the tuner is released (admin tuners endpoint). Ledger results.

---

### Task 8: Web — audio picker and CC toggle (hls.js)

**Files:**
- Create: `web/src/player/tracksModel.ts`, `web/src/player/tracksModel.test.ts`
- Modify: `web/src/player/Player.tsx`, `web/src/player/Player.module.css`

**Interfaces:**
- Produces:
  - `type TrackPrefs = { audioLang: string | null; captions: boolean | null }`
  - `loadTrackPrefs(storage?: Storage): TrackPrefs`, `saveTrackPrefs(p: TrackPrefs, storage?: Storage): void`
  - `pickAudioIndex(tracks: { lang?: string }[], preferred: string | null): number` (−1 = leave default)
  - `audioTrackLabel(t: { name?: string; lang?: string }, i: number): string`

- [ ] **Step 1: Tests** (`tracksModel.test.ts`):

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

- [ ] **Step 2: Run** `cd web && npx vitest run src/player/tracksModel.test.ts` — Expected: FAIL (module not found).

- [ ] **Step 3: Implement `tracksModel.ts`**

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
    /* private mode / quota: preference just isn't remembered */
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

- [ ] **Step 4: Run** the test — Expected: PASS.

- [ ] **Step 5: Wire into `Player.tsx`.** State: `const [audioTracks, setAudioTracks] = useState<{ name?: string; lang?: string }[]>([])`, `const [audioIdx, setAudioIdx] = useState(0)`, `const [hasCaptions, setHasCaptions] = useState(false)`, `const [captionsOn, setCaptionsOn] = useState(false)`. In `attachPlayback` (hls.js branch), after `attachMedia`:

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
```
Handlers:

```ts
  const onPickAudio = useCallback((i: number) => {
    const hls = hlsRef.current
    if (!hls) return
    hls.audioTrack = i
    const t = hls.audioTracks[i]
    saveTrackPrefs({ ...loadTrackPrefs(), audioLang: t?.lang ?? null })
  }, [])

  const onToggleCaptions = useCallback(() => {
    const hls = hlsRef.current
    if (!hls) return
    const next = hls.subtitleTrack < 0
    hls.subtitleTrack = next ? 0 : -1
    setCaptionsOn(next)
    saveTrackPrefs({ ...loadTrackPrefs(), captions: next })
  }, [])
```
Reset `audioTracks`/`hasCaptions` in `destroyHls`. In the controls bar next to the Quality control (`Player.tsx` ~line 762), render when available:

```tsx
              {audioTracks.length > 1 ? (
                <select
                  className={styles.btn}
                  aria-label="Audio track"
                  value={audioIdx}
                  onChange={(e) => onPickAudio(Number(e.target.value))}
                >
                  {audioTracks.map((t, i) => (
                    <option key={i} value={i}>{audioTrackLabel(t, i)}</option>
                  ))}
                </select>
              ) : null}
              {hasCaptions ? (
                <button
                  type="button"
                  className={`${styles.btn} ${captionsOn ? styles.btnPrimary : ''}`}
                  aria-pressed={captionsOn}
                  aria-label="Closed captions"
                  onClick={onToggleCaptions}
                >
                  CC
                </button>
              ) : null}
```
Add a `::cue` rule to `Player.module.css` only if captions are unreadable on the dark player (`video::cue { font-family: inherit; background: rgba(0,0,0,0.75); }`).

- [ ] **Step 6: Run** `cd web && npm test && npm run lint && npm run build` — Expected: all pass.
- [ ] **Step 7: Browser check** (server from Task 7): Playwright → play 9.1 → audio select shows "English"/"Español"; pick Español → network shows `aud1.m3u8`; CC → `main_vtt.m3u8` requested and cue text visible in a screenshot. Reload → choices restored.
- [ ] **Step 8: Commit**

```bash
git add web/src/player/
git commit -m "feat(web): audio track picker and closed-caption toggle"
```

---

### Task 9: iOS / iPadOS / Apple TV — apply and remember media selection

AVPlayerViewController (`showsPlaybackControls = true` in `ios/App/iOS/PlayerView.swift:869` and `ios/App/tvOS/TVPlayerView.swift:622`) already shows audio and subtitle menus for the master's renditions. This task only remembers the choice.

**Files:**
- Create: `ios/BowtieKit/Sources/BowtieKit/MediaPrefs.swift`, `ios/BowtieKit/Tests/BowtieKitTests/MediaPrefsTests.swift`
- Modify: `ios/App/Shared/PlayerModel.swift`

**Interfaces:**
- Produces: `public struct MediaPrefs: Codable, Equatable { public var audioLanguage: String?; public var captionsOn: Bool? }`, `MediaPrefs.load(from: UserDefaults) -> MediaPrefs`, `func save(to: UserDefaults)`, `static func pickIndex(languages: [String?], preferred: String?) -> Int?`

- [ ] **Step 1: Tests**

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
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs(audioLanguage: nil, captionsOn: nil))
        MediaPrefs(audioLanguage: "es", captionsOn: true).save(to: d)
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs(audioLanguage: "es", captionsOn: true))
        d.set(Data("junk".utf8), forKey: MediaPrefs.defaultsKey)
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs())
    }
}
```

- [ ] **Step 2: Run** `cd ios/BowtieKit && swift test --filter MediaPrefsTests` — Expected: FAIL (cannot find `MediaPrefs`).

- [ ] **Step 3: Implement**

```swift
import Foundation

/// The viewer's remembered audio language and captions choice. `nil` means
/// "never chosen": leave the player's (and the system accessibility) default.
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

    /// Index of the option whose language matches `preferred` by primary subtag.
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

- [ ] **Step 4: Run** the tests — Expected: PASS.

- [ ] **Step 5: Wire `PlayerModel`.** Add `private var mediaPrefsApplied = false` (reset in `observe(item:)`), and in `handleItemStatus` on `.readyToPlay` call `Task { await applyMediaPrefs(to: item) }`:

```swift
    private func applyMediaPrefs(to item: AVPlayerItem) async {
        let prefs = MediaPrefs.load()
        if let audible = try? await item.asset.loadMediaSelectionGroup(for: .audible),
           let i = MediaPrefs.pickIndex(languages: audible.options.map(\.extendedLanguageTag), preferred: prefs.audioLanguage) {
            item.select(audible.options[i], in: audible)
        }
        if let on = prefs.captionsOn,
           let legible = try? await item.asset.loadMediaSelectionGroup(for: .legible) {
            let cc = legible.options.first { !$0.hasMediaCharacteristic(.containsOnlyForcedSubtitles) }
            item.select(on ? cc : nil, in: legible)
        }
        mediaPrefsApplied = true
    }
```
and observe changes (store the token with the other observers; remove in `tearDownObservers`):

```swift
        mediaSelectionObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.mediaSelectionDidChangeNotification, object: item, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.recordMediaSelection(item) }
        }
```
```swift
    private func recordMediaSelection(_ item: AVPlayerItem) {
        // Ignore the player's own default selection before ours is applied,
        // or it would overwrite the saved choice (Review Focus 4).
        guard mediaPrefsApplied else { return }
        Task {
            var prefs = MediaPrefs.load()
            if let audible = try? await item.asset.loadMediaSelectionGroup(for: .audible),
               let opt = item.currentMediaSelection.selectedMediaOption(in: audible) {
                prefs.audioLanguage = opt.extendedLanguageTag
            }
            if let legible = try? await item.asset.loadMediaSelectionGroup(for: .legible) {
                prefs.captionsOn = item.currentMediaSelection.selectedMediaOption(in: legible) != nil
            }
            prefs.save()
        }
    }
```
- [ ] **Step 6: Build both apps** `cd ios && xcodegen && xcodebuild -scheme Bowtie -destination 'generic/platform=iOS Simulator' build -quiet && xcodebuild -scheme BowtieTV -destination 'generic/platform=tvOS Simulator' build -quiet` — Expected: BUILD SUCCEEDED.
- [ ] **Step 7: Simulator check** (server from Task 7): extend `UITests/PlaybackUITests.swift` with a test that plays 9.1, opens the AVPlayerViewController audio/subtitle menu (`app.buttons["Audio & Subtitles"]` or the speech-bubble button — inspect `app.debugDescription` to find its identifier), selects "Spanish" and "English CC", screenshots; then relaunches, plays again, and asserts via screenshot that captions show. Run per `local-test-setup` (signed). Export screenshots with `xcrun xcresulttool export attachments`; look at them for on-screen caption sync with speech.
- [ ] **Step 8: Commit**

```bash
git add ios/
git commit -m "feat(ios,tvos): remember audio language and captions choice"
```

---

### Task 10: Android / Fire TV — audio and captions via Media3

**Files:**
- Create: `android/core/src/main/kotlin/app/bowtie/core/player/TrackPrefs.kt`, `android/core/src/test/kotlin/app/bowtie/core/TrackPrefsTest.kt`
- Modify: `android/core/src/main/kotlin/app/bowtie/core/player/PlayerEngine.kt`
- Modify: `android/app/src/main/kotlin/app/bowtie/ui/PlayerScreen.kt`, `android/tv/src/main/kotlin/app/bowtie/tv/ui/TvPlayerScreen.kt`

**Interfaces:**
- Produces:
  - `data class TrackPrefs(val audioLanguage: String? = null, val captionsOn: Boolean? = null)` with `fun encode(): String` and `companion fun decode(s: String?): TrackPrefs`
  - `data class AudioOption(val language: String?, val label: String)`
  - `fun audioLabel(language: String?, label: String?, index: Int): String`
  - `PlayerEngine.audioOptions(): List<AudioOption>`, `selectAudio(language: String?)`, `setCaptions(on: Boolean)`, `hasCaptions(): Boolean`, `captionsOn(): Boolean`

- [ ] **Step 1: Tests**

```kotlin
package app.bowtie.core

import app.bowtie.core.player.TrackPrefs
import app.bowtie.core.player.audioLabel
import kotlin.test.Test
import kotlin.test.assertEquals

class TrackPrefsTest {
    @Test fun roundTrip() {
        val p = TrackPrefs(audioLanguage = "es", captionsOn = true)
        assertEquals(p, TrackPrefs.decode(p.encode()))
        assertEquals(TrackPrefs(), TrackPrefs.decode(TrackPrefs().encode()))
    }

    @Test fun decodeGarbageIsEmpty() {
        assertEquals(TrackPrefs(), TrackPrefs.decode(null))
        assertEquals(TrackPrefs(), TrackPrefs.decode("{not json"))
    }

    @Test fun labels() {
        assertEquals("Español", audioLabel("es", "Español", 1))
        assertEquals("es", audioLabel("es", null, 1))
        assertEquals("Audio 2", audioLabel(null, null, 1))
    }
}
```

- [ ] **Step 2: Run** `cd android && ./gradlew :core:testDebugUnitTest --tests '*TrackPrefsTest*'` — Expected: FAIL (unresolved reference).

- [ ] **Step 3: Implement `TrackPrefs.kt`** (use `org.json.JSONObject` only if core tests already run with Android's JSON available; otherwise a two-field `key=value;key=value` encoding):

```kotlin
package app.bowtie.core.player

/** Remembered audio language / captions choice; null = never chosen (player default). */
data class TrackPrefs(val audioLanguage: String? = null, val captionsOn: Boolean? = null) {
    fun encode(): String = "a=${audioLanguage.orEmpty()};c=${captionsOn?.toString().orEmpty()}"

    companion object {
        fun decode(s: String?): TrackPrefs {
            if (s == null) return TrackPrefs()
            val m = s.split(';').mapNotNull {
                val kv = it.split('=', limit = 2)
                if (kv.size == 2) kv[0] to kv[1] else null
            }.toMap()
            if (!m.containsKey("a") || !m.containsKey("c")) return TrackPrefs()
            return TrackPrefs(
                audioLanguage = m["a"]?.ifEmpty { null },
                captionsOn = m["c"]?.toBooleanStrictOrNull(),
            )
        }
    }
}

data class AudioOption(val language: String?, val label: String)

fun audioLabel(language: String?, label: String?, index: Int): String =
    label?.takeIf { it.isNotBlank() } ?: language?.takeIf { it.isNotBlank() } ?: "Audio ${index + 1}"
```

- [ ] **Step 4: Run** the test — Expected: PASS.

- [ ] **Step 5: Engine.** `PlayerEngine` takes a `prefsStore: (TrackPrefs?) -> TrackPrefs` pair or simpler: constructor params `loadPrefs: () -> TrackPrefs = { TrackPrefs() }`, `savePrefs: (TrackPrefs) -> Unit = {}`. In `init` after building the player, apply:

```kotlin
    private fun applyPrefs(p: TrackPrefs) {
        val b = player.trackSelectionParameters.buildUpon()
        p.audioLanguage?.let { b.setPreferredAudioLanguage(it) }
        when (p.captionsOn) {
            true -> b.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false).setPreferredTextLanguage("en")
            false -> b.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            null -> {}
        }
        player.trackSelectionParameters = b.build()
    }

    fun audioOptions(): List<AudioOption> =
        player.currentTracks.groups.filter { it.type == C.TRACK_TYPE_AUDIO }.mapIndexed { i, g ->
            val f = g.getTrackFormat(0)
            AudioOption(f.language, audioLabel(f.language, f.label, i))
        }

    fun selectAudio(language: String?) {
        val p = loadPrefs().copy(audioLanguage = language)
        savePrefs(p)
        applyPrefs(p)
    }

    fun hasCaptions(): Boolean = player.currentTracks.groups.any { it.type == C.TRACK_TYPE_TEXT }

    fun captionsOn(): Boolean = player.currentTracks.groups.any { it.type == C.TRACK_TYPE_TEXT && it.isSelected }

    fun setCaptions(on: Boolean) {
        val p = loadPrefs().copy(captionsOn = on)
        savePrefs(p)
        applyPrefs(p)
    }
```
Phone and TV apps pass `SharedPreferences`-backed lambdas (`context.getSharedPreferences("bowtie.player", MODE_PRIVATE)`, key `trackPrefs`). Add a `onTracksChanged` listener callback `onTracksAvailable()` so screens refresh their chips.

- [ ] **Step 6: UI.** Phone `PlayerScreen.kt`: next to the Quality `ControlChip`, show `ControlChip("Audio: <label>")` (when `audioOptions().size > 1`) opening a `DropdownMenu` of options → `selectAudio(option.language)`, and `ControlChip(if (captionsOn) "CC on" else "CC")` (when `hasCaptions()`) → `setCaptions(!captionsOn)`. TV `TvPlayerScreen.kt`: add the same two entries to the transport/quality drawer (DPAD_CENTER long / MENU, see `PlayerKeyHandler.kt:69`) as focusable rows. `PlayerView` renders WebVTT through its built-in `SubtitleView` — verify it is not hidden (`useController`/`subtitleView` visibility).
- [ ] **Step 7: Run** `cd android && ./gradlew :core:testDebugUnitTest :app:assembleDebug :tv:assembleDebug` — Expected: BUILD SUCCESSFUL. If an Android emulator is available (`emulator -list-avds`), install the phone app, play 9.1 against the Task 7 server, switch to Español and CC, screenshot (`adb exec-out screencap -p`). Otherwise ledger "Android verified by build only".
- [ ] **Step 8: Commit**

```bash
git add android/
git commit -m "feat(android,firetv): audio track and captions controls"
```

---

### Task 11: Roku — verify built-in track menus

Roku's Video node offers audio tracks and captions in its `*` options overlay for HLS renditions. No code unless the check fails.

- [ ] **Step 1:** Confirm `roku/components/PlayerScene.bs` does not swallow the `options` key (grep `"options"` in its `onKeyEvent`; today it has none). If it does, return `false` for `options` so the system overlay opens.
- [ ] **Step 2:** Add to the release notes a Roku check for the user (sideload; no Roku developer account): during 9.1 playback press `*` → Audio shows English/Español, Closed captioning toggles captions.
- [ ] **Step 3:** Commit only if Step 1 changed code (`git commit -m "fix(roku): let the options key reach the system track menu"`).

---

### Task 12: Docs, changelog, release

**Files:** `CHANGELOG.md`, `docs/dev/fake-hdhomerun.md` (if it mentions `live.m3u8`), `README.md` (env var `BOWTIE_MULTITRACK`).

- [ ] **Step 1:** `grep -rn "live.m3u8\|seg%05d\|seg00000" docs README.md` and update names.
- [ ] **Step 2:** CHANGELOG `## 0.8.0`: captions (WebVTT from broadcast CC) and alternate audio in every app; `BOWTIE_MULTITRACK=off` kill switch; automatic fallback to video + first audio; Roku: use `*` menu; QSV note: user check on TrueNAS (press CC on 9.1, pick Español).
- [ ] **Step 3:** Run every suite: `cd server && go test ./... && golangci-lint run ./...`; `cd web && npm test && npm run lint`; `cd ios/BowtieKit && swift test`; `cd android && ./gradlew :core:testDebugUnitTest`. All must pass (check exit codes).
- [ ] **Step 4:** Commit, push branch, PR, wait for checks green (`gh pr checks --json bucket`), merge, fetch tags, abort if `v0.8.0` exists or the merge SHA is empty, tag `v0.8.0`, push tag, confirm the ghcr image builds.

```bash
git add CHANGELOG.md README.md docs/
git commit -m "docs: 0.8.0 changelog (captions + alternate audio)"
```
