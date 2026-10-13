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
func serverLogHits(t testing.TB, path string, start, end time.Time, needles []string) ([]logHit, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("server log: %w", err)
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), logScanMax)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("server log: %w", err)
	}
	return assessServerLog(lines, start, end, needles)
}

// rebaseAftermath is how long after a re-base the soak must keep watching.
const rebaseAftermath = time.Minute

// falseRebase says why a re-base at `at` was harmful. "" means the next
// rebaseAftermath was observed and clean: no audio hole over 50 ms, no A/V
// skew over 100 ms, and timeline excess within 1 s of its value at the
// re-base. events are in time order, as OnSegment records them. A hole or
// skew of exactly the limit, and an excess move of exactly 1 s, are a true
// splice re-base. No event at or before `at`, or an observation that ends
// before the aftermath does, is unmeasured. A hole, skew, or excess move is
// reported ahead of either of those.
func falseRebase(at time.Time, events []testplayer.SegmentEvent, observedUntil time.Time) string {
	excessAt, haveExcess := 0.0, false
	for _, e := range events {
		if e.At.After(at) {
			break
		}
		excessAt, haveExcess = e.TimelineExcess, true
	}
	limit := at.Add(rebaseAftermath)
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
	if !haveExcess {
		return "unmeasured: no timeline excess before the re-base"
	}
	if observedUntil.Before(at.Add(rebaseAftermath)) {
		return "unmeasured: 60s aftermath not observed"
	}
	return ""
}

// soakExtraPlay is how much longer than nominalEnd the player must run so
// every re-base in [origin, nominalEnd] is followed by rebaseAftermath of
// playback. A re-base after nominalEnd adds nothing. The result is in
// [0, rebaseAftermath].
func soakExtraPlay(origin, nominalEnd time.Time, rebases []rebase) time.Duration {
	var extra time.Duration
	for _, rb := range rebases {
		at := origin.Add(rb.At)
		if at.After(nominalEnd) {
			continue
		}
		need := at.Add(rebaseAftermath).Sub(nominalEnd)
		if need > extra {
			extra = need
		}
	}
	if extra > rebaseAftermath {
		extra = rebaseAftermath
	}
	if extra < 0 {
		return 0
	}
	return extra
}

// requireFFmpegLog reports whether log shows this soak's FFmpeg actually
// started. An empty log, or argv lines from an earlier run, is not evidence.
func requireFFmpegLog(log ffLog) error {
	if len(log.Argv) == 0 {
		return fmt.Errorf("ffmpeg log: no argv line in the soak window")
	}
	return nil
}

// soakFFmpegLog is ffLogSince for the soak window, refused when the wrapper
// left no argv line there. A read error drops any partial log.
func soakFFmpegLog(start, end time.Time) (ffLog, error) {
	log, err := ffLogSince(start, end)
	if err != nil {
		return ffLog{}, err
	}
	if err := requireFFmpegLog(log); err != nil {
		return ffLog{}, err
	}
	return log, nil
}

// soakMinServerLines is the least `docker logs -t` lines a soak window must
// contain. The server logs the session start, so zero means the file is the
// wrong stream.
const soakMinServerLines = 1

// assessServerLog is dockerLogHits after requiring parseable timestamps
// inside the window. A Go log prefix, an empty file, or only stale lines
// fails; the fault needles stay a separate count.
func assessServerLog(lines []string, start, end time.Time, needles []string) ([]logHit, error) {
	stamped := 0
	for _, line := range lines {
		at, _, ok := parseDockerLogLine(line)
		if ok && !at.Before(start) && !at.After(end) {
			stamped++
		}
	}
	if stamped < soakMinServerLines {
		return nil, fmt.Errorf("server log: %d timestamped lines in the soak window, want at least %d", stamped, soakMinServerLines)
	}
	return dockerLogHits(lines, start, end, needles), nil
}
