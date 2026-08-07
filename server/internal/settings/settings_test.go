package settings_test

import (
	"path/filepath"
	"testing"

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
