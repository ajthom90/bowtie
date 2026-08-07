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

	// Defaults present as raw DB strings too.
	enc, _ := st.GetSetting(settings.KeyTranscodeEncoder)
	rh, _ := st.GetSetting(settings.KeyXMLTVRefreshHours)
	ah, _ := st.GetSetting(settings.KeyTranscodeAllowHEVC)
	if enc != "auto" || rh != "12" || ah != "false" {
		t.Errorf("raw defaults encoder=%q refreshHours=%q allowHevc=%q", enc, rh, ah)
	}
}
