package stream

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var tl0 = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)

func sec(f float64) time.Time { return tl0.Add(time.Duration(f * float64(time.Second))) }

// seg is one segment: EXTINF, the file's mtime (seconds after tl0) and the
// first PTS inside it (seconds; negative: the file carries no PTS).
type seg struct{ dur, end, pts float64 }

// tsWithPTS is one TS packet starting a video PES with the given PTS.
func tsWithPTS(pts float64) []byte {
	v := int64(math.Round(pts*90000)) & (int64(1)<<33 - 1)
	p := make([]byte, 188)
	for i := range p {
		p[i] = 0xFF
	}
	copy(p, []byte{0x47, 0x41, 0x00, 0x10, 0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 5})
	p[13] = byte(0x21 | (v>>29)&0x0E)
	p[14] = byte(v >> 22)
	p[15] = byte((v>>14)&0xFE | 1)
	p[16] = byte(v >> 7)
	p[17] = byte((v<<1)&0xFE | 1)
	return p
}

// writeRun writes name's playlist and segment files (PTS, mtimes) and
// returns the playlist bytes.
func writeRun(t *testing.T, dir, name string, mseq int64, segs ...seg) []byte {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
	for i, s := range segs {
		uri := fmt.Sprintf("%s_%05d.ts", name, mseq+int64(i))
		path := filepath.Join(dir, uri)
		body := []byte("x")
		if s.pts >= 0 {
			body = tsWithPTS(s.pts)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, sec(s.end), sec(s.end)); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s\n", s.dur, uri)
	}
	raw := []byte(b.String())
	if err := os.WriteFile(filepath.Join(dir, name+".m3u8"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return raw
}

// chain is n segments of dur seconds ending at end0+dur, end0+2·dur, …,
// with PTS pts0, pts0+dur, …
func chain(end0, dur float64, n int, pts0 float64) []seg {
	out := make([]seg, n)
	for i := range out {
		out[i] = seg{dur, end0 + dur*float64(i+1), pts0 + dur*float64(i)}
	}
	return out
}

func catSegs(parts ...[]seg) []seg {
	var out []seg
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// newTestTimeline returns a timeline whose clock is *clock (start: 1000 s).
func newTestTimeline(dir string, renditions ...string) (*Timeline, *[]string, *time.Time) {
	var logs []string
	clock := sec(1000)
	tl := NewTimeline(dir, renditions, func() time.Time { return clock })
	tl.logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	return tl, &logs, &clock
}

type servedSeg struct {
	pdt  time.Time
	disc bool
	uri  string
}

func parseServed(t *testing.T, out []byte) (mseq, dseq int64, segs []servedSeg) {
	t.Helper()
	var cur servedSeg
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			mseq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			dseq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			cur.disc = true
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			v, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"))
			if err != nil {
				t.Fatalf("bad PDT line %q", line)
			}
			cur.pdt = v
		case line != "" && !strings.HasPrefix(line, "#"):
			cur.uri = line
			segs = append(segs, cur)
			cur = servedSeg{}
		}
	}
	return mseq, dseq, segs
}

func served(t *testing.T, tl *Timeline, rendition string, raw []byte) (int64, int64, []servedSeg) {
	t.Helper()
	return parseServed(t, tl.Retime(rendition, raw))
}

// dnum is the discontinuity number a client computes for segs[i].
func dnum(dseq int64, segs []servedSeg, i int) int64 {
	n := dseq
	for _, s := range segs[:i+1] {
		if s.disc {
			n++
		}
	}
	return n
}

// near allows for millisecond PDT formatting.
func near(a, b time.Time) bool {
	d := a.Sub(b)
	return d >= -2*time.Millisecond && d <= 2*time.Millisecond
}

func TestTimelineStampsFromPTSAndMtime(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4, 10}, seg{4, 8, 14}, seg{4, 12.3, 18})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	for i, want := range []time.Time{sec(0), sec(4), sec(8)} {
		if !near(segs[i].pdt, want) || segs[i].disc {
			t.Fatalf("segment %d: %+v, want PDT %v and no discontinuity", i, segs[i], want)
		}
	}
}

// FFmpeg closed a 5 s outage (contiguous PTS): the file mtimes show it.
func TestTimelineJumpsForwardOverAClosedGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	raw := writeRun(t, dir, "v480", 0, catSegs(chain(0, 4, 2, 1.4), []seg{{4, 17, 9.4}})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !near(segs[2].pdt, sec(13)) || segs[2].disc {
		t.Fatalf("segment 2: %+v, want PDT %v without a discontinuity", segs[2], sec(13))
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "gap") {
		t.Fatalf("logs %v, want one gap line", *logs)
	}
}

// FFmpeg kept the hole in the PTS: the media offset already carries it.
func TestTimelinePTSHoleNeedsNoAnchor(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4, 1.4}, seg{4, 8, 5.4}, seg{4, 17, 14.4})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !near(segs[2].pdt, sec(13)) || len(*logs) != 0 {
		t.Fatalf("segment 2 at %v (want %v), logs %v", segs[2].pdt, sec(13), *logs)
	}
}

// Audio (4.032 s) and video (4.004 s) are cut at different points: each
// chains on its own segments (no overlap), and the same PTS gets the same
// wall-clock time in both. An audio segment waits until video covers its
// whole span, so the tenth audio segment needs an eleventh video one.
func TestTimelineUnequalDurationsNeverOverlap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4.004, 10, 1.4)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4.032, 10, 1.4)...)
	tl.Observe()
	if _, _, as := served(t, tl, "aac0", ra); len(as) != 9 {
		t.Fatalf("served %d audio segments, want 9 (the tenth ends after the video)", len(as))
	}
	rv := writeRun(t, dir, "v480", 0, chain(0, 4.004, 11, 1.4)...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 11 || len(as) != 10 || len(*logs) != 0 {
		t.Fatalf("served %d video, %d audio; logs %v", len(vs), len(as), *logs)
	}
	for i := 1; i < 10; i++ {
		if end := as[i-1].pdt.Add(4032 * time.Millisecond); as[i].pdt.Before(end.Add(-time.Millisecond)) {
			t.Fatalf("audio segment %d starts %v before segment %d ends", i, end.Sub(as[i].pdt), i-1)
		}
		if want := vs[i].pdt.Add(time.Duration(i) * 28 * time.Millisecond); !near(as[i].pdt, want) {
			t.Fatalf("audio segment %d at %v, want %v (its PTS is %d ms after video's)", i, as[i].pdt, want, 28*i)
		}
	}
}

// A sibling playlist that shows up 5 s late, with files written late, gets
// the same wall clock for the same PTS; the reference isn't disturbed.
func TestTimelineStaggeredPublicationKeepsAlignment(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	tl.Observe()
	ra := writeRun(t, dir, "aac0", 0, chain(5, 4, 3, 1.42)...) // every file 5 s late
	rv := writeRun(t, dir, "v480", 0, chain(0, 4, 5, 1.4)...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(as) != 3 || len(vs) != 5 || len(*logs) != 0 {
		t.Fatalf("served %d audio, %d video; logs %v", len(as), len(vs), *logs)
	}
	for i := range as {
		if !near(as[i].pdt, vs[i].pdt.Add(20*time.Millisecond)) {
			t.Fatalf("audio %d at %v, video %d at %v: want 20 ms apart (their PTS)", i, as[i].pdt, i, vs[i].pdt)
		}
	}
	if !near(vs[4].pdt, sec(16)) {
		t.Fatalf("video segment 4 at %v, want %v", vs[4].pdt, sec(16))
	}
}

// The reference's files land 3 s after the audio's (encoder delay): a
// steady lag, not a gap. A real 5 s gap later shifts both renditions at
// the same media point.
func TestTimelineEncoderDelayIsNotAGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(3, 4, 5, 1.4)...)
	writeRun(t, dir, "aac0", 0, chain(0, 4, 5, 1.4)...)
	tl.Observe()
	if len(*logs) != 0 {
		t.Fatalf("encoder delay logged as a gap: %v", *logs)
	}
	rv := writeRun(t, dir, "v480", 0, catSegs(chain(3, 4, 5, 1.4), []seg{{4, 3 + 24 + 5, 21.4}})...)
	ra := writeRun(t, dir, "aac0", 0, catSegs(chain(0, 4, 5, 1.4), []seg{{4, 24 + 5, 21.4}})...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "gap") {
		t.Fatalf("logs %v, want one gap", *logs)
	}
	if !near(vs[5].pdt, sec(25)) || !near(as[5].pdt, sec(25)) || !near(vs[0].pdt, sec(0)) {
		t.Fatalf("after the gap: video %v audio %v, want %v", vs[5].pdt, as[5].pdt, sec(25))
	}
}

// Stamps don't depend on when the observer looked or the order playlists
// are served.
func TestTimelineObservationTimingDoesNotMatter(t *testing.T) {
	dir := t.TempDir()
	t1, _, _ := newTestTimeline(dir, "v480", "aac0")
	t2, _, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0.3, 4.004, 2, 1.4)...)
	writeRun(t, dir, "aac0", 0, chain(0.1, 4.032, 2, 1.41)...)
	t1.Observe()
	rv := writeRun(t, dir, "v480", 0, chain(0.3, 4.004, 3, 1.4)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0.1, 4.032, 3, 1.41)...)
	t1.Observe()
	t2.Observe()
	a1, v1 := string(t1.Retime("aac0", ra)), string(t1.Retime("v480", rv))
	v2, a2 := string(t2.Retime("v480", rv)), string(t2.Retime("aac0", ra))
	if a1 != a2 || v1 != v2 {
		t.Fatalf("observation timing changed the stamps:\n%s\n%s\n%s\n%s", a1, a2, v1, v2)
	}
}

// Two crashes; the middle run wrote audio only. It is never served; the
// next run opens as discontinuity 1 in both renditions, which carry
// Bowtie's own media sequence numbers. The trailing audio segment the
// reference never covered isn't served either.
func TestTimelinePartialRunIsNeverServed(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	writeRun(t, dir, "aac0", 0, chain(0, 4, 4, 1.4)...) // audio one segment ahead
	restarted(tl)
	held := writeRun(t, dir, "aac0", 0, catSegs(chain(0, 4, 4, 1.4), []seg{{4, 40, 1.4}})...) // run 1: audio only
	tl.Observe()
	if _, _, as := served(t, tl, "aac0", held); len(as) != 3 {
		t.Fatalf("served %d audio segments while run 1 is held, want 3", len(as))
	}
	restarted(tl)
	// Run 2: audio's first segment ends 20 ms after video's, so video's
	// second segment is what lets the run open.
	rv := writeRun(t, dir, "v480", 0, catSegs(chain(0, 4, 3, 1.4), []seg{{4, 64, 1.4}, {4, 68, 5.4}})...)
	ra := writeRun(t, dir, "aac0", 0, catSegs(chain(0, 4, 4, 1.4), []seg{{4, 40, 1.4}, {4, 64, 1.42}})...)
	tl.Observe()
	mv, dv, vs := served(t, tl, "v480", rv)
	ma, da, as := served(t, tl, "aac0", ra)
	if len(vs) != 5 || len(as) != 4 || as[3].uri != "aac0_00005.ts" {
		t.Fatalf("video %+v\naudio %+v", vs, as)
	}
	if !vs[3].disc || !as[3].disc || mv != 0 || ma != 0 || dv != 0 || da != 0 {
		t.Fatalf("video mseq %d dseq %d %+v\naudio mseq %d dseq %d %+v", mv, dv, vs, ma, da, as)
	}
	if dnum(dv, vs, 3) != 1 || dnum(da, as, 3) != 1 {
		t.Fatal("run 2 is not discontinuity 1 in both renditions")
	}
	if !near(as[3].pdt, vs[3].pdt.Add(20*time.Millisecond)) {
		t.Fatalf("run 2 starts at %v (video) and %v (audio), want 20 ms apart", vs[3].pdt, as[3].pdt)
	}
	// The windows slide: the tagged segment is the head, then gone.
	rv = writeRun(t, dir, "v480", 3, seg{4, 64, 1.4}, seg{4, 68, 5.4})
	ra = writeRun(t, dir, "aac0", 5, seg{4, 64, 1.42}, seg{4, 68, 5.42})
	tl.Observe()
	mv, dv, vs = served(t, tl, "v480", rv)
	ma, da, as = served(t, tl, "aac0", ra)
	if mv != 3 || ma != 3 || !vs[0].disc || !as[0].disc || dnum(dv, vs, 0) != 1 || dnum(da, as, 0) != 1 {
		t.Fatalf("head: video mseq %d dseq %d %+v; audio mseq %d dseq %d %+v", mv, dv, vs, ma, da, as)
	}
	rv = writeRun(t, dir, "v480", 4, seg{4, 68, 5.4}, seg{4, 72, 9.4})
	ra = writeRun(t, dir, "aac0", 6, seg{4, 68, 5.42}, seg{4, 72, 9.42})
	tl.Observe()
	mv, dv, vs = served(t, tl, "v480", rv)
	ma, da, as = served(t, tl, "aac0", ra)
	if mv != 4 || ma != 4 || dv != 1 || da != 1 || vs[0].disc || as[0].disc {
		t.Fatalf("after the slide: video mseq %d dseq %d; audio mseq %d dseq %d", mv, dv, ma, da)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "discarded") {
		t.Fatalf("discard not logged: %v", *logs)
	}
}

// A rendition misses two restarted runs (each held, then restarted after
// the gate timeout). When it returns, every rendition opens the run as
// discontinuity 1 while the old segments are still served.
func TestTimelineRenditionMissingTwoRunsReturnsAligned(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0", "ac30")
	base := chain(0, 4, 3, 1.4)
	for _, n := range []string{"v480", "aac0", "ac30"} {
		writeRun(t, dir, n, 0, base...)
	}
	tl.Observe()
	extra := []seg{}
	for _, end := range []float64{30, 50} { // runs 1 and 2: no ac30
		restarted(tl)
		extra = append(extra, seg{4, end, 1.4})
		writeRun(t, dir, "v480", 0, catSegs(base, extra)...)
		writeRun(t, dir, "aac0", 0, catSegs(base, extra)...)
		tl.Observe()
		if _, _, expired := tl.ExpireStalledRun(10*time.Second, true); expired {
			t.Fatal("expired before the timeout")
		}
		// The first restart (a crash) counted; each expiry counts.
		missing, stalls, expired := tl.ExpireStalledRun(10*time.Second, true)
		if !expired || strings.Join(missing, ",") != "ac30" || stalls != 1+len(extra) {
			t.Fatalf("ExpireStalledRun = %v, %d, %v; want [ac30], %d, true", missing, stalls, expired, 1+len(extra))
		}
	}
	restarted(tl) // the kill path restarted FFmpeg again
	extra = append(extra, seg{4, 70, 1.4})
	raw := map[string][]byte{
		"v480": writeRun(t, dir, "v480", 0, catSegs(base, extra)...),
		"aac0": writeRun(t, dir, "aac0", 0, catSegs(base, extra)...),
		"ac30": writeRun(t, dir, "ac30", 0, catSegs(base, []seg{{4, 70, 1.4}})...),
	}
	tl.Observe()
	// Opening doesn't reset the stall budget (only a healthy stretch does),
	// and the run that just published every rendition isn't stalled.
	if _, _, expired := tl.ExpireStalledRun(timelineGateTimeout, true); expired || tl.stalls != 3 {
		t.Fatalf("expired %v, %d stalls after the run opened; want false, 3", expired, tl.stalls)
	}
	for name, body := range raw {
		_, dseq, segs := served(t, tl, name, body)
		last := len(segs) - 1
		if len(segs) != 4 || !segs[last].disc || dnum(dseq, segs, last) != 1 || dnum(dseq, segs, 0) != 0 {
			t.Fatalf("%s: dseq %d %+v", name, dseq, segs)
		}
	}
}

// At startup a rendition that shows up late joins run 0 untagged.
func TestTimelineLateRenditionAtStartupJoinsEpochZero(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	rv := writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4, 2, 1.38)...)
	tl.Observe()
	_, dv, vs := served(t, tl, "v480", rv)
	_, da, as := served(t, tl, "aac0", ra)
	if len(as) != 2 || as[0].disc || da != 0 || dv != 0 || vs[0].disc {
		t.Fatalf("video dseq %d %+v; audio dseq %d %+v", dv, vs, da, as)
	}
	if !near(as[0].pdt, vs[0].pdt.Add(-20*time.Millisecond)) {
		t.Fatalf("late audio at %v, video at %v", as[0].pdt, vs[0].pdt)
	}
}

func TestTimelineNeverGoesBackwardsAtRestart(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	restarted(tl)
	// The new run's first segment claims to end at 10 s, before the old run's 12 s end.
	raw := writeRun(t, dir, "v480", 0, catSegs(chain(0, 4, 3, 1.4), []seg{{4, 10, 1.4}})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[3].disc || !near(segs[3].pdt, sec(12)) {
		t.Fatalf("restart segment %+v, want DISCONTINUITY at %v", segs[3], sec(12))
	}
}

func TestTimelineRestartWithoutSegmentsCountsOnce(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	restarted(tl) // this run dies before writing a segment
	restarted(tl)
	raw := writeRun(t, dir, "v480", 0, catSegs(chain(0, 4, 2, 1.4), []seg{{4, 30, 1.4}})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[2].disc || segs[1].disc {
		t.Fatalf("segments %+v, want one DISCONTINUITY on segment 2", segs)
	}
	raw = writeRun(t, dir, "v480", 3, seg{4, 34, 5.4})
	tl.Observe()
	if _, dseq, _ := served(t, tl, "v480", raw); dseq != 1 {
		t.Fatalf("DISCONTINUITY-SEQUENCE %d, want 1", dseq)
	}
}

// Serving reads the snapshot only: newer segments wait for the observer,
// segments that slid out are dropped from the head; nothing is restamped.
func TestTimelineServesOnlyPublishedSegments(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	newer := writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...) // not observed yet
	if _, _, segs := served(t, tl, "v480", newer); len(segs) != 2 {
		t.Fatalf("served %d segments, want the 2 observed", len(segs))
	}
	writeRun(t, dir, "v480", 2, seg{4, 12, 9.4}, seg{4, 16, 13.4})
	tl.Observe()
	mseq, _, segs := served(t, tl, "v480", newer) // stale: still starts at 0
	if mseq != 2 || len(segs) != 1 || !near(segs[0].pdt, sec(8)) {
		t.Fatalf("stale playlist served from %d: %+v", mseq, segs)
	}
}

// Caption playlists take the rung's stamps and tags by sequence number;
// their URIs (FFmpeg 5.1's "." included) pass through, and no .vtt is read.
func TestTimelineCaptionsUseRungStamps(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720")
	writeRun(t, dir, "v720", 0, chain(0, 4, 2, 1.4)...)
	restarted(tl)
	rv := writeRun(t, dir, "v720", 0, catSegs(chain(0, 4, 2, 1.4), []seg{{4, 30, 1.4}})...)
	tl.Observe()
	caps := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.000,\n.\n" +
		"#EXTINF:4.000,\nv7201.vtt\n#EXT-X-DISCONTINUITY\n#EXTINF:4.000,\nv7202.vtt\n")
	cm, cd, cs := served(t, tl, "v720_vtt", caps)
	vm, vd, vs := served(t, tl, "v720", rv)
	if len(cs) != 3 || cs[0].uri != "." || cs[2].uri != "v7202.vtt" {
		t.Fatalf("caption segments %+v", cs)
	}
	if cm != 0 || vm != 0 || cd != 0 || vd != 0 || dnum(cd, cs, 0) != 0 || dnum(cd, cs, 2) != 1 || dnum(vd, vs, 2) != 1 {
		t.Fatalf("caption mseq %d dseq %d dnum %d→%d; video mseq %d dseq %d dnum %d",
			cm, cd, dnum(cd, cs, 0), dnum(cd, cs, 2), vm, vd, dnum(vd, vs, 2))
	}
	for i := range cs {
		if !cs[i].pdt.Equal(vs[i].pdt) || cs[i].disc != vs[i].disc {
			t.Fatalf("caption %d %+v, video %+v", i, cs[i], vs[i])
		}
	}
}

func TestTimelineDropsFFmpegTimelineTags(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720")
	raw := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY\n" +
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T19:00:00.000+0000\n#EXTINF:4.004000,\nv720_00000.ts\n" +
		"#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:2026-10-11T18:00:00.000+0000\n#EXTINF:4.004000,\nv720_00001.ts\n")
	if err := os.WriteFile(filepath.Join(dir, "v720.m3u8"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, pts := range []float64{1.4, 5.404} { // a run opens only once its first PTS is read
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("v720_%05d.ts", i)), tsWithPTS(pts), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tl.Observe()
	out := string(tl.Retime("v720", raw))
	if strings.Contains(out, "#EXT-X-DISCONTINUITY\n") || strings.Contains(out, "+0000") {
		t.Fatalf("FFmpeg's timeline tags survived:\n%s", out)
	}
	if !strings.HasPrefix(out, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n") {
		t.Fatalf("header:\n%s", out)
	}
	if strings.Count(out, "#EXT-X-PROGRAM-DATE-TIME:") != 2 || !strings.Contains(out, "#EXTINF:4.004000,\nv720_00001.ts\n") {
		t.Fatalf("segments:\n%s", out)
	}
}

// Long sessions keep bounded state: stamps and skips for the window only,
// one current run, anchors compacted.
func TestTimelineStateStaysBounded(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	end, pts := 0.0, 1.4
	var window []seg
	for i := 0; i < 60; i++ {
		if i%6 == 5 {
			end += 5 // an outage FFmpeg closed
		}
		if i%15 == 14 {
			restarted(tl) // and now and then a restart
			pts = 1.4
		}
		end += 4
		window = append(window, seg{4, end, pts})
		pts += 4
		if len(window) > 5 {
			window = window[1:]
		}
		writeRun(t, dir, "v480", int64(i+1-len(window)), window...)
		writeRun(t, dir, "aac0", int64(i+1-len(window)), window...)
		tl.Observe()
	}
	for _, n := range []string{"v480", "aac0"} {
		r := tl.rends[n]
		if len(r.segs) > 5 || len(r.skip) > 5 {
			t.Fatalf("%s keeps %d stamps and %d skips for a 5-segment window", n, len(r.segs), len(r.skip))
		}
	}
	if len(tl.cur.anchors) > 1 {
		t.Fatalf("%d anchors kept", len(tl.cur.anchors))
	}
	// Audio runs far ahead of a reference that stopped: its waiting
	// segments are bounded too.
	writeRun(t, dir, "aac0", 60, chain(end, 4, 2*timelineMaxPending, pts)...)
	tl.Observe()
	if n := len(tl.rends["aac0"].pending); n > timelineMaxPending {
		t.Fatalf("%d audio segments waiting", n)
	}
}

func TestTimelineUnwrapsPTS(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4, ptsPeriod - 6}, seg{4, 8, ptsPeriod - 2}, seg{4, 12, 2})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !near(segs[2].pdt, sec(8)) {
		t.Fatalf("segment after the PTS wrap at %v, want %v", segs[2].pdt, sec(8))
	}
}

func TestSegmentFirstPTSSkipsTables(t *testing.T) {
	pat := make([]byte, 188)
	for i := range pat {
		pat[i] = 0xFF
	}
	copy(pat, []byte{0x47, 0x40, 0x00, 0x10, 0x00, 0x00, 0xB0, 0x0D})
	path := filepath.Join(t.TempDir(), "s.ts")
	if err := os.WriteFile(path, append(pat, tsWithPTS(12.5)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, ok := segmentFirstPTS(path); !ok || v != 12*90000+45000 {
		t.Fatalf("segmentFirstPTS = %d, %v", v, ok)
	}
}

func TestTimelineWindow(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 5, seg{4, 4, 1.4}, seg{4, 8, 5.4})
	tl.Observe()
	w := tl.Window("v480")
	if w.MediaSequence != 0 || len(w.Segments) != 2 || !near(w.WindowStart(), sec(0)) || !near(w.LiveEdge(), sec(8)) {
		t.Fatalf("window %+v start %v edge %v", w, w.WindowStart(), w.LiveEdge())
	}
	if (WindowTimeline{}).LiveEdge() != (time.Time{}) || len(tl.Window("nope").Segments) != 0 {
		t.Fatal("empty window has segments or a live edge")
	}
}

func TestParseMediaPlaylist(t *testing.T) {
	p := ParseMediaPlaylist([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:9\n#EXT-X-DISCONTINUITY\n" +
		"#EXTINF:4.004000,\nv_00009.ts\n#EXT-X-ENDLIST\n"))
	if p.MediaSequence != 9 || len(p.Header) != 2 || len(p.Segments) != 1 || len(p.Trailer) != 1 {
		t.Fatalf("%+v", p)
	}
	if s := p.Segments[0]; s.Duration != 4.004 || s.URI != "v_00009.ts" || len(s.Tags) != 1 {
		t.Fatalf("segment %+v", s)
	}
}

// The review's storm case: a restarted run's video covers media [0,4) and its audio
// starts at 8. Seen is not publishable: the run stays held instead of
// opening with video alone, and the crash that follows discards it. The
// next run opens as discontinuity 1 in both renditions, and audio is still
// required afterwards.
func TestTimelineGateWaitsForAPublishableSegmentInEveryRendition(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	base := chain(0, 4, 3, 1.4)
	writeRun(t, dir, "v480", 0, base...)
	writeRun(t, dir, "aac0", 0, base...)
	tl.Observe()
	restarted(tl)
	rv := writeRun(t, dir, "v480", 0, catSegs(base, []seg{{4, 30, 1.4}})...) // run 1: video [0,4)
	writeRun(t, dir, "aac0", 0, catSegs(base, []seg{{4, 38, 9.4}})...)       // audio from media 8
	tl.Observe()
	if _, _, vs := served(t, tl, "v480", rv); len(vs) != 3 {
		t.Fatalf("run 1 opened with video alone: %d video segments served", len(vs))
	}
	if missing := tl.blockingLocked(tl.cur); strings.Join(missing, ",") != "aac0" {
		t.Fatalf("blocking %v, want [aac0]", missing)
	}
	restarted(tl) // the crash: run 1 is discarded
	rv = writeRun(t, dir, "v480", 0, catSegs(base, []seg{{4, 30, 1.4}, {4, 60, 1.4}, {4, 64, 5.4}, {4, 68, 9.4}})...)
	ra := writeRun(t, dir, "aac0", 0, catSegs(base, []seg{{4, 38, 9.4}, {4, 60, 1.42}, {4, 64, 5.42}})...)
	tl.Observe()
	_, dv, vs := served(t, tl, "v480", rv)
	_, da, as := served(t, tl, "aac0", ra)
	if len(vs) != 6 || len(as) != 5 || !vs[3].disc || !as[3].disc || vs[3].uri != "v480_00004.ts" || as[3].uri != "aac0_00004.ts" {
		t.Fatalf("run 2:\nvideo %+v\naudio %+v", vs, as)
	}
	for i := 3; i < 5; i++ {
		if dnum(dv, vs, i) != 1 || dnum(da, as, i) != 1 {
			t.Fatalf("segment %d: video discontinuity %d, audio %d; want 1 and 1", i, dnum(dv, vs, i), dnum(da, as, i))
		}
	}
	// The required cohort survived the discarded run: a run without audio
	// is held, then expired.
	restarted(tl)
	writeRun(t, dir, "v480", 0, catSegs(base, []seg{{4, 30, 1.4}, {4, 60, 1.4}, {4, 64, 5.4}, {4, 68, 9.4}, {4, 90, 1.4}})...)
	tl.Observe()
	if missing, _, expired := tl.ExpireStalledRun(timelineGateTimeout, true); !expired || strings.Join(missing, ",") != "aac0" {
		t.Fatalf("ExpireStalledRun = %v, %v; want [aac0], true", missing, expired)
	}
}

// Gap anchors at media 8 and 16 are both kept while the audio playlist is
// missing; when it shows up, every audio segment gets the shift the video
// segment at the same media point got.
func TestTimelineDelayedRenditionGetsEveryGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	video := []seg{{4, 4, 1.4}, {4, 8, 5.4}, {4, 17, 9.4}, {4, 21, 13.4}, {4, 30, 17.4}, {4, 34, 21.4}, {4, 38, 25.4}}
	writeRun(t, dir, "v480", 0, video[:3]...)
	tl.Observe()
	rv := writeRun(t, dir, "v480", 0, video...)
	tl.Observe()
	tl.Observe()
	if n := strings.Count(strings.Join(*logs, "\n"), " gap before "); n != 2 {
		t.Fatalf("%d gaps logged, want 2: %v", n, *logs)
	}
	ra := writeRun(t, dir, "aac0", 0, chain(40, 4, 6, 1.42)...) // late playlist, late files
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 7 || len(as) != 6 {
		t.Fatalf("served %d video, %d audio", len(vs), len(as))
	}
	for i := range as {
		if !near(as[i].pdt, vs[i].pdt.Add(20*time.Millisecond)) {
			t.Fatalf("audio %d at %v, video %d at %v: want 20 ms apart", i, as[i].pdt, i, vs[i].pdt)
		}
	}
}

// A first segment whose PTS can't be read yet (file not complete) keeps the
// run held; once it reads, the segment lands where its PTS says (6 s after
// the audio's), not where EXTINF chaining would have put it.
func TestTimelineWaitsForFirstSegmentPTS(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0", "ac30")
	writeRun(t, dir, "v480", 0, seg{4, 4, -1}, seg{4, 8, 11.4}) // the first file has no PTS yet
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4, 4, 1.4)...)
	for i := 0; i < timelinePTSReads+2; i++ {
		tl.Observe()
	}
	if _, _, as := served(t, tl, "aac0", ra); len(as) != 0 {
		t.Fatalf("run opened before the reference's first PTS was read: %d audio segments served", len(as))
	}
	if missing, _, expired := tl.ExpireStalledRun(time.Hour, false); expired {
		t.Fatalf("expired with no input fed (%v)", missing)
	}
	rv := writeRun(t, dir, "v480", 0, seg{4, 10, 7.4}, seg{4, 14, 11.4}) // now complete
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 2 || len(as) != 3 { // audio [12,16) waits for video past 16
		t.Fatalf("served %d video, %d audio", len(vs), len(as))
	}
	if !near(vs[0].pdt, as[0].pdt.Add(6*time.Second)) {
		t.Fatalf("video starts at %v, audio at %v: want 6 s apart (their PTS)", vs[0].pdt, as[0].pdt)
	}
	// A rendition that joins the open run late (not required yet) waits for
	// its first PTS too, rather than chaining from media 0.
	writeRun(t, dir, "ac30", 0, seg{4, 12, -1}, seg{4, 16, 13.4})
	for i := 0; i < timelinePTSReads+2; i++ {
		tl.Observe()
	}
	if n := len(tl.Window("ac30").Segments); n != 0 {
		t.Fatalf("%d AC-3 segments stamped before the first PTS was read", n)
	}
	writeRun(t, dir, "ac30", 0, seg{4, 12, 9.4}, seg{4, 16, 13.4})
	tl.Observe()
	// [8,12) is covered by the video's [6,14); [12,16) waits for more video.
	if w := tl.Window("ac30"); len(w.Segments) != 1 || !near(w.Segments[0].Start, as[0].pdt.Add(8*time.Second)) {
		t.Fatalf("AC-3 window %+v, want 1 segment from %v", w, as[0].pdt.Add(8*time.Second))
	}
}

// The caption playlist lags the reference across a window slide: the
// reference's stamps stay while the caption playlist still lists them,
// and its MEDIA-SEQUENCE never goes back, even for a staler read.
func TestTimelineCaptionSequenceNeverGoesBack(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720")
	caps := func(mseq int64, n int) []byte {
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "#EXTINF:4.000,\nv720%d.vtt\n", mseq+int64(i))
		}
		if err := os.WriteFile(filepath.Join(dir, "v720_vtt.m3u8"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte(b.String())
	}
	writeRun(t, dir, "v720", 0, chain(0, 4, 3, 1.4)...)
	c0 := caps(0, 3)
	tl.Observe()
	// The video window slides to 4..6; the caption playlist is still at 0.
	writeRun(t, dir, "v720", 4, seg{4, 20, 17.4}, seg{4, 24, 21.4}, seg{4, 28, 25.4})
	tl.Observe() // (3 slid out unobserved: no stamp, never served)
	mseq, _, cs := served(t, tl, "v720_vtt", c0)
	if mseq != 0 || len(cs) != 3 || !near(cs[0].pdt, sec(0)) {
		t.Fatalf("lagging captions: mseq %d %+v", mseq, cs)
	}
	c2 := caps(2, 5)
	tl.Observe()
	if mseq, _, cs = served(t, tl, "v720_vtt", c2); mseq != 2 || len(cs) != 1 {
		t.Fatalf("captions from 2: mseq %d %+v (2 is stamped; 3 never was)", mseq, cs)
	}
	if mseq, _, _ = served(t, tl, "v720_vtt", c0); mseq < 2 {
		t.Fatalf("a staler caption read served MEDIA-SEQUENCE %d after 2", mseq)
	}
	// A read listing only the never-stamped 3 is empty, numbered for what
	// follows it (4 is Bowtie's 3), not for the end of the window.
	if mseq, _, cs = served(t, tl, "v720_vtt", caps(3, 1)); mseq != 3 || len(cs) != 0 {
		t.Fatalf("captions listing only 3: mseq %d %+v", mseq, cs)
	}
	if mseq, _, cs = served(t, tl, "v720_vtt", c2); mseq != 3 || len(cs) != 0 {
		t.Fatalf("captions went back after an empty read: mseq %d %+v", mseq, cs)
	}
	if mseq, _, cs = served(t, tl, "v720_vtt", caps(4, 3)); mseq != 3 || len(cs) != 3 {
		t.Fatalf("captions from 4: mseq %d %+v", mseq, cs)
	}
	// 2's stamp is still kept (the observer last saw the captions at 2),
	// but this playlist was already served from 3.
	if mseq, _, cs = served(t, tl, "v720_vtt", c2); mseq != 3 || len(cs) != 0 {
		t.Fatalf("captions went back: mseq %d %+v", mseq, cs)
	}
	if mseq, _, vs := served(t, tl, "v720", writeRun(t, dir, "v720", 4, seg{4, 20, 17.4}, seg{4, 24, 21.4}, seg{4, 28, 25.4})); mseq != 3 || len(vs) != 3 {
		t.Fatalf("video after the slide: mseq %d, %d segments", mseq, len(vs))
	}
	caps(6, 1)
	tl.Observe()
	if len(tl.rends["v720"].segs) != 3 {
		t.Fatalf("%d stamps kept once the captions caught up, want the 3 in the window", len(tl.rends["v720"].segs))
	}
}

// Liveness: a run is charged fed time from its process start; quiet
// ticks (no fresh bytes) pause the clock and never erase it; an expired run
// never opens; an open run in which a required rendition stops publishing
// expires too.
func TestTimelineStalledRunsExpireAtomically(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	for i := 0; i < 100; i++ { // an outage: never charged
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, false); expired {
			t.Fatal("expired while not fed")
		}
	}
	for _, fresh := range []bool{true, false, true, false} { // 10 s fed, interleaved with quiet ticks
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, fresh); expired {
			t.Fatal("expired after 10 s fed")
		}
	}
	if missing, stalls, expired := tl.ExpireStalledRun(5*time.Second, true); !expired || stalls != 1 || strings.Join(missing, ",") != "v480" {
		t.Fatalf("a run with no segment after 15 s fed: %v, %d, %v", missing, stalls, expired)
	}
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	if tl.cur.state != runHeld {
		t.Fatal("an expired run opened")
	}
	if stalls := restarted(tl); stalls != 1 {
		t.Fatalf("MarkRestart after an expiry: %d stalls, want 1 (counted once)", stalls)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "discarded") {
		t.Fatalf("expired run not discarded: %v", *logs)
	}
	rv := writeRun(t, dir, "v480", 0, catSegs(chain(0, 4, 2, 1.4), chain(40, 4, 2, 1.4))...)
	writeRun(t, dir, "aac0", 0, chain(40, 4, 2, 1.4)...)
	tl.Observe()
	if _, _, vs := served(t, tl, "v480", rv); len(vs) != 2 || tl.stalls != 1 {
		t.Fatalf("run 1: %d segments served, %d stalls (opening must not reset them)", len(vs), tl.stalls)
	}
	// The reference stops; audio goes on (and waits for it).
	writeRun(t, dir, "aac0", 0, chain(40, 4, 6, 1.4)...)
	tl.Observe()
	tl.ExpireStalledRun(5*time.Second, true) // publication at open counts for this tick
	for i := 0; i < 2; i++ {
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, true); expired {
			t.Fatalf("expired after %d s of fed time without publishing", 5*(i+1))
		}
	}
	missing, stalls, expired := tl.ExpireStalledRun(5*time.Second, true)
	if !expired || stalls != 2 || !strings.HasPrefix(strings.Join(missing, ","), "v480") {
		t.Fatalf("an open run whose reference stopped: %v, %d, %v", missing, stalls, expired)
	}
	if _, _, expired := tl.ExpireStalledRun(time.Hour, true); expired {
		t.Fatal("the same run expired twice")
	}
	// An expired open run never publishes again: the reference resumes
	// before the kill lands, and neither the observer nor MarkRestart's
	// final observation stamps or serves any of it.
	ra := writeRun(t, dir, "aac0", 0, chain(40, 4, 6, 1.4)...)
	_, _, before := served(t, tl, "aac0", ra)
	vWin, aWin := len(tl.Window("v480").Segments), len(tl.Window("aac0").Segments)
	rv = writeRun(t, dir, "v480", 0, catSegs(chain(0, 4, 2, 1.4), chain(40, 4, 6, 1.4))...)
	check := func(when string) {
		t.Helper()
		_, _, vs := served(t, tl, "v480", rv)
		_, _, as := served(t, tl, "aac0", ra)
		if len(vs) != 2 || len(as) != len(before) ||
			len(tl.Window("v480").Segments) != vWin || len(tl.Window("aac0").Segments) != aWin {
			t.Fatalf("%s: an expired run published: video %d (want 2), audio %d (want %d)", when, len(vs), len(as), len(before))
		}
	}
	tl.Observe()
	check("after Observe")
	if stalls := tl.MarkRestart(); stalls != 2 {
		t.Fatalf("MarkRestart after an open-run expiry: %d stalls, want 2 (counted once)", stalls)
	}
	check("after MarkRestart")
	// Attach attempts that never start FFmpeg (an outage) end nothing and
	// count nothing: MarkRestart is idempotent until ProcessStarted.
	cur, runs := tl.cur, tl.runs
	for i := 0; i < 5; i++ {
		if stalls := tl.MarkRestart(); stalls != 2 || tl.cur != cur || tl.runs != runs {
			t.Fatalf("attempt %d without a process: %d stalls, new run %v", i, stalls, tl.cur != cur)
		}
	}
	tl.ProcessStarted()
	if stalls := tl.MarkRestart(); stalls != 3 {
		t.Fatalf("a started process that crashed: %d stalls, want 3", stalls)
	}
}

// grower appends segments to renditions over time (a window of 5), with
// PTS and mtimes advancing together.
type grower struct {
	t     *testing.T
	dir   string
	segs  map[string][]seg
	first map[string]int64 // FFmpeg sequence of segs[name][0]
}

func newGrower(t *testing.T, dir string) *grower {
	return &grower{t: t, dir: dir, segs: map[string][]seg{}, first: map[string]int64{}}
}

// add appends one segment to name and rewrites its playlist.
func (g *grower) add(name string, s seg) []byte {
	g.segs[name] = append(g.segs[name], s)
	if len(g.segs[name]) > 5 {
		g.segs[name] = g.segs[name][1:]
		g.first[name]++
	}
	return writeRun(g.t, g.dir, name, g.first[name], g.segs[name]...)
}

// Ruling: opening doesn't reset the stall budget; three
// open-then-reference-stopped runs use it up; only 60 s of every required
// rendition publishing (fed) resets it.
func TestTimelineStallBudgetOnlyResetsAfterHealthyOutput(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	g := newGrower(t, dir)
	end := 0.0
	for cycle := 1; cycle <= timelineMaxStalls; cycle++ {
		if cycle > 1 {
			if stalls := restarted(tl); stalls != cycle-1 {
				t.Fatalf("cycle %d: MarkRestart after an expiry = %d", cycle, stalls)
			}
		}
		for i, pts := range []float64{1.4, 5.4} { // a new run that opens
			end += 4
			g.add("v480", seg{4, end, pts})
			g.add("aac0", seg{4, end, pts})
			_ = i
		}
		tl.Observe()
		if tl.cur.state != runOpen {
			t.Fatalf("cycle %d: run didn't open", cycle)
		}
		var stalls int
		expired := false
		for i := 0; i < 5 && !expired; i++ { // the reference stops; FFmpeg is still fed
			_, stalls, expired = tl.ExpireStalledRun(5*time.Second, true)
		}
		if !expired || stalls != cycle {
			t.Fatalf("cycle %d: expired %v, stalls %d", cycle, expired, stalls)
		}
	}
	// A healthy run at broadcast cadence: video skips one tick in three
	// (segments a little over 5 s) and audio trails it by a tick, so each
	// rendition sometimes has a tick without a new segment. That is still
	// continuous; two silent ticks in a row are not, and restart the 60 s.
	restarted(tl)
	pts, vEnd := 1.4, end
	var lastVideo *seg
	tick := func(video bool) {
		t.Helper()
		if lastVideo != nil { // audio: the segment video added a tick ago
			g.add("aac0", *lastVideo)
			lastVideo = nil
		}
		if video {
			vEnd += 4
			s := seg{4, vEnd, pts}
			g.add("v480", s)
			lastVideo = &s
			pts += 4
		}
		tl.Observe()
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, true); expired {
			t.Fatal("a healthy run expired")
		}
	}
	for i := 0; i < 10; i++ { // opens at tick 1; 45 s healthy
		tick(i%3 != 2)
	}
	if tl.cur.state != runOpen || tl.stalls != timelineMaxStalls {
		t.Fatalf("state %v, budget %d before the gap", tl.cur.state, tl.stalls)
	}
	tick(false) // 50 s
	tick(false) // video silent for two ticks: not continuous
	for i := 0; i < 16; i++ {
		tick(i%3 != 2)
		if i < 11 && tl.stalls != timelineMaxStalls {
			t.Fatalf("budget reset %d s after a two-tick silence", 5*(i+1))
		}
	}
	if tl.stalls != 0 {
		t.Fatalf("budget %d after 75 s of output at broadcast cadence, want 0", tl.stalls)
	}
}

// A later reference segment whose PTS can't be read yet isn't published:
// nothing is stamped (or anchored) from a guess. When it reads, 6 s past
// where EXTINF would put it (a hole FFmpeg kept), both renditions land on
// its PTS.
func TestTimelineWaitsForEveryReferencePTS(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, seg{4, 4, 1.4})
	writeRun(t, dir, "aac0", 0, seg{4, 4, 1.4})
	tl.Observe()
	rv := writeRun(t, dir, "v480", 0, seg{4, 4, 1.4}, seg{4, 14, -1}) // not readable yet
	ra := writeRun(t, dir, "aac0", 0, seg{4, 4, 1.4}, seg{4, 14, 11.4})
	for i := 0; i < timelinePTSReads-1; i++ {
		tl.Observe()
	}
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 1 || len(as) != 1 {
		t.Fatalf("served %d video, %d audio while the video PTS is unread; want 1 and 1", len(vs), len(as))
	}
	writeRun(t, dir, "v480", 0, seg{4, 4, 1.4}, seg{4, 14, 11.4})
	tl.Observe()
	_, _, vs = served(t, tl, "v480", rv)
	_, _, as = served(t, tl, "aac0", ra)
	if len(vs) != 2 || len(as) != 2 || !near(vs[1].pdt, sec(10)) || !near(as[1].pdt, sec(10)) {
		t.Fatalf("video %+v\naudio %+v\nwant both second segments at %v", vs, as, sec(10))
	}
	if strings.Contains(strings.Join(*logs, "\n"), "gap") {
		t.Fatalf("a gap was anchored: %v", *logs)
	}
}

// Audio cut 20 ms before or after the video around a closed 5 s gap gets
// the video's shift (the reference interval it overlaps most), whichever
// playlist is published first: the same moment carries the same PDT
// within one segment, and the same discontinuity number.
func TestTimelineNearAlignedCutsShareTheGapShift(t *testing.T) {
	for _, off := range []float64{-0.02, 0.02} {
		for _, audioFirst := range []bool{false, true} {
			name := fmt.Sprintf("audio %+.0f ms, audio first %v", off*1000, audioFirst)
			dir := t.TempDir()
			tl, _, _ := newTestTimeline(dir, "v480", "aac0")
			ends := []float64{4, 8, 17, 21, 25} // the gap is before the third segment
			var video, audio []seg
			for i, e := range ends {
				video = append(video, seg{4, e, 1.4 + 4*float64(i)})
				audio = append(audio, seg{4, e + off, 1.4 + off + 4*float64(i)})
			}
			writeRun(t, dir, "v480", 0, video[:2]...)
			if audioFirst {
				writeRun(t, dir, "aac0", 0, audio...)
			}
			tl.Observe()
			rv := writeRun(t, dir, "v480", 0, video...)
			ra := writeRun(t, dir, "aac0", 0, audio...)
			tl.Observe()
			_, dv, vs := served(t, tl, "v480", rv)
			_, da, as := served(t, tl, "aac0", ra)
			if len(vs) != 5 || len(as) < 4 {
				t.Fatalf("%s: served %d video, %d audio", name, len(vs), len(as))
			}
			for i := 0; i < 4; i++ {
				if d := as[i].pdt.Sub(vs[i].pdt); d < -30*time.Millisecond || d > 30*time.Millisecond {
					t.Fatalf("%s: segment %d: audio %v, video %v (%v apart)", name, i, as[i].pdt, vs[i].pdt, d)
				}
				if dnum(dv, vs, i) != dnum(da, as, i) {
					t.Fatalf("%s: segment %d: discontinuity %d vs %d", name, i, dnum(dv, vs, i), dnum(da, as, i))
				}
			}
		}
	}
}

// More than timelineMaxAnchors gaps while a required rendition lags: the
// anchors it needs are never dropped; the run stops publishing and
// expires (counted), and nothing is stamped from partial history. A
// rendition that has never published only loses its segments from before
// the kept history.
func TestTimelineAnchorCapNeverStampsFromIncompleteHistory(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	g := newGrower(t, dir)
	g.add("v480", seg{4, 4, 1.4})
	g.add("aac0", seg{4, 4, 1.4})
	tl.Observe()
	end, pts := 4.0, 1.4
	for i := 0; i < timelineMaxAnchors+3; i++ { // a 3 s closed gap before every segment
		end, pts = end+7, pts+4
		g.add("v480", seg{4, end, pts})
		tl.Observe()
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "stopping the run") {
		t.Fatalf("anchor cap not hit: %d anchors", len(tl.cur.anchors))
	}
	if a := tl.cur.anchors; len(a) == 0 || a[0].media > 4+1e-9 {
		t.Fatalf("an anchor aac0 needs was dropped: first kept at media %v", a[0].media)
	}
	missing, stalls, expired := tl.ExpireStalledRun(0, false) // no fed time needed
	if !expired || stalls != 1 || strings.Join(missing, ",") != "aac0" {
		t.Fatalf("ExpireStalledRun = %v, %d, %v; want [aac0], 1, true", missing, stalls, expired)
	}
	g.add("aac0", seg{4, 11, 5.4}) // the lagging rendition catches up: still nothing stamped
	tl.Observe()
	if n := len(tl.Window("aac0").Segments); n != 1 {
		t.Fatalf("%d audio segments stamped after the run stopped, want 1", n)
	}
	if stalls := restarted(tl); stalls != 1 {
		t.Fatalf("MarkRestart = %d stalls, want 1 (the expiry already counted)", stalls)
	}

	// ac30 has never published: history only it needs is dropped, and its
	// segments from before the kept history are never served.
	dir2 := t.TempDir()
	tl2, logs2, _ := newTestTimeline(dir2, "v480", "ac30")
	g2 := newGrower(t, dir2)
	end, pts = 4.0, 1.4
	g2.add("v480", seg{4, end, pts})
	tl2.Observe()
	for i := 0; i < timelineMaxAnchors+3; i++ {
		end, pts = end+7, pts+4
		g2.add("v480", seg{4, end, pts})
		tl2.Observe()
	}
	if !strings.Contains(strings.Join(*logs2, "\n"), "never served") || len(tl2.cur.anchors) > timelineMaxAnchors {
		t.Fatalf("%d anchors kept; logs %v", len(tl2.cur.anchors), (*logs2)[len(*logs2)-1])
	}
	vw := tl2.Window("v480")
	last := vw.Segments[len(vw.Segments)-2]
	writeRun(t, dir2, "ac30", 0, seg{4, 4, 1.4}, seg{4, end - 4, pts - 4}) // media 0, then the video's last-but-one
	tl2.Observe()
	aw := tl2.Window("ac30")
	if len(aw.Segments) != 1 || !near(aw.Segments[0].Start, last.Start) {
		t.Fatalf("ac30 window %+v, want one segment at %v", aw, last.Start)
	}
}

// A held rendition's first segment is not the last one trimmed when the
// window jumps by more than one. The survivor has to stay the run's first,
// or the gate never opens.
func TestTimelineTrimCarriesFirstToTheSurvivor(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	base := chain(0, 4, 2, 1.4)
	writeRun(t, dir, "v480", 0, base...)
	writeRun(t, dir, "aac0", 0, base...)
	tl.Observe()
	restarted(tl)

	// Video seq 2–5, only 2 marked first. Audio has not arrived, so the run stays held.
	writeRun(t, dir, "v480", 2, chain(20, 4, 4, 1.4)...)
	tl.Observe()
	if tl.cur.state != runHeld {
		t.Fatal("video-only run opened")
	}
	// Window advances to 4: seq 2 and 3 drop. Seq 2 carried first; seq 3 did not.
	rv := writeRun(t, dir, "v480", 4, seg{4, 32, 9.4}, seg{4, 36, 13.4})
	ra := writeRun(t, dir, "aac0", 2, seg{4, 40, 9.4})
	tl.Observe()
	if tl.cur.state != runOpen {
		t.Fatalf("run stayed held after the window slide; blocking %v", tl.blockingLocked(tl.cur))
	}
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 2 || len(as) != 1 || !vs[0].disc || !as[0].disc {
		t.Fatalf("video %+v\naudio %+v", vs, as)
	}
	if !near(vs[0].pdt, as[0].pdt) || !near(vs[1].pdt, sec(32)) {
		t.Fatalf("video %+v audio %+v, want a shared start and video[1] at %v", vs, as, sec(32))
	}
}

// Trimming the whole queue must keep "first" for the next segment that
// arrives, which is now this rendition's first in the run.
func TestTimelineTrimKeepsFirstForTheNextArrival(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	base := chain(0, 4, 2, 1.4)
	writeRun(t, dir, "v480", 0, base...)
	writeRun(t, dir, "aac0", 0, base...)
	tl.Observe()
	restarted(tl)

	writeRun(t, dir, "aac0", 2, chain(20, 4, 2, 1.4)...) // seq 2 (first) and 3, no video
	tl.Observe()
	if err := os.WriteFile(filepath.Join(dir, "aac0.m3u8"),
		[]byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tl.Observe() // both pending segments slide out; the queue is empty

	ra := writeRun(t, dir, "aac0", 6, seg{4, 50, 1.4})
	rv := writeRun(t, dir, "v480", 2, chain(40, 4, 2, 1.4)...)
	tl.Observe()
	if tl.cur.state != runOpen {
		t.Fatalf("run stayed held for the arrival after an empty trim; blocking %v", tl.blockingLocked(tl.cur))
	}
	_, _, as := served(t, tl, "aac0", ra)
	_, _, vs := served(t, tl, "v480", rv)
	if len(as) != 1 || !as[0].disc || len(vs) != 2 || !vs[0].disc || !near(as[0].pdt, vs[0].pdt) {
		t.Fatalf("video %+v\naudio %+v", vs, as)
	}
}

// A rendition that has never published joins after the reference has passed
// half a 33-bit PTS period. Its first segment must land on the reference's
// media clock, not one period (~26.5 h) earlier.
func TestTimelineLateJoinPastHalfPTSPeriod(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	// Steps stay under half a PTS period, so the reference itself chains
	// forward. The run's media end is past that halfway point.
	const join = 50400.0
	rv := writeRun(t, dir, "v480", 0,
		seg{4, 4, 1.4},
		seg{40000, 40004, 5.4},
		seg{20000, 60004, 40005.4},
	)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	if len(vs) != 3 || !near(vs[0].pdt, sec(0)) || !near(vs[2].pdt, sec(40004)) {
		t.Fatalf("video %+v, want the reference out to %v", vs, sec(40004))
	}
	ra := writeRun(t, dir, "aac0", 0, seg{4, 100, 1.4 + join})
	tl.Observe()
	_, _, as := served(t, tl, "aac0", ra)
	if len(as) != 1 || as[0].disc || !near(as[0].pdt, sec(join)) {
		got := "<none>"
		if len(as) == 1 {
			got = as[0].pdt.Format(time.RFC3339Nano)
		}
		t.Fatalf("audio at %s disc %v, want one untagged segment at %s", got, len(as) == 1 && as[0].disc, sec(join).Format(time.RFC3339Nano))
	}
}

// Captions still list sequence 0 while the video window has slid to 3.
// Sequence 2 was a discarded run. Its skip marker has to survive with the
// stamps, or retiming stops at 2 and withholds 3 (and its discontinuity).
func TestTimelineCaptionSkipsSurviveALaggingPlaylist(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720", "aac0")
	caps := func(mseq int64, n int) []byte {
		t.Helper()
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "#EXTINF:4.000,\nv720%d.vtt\n", mseq+int64(i))
		}
		raw := []byte(b.String())
		if err := os.WriteFile(filepath.Join(dir, "v720_vtt.m3u8"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	base := chain(0, 4, 2, 1.4)
	writeRun(t, dir, "v720", 0, base...)
	writeRun(t, dir, "aac0", 0, base...)
	c0 := caps(0, 2)
	tl.Observe()
	restarted(tl)

	// Run held: video seq 2 only. Audio is required and absent, so 2 is discarded.
	writeRun(t, dir, "v720", 0, catSegs(base, []seg{{4, 30, 1.4}})...)
	tl.Observe()
	restarted(tl)

	rv := writeRun(t, dir, "v720", 3, seg{4, 40, 1.4}, seg{4, 44, 5.4})
	writeRun(t, dir, "aac0", 2, seg{4, 40, 1.42})
	tl.Observe()
	_, _, vs := served(t, tl, "v720", rv)
	if len(vs) != 2 || !vs[0].disc {
		t.Fatalf("recovered video %+v", vs)
	}
	// Captions still start at 0 and list the discarded 2 between 1 and 3.
	mseq, dseq, cs := served(t, tl, "v720_vtt", caps(0, 4))
	if mseq != 0 || len(cs) != 3 || cs[0].uri != "v7200.vtt" || cs[2].uri != "v7203.vtt" || !cs[2].disc || dnum(dseq, cs, 2) != 1 {
		t.Fatalf("lagging captions: mseq %d dseq %d %+v", mseq, dseq, cs)
	}
	if !cs[2].pdt.Equal(vs[0].pdt) || dnum(dseq, cs, 0) != 0 {
		t.Fatalf("caption discontinuity %+v, video %v", cs, vs[0].pdt)
	}
	// The on-disk playlist the observer last saw is still the pre-slide one.
	if _, _, stale := served(t, tl, "v720_vtt", c0); len(stale) != 2 {
		t.Fatalf("captions the observer saw: %+v", stale)
	}
}

// restarted is an append restart whose FFmpeg started.
func restarted(tl *Timeline) int {
	stalls := tl.MarkRestart()
	tl.ProcessStarted()
	return stalls
}
