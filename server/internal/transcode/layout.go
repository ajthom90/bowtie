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

func (l Layout) withRungs(r []Rung) Layout {
	l.Rungs = r
	return l
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
