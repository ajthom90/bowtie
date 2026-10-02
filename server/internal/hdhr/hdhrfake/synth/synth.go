// Package synth generates ATSC-like test streams (MPEG-2 video + AC-3 audio in
// MPEG-TS) with FFmpeg, so the fake HDHomeRun can broadcast realistic formats
// without committing large binaries or copyrighted captures.
package synth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Preset is a broadcast video format.
type Preset string

const (
	P480i  Preset = "480i"
	P720p  Preset = "720p"
	P1080i Preset = "1080i"
)

type format struct {
	size, rate string
	video      []string
}

var formats = map[Preset]format{
	P480i:  {"720x480", "30000/1001", []string{"-b:v", "3M", "-flags", "+ilme+ildct", "-top", "1"}},
	P720p:  {"1280x720", "60000/1001", []string{"-b:v", "10M"}},
	P1080i: {"1920x1080", "30000/1001", []string{"-b:v", "15M", "-maxrate", "19M", "-bufsize", "9781k", "-flags", "+ilme+ildct", "-top", "1"}},
}

// Args returns the FFmpeg argv (without the binary) that renders preset p for
// dur into out.
func Args(p Preset, dur time.Duration, out string) []string {
	f, ok := formats[p]
	if !ok {
		return nil
	}
	secs := strconv.FormatFloat(dur.Seconds(), 'f', 3, 64)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=" + f.size + ":rate=" + f.rate,
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000",
		"-t", secs,
		"-c:v", "mpeg2video", "-g", "15", "-bf", "2",
	}
	args = append(args, f.video...)
	return append(args,
		"-c:a", "ac3", "-b:a", "384k", "-ac", "2",
		"-f", "mpegts", "-mpegts_pmt_start_pid", "4096", "-mpegts_start_pid", "256",
		out,
	)
}

// Generate renders preset p for dur into out.
func Generate(ctx context.Context, ffmpegPath string, p Preset, dur time.Duration, out string) error {
	args := Args(p, dur, out)
	if args == nil {
		return fmt.Errorf("synth: unknown preset %q", p)
	}
	tmp := out + ".tmp.ts"
	args[len(args)-1] = tmp
	if b, err := exec.CommandContext(ctx, ffmpegPath, args...).CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("synth: ffmpeg %s: %w: %s", p, err, b)
	}
	return os.Rename(tmp, out)
}

// Cached returns dir/<preset>-<dur>.ts, generating it only if missing.
func Cached(ctx context.Context, ffmpegPath string, p Preset, dur time.Duration, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.ts", p, dur))
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return path, nil
	}
	if err := Generate(ctx, ffmpegPath, p, dur, path); err != nil {
		return "", err
	}
	return path, nil
}
