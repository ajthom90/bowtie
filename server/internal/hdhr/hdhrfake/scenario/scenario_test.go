package scenario

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/faults"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

const full = `
name: mid-stream-ffmpeg-restart
source: synthetic-1080i
channel: "90.1"
duration: 45s
timeline:
  - {at: 15s, fault: drop, mode: reset}
  - {at: 25s, fault: signal, quality: 40, for: 8s}
  - {at: 30s, fault: stall, scope: connections}
bowtie:
  slowConsumer: {at: 10s, for: 5s}
expect:
  sequenceMonotonic: true
  maxGapSeconds: 12
  sessionSurvives: true
  maxFfmpegRestarts: 1
  tunersBusy: false
`

func TestParseFull(t *testing.T) {
	s, err := Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "mid-stream-ffmpeg-restart" || s.Source != "synthetic-1080i" || s.Channel != "90.1" || s.Duration != 45*time.Second {
		t.Fatalf("header = %+v", s)
	}
	if len(s.Timeline) != 3 {
		t.Fatalf("timeline = %+v", s.Timeline)
	}
	drop, sig, stall := s.Timeline[0], s.Timeline[1], s.Timeline[2]
	if drop.At != 15*time.Second || drop.Fault != faults.Drop || drop.Mode != "reset" {
		t.Fatalf("drop = %+v", drop)
	}
	if sig.Fault != faults.Signal || sig.Quality == nil || *sig.Quality != 40 || sig.For != 8*time.Second {
		t.Fatalf("signal = %+v", sig)
	}
	if stall.Scope != faults.ScopeConnections {
		t.Fatalf("stall scope = %q", stall.Scope)
	}
	if s.Bowtie == nil || s.Bowtie.SlowConsumer == nil || s.Bowtie.SlowConsumer.At != 10*time.Second || s.Bowtie.SlowConsumer.For != 5*time.Second {
		t.Fatalf("bowtie = %+v", s.Bowtie)
	}
	e := s.Expect
	if e == nil || !*e.SequenceMonotonic || *e.MaxGapSeconds != 12 || !*e.SessionSurvives || *e.MaxFfmpegRestarts != 1 || *e.TunersBusy {
		t.Fatalf("expect = %+v", e)
	}
}

func TestParseDefaults(t *testing.T) {
	s, err := Parse([]byte("name: x\nduration: 5s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Channel != "90.1" || s.Source != "synthetic-480i" {
		t.Fatalf("defaults = %+v", s)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"unknown field":   "name: x\nduration: 5s\nbogus: 1\n",
		"no name":         "duration: 5s\n",
		"no duration":     "name: x\n",
		"step after end":  "name: x\nduration: 5s\ntimeline:\n  - {at: 6s, fault: stall}\n",
		"bad fault":       "name: x\nduration: 5s\ntimeline:\n  - {at: 1s, fault: zap}\n",
		"overlap":         "name: x\nduration: 30s\ntimeline:\n  - {at: 1s, fault: stall, for: 10s}\n  - {at: 5s, fault: stall, for: 2s}\n",
		"open overlap":    "name: x\nduration: 30s\ntimeline:\n  - {at: 1s, fault: stall}\n  - {at: 20s, fault: stall, for: 2s}\n",
		"slow past end":   "name: x\nduration: 10s\nbowtie:\n  slowConsumer: {at: 8s, for: 5s}\n",
		"xfail alone":     "name: x\nduration: 5s\nexpect:\n  xfail: why\n",
		"flaky alone":     "name: x\nduration: 5s\nexpect:\n  flaky: why\n",
		"flaky and xfail": "name: x\nduration: 5s\nexpect:\n  maxGapSeconds: 5\n  xfail: a\n  flaky: b\n",
		"bad scope":       "name: x\nduration: 5s\ntimeline:\n  - {at: 1s, fault: stall, scope: planet}\n",
		"step field typo": "name: x\nduration: 5s\ntimeline:\n  - {at: 1s, fault: stall, fro: 2s}\n",
		"kill past end":   "name: x\nduration: 10s\nbowtie:\n  killTranscoderAt: 10s\n",
	}
	for name, y := range cases {
		if _, err := Parse([]byte(y)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseFlaky(t *testing.T) {
	s, err := Parse([]byte("name: x\nduration: 5s\nexpect:\n  maxGapSeconds: 5\n  flaky: ramp-up\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Expect.Flaky != "ramp-up" {
		t.Fatalf("Flaky = %q", s.Expect.Flaky)
	}
}

func TestParseAllowsSequentialSameKind(t *testing.T) {
	y := "name: x\nduration: 30s\ntimeline:\n  - {at: 5s, fault: signal, quality: 65, for: 10s}\n  - {at: 15s, fault: signal, quality: 45, for: 6s}\n"
	if _, err := Parse([]byte(y)); err != nil {
		t.Fatal(err)
	}
}

type recorder struct {
	mu    sync.Mutex
	start time.Time
	now   func() time.Time
	got   []string
}

func (r *recorder) Apply(t faults.Target, s faults.Spec) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, r.now().Sub(r.start).String()+" "+string(s.Fault)+" "+t.Channel+" "+string(t.Scope)+" for="+s.For.String())
	return "id", nil
}

func TestRunFiresInOrder(t *testing.T) {
	s, err := Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	clock := tsloop.Clock{
		Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		After: func(d time.Duration) <-chan time.Time {
			mu.Lock()
			now = now.Add(d)
			c := make(chan time.Time, 1)
			c <- now
			mu.Unlock()
			return c
		},
	}
	rec := &recorder{start: now, now: clock.Now}
	if err := s.Run(context.Background(), rec, clock); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"15s drop 90.1  for=0s",
		"25s signal 90.1  for=8s",
		"30s stall 90.1 connections for=0s",
	}
	if strings.Join(rec.got, "|") != strings.Join(want, "|") {
		t.Fatalf("applied:\n%s\nwant:\n%s", strings.Join(rec.got, "\n"), strings.Join(want, "\n"))
	}
	if el := clock.Now().Sub(rec.start); el != 45*time.Second {
		t.Fatalf("Run returned after %v, want the full 45s", el)
	}
}

func TestRunStopsOnContext(t *testing.T) {
	s, _ := Parse([]byte(full))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx, &recorder{now: time.Now}, tsloop.RealClock()); err == nil {
		t.Fatal("Run ignored a cancelled context")
	}
}

func TestCheck(t *testing.T) {
	s, _ := Parse([]byte(full))
	good := Observed{SequenceMonotonic: true, MaxGap: 5 * time.Second, SessionSurvived: true, FfmpegRestarts: 1}
	if v := s.Expect.Check(good); len(v) != 0 {
		t.Fatalf("good observation violated: %v", v)
	}
	bad := Observed{SequenceMonotonic: false, MaxGap: 13 * time.Second, SessionSurvived: false, FfmpegRestarts: 2, TunersBusy: true}
	v := s.Expect.Check(bad)
	if len(v) != 5 {
		t.Fatalf("violations = %v, want 5", v)
	}
	for _, want := range []string{"sequence", "gap", "session", "restarts", "tuners"} {
		if !strings.Contains(strings.Join(v, " "), want) {
			t.Fatalf("no %q violation in %v", want, v)
		}
	}
}

func TestParseKillTranscoder(t *testing.T) {
	s, err := Parse([]byte("name: x\nduration: 30s\nbowtie:\n  killTranscoderAt: 15s\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Bowtie == nil || s.Bowtie.KillTranscoderAt != 15*time.Second {
		t.Fatalf("KillTranscoderAt = %+v", s.Bowtie)
	}
}

func TestPTSJumpStepsNeverOverlap(t *testing.T) {
	_, err := Parse([]byte(`
name: pj
duration: 30s
timeline:
  - {at: 8s, fault: pts-jump, by: 3s}
  - {at: 9s, fault: pts-jump, by: 6s}
`))
	if err != nil {
		t.Fatalf("one-shot pts-jump steps rejected: %v", err)
	}
}

func TestCheckTimelineExpectations(t *testing.T) {
	yes, f := true, func(v float64) *float64 { return &v }
	one, ds := 1, int64(1)
	e := &Expect{
		RenditionsMonotonic: &yes, DiscontinuitiesAligned: &yes, DiscontinuitySequenceOK: &yes,
		PDTMonotonic: &yes, PDTAligned: &yes, MinDiscontinuitySequence: &ds,
		MaxTimelineExcessSeconds: f(2), MinTimelineExcessSeconds: f(-2),
		MaxAudioHoleMs: f(50), MaxAVStartSkewMs: f(100), MinFfmpegRestarts: &one,
	}
	good := Observed{
		RenditionsMonotonic: true, DiscontinuitiesAligned: true, DiscontinuitySequenceOK: true,
		PDTMonotonic: true, PDTAligned: true, MaxDiscontinuitySequence: 1, TimelineExcess: 0.5,
		MaxAudioHole: 20 * time.Millisecond, MaxAVStartSkew: 40 * time.Millisecond,
		ProbedAudio: 3, ProbedVideo: 3, AVPairs: 3, FfmpegRestarts: 1,
	}
	if v := e.Check(good); len(v) != 0 {
		t.Fatalf("good run violations: %v", v)
	}
	bad := Observed{TimelineExcess: 9, MaxAudioHole: 200 * time.Millisecond, ProbedAudio: 1, ProbedVideo: 1,
		MaxAVStartSkew: 300 * time.Millisecond}
	// Every check but the minimum excess fails: 10 violations.
	if v := e.Check(bad); len(v) != 10 {
		t.Fatalf("bad run: %d violations %v, want 10", len(v), v)
	}
	if v := e.Check(Observed{TimelineExcess: -9}); !strings.Contains(strings.Join(v, ";"), "timeline excess") {
		t.Fatalf("low excess not reported: %v", v)
	}
	if v := (&Expect{MaxAudioHoleMs: f(50)}).Check(Observed{}); len(v) != 1 || !strings.Contains(v[0], "no audio") {
		t.Fatalf("unprobed audio must be a violation: %v", v)
	}
}

func TestParseWindowSegments(t *testing.T) {
	s, err := Parse([]byte("name: w\nduration: 10s\nwindowSegments: 8\n"))
	if err != nil || s.WindowSegments != 8 {
		t.Fatalf("windowSegments: %v %+v", err, s)
	}
	if _, err := Parse([]byte("name: w\nduration: 10s\nwindowSegments: 2\n")); err == nil {
		t.Fatal("windowSegments 2 accepted")
	}
}

func TestXFailNeedsXFailOnAndNewExpectations(t *testing.T) {
	if _, err := Parse([]byte("name: x\nduration: 5s\nexpect:\n  maxGapSeconds: 5\n  xfail: why\n")); err == nil {
		t.Fatal("xfail without xfailOn accepted")
	}
	s, err := Parse([]byte("name: x\nduration: 40s\ntimeline:\n  - {at: 10s, fault: stall, for: 4s}\n" +
		"bowtie: {layout: production, killTranscoderAt: 20s}\n" +
		"expect:\n  xfail: why\n  xfailOn: timeline excess\n  maxTimelineExcessSeconds: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Bowtie.Layout != "production" || s.LastFaultEnd() != 20*time.Second {
		t.Fatalf("layout %q, last fault end %v", s.Bowtie.Layout, s.LastFaultEnd())
	}
	// The stall ends at 14 s and the kill comes 6 s later: one cluster.
	if got := s.FaultEnds(); len(got) != 1 || got[0] != 20*time.Second {
		t.Fatalf("fault ends %v, want [20s]", got)
	}
	if _, err := Parse([]byte("name: x\nduration: 5s\nbowtie: {layout: fancy}\n")); err == nil {
		t.Fatal("unknown layout accepted")
	}
	three, two, yes := 3, 2, true
	e := &Expect{MinSegmentsAfterFault: &three, NoFetchErrors: &yes, MinRenditions: &two, MaxStartSeconds: func(v float64) *float64 { return &v }(8)}
	if v := e.Check(Observed{MinSegmentsAfterFault: 3, RenditionsCovered: true, Renditions: 2, StartLatency: time.Second}); len(v) != 0 {
		t.Fatalf("good: %v", v)
	}
	if v := e.Check(Observed{MinSegmentsAfterFault: 1, FetchErrors: 1, Renditions: 1, StartLatency: 9 * time.Second}); len(v) != 4 {
		t.Fatalf("bad: %d violations %v, want 4", len(v), v)
	}
}

func TestCheckStartAVOffset(t *testing.T) {
	s, err := Parse([]byte("name: x\nduration: 5s\nexpect:\n  maxStartAVOffsetMs: 100\n"))
	if err != nil || s.Expect == nil || s.Expect.MaxStartAVOffsetMs == nil || *s.Expect.MaxStartAVOffsetMs != 100 {
		t.Fatalf("parse maxStartAVOffsetMs: %v %+v", err, s.Expect)
	}
	bad := s.Expect.Check(Observed{MaxStartAVOffset: 400 * time.Millisecond, ProbedAudio: 1, ProbedVideo: 1})
	if len(bad) != 1 || !strings.Contains(bad[0], "400") || !strings.Contains(bad[0], "100") {
		t.Fatalf("violations %v, want one 400ms-over-100ms start-offset violation", bad)
	}
	if v := s.Expect.Check(Observed{MaxStartAVOffset: 20 * time.Millisecond, ProbedAudio: 1, ProbedVideo: 1}); len(v) != 0 {
		t.Fatalf("aligned: %v", v)
	}
	if v := s.Expect.Check(Observed{}); len(v) != 1 || !strings.Contains(v[0], "no audio") {
		t.Fatalf("unmeasured: %v", v)
	}
}

func TestCheckCoverageExpectations(t *testing.T) {
	yes, f := true, func(v float64) *float64 { return &v }
	e := &Expect{PairedAfterEachFault: &yes, MaxTailGapSeconds: f(12), CaptionsTimed: &yes, MaxAVStartSkewMs: f(100)}
	good := Observed{AVPairs: 4, ProbedAudio: 4, ProbedVideo: 4, MaxTailGap: 5 * time.Second, MaxTailGapRendition: "aac0", Cues: 10, CuesTimed: 10}
	if v := e.Check(good); len(v) != 0 {
		t.Fatalf("good: %v", v)
	}
	bad := Observed{
		ProbedAudio: 4, ProbedVideo: 4, // probed, but never paired
		UnpairedCoverage: []string{"window 2: aac0 has no A/V-paired segment (0 fetched)"},
		MaxTailGap:       30 * time.Second, MaxTailGapRendition: "aac0",
		Cues: 3, CuesTimed: 2, CueErrors: 1, UntimedCueWindows: []string{"window 1: v480_vtt has no timed cues (1 cues)"},
	}
	v := e.Check(bad)
	joined := strings.Join(v, "\n")
	for _, want := range []string{"no audio/video segment pairs", "window 2: aac0", "aac0 stopped 30.0s before the end", "cues"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, v)
		}
	}
	if len(v) != 4 {
		t.Fatalf("%d violations %v, want 4", len(v), v)
	}
}

func TestFaultEndsClusters(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"spaced point faults", "timeline:\n  - {at: 8s, fault: pts-jump, by: 3s}\n  - {at: 18s, fault: pts-jump, by: 3s}\n  - {at: 28s, fault: pts-jump, by: 3s}\n", "[8s 18s 28s]"},
		{"8 s apart is not a cluster", "timeline:\n  - {at: 30s, fault: timestamp-jump, by: 2s}\n  - {at: 38s, fault: timestamp-jump, by: 2s}\n", "[30s 38s]"},
		{"closer than 8 s is one cluster", "timeline:\n  - {at: 30s, fault: timestamp-jump, by: 2s}\n  - {at: 35s, fault: timestamp-jump, by: 2s}\n", "[35s]"},
		{"kill inside an outage", "timeline:\n  - {at: 10s, fault: signal, quality: 10, for: 65s}\nbowtie: {killTranscoderAt: 30s}\n", "[1m15s]"},
		{"jump inside corruption", "timeline:\n  - {at: 10s, fault: corrupt, type: tei, rate: 0.01, for: 50s}\n  - {at: 12s, fault: pts-jump, by: 3s}\n", "[12s 1m0s]"},
		{"jump inside the slow consumer", "timeline:\n  - {at: 20s, fault: pts-jump, by: 3s}\nbowtie: {slowConsumer: {at: 10s, for: 40s}}\n", "[50s]"},
		{"no faults", "", "[]"},
	} {
		s, err := Parse([]byte("name: f\nduration: 100s\n" + tc.yaml))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := fmt.Sprint(s.FaultEnds()); got != tc.want {
			t.Errorf("%s: fault ends %s, want %s", tc.name, got, tc.want)
		}
	}
}
