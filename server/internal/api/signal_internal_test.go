package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestSignalWeakNeedsTwoBadReadings(t *testing.T) {
	bad := signalSample{Strength: 96, Quality: 46, SymbolQuality: 0}
	good := signalSample{Strength: 90, Quality: 92, SymbolQuality: 100}
	lowQuality := signalSample{Strength: 80, Quality: 40, SymbolQuality: 100}
	for _, c := range []struct {
		name string
		h    []signalSample
		want bool
	}{
		{"no readings", nil, false},
		{"one bad reading (could be a blip)", []signalSample{bad}, false},
		{"two bad readings", []signalSample{bad, bad}, true},
		{"bad then good", []signalSample{bad, good}, false},
		{"good then bad", []signalSample{good, bad}, false},
		{"low signal quality counts", []signalSample{lowQuality, lowQuality}, true},
		{"symbol quality just under 85", []signalSample{{Quality: 90, SymbolQuality: 84}, {Quality: 90, SymbolQuality: 84}}, true},
		{"symbol quality 85 is fine", []signalSample{{Quality: 90, SymbolQuality: 85}, {Quality: 90, SymbolQuality: 85}}, false},
	} {
		if got := signalWeak(c.h); got != c.want {
			t.Errorf("%s: weak = %v, want %v", c.name, got, c.want)
		}
	}
}

// fakeStatus serves a scripted sequence of status.json readings and counts fetches.
type fakeStatus struct {
	readings [][]hdhr.TunerStatus
	calls    int
	err      error
}

func (f *fakeStatus) fetch(ctx context.Context, baseURL string) ([]hdhr.TunerStatus, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls - 1
	if i >= len(f.readings) {
		i = len(f.readings) - 1
	}
	return f.readings[i], nil
}

func tuned(guide string, strength, quality, symbol int) []hdhr.TunerStatus {
	return []hdhr.TunerStatus{
		{Resource: "tuner0", VctNumber: guide, SignalStrengthPercent: strength, SignalQualityPercent: quality, SymbolQualityPercent: symbol},
		{Resource: "tuner1"},
	}
}

func TestSignalMonitorCachesAndTracksHistory(t *testing.T) {
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	f := &fakeStatus{readings: [][]hdhr.TunerStatus{tuned("9.1", 96, 46, 0), tuned("9.1", 95, 47, 0)}}
	m := newSignalMonitor(f.fetch, func() time.Time { return now })
	dev := store.Device{DeviceID: "D1", IP: "192.168.50.32"}
	ch := store.Channel{ID: 7, DeviceID: "D1", GuideNumber: "9.1"}

	r, ok := m.read(context.Background(), dev, ch)
	if !ok || r.Weak || r.Quality != 46 || r.SymbolQuality != 0 || r.Strength != 96 {
		t.Fatalf("first read = %+v ok=%v, want the reading, not yet weak", r, ok)
	}
	// Within the cache window: no new fetch, and the same sample isn't counted twice.
	now = now.Add(5 * time.Second)
	if r, _ = m.read(context.Background(), dev, ch); r.Weak || f.calls != 1 {
		t.Fatalf("cached read = %+v calls=%d, want not weak and 1 fetch", r, f.calls)
	}
	// A fresh reading after the cache expires: two bad samples → weak.
	now = now.Add(10 * time.Second)
	if r, _ = m.read(context.Background(), dev, ch); !r.Weak || f.calls != 2 || r.Quality != 47 {
		t.Fatalf("second fresh read = %+v calls=%d, want weak", r, f.calls)
	}
}

func TestSignalMonitorUnknown(t *testing.T) {
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	dev := store.Device{DeviceID: "D1", IP: "192.168.50.32"}
	ch := store.Channel{ID: 7, DeviceID: "D1", GuideNumber: "9.1"}

	// No tuner on this channel (e.g. it just stopped): unknown, not weak.
	f := &fakeStatus{readings: [][]hdhr.TunerStatus{tuned("5.1", 90, 90, 100)}}
	if _, ok := newSignalMonitor(f.fetch, func() time.Time { return now }).read(context.Background(), dev, ch); ok {
		t.Fatal("no tuner on 9.1: want unknown")
	}
	// HDHomeRun unreachable: unknown.
	f = &fakeStatus{err: errors.New("dial tcp: timeout")}
	if _, ok := newSignalMonitor(f.fetch, func() time.Time { return now }).read(context.Background(), dev, ch); ok {
		t.Fatal("fetch error: want unknown")
	}
}
