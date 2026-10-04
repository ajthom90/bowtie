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

	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// MasterName is the playlist clients open for a ready recording.
const MasterName = "index.m3u8"

// legacyVideoPlaylist is the 720p rendition every recording had before the
// quality setting (and the fallback when a master names none).
const legacyVideoPlaylist = "v720.m3u8"

// probeLimit bounds how much of the first capture part is read for the
// broadcast's height (its first sequence header is near the start).
const probeLimit = 8 << 20

// vodRung is the one video rendition for a quality setting and the broadcast's
// height (0 = unknown). 720p is the same for every channel. 1080p keeps a
// 1080-line broadcast (1080i is deinterlaced to 1080p), gives a 720p
// broadcast more bits instead of upscaling it, and is never worse than 720p.
func vodRung(quality string, srcHeight int) transcode.Rung {
	if srcHeight <= 0 {
		srcHeight = transcode.UnknownSourceHeight // as live: never upscale a guess
	}
	if quality == settings.DVRQuality1080p {
		switch {
		case srcHeight >= 1080:
			return transcode.Rung{Height: 1080, VideoKbps: 8000}
		case srcHeight >= 720:
			return transcode.Rung{Height: 720, VideoKbps: 6000}
		}
	}
	return transcode.Rung{Height: 720, VideoKbps: 4000}
}

// vodLayout is a VOD's output: one rung plus the first audio track (as AAC);
// Bowtie's master playlist (index.m3u8) points at FFmpeg's v<height>.m3u8
// and aac0.m3u8.
func vodLayout(r transcode.Rung) transcode.Layout {
	return transcode.Layout{
		Rungs:      []transcode.Rung{r},
		VideoCodec: "h264",
		AudioKbps:  160,
	}
}

func vodMaster(l transcode.Layout) string { return transcode.MasterPlaylist(l, 0, "", false) }

// vodVideoPlaylist is the path of a VOD's video playlist in dir: the first
// variant its master names, else v720.m3u8 (older recordings).
func vodVideoPlaylist(dir string) string {
	if m, err := os.ReadFile(filepath.Join(dir, MasterName)); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(m))
		variant := false
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			switch {
			case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
				variant = true
			case variant && line != "" && !strings.HasPrefix(line, "#"):
				if name, _, _ := strings.Cut(line, "?"); filepath.Base(name) == name && strings.HasSuffix(name, ".m3u8") {
					return filepath.Join(dir, name)
				}
				variant = false
			}
		}
	}
	return filepath.Join(dir, legacyVideoPlaylist)
}

// FFmpegConverter converts capture parts with the configured encoder.
type FFmpegConverter struct {
	FFmpegPath string
	// Decide picks the encoder/backend (as for a live 720p H.264 viewer).
	Decide func() (transcode.Decision, error)
	// Quality returns the dvr.quality setting, read at each conversion
	// (nil or an error: 720p).
	Quality func() (string, error)
}

func (c FFmpegConverter) quality() string {
	if c.Quality == nil {
		return settings.DVRQuality720p
	}
	q, err := c.Quality()
	if err != nil {
		return settings.DVRQuality720p
	}
	return q
}

// sourceHeight is the broadcast's MPEG-2 height from the first part (0 =
// unknown).
func sourceHeight(part string) int {
	f, err := os.Open(part)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	return stream.SourceHeightTS(f, probeLimit)
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
	quality := c.quality()
	src := 0
	if quality != settings.DVRQuality720p {
		src = sourceHeight(parts[0])
	}
	layout := vodLayout(vodRung(quality, src))
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
		Layout:   layout,
		VOD:      true,
	}
	cmd := transcode.Command(ctx, c.FFmpegPath, spec)
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffmpeg: %w", err)
	}
	video, err := os.ReadFile(filepath.Join(outDir, layout.TopName()+".m3u8"))
	if err != nil {
		return 0, fmt.Errorf("no video playlist: %w", err)
	}
	if !bytes.Contains(video, []byte("#EXT-X-ENDLIST")) {
		return 0, errors.New("video playlist is incomplete")
	}
	if err := os.WriteFile(filepath.Join(outDir, MasterName), []byte(vodMaster(layout)), 0o644); err != nil {
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
