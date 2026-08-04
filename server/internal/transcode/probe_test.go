package transcode_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// writeFakeFFmpeg installs a shell script that pretends to be ffmpeg.
// successEncoders lists -c:v names that should exit 0; all others exit 1.
// -version always succeeds and prints a fixed banner.
func writeFakeFFmpeg(t *testing.T, dir string, successEncoders []string) string {
	t.Helper()
	var cases strings.Builder
	for _, e := range successEncoders {
		cases.WriteString("  ")
		cases.WriteString(e)
		cases.WriteString(") exit 0 ;;\n")
	}
	script := `#!/bin/sh
# Fake ffmpeg for unit tests (no real encode).
args="$*"
if echo " $args " | grep -q ' -version '; then
  echo "ffmpeg version 6.1.1-fake Copyright (c) 2000-2024 the FFmpeg developers"
  exit 0
fi
encoder=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-c:v" ]; then
    encoder="$arg"
    break
  fi
  prev="$arg"
done
case "$encoder" in
` + cases.String() + `  *) exit 1 ;;
esac
`
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsBackend(list []transcode.Backend, b transcode.Backend) bool {
	for _, x := range list {
		if x == b {
			return true
		}
	}
	return false
}

func TestProbeSoftwareOnly(t *testing.T) {
	dir := t.TempDir()
	ffmpeg := writeFakeFFmpeg(t, dir, []string{"libx264", "libx265"})

	caps := transcode.Probe(context.Background(), ffmpeg)

	if caps.FFmpegVersion != "6.1.1-fake" {
		t.Errorf("FFmpegVersion = %q, want 6.1.1-fake", caps.FFmpegVersion)
	}
	if !containsBackend(caps.Available, transcode.BackendSoftware) {
		t.Errorf("Available = %v, want software present", caps.Available)
	}
	// Hardware backends must not be listed when their encoders fail.
	for _, hw := range []transcode.Backend{
		transcode.BackendVideoToolbox,
		transcode.BackendQSV,
		transcode.BackendNVENC,
		transcode.BackendVAAPI,
	} {
		if containsBackend(caps.Available, hw) {
			t.Errorf("Available unexpectedly contains %s", hw)
		}
	}
	if !caps.HEVC[transcode.BackendSoftware] {
		t.Error("HEVC[software] = false, want true")
	}
	// software should be last (or only) entry
	if n := len(caps.Available); n == 0 || caps.Available[n-1] != transcode.BackendSoftware {
		t.Errorf("Available last = %v, want software last", caps.Available)
	}
}

func TestProbeHardwareH264AndHEVC(t *testing.T) {
	dir := t.TempDir()
	// Enable the platform-primary HW encoder + software; HEVC only for HW.
	var success []string
	var wantHW transcode.Backend
	switch runtime.GOOS {
	case "darwin":
		wantHW = transcode.BackendVideoToolbox
		success = []string{"h264_videotoolbox", "hevc_videotoolbox", "libx264"}
	case "linux":
		// Only QSV succeeds among linux HW options.
		wantHW = transcode.BackendQSV
		success = []string{"h264_qsv", "hevc_qsv", "libx264"}
	default:
		t.Skip("no platform HW backend order for " + runtime.GOOS)
	}
	ffmpeg := writeFakeFFmpeg(t, dir, success)

	caps := transcode.Probe(context.Background(), ffmpeg)

	if !containsBackend(caps.Available, wantHW) {
		t.Fatalf("Available = %v, want %s", caps.Available, wantHW)
	}
	if !containsBackend(caps.Available, transcode.BackendSoftware) {
		t.Fatalf("Available = %v, want software", caps.Available)
	}
	// Ranked: HW before software.
	if caps.Available[0] != wantHW {
		t.Errorf("Available[0] = %s, want %s (ranked first)", caps.Available[0], wantHW)
	}
	if !caps.HEVC[wantHW] {
		t.Errorf("HEVC[%s] = false, want true", wantHW)
	}
	if caps.HEVC[transcode.BackendSoftware] {
		t.Error("HEVC[software] = true, want false (libx265 not allowed)")
	}
}

func TestProbeMissingBinary(t *testing.T) {
	caps := transcode.Probe(context.Background(), filepath.Join(t.TempDir(), "no-such-ffmpeg"))
	if caps.FFmpegVersion != "" {
		t.Errorf("FFmpegVersion = %q, want empty", caps.FFmpegVersion)
	}
	if len(caps.Available) != 0 {
		t.Errorf("Available = %v, want empty", caps.Available)
	}
}

func TestSelect(t *testing.T) {
	caps := transcode.Capabilities{
		Available: []transcode.Backend{transcode.BackendVideoToolbox, transcode.BackendSoftware},
		HEVC:      map[transcode.Backend]bool{transcode.BackendVideoToolbox: true},
	}

	got, err := caps.Select("auto")
	if err != nil {
		t.Fatalf("Select(auto): %v", err)
	}
	if got != transcode.BackendVideoToolbox {
		t.Errorf("Select(auto) = %s, want videotoolbox", got)
	}

	got, err = caps.Select("software")
	if err != nil {
		t.Fatalf("Select(software): %v", err)
	}
	if got != transcode.BackendSoftware {
		t.Errorf("Select(software) = %s, want software", got)
	}

	if _, err := caps.Select("nvenc"); err == nil {
		t.Error("Select(nvenc): want error, got nil")
	}

	empty := transcode.Capabilities{}
	if _, err := empty.Select("auto"); err == nil {
		t.Error("Select(auto) on empty Available: want error, got nil")
	}
}

func TestProbePassesInitFlagsForQSVAndVAAPI(t *testing.T) {
	// Ensure Probe invokes the documented init flags so a script that requires
	// them can succeed (guards against dropping -init_hw_device / -vf).
	if runtime.GOOS != "linux" {
		t.Skip("qsv/vaapi only probed on linux")
	}
	dir := t.TempDir()
	// Script exits 0 for h264_qsv only when -init_hw_device qsv=hw is present.
	script := `#!/bin/sh
if echo " $* " | grep -q ' -version '; then
  echo "ffmpeg version 6.1.1-fake Copyright (c) test"
  exit 0
fi
encoder=""; prev=""
for arg in "$@"; do
  if [ "$prev" = "-c:v" ]; then encoder="$arg"; break; fi
  prev="$arg"
done
case "$encoder" in
  h264_qsv)
    echo " $* " | grep -q -- ' -init_hw_device qsv=hw ' && exit 0
    exit 1
    ;;
  h264_vaapi)
    echo " $* " | grep -q -- ' -init_hw_device vaapi=va:/dev/dri/renderD128 ' || exit 1
    echo " $* " | grep -q -- ' -vf format=nv12,hwupload ' || exit 1
    exit 0
    ;;
  libx264) exit 0 ;;
  *) exit 1 ;;
esac
`
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	caps := transcode.Probe(context.Background(), path)
	if !containsBackend(caps.Available, transcode.BackendQSV) {
		t.Errorf("Available = %v, want qsv (init flags must be passed)", caps.Available)
	}
	if !containsBackend(caps.Available, transcode.BackendVAAPI) {
		t.Errorf("Available = %v, want vaapi (init flags must be passed)", caps.Available)
	}
}
