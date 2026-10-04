//go:build ffmpeg

package dvr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// Two capture parts (as after a dropped stream) become one VOD with the
// combined duration.
func TestConvertTwoPartsE2E(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	var parts []string
	for i, sec := range []string{"4", "3"} {
		p := filepath.Join(dir, "part-00"+string(rune('1'+i))+".ts")
		gen := exec.Command(ff, "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc2=size=640x480:rate=30000/1001",
			"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", sec, "-c:v", "mpeg2video", "-c:a", "ac3", "-f", "mpegts", p)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("generate part: %v\n%s", err, out)
		}
		parts = append(parts, p)
	}
	conv := FFmpegConverter{FFmpegPath: ff, Decide: func() (transcode.Decision, error) {
		return transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264", Backend: transcode.BackendSoftware,
			Profile: transcode.Profile{Name: "high", Height: 720, VideoKbps: 4000, AudioKbps: 160}}, nil
	}}
	out := filepath.Join(dir, "hls")
	dur, err := conv.Convert(context.Background(), parts, out)
	if err != nil {
		t.Fatal(err)
	}
	if dur < 6*time.Second || dur > 8*time.Second {
		t.Fatalf("duration %v, want ~7s", dur)
	}
	for _, f := range []string{MasterName, "v720.m3u8", "aac0.m3u8", "v720_00000.ts"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
}
