//go:build ffmpeg

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rebase is one FFmpeg "timestamp discontinuity" line, offset from a start.
type rebase struct {
	At   time.Duration
	Line string
}

// ffLog is what the FFmpeg wrappers (scripts/ffmpeg51, scripts/ffmpeg-soak)
// logged to FF51_LOGDIR after a start.
type ffLog struct {
	Rebases []rebase
	// DupFrames sums FFmpeg 5.1's per-burst "*** N dup!" lines (verbose
	// level); MaxDupBurst is the largest N; DupWarnings counts "More than
	// 1000 frames duplicated".
	DupFrames, MaxDupBurst, DupWarnings int
	// Argv are the "argv:" lines, one per FFmpeg run.
	Argv []string
}

var dupRe = regexp.MustCompile(`\*\*\* (\d+) dup!`)

// logScanMax is the longest line a soak log reader accepts. A longer token
// is a read error, not a short log.
const logScanMax = 1024 * 1024

// ffLogSince reads FF51_LOGDIR for lines logged in [start, end], inclusive.
// FF51_LOGDIR unset is the zero ffLog and a nil error: the ordinary suites
// have no wrapper log. A missing directory is an empty log, not an error.
// An open, glob, or scanner failure is returned with no partial result.
func ffLogSince(start, end time.Time) (ffLog, error) {
	var out ffLog
	dir := os.Getenv("FF51_LOGDIR")
	if dir == "" {
		return out, nil
	}
	files, err := filepath.Glob(filepath.Join(dir, "ffmpeg-*.log"))
	if err != nil {
		return ffLog{}, fmt.Errorf("ffmpeg log: %w", err)
	}
	for _, path := range files {
		fh, err := os.Open(path)
		if err != nil {
			return ffLog{}, fmt.Errorf("ffmpeg log: %w", err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), logScanMax)
		for sc.Scan() {
			ms, rest, ok := strings.Cut(sc.Text(), " ")
			v, convErr := strconv.ParseInt(ms, 10, 64)
			if !ok || convErr != nil {
				continue
			}
			at := time.UnixMilli(v)
			if at.Before(start) || at.After(end) {
				continue
			}
			switch {
			case strings.HasPrefix(rest, "argv: "):
				out.Argv = append(out.Argv, strings.TrimPrefix(rest, "argv: "))
			// A re-base line names its new offset; option parsing at debug
			// level also prints "timestamp discontinuity delta threshold".
			case strings.Contains(rest, "timestamp discontinuity") && strings.Contains(rest, "new offset"):
				out.Rebases = append(out.Rebases, rebase{At: at.Sub(start), Line: rest})
			case strings.Contains(rest, "frames duplicated"):
				out.DupWarnings++
			default:
				if m := dupRe.FindStringSubmatch(rest); m != nil {
					n, _ := strconv.Atoi(m[1])
					out.DupFrames += n
					if n > out.MaxDupBurst {
						out.MaxDupBurst = n
					}
				}
			}
		}
		scanErr := sc.Err()
		_ = fh.Close()
		if scanErr != nil {
			return ffLog{}, fmt.Errorf("ffmpeg log %s: %w", path, scanErr)
		}
	}
	return out, nil
}

// rebasesSince is the re-bases of ffLogSince in [start, end].
func rebasesSince(start, end time.Time) ([]rebase, error) {
	log, err := ffLogSince(start, end)
	return log.Rebases, err
}

func TestFFLogSince(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FF51_LOGDIR", dir)
	start := time.UnixMilli(1_760_000_000_000)
	body := "1759999999000 [debug] [mpegts @ 0x1] timestamp discontinuity (stream id=0): -270000, new offset= 0\n" +
		"1760000000001 argv: -hide_banner -loglevel level+debug -i pipe:0 -dts_delta_threshold 2.6\n" +
		"1760000000200 [debug] Reading option '-dts_delta_threshold' ... matched as option 'dts_delta_threshold' (timestamp discontinuity delta threshold) with argument '2.6'.\n" +
		"1760000000200 [debug] Applying option dts_delta_threshold (timestamp discontinuity delta threshold) with argument 2.6.\n" +
		"1760000002500 [debug] timestamp discontinuity (stream id=0): 270000, new offset= -270000\n" +
		"1760000002600 [debug] timestamp discontinuity for stream #0:0 (id=256, type=video): -2000000, new offset= 2475967\n" +
		"1760000003000 [verbose] *** 2 dup!\n" +
		"1760000003100 [verbose] *** 87 dup!\n" +
		"1760000004000 [warning] More than 1000 frames duplicated\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg-1.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ffLogSince(start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rebases) != 2 || got.Rebases[0].At != 2500*time.Millisecond || got.Rebases[1].At != 2600*time.Millisecond {
		t.Fatalf("rebases %+v", got.Rebases)
	}
	if got.DupFrames != 89 || got.MaxDupBurst != 87 || got.DupWarnings != 1 {
		t.Fatalf("dups %d (max %d), warnings %d", got.DupFrames, got.MaxDupBurst, got.DupWarnings)
	}
	if len(got.Argv) != 1 || !strings.Contains(got.Argv[0], "-dts_delta_threshold 2.6") {
		t.Fatalf("argv %q", got.Argv)
	}
}
