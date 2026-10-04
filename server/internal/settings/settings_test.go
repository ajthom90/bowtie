package settings_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func openProvider(t *testing.T) (*settings.Provider, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return settings.NewProvider(st), st
}

// TestSeedOnlyWhenAbsent is the disable-survives-restart scenario from the spec:
// seed from config with an XMLTV source, clear it via SetXMLTV, seed again — the
// empty value must stay (presence-based seeding never re-seeds a present key).
func TestSeedOnlyWhenAbsent(t *testing.T) {
	p, st := openProvider(t)

	cfg := config.Config{}
	cfg.XMLTV.Source = "http://example.com/guide.xml"
	cfg.XMLTV.RefreshHours = 6
	cfg.Encoder = "software"
	cfg.AllowHEVC = true

	if err := p.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig first: %v", err)
	}
	xmltv, err := p.XMLTV()
	if err != nil {
		t.Fatalf("XMLTV after seed: %v", err)
	}
	if xmltv.Source != "http://example.com/guide.xml" {
		t.Fatalf("Source after seed = %q", xmltv.Source)
	}
	if xmltv.RefreshHours != 6 {
		t.Fatalf("RefreshHours after seed = %d, want 6", xmltv.RefreshHours)
	}

	// UI disable: store empty source (presence retained).
	if err := p.SetXMLTV(settings.XMLTV{Source: "", RefreshHours: 6}); err != nil {
		t.Fatalf("SetXMLTV clear: %v", err)
	}
	has, err := st.HasSetting(settings.KeyXMLTVSource)
	if err != nil {
		t.Fatalf("HasSetting: %v", err)
	}
	if !has {
		t.Fatal("after clear, HasSetting(xmltv.source) = false; empty must remain present")
	}

	// Restart re-seeds from the same config — must NOT restore the source.
	if err := p.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig second: %v", err)
	}
	xmltv, err = p.XMLTV()
	if err != nil {
		t.Fatalf("XMLTV after second seed: %v", err)
	}
	if xmltv.Source != "" {
		t.Fatalf("disable-survives-restart: Source = %q, want \"\" (must not re-seed present key)", xmltv.Source)
	}
	if xmltv.RefreshHours != 6 {
		t.Fatalf("RefreshHours after second seed = %d, want 6", xmltv.RefreshHours)
	}
}

func TestTypedRoundTrips(t *testing.T) {
	p, _ := openProvider(t)

	if err := p.SetXMLTV(settings.XMLTV{
		Source:       "/var/lib/bowtie/guide.xml",
		RefreshHours: 24,
	}); err != nil {
		t.Fatalf("SetXMLTV: %v", err)
	}
	if err := p.SetSD(settings.SD{
		Username: "alice",
		Password: "s3cret",
		LineupID: "USA-NY12345-X",
	}); err != nil {
		t.Fatalf("SetSD: %v", err)
	}
	if err := p.SetTranscode(settings.Transcode{
		Encoder:   "videotoolbox",
		AllowHEVC: true,
	}); err != nil {
		t.Fatalf("SetTranscode: %v", err)
	}
	if err := p.SetStreaming(settings.Streaming{BufferMinutes: 30}); err != nil {
		t.Fatalf("SetStreaming: %v", err)
	}

	xmltv, err := p.XMLTV()
	if err != nil {
		t.Fatalf("XMLTV: %v", err)
	}
	if xmltv.Source != "/var/lib/bowtie/guide.xml" || xmltv.RefreshHours != 24 {
		t.Errorf("XMLTV = %+v", xmltv)
	}

	sd, err := p.SD()
	if err != nil {
		t.Fatalf("SD: %v", err)
	}
	if sd.Username != "alice" || sd.Password != "s3cret" || sd.LineupID != "USA-NY12345-X" {
		t.Errorf("SD = %+v", sd)
	}

	tc, err := p.Transcode()
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	if tc.Encoder != "videotoolbox" || !tc.AllowHEVC {
		t.Errorf("Transcode = %+v", tc)
	}

	st, err := p.Streaming()
	if err != nil {
		t.Fatalf("Streaming: %v", err)
	}
	if st.BufferMinutes != 30 {
		t.Errorf("Streaming.BufferMinutes = %d, want 30", st.BufferMinutes)
	}

	// Bool false and int round-trip
	if err := p.SetTranscode(settings.Transcode{Encoder: "auto", AllowHEVC: false}); err != nil {
		t.Fatalf("SetTranscode false: %v", err)
	}
	tc, err = p.Transcode()
	if err != nil {
		t.Fatalf("Transcode after false: %v", err)
	}
	if tc.AllowHEVC {
		t.Error("AllowHEVC = true, want false")
	}
	if err := p.SetXMLTV(settings.XMLTV{Source: "x", RefreshHours: 1}); err != nil {
		t.Fatalf("SetXMLTV 1h: %v", err)
	}
	xmltv, err = p.XMLTV()
	if err != nil {
		t.Fatalf("XMLTV 1h: %v", err)
	}
	if xmltv.RefreshHours != 1 {
		t.Errorf("RefreshHours = %d, want 1", xmltv.RefreshHours)
	}
	if err := p.SetStreaming(settings.Streaming{BufferMinutes: 2}); err != nil {
		t.Fatalf("SetStreaming 2: %v", err)
	}
	st, err = p.Streaming()
	if err != nil {
		t.Fatalf("Streaming 2: %v", err)
	}
	if st.BufferMinutes != 2 {
		t.Errorf("BufferMinutes = %d, want 2", st.BufferMinutes)
	}
}

func TestDefaultsSeeded(t *testing.T) {
	p, st := openProvider(t)

	// Empty config: product keys still get documented defaults.
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatalf("SeedFromConfig: %v", err)
	}

	for _, key := range []string{
		settings.KeyXMLTVSource,
		settings.KeyXMLTVRefreshHours,
		settings.KeySDUsername,
		settings.KeySDPassword,
		settings.KeySDLineupID,
		settings.KeyTranscodeEncoder,
		settings.KeyTranscodeAllowHEVC,
		settings.KeyStreamingBufferMinutes,
		settings.KeyDVRPadStartSeconds,
		settings.KeyDVRPadEndSeconds,
	} {
		has, err := st.HasSetting(key)
		if err != nil {
			t.Fatalf("HasSetting %s: %v", key, err)
		}
		if !has {
			t.Errorf("after empty-cfg seed, key %q absent", key)
		}
	}

	xmltv, err := p.XMLTV()
	if err != nil {
		t.Fatalf("XMLTV: %v", err)
	}
	if xmltv.Source != "" {
		t.Errorf("Source = %q, want empty", xmltv.Source)
	}
	if xmltv.RefreshHours != 12 {
		t.Errorf("RefreshHours = %d, want 12", xmltv.RefreshHours)
	}

	tc, err := p.Transcode()
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	if tc.Encoder != "auto" {
		t.Errorf("Encoder = %q, want auto", tc.Encoder)
	}
	if tc.AllowHEVC {
		t.Error("AllowHEVC = true, want false")
	}

	stream, err := p.Streaming()
	if err != nil {
		t.Fatalf("Streaming: %v", err)
	}
	if stream.BufferMinutes != 15 {
		t.Errorf("BufferMinutes = %d, want 15", stream.BufferMinutes)
	}

	// Defaults present as raw DB strings too.
	enc, _ := st.GetSetting(settings.KeyTranscodeEncoder)
	rh, _ := st.GetSetting(settings.KeyXMLTVRefreshHours)
	ah, _ := st.GetSetting(settings.KeyTranscodeAllowHEVC)
	bm, _ := st.GetSetting(settings.KeyStreamingBufferMinutes)
	if enc != "auto" || rh != "12" || ah != "false" || bm != "15" {
		t.Errorf("raw defaults encoder=%q refreshHours=%q allowHevc=%q bufferMinutes=%q", enc, rh, ah, bm)
	}
}

// TestStreamingRoundTripAndSeedDefault covers Streaming()/SetStreaming and the
// presence-seeded default of 15 for streaming.bufferMinutes.
func TestStreamingRoundTripAndSeedDefault(t *testing.T) {
	p, st := openProvider(t)

	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatalf("SeedFromConfig: %v", err)
	}
	s, err := p.Streaming()
	if err != nil {
		t.Fatalf("Streaming after seed: %v", err)
	}
	if s.BufferMinutes != settings.DefaultBufferMinutes {
		t.Fatalf("BufferMinutes after seed = %d, want %d", s.BufferMinutes, settings.DefaultBufferMinutes)
	}

	if err := p.SetStreaming(settings.Streaming{BufferMinutes: 45}); err != nil {
		t.Fatalf("SetStreaming: %v", err)
	}
	s, err = p.Streaming()
	if err != nil {
		t.Fatalf("Streaming after set: %v", err)
	}
	if s.BufferMinutes != 45 {
		t.Fatalf("BufferMinutes = %d, want 45", s.BufferMinutes)
	}

	// Presence seed must not overwrite deliberate value.
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatalf("SeedFromConfig second: %v", err)
	}
	s, err = p.Streaming()
	if err != nil {
		t.Fatalf("Streaming after re-seed: %v", err)
	}
	if s.BufferMinutes != 45 {
		t.Fatalf("re-seed overwrote BufferMinutes = %d, want 45", s.BufferMinutes)
	}
	raw, _ := st.GetSetting(settings.KeyStreamingBufferMinutes)
	if raw != "45" {
		t.Fatalf("raw bufferMinutes = %q, want 45", raw)
	}
}

// streaming.adaptive (shared ladder) is seeded off and round-trips.
func TestStreamingAdaptiveSeededOffAndRoundTrips(t *testing.T) {
	p, st := openProvider(t)
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatalf("SeedFromConfig: %v", err)
	}
	s, err := p.Streaming()
	if err != nil {
		t.Fatal(err)
	}
	if s.Adaptive {
		t.Fatal("adaptive must default to false")
	}
	if raw, _ := st.GetSetting(settings.KeyStreamingAdaptive); raw != "false" {
		t.Fatalf("raw seed = %q, want false", raw)
	}
	if err := p.SetStreaming(settings.Streaming{BufferMinutes: 15, Adaptive: true}); err != nil {
		t.Fatal(err)
	}
	s, err = p.Streaming()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Adaptive || s.BufferMinutes != 15 {
		t.Fatalf("round trip = %+v", s)
	}
	if raw, _ := st.GetSetting(settings.KeyStreamingAdaptive); raw != "true" {
		t.Fatalf("raw = %q, want true", raw)
	}
}

// The free HDHomeRun guide is on by default (absent key and first-boot seed)
// and can be turned off.
func TestHDHomeRunGuideDefaultOnAndRoundTrips(t *testing.T) {
	p, st := openProvider(t)
	if g, err := p.HDHomeRunGuide(); err != nil || !g.Enabled {
		t.Fatalf("absent key = %+v err=%v, want enabled", g, err)
	}
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := st.GetSetting(settings.KeyEPGHDHomeRun); raw != "true" {
		t.Fatalf("raw seed = %q, want true", raw)
	}
	if err := p.SetHDHomeRunGuide(settings.HDHomeRunGuide{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if g, err := p.HDHomeRunGuide(); err != nil || g.Enabled {
		t.Fatalf("after off = %+v err=%v", g, err)
	}
	// A restart's re-seed never turns it back on.
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatal(err)
	}
	if g, _ := p.HDHomeRunGuide(); g.Enabled {
		t.Fatal("re-seed must not re-enable")
	}
}

// DVR padding: absent keys read as the defaults, 0 is a real value, and the
// first-boot seed writes the defaults.
func TestDVRPaddingDefaultsAndRoundTrips(t *testing.T) {
	p, st := openProvider(t)
	d, err := p.DVR()
	if err != nil || d.PadStartSeconds != 60 || d.PadEndSeconds != 180 {
		t.Fatalf("absent keys = %+v err=%v, want 60/180", d, err)
	}
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := st.GetSetting(settings.KeyDVRPadStartSeconds); raw != "60" {
		t.Fatalf("raw padStart seed = %q, want 60", raw)
	}
	if raw, _ := st.GetSetting(settings.KeyDVRPadEndSeconds); raw != "180" {
		t.Fatalf("raw padEnd seed = %q, want 180", raw)
	}
	if err := p.SetDVR(settings.DVR{PadStartSeconds: 0, PadEndSeconds: 0}); err != nil {
		t.Fatal(err)
	}
	if d, err := p.DVR(); err != nil || d.PadStartSeconds != 0 || d.PadEndSeconds != 0 {
		t.Fatalf("zero round trip = %+v err=%v", d, err)
	}
	if err := p.SetDVR(settings.DVR{PadStartSeconds: 120, PadEndSeconds: 600}); err != nil {
		t.Fatal(err)
	}
	if err := p.SeedFromConfig(config.Config{}); err != nil {
		t.Fatal(err)
	}
	if d, err := p.DVR(); err != nil || d.PadStartSeconds != 120 || d.PadEndSeconds != 600 {
		t.Fatalf("round trip after re-seed = %+v err=%v", d, err)
	}
}

func TestDVRPaddingDurations(t *testing.T) {
	p, _ := openProvider(t)
	if err := p.SetDVR(settings.DVR{PadStartSeconds: 90, PadEndSeconds: 600}); err != nil {
		t.Fatal(err)
	}
	start, end, err := p.DVRPadding()
	if err != nil || start != 90*time.Second || end != 10*time.Minute {
		t.Fatalf("DVRPadding = %v %v %v", start, end, err)
	}
}
