package testplayer

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/scenario"
)

// liveSeg is one primary segment, visible once `at` has passed since the
// server started, with EXTINF `dur` seconds.
type liveSeg struct {
	at  time.Duration
	dur float64
}

// runLive plays segs on a fake HLS server and returns the report. The
// playlist grows as each segment's `at` arrives; nothing is published ahead
// of that clock.
func runLive(t *testing.T, segs []liveSeg, poll, runFor time.Duration, on func(SegmentEvent)) Report {
	t.Helper()
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			elapsed := time.Since(start)
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n")
			for i, s := range segs {
				if elapsed < s.at {
					break
				}
				fmt.Fprintf(&b, "#EXTINF:%.3f,\n/s/%05d.ts\n", s.dur, i)
			}
			_, _ = w.Write([]byte(b.String()))
		case len(r.URL.Path) > 3 && r.URL.Path[:3] == "/s/":
			_, _ = w.Write([]byte{0x47})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: poll,
		Probe:     func(context.Context, []byte) ([]Packet, error) { return []Packet{{Dur: 0.25}}, nil },
		OnSegment: on,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runFor)
	defer cancel()
	p.Run(ctx)
	return p.Report()
}

func nearSec(got, want, tol float64) bool {
	return math.Abs(got-want) < tol
}

// A long wait before the first segment after the opening one is not excess:
// the clock starts when that segment arrives, and real-time delivery after
// it reads about zero.
func TestTimelineExcessRealTimeAfterLeadIn(t *testing.T) {
	const (
		seg  = 250 * time.Millisecond
		lead = 1200 * time.Millisecond
		d    = 0.25
	)
	r := runLive(t, []liveSeg{
		{at: 0, dur: d},    // base: newest segment of the first poll
		{at: lead, dur: d}, // reference: first new segment, not counted
		{at: lead + seg, dur: d},
		{at: lead + 2*seg, dur: d},
		{at: lead + 3*seg, dur: d},
	}, 20*time.Millisecond, lead+3*seg+400*time.Millisecond, nil)
	if !nearSec(r.TimelineExcess, 0, 0.5) {
		t.Fatalf("TimelineExcess=%v, want |excess| < 0.5s on a real-time stream after a %.1fs lead-in", r.TimelineExcess, lead.Seconds())
	}
}

// Five extra seconds of media, injected after the reference segment, read
// about +5s. The lead-in before the reference must not move that reading.
func TestTimelineExcessCountsInjectedMedia(t *testing.T) {
	const (
		seg  = 250 * time.Millisecond
		lead = 1200 * time.Millisecond
		d    = 0.25
	)
	var events []SegmentEvent
	r := runLive(t, []liveSeg{
		{at: 0, dur: d},
		{at: lead, dur: d},             // reference
		{at: lead + seg, dur: d},       // real time, excess ≈ 0
		{at: lead + 2*seg, dur: d + 5}, // 5s of media with no extra wall time
		{at: lead + 3*seg, dur: d},
	}, 20*time.Millisecond, lead+3*seg+400*time.Millisecond, func(e SegmentEvent) { events = append(events, e) })
	if !nearSec(r.TimelineExcess, 5, 0.5) {
		t.Fatalf("TimelineExcess=%v, want ≈ +5s", r.TimelineExcess)
	}
	var before, last *SegmentEvent
	for i := range events {
		switch events[i].Seq {
		case 2:
			before = &events[i]
		case 4:
			last = &events[i]
		}
	}
	if before == nil || !nearSec(before.TimelineExcess, 0, 0.5) {
		got := math.NaN()
		if before != nil {
			got = before.TimelineExcess
		}
		t.Fatalf("running excess before the extra media = %v, want ≈ 0", got)
	}
	if last == nil || !nearSec(last.TimelineExcess, 5, 0.5) {
		got := math.NaN()
		if last != nil {
			got = last.TimelineExcess
		}
		t.Fatalf("running excess after the extra media = %v, want ≈ +5s", got)
	}
}

// Five seconds of wall time with no segment reads about −5s.
func TestTimelineExcessCountsSkippedMedia(t *testing.T) {
	const (
		seg  = 250 * time.Millisecond
		lead = 1200 * time.Millisecond
		hole = 5 * time.Second
		d    = 0.25
	)
	// After the reference and one real-time segment, the next segment is
	// hole later than its duration says it should be.
	third := lead + seg + seg + hole
	r := runLive(t, []liveSeg{
		{at: 0, dur: d},
		{at: lead, dur: d},
		{at: lead + seg, dur: d},
		{at: third, dur: d},
	}, 20*time.Millisecond, third+400*time.Millisecond, nil)
	if !nearSec(r.TimelineExcess, -5, 0.5) {
		t.Fatalf("TimelineExcess=%v, want ≈ −5s", r.TimelineExcess)
	}
}

// The first poll after the base that delivers several segments at once
// anchors on the newest of them. None of that batch counts, so the extra
// segments do not become excess at zero wall time. Real-time segments
// after the batch still read about zero.
func TestTimelineExcessAnchorsOnNewestOfFirstBatch(t *testing.T) {
	const d = 1.0
	dur := time.Second
	var (
		mu      sync.Mutex
		polls   int
		batchAt time.Time
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			mu.Lock()
			polls++
			n := polls
			if n > 1 && batchAt.IsZero() {
				batchAt = time.Now()
			}
			elapsed := time.Duration(0)
			if !batchAt.IsZero() {
				elapsed = time.Since(batchAt)
			}
			mu.Unlock()
			// Poll 1: the base only. The next poll releases three new
			// segments together. Two more follow one duration apart.
			count := 1
			if n > 1 {
				count = 4
				if elapsed >= dur {
					count = 5
				}
				if elapsed >= 2*dur {
					count = 6
				}
			}
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&b, "#EXTINF:%.3f,\n/s/%05d.ts\n", d, i)
			}
			_, _ = w.Write([]byte(b.String()))
		case len(r.URL.Path) > 3 && r.URL.Path[:3] == "/s/":
			_, _ = w.Write([]byte{0x47})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*dur+time.Second)
	defer cancel()
	p.Run(ctx)
	r := p.Report()
	if r.SegmentsFetched < 6 {
		t.Fatalf("fetched %d segments, want the base, the batch of 3, and 2 real-time segments", r.SegmentsFetched)
	}
	if !nearSec(r.TimelineExcess, 0, 0.5) {
		t.Fatalf("TimelineExcess=%v, want |excess| < 0.5s when three segments arrive in one poll", r.TimelineExcess)
	}
}

// avSeg is one segment of a rendition in a start-offset fixture.
type avSeg struct {
	disc bool
	dur  float64
	pdt  time.Duration
	pts  float64
}

// runAV serves video (primary), aac0 and any extra audio renditions, probes
// each segment's PTS, and returns the report.
func runAV(t *testing.T, video, aac []avSeg, extra map[string][]avSeg) Report {
	t.Helper()
	t0 := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	ptsOf := map[string]float64{}
	playlists := map[string]string{}
	build := func(name string, segs []avSeg) {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n")
		for i, s := range segs {
			if s.disc {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.3f,\n/s/%s_%05d.ts\n",
				t0.Add(s.pdt).Format("2006-01-02T15:04:05.000Z07:00"), s.dur, name, i)
			ptsOf[fmt.Sprintf("%s_%05d.ts", name, i)] = s.pts
		}
		playlists[name] = b.String()
	}
	build("v480", video)
	if aac != nil {
		build("aac0", aac)
	}
	var media []string
	for name, segs := range extra {
		build(name, segs)
		media = append(media, name)
	}
	// An extra rendition (ac30) is listed before aac0, so the first audio
	// rendition is not aac0. The measurement still prefers aac0 when it exists.
	order := append([]string(nil), media...)
	if aac != nil {
		order = append(order, "aac0")
	}
	var master strings.Builder
	master.WriteString("#EXTM3U\n")
	for _, name := range order {
		fmt.Fprintf(&master, "#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=%q,NAME=%q,URI=\"%s.m3u8?token=tk\"\n", name, name, name)
	}
	master.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aac\"\nv480.m3u8?token=tk\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			_, _ = w.Write([]byte(master.String()))
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/s/"), ".m3u8")
			pl, ok := playlists[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(pl))
		case strings.HasSuffix(r.URL.Path, ".ts"):
			_, _ = w.Write(append([]byte{0x47}, r.URL.Path...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: 20 * time.Millisecond,
		AllRenditions: true,
		Probe: func(_ context.Context, seg []byte) ([]Packet, error) {
			path := string(seg[1:])
			base := path[strings.LastIndex(path, "/")+1:]
			pts, ok := ptsOf[base]
			if !ok {
				return nil, fmt.Errorf("unexpected segment %q", path)
			}
			return []Packet{{PTS: pts, Dur: 1}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	r := p.Report()
	if len(r.Errors) != 0 || r.ProbedAudio == 0 || r.ProbedVideo == 0 {
		t.Fatalf("probed audio=%d video=%d errors %v", r.ProbedAudio, r.ProbedVideo, r.Errors)
	}
	return r
}

func nearDur(got, want, tol time.Duration) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < tol
}

func wantMeasuredRuns(t *testing.T, r Report, n int) {
	t.Helper()
	if r.StartAVRuns != n || r.StartAVMeasured != n || len(r.UnmeasuredStartAV) != 0 {
		t.Fatalf("start runs=%d measured=%d unmeasured=%v, want %d runs all measured",
			r.StartAVRuns, r.StartAVMeasured, r.UnmeasuredStartAV, n)
	}
}

// Audio starting 400 ms before video is invisible to AVSkew once PDT is
// derived from PTS, and visible to MaxStartAVOffset. aac0 is preferred
// over an earlier audio rendition with a larger offset.
func TestStartAVOffsetAudioLeadsVideo(t *testing.T) {
	const d = 1.0
	r := runAV(t,
		[]avSeg{{dur: d, pdt: 400 * time.Millisecond, pts: 10}, {dur: d, pdt: 1400 * time.Millisecond, pts: 11}},
		[]avSeg{{dur: d, pdt: 0, pts: 9.6}, {dur: d, pdt: time.Second, pts: 10.6}},
		map[string][]avSeg{"ac30": {{dur: d, pdt: 0, pts: 8}, {dur: d, pdt: time.Second, pts: 9}}},
	)
	if !nearDur(r.MaxStartAVOffset, 400*time.Millisecond, 2*time.Millisecond) {
		t.Fatalf("MaxStartAVOffset=%v, want 400ms (aac0, not ac30's 2s)", r.MaxStartAVOffset)
	}
	ms := 100.0
	v := (&scenario.Expect{MaxStartAVOffsetMs: &ms}).Check(scenario.Observed{
		MaxStartAVOffset: r.MaxStartAVOffset, ProbedAudio: r.ProbedAudio, ProbedVideo: r.ProbedVideo,
	})
	if len(v) != 1 || !strings.Contains(v[0], "400") || !strings.Contains(v[0], "100") {
		t.Fatalf("violations %v, want one 400ms-over-100ms start-offset violation", v)
	}
}

// Aligned first segments stay under 50 ms, and a 100 ms cap accepts them.
func TestStartAVOffsetAligned(t *testing.T) {
	const d = 1.0
	r := runAV(t,
		[]avSeg{{dur: d, pdt: 0, pts: 10}, {dur: d, pdt: time.Second, pts: 11}},
		[]avSeg{{dur: d, pdt: 0, pts: 10}, {dur: d, pdt: time.Second, pts: 11}},
		nil,
	)
	if r.MaxStartAVOffset >= 50*time.Millisecond {
		t.Fatalf("MaxStartAVOffset=%v, want < 50ms", r.MaxStartAVOffset)
	}
	wantMeasuredRuns(t, r, 1)
	ms := 100.0
	if v := (&scenario.Expect{MaxStartAVOffsetMs: &ms}).Check(scenario.Observed{
		MaxStartAVOffset: r.MaxStartAVOffset, ProbedAudio: r.ProbedAudio, ProbedVideo: r.ProbedVideo,
	}); len(v) != 0 {
		t.Fatalf("aligned stream violations %v", v)
	}
}

// A discontinuity opens a new run. Its own first-segment offset is measured,
// and a later segment of that run with a larger gap is not.
func TestStartAVOffsetMeasuresEachDiscontinuity(t *testing.T) {
	const d = 4.0
	r := runAV(t,
		[]avSeg{
			{dur: d, pdt: 0, pts: 20},
			{dur: d, pdt: 4 * time.Second, pts: 24},
			{disc: true, dur: d, pdt: 8 * time.Second, pts: 80},
			{dur: d, pdt: 12 * time.Second, pts: 84},
		},
		[]avSeg{
			{dur: d, pdt: 0, pts: 20},
			{dur: d, pdt: 4 * time.Second, pts: 24},
			{disc: true, dur: d, pdt: 7600 * time.Millisecond, pts: 79.6}, // 400 ms before this run's video
			{dur: d, pdt: 9 * time.Second, pts: 81},                       // 3 s before the second video segment; PDT follows, so skew stays 0
		},
		nil,
	)
	if !nearDur(r.MaxStartAVOffset, 400*time.Millisecond, 2*time.Millisecond) {
		t.Fatalf("MaxStartAVOffset=%v, want 400ms from the discontinuity's first segments", r.MaxStartAVOffset)
	}
	wantMeasuredRuns(t, r, 2)
	if r.MaxAVStartSkew > 2*time.Millisecond {
		t.Fatalf("MaxAVStartSkew=%v, want ≈0", r.MaxAVStartSkew)
	}
}

// The offset uses the same 33-bit unwrap as the rest of the player.
func TestStartAVOffsetUnwrapsPTS(t *testing.T) {
	const d = 1.0
	videoPTS := 0.25
	audioPTS := ptsPeriod - 0.15 // 400 ms before video, across the wrap
	r := runAV(t,
		[]avSeg{{dur: d, pdt: 400 * time.Millisecond, pts: videoPTS}},
		[]avSeg{{dur: d, pdt: 0, pts: audioPTS}},
		nil,
	)
	if !nearDur(r.MaxStartAVOffset, 400*time.Millisecond, 2*time.Millisecond) {
		t.Fatalf("MaxStartAVOffset=%v, want 400ms across the 33-bit wrap", r.MaxStartAVOffset)
	}
}

// With no aac0, the first audio rendition is the one measured.
func TestStartAVOffsetFallsBackToFirstAudio(t *testing.T) {
	const d = 1.0
	r := runAV(t,
		[]avSeg{{dur: d, pdt: 400 * time.Millisecond, pts: 10}},
		nil,
		map[string][]avSeg{"ac30": {{dur: d, pdt: 0, pts: 9.6}}},
	)
	if !nearDur(r.MaxStartAVOffset, 400*time.Millisecond, 2*time.Millisecond) {
		t.Fatalf("MaxStartAVOffset=%v, want 400ms from the only audio rendition", r.MaxStartAVOffset)
	}
}

// Adjacent PDT ranges do not overlap, but they are still the first segments
// of the same run. A 1 s PTS gap must be reported.
func TestStartAVOffsetAdjacentSegments(t *testing.T) {
	const d = 1.0
	r := runAV(t,
		[]avSeg{{dur: d, pdt: time.Second, pts: 10}},
		[]avSeg{{dur: d, pdt: 0, pts: 9}},
		nil,
	)
	if !nearDur(r.MaxStartAVOffset, time.Second, 2*time.Millisecond) {
		t.Fatalf("MaxStartAVOffset=%v, want 1000ms", r.MaxStartAVOffset)
	}
	wantMeasuredRuns(t, r, 1)
}

func startOffsetViolations(r Report) []string {
	ms := 100.0
	return (&scenario.Expect{MaxStartAVOffsetMs: &ms}).Check(scenario.Observed{
		MaxStartAVOffset:  r.MaxStartAVOffset,
		ProbedAudio:       r.ProbedAudio,
		ProbedVideo:       r.ProbedVideo,
		StartAVRuns:       r.StartAVRuns,
		StartAVMeasured:   r.StartAVMeasured,
		UnmeasuredStartAV: append([]int64(nil), r.UnmeasuredStartAV...),
	})
}

// Video probed only in run 0 and audio only in run 1: neither run has a pair.
func TestStartAVOffsetUnmeasuredWhenSidesSplitAcrossRuns(t *testing.T) {
	const d = 1.0
	r := runAV(t,
		[]avSeg{{dur: d, pdt: 0, pts: 10}},
		[]avSeg{{disc: true, dur: d, pdt: 0, pts: 9}},
		nil,
	)
	if r.StartAVRuns != 2 || r.StartAVMeasured != 0 || len(r.UnmeasuredStartAV) != 2 ||
		r.UnmeasuredStartAV[0] != 0 || r.UnmeasuredStartAV[1] != 1 {
		t.Fatalf("runs=%d measured=%d unmeasured=%v, want 2 runs missing 0 and 1",
			r.StartAVRuns, r.StartAVMeasured, r.UnmeasuredStartAV)
	}
	v := startOffsetViolations(r)
	got := strings.Join(v, "; ")
	if !strings.Contains(got, "discontinuity 0") || !strings.Contains(got, "discontinuity 1") {
		t.Fatalf("violations %v, want both runs unmeasured", v)
	}
}

// aac0 is the chosen audio rendition. Segments from ac30 do not measure the
// run while aac0 itself was never probed.
func TestStartAVOffsetUnmeasuredWhenChosenAudioMissing(t *testing.T) {
	const d = 1.0
	r := runAV(t,
		[]avSeg{{dur: d, pdt: 0, pts: 10}},
		[]avSeg{},
		map[string][]avSeg{"ac30": {{dur: d, pdt: 0, pts: 8}}},
	)
	if r.ProbedAudio == 0 || r.ProbedVideo == 0 {
		t.Fatalf("probed audio=%d video=%d, want both (ac30 must not count as the chosen audio)", r.ProbedAudio, r.ProbedVideo)
	}
	if r.StartAVRuns != 1 || r.StartAVMeasured != 0 || len(r.UnmeasuredStartAV) != 1 || r.UnmeasuredStartAV[0] != 0 {
		t.Fatalf("runs=%d measured=%d unmeasured=%v, want run 0 unmeasured",
			r.StartAVRuns, r.StartAVMeasured, r.UnmeasuredStartAV)
	}
	if r.MaxStartAVOffset != 0 {
		t.Fatalf("MaxStartAVOffset=%v, want 0 when aac0 was not probed", r.MaxStartAVOffset)
	}
	v := startOffsetViolations(r)
	if len(v) != 1 || !strings.Contains(v[0], "discontinuity 0") {
		t.Fatalf("violations %v, want discontinuity 0 unmeasured", v)
	}
}
