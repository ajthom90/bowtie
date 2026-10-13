package testplayer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPlayerFollowsEveryRenditionAndProbes(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	segRe := regexp.MustCompile(`/s/(v480|aac0)_(\d{5})\.ts$`)
	pdt := func(i int) string {
		return time.Date(2026, 10, 11, 20, 0, 4*i, 0, time.UTC).Format("2006-01-02T15:04:05.000Z07:00")
	}
	media := func(name string, n int) string {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
		for i := 0; i <= n; i++ {
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n/s/%s_%05d.ts\n", pdt(i), name, i)
		}
		return b.String()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aac\",NAME=\"English\",URI=\"aac0.m3u8?token=tk\"\n"+
				"#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aac\"\nv480.m3u8?token=tk\n")
		case r.URL.Path == "/s/v480.m3u8" || r.URL.Path == "/s/aac0.m3u8":
			mu.Lock()
			if r.URL.Path == "/s/v480.m3u8" && polls < 4 {
				polls++
			}
			n := polls
			mu.Unlock()
			_, _ = fmt.Fprint(w, media(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/s/"), ".m3u8"), n))
		case segRe.MatchString(r.URL.Path):
			_, _ = w.Write(append([]byte{0x47}, r.URL.Path...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	probe := func(_ context.Context, seg []byte) ([]Packet, error) {
		m := segRe.FindStringSubmatch(string(seg[1:]))
		if m == nil {
			return nil, fmt.Errorf("unexpected segment %q", seg)
		}
		seq, _ := strconv.Atoi(m[2])
		start := float64(seq) * 4
		if m[1] == "aac0" && seq == 2 {
			start += 0.2 // a 200 ms hole before audio segment 2
		}
		var pk []Packet
		for ts := start; ts < float64(seq+1)*4-1e-9; ts += 0.5 {
			pk = append(pk, Packet{PTS: ts, Dur: 0.5})
		}
		return pk, nil
	}

	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: 20 * time.Millisecond,
		AllRenditions: true, Probe: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	r := p.Report()

	if got := strings.Join(r.Renditions, ","); got != "v480,aac0" {
		t.Fatalf("renditions %q, want v480,aac0", got)
	}
	if r.ProbedAudio != 5 || r.ProbedVideo != 5 {
		t.Fatalf("probed audio=%d video=%d, want 5 and 5", r.ProbedAudio, r.ProbedVideo)
	}
	near := func(got, want time.Duration) bool {
		d := got - want
		return d > -time.Millisecond && d < time.Millisecond
	}
	if !near(r.MaxAudioHole, 200*time.Millisecond) {
		t.Fatalf("MaxAudioHole=%v, want 200ms", r.MaxAudioHole)
	}
	if !near(r.MaxAVStartSkew, 200*time.Millisecond) || r.AVPairs != 5 {
		t.Fatalf("MaxAVStartSkew=%v over %d pairs, want 200ms over 5", r.MaxAVStartSkew, r.AVPairs)
	}
	if got := r.Windows[0].Paired; got["v480"][0] != 5 || got["aac0"][0] != 5 {
		t.Fatalf("paired coverage %v, want 5 per rendition in discontinuity 0", got)
	}
	c := r.Consistency
	if !c.PDTMonotonic || !c.PDTAligned || !c.RenditionsMonotonic || !c.DiscontinuitySequenceOK || !c.DiscontinuitiesAligned {
		t.Fatalf("consistency %+v", c)
	}
	if r.TimelineExcess <= 0 {
		t.Fatalf("TimelineExcess=%v: 16 s of segments appeared in well under a second", r.TimelineExcess)
	}
	if len(r.Errors) != 0 || r.FetchErrors != 0 {
		t.Fatalf("errors %v (FetchErrors %d)", r.Errors, r.FetchErrors)
	}
}

func vttTime(sec float64) string {
	ms := int64(sec*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// After a kill the audio numbering runs one ahead of the video; pairing by
// PDT still compares the same moment. Coverage, errors and cues are counted
// per window.
func TestPlayerPairsAVByPDTAndCountsPerWindow(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	segRe := regexp.MustCompile(`/s/(v480|aac0)_(\d{5})\.ts$|/s/v480_vtt_(\d+)\.vtt$`)
	restart := map[string]int{"v480": 2, "aac0": 3, "v480_vtt": 2}
	// Video: 0,1 then a restart at 2. Audio: 0,1,2 then a restart at 3. The
	// restart is the same moment, 68 s, in both.
	pdtOf := func(name string, i int) float64 {
		if i >= restart[name] {
			return 68 + 4*float64(i-restart[name])
		}
		return 4 * float64(i)
	}
	// Both renditions' PTS restart at 100 s.
	ptsOf := func(name string, i int) float64 {
		if i >= restart[name] {
			return 100 + 4*float64(i-restart[name])
		}
		return 4 * float64(i)
	}
	media := func(name string, n int) string {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n")
		for i := 0; i <= n+restart[name]-2; i++ {
			if i == restart[name] {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			at := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC).Add(time.Duration(pdtOf(name, i) * float64(time.Second)))
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n", at.Format("2006-01-02T15:04:05.000Z07:00"))
			if name == "v480_vtt" {
				fmt.Fprintf(&b, "/s/v480_vtt_%d.vtt\n", i)
			} else {
				fmt.Fprintf(&b, "/s/%s_%05d.ts\n", name, i)
			}
		}
		return b.String()
	}
	gone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			_, _ = fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aac\",NAME=\"English\",URI=\"aac0.m3u8?token=tk\"\n"+
				"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"cc\",NAME=\"CC\",URI=\"v480_vtt.m3u8?token=tk\"\n"+
				"#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aac\",SUBTITLES=\"cc\"\nv480.m3u8?token=tk\n")
		case r.URL.Path == "/s/v480.m3u8":
			if polls < 4 {
				polls++
			}
			_, _ = fmt.Fprint(w, media("v480", polls))
		case r.URL.Path == "/s/aac0.m3u8":
			if gone {
				http.NotFound(w, r) // went missing after it was served: an error
				return
			}
			_, _ = fmt.Fprint(w, media("aac0", polls))
		case r.URL.Path == "/s/v480_vtt.m3u8":
			_, _ = fmt.Fprint(w, media("v480_vtt", polls))
		case strings.HasSuffix(r.URL.Path, ".vtt"):
			i, _ := strconv.Atoi(segRe.FindStringSubmatch(r.URL.Path)[3])
			// One cue half a second into the segment, on FFmpeg's clock
			// (Bowtie maps local 0 to PTS 1.4 s). Segment 3's cue is 10 s off.
			cue := ptsOf("v480_vtt", i) + 0.5 - 1.4
			if i == 3 {
				cue += 10
			}
			_, _ = fmt.Fprintf(w, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n\n%s --> %s\nhello\n\n", vttTime(cue), vttTime(cue+1))
		case segRe.MatchString(r.URL.Path):
			_, _ = w.Write(append([]byte{0x47}, r.URL.Path...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	probe := func(_ context.Context, seg []byte) ([]Packet, error) {
		m := segRe.FindStringSubmatch(string(seg[1:]))
		seq, _ := strconv.Atoi(m[2])
		return []Packet{{PTS: ptsOf(m[1], seq), Dur: 4}}, nil
	}
	var events []SegmentEvent
	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: 20 * time.Millisecond,
		AllRenditions: true, Probe: probe,
		OnSegment: func(e SegmentEvent) { events = append(events, e) }, // Run goroutine only
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	p.Run(ctx)
	cancel()
	p.Mark()
	mu.Lock()
	polls, gone = 6, true // two more segments in video and captions; audio disappears
	mu.Unlock()
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	p.Run(ctx)
	cancel()
	r := p.Report()

	if got := strings.Join(r.Renditions, ","); got != "v480,aac0,v480_vtt" {
		t.Fatalf("renditions %q", got)
	}
	if r.MaxAVStartSkew > time.Millisecond || r.AVPairs == 0 {
		t.Fatalf("MaxAVStartSkew=%v over %d pairs, want 0 over some (paired by PDT)", r.MaxAVStartSkew, r.AVPairs)
	}
	if len(r.Windows) != 2 {
		t.Fatalf("%d windows, want 2", len(r.Windows))
	}
	w0, w1 := r.Windows[0], r.Windows[1]
	if w0.Paired["aac0"][1] == 0 || w0.Paired["v480"][1] == 0 {
		t.Fatalf("window 0 paired %v, want both renditions paired in discontinuity 1", w0.Paired)
	}
	if w1.Segments["v480"] != 2 || w1.Segments["v480_vtt"] != 2 || w1.Segments["aac0"] != 0 || len(w1.Paired["v480"]) != 0 {
		t.Fatalf("window 1 segments %v paired %v: video alone must not count as paired", w1.Segments, w1.Paired)
	}
	if r.SegmentsSinceMark["v480"] != 2 {
		t.Fatalf("since mark %v", r.SegmentsSinceMark)
	}
	if r.PlaylistErrors == 0 || r.FetchErrors < r.PlaylistErrors {
		t.Fatalf("a rendition that went missing was not counted: playlist %d fetch %d", r.PlaylistErrors, r.FetchErrors)
	}
	if r.TailGap["aac0"]-r.TailGap["v480"] < 150*time.Millisecond {
		t.Fatalf("tail gaps %v: audio stopped, video did not", r.TailGap)
	}
	if r.Cues != 6 || r.CuesTimed != 6 || r.CueErrors != 1 {
		t.Fatalf("cues %d timed %d errors %d, want 6, 6 and 1 (segment 3)", r.Cues, r.CuesTimed, r.CueErrors)
	}
	if w0.Cues["v480_vtt"] != 4 || w1.Cues["v480_vtt"] != 2 {
		t.Fatalf("cues per window %v / %v", w0.Cues, w1.Cues)
	}
	if len(events) == 0 || events[0].Rendition == "" {
		t.Fatalf("events %+v", events)
	}
}

// Cues that arrive before their video segment wait for it; cues that never
// find one, and comparisons still waiting, are settled when observation ends.
func TestPlayerDrainsWaitingComparisons(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	now := time.Now()
	v, a, cc := newRendition("v480", "", now), newRendition("aac0", "", now), newRendition("v480_vtt", "", now)
	p := &Player{primary: v, others: []*rendition{a, cc}, windows: []*CoverageWindow{newWindow(now)}}
	seg := func(i int) SegmentObs {
		return SegmentObs{Seq: int64(i), Duration: 4, PDT: t0.Add(time.Duration(4*i) * time.Second)}
	}
	vtt := func(cue float64) []byte {
		return []byte(fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n\n%s --> %s\nhi\n", vttTime(cue), vttTime(cue+1)))
	}
	// Caption segment 0 arrives before video segment 0 is probed: it waits.
	p.cues(cc, seg(0), 0, vtt(100.5-1.4))
	if r := p.Report(); r.Cues != 1 || r.CuesTimed != 0 || r.CueErrors != 0 {
		t.Fatalf("before the video: cues %d timed %d errors %d", r.Cues, r.CuesTimed, r.CueErrors)
	}
	p.mu.Lock()
	v.addProbed(&probed{seg: PDTSeg{Start: seg(0).PDT, Dur: 4}, pts: 100, end: 104})
	p.retryCuesLocked()
	p.mu.Unlock()
	if r := p.Report(); r.CuesTimed != 1 || r.Windows[0].CuesTimed["v480_vtt"] != 1 || r.CueErrors != 0 {
		t.Fatalf("after the video: timed %d (window %v) errors %d", r.CuesTimed, r.Windows[0].CuesTimed, r.CueErrors)
	}
	// Caption segment 5 never gets a video segment: unmatched at the end.
	p.cues(cc, seg(5), 0, vtt(120.5-1.4))
	// Audio segment 0 was probed but its pairing never ran: drain pairs it.
	p.mu.Lock()
	a.addProbed(&probed{seg: PDTSeg{Start: seg(0).PDT, Dur: 4}, pts: 100.03, end: 104})
	p.mu.Unlock()
	p.drain()
	r := p.Report()
	if r.CueErrors != 1 || !strings.Contains(strings.Join(r.Errors, "\n"), "no probed v480 segment") {
		t.Fatalf("unmatched cue: errors %d %q", r.CueErrors, r.Errors)
	}
	if r.AVPairs != 1 || r.Windows[0].Paired["aac0"][0] != 1 || r.MaxAVStartSkew != 30*time.Millisecond {
		t.Fatalf("drain pairing: pairs %d paired %v skew %v", r.AVPairs, r.Windows[0].Paired, r.MaxAVStartSkew)
	}
}

// probedCaptionPlayer has one probed video segment (PTS pts–end) and the
// subtitle rendition timed against it. The caption segment shares that PDT.
func probedCaptionPlayer(pts, end float64) (*Player, *rendition, SegmentObs) {
	now := time.Now()
	t0 := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	v, cc := newRendition("v480", "", now), newRendition("v480_vtt", "", now)
	p := &Player{primary: v, others: []*rendition{cc}, windows: []*CoverageWindow{newWindow(now)}}
	seg := SegmentObs{Seq: 5, Duration: 4, PDT: t0}
	p.mu.Lock()
	v.addProbed(&probed{seg: PDTSeg{Start: t0, Dur: 4}, pts: pts, end: end})
	p.mu.Unlock()
	return p, cc, seg
}

// FFmpeg 5.1 writes WebVTT with no X-TIMESTAMP-MAP; cue times already sit on
// the video PTS timeline. RFC 8216 §3.5 maps cue time 0 to MPEG-TS 0, the
// same as X-TIMESTAMP-MAP=MPEGTS:0,LOCAL:00:00:00.000.
func TestPlayerTimesUnmappedCueInsideVideoPTS(t *testing.T) {
	p, cc, seg := probedCaptionPlayer(21.49, 25.49)
	body := fmt.Sprintf("WEBVTT\n\n%s --> %s\nhello\n\n", vttTime(21.39), vttTime(22.39))
	p.cues(cc, seg, 0, []byte(body))
	r := p.Report()
	if r.Cues != 1 || r.CuesTimed != 1 || r.Windows[0].CuesTimed["v480_vtt"] != 1 || r.CueErrors != 0 {
		t.Fatalf("unmapped cue inside range: cues %d timed %d window %v errors %d %q",
			r.Cues, r.CuesTimed, r.Windows[0].CuesTimed, r.CueErrors, r.Errors)
	}
}

// A cue with no map is still a CueError when it lands beyond cueTolerance
// of the PDT-paired video segment.
func TestPlayerUnmappedCueOutsideRangeIsCueError(t *testing.T) {
	p, cc, seg := probedCaptionPlayer(21.49, 25.49)
	body := fmt.Sprintf("WEBVTT\n\n%s --> %s\nhello\n\n", vttTime(30), vttTime(31))
	p.cues(cc, seg, 0, []byte(body))
	r := p.Report()
	if r.Cues != 1 || r.CuesTimed != 1 || r.CueErrors != 1 || !strings.Contains(strings.Join(r.Errors, "\n"), "outside") {
		t.Fatalf("unmapped cue outside range: cues %d timed %d errors %d %q", r.Cues, r.CuesTimed, r.CueErrors, r.Errors)
	}
}

// An X-TIMESTAMP-MAP line with MPEGTS or LOCAL missing or unparseable stays
// a CueError. This cue would sit inside the video PTS range if timed from 0.
func TestPlayerMalformedTimestampMapIsCueError(t *testing.T) {
	p, cc, seg := probedCaptionPlayer(21.49, 25.49)
	body := fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:not-a-time\n\n%s --> %s\nhello\n\n", vttTime(21.39), vttTime(22.39))
	p.cues(cc, seg, 0, []byte(body))
	r := p.Report()
	if r.Cues != 1 || r.CuesTimed != 0 || r.CueErrors != 1 || !strings.Contains(strings.Join(r.Errors, "\n"), "X-TIMESTAMP-MAP") {
		t.Fatalf("malformed map: cues %d timed %d errors %d %q", r.Cues, r.CuesTimed, r.CueErrors, r.Errors)
	}
}

// A colon separator is still a map header. This cue is inside the video PTS
// range from MPEG-TS 0 and from MPEGTS:126000, so only rejecting the header
// produces a CueError.
func TestPlayerTimestampMapColonIsCueError(t *testing.T) {
	p, cc, seg := probedCaptionPlayer(21.49, 25.49)
	body := fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP:MPEGTS:126000,LOCAL:00:00:00.000\n\n%s --> %s\nhello\n\n", vttTime(21.39), vttTime(22.39))
	p.cues(cc, seg, 0, []byte(body))
	r := p.Report()
	if r.Cues != 1 || r.CuesTimed != 0 || r.CueErrors != 1 || !strings.Contains(strings.Join(r.Errors, "\n"), "X-TIMESTAMP-MAP") {
		t.Fatalf("colon map: cues %d timed %d errors %d %q", r.Cues, r.CuesTimed, r.CueErrors, r.Errors)
	}
}

// A malformed map header stays a CueError when a later line is valid. The
// cue is inside the video PTS range under that valid map.
func TestPlayerMalformedMapBeforeValidMapIsCueError(t *testing.T) {
	p, cc, seg := probedCaptionPlayer(21.49, 25.49)
	body := fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP:MPEGTS:0,LOCAL:00:00:00.000\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n\n%s --> %s\nhello\n\n", vttTime(21.39), vttTime(22.39))
	p.cues(cc, seg, 0, []byte(body))
	r := p.Report()
	if r.Cues != 1 || r.CuesTimed != 0 || r.CueErrors != 1 || !strings.Contains(strings.Join(r.Errors, "\n"), "X-TIMESTAMP-MAP") {
		t.Fatalf("malformed then valid map: cues %d timed %d errors %d %q", r.Cues, r.CuesTimed, r.CueErrors, r.Errors)
	}
}

// An hour-long soak polls each rendition thousands of times. What the player
// keeps must grow with the segments it saw at most, never with the polls,
// and the incremental consistency result must equal the batch one.
func TestPlayerRetentionStaysBounded(t *testing.T) {
	const polls, window, perSeg = 3600, 6, 4 // one poll a second, 4 s segments
	names := []string{"aac0", "v480", "v720"}
	var c consistencyChecker
	var all []Snapshot
	for i := 0; i < polls; i++ {
		newest := int64(i / perSeg)
		for _, name := range names {
			var es []ent
			mseq := newest - window + 1
			if mseq < 0 {
				mseq = 0
			}
			// aac0 tags a second boundary one segment before the shared one:
			// Problems are non-empty (and capped).
			tagged := func(q int64) bool { return q == 500 || (name == "aac0" && q == 499) }
			dseq := int64(0)
			for q := int64(0); q < mseq; q++ {
				if tagged(q) {
					dseq++
				}
			}
			for q := mseq; q <= newest; q++ {
				es = append(es, ent{pdt: at(4 * float64(q)), disc: tagged(q)})
			}
			s := snap(name, mk(name, mseq, dseq, es...), i)
			c.add(s)
			all = append(all, s)
		}
	}
	unique := polls / perSeg
	if got, limit := c.retained(), len(names)*(unique+window); got > limit {
		t.Fatalf("checker retains %d segment records after %d polls, want ≤ %d (segments, not polls)", got, polls*len(names), limit)
	}
	got := c.result()
	if want := CheckConsistency(all); !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental result\n%+v\nbatch\n%+v", got, want)
	}
	if got.DiscontinuitiesAligned || len(got.Problems) == 0 {
		t.Fatalf("early audio tag not flagged: %+v", got)
	}
	if again := c.result(); !reflect.DeepEqual(again, got) {
		t.Fatalf("result changed on a second call:\n%+v\n%+v", again, got)
	}

	r := newRendition("v480", "", time.Now())
	for i := 0; i < 900; i++ {
		r.addProbed(&probed{seg: PDTSeg{Start: at(4 * float64(i)), Dur: 4}})
	}
	if len(r.probed) != maxProbed || !r.probed[maxProbed-1].seg.Start.Equal(at(4*899)) {
		t.Fatalf("probed keeps %d segments, want the last %d", len(r.probed), maxProbed)
	}
}
