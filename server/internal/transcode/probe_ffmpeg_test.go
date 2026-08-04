//go:build ffmpeg

package transcode_test

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// TestProbeRealFFmpeg exercises the real binary. Requires -tags ffmpeg and a
// working ffmpeg on PATH (or BOWTIE_FFMPEG_PATH). Asserts software is always
// available; on darwin, videotoolbox is expected when the build enables it.
func TestProbeRealFFmpeg(t *testing.T) {
	ffmpegPath := "ffmpeg"
	if p := os.Getenv("BOWTIE_FFMPEG_PATH"); p != "" {
		ffmpegPath = p
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	caps := transcode.Probe(ctx, ffmpegPath)
	t.Logf("Probe result: version=%q available=%v hevc=%v", caps.FFmpegVersion, caps.Available, caps.HEVC)

	if caps.FFmpegVersion == "" {
		t.Fatal("FFmpegVersion empty; is ffmpeg installed and runnable?")
	}
	if !containsBackend(caps.Available, transcode.BackendSoftware) {
		t.Fatalf("Available = %v, want software (libx264) when real ffmpeg is present", caps.Available)
	}

	if runtime.GOOS == "darwin" {
		if !containsBackend(caps.Available, transcode.BackendVideoToolbox) {
			t.Errorf("Available = %v, want videotoolbox on darwin", caps.Available)
		}
	}

	// Select auto should return the highest-ranked available backend.
	sel, err := caps.Select("auto")
	if err != nil {
		t.Fatalf("Select(auto): %v", err)
	}
	if sel != caps.Available[0] {
		t.Errorf("Select(auto) = %s, want %s", sel, caps.Available[0])
	}
	t.Logf("selected backend: %s", sel)
}
