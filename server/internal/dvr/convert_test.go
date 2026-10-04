package dvr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestPlaylistDuration(t *testing.T) {
	pl := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.006000,\nv720_00000.ts\n#EXTINF:5.5,\nv720_00001.ts\n#EXT-X-ENDLIST\n"
	if d := playlistDuration([]byte(pl)); d != 11506*time.Millisecond {
		t.Fatalf("duration %v", d)
	}
}

func TestVODMasterIsRelative(t *testing.T) {
	m := vodMaster(vodLayout(vodRung(settings.DVRQuality720p, 1080)))
	if !strings.Contains(m, "\nv720.m3u8\n") || !strings.Contains(m, `URI="aac0.m3u8"`) || !strings.Contains(m, "RESOLUTION=1280x720") {
		t.Fatalf("master:\n%s", m)
	}
}

func TestVODMaster1080(t *testing.T) {
	m := vodMaster(vodLayout(vodRung(settings.DVRQuality1080p, 1080)))
	if !strings.Contains(m, "\nv1080.m3u8\n") || !strings.Contains(m, "RESOLUTION=1920x1080") || !strings.Contains(m, `URI="aac0.m3u8"`) {
		t.Fatalf("master:\n%s", m)
	}
}

func TestVODRung(t *testing.T) {
	cases := []struct {
		quality string
		src     int
		want    transcode.Rung
	}{
		// 720p is today's output whatever the broadcast.
		{settings.DVRQuality720p, 1080, transcode.Rung{Height: 720, VideoKbps: 4000}},
		{settings.DVRQuality720p, 720, transcode.Rung{Height: 720, VideoKbps: 4000}},
		{settings.DVRQuality720p, 480, transcode.Rung{Height: 720, VideoKbps: 4000}},
		{settings.DVRQuality720p, 0, transcode.Rung{Height: 720, VideoKbps: 4000}},
		// 1080p keeps a 1080-line broadcast (some encoders write 1088).
		{settings.DVRQuality1080p, 1080, transcode.Rung{Height: 1080, VideoKbps: 8000}},
		{settings.DVRQuality1080p, 1088, transcode.Rung{Height: 1080, VideoKbps: 8000}},
		// Unknown height (H.264 video): as the live single-rung rule.
		{settings.DVRQuality1080p, 0, transcode.Rung{Height: 1080, VideoKbps: 8000}},
		// 720p broadcasts aren't upscaled; they get more bits instead.
		{settings.DVRQuality1080p, 720, transcode.Rung{Height: 720, VideoKbps: 6000}},
		// Never worse than 720p mode.
		{settings.DVRQuality1080p, 480, transcode.Rung{Height: 720, VideoKbps: 4000}},
		// Unknown setting: the default.
		{"", 1080, transcode.Rung{Height: 720, VideoKbps: 4000}},
		{"4k", 1080, transcode.Rung{Height: 720, VideoKbps: 4000}},
	}
	for _, c := range cases {
		if got := vodRung(c.quality, c.src); got != c.want {
			t.Errorf("vodRung(%q, %d) = %+v, want %+v", c.quality, c.src, got, c.want)
		}
	}
}

// The 1080p VOD deinterlaces and scales with the same filters as live
// transcodes, at its own bitrate.
func TestVOD1080Args(t *testing.T) {
	d := transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264", Backend: transcode.BackendSoftware}
	args := strings.Join(transcode.BuildArgs(transcode.JobSpec{InputURL: "parts.txt", OutDir: "/out", D: d,
		Layout: vodLayout(vodRung(settings.DVRQuality1080p, 1080)), VOD: true}), " ")
	for _, want := range []string{"[0:v]yadif=0:-1:0,scale=-2:1080[v0]", "-b:v:0 8000k", "-b:a:0 160k", "name:v1080", "-hls_playlist_type vod"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q:\n%s", want, args)
		}
	}
}

func TestVODVideoPlaylist(t *testing.T) {
	dir := t.TempDir()
	// No master (or one without variants, as older test fixtures write): v720.
	if got := vodVideoPlaylist(dir); got != filepath.Join(dir, "v720.m3u8") {
		t.Fatalf("no master: %s", got)
	}
	_ = os.WriteFile(filepath.Join(dir, MasterName), []byte("#EXTM3U\n"), 0o644)
	if got := vodVideoPlaylist(dir); got != filepath.Join(dir, "v720.m3u8") {
		t.Fatalf("empty master: %s", got)
	}
	_ = os.WriteFile(filepath.Join(dir, MasterName), []byte(vodMaster(vodLayout(vodRung(settings.DVRQuality1080p, 1080)))), 0o644)
	if got := vodVideoPlaylist(dir); got != filepath.Join(dir, "v1080.m3u8") {
		t.Fatalf("1080 master: %s", got)
	}
	_ = os.WriteFile(filepath.Join(dir, MasterName), []byte(vodMaster(vodLayout(vodRung(settings.DVRQuality720p, 1080)))), 0o644)
	if got := vodVideoPlaylist(dir); got != filepath.Join(dir, "v720.m3u8") {
		t.Fatalf("720 master: %s", got)
	}
	// A master pointing outside the folder is ignored.
	_ = os.WriteFile(filepath.Join(dir, MasterName), []byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n../../etc/passwd\n"), 0o644)
	if got := vodVideoPlaylist(dir); got != filepath.Join(dir, "v720.m3u8") {
		t.Fatalf("escaping master: %s", got)
	}
}

func TestConverterQuality(t *testing.T) {
	if q := (FFmpegConverter{}).quality(); q != settings.DVRQuality720p {
		t.Fatalf("nil hook: %q", q)
	}
	c := FFmpegConverter{Quality: func() (string, error) { return "", errors.New("db") }}
	if q := c.quality(); q != settings.DVRQuality720p {
		t.Fatalf("error: %q", q)
	}
	c.Quality = func() (string, error) { return settings.DVRQuality1080p, nil }
	if q := c.quality(); q != settings.DVRQuality1080p {
		t.Fatalf("1080p: %q", q)
	}
}

// A 1080p VOD's duration comes from v1080.m3u8 (what the comskip worker and
// the restart path read), found through the master.
func TestVODDurationFindsRendition(t *testing.T) {
	dir := t.TempDir()
	hls := filepath.Join(dir, hlsDir)
	_ = os.MkdirAll(hls, 0o755)
	_ = os.WriteFile(filepath.Join(hls, MasterName), []byte(vodMaster(vodLayout(vodRung(settings.DVRQuality1080p, 1080)))), 0o644)
	_ = os.WriteFile(filepath.Join(hls, "v1080.m3u8"), []byte("#EXTM3U\n#EXTINF:6.0,\nv1080_00000.ts\n#EXTINF:4.5,\nv1080_00001.ts\n#EXT-X-ENDLIST\n"), 0o644)
	if d := vodDuration(store.Recording{Dir: dir, DurationSec: 99}); d != 10.5 {
		t.Fatalf("duration %v, want 10.5", d)
	}
}
