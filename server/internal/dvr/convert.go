package dvr

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// VOD output: one 720p rung plus the first audio track (as AAC); Bowtie's
// master playlist (index.m3u8) points at FFmpeg's v720.m3u8 and aac0.m3u8.
var vodLayout = transcode.Layout{
	Rungs:      []transcode.Rung{{Height: 720, VideoKbps: 4000}},
	VideoCodec: "h264",
	AudioKbps:  160,
}

// MasterName is the playlist clients open for a ready recording.
const MasterName = "index.m3u8"

func vodMaster() string { return transcode.MasterPlaylist(vodLayout, 0, "", false) }

// FFmpegConverter converts capture parts with the configured encoder.
type FFmpegConverter struct {
	FFmpegPath string
	// Decide picks the encoder/backend (as for a live 720p H.264 viewer).
	Decide func() (transcode.Decision, error)
}

// Convert writes an HLS VOD of parts into outDir and returns its duration.
func (c FFmpegConverter) Convert(ctx context.Context, parts []string, outDir string) (time.Duration, error) {
	if len(parts) == 0 {
		return 0, errors.New("no capture parts")
	}
	d, err := c.Decide()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	list := filepath.Join(outDir, "parts.txt")
	var lb strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&lb, "file '%s'\n", strings.ReplaceAll(p, "'", `'\''`))
	}
	if err := os.WriteFile(list, []byte(lb.String()), 0o644); err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(list) }()
	spec := transcode.JobSpec{
		InputURL: list,
		OutDir:   outDir,
		D:        d,
		Layout:   vodLayout,
		VOD:      true,
	}
	cmd := transcode.Command(ctx, c.FFmpegPath, spec)
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffmpeg: %w", err)
	}
	video, err := os.ReadFile(filepath.Join(outDir, vodLayout.TopName()+".m3u8"))
	if err != nil {
		return 0, fmt.Errorf("no video playlist: %w", err)
	}
	if !bytes.Contains(video, []byte("#EXT-X-ENDLIST")) {
		return 0, errors.New("video playlist is incomplete")
	}
	if err := os.WriteFile(filepath.Join(outDir, MasterName), []byte(vodMaster()), 0o644); err != nil {
		return 0, err
	}
	return playlistDuration(video), nil
}

// playlistDuration sums a media playlist's #EXTINF durations.
func playlistDuration(pl []byte) time.Duration {
	var total time.Duration
	sc := bufio.NewScanner(bytes.NewReader(pl))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		v := strings.TrimPrefix(line, "#EXTINF:")
		if i := strings.IndexByte(v, ','); i >= 0 {
			v = v[:i]
		}
		if sec, err := strconv.ParseFloat(v, 64); err == nil {
			total += time.Duration(sec * float64(time.Second))
		}
	}
	return total
}
