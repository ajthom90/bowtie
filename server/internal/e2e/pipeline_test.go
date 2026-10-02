package e2e

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/faults"
	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

const guide = "90.1"

type pipeline struct {
	h     *Harness
	stub  *StubRunner
	gate  *Gate
	count *CountingRunner
	p     *testplayer.Player
	// baseDials is the device dial count once session start has settled
	// (today that includes the redial after the request-scoped first dial).
	baseDials int64
}

// newPipeline wires Bowtie (stub FFmpeg) to a one-channel fake and starts a
// viewer. Set start=false to only build the stack.
func newPipeline(t *testing.T, start bool) *pipeline {
	t.Helper()
	stub := &StubRunner{}
	gate := NewGate()
	count := &CountingRunner{Inner: &GatedRunner{Inner: stub, Gate: gate}}
	h := New(t, Options{
		Fake:   hdhrfake.Options{TunerCount: 2, Lineup: []hdhrfake.LineupEntry{{GuideNumber: guide, GuideName: "FAKE"}}},
		Runner: count,
	})
	pl := &pipeline{h: h, stub: stub, gate: gate, count: count}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, e := range h.Fake.Events() {
			t.Logf("fake: %s %s %s %s %s", e.At.Format("15:04:05.000"), e.Kind, e.Channel, e.Conn, e.Detail)
		}
		for _, d := range h.DialLog() {
			t.Logf("ingest: %s", d)
		}
	})
	if !start {
		return pl
	}
	p, err := h.Player(t, guide)
	if err != nil {
		t.Fatalf("start viewer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go p.Run(ctx)
	pl.p = p
	if !within(5*time.Second, func() bool { return stub.BytesIn() > 0 }) {
		t.Fatal("no bytes reached the transcoder")
	}
	time.Sleep(1500 * time.Millisecond) // past the ingest's 1s reconnect backoff
	pl.baseDials = h.Fake.TotalDials()
	return pl
}

func (pl *pipeline) bytesGrow(d time.Duration) bool {
	before := pl.stub.BytesIn()
	return within(d, func() bool { return pl.stub.BytesIn() > before })
}

func TestPipelineDropReconnects(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, true)
	if _, err := pl.h.Fake.Apply(faults.Target{Channel: guide, Scope: faults.ScopeConnections}, faults.Spec{Fault: faults.Drop, Mode: "reset"}); err != nil {
		t.Fatal(err)
	}
	if !within(5*time.Second, func() bool { return pl.h.Fake.TotalDials() == pl.baseDials+1 }) {
		t.Fatalf("ingest did not redial after drop: dials=%d (base %d)", pl.h.Fake.TotalDials(), pl.baseDials)
	}
	if !pl.bytesGrow(5 * time.Second) {
		t.Fatal("bytes stopped after reconnect")
	}
	if n := pl.count.Starts(); n != 1 {
		t.Fatalf("transcoder starts = %d, want 1 (a device drop must not restart FFmpeg)", n)
	}
	if n := len(pl.h.Sessions(t)); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

func TestPipelineSignalFadeKeepsSession(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, true)
	q := 40
	if _, err := pl.h.Fake.Apply(faults.Target{Channel: guide}, faults.Spec{Fault: faults.Signal, Quality: &q, For: 4 * time.Second}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	if !pl.bytesGrow(3 * time.Second) {
		t.Fatal("bytes stopped after the fade")
	}
	if n := pl.count.Starts(); n != 1 {
		t.Fatalf("transcoder starts = %d, want 1", n)
	}
}

func TestPipelineSlowConsumerRestarts(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, true)
	// Documents current behavior: a transcoder that stops reading for longer
	// than the ingest queue + stall timeout is cut off and restarted.
	pl.gate.Pause()
	time.Sleep(8 * time.Second)
	pl.gate.Resume()
	if !within(10*time.Second, func() bool { return pl.count.Starts() == 2 }) {
		t.Fatalf("transcoder starts = %d, want 2 after a slow-consumer cutoff", pl.count.Starts())
	}
	if !pl.bytesGrow(5 * time.Second) {
		t.Fatal("restarted transcoder gets no bytes")
	}
}

func TestPipelineBusy(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, false)
	if _, err := pl.h.Fake.Apply(faults.Target{Scope: faults.ScopeDevice}, faults.Spec{Fault: faults.Busy}); err != nil {
		t.Fatal(err)
	}
	_, err := pl.h.Player(t, guide)
	var se *testplayer.StartError
	if !errors.As(err, &se) || se.Status != http.StatusServiceUnavailable {
		t.Fatalf("start with busy device: %v, want HTTP 503", err)
	}
}

func TestPipelineHalfOpenStallRedials(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, true)
	// The open connection goes silent; new connections would work.
	if _, err := pl.h.Fake.Apply(faults.Target{Channel: guide, Scope: faults.ScopeConnections}, faults.Spec{Fault: faults.Stall}); err != nil {
		t.Fatal(err)
	}
	xfail(t, "ingest has no read deadline (Plan 2)", func() error {
		if !within(12*time.Second, func() bool { return pl.h.Fake.TotalDials() > pl.baseDials }) {
			return errorf("no redial within 12s of a silent connection (dials=%d)", pl.h.Fake.TotalDials())
		}
		return nil
	})
}

func TestPipelineDialHangFailsFast(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, false)
	if _, err := pl.h.Fake.Apply(faults.Target{Scope: faults.ScopeDevice}, faults.Spec{Fault: faults.Hang}); err != nil {
		t.Fatal(err)
	}
	xfail(t, "device dial has no timeout (Plan 2)", func() error {
		start := time.Now()
		_, err := pl.h.PlayerWith(t, guide, &http.Client{Timeout: 20 * time.Second})
		if el := time.Since(start); el > 15*time.Second {
			return errorf("session start took %v against a hung device (err=%v)", el.Round(time.Second), err)
		}
		return nil
	})
}

func TestPipelineStartKeepsFirstDial(t *testing.T) {
	t.Parallel()
	pl := newPipeline(t, true)
	xfail(t, "first device dial is bound to the POST /sessions request context (Plan 2)", func() error {
		if pl.baseDials != 1 {
			var log []string
			for _, e := range pl.h.Fake.Events() {
				log = append(log, e.Kind+" "+e.Conn+" "+e.Detail)
			}
			return errorf("session start dialed the device %d times: %v", pl.baseDials, log)
		}
		return nil
	})
}
