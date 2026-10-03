package faults

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

// Signal-model thresholds (percent quality). They approximate how a tuner
// degrades; scenarios can bypass them with raw corrupt/stall faults.
const (
	cleanQuality  = 80    // at or above: no errors
	burstQuality  = 50    // below: error bursts
	lockQuality   = 30    // below: loss of lock (silence)
	maxErrorRate  = 0.05  // per-packet error probability at burstQuality
	burstStartP   = 0.002 // per-packet chance a burst starts below burstQuality
	burstMinPkts  = 20
	burstMaxPkts  = 200
	dropFaultLife = 10 * time.Second // a drop expires once fired or after this
)

// Action is what the device does with one packet.
type Action int

const (
	Emit  Action = iota // write Decision.Packets
	Skip                // drop this packet silently
	Hold                // stall: withhold (Burst: deliver later) or discard
	Close               // end the connection (Reset: abortive)
)

// Decision is the engine's verdict on one packet.
type Decision struct {
	Action  Action
	Packets [][]byte // for Emit; may include a non-188-byte junk prefix (sync corruption)
	Burst   bool
	Reset   bool
}

// Levels is a tuner's reported signal.
type Levels struct{ Strength, Quality, Symbol int }

// Locked reports whether the tuner holds lock at these levels.
func (l Levels) Locked() bool { return l.Quality >= lockQuality }

// ActiveFault describes a fault currently in effect.
type ActiveFault struct {
	ID     string    `json:"id"`
	Target Target    `json:"target"`
	Spec   Spec      `json:"spec"`
	Since  time.Time `json:"since"`
}

type fault struct {
	ActiveFault
	until time.Time       // zero = until removed
	conns map[string]bool // snapshot for ScopeConnections and drop
	fired map[string]bool // drop: connections already closed
}

func (f *fault) hits(channel, conn string) bool {
	if f.Spec.Fault.IsDevice() || f.Target.Channel != channel {
		return false
	}
	if f.Target.Scope == ScopeConnections || f.Spec.Fault == Drop {
		return f.conns[conn]
	}
	return true
}

// Engine holds active faults and decides each packet's fate. Safe for
// concurrent use; deterministic for a given seed and call order.
type Engine struct {
	clock tsloop.Clock

	mu     sync.Mutex
	rng    *rand.Rand
	seq    int
	faults []*fault
	burst  map[string]int     // channel|conn → remaining burst packets
	slow   map[string]float64 // channel|conn → emit accumulator
}

// NewEngine returns an engine using clock for fault expiry and seed for all
// randomness.
func NewEngine(clock tsloop.Clock, seed int64) *Engine {
	if clock.Now == nil {
		clock = tsloop.RealClock()
	}
	return &Engine{
		clock: clock,
		rng:   rand.New(rand.NewSource(seed)),
		burst: map[string]int{},
		slow:  map[string]float64{},
	}
}

// Add activates a packet or device fault. liveConns is the set of connections
// currently on the target channel (snapshot for connection-scoped faults and
// drops). Timeline faults return ErrTimelineFault.
func (e *Engine) Add(t Target, s Spec, liveConns []string) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	if s.Fault.IsTimeline() {
		return "", ErrTimelineFault
	}
	if t.Scope == "" {
		t.Scope = ScopeChannel
	}
	if s.Fault.IsDevice() {
		t = Target{Scope: ScopeDevice}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	e.seq++
	f := &fault{
		ActiveFault: ActiveFault{ID: fmt.Sprintf("f%d", e.seq), Target: t, Spec: s, Since: now},
		conns:       map[string]bool{},
		fired:       map[string]bool{},
	}
	for _, c := range liveConns {
		f.conns[c] = true
	}
	switch {
	case s.For > 0:
		f.until = now.Add(s.For)
	case s.Fault == Drop:
		f.until = now.Add(dropFaultLife)
	}
	e.faults = append(e.faults, f)
	return f.ID, nil
}

// Remove deactivates a fault; unknown ids are ignored.
func (e *Engine) Remove(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, f := range e.faults {
		if f.ID == id {
			e.faults = append(e.faults[:i], e.faults[i+1:]...)
			return
		}
	}
}

// pruneLocked drops expired faults and fully fired drops.
func (e *Engine) pruneLocked() {
	now := e.clock.Now()
	kept := e.faults[:0]
	for _, f := range e.faults {
		if !f.until.IsZero() && !now.Before(f.until) {
			continue
		}
		if f.Spec.Fault == Drop && len(f.fired) >= len(f.conns) {
			continue
		}
		kept = append(kept, f)
	}
	e.faults = kept
}

// Active returns the faults currently in effect, oldest first.
func (e *Engine) Active() []ActiveFault {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked()
	out := make([]ActiveFault, 0, len(e.faults))
	for _, f := range e.faults {
		out = append(out, f.ActiveFault)
	}
	return out
}

// Signal returns the reported signal for a channel (100/100/100 by default;
// the most recently added signal fault wins per field).
func (e *Engine) Signal(channel string) Levels {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked()
	return e.signalLocked(channel)
}

func (e *Engine) signalLocked(channel string) Levels {
	l := Levels{100, 100, 100}
	for _, f := range e.faults {
		if f.Spec.Fault != Signal || f.Target.Channel != channel {
			continue
		}
		if f.Spec.Strength != nil {
			l.Strength = *f.Spec.Strength
		}
		if f.Spec.Quality != nil {
			l.Quality = *f.Spec.Quality
		}
		if f.Spec.Symbol != nil {
			l.Symbol = *f.Spec.Symbol
		}
	}
	return l
}

func (e *Engine) deviceFlag(k Kind) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked()
	for _, f := range e.faults {
		if f.Spec.Fault == k {
			return true
		}
	}
	return false
}

// Busy reports whether new dials should get 503.
func (e *Engine) Busy() bool { return e.deviceFlag(Busy) }

// Hang reports whether new dials should hang without a response.
func (e *Engine) Hang() bool { return e.deviceFlag(Hang) }

// Decide returns what to do with one packet for one connection. It never
// modifies pkt.
func (e *Engine) Decide(channel, conn string, pkt []byte, track tsloop.Track) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.pruneLocked()
	key := channel + "|" + conn

	var hits []*fault
	for _, f := range e.faults {
		if f.hits(channel, conn) {
			hits = append(hits, f)
		}
	}
	for _, f := range hits {
		if f.Spec.Fault == Drop && !f.fired[conn] {
			f.fired[conn] = true
			return Decision{Action: Close, Reset: f.Spec.Mode == "reset"}
		}
	}
	for _, f := range hits {
		if f.Spec.Fault == Stall {
			return Decision{Action: Hold, Burst: f.Spec.Burst}
		}
	}
	q := e.signalLocked(channel).Quality
	if q < lockQuality {
		return Decision{Action: Hold}
	}
	for _, f := range hits {
		if f.Spec.Fault == PIDLoss && trackMatches(f.Spec.Track, track) {
			return Decision{Action: Skip}
		}
	}
	for _, f := range hits {
		if f.Spec.Fault == Slow {
			e.slow[key] += f.Spec.Rate
			if e.slow[key] < 1 {
				return Decision{Action: Skip}
			}
			e.slow[key]--
		}
	}

	out := pkt
	var junk []byte
	copied := false
	own := func() {
		if !copied {
			out = append([]byte(nil), pkt...)
			copied = true
		}
	}

	if q < cleanQuality {
		if q < burstQuality {
			if e.burst[key] > 0 {
				e.burst[key]--
				return Decision{Action: Skip}
			}
			if e.rng.Float64() < burstStartP {
				e.burst[key] = burstMinPkts + e.rng.Intn(burstMaxPkts-burstMinPkts+1) - 1
				return Decision{Action: Skip}
			}
		}
		p := float64(cleanQuality-q) / float64(cleanQuality-burstQuality)
		if p > 1 {
			p = 1
		}
		p *= maxErrorRate
		switch r := e.rng.Float64(); {
		case r < p/2:
			own()
			out[1] |= 0x80
		case r < p:
			return Decision{Action: Skip}
		}
	}

	for _, f := range hits {
		if f.Spec.Fault != Corrupt || e.rng.Float64() >= f.Spec.Rate {
			continue
		}
		switch f.Spec.Type {
		case "tei":
			own()
			out[1] |= 0x80
		case "sync":
			junk = make([]byte, 1+e.rng.Intn(tsloop.PacketSize-1))
			e.rng.Read(junk)
		default: // bitflip
			own()
			i := 4 + e.rng.Intn(len(out)-4)
			out[i] ^= 1 << uint(e.rng.Intn(8))
		}
	}

	if junk != nil {
		return Decision{Action: Emit, Packets: [][]byte{junk, out}}
	}
	return Decision{Action: Emit, Packets: [][]byte{out}}
}

func trackMatches(want string, t tsloop.Track) bool {
	return (want == "audio" && t == tsloop.TrackAudio) || (want == "video" && t == tsloop.TrackVideo)
}
