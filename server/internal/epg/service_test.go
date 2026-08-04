package epg

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
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

func TestRefreshAllImportsAndPrunes(t *testing.T) {
	st := testStore(t)

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

	// Resolve path to the shared golden XMLTV fixture.
	fixture := filepath.Join("xmltv", "testdata", "guide.xml")

	cfg := config.Config{}
	cfg.XMLTV.Source = fixture
	cfg.XMLTV.RefreshHours = 12

	svc := NewService(st, cfg)
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
	// Query a wide window covering both fixture programmes.
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
	cfg := config.Config{}
	cfg.XMLTV.Source = "/nonexistent/guide.xml"
	cfg.XMLTV.RefreshHours = 12

	svc := NewService(st, cfg)
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
	// Stale when Sub > 2*interval, so equal is not stale.
	edge := fixedNow.Add(-24 * time.Hour)
	if err := st.SetSetting(settingXMLTVLastSuccess, edge.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	st3 := svc.Status()
	if st3.XMLTV.Stale {
		t.Error("lastSuccess exactly 2×interval should not be stale (need older than)")
	}

	// Unconfigured source is never stale.
	svc2 := NewService(st, config.Config{})
	svc2.now = func() time.Time { return fixedNow }
	st4 := svc2.Status()
	if st4.XMLTV.Configured || st4.XMLTV.Stale {
		t.Errorf("unconfigured: %+v", st4.XMLTV)
	}
}

func TestGuideEnabledOnlyAndUnmappedEmpty(t *testing.T) {
	st := testStore(t)
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

	svc := NewService(st, config.Config{})
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
