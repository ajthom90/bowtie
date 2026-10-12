//go:build ffmpeg

package e2e

import (
	"bufio"
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

// ffLogSince reads FF51_LOGDIR for lines logged at or after start. The
// zero ffLog when FF51_LOGDIR is unset.
func ffLogSince(t testing.TB, start time.Time) ffLog {
	t.Helper()
	var out ffLog
	dir := os.Getenv("FF51_LOGDIR")
	if dir == "" {
		return out
	}
	files, _ := filepath.Glob(filepath.Join(dir, "ffmpeg-*.log"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			ms, rest, ok := strings.Cut(sc.Text(), " ")
			v, err := strconv.ParseInt(ms, 10, 64)
			if !ok || err != nil {
				continue
			}
			at := time.UnixMilli(v)
			if at.Before(start) {
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
		_ = fh.Close()
	}
	return out
}

// rebasesSince is the re-bases of ffLogSince.
func rebasesSince(t testing.TB, start time.Time) []rebase {
	t.Helper()
	return ffLogSince(t, start).Rebases
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
	got := ffLogSince(t, start)
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
