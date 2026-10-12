//go:build ffmpeg

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

// ffprobePath is the host ffprobe (segments are plain MPEG-TS; any version works).
func ffprobePath() string {
	if p := os.Getenv("BOWTIE_FFPROBE_PATH"); p != "" {
		return p
	}
	return "ffprobe"
}

// FFprobe returns a testplayer probe listing the first stream's packets.
func FFprobe(bin string) testplayer.SegmentProbe {
	return func(ctx context.Context, seg []byte) ([]testplayer.Packet, error) {
		cmd := exec.CommandContext(ctx, bin, "-v", "error", "-select_streams", "0",
			"-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", "-i", "pipe:0")
		cmd.Stdin = bytes.NewReader(seg)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("ffprobe: %w", err)
		}
		return parseProbeCSV(out), nil
	}
}

// parseProbeCSV reads "pts_time,duration_time" lines, skipping blank or
// incomplete ones (ffprobe can print N/A or a trailing comma).
func parseProbeCSV(out []byte) []testplayer.Packet {
	var pk []testplayer.Packet
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(strings.TrimSpace(line), ","), ",")
		if len(f) < 2 {
			continue
		}
		pts, err1 := strconv.ParseFloat(f[0], 64)
		dur, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		pk = append(pk, testplayer.Packet{PTS: pts, Dur: dur})
	}
	return pk
}

func TestParseProbeCSV(t *testing.T) {
	got := parseProbeCSV([]byte("1.400000,0.021333\n\nN/A,0.021333\n1.421333,0.021333,\n"))
	if len(got) != 2 || got[0].PTS != 1.4 || got[1].Dur != 0.021333 {
		t.Fatalf("parsed %+v", got)
	}
}
