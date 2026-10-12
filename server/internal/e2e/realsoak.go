//go:build ffmpeg

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

const (
	// soakWindowPeriod is how often the soak closes a coverage window.
	soakWindowPeriod = 5 * time.Minute
	// soakMinWindow is the shortest window a tick may open. UnpairedAfterFaults
	// fails a window with no PDT-paired segment, so a tick this close to the
	// deadline would book a stub.
	soakMinWindow = time.Minute
)

// soakFaultNeedles are the substrings of internal/stream log lines that fail
// the soak: an FFmpeg restart, a session end, or the stuck kill.
// "transcoder read nothing" is the drain that comes first; the process is
// closed only by "transcoder stuck for".
var soakFaultNeedles = []string{
	"restarting it",
	"restarting ffmpeg",
	"ffmpeg restarted",
	"ffmpeg restart failed",
	"ffmpeg exited after",
	"for restart",
	"ingest not configured",
	"ending the session",
	"transcoder stuck for",
}

// markSoakWindow reports whether a tick with this much time left should
// close the current coverage window.
func markSoakWindow(remaining time.Duration) bool {
	return remaining > soakMinWindow
}

// soakMarkOffsets is every Player.Mark offset from the start of a soak that
// long: one at the start, so the lead-in stays an unchecked window, then one
// per soakWindowPeriod while markSoakWindow still allows it.
func soakMarkOffsets(dur time.Duration) []time.Duration {
	if dur <= 0 {
		return nil
	}
	out := []time.Duration{0}
	for at := soakWindowPeriod; at < dur; at += soakWindowPeriod {
		if !markSoakWindow(dur - at) {
			break
		}
		out = append(out, at)
	}
	return out
}

// logHit is one `docker logs -t` line inside the soak window whose text
// contains a fault needle. Needle is the first needle that matched.
type logHit struct {
	Needle string
	Line   string
}

// parseDockerLogLine splits a `docker logs -t` line into its timestamp and
// the server's text. Docker writes an RFC3339 timestamp, fractional seconds
// optional, as the first field.
func parseDockerLogLine(line string) (at time.Time, text string, ok bool) {
	ts, text, found := strings.Cut(line, " ")
	if !found {
		return time.Time{}, "", false
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", false
	}
	return at, text, true
}

// dockerLogHits returns the lines of a `docker logs -t` stream whose
// timestamp is inside [start, end] (inclusive) and whose text contains a
// needle. A line that matches two needles is reported once, under the first.
// A line with no parseable timestamp is skipped.
func dockerLogHits(lines []string, start, end time.Time, needles []string) []logHit {
	var out []logHit
	for _, line := range lines {
		at, text, ok := parseDockerLogLine(line)
		if !ok || at.Before(start) || at.After(end) {
			continue
		}
		for _, n := range needles {
			if n != "" && strings.Contains(text, n) {
				out = append(out, logHit{Needle: n, Line: line})
				break
			}
		}
	}
	return out
}

// serverLogHits is dockerLogHits over a log file `docker logs -t` is writing.
func serverLogHits(t testing.TB, path string, start, end time.Time, needles []string) []logHit {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("server log: %v", err)
	}
	return dockerLogHits(lines, start, end, needles)
}

// falseRebase says why a re-base at `at` was harmful ("" when it wasn't):
// within the next 60 s, a probed segment with an audio hole over 50 ms, an
// A/V skew over 100 ms, or a timeline excess more than 1 s from its value
// at the re-base. events are in time order, as OnSegment records them.
// A hole or skew of exactly the limit, and an excess move of exactly 1 s,
// are a true splice re-base.
func falseRebase(at time.Time, events []testplayer.SegmentEvent) string {
	excessAt, haveExcess := 0.0, false
	for _, e := range events {
		if e.At.After(at) {
			break
		}
		excessAt, haveExcess = e.TimelineExcess, true
	}
	limit := at.Add(time.Minute)
	for _, e := range events {
		if e.At.Before(at) || e.At.After(limit) {
			continue
		}
		switch {
		case e.AudioHole > 50*time.Millisecond:
			return fmt.Sprintf("%s segment %d: audio hole %v", e.Rendition, e.Seq, e.AudioHole)
		case e.AVSkew > 100*time.Millisecond:
			return fmt.Sprintf("%s segment %d: A/V skew %v", e.Rendition, e.Seq, e.AVSkew)
		case haveExcess && (e.TimelineExcess-excessAt > 1 || excessAt-e.TimelineExcess > 1):
			return fmt.Sprintf("timeline excess %.2fs → %.2fs", excessAt, e.TimelineExcess)
		}
	}
	return ""
}
