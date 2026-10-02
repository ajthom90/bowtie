package faults

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (m *manualClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *manualClock) Advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	m.mu.Unlock()
}

func newEngine(t *testing.T) (*Engine, *manualClock) {
	t.Helper()
	mc := &manualClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	clock := tsloop.Clock{Now: mc.Now, After: func(d time.Duration) <-chan time.Time {
		mc.Advance(d)
		ch := make(chan time.Time, 1)
		ch <- mc.Now()
		return ch
	}}
	return NewEngine(clock, 1), mc
}

func intp(v int) *int { return &v }

func pkt(pid uint16) []byte {
	p := make([]byte, tsloop.PacketSize)
	p[0] = 0x47
	p[1] = byte(pid >> 8)
	p[2] = byte(pid)
	p[3] = 0x10
	for i := 4; i < len(p); i++ {
		p[i] = byte(i)
	}
	return p
}

var ch = Target{Channel: "90.1"}

func mustAdd(t *testing.T, e *Engine, tg Target, s Spec, conns ...string) string {
	t.Helper()
	id, err := e.Add(tg, s, conns)
	if err != nil {
		t.Fatalf("Add(%+v): %v", s, err)
	}
	return id
}

// tally runs n decisions and counts actions; TEI-marked emits count as "tei".
func tally(e *Engine, n int, track tsloop.Track) map[string]int {
	out := map[string]int{}
	for i := 0; i < n; i++ {
		d := e.Decide("90.1", "c1", pkt(0x100), track)
		switch d.Action {
		case Emit:
			if d.Packets[len(d.Packets)-1][1]&0x80 != 0 {
				out["tei"]++
			} else {
				out["emit"]++
			}
		case Skip:
			out["skip"]++
		case Hold:
			out["hold"]++
		case Close:
			out["close"]++
		}
	}
	return out
}

func TestSignalDefaultLevels(t *testing.T) {
	e, _ := newEngine(t)
	if got := e.Signal("90.1"); got != (Levels{100, 100, 100}) {
		t.Fatalf("default = %+v", got)
	}
}

func TestSignalSetAndExpire(t *testing.T) {
	e, mc := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: Signal, Quality: intp(55), For: 5 * time.Second})
	if got := e.Signal("90.1"); got.Quality != 55 || got.Strength != 100 {
		t.Fatalf("during = %+v", got)
	}
	if got := e.Signal("90.2"); got.Quality != 100 {
		t.Fatalf("other channel = %+v", got)
	}
	mc.Advance(5 * time.Second)
	if got := e.Signal("90.1"); got.Quality != 100 {
		t.Fatalf("after expiry = %+v", got)
	}
	if len(e.Active()) != 0 {
		t.Fatalf("expired fault still active: %+v", e.Active())
	}
}

func TestSignalErrorRates(t *testing.T) {
	cases := []struct {
		q     int
		check func(t *testing.T, c map[string]int)
	}{
		{90, func(t *testing.T, c map[string]int) {
			if c["emit"] != 100000 {
				t.Fatalf("q=90 tally %v", c)
			}
		}},
		{65, func(t *testing.T, c map[string]int) {
			bad := float64(c["tei"]+c["skip"]) / 100000
			if bad < 0.020 || bad > 0.030 || c["tei"] == 0 || c["skip"] == 0 {
				t.Fatalf("q=65 tally %v (bad %.4f)", c, bad)
			}
		}},
		{20, func(t *testing.T, c map[string]int) {
			if c["hold"] != 100000 {
				t.Fatalf("q=20 tally %v", c)
			}
		}},
	}
	for _, tc := range cases {
		e, _ := newEngine(t)
		mustAdd(t, e, ch, Spec{Fault: Signal, Quality: intp(tc.q)})
		tc.check(t, tally(e, 100000, tsloop.TrackVideo))
	}
}

func TestSignalBurstsBelow50(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: Signal, Quality: intp(40)})
	run, longest := 0, 0
	for i := 0; i < 100000; i++ {
		if e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo).Action == Skip {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest < 20 {
		t.Fatalf("longest skip run = %d, want a burst ≥ 20", longest)
	}
}

func TestStallHoldsThenResumes(t *testing.T) {
	e, mc := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: Stall, For: 2 * time.Second, Burst: true})
	d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo)
	if d.Action != Hold || !d.Burst {
		t.Fatalf("during stall = %+v", d)
	}
	mc.Advance(2 * time.Second)
	if d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo); d.Action != Emit {
		t.Fatalf("after stall = %+v", d)
	}
}

func TestConnectionsScopeOnlyHitsLiveConns(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, Target{Channel: "90.1", Scope: ScopeConnections}, Spec{Fault: Stall}, "c1")
	if d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo); d.Action != Hold {
		t.Fatalf("live conn = %+v", d)
	}
	if d := e.Decide("90.1", "c2", pkt(0x100), tsloop.TrackVideo); d.Action != Emit {
		t.Fatalf("later conn = %+v", d)
	}
}

func TestDropOneShotPerConn(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, Target{Channel: "90.1", Scope: ScopeConnections}, Spec{Fault: Drop}, "c1")
	if d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo); d.Action != Close || d.Reset {
		t.Fatalf("first = %+v", d)
	}
	if d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo); d.Action != Emit {
		t.Fatalf("second = %+v", d)
	}
	if d := e.Decide("90.1", "c2", pkt(0x100), tsloop.TrackVideo); d.Action != Emit {
		t.Fatalf("new conn = %+v", d)
	}
}

func TestDropReset(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: Drop, Mode: "reset"}, "c1")
	if d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo); d.Action != Close || !d.Reset {
		t.Fatalf("got %+v", d)
	}
}

func TestCorruptKinds(t *testing.T) {
	for _, kind := range []string{"tei", "bitflip", "sync"} {
		e, _ := newEngine(t)
		mustAdd(t, e, ch, Spec{Fault: Corrupt, Rate: 1, Type: kind})
		in := pkt(0x100)
		orig := append([]byte(nil), in...)
		d := e.Decide("90.1", "c1", in, tsloop.TrackVideo)
		if !bytes.Equal(in, orig) {
			t.Fatalf("%s: input mutated", kind)
		}
		if d.Action != Emit {
			t.Fatalf("%s: action %v", kind, d.Action)
		}
		out := d.Packets[len(d.Packets)-1]
		switch kind {
		case "tei":
			if out[1]&0x80 == 0 || !bytes.Equal(out[2:], orig[2:]) {
				t.Fatal("tei: bit not set or payload changed")
			}
		case "bitflip":
			diff := 0
			for i := range out {
				x := out[i] ^ orig[i]
				for ; x != 0; x &= x - 1 {
					diff++
				}
				if x := out[i] ^ orig[i]; x != 0 && i < 4 {
					t.Fatalf("bitflip touched header byte %d", i)
				}
			}
			if diff != 1 {
				t.Fatalf("bitflip changed %d bits", diff)
			}
		case "sync":
			if len(d.Packets) != 2 || len(d.Packets[0]) < 1 || len(d.Packets[0]) >= tsloop.PacketSize || !bytes.Equal(out, orig) {
				t.Fatalf("sync: packets %d first len %d", len(d.Packets), len(d.Packets[0]))
			}
		}
	}
}

func TestPIDLossAudioOnly(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: PIDLoss, Track: "audio", For: time.Second})
	if d := e.Decide("90.1", "c1", pkt(0x101), tsloop.TrackAudio); d.Action != Skip {
		t.Fatalf("audio = %+v", d)
	}
	if d := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo); d.Action != Emit {
		t.Fatalf("video = %+v", d)
	}
}

func TestSlowRateLimits(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: Slow, Rate: 0.5})
	c := tally(e, 10000, tsloop.TrackVideo)
	if c["emit"] < 4900 || c["emit"] > 5100 {
		t.Fatalf("slow 0.5 tally %v", c)
	}
}

func TestBusyHangFlags(t *testing.T) {
	e, _ := newEngine(t)
	if e.Busy() || e.Hang() {
		t.Fatal("flags set by default")
	}
	id := mustAdd(t, e, Target{Scope: ScopeDevice}, Spec{Fault: Busy})
	mustAdd(t, e, Target{Scope: ScopeDevice}, Spec{Fault: Hang, For: time.Second})
	if !e.Busy() || !e.Hang() {
		t.Fatal("flags not set")
	}
	e.Remove(id)
	if e.Busy() {
		t.Fatal("busy not cleared by Remove")
	}
}

func TestTimelineKindsRejected(t *testing.T) {
	e, _ := newEngine(t)
	for _, s := range []Spec{{Fault: TimestampJump, By: time.Second}, {Fault: SourceSwitch, To: "x"}} {
		if _, err := e.Add(ch, s, nil); !errors.Is(err, ErrTimelineFault) {
			t.Fatalf("%s: err = %v, want ErrTimelineFault", s.Fault, err)
		}
	}
}

func TestValidate(t *testing.T) {
	bad := []Spec{
		{Fault: "nope"},
		{Fault: Signal},
		{Fault: Signal, Quality: intp(101)},
		{Fault: Stall, For: -time.Second},
		{Fault: Drop, Mode: "bogus"},
		{Fault: Corrupt, Rate: 0},
		{Fault: Corrupt, Rate: 0.1, Type: "zap"},
		{Fault: Slow, Rate: 1.5},
		{Fault: PIDLoss, Track: "subtitles"},
		{Fault: TimestampJump},
		{Fault: SourceSwitch},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", s)
		}
	}
	good := []Spec{
		{Fault: Signal, Quality: intp(40), For: time.Second},
		{Fault: Stall, Burst: true},
		{Fault: Drop, Mode: "clean"},
		{Fault: Corrupt, Rate: 0.02, Type: "sync"},
		{Fault: Slow, Rate: 0.8},
		{Fault: PIDLoss, Track: "video"},
		{Fault: TimestampJump, By: -5 * time.Second},
		{Fault: SourceSwitch, To: "synthetic-480i"},
		{Fault: Busy},
		{Fault: Hang},
	}
	for _, s := range good {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", s, err)
		}
	}
}

func TestDeterministicWithSeed(t *testing.T) {
	run := func() []Action {
		e, _ := newEngine(t)
		mustAdd(t, e, ch, Spec{Fault: Signal, Quality: intp(45)})
		mustAdd(t, e, ch, Spec{Fault: Corrupt, Rate: 0.1})
		var out []Action
		for i := 0; i < 5000; i++ {
			out = append(out, e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo).Action)
		}
		return out
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("decision %d differs: %v vs %v", i, a[i], b[i])
		}
	}
}
