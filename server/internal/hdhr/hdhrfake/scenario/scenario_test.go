package scenario

import (
	"context"
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
