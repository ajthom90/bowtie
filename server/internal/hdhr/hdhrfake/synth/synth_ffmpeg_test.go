//go:build ffmpeg

package synth

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

func ffmpegBin() string {
	if p := os.Getenv("BOWTIE_FFMPEG_PATH"); p != "" {
		return p
	}
	return "ffmpeg"
}

func TestGenerateLoadsAsSource(t *testing.T) {
	want := map[Preset]string{P480i: "720,480", P720p: "1280,720", P1080i: "1920,1080"}
	for preset, size := range want {
		preset, size := preset, size
		t.Run(string(preset), func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(t.TempDir(), string(preset)+".ts")
			if err := Generate(context.Background(), ffmpegBin(), preset, 3*time.Second, out); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			src, err := tsloop.LoadFile(out)
			if err != nil {
				t.Fatalf("LoadFile: %v", err)
			}
			if d := src.LoopDuration(); d < 2900*time.Millisecond || d > 3200*time.Millisecond {
				t.Fatalf("LoopDuration = %v, want ≈ 3s", d)
			}
			probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,width,height",
				"-of", "csv=p=0", out).Output()
			if err != nil {
				t.Fatalf("ffprobe: %v", err)
			}
			got := string(probe)
			if !strings.Contains(got, "mpeg2video,"+size) || !strings.Contains(got, "ac3") {
				t.Fatalf("ffprobe = %q, want mpeg2video %s + ac3", got, size)
			}
		})
	}
}

func TestCachedReusesFile(t *testing.T) {
	dir := t.TempDir()
	p1, err := Cached(context.Background(), ffmpegBin(), P480i, 2*time.Second, dir)
	if err != nil {
		t.Fatal(err)
	}
	st1, _ := os.Stat(p1)
	p2, err := Cached(context.Background(), ffmpegBin(), P480i, 2*time.Second, dir)
	if err != nil || p2 != p1 {
		t.Fatalf("second Cached = %q, %v", p2, err)
	}
	st2, _ := os.Stat(p2)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("cached file was regenerated")
	}
}
