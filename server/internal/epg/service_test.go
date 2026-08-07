package epg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func testProvider(t *testing.T, st *store.Store) *settings.Provider {
	t.Helper()
	return settings.NewProvider(st)
}

// afterGate is a controllable time.After substitute for supervisor tests.
// Each After(d) queues a channel; AdvanceAll releases every pending wait.
// Callers must wait for PendingCount > 0 before AdvanceAll so wakes are not lost.
type afterGate struct {
	mu      sync.Mutex
	pending []chan time.Time
}

func (g *afterGate) After(d time.Duration) <-chan time.Time {
	_ = d
	ch := make(chan time.Time, 1)
	g.mu.Lock()
	g.pending = append(g.pending, ch)
	g.mu.Unlock()
	return ch
}

func (g *afterGate) AdvanceAll() {
	g.mu.Lock()
	pending := g.pending
	g.pending = nil
	g.mu.Unlock()
	for _, ch := range pending {
		ch <- time.Time{}
	}
}

func (g *afterGate) PendingCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pending)
}

// waitUntil polls cond until true or timeout.
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func fixtureXMLTVBody(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("xmltv", "testdata", "guide.xml"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// xmltvStubServer serves the golden XMLTV fixture and counts hits.
func xmltvStubServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	body := fixtureXMLTVBody(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func startSupervisor(t *testing.T, svc *Service, gate *afterGate) context.CancelFunc {
	t.Helper()
	svc.after = gate.After
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		// Unblock any sleepOrDone waits so Run can observe ctx cancel promptly.
		gate.AdvanceAll()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not exit after cancel")
		}
	})
	return cancel
}

// TestSupervisorStartsSourceEnabledAtRuntime: boot unconfigured; poll tick;
// SetXMLTV → next tick refreshes via stub fetcher.
func TestSupervisorStartsSourceEnabledAtRuntime(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	// Explicit empty source so HasSetting is true and configured is false.
	if err := prov.SetXMLTV(settings.XMLTV{Source: "", RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	srv, hits := xmltvStubServer(t)
	svc := NewService(st, prov)
	gate := &afterGate{}
	startSupervisor(t, svc, gate)

	// Both supervisors enter unconfigured poll (60s).
	waitUntil(t, 2*time.Second, func() bool {
		return svc.lastWaitFor("xmltv") == unconfiguredPoll && gate.PendingCount() >= 1
	})
	if hits.Load() != 0 {
		t.Fatalf("hits before enable = %d, want 0", hits.Load())
	}

	if err := prov.SetXMLTV(settings.XMLTV{Source: srv.URL, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	// Release the unconfigured poll → supervisor re-reads provider and refreshes.
	waitUntil(t, 2*time.Second, func() bool { return gate.PendingCount() >= 2 })
	gate.AdvanceAll()
	// lastWait is recorded only after refresh completes (including LastSuccess write).
	waitUntil(t, 2*time.Second, func() bool {
		w := svc.lastWaitFor("xmltv")
		return hits.Load() >= 1 && w > 0 && w != unconfiguredPoll
	})

	if hits.Load() < 1 {
		t.Fatal("expected stub fetcher to be hit after enabling source at runtime")
	}
	status := svc.Status()
	if !status.XMLTV.Configured {
		t.Error("Status.Configured should be true after SetXMLTV")
	}
	if status.XMLTV.LastSuccess.IsZero() {
		t.Error("LastSuccess should be set after runtime enable refresh")
	}
}

// TestSupervisorStopsWhenCleared: configured → refresh; clear → no further
// fetches and no error status accumulation.
func TestSupervisorStopsWhenCleared(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	srv, hits := xmltvStubServer(t)
	if err := prov.SetXMLTV(settings.XMLTV{Source: srv.URL, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(st, prov)
	gate := &afterGate{}
	startSupervisor(t, svc, gate)

	// lastWait is recorded only after refresh finishes — do not poll Status during
	// the write path (avoids SQLITE_BUSY noise with the open transaction).
	waitUntil(t, 2*time.Second, func() bool {
		w := svc.lastWaitFor("xmltv")
		return hits.Load() >= 1 && w > 0 && w != unconfiguredPoll
	})
	afterFirst := hits.Load()
	if afterFirst < 1 {
		t.Fatal("expected at least one refresh while configured")
	}
	status := svc.Status()
	if status.XMLTV.LastSuccess.IsZero() {
		t.Fatal("LastSuccess should be set after configured refresh")
	}
	if status.XMLTV.LastError != "" {
		t.Fatalf("unexpected LastError after success: %q", status.XMLTV.LastError)
	}

	// Clear source (UI disable).
	if err := prov.SetXMLTV(settings.XMLTV{Source: "", RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	// Advance post-refresh sleep → next tick sees empty source, polls 60s.
	waitUntil(t, 2*time.Second, func() bool { return gate.PendingCount() >= 1 })
	gate.AdvanceAll()
	waitUntil(t, 2*time.Second, func() bool {
		return svc.lastWaitFor("xmltv") == unconfiguredPoll && gate.PendingCount() >= 1
	})

	// Several more unconfigured polls must not fetch or accumulate errors.
	for i := 0; i < 3; i++ {
		gate.AdvanceAll()
		waitUntil(t, 2*time.Second, func() bool {
			return svc.lastWaitFor("xmltv") == unconfiguredPoll && gate.PendingCount() >= 1
		})
	}

	if hits.Load() != afterFirst {
		t.Fatalf("hits after clear = %d, want still %d (no further fetches)", hits.Load(), afterFirst)
	}
	status = svc.Status()
	if status.XMLTV.Configured {
		t.Error("Configured should be false after clear")
	}
	if status.XMLTV.LastError != "" {
		t.Errorf("LastError after clear = %q, want empty (no error spam)", status.XMLTV.LastError)
	}
}

// TestIntervalChangeApplies: refreshHours 12→1 → next sleep is ~1h (±10% jitter).
func TestIntervalChangeApplies(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	srv, hits := xmltvStubServer(t)
	if err := prov.SetXMLTV(settings.XMLTV{Source: srv.URL, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(st, prov)
	gate := &afterGate{}
	startSupervisor(t, svc, gate)

	// First configured cycle: refresh then wait ~12h.
	waitUntil(t, 2*time.Second, func() bool {
		return hits.Load() >= 1 && inJitterBand(svc.lastWaitFor("xmltv"), 12*time.Hour)
	})
	w12 := svc.lastWaitFor("xmltv")
	if !inJitterBand(w12, 12*time.Hour) {
		t.Fatalf("first wait = %v, want 12h ±10%%", w12)
	}

	if err := prov.SetXMLTV(settings.XMLTV{Source: srv.URL, RefreshHours: 1}); err != nil {
		t.Fatal(err)
	}

	// Complete the 12h sleep; next cycle re-reads hours=1 and waits ~1h.
	waitUntil(t, 2*time.Second, func() bool { return gate.PendingCount() >= 1 })
	gate.AdvanceAll()
	waitUntil(t, 2*time.Second, func() bool {
		return hits.Load() >= 2 && inJitterBand(svc.lastWaitFor("xmltv"), time.Hour)
	})
	w1 := svc.lastWaitFor("xmltv")
	if !inJitterBand(w1, time.Hour) {
		t.Fatalf("after hours change wait = %v, want 1h ±10%%", w1)
	}
}

func inJitterBand(got, base time.Duration) bool {
	if base <= 0 {
		return got == base
	}
	lo := base - base/10
	hi := base + base/10
	return got >= lo && got <= hi
}

// TestRefreshAllReadsProviderLive: RefreshAll picks up a source set AFTER construction.
func TestRefreshAllReadsProviderLive(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	// Construct with empty/unconfigured provider.
	if err := prov.SetXMLTV(settings.XMLTV{Source: "", RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, prov)

	if err := svc.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll unconfigured: %v", err)
	}
	if svc.Status().XMLTV.Configured {
		t.Fatal("should not be configured before SetXMLTV")
	}
	epgs, err := st.ListEPGChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(epgs) != 0 {
		t.Fatalf("epg channels before enable = %d, want 0", len(epgs))
	}

	fixture := filepath.Join("xmltv", "testdata", "guide.xml")
	if err := prov.SetXMLTV(settings.XMLTV{Source: fixture, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	// Same service instance — must hot-read the provider.
	if err := svc.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll after SetXMLTV: %v", err)
	}
	if !svc.Status().XMLTV.Configured {
		t.Error("Configured should be true after SetXMLTV")
	}
	epgs, err = st.ListEPGChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(epgs) < 2 {
		t.Fatalf("epg channels after live RefreshAll = %d, want >= 2", len(epgs))
	}
	if svc.Status().XMLTV.LastSuccess.IsZero() {
		t.Error("LastSuccess should be set")
	}
}

func TestRefreshAllImportsAndPrunes(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)

	// Fixture programmes from guide.xml:
	// ch1: 2026-08-04 19:00-20:00 -0500 → 00:00-01:00 UTC
	// ch2: 2026-08-05 01:00-02:00 UTC
	// bad-time programme skipped
	//
	// Seed an old program that should be pruned (ended more than 24h before "now").
	fixedNow := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	oldStart := fixedNow.Add(-48 * time.Hour)
	oldStop := fixedNow.Add(-36 * time.Hour)
	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{
		{ID: "old-ch", DisplayName: "Old", Callsign: "OLD", Source: "xmltv"},
	}, []store.Program{
		{
			EPGChannelID: "old-ch",
			Start:        oldStart,
			Stop:         oldStop,
			Title:        "Should Be Pruned",
		},
	}); err != nil {
		t.Fatalf("seed old epg: %v", err)
	}

	fixture := filepath.Join("xmltv", "testdata", "guide.xml")
	if err := prov.SetXMLTV(settings.XMLTV{Source: fixture, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(st, prov)
	svc.now = func() time.Time { return fixedNow }

	if err := svc.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	// XMLTV channels replaced (old-ch gone; ch1/ch2 present).
	epgs, err := st.ListEPGChannels()
	if err != nil {
		t.Fatalf("ListEPGChannels: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range epgs {
		ids[e.ID] = true
		if e.Source != "xmltv" {
			t.Errorf("channel %s source = %q, want xmltv", e.ID, e.Source)
		}
	}
	if !ids["ch1.example"] || !ids["ch2.example"] {
		t.Fatalf("epg channels = %v, want ch1.example and ch2.example", ids)
	}
	if ids["old-ch"] {
		t.Error("old-ch should have been replaced by RefreshAll")
	}

	// Imported programmes present; pruned programme gone.
	progs, err := st.ProgramsInRange(
		[]string{"ch1.example", "ch2.example", "old-ch"},
		time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("ProgramsInRange: %v", err)
	}
	if len(progs) != 2 {
		t.Fatalf("programs len = %d, want 2 (old pruned, bad skipped): %+v", len(progs), progs)
	}
	titles := map[string]bool{}
	for _, p := range progs {
		titles[p.Title] = true
	}
	if !titles["Evening News"] || !titles["Late Night Movie"] {
		t.Errorf("titles = %v", titles)
	}
	if titles["Should Be Pruned"] {
		t.Error("pruned programme still present")
	}

	// LastSuccess recorded.
	status := svc.Status()
	if !status.XMLTV.Configured {
		t.Error("xmltv should be configured")
	}
	if status.XMLTV.LastSuccess.IsZero() {
		t.Error("xmltv LastSuccess should be set")
	}
	if !status.XMLTV.LastSuccess.Equal(fixedNow) {
		t.Errorf("LastSuccess = %v, want %v", status.XMLTV.LastSuccess, fixedNow)
	}
	if status.XMLTV.LastError != "" {
		t.Errorf("LastError = %q, want empty", status.XMLTV.LastError)
	}
	if status.XMLTV.Stale {
		t.Error("fresh success should not be stale")
	}
	if status.SD.Configured {
		t.Error("sd should not be configured")
	}
}

func TestStatusStale(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	if err := prov.SetXMLTV(settings.XMLTV{Source: "/nonexistent/guide.xml", RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(st, prov)
	fixedNow := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	// Never succeeded → stale.
	st0 := svc.Status()
	if !st0.XMLTV.Configured {
		t.Fatal("expected configured")
	}
	if !st0.XMLTV.Stale {
		t.Error("never-succeeded configured source should be stale")
	}

	// Success just now → not stale.
	if err := st.SetSetting(settingXMLTVLastSuccess, fixedNow.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	st1 := svc.Status()
	if st1.XMLTV.Stale {
		t.Error("fresh lastSuccess should not be stale")
	}

	// Success older than 2× interval (24h for 12h refresh) → stale.
	old := fixedNow.Add(-25 * time.Hour)
	if err := st.SetSetting(settingXMLTVLastSuccess, old.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	st2 := svc.Status()
	if !st2.XMLTV.Stale {
		t.Error("lastSuccess older than 2×interval should be stale")
	}

	// Just inside the threshold (exactly 2×interval is not older → not stale).
	edge := fixedNow.Add(-24 * time.Hour)
	if err := st.SetSetting(settingXMLTVLastSuccess, edge.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	st3 := svc.Status()
	if st3.XMLTV.Stale {
		t.Error("lastSuccess exactly 2×interval should not be stale (need older than)")
	}

	// Unconfigured source is never stale (live provider read).
	if err := prov.SetXMLTV(settings.XMLTV{Source: "", RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}
	st4 := svc.Status()
	if st4.XMLTV.Configured || st4.XMLTV.Stale {
		t.Errorf("unconfigured: %+v", st4.XMLTV)
	}
}

func TestGuideEnabledOnlyAndUnmappedEmpty(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	// Two channels: one enabled+mapped, one enabled+unmapped, one disabled+mapped.
	if err := st.UpsertDevice(store.Device{
		DeviceID: "dev1", IP: "1.2.3.4", Model: "X", TunerCount: 1, StreamPort: 5004,
		LastSeen: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("dev1", []store.Channel{
		{DeviceID: "dev1", GuideNumber: "5.1", Name: "WABC"},
		{DeviceID: "dev1", GuideNumber: "7.1", Name: "WXYZ"},
		{DeviceID: "dev1", GuideNumber: "9.1", Name: "Disabled"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := st.ListChannels(false)
	if err != nil {
		t.Fatal(err)
	}
	var id51, id71, id91 int64
	for _, c := range chans {
		switch c.GuideNumber {
		case "5.1":
			id51 = c.ID
		case "7.1":
			id71 = c.ID
		case "9.1":
			id91 = c.ID
		}
	}
	if err := st.UpdateChannel(id51, true, "ch1.example"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateChannel(id71, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateChannel(id91, false, "ch1.example"); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	stop := time.Date(2026, 8, 4, 2, 0, 0, 0, time.UTC)
	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{
		{ID: "ch1.example", DisplayName: "WABC", Callsign: "WABC", IconURL: "https://example.com/wabc.png", Source: "xmltv"},
	}, []store.Program{
		{
			EPGChannelID: "ch1.example",
			Start:        start,
			Stop:         stop,
			Title:        "Evening News",
			Subtitle:     "Sub",
			Description:  "Desc",
			Category:     "News",
		},
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(st, prov)
	guide, err := svc.Guide(context.Background(), start, stop)
	if err != nil {
		t.Fatalf("Guide: %v", err)
	}
	if len(guide) != 2 {
		t.Fatalf("guide len = %d, want 2 enabled channels", len(guide))
	}

	byNum := map[string]GuideChannel{}
	for _, g := range guide {
		byNum[g.GuideNumber] = g
	}
	mapped := byNum["5.1"]
	if mapped.ChannelID != id51 || mapped.LogoURL != "https://example.com/wabc.png" {
		t.Errorf("mapped channel = %+v", mapped)
	}
	if len(mapped.Programs) != 1 || mapped.Programs[0].Title != "Evening News" {
		t.Errorf("mapped programs = %+v", mapped.Programs)
	}
	unmapped := byNum["7.1"]
	if unmapped.ChannelID != id71 {
		t.Errorf("unmapped id = %d", unmapped.ChannelID)
	}
	if unmapped.Programs == nil || len(unmapped.Programs) != 0 {
		t.Errorf("unmapped programs = %#v, want empty non-nil slice", unmapped.Programs)
	}
	if _, ok := byNum["9.1"]; ok {
		t.Error("disabled channel should not appear in guide")
	}
}
