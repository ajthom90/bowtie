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

// clipFFmpegLog is the FFmpeg log for an OTA clip. A missing directory, an
// empty directory, or only stale argv is not evidence of zero re-bases:
// those are the same as the soak's check. Re-base lines are debug-level, so
// an argv in the window that is not debug or trace is not evidence either.
func clipFFmpegLog(start, end time.Time) (ffLog, error) {
	log, err := soakFFmpegLog(start, end)
	if err != nil {
		return ffLog{}, err
	}
	for _, argv := range log.Argv {
		if !ffmpegArgvDebug(argv) {
			return ffLog{}, fmt.Errorf("ffmpeg log: argv in the window is not debug or trace")
		}
	}
	return log, nil
}

// ffmpegArgvDebug reports whether argv's -loglevel enables FFmpeg debug
// output. Debug is 48 and trace is 56. Numeric levels count, including the
// flag prefixes FFmpeg's -loglevel accepts (repeat, level, time, datetime,
// each optional + or -). The first -loglevel is the one that applies.
func ffmpegArgvDebug(argv string) bool {
	fields := strings.Fields(argv)
	for i, f := range fields {
		if f != "-loglevel" || i+1 >= len(fields) {
			continue
		}
		return loglevelDebug(fields[i+1])
	}
	return false
}

// loglevelDebug parses one -loglevel argument the way FFmpeg's opt_loglevel
// does: strip flag names, then a level name or an integer.
func loglevelDebug(s string) bool {
	for {
		rest, ok := cutLogFlag(s)
		if !ok {
			break
		}
		s = strings.TrimPrefix(rest, "+")
		if s == "" {
			return false
		}
	}
	switch s {
	case "debug", "trace":
		return true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n >= 48
}

func cutLogFlag(s string) (string, bool) {
	// datetime before time, so a datetime flag is not read as time.
	for _, flag := range []string{"repeat", "level", "datetime", "time"} {
		for _, p := range []string{flag, "+" + flag, "-" + flag} {
			if rest, ok := strings.CutPrefix(s, p); ok {
				return rest, true
			}
		}
	}
	return s, false
}

func TestFFmpegArgvDebug(t *testing.T) {
	cases := []struct {
		argv string
		want bool
	}{
		{"-loglevel debug", true},
		{"-loglevel trace", true},
		{"-loglevel level+debug", true},
		{"-loglevel repeat+48", true},
		{"-loglevel 48", true},
		{"-loglevel 56", true},
		{"-loglevel 47", false},
		{"-loglevel warning", false},
		{"-loglevel repeat+level+trace", true},
		{"-loglevel level+48", true},
		{"-loglevel +48", true},
		{"-loglevel verbose", false},
		{"-loglevel 24", false},
		{"-i pipe:0", false},
		{"-loglevel repeat", false},
		{"-loglevel quiet", false},
		{"-loglevel -8", false},
		{"-loglevel datetime+debug", true},
		{"-loglevel time+56", true},
		{"-loglevel repeat+47", false},
		{"ffmpeg -hide_banner -loglevel level+debug -i pipe:0", true},
	}
	for _, tc := range cases {
		if got := ffmpegArgvDebug(tc.argv); got != tc.want {
			t.Errorf("%q = %v, want %v", tc.argv, got, tc.want)
		}
	}
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

// An OTA clip with no current debug argv must not pass as zero re-bases.
func TestClipFFmpegLogRequiresCurrentDebugArgv(t *testing.T) {
	start := time.UnixMilli(1_760_000_000_000)
	end := start.Add(time.Hour)
	debugArgv := strconvMS(start.Add(time.Second)) + " argv: -hide_banner -loglevel level+debug -i pipe:0\n"
	traceArgv := strconvMS(start.Add(time.Second)) + " argv: -loglevel trace -i pipe:0\n"
	warnArgv := strconvMS(start.Add(time.Second)) + " argv: -loglevel warning -i pipe:0\n"
	noLevel := strconvMS(start.Add(time.Second)) + " argv: -i pipe:0 -dts_delta_threshold 2.6\n"
	rebase := strconvMS(start.Add(2*time.Second)) + " [debug] timestamp discontinuity (stream id=0): 270000, new offset= -270000\n"
	stale := strconvMS(start.Add(-time.Hour)) + " argv: -loglevel level+debug -i pipe:0\n"
	tests := []struct {
		name    string
		body    string
		missing bool
		unset   bool
		wantErr bool
		rebases int
	}{
		{name: "log dir unset", unset: true, wantErr: true},
		{name: "directory missing", missing: true, wantErr: true},
		{name: "empty directory", wantErr: true},
		{name: "stale debug argv only", body: stale, wantErr: true},
		{name: "warning-level argv", body: warnArgv, wantErr: true},
		{name: "argv without -loglevel", body: noLevel, wantErr: true},
		{name: "level+debug argv and a re-base", body: debugArgv + rebase, rebases: 1},
		{name: "trace argv", body: traceArgv, rebases: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			switch {
			case tc.unset:
				t.Setenv("FF51_LOGDIR", "")
			case tc.missing:
				t.Setenv("FF51_LOGDIR", filepath.Join(t.TempDir(), "missing"))
			default:
				dir := t.TempDir()
				t.Setenv("FF51_LOGDIR", dir)
				if err := os.WriteFile(filepath.Join(dir, "ffmpeg-1.log"), []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := clipFFmpegLog(start, end)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("err = nil, log %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Rebases) != tc.rebases {
				t.Fatalf("rebases %+v, want %d", got.Rebases, tc.rebases)
			}
			if len(got.Argv) == 0 {
				t.Fatal("argv missing")
			}
		})
	}
}
