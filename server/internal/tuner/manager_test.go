package tuner_test

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRefreshAggregatesManualAndStored(t *testing.T) {
	// Fake A: will be discovered via manual cfg.Devices
	fakeA := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "AAA11111",
		TunerCount: 2,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "5.1", GuideName: "WABC"},
		},
	})
	// Fake B: pre-stored; reachable during refresh
	fakeB := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "BBB22222",
		TunerCount: 3,
	})
	// Unreachable stored device
	const deadID = "DEAD0000"
	const deadIP = "127.0.0.1"
	// Use a closed port so FetchDiscover fails quickly.
	const deadPort = 1

	st := openTestStore(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	// Pre-store B (reachable) and a dead device.
	uB, err := url.Parse(fakeB.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDevice(store.Device{
		DeviceID:   "BBB22222",
		IP:         uB.Hostname(),
		Model:      "OLD-MODEL",
		TunerCount: 1,
		Manual:     false,
		LastSeen:   now.Add(-time.Hour),
		StreamPort: hdhr.StreamPortFromBaseURL(fakeB.URL),
	}); err != nil {
		t.Fatalf("UpsertDevice B: %v", err)
	}
	if err := st.UpsertDevice(store.Device{
		DeviceID:   deadID,
		IP:         deadIP,
		Model:      "GONE",
		TunerCount: 2,
		Manual:     true,
		LastSeen:   now.Add(-24 * time.Hour),
		StreamPort: deadPort,
	}); err != nil {
		t.Fatalf("UpsertDevice dead: %v", err)
	}

	uA, err := url.Parse(fakeA.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Manual entry: host:port so HTTP hits the fake (not port 80).
	manualHost := uA.Host // host:port

	cfg := config.Config{
		Devices: []string{manualHost},
	}
	m := tuner.New(st, cfg)
	// Suppress real UDP discovery in CI (best-effort; inject empty).
	m.SetDiscoverFunc(func(ctx context.Context, timeout time.Duration) ([]hdhr.DiscoverInfo, error) {
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := m.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Stored rows: A (new manual), B (updated), dead (kept).
	devs, err := st.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	byID := map[string]store.Device{}
	for _, d := range devs {
		byID[d.DeviceID] = d
	}
	if len(byID) != 3 {
		t.Fatalf("stored devices = %d, want 3; got %+v", len(byID), byID)
	}

	a, ok := byID["AAA11111"]
	if !ok {
		t.Fatal("missing manual device AAA11111")
	}
	if !a.Manual {
		t.Error("manual device should have Manual=true")
	}
	if a.TunerCount != 2 || a.Model != "HDFX-4US" {
		t.Errorf("manual device = %+v", a)
	}
	if a.StreamPort != hdhr.StreamPortFromBaseURL(fakeA.URL) {
		t.Errorf("StreamPort A = %d, want %d", a.StreamPort, hdhr.StreamPortFromBaseURL(fakeA.URL))
	}

	b, ok := byID["BBB22222"]
	if !ok {
		t.Fatal("missing stored device BBB22222")
	}
	if b.TunerCount != 3 {
		t.Errorf("B TunerCount = %d, want 3 (refreshed)", b.TunerCount)
	}
	if b.Model != "HDFX-4US" {
		t.Errorf("B Model = %q", b.Model)
	}

	dead, ok := byID[deadID]
	if !ok {
		t.Fatal("unreachable device row should be kept")
	}
	if dead.Model != "GONE" {
		t.Errorf("dead model changed to %q", dead.Model)
	}

	// DeviceStatus cache: A+B reachable, dead not.
	statuses := m.Devices()
	if len(statuses) != 3 {
		t.Fatalf("Devices() len = %d", len(statuses))
	}
	statusByID := map[string]tuner.DeviceStatus{}
	for _, s := range statuses {
		statusByID[s.Device.DeviceID] = s
	}
	if !statusByID["AAA11111"].Reachable {
		t.Error("A should be reachable")
	}
	if !statusByID["BBB22222"].Reachable {
		t.Error("B should be reachable")
	}
	if statusByID[deadID].Reachable {
		t.Error("dead should not be reachable")
	}
	// Live status for reachable devices has tuner resources.
	if len(statusByID["AAA11111"].Tuners) != 2 {
		t.Errorf("A tuners = %d", len(statusByID["AAA11111"].Tuners))
	}
	if len(statusByID["BBB22222"].Tuners) != 3 {
		t.Errorf("B tuners = %d", len(statusByID["BBB22222"].Tuners))
	}
	if len(statusByID[deadID].Tuners) != 0 {
		t.Errorf("dead tuners = %d, want 0", len(statusByID[deadID].Tuners))
	}
}

func TestStreamURLPortRule(t *testing.T) {
	st := openTestStore(t)

	// Real-device style: BaseURL port 80 → stream port 5004
	if err := st.UpsertDevice(store.Device{
		DeviceID:   "REAL1",
		IP:         "1.2.3.4",
		Model:      "HDHR",
		TunerCount: 2,
		StreamPort: hdhr.StreamPortFromBaseURL("http://1.2.3.4:80"),
		LastSeen:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// Fake style: non-80 BaseURL port reused
	if err := st.UpsertDevice(store.Device{
		DeviceID:   "FAKE1",
		IP:         "127.0.0.1",
		Model:      "FAKE",
		TunerCount: 2,
		StreamPort: hdhr.StreamPortFromBaseURL("http://127.0.0.1:54321"),
		LastSeen:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	m := tuner.New(st, config.Config{})
	// Seed cache without network by refreshing empty (uses stored devices as unreachable
	// unless we inject). StreamURL only needs store rows + StreamPort.
	// Force-load cache from store via a no-op discover and failing fetches is awkward;
	// call StreamURL after manually priming via Refresh with inject that marks them known.
	// Simpler: StreamURL looks up channel's device in store (and/or cache).
	urlReal, err := m.StreamURL(store.Channel{DeviceID: "REAL1", GuideNumber: "5.1"})
	if err != nil {
		t.Fatalf("StreamURL real: %v", err)
	}
	wantReal := "http://1.2.3.4:5004/auto/v5.1"
	if urlReal != wantReal {
		t.Errorf("real StreamURL = %q, want %q", urlReal, wantReal)
	}

	urlFake, err := m.StreamURL(store.Channel{DeviceID: "FAKE1", GuideNumber: "5.1"})
	if err != nil {
		t.Fatalf("StreamURL fake: %v", err)
	}
	wantFake := "http://127.0.0.1:54321/auto/v5.1"
	if urlFake != wantFake {
		t.Errorf("fake StreamURL = %q, want %q", urlFake, wantFake)
	}

	// Also assert the pure port rule as specified in the plan.
	if p := hdhr.StreamPortFromBaseURL("http://1.2.3.4:80"); p != 5004 {
		t.Errorf("port rule :80 = %d", p)
	}
	if p := hdhr.StreamPortFromBaseURL("http://127.0.0.1:54321"); p != 54321 {
		t.Errorf("port rule fake = %d", p)
	}
}
