package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

// testAPIWithTuners builds an API with a real tuner.Manager (UDP discovery suppressed).
func testAPIWithTuners(t *testing.T) (http.Handler, *store.Store, *tuner.Manager) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	m := tuner.New(st, config.Config{})
	m.SetDiscoverFunc(func(ctx context.Context, timeout time.Duration) ([]hdhr.DiscoverInfo, error) {
		return nil, nil
	})
	h := api.New(api.Deps{
		Cfg:    config.Config{ListenAddr: ":0"},
		Store:  st,
		Auth:   a,
		Tuners: m,
	})
	return h, st, m
}

func TestDeviceChannelFlowAndViewerList(t *testing.T) {
	fake := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "FAKEDEV01",
		TunerCount: 2,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "5.1", GuideName: "WABC"},
			{GuideNumber: "7.1", GuideName: "WXYZ"},
		},
	})
	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	// host:port so HTTP hits the fake (not port 80).
	deviceIP := u.Host

	h, st, _ := testAPIWithTuners(t)
	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	// Seed an EPG channel with an icon for mapping later.
	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{
		{ID: "epg-wabc", DisplayName: "WABC", Callsign: "WABC", IconURL: "https://example.com/wabc.png", Source: "xmltv"},
	}, nil); err != nil {
		t.Fatalf("ReplaceEPG: %v", err)
	}

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}

	// Add device by IP → discover + lineup sync.
	rr = doJSON(t, h, "POST", "/api/v1/admin/devices", map[string]string{
		"ip": deviceIP,
	}, adminAuth)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("add device status = %d, body=%q", rr.Code, rr.Body.String())
	}

	// List admin channels — both from lineup, disabled.
	rr = doJSON(t, h, "GET", "/api/v1/admin/channels", nil, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin channels status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var adminChans []struct {
		ID           int64  `json:"id"`
		DeviceID     string `json:"deviceId"`
		GuideNumber  string `json:"guideNumber"`
		Name         string `json:"name"`
		Enabled      bool   `json:"enabled"`
		EPGChannelID string `json:"epgChannelId"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&adminChans); err != nil {
		t.Fatalf("decode admin channels: %v", err)
	}
	if len(adminChans) != 2 {
		t.Fatalf("admin channels len = %d, want 2: %+v", len(adminChans), adminChans)
	}
	var enableID int64
	for _, c := range adminChans {
		if c.GuideNumber == "5.1" {
			enableID = c.ID
			if c.Name != "WABC" || c.Enabled {
				t.Errorf("channel 5.1 = %+v, want WABC disabled", c)
			}
			if c.DeviceID != "FAKEDEV01" {
				t.Errorf("deviceId = %q", c.DeviceID)
			}
		}
	}
	if enableID == 0 {
		t.Fatal("channel 5.1 not found")
	}

	// Enable + map EPG.
	path := "/api/v1/admin/channels/" + strconv.FormatInt(enableID, 10)
	rr = doJSON(t, h, "PATCH", path, map[string]any{
		"enabled":      true,
		"epgChannelId": "epg-wabc",
	}, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("patch channel status = %d, body=%q", rr.Code, rr.Body.String())
	}

	// Viewer list: only enabled, with logoUrl from EPG icon.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	viewerAuth := map[string]string{"Authorization": "Bearer " + viewerTok.AccessToken}

	rr = doJSON(t, h, "GET", "/api/v1/channels", nil, viewerAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("viewer channels status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var viewerChans []struct {
		ID          int64  `json:"id"`
		GuideNumber string `json:"guideNumber"`
		Name        string `json:"name"`
		LogoURL     string `json:"logoUrl"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&viewerChans); err != nil {
		t.Fatalf("decode viewer channels: %v", err)
	}
	if len(viewerChans) != 1 {
		t.Fatalf("viewer channels len = %d, want 1: %+v", len(viewerChans), viewerChans)
	}
	if viewerChans[0].GuideNumber != "5.1" || viewerChans[0].Name != "WABC" {
		t.Errorf("viewer channel = %+v", viewerChans[0])
	}
	if viewerChans[0].LogoURL != "https://example.com/wabc.png" {
		t.Errorf("logoUrl = %q, want EPG icon", viewerChans[0].LogoURL)
	}

	// GET admin/tuners returns the device as reachable.
	rr = doJSON(t, h, "GET", "/api/v1/admin/tuners", nil, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("tuners status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var tuners []struct {
		Device struct {
			DeviceID string `json:"deviceId"`
			IP       string `json:"ip"`
			Manual   bool   `json:"manual"`
		} `json:"device"`
		Reachable bool `json:"reachable"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&tuners); err != nil {
		t.Fatalf("decode tuners: %v", err)
	}
	if len(tuners) != 1 || tuners[0].Device.DeviceID != "FAKEDEV01" || !tuners[0].Reachable || !tuners[0].Device.Manual {
		t.Fatalf("tuners = %+v", tuners)
	}

	// Sync lineup re-fetches (still 2 channels; enabled preserved).
	rr = doJSON(t, h, "POST", "/api/v1/admin/channels/sync", nil, adminAuth)
	if rr.Code != http.StatusOK && rr.Code != http.StatusNoContent {
		t.Fatalf("sync status = %d, body=%q", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h, "GET", "/api/v1/admin/channels", nil, adminAuth)
	if err := json.NewDecoder(rr.Body).Decode(&adminChans); err != nil {
		t.Fatalf("decode after sync: %v", err)
	}
	foundEnabled := false
	for _, c := range adminChans {
		if c.GuideNumber == "5.1" && c.Enabled && c.EPGChannelID == "epg-wabc" {
			foundEnabled = true
		}
	}
	if !foundEnabled {
		t.Fatalf("enabled+mapped channel not preserved after sync: %+v", adminChans)
	}

	// Unmapped enabled channel has empty logoUrl.
	// Enable 7.1 without mapping.
	var otherID int64
	for _, c := range adminChans {
		if c.GuideNumber == "7.1" {
			otherID = c.ID
		}
	}
	rr = doJSON(t, h, "PATCH", "/api/v1/admin/channels/"+strconv.FormatInt(otherID, 10), map[string]any{
		"enabled": true,
	}, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable 7.1: %d %s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h, "GET", "/api/v1/channels", nil, viewerAuth)
	if err := json.NewDecoder(rr.Body).Decode(&viewerChans); err != nil {
		t.Fatalf("decode viewer after enable 7.1: %v", err)
	}
	if len(viewerChans) != 2 {
		t.Fatalf("viewer channels after enable 7.1: %d", len(viewerChans))
	}
	for _, c := range viewerChans {
		if c.GuideNumber == "7.1" && c.LogoURL != "" {
			t.Errorf("unmapped channel logoUrl = %q, want empty", c.LogoURL)
		}
	}

	// DELETE device.
	rr = doJSON(t, h, "DELETE", "/api/v1/admin/devices/FAKEDEV01", nil, adminAuth)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete device status = %d, body=%q", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h, "GET", "/api/v1/admin/tuners", nil, adminAuth)
	if err := json.NewDecoder(rr.Body).Decode(&tuners); err != nil {
		t.Fatalf("decode tuners after delete: %v", err)
	}
	if len(tuners) != 0 {
		t.Fatalf("tuners after delete = %+v", tuners)
	}
}

func TestAddDeviceUnreachable422(t *testing.T) {
	h, st, _ := testAPIWithTuners(t)
	seedUser(t, st, "admin", "adminpass", "admin")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)

	// Closed/unused port — FetchDiscover fails quickly.
	rr = doJSON(t, h, "POST", "/api/v1/admin/devices", map[string]string{
		"ip": "127.0.0.1:1",
	}, map[string]string{"Authorization": "Bearer " + adminTok.AccessToken})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unreachable device status = %d, want 422, body=%q", rr.Code, rr.Body.String())
	}
}

func TestAdminDeviceChannelRoutesForbiddenForViewer(t *testing.T) {
	h, st, _ := testAPIWithTuners(t)
	seedUser(t, st, "viewer", "viewerpass", "viewer")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	tok := decodeLogin(t, rr)
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	checks := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/admin/tuners", nil},
		{"POST", "/api/v1/admin/devices", map[string]string{"ip": "1.2.3.4"}},
		{"DELETE", "/api/v1/admin/devices/x", nil},
		{"POST", "/api/v1/admin/channels/sync", nil},
		{"GET", "/api/v1/admin/channels", nil},
		{"PATCH", "/api/v1/admin/channels/1", map[string]any{"enabled": true}},
	}
	for _, c := range checks {
		rr := doJSON(t, h, c.method, c.path, c.body, authH)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403, body=%q", c.method, c.path, rr.Code, rr.Body.String())
		}
	}
}
