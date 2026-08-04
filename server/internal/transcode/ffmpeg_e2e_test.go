//go:build ffmpeg

package transcode_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func ffmpegBin() string {
	if p := os.Getenv("BOWTIE_FFMPEG_PATH"); p != "" {
		return p
	}
	return "ffmpeg"
}

func ffprobeBin() string {
	// Prefer sibling of BOWTIE_FFMPEG_PATH when set.
	if p := os.Getenv("BOWTIE_FFMPEG_PATH"); p != "" {
		dir := filepath.Dir(p)
		cand := filepath.Join(dir, "ffprobe")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "ffprobe"
}

// TestCommandSoftwareE2E generates a short interlaced MPEG-TS fixture, runs
// the software HLS pipeline, and asserts playlist/segments plus codecs.
func TestCommandSoftwareE2E(t *testing.T) {
	runCommandE2E(t, transcode.BackendSoftware, "libx264")
}

// TestCommandVideoToolboxE2E is darwin-only hardware path.
func TestCommandVideoToolboxE2E(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("videotoolbox only on darwin")
	}
	runCommandE2E(t, transcode.BackendVideoToolbox, "h264_videotoolbox")
}

func runCommandE2E(t *testing.T, backend transcode.Backend, encoder string) {
	t.Helper()
	ffmpeg := ffmpegBin()
	ffprobe := ffprobeBin()

	tmp := t.TempDir()
	input := filepath.Join(tmp, "input.ts")
	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Self-contained fixture: interlaced MPEG-2 + AC-3 in MPEG-TS (no hdhrfake).
	genCtx, genCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer genCancel()
	gen := exec.CommandContext(genCtx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=duration=5:size=720x480:rate=29.97",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5",
		"-c:v", "mpeg2video", "-b:v", "2M", "-flags", "+ilme+ildct",
		"-c:a", "ac3", "-b:a", "192k",
		"-f", "mpegts", input,
	)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate input: %v\n%s", err, out)
	}

	low, ok := transcode.ProfileByName(transcode.DefaultProfiles(), "low")
	if !ok {
		t.Fatal("low profile missing")
	}
	spec := transcode.JobSpec{
		InputURL: input,
		OutDir:   outDir,
		D: transcode.Decision{
			VideoCodec:   "h264",
			VideoEncoder: encoder,
			AudioCopy:    false, // aac
			Profile:      low,
			Backend:      backend,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := transcode.Command(ctx, ffmpeg, spec)
	if err := cmd.Run(); err != nil {
		// File input should exit 0 when finished; surface failure clearly.
		t.Fatalf("ffmpeg %s: %v", backend, err)
	}

	playlist := filepath.Join(outDir, "live.m3u8")
	if _, err := os.Stat(playlist); err != nil {
		t.Fatalf("live.m3u8 missing: %v", err)
	}

	segs, err := filepath.Glob(filepath.Join(outDir, "seg*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 1 {
		t.Fatal("want ≥1 seg*.ts")
	}

	// Probe first segment for codecs.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer probeCancel()
	probe := exec.CommandContext(probeCtx, ffprobe,
		"-v", "error",
		"-show_entries", "stream=codec_name",
		"-of", "csv=p=0",
		segs[0],
	)
	out, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	codecs := strings.TrimSpace(string(out))
	// csv=p=0 prints one codec per line typically.
	lines := strings.FieldsFunc(codecs, func(r rune) bool { return r == '\n' || r == '\r' })
	hasH264, hasAAC := false, false
	for _, c := range lines {
		c = strings.TrimSpace(c)
		if c == "h264" {
			hasH264 = true
		}
		if c == "aac" {
			hasAAC = true
		}
	}
	if !hasH264 || !hasAAC {
		t.Fatalf("segment codecs = %q (lines %v), want h264 and aac", codecs, lines)
	}
	t.Logf("backend=%s encoder=%s segs=%d codecs=%v", backend, encoder, len(segs), lines)
}
