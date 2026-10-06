package api

import (
	"context"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// Reception thresholds for the players' "Weak signal" note. Symbol quality
// below 100% means uncorrected errors (the picture breaks up); signal quality
// is the tuner's SNR-based grade.
const (
	weakSymbolQuality = 85
	weakSignalQuality = 50
	// signalCacheTTL bounds status.json reads per HDHomeRun, however many
	// viewers send heartbeats.
	signalCacheTTL = 10 * time.Second
)

// signalSample is one reading of a channel's tuner (percentages).
type signalSample struct {
	Strength      int  `json:"strength"`
	Quality       int  `json:"quality"`
	SymbolQuality int  `json:"symbolQuality"`
	Weak          bool `json:"weak"`
}

func (s signalSample) bad() bool {
	return s.SymbolQuality < weakSymbolQuality || s.Quality < weakSignalQuality
}

// signalWeak: the last two fresh readings were both bad, so one blip never
// shows the note.
func signalWeak(history []signalSample) bool {
	if len(history) < 2 {
		return false
	}
	for _, s := range history[len(history)-2:] {
		if !s.bad() {
			return false
		}
	}
	return true
}

type deviceStatus struct {
	tuners []hdhr.TunerStatus
	at     time.Time
	err    error
}

// signalMonitor reads tuner reception from each HDHomeRun's status.json,
// cached per device, and keeps the last two fresh samples per channel.
type signalMonitor struct {
	fetch func(ctx context.Context, baseURL string) ([]hdhr.TunerStatus, error)
	now   func() time.Time

	mu      sync.Mutex
	devices map[string]deviceStatus
	history map[int64][]signalSample
}

func newSignalMonitor(fetch func(context.Context, string) ([]hdhr.TunerStatus, error), now func() time.Time) *signalMonitor {
	if fetch == nil {
		fetch = hdhr.FetchStatus
	}
	if now == nil {
		now = time.Now
	}
	return &signalMonitor{fetch: fetch, now: now, devices: map[string]deviceStatus{}, history: map[int64][]signalSample{}}
}

// read returns the channel's current reception, or ok=false when unknown (no
// tuner on that channel right now, or the HDHomeRun can't be reached).
func (m *signalMonitor) read(ctx context.Context, dev store.Device, ch store.Channel) (signalSample, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	st, cached := m.devices[dev.DeviceID]
	fresh := !cached || now.Sub(st.at) >= signalCacheTTL
	if fresh {
		tuners, err := m.fetch(ctx, hdhr.HTTPBaseURL(dev.IP, dev.StreamPort))
		st = deviceStatus{tuners: tuners, at: now, err: err}
		m.devices[dev.DeviceID] = st
	}
	if st.err != nil {
		return signalSample{}, false
	}
	for _, t := range st.tuners {
		if t.VctNumber != ch.GuideNumber {
			continue
		}
		s := signalSample{Strength: t.SignalStrengthPercent, Quality: t.SignalQualityPercent, SymbolQuality: t.SymbolQualityPercent}
		h := m.history[ch.ID]
		if fresh {
			h = append(h, s)
			if len(h) > 2 {
				h = h[len(h)-2:]
			}
			m.history[ch.ID] = h
		}
		s.Weak = signalWeak(h)
		return s, true
	}
	delete(m.history, ch.ID)
	return signalSample{}, false
}
