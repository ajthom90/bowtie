//go:build comskip

package dvr

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// A synthetic broadcast with a known commercial break goes through the real
// conversion and the real Comskip; the break comes back on the VOD's
// playback timeline. Run with: go test -tags comskip -run Comskip ./internal/dvr
// (needs ffmpeg and comskip on PATH, or BOWTIE_COMSKIP_PATH).
func TestComskipFindsBreakOnPlaybackTimelineE2E(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	cs := os.Getenv("BOWTIE_COMSKIP_PATH")
	if cs == "" {
		cs = "comskip"
	}
	if cs, err = exec.LookPath(cs); err != nil {
		t.Skip("comskip not installed")
	}
	dir := t.TempDir()
	// show 150 s | black 1 s | 4 x (ad 30 s | black 1 s) | show 150 s:
	// the break runs from 150 s to 275 s.
	type piece struct {
		video, audio string
		sec          int
	}
	black := piece{"color=c=black:", "anullsrc=r=48000:cl=stereo", 1}
	pieces := []piece{
		{"testsrc2", "sine=frequency=440:sample_rate=48000", 150}, black,
		{"smptehdbars", "sine=frequency=880:sample_rate=48000", 30}, black,
		{"mandelbrot", "sine=frequency=660:sample_rate=48000", 30}, black,
		{"rgbtestsrc", "sine=frequency=550:sample_rate=48000", 30}, black,
		{"testsrc", "sine=frequency=330:sample_rate=48000", 30}, black,
		{"testsrc2", "sine=frequency=440:sample_rate=48000", 150},
	}
	var parts []string
	for i, p := range pieces {
		path := filepath.Join(dir, fmt.Sprintf("part-%03d.ts", i+1))
		gen := exec.Command(ff, "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", p.video+sep(p.video)+"size=704x480:rate=30000/1001",
			"-f", "lavfi", "-i", p.audio,
			"-t", fmt.Sprint(p.sec), "-c:v", "mpeg2video", "-b:v", "4M", "-c:a", "ac3", "-f", "mpegts", path)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("generate %s: %v\n%s", p.video, err, out)
		}
		parts = append(parts, path)
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
	work := filepath.Join(dir, detectWorkDir)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	segs, err := ComskipDetector{Path: cs, DataDir: dir}.Detect(context.Background(), filepath.Join(out, MasterName), work)
	if err != nil {
		t.Fatal(err)
	}
	clean := CleanSegments(segs, dur.Seconds())
	t.Logf("duration %.2fs, EDL %v, cleaned %v", dur.Seconds(), segs, clean)
	if len(clean) != 1 || math.Abs(clean[0].Start-150) > 2 || math.Abs(clean[0].End-275) > 2 {
		t.Fatalf("breaks %v, want one at ~150-275 s", clean)
	}
}

// sep joins a lavfi source and its options ("testsrc2=" + opts, "color=c=black:" + opts).
func sep(src string) string {
	if src[len(src)-1] == ':' {
		return ""
	}
	return "="
}
