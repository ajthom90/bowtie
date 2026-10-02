package tsloop

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeClock advances instantly whenever someone waits on After.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	f.Advance(d)
	ch := make(chan time.Time, 1)
	ch <- f.Now()
	return ch
}

func (f *fakeClock) Clock() Clock { return Clock{Now: f.Now, After: f.After} }

// collect reads until the fake clock passes until.
func collect(t *testing.T, r *Reader, fc *fakeClock, until time.Time) [][]byte {
	t.Helper()
	var out [][]byte
	for fc.Now().Before(until) {
		batch, err := r.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, batch...)
	}
	return out
}

// unwrap returns ts made monotonic across 33-bit wraps relative to prev.
func unwrap(prev, ts int64) int64 {
	for ts < prev-(1<<32) {
		ts += 1 << 33
	}
	return ts
}

func checkMonotonic(t *testing.T, pkts [][]byte) (wraps int) {
	t.Helper()
	lastTS := map[uint16]int64{}
	lastPCR, lastRawPCR := int64(-1), int64(-1)
	lastCC := map[uint16]int{}
	for n, p := range pkts {
		pid := PID(p)
		if base, _, ok := PCR(p); ok {
			if lastRawPCR >= 0 && base < lastRawPCR {
				wraps++
			}
			lastRawPCR = base
			if lastPCR >= 0 {
				u := unwrap(lastPCR, base)
				if u <= lastPCR {
					t.Fatalf("packet %d: PCR %d not after %d", n, u, lastPCR)
				}
				base = u
			}
			lastPCR = base
		}
		if pts, dts, hasPTS, hasDTS := PESTimestamps(p); hasPTS {
			ts := pts
			if hasDTS {
				ts = dts
			}
			if prev, ok := lastTS[pid]; ok {
				ts = unwrap(prev, ts)
				if ts <= prev {
					t.Fatalf("packet %d pid %#x: ts %d not after %d", n, pid, ts, prev)
				}
			}
			lastTS[pid] = ts
		}
		if HasPayload(p) {
			if prev, ok := lastCC[pid]; ok && int(CC(p)) != (prev+1)&0xF {
				t.Fatalf("packet %d pid %#x: CC %d after %d", n, pid, CC(p), prev)
			}
			lastCC[pid] = int(CC(p))
		}
	}
	return wraps
}

func TestReaderTimestampsMonotonicAcrossLoops(t *testing.T) {
	src := loadFixture(t)
	fc := newFakeClock()
	ch := NewChannel(src, fc.Clock(), src.Origin90k())
	r := ch.NewReader()
	pkts := collect(t, r, fc, fc.Now().Add(100*src.LoopDuration()))
	if len(pkts) < 99*src.Packets() {
		t.Fatalf("got %d packets, want ≥ %d", len(pkts), 99*src.Packets())
	}
	checkMonotonic(t, pkts)
}

func TestReaderPacing(t *testing.T) {
	src := loadFixture(t)
	fc := newFakeClock()
	start := fc.Now()
	ch := NewChannel(src, fc.Clock(), src.Origin90k())
	r := ch.NewReader()
	n := 0
	end := start.Add(5 * src.LoopDuration())
	for {
		batch, err := r.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if fc.Now().After(end) {
			break
		}
		n += len(batch)
	}
	if want := 5 * src.Packets(); n < want-1 || n > want+1 {
		t.Fatalf("packets in 5 loops = %d, want %d±1", n, want)
	}
}

func TestJoinMidStream(t *testing.T) {
	src := loadFixture(t)
	fc := newFakeClock()
	ch := NewChannel(src, fc.Clock(), src.Origin90k())
	fc.Advance(src.LoopDuration() / 2)
	r := ch.NewReader()
	for {
		batch, err := r.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) > src.Packets()/2+1 {
			t.Fatalf("first batches look like a restart from packet 0 (%d packets)", len(batch))
		}
		for _, p := range batch {
			if base, _, ok := PCR(p); ok {
				if diff := base - ch.Now90k(); diff < -9000 || diff > 9000 {
					t.Fatalf("joined PCR %d vs channel clock %d (diff %d ticks)", base, ch.Now90k(), diff)
				}
				return
			}
		}
	}
}

func TestChannelWrap(t *testing.T) {
	src := loadFixture(t)
	fc := newFakeClock()
	ch := NewChannel(src, fc.Clock(), tsMask-2*90000)
	pkts := collect(t, ch.NewReader(), fc, fc.Now().Add(3*src.LoopDuration()))
	if wraps := checkMonotonic(t, pkts); wraps != 1 {
		t.Fatalf("PCR wrapped %d times, want 1", wraps)
	}
}

func TestSwitchKeepsTimelineMonotonic(t *testing.T) {
	src := loadFixture(t)
	other, err := Load("other", mustOpen(t))
	if err != nil {
		t.Fatal(err)
	}
	fc := newFakeClock()
	ch := NewChannel(src, fc.Clock(), src.Origin90k())
	r := ch.NewReader()
	pkts := collect(t, r, fc, fc.Now().Add(src.LoopDuration()*3/2))
	ch.Switch(other)
	if ch.Source() != other {
		t.Fatal("Source() not switched")
	}
	pkts = append(pkts, collect(t, r, fc, fc.Now().Add(2*src.LoopDuration()))...)
	lastPCR := int64(-1)
	for _, p := range pkts {
		if base, _, ok := PCR(p); ok {
			if base <= lastPCR {
				t.Fatalf("PCR went back across switch: %d after %d", base, lastPCR)
			}
			lastPCR = base
		}
	}
}

func TestJumpShiftsTimestamps(t *testing.T) {
	src := loadFixture(t)
	// Identical runs, one with a jump: the first PCR after the jump point must
	// differ by exactly the jump.
	run := func(jump time.Duration) int64 {
		fc := newFakeClock()
		ch := NewChannel(src, fc.Clock(), src.Origin90k())
		r := ch.NewReader()
		collect(t, r, fc, fc.Now().Add(src.LoopDuration()*3/2))
		ch.Jump(jump)
		return firstPCR(collect(t, r, fc, fc.Now().Add(src.LoopDuration())))
	}
	if d := run(10*time.Second) - run(0); d != 10*90000 {
		t.Fatalf("PCR shift from jump = %d ticks, want 900000", d)
	}
}

func TestReaderHonorsContext(t *testing.T) {
	src := loadFixture(t)
	ch := NewChannel(src, RealClock(), src.Origin90k())
	r := ch.NewReader()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Next(ctx); err == nil {
		t.Fatal("Next with cancelled context returned no error")
	}
}

func TestRealClockPacingSmoke(t *testing.T) {
	src := loadFixture(t)
	ch := NewChannel(src, RealClock(), src.Origin90k())
	r := ch.NewReader()
	// One loop's worth of packets should take about one loop of wall time.
	start := time.Now()
	for n := 0; n < src.Packets(); {
		batch, err := r.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		n += len(batch)
	}
	loop := src.LoopDuration()
	if el := time.Since(start); el < loop*85/100 || el > loop*115/100 {
		t.Fatalf("one loop took %v, want ≈ %v", el, loop)
	}
}

func mustOpen(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func firstPCR(pkts [][]byte) int64 {
	for _, p := range pkts {
		if base, _, ok := PCR(p); ok {
			return base
		}
	}
	return -1
}
