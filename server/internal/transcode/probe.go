package transcode

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Backend is an FFmpeg encoder backend identifier.
type Backend string

const (
	BackendVideoToolbox Backend = "videotoolbox"
	BackendQSV          Backend = "qsv"
	BackendNVENC        Backend = "nvenc"
	BackendVAAPI        Backend = "vaapi"
	BackendSoftware     Backend = "software"
)

// Capabilities holds the result of probing FFmpeg encoder support.
// Probe is intended to be called once at startup; callers cache this value.
type Capabilities struct {
	Available     []Backend
	HEVC          map[Backend]bool
	FFmpegVersion string
}

const probeTimeout = 10 * time.Second

// encoderNames maps a backend to its H.264 and HEVC FFmpeg encoder names.
var encoderNames = map[Backend]struct {
	h264, hevc string
}{
	BackendVideoToolbox: {"h264_videotoolbox", "hevc_videotoolbox"},
	BackendQSV:          {"h264_qsv", "hevc_qsv"},
	BackendNVENC:        {"h264_nvenc", "hevc_nvenc"},
	BackendVAAPI:        {"h264_vaapi", "hevc_vaapi"},
	BackendSoftware:     {"libx264", "libx265"},
}

// platformBackends returns backends to probe in preference order for this OS.
// Software is always last as the last-resort fallback.
func platformBackends() []Backend {
	var order []Backend
	switch runtime.GOOS {
	case "darwin":
		order = []Backend{BackendVideoToolbox}
	case "linux":
		order = []Backend{BackendQSV, BackendNVENC, BackendVAAPI}
	}
	return append(order, BackendSoftware)
}

// backendInitFlags returns FFmpeg args inserted after the input and before -c:v.
func backendInitFlags(b Backend) []string {
	switch b {
	case BackendQSV:
		return []string{"-init_hw_device", "qsv=hw"}
	case BackendVAAPI:
		return []string{
			"-init_hw_device", "vaapi=va:/dev/dri/renderD128",
			"-vf", "format=nv12,hwupload",
		}
	default:
		return nil
	}
}

// Probe discovers which encoder backends work with the given ffmpeg binary.
// Each attempt has a 10s timeout. Results are returned in the Capabilities
// struct for the caller to cache (Probe itself is stateless).
func Probe(ctx context.Context, ffmpegPath string) Capabilities {
	caps := Capabilities{
		Available:     nil,
		HEVC:          make(map[Backend]bool),
		FFmpegVersion: ffmpegVersion(ctx, ffmpegPath),
	}

	for _, b := range platformBackends() {
		names := encoderNames[b]
		if !tryEncode(ctx, ffmpegPath, b, names.h264) {
			continue
		}
		caps.Available = append(caps.Available, b)
		if tryEncode(ctx, ffmpegPath, b, names.hevc) {
			caps.HEVC[b] = true
		}
	}
	return caps
}

// Select chooses a backend. forced=="auto" picks the first Available entry
// (highest ranked). Otherwise forced must name an available backend.
func (c Capabilities) Select(forced string) (Backend, error) {
	if forced == "auto" {
		if len(c.Available) == 0 {
			return "", fmt.Errorf("no encoder backends available")
		}
		return c.Available[0], nil
	}
	want := Backend(forced)
	for _, b := range c.Available {
		if b == want {
			return b, nil
		}
	}
	return "", fmt.Errorf("encoder backend %q not available", forced)
}

func ffmpegVersion(ctx context.Context, ffmpegPath string) string {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	// "ffmpeg version 8.0.1 Copyright ..." → "8.0.1"
	fields := strings.Fields(line)
	if len(fields) >= 3 && fields[0] == "ffmpeg" && fields[1] == "version" {
		return fields[2]
	}
	return strings.TrimSpace(line)
}

func tryEncode(ctx context.Context, ffmpegPath string, backend Backend, encoder string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := []string{
		"-hide_banner",
		"-f", "lavfi",
		"-i", "testsrc2=duration=0.5:size=320x240:rate=30",
	}
	args = append(args, backendInitFlags(backend)...)
	args = append(args, "-c:v", encoder, "-f", "null", "-")

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	// Discard output; success is exit code only.
	return cmd.Run() == nil
}
