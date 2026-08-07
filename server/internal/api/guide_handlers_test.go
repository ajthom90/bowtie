package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func testAPIWithEPG(t *testing.T, cfg config.Config) (http.Handler, *store.Store, *epg.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	prov := settings.NewProvider(st)
	if err := prov.SeedFromConfig(cfg); err != nil {
		t.Fatalf("SeedFromConfig: %v", err)
	}
	svc := epg.NewService(st, prov)
	h := api.New(api.Deps{
		Cfg:   cfg,
		Store: st,
		Auth:  a,
		EPG:   svc,
	})
	return h, st, svc
}

func TestGuideReturnsEnabledOnly(t *testing.T) {
	h, st, _ := testAPIWithEPG(t, config.Config{})
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	if err := st.UpsertDevice(store.Device{
		DeviceID: "dev1", IP: "1.2.3.4", Model: "X", TunerCount: 1, StreamPort: 5004,
		LastSeen: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("dev1", []store.Channel{
		{DeviceID: "dev1", GuideNumber: "5.1", Name: "WABC"},
		{DeviceID: "dev1", GuideNumber: "7.1", Name: "Off Air"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := st.ListChannels(false)
	if err != nil {
		t.Fatal(err)
	}
	var enableID int64
	for _, c := range chans {
		if c.GuideNumber == "5.1" {
			enableID = c.ID
		}
	}
	start := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	stop := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{
		{ID: "epg-1", DisplayName: "WABC", Callsign: "WABC", IconURL: "https://example.com/i.png", Source: "xmltv"},
	}, []store.Program{
		{EPGChannelID: "epg-1", Start: start, Stop: stop, Title: "Show", Category: "Drama"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateChannel(enableID, true, "epg-1"); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	tok := decodeLogin(t, rr)
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	q := url.Values{}
	q.Set("start", start.Format(time.RFC3339))
	q.Set("stop", stop.Format(time.RFC3339))
	rr = doJSON(t, h, "GET", "/api/v1/guide?"+q.Encode(), nil, authH)
	if rr.Code != http.StatusOK {
		t.Fatalf("guide status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var guide []struct {
		ChannelID   int64  `json:"channelId"`
		GuideNumber string `json:"guideNumber"`
		Name        string `json:"name"`
		LogoURL     string `json:"logoUrl"`
		Programs    []struct {
			Title    string `json:"title"`
			Category string `json:"category"`
		} `json:"programs"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&guide); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(guide) != 1 {
		t.Fatalf("guide len = %d, want 1 enabled: %+v", len(guide), guide)
	}
	if guide[0].GuideNumber != "5.1" || guide[0].ChannelID != enableID {
		t.Errorf("channel = %+v", guide[0])
	}
	if guide[0].LogoURL != "https://example.com/i.png" {
		t.Errorf("logoUrl = %q", guide[0].LogoURL)
	}
	if len(guide[0].Programs) != 1 || guide[0].Programs[0].Title != "Show" {
		t.Errorf("programs = %+v", guide[0].Programs)
	}

	// Unauthenticated → 401.
	rr = doJSON(t, h, "GET", "/api/v1/guide?"+q.Encode(), nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauth status = %d, want 401", rr.Code)
	}
}

func TestGuideDefaultsAndSpanLimit(t *testing.T) {
	h, st, _ := testAPIWithEPG(t, config.Config{})
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	tok := decodeLogin(t, rr)
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	// Defaults (no query) → 200 empty guide.
	rr = doJSON(t, h, "GET", "/api/v1/guide", nil, authH)
	if rr.Code != http.StatusOK {
		t.Fatalf("default guide status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var guide []any
	if err := json.NewDecoder(rr.Body).Decode(&guide); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if guide == nil {
		t.Error("expected non-null array")
	}

	// Span > 24h → 422.
	start := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	stop := start.Add(25 * time.Hour)
	q := url.Values{}
	q.Set("start", start.Format(time.RFC3339))
	q.Set("stop", stop.Format(time.RFC3339))
	rr = doJSON(t, h, "GET", "/api/v1/guide?"+q.Encode(), nil, authH)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("span>24h status = %d, want 422, body=%q", rr.Code, rr.Body.String())
	}

	// Exactly 24h is allowed.
	stop24 := start.Add(24 * time.Hour)
	q.Set("stop", stop24.Format(time.RFC3339))
	rr = doJSON(t, h, "GET", "/api/v1/guide?"+q.Encode(), nil, authH)
	if rr.Code != http.StatusOK {
		t.Fatalf("span=24h status = %d, body=%q", rr.Code, rr.Body.String())
	}

	// Bad RFC3339 → 400.
	rr = doJSON(t, h, "GET", "/api/v1/guide?start=not-a-time", nil, authH)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad start status = %d, want 400", rr.Code)
	}
}

func TestAdminEPGStatusAndChannels(t *testing.T) {
	cfg := config.Config{}
	cfg.XMLTV.Source = "/tmp/guide.xml"
	cfg.XMLTV.RefreshHours = 12
	h, st, _ := testAPIWithEPG(t, cfg)
	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{
		{ID: "ch-a", DisplayName: "A", Callsign: "A", IconURL: "http://x/a.png", Source: "xmltv"},
	}, nil); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}

	rr = doJSON(t, h, "GET", "/api/v1/admin/epg/status", nil, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var status struct {
		XMLTV struct {
			Configured bool `json:"configured"`
			Stale      bool `json:"stale"`
		} `json:"xmltv"`
		SD struct {
			Configured bool `json:"configured"`
		} `json:"sd"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.XMLTV.Configured {
		t.Error("xmltv should be configured")
	}
	if status.SD.Configured {
		t.Error("sd should not be configured")
	}

	rr = doJSON(t, h, "GET", "/api/v1/admin/epg/channels", nil, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("channels status = %d", rr.Code)
	}
	var epgs []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		Source      string `json:"source"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&epgs); err != nil {
		t.Fatal(err)
	}
	if len(epgs) != 1 || epgs[0].ID != "ch-a" {
		t.Fatalf("epg channels = %+v", epgs)
	}

	// Refresh returns 202.
	rr = doJSON(t, h, "POST", "/api/v1/admin/epg/refresh", nil, adminAuth)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("refresh status = %d, want 202", rr.Code)
	}

	// Viewer forbidden.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	viewerAuth := map[string]string{"Authorization": "Bearer " + viewerTok.AccessToken}
	rr = doJSON(t, h, "GET", "/api/v1/admin/epg/status", nil, viewerAuth)
	if rr.Code != http.StatusForbidden {
		t.Errorf("viewer status = %d, want 403", rr.Code)
	}
}
