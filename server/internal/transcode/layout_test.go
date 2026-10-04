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
		Rungs:      transcode.Ladder(720),
		Audio:      []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}},
		AudioKbps:  128,
		Captions:   true,
		AC3Copy:    true,
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
