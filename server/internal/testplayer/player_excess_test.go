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
)

// liveSeg is one primary segment. Poll n lists the first n segments, and
// the handler holds the gap `at` since the previous segment so that poll
// starts on that schedule. EXTINF is `dur` seconds.
type liveSeg struct {
	at  time.Duration
	dur float64
}

// runLive plays segs on a fake HLS server and returns the report. Each poll
// lists one more segment than the poll before it. The gap until the next
// segment is slept inside that poll, so a late wake cannot publish several
// segments at once.
func runLive(t *testing.T, segs []liveSeg, poll, runFor time.Duration, on func(SegmentEvent)) Report {
	t.Helper()
	var (
		mu    sync.Mutex
		polls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			count := n
			if count > len(segs) {
				count = len(segs)
			}
			if n < len(segs) {
				if gap := segs[n].at - segs[n-1].at; gap > 0 {
					time.Sleep(gap)
				}
			}
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n")
			for i := 0; i < count; i++ {
				fmt.Fprintf(&b, "#EXTINF:%.3f,\n/s/%05d.ts\n", segs[i].dur, i)
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
	// Poll 1 is the base. Poll 2 lists three new segments together and
	// holds one duration. Each later poll adds one segment and holds the
	// same duration, so the two segments after the batch take real time.
	script := []struct {
		count int
		hold  time.Duration
	}{
		{1, 0},
		{4, dur},
		{5, dur},
		{6, 0},
	}
	var (
		mu    sync.Mutex
		polls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			step := script[len(script)-1]
			if n <= len(script) {
				step = script[n-1]
			}
			if step.hold > 0 {
				time.Sleep(step.hold)
			}
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n")
			for i := 0; i < step.count; i++ {
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
