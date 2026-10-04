//go:build ffmpeg

package dvr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func softwareDecision() (transcode.Decision, error) {
	return transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264", Backend: transcode.BackendSoftware,
		Profile: transcode.Profile{Name: "high", Height: 720, VideoKbps: 4000, AudioKbps: 160}}, nil
}

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
	conv := FFmpegConverter{FFmpegPath: ff, Decide: softwareDecision}
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

// A 1080i MPEG-2 broadcast converts to 1920x1080 progressive at the 1080p
// setting and to 1280x720 at the default; both VODs decode end to end.
func TestConvert1080iQualityE2E(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	fp := filepath.Join(filepath.Dir(ff), "ffprobe")
	if _, err := os.Stat(fp); err != nil {
		if fp, err = exec.LookPath("ffprobe"); err != nil {
			t.Skip("ffprobe not installed")
		}
	}
	dir := t.TempDir()
	part := filepath.Join(dir, "part-001.ts")
	gen := exec.Command(ff, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30000/1001",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "3", "-c:v", "mpeg2video", "-b:v", "8M", "-flags", "+ildct+ilme", "-top", "1",
		"-c:a", "ac3", "-f", "mpegts", part)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate part: %v\n%s", err, out)
	}
	// The fixture really is interlaced, so "progressive" below means something.
	if got := probeVideo(t, fp, part); got != "mpeg2video,1920,1080,tt" {
		t.Fatalf("fixture = %s, want interlaced 1080 MPEG-2", got)
	}
	if h := sourceHeight(part); h != 1080 {
		t.Fatalf("sourceHeight = %d, want 1080", h)
	}

	for _, c := range []struct {
		quality, rendition, want string
	}{
		{settings.DVRQuality1080p, "v1080", "h264,1920,1080,progressive"},
		{settings.DVRQuality720p, "v720", "h264,1280,720,progressive"},
	} {
		t.Run(c.quality, func(t *testing.T) {
			conv := FFmpegConverter{FFmpegPath: ff, Decide: softwareDecision,
				Quality: func() (string, error) { return c.quality, nil }}
			out := filepath.Join(dir, "hls-"+c.quality)
			dur, err := conv.Convert(context.Background(), []string{part}, out)
			if err != nil {
				t.Fatal(err)
			}
			if dur < 2*time.Second || dur > 4*time.Second {
				t.Fatalf("duration %v, want ~3s", dur)
			}
			master, err := os.ReadFile(filepath.Join(out, MasterName))
			if err != nil || !strings.Contains(string(master), "\n"+c.rendition+".m3u8\n") {
				t.Fatalf("master (%v):\n%s", err, master)
			}
			if got := vodVideoPlaylist(out); got != filepath.Join(out, c.rendition+".m3u8") {
				t.Fatalf("video playlist %s", got)
			}
			if got := probeVideo(t, fp, filepath.Join(out, c.rendition+"_00000.ts")); got != c.want {
				t.Fatalf("segment = %s, want %s", got, c.want)
			}
			// Plays: the whole VOD decodes from the master without errors.
			play := exec.Command(ff, "-hide_banner", "-v", "error", "-i", filepath.Join(out, MasterName), "-f", "null", "-")
			if o, err := play.CombinedOutput(); err != nil || len(strings.TrimSpace(string(o))) > 0 {
				t.Fatalf("decode VOD: %v\n%s", err, o)
			}
		})
	}
}

// probeVideo is the first video stream's "codec,width,height,field_order".
func probeVideo(t *testing.T, ffprobe, file string) string {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height,field_order", "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", file, err)
	}
	// MPEG-TS lists the stream again under its program; the first line will do.
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.Trim(strings.TrimSpace(line), ","); line != "" {
			return line
		}
	}
	return ""
}
