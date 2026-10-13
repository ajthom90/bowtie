//go:build ffmpeg

package e2e

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

func TestFalseRebase(t *testing.T) {
	t0 := time.Unix(1_760_000_000, 0).UTC()
	ev := func(d time.Duration, hole, skew time.Duration, excess float64) testplayer.SegmentEvent {
		return testplayer.SegmentEvent{
			At: t0.Add(d), Rendition: "aac0", Seq: 7,
			AudioHole: hole, AVSkew: skew, TimelineExcess: excess,
		}
	}
	clean := []testplayer.SegmentEvent{
		ev(-4*time.Second, 0, 0, 0.2),
		ev(4*time.Second, 10*time.Millisecond, 20*time.Millisecond, 0.4),
		ev(50*time.Second, 0, 0, 0.9),
	}
	tests := []struct {
		name   string
		events []testplayer.SegmentEvent
		until  time.Time
		want   string
	}{
		{name: "clean splice", events: clean, want: ""},
		{
			name: "hole before the re-base",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 80*time.Millisecond, 0, 0),
				ev(4*time.Second, 0, 0, 0),
			},
			want: "",
		},
		{
			name: "audio hole",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(8*time.Second, 80*time.Millisecond, 0, 0),
			},
			want: "aac0 segment 7: audio hole 80ms",
		},
		{
			name: "av skew",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(8*time.Second, 0, 150*time.Millisecond, 0),
			},
			want: "aac0 segment 7: A/V skew 150ms",
		},
		{
			name: "excess up",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(30*time.Second, 0, 0, 1.6),
			},
			want: "timeline excess 0.00s → 1.60s",
		},
		{
			name: "excess down",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 2),
				ev(10*time.Second, 0, 0, 0.4),
			},
			want: "timeline excess 2.00s → 0.40s",
		},
		{
			name: "limits are inclusive",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(8*time.Second, 50*time.Millisecond, 100*time.Millisecond, 1),
			},
			want: "",
		},
		{
			name: "after 60s",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(70*time.Second, 80*time.Millisecond, 0, 3),
			},
			want: "",
		},
		{
			name: "at exactly 60s",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(time.Minute, 80*time.Millisecond, 0, 0),
			},
			want: "aac0 segment 7: audio hole 80ms",
		},
		{
			name: "just after 60s",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(time.Minute+time.Millisecond, 80*time.Millisecond, 0, 0),
			},
			want: "",
		},
		{
			name: "no baseline is unmeasured",
			events: []testplayer.SegmentEvent{
				ev(8*time.Second, 0, 0, 1.6),
			},
			want: "unmeasured: no timeline excess before the re-base",
		},
		{
			name: "aftermath not observed",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0.2),
				ev(10*time.Second, 0, 0, 0.3),
			},
			until: t0.Add(10 * time.Second),
			want:  "unmeasured: 60s aftermath not observed",
		},
		{
			name: "aftermath one nanosecond short",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0.2),
				ev(10*time.Second, 0, 0, 0.3),
			},
			until: t0.Add(time.Minute - time.Nanosecond),
			want:  "unmeasured: 60s aftermath not observed",
		},
		{
			name: "full minute of clean aftermath",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0.2),
				ev(10*time.Second, 0, 0, 0.3),
			},
			until: t0.Add(time.Minute),
			want:  "",
		},
		{
			name: "a seen hole fails even when the aftermath is short",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(8*time.Second, 80*time.Millisecond, 0, 0),
			},
			until: t0.Add(10 * time.Second),
			want:  "aac0 segment 7: audio hole 80ms",
		},
		{
			name: "late excess counts once the minute is observed",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(10*time.Second, 0, 0, 0.2),
				ev(20*time.Second, 0, 0, 1.8),
			},
			until: t0.Add(time.Minute),
			want:  "timeline excess 0.00s → 1.80s",
		},
		{
			name: "hole wins over skew on the same segment",
			events: []testplayer.SegmentEvent{
				ev(-4*time.Second, 0, 0, 0),
				ev(8*time.Second, 80*time.Millisecond, 150*time.Millisecond, 0),
			},
			want: "aac0 segment 7: audio hole 80ms",
		},
		{
			name: "hole at the re-base instant",
			events: []testplayer.SegmentEvent{
				ev(0, 80*time.Millisecond, 0, 0.2),
			},
			want: "aac0 segment 7: audio hole 80ms",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			until := tc.until
			if until.IsZero() {
				until = t0.Add(2 * time.Minute)
			}
			if got := falseRebase(t0, tc.events, until); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSoakMarkOffsets(t *testing.T) {
	upTo := func(last int) []time.Duration {
		out := []time.Duration{0}
		for i := 1; i <= last; i++ {
			out = append(out, time.Duration(i)*5*time.Minute)
		}
		return out
	}
	tests := []struct {
		name string
		dur  time.Duration
		want []time.Duration
	}{
		{name: "one hour", dur: time.Hour, want: upTo(11)},
		{name: "tick leaves exactly the minimum", dur: 6 * time.Minute, want: []time.Duration{0}},
		{name: "tick leaves just over the minimum", dur: 6*time.Minute + time.Second, want: []time.Duration{0, 5 * time.Minute}},
		{name: "shorter than one period", dur: 4 * time.Minute, want: []time.Duration{0}},
		{name: "zero", dur: 0, want: nil},
		{name: "negative", dur: -time.Second, want: nil},
		{name: "fifty-six minutes drops the stub", dur: 56 * time.Minute, want: upTo(10)},
		{name: "fifty-six minutes plus a second keeps it", dur: 56*time.Minute + time.Second, want: upTo(11)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := soakMarkOffsets(tc.dur)
			if len(got) != len(tc.want) {
				t.Fatalf("marks %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("marks %v, want %v", got, tc.want)
				}
				if i > 0 && !markSoakWindow(tc.dur-got[i]) {
					t.Fatalf("mark at %v with %v left", got[i], tc.dur-got[i])
				}
			}
		})
	}
	bounds := []struct {
		left time.Duration
		mark bool
	}{
		{soakMinWindow + time.Nanosecond, true},
		{soakMinWindow, false},
		{0, false},
		{-time.Second, false},
	}
	for _, b := range bounds {
		if got := markSoakWindow(b.left); got != b.mark {
			t.Errorf("markSoakWindow(%v) = %v, want %v", b.left, got, b.mark)
		}
	}
}

func TestDockerLogHits(t *testing.T) {
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	stamp := func(at time.Time) string { return at.Format(time.RFC3339Nano) }
	inside := start.Add(time.Minute)
	fault := "stream: session s: restarting ffmpeg (append to playlist)"
	tests := []struct {
		name string
		line string
		want int
	}{
		{name: "before the window", line: stamp(start.Add(-time.Second)) + " " + fault, want: 0},
		{name: "at the start", line: stamp(start) + " " + fault, want: 1},
		{name: "inside", line: stamp(inside) + " " + fault, want: 1},
		{name: "at the end", line: stamp(end) + " " + fault, want: 1},
		{name: "after the window", line: stamp(end.Add(time.Second)) + " " + fault, want: 0},
		{name: "offset timestamp inside", line: inside.In(time.FixedZone("EDT", -4*3600)).Format(time.RFC3339Nano) + " " + fault, want: 1},
		{name: "no fractional seconds", line: inside.Format(time.RFC3339) + " " + fault, want: 1},
		{name: "not a docker line", line: fault, want: 0},
		{name: "bad timestamp", line: "yesterday " + fault, want: 0},
		{name: "inside but not a fault", line: stamp(inside) + " stream: removed 2 stale caption segments in s", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dockerLogHits([]string{tc.line}, start, end, soakFaultNeedles)
			if len(got) != tc.want {
				t.Fatalf("hits %+v, want %d", got, tc.want)
			}
			if tc.want == 1 && (got[0].Needle != "restarting ffmpeg" || got[0].Line != tc.line) {
				t.Fatalf("hit %+v", got[0])
			}
		})
	}
}

func TestSoakServerFaultLines(t *testing.T) {
	// Lines internal/stream prints, and whether the soak treats them as a
	// restart, a session end, or the stuck kill.
	samples := []struct {
		name  string
		msg   string
		fault bool
	}{
		{"gate restart", "stream: session abc: ffmpeg published no aac0,v480 for 15s of fed time; restarting it", true},
		{"append restart", "stream: session abc: restarting ffmpeg (append to playlist)", true},
		{"restart committed", "stream: session abc channel 4: ffmpeg restarted", true},
		{"restart failed", "stream: session abc: ffmpeg restart failed: exit status 1", true},
		{"process exit", "stream: session abc channel 4 (software): ffmpeg exited after 1m0s: signal: killed", true},
		{"stall budget with cause", "stream: session abc: 3 failed runs in a row (last: no aac0); ending the session", true},
		{"stall budget", "stream: session abc: 3 failed runs in a row; ending the session", true},
		{"mkdir failed", "stream: session abc: mkdir session dir /data/segments/abc for restart: permission denied", true},
		{"ingest missing", "stream: restart abc: ingest not configured", true},
		{"reattach failed", "stream: session abc: re-Attach channel 4 for restart: no signal", true},
		{"stuck kill", "ingest: channel 4: transcoder stuck for 1m0s, closing its input", true},
		{"drain", "ingest: channel 4: transcoder read nothing for 30s; dropped its 8 KiB backlog and resent the stream headers", false},
		{"behind", "ingest: channel 4: transcoder behind, dropped 64 KiB of old data", false},
		{"recovered", "ingest: channel 4: transcoder reading again 2s after its backlog was dropped", false},
		{"startup error", "session start failed: user=admin: ffmpeg exited before playlist ready: exit status 1", false},
		{"captions", "stream: session abc: caption tap re-attach failed: dial (this process runs without captions)", false},
	}
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	for _, tc := range samples {
		t.Run(tc.name, func(t *testing.T) {
			line := start.Add(time.Minute).Format(time.RFC3339Nano) + " " + tc.msg
			hits := dockerLogHits([]string{line}, start, start.Add(time.Hour), soakFaultNeedles)
			if tc.fault && len(hits) != 1 {
				t.Fatalf("not judged a fault: %s", tc.msg)
			}
			if !tc.fault && len(hits) != 0 {
				t.Fatalf("judged a fault via %q: %s", hits[0].Needle, tc.msg)
			}
		})
	}
}

func TestServerLogHits(t *testing.T) {
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	var b strings.Builder
	fmt.Fprintf(&b, "%s ingest: channel 1: transcoder stuck for 1m0s, closing its input\n", start.Add(-time.Second).Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "%s stream: session s: restarting ffmpeg (append to playlist)\n", start.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "%s ingest: channel 1: transcoder read nothing for 30s; dropped its 1 KiB backlog and resent the stream headers\n", start.Add(time.Minute).Format(time.RFC3339Nano))
	b.WriteString("bogus line without a stamp\n")
	fmt.Fprintf(&b, "%s stream: session s: 3 failed runs in a row; ending the session\n", end.Add(time.Second).Format(time.RFC3339Nano))
	path := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, err := serverLogHits(t, path, start, end, soakFaultNeedles)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Needle != "restarting ffmpeg" || !strings.Contains(hits[0].Line, "restarting ffmpeg") {
		t.Fatalf("hits %+v", hits)
	}
}

func TestSoakExtraPlay(t *testing.T) {
	origin := time.Unix(1_760_000_000, 0).UTC()
	nominal := origin.Add(time.Hour)
	rb := func(d time.Duration) rebase { return rebase{At: d, Line: "timestamp discontinuity, new offset"} }
	tests := []struct {
		name string
		rbs  []rebase
		want time.Duration
	}{
		{name: "late re-base", rbs: []rebase{rb(59*time.Minute + 50*time.Second)}, want: 50 * time.Second},
		{name: "early re-base", rbs: []rebase{rb(10 * time.Minute)}, want: 0},
		{name: "at the nominal end", rbs: []rebase{rb(time.Hour)}, want: time.Minute},
		{name: "none", want: 0},
		{name: "after the nominal window", rbs: []rebase{rb(time.Hour + 10*time.Second)}, want: 0},
		{name: "the later one wins", rbs: []rebase{rb(50 * time.Minute), rb(59*time.Minute + 40*time.Second)}, want: 40 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := soakExtraPlay(origin, nominal, tc.rbs); got != tc.want {
				t.Fatalf("extra %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSoakFFmpegLog(t *testing.T) {
	start := time.UnixMilli(1_760_000_000_000)
	end := start.Add(time.Hour)
	argv := strconvMS(start.Add(time.Second)) + " argv: -i pipe:0 -dts_delta_threshold 2.6\n"
	inside := strconvMS(start.Add(2*time.Second)) + " [debug] timestamp discontinuity (stream id=0): 270000, new offset= -270000\n"
	after := strconvMS(end.Add(time.Second)) + " [debug] timestamp discontinuity (stream id=0): 1, new offset= 0\n"
	stale := strconvMS(start.Add(-time.Hour)) + " argv: -i pipe:0\n"
	tests := []struct {
		name    string
		body    string
		missing bool
		wantErr bool
		rebases int
	}{
		{name: "directory missing", missing: true, wantErr: true},
		{name: "empty directory", wantErr: true},
		{name: "stale argv only", body: stale, wantErr: true},
		{name: "argv and a re-base inside, one after the window", body: argv + inside + after, rebases: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.missing {
				t.Setenv("FF51_LOGDIR", filepath.Join(t.TempDir(), "missing"))
			} else {
				dir := t.TempDir()
				t.Setenv("FF51_LOGDIR", dir)
				if err := os.WriteFile(filepath.Join(dir, "ffmpeg-1.log"), []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := soakFFmpegLog(start, end)
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

func strconvMS(t time.Time) string {
	return fmt.Sprintf("%d", t.UnixMilli())
}

// TestSoakFFmpegLogScannerError puts a valid argv line ahead of a token the
// scanner cannot read, with a re-base after it. Dropping sc.Err() would
// return that argv and look like evidence while losing the re-base.
func TestSoakFFmpegLogScannerError(t *testing.T) {
	start := time.UnixMilli(1_760_000_000_000)
	end := start.Add(time.Hour)
	dir := t.TempDir()
	t.Setenv("FF51_LOGDIR", dir)
	body := strconvMS(start.Add(time.Second)) + " argv: -i pipe:0 -dts_delta_threshold 2.6\n" +
		strings.Repeat("x", logScanMax+8) +
		strconvMS(start.Add(2*time.Second)) + " [debug] timestamp discontinuity (stream id=0): 270000, new offset= -270000\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg-1.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := ffLogSince(start, end)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("ffLogSince err = %v, log %+v", err, log)
	}
	rebases, err := rebasesSince(start, end)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("rebasesSince err = %v, rebases %+v", err, rebases)
	}
}

func TestSoakServerLogEvidence(t *testing.T) {
	start := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	stamp := func(at time.Time) string { return at.Format(time.RFC3339Nano) }
	tests := []struct {
		name    string
		lines   []string
		wantErr bool
		hits    int
	}{
		{
			name:    "go log without docker timestamps",
			lines:   []string{"2026/10/12 15:01:00 stream: session abc: restarting ffmpeg (append to playlist)"},
			wantErr: true,
		},
		{
			name:    "only stale lines",
			lines:   []string{stamp(start.Add(-time.Hour)) + " bowtie listening"},
			wantErr: true,
		},
		{name: "empty", wantErr: true},
		{
			name:  "session start inside the window",
			lines: []string{stamp(start.Add(time.Second)) + " bowtie listening"},
		},
		{
			name:  "fault inside the window",
			lines: []string{stamp(start) + " stream: session s: restarting ffmpeg (append to playlist)"},
			hits:  1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := assessServerLog(tc.lines, start, end, soakFaultNeedles)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("err = nil, hits %+v", hits)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != tc.hits {
				t.Fatalf("hits %+v, want %d", hits, tc.hits)
			}
		})
	}
}
