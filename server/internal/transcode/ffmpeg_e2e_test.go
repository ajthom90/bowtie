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
	runCommandE2E(t, transcode.BackendSoftware, "libx264", false)
}

// TestCommandVideoToolboxE2E is darwin-only hardware path.
func TestCommandVideoToolboxE2E(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("videotoolbox only on darwin")
	}
	runCommandE2E(t, transcode.BackendVideoToolbox, "h264_videotoolbox", false)
}

// TestCommandSoftwarePipeE2E feeds generated MPEG-TS via JobSpec.Stdin (pipe:0)
// instead of a file/URL — the ingest fan-out path used in production.
func TestCommandSoftwarePipeE2E(t *testing.T) {
	runCommandE2E(t, transcode.BackendSoftware, "libx264", true)
}

func runCommandE2E(t *testing.T, backend transcode.Backend, encoder string, pipeInput bool) {
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
	if pipeInput {
		f, err := os.Open(input)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		// Empty URL; BuildArgs must use pipe:0, not the path.
		spec.InputURL = ""
		spec.Stdin = f
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := transcode.Command(ctx, ffmpeg, spec)
	if pipeInput {
		if cmd.Stdin == nil {
			t.Fatal("pipe mode: Command must wire Stdin")
		}
		// Sanity: argv must not reference a file URL when pipe-fed.
		for _, a := range cmd.Args {
			if a == input {
				t.Fatalf("pipe mode must not pass input path in args: %v", cmd.Args)
			}
		}
	}
	if err := cmd.Run(); err != nil {
		// File/pipe input should exit 0 when finished; surface failure clearly.
		t.Fatalf("ffmpeg %s pipe=%v: %v", backend, pipeInput, err)
	}

	playlist := filepath.Join(outDir, "v480.m3u8")
	if _, err := os.Stat(playlist); err != nil {
		t.Fatalf("v480.m3u8 missing: %v", err)
	}

	segs, err := filepath.Glob(filepath.Join(outDir, "v480_*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) < 1 {
		t.Fatal("want ≥1 v480_*.ts")
	}
	audioSegs, err := filepath.Glob(filepath.Join(outDir, "aac0_*.ts"))
	if err != nil || len(audioSegs) < 1 {
		t.Fatalf("want ≥1 aac0_*.ts (err %v)", err)
	}

	// Probe first segment for codecs.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer probeCancel()
	var out []byte
	for _, f := range []string{segs[0], audioSegs[0]} {
		probe := exec.CommandContext(probeCtx, ffprobe,
			"-v", "error",
			"-show_entries", "stream=codec_name",
			"-of", "csv=p=0",
			f,
		)
		o, err := probe.Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", f, err)
		}
		out = append(out, o...)
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
	t.Logf("backend=%s encoder=%s pipe=%v segs=%d codecs=%v", backend, encoder, pipeInput, len(segs), lines)
}

// TestLadderSoftwareE2E runs a two-rung ladder and checks the rungs line up:
// same segment count and a keyframe at the same PTS at each segment start.
func TestLadderSoftwareE2E(t *testing.T) {
	ffmpeg, ffprobe := ffmpegBin(), ffprobeBin()
	tmp := t.TempDir()
	input := filepath.Join(tmp, "input.ts")
	outDir := filepath.Join(tmp, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gen := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=duration=10:size=720x480:rate=29.97",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=10",
		"-c:v", "mpeg2video", "-b:v", "2M", "-c:a", "ac3", "-b:a", "192k", "-f", "mpegts", input)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate input: %v\n%s", err, out)
	}
	spec := transcode.JobSpec{
		InputURL: input,
		OutDir:   outDir,
		D:        transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264", Backend: transcode.BackendSoftware},
		Layout:   transcode.Layout{Rungs: transcode.Ladder(480), AudioKbps: 96, VideoCodec: "h264"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := transcode.Command(ctx, ffmpeg, spec).Run(); err != nil {
		t.Fatalf("ffmpeg ladder: %v", err)
	}
	count := func(name string) int {
		b, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "#EXTINF")
	}
	if a, b := count("v480.m3u8"), count("v360.m3u8"); a != b || a < 2 {
		t.Fatalf("rung segment counts differ: v480=%d v360=%d", a, b)
	}
	first := func(seg string) string {
		out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v",
			"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", filepath.Join(outDir, seg)).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", seg, err)
		}
		return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	}
	a, b := first("v480_00001.ts"), first("v360_00001.ts")
	if a != b || !strings.Contains(a, "K") {
		t.Fatalf("segment 1 starts differ or not on a keyframe: v480=%q v360=%q", a, b)
	}
}
