package hdhrfake_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/faults"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

func oneChannel(t *testing.T) *hdhrfake.Fake {
	t.Helper()
	return hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "FAULTS01",
		TunerCount: 2,
		Lineup:     []hdhrfake.LineupEntry{{GuideNumber: "90.1", GuideName: "FAKE"}},
	})
}

// stream is a client-side view of /auto/v90.1: bytes with arrival times.
type stream struct {
	mu     sync.Mutex
	chunks []chunk
	err    error
	done   chan struct{}
}

type chunk struct {
	at   time.Time
	data []byte
}

func openStream(t *testing.T, f *hdhrfake.Fake) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.URL+"/auto/v90.1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	s := &stream{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		defer func() { _ = resp.Body.Close() }()
		buf := make([]byte, 64*1024)
		for {
			n, err := resp.Body.Read(buf)
			s.mu.Lock()
			if n > 0 {
				s.chunks = append(s.chunks, chunk{time.Now(), append([]byte(nil), buf[:n]...)})
			}
			if err != nil {
				s.err = err
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *stream) bytesBetween(a, b time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.chunks {
		if !c.at.Before(a) && c.at.Before(b) {
			n += len(c.data)
		}
	}
	return n
}

// pcrs returns PCR bases (90 kHz) in arrival order.
func (s *stream) pcrs() []int64 {
	s.mu.Lock()
	var all []byte
	for _, c := range s.chunks {
		all = append(all, c.data...)
	}
	s.mu.Unlock()
	var out []int64
	for off := 0; off+tsloop.PacketSize <= len(all); off += tsloop.PacketSize {
		if base, _, ok := tsloop.PCR(all[off : off+tsloop.PacketSize]); ok {
			out = append(out, base)
		}
	}
	return out
}

func (s *stream) wait(t *testing.T, d time.Duration) error {
	t.Helper()
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	case <-time.After(d):
		t.Fatalf("stream still open after %v", d)
		return nil
	}
}

func apply(t *testing.T, f *hdhrfake.Fake, tg faults.Target, s faults.Spec) string {
	t.Helper()
	id, err := f.Apply(tg, s)
	if err != nil {
		t.Fatalf("Apply(%+v): %v", s, err)
	}
	return id
}

func waitConns(t *testing.T, f *hdhrfake.Fake, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(f.Connections()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("connections = %d, want %d", len(f.Connections()), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartCloseWithoutTesting(t *testing.T) {
	f, err := hdhrfake.Start(hdhrfake.Options{Lineup: []hdhrfake.LineupEntry{{GuideNumber: "90.1", GuideName: "FAKE"}}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(f.URL + "/discover.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	f.Close()
	if _, err := http.Get(f.URL + "/discover.json"); err == nil {
		t.Fatal("server still answering after Close")
	}
}

func TestFakeJoinMidStream(t *testing.T) {
	f := oneChannel(t)
	time.Sleep(1200 * time.Millisecond)
	s := openStream(t, f)
	time.Sleep(time.Second)
	p := s.pcrs()
	if len(p) < 5 {
		t.Fatalf("only %d PCRs in 1s", len(p))
	}
	for i := 1; i < len(p); i++ {
		if p[i] <= p[i-1] {
			t.Fatalf("PCR %d went back: %d after %d", i, p[i], p[i-1])
		}
	}
	// The embedded clip's first PCR is ~0.7s in; joining 1.2s after Start must
	// land mid-timeline, not at the clip's start.
	if p[0] < 90000*15/10 {
		t.Fatalf("first PCR %d looks like the clip start, not a mid-stream join", p[0])
	}
}

func TestFakeStatusReflectsSignal(t *testing.T) {
	f := oneChannel(t)
	openStream(t, f)
	waitConns(t, f, 1)
	q := 55
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.Signal, Quality: &q})
	resp, err := http.Get(f.URL + "/status.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var status []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	tuned := status[0]
	if tuned["VctNumber"] != "90.1" || tuned["VctName"] != "FAKE" || tuned["SignalQualityPercent"] != float64(55) ||
		tuned["SignalStrengthPercent"] != float64(100) || tuned["TargetIP"] != "127.0.0.1" {
		t.Fatalf("tuner0 status = %v", tuned)
	}
}

func TestFakeBusyAndHang(t *testing.T) {
	f := oneChannel(t)
	id := apply(t, f, faults.Target{Scope: faults.ScopeDevice}, faults.Spec{Fault: faults.Busy})
	resp, err := http.Get(f.URL + "/auto/v90.1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("busy status = %d", resp.StatusCode)
	}
	f.Remove(id)
	apply(t, f, faults.Target{Scope: faults.ScopeDevice}, faults.Spec{Fault: faults.Hang})
	c := &http.Client{Timeout: 300 * time.Millisecond}
	if _, err := c.Get(f.URL + "/auto/v90.1"); err == nil {
		t.Fatal("hang returned a response")
	}
	if f.ActiveStreams() != 0 {
		t.Fatalf("hang consumed a tuner: active=%d", f.ActiveStreams())
	}
}

func TestFakeDropClean(t *testing.T) {
	f := oneChannel(t)
	s := openStream(t, f)
	waitConns(t, f, 1)
	apply(t, f, faults.Target{Channel: "90.1", Scope: faults.ScopeConnections}, faults.Spec{Fault: faults.Drop})
	if err := s.wait(t, 2*time.Second); err != io.EOF {
		t.Fatalf("clean drop ended with %v, want io.EOF", err)
	}
}

func TestFakeDropReset(t *testing.T) {
	f := oneChannel(t)
	s := openStream(t, f)
	waitConns(t, f, 1)
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.Drop, Mode: "reset"})
	if err := s.wait(t, 2*time.Second); err == nil || err == io.EOF {
		t.Fatalf("reset drop ended with %v, want a connection error", err)
	}
}

func TestFakeStallBurst(t *testing.T) {
	f := oneChannel(t)
	s := openStream(t, f)
	time.Sleep(500 * time.Millisecond)
	t0 := time.Now()
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.Stall, For: time.Second, Burst: true})
	time.Sleep(1500 * time.Millisecond)
	if n := s.bytesBetween(t0.Add(300*time.Millisecond), t0.Add(900*time.Millisecond)); n != 0 {
		t.Fatalf("%d bytes arrived during the stall", n)
	}
	// ~1s of backlog arrives at once right after the stall (fixture ≈ 349 KB/s).
	if n := s.bytesBetween(t0.Add(950*time.Millisecond), t0.Add(1200*time.Millisecond)); n < 200_000 {
		t.Fatalf("burst after stall = %d bytes, want the ~1s backlog", n)
	}
}

func TestFakeTimestampJumpAndSwitch(t *testing.T) {
	alt, err := tsloop.LoadFile("testdata/fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	f := hdhrfake.New(t, hdhrfake.Options{
		Lineup:  []hdhrfake.LineupEntry{{GuideNumber: "90.1", GuideName: "FAKE"}},
		Sources: map[string]*tsloop.Source{"alt": alt},
	})
	s := openStream(t, f)
	time.Sleep(500 * time.Millisecond)
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.TimestampJump, By: 10 * time.Second})
	time.Sleep(500 * time.Millisecond)
	p := s.pcrs()
	jumped := false
	for i := 1; i < len(p); i++ {
		if p[i]-p[i-1] >= 10*90000 {
			jumped = true
		}
	}
	if !jumped {
		t.Fatalf("no ≥10s PCR jump in %v", p)
	}
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.SourceSwitch, To: "alt"})
	if _, err := f.Apply(faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.SourceSwitch, To: "nope"}); err == nil {
		t.Fatal("switch to unknown source succeeded")
	}
	if _, err := f.Apply(faults.Target{Channel: "99.9"}, faults.Spec{Fault: faults.Stall}); err == nil {
		t.Fatal("fault on unknown channel succeeded")
	}
}

func TestFakeEventsRecorded(t *testing.T) {
	f := oneChannel(t)
	s := openStream(t, f)
	waitConns(t, f, 1)
	apply(t, f, faults.Target{Channel: "90.1"}, faults.Spec{Fault: faults.Drop})
	_ = s.wait(t, 2*time.Second)
	kinds := map[string]bool{}
	for _, e := range f.Events() {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"dial", "fault", "close"} {
		if !kinds[k] {
			t.Fatalf("no %q event in %+v", k, f.Events())
		}
	}
}
