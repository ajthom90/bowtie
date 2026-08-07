package api_test

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func testAPIWithSettings(t *testing.T, sdBase string, sdHTTP *http.Client) (http.Handler, *store.Store, *settings.Provider) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prov := settings.NewProvider(st)
	if err := prov.SeedFromConfig(config.Config{}); err != nil {
		t.Fatalf("SeedFromConfig: %v", err)
	}

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	h := api.New(api.Deps{
		Cfg:   config.Config{ListenAddr: ":0", Encoder: "auto"},
		Store: st,
		Auth:  a,
		Probe: func() transcode.Capabilities {
			return transcode.Capabilities{
				Available: []transcode.Backend{
					transcode.BackendSoftware,
					transcode.BackendVideoToolbox,
				},
				HEVC: map[transcode.Backend]bool{
					transcode.BackendSoftware:     false,
					transcode.BackendVideoToolbox: true,
				},
				FFmpegVersion: "test-8.0",
			}
		},
		Settings:  prov,
		SDBaseURL: sdBase,
		SDHTTP:    sdHTTP,
	})
	return h, st, prov
}

func adminAuth(t *testing.T, h http.Handler, st *store.Store) string {
	t.Helper()
	seedUser(t, st, "admin", "adminpass", "admin")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin login status = %d body=%q", rr.Code, rr.Body.String())
	}
	return decodeLogin(t, rr).AccessToken
}

func authHeader(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func decodeSettings(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode settings: %v body=%q", err, rr.Body.String())
	}
	return out
}

func section(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

// --- GET shape ---

func TestGetSettingsShapePasswordConfigured(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	// No SD password yet.
	rr := doJSON(t, h, "GET", "/api/v1/admin/settings", nil, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%q", rr.Code, rr.Body.String())
	}
	body := decodeSettings(t, rr)
	raw := rr.Body.String()
	if strings.Contains(raw, `"password"`) && !strings.Contains(raw, `"passwordConfigured"`) {
		t.Fatalf("response must never include password field: %s", raw)
	}
	if strings.Contains(raw, `"password":`) {
		t.Fatalf("response must never return password: %s", raw)
	}
	sdSec := section(body, "schedulesDirect")
	if sdSec["passwordConfigured"] != false {
		t.Fatalf("passwordConfigured = %v, want false", sdSec["passwordConfigured"])
	}
	// Defaults from seed.
	xmltv := section(body, "xmltv")
	if xmltv["source"] != "" {
		t.Fatalf("xmltv.source = %v", xmltv["source"])
	}
	if int(xmltv["refreshHours"].(float64)) != 12 {
		t.Fatalf("refreshHours = %v, want 12", xmltv["refreshHours"])
	}
	tc := section(body, "transcode")
	if tc["encoder"] != "auto" {
		t.Fatalf("encoder = %v", tc["encoder"])
	}
	if tc["allowHevc"] != false {
		t.Fatalf("allowHevc = %v", tc["allowHevc"])
	}
	avail, _ := tc["available"].([]any)
	if len(avail) != 2 {
		t.Fatalf("available = %v (from injected probe)", avail)
	}
	hevc, _ := tc["hevcCapable"].(map[string]any)
	if hevc["videotoolbox"] != true || hevc["software"] != false {
		t.Fatalf("hevcCapable = %v", hevc)
	}
	streamSec := section(body, "streaming")
	if int(streamSec["bufferMinutes"].(float64)) != 15 {
		t.Fatalf("streaming.bufferMinutes = %v, want 15 (seed default)", streamSec["bufferMinutes"])
	}

	// Set SD password → passwordConfigured true; still no password field.
	if err := prov.SetSD(settings.SD{Username: "u", Password: "secret", LineupID: "L1"}); err != nil {
		t.Fatal(err)
	}
	rr = doJSON(t, h, "GET", "/api/v1/admin/settings", nil, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET after set status = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), `"password":`) {
		t.Fatalf("password leaked: %s", rr.Body.String())
	}
	sdSec = section(decodeSettings(t, rr), "schedulesDirect")
	if sdSec["passwordConfigured"] != true {
		t.Fatalf("passwordConfigured = %v, want true", sdSec["passwordConfigured"])
	}
	if sdSec["username"] != "u" || sdSec["lineupId"] != "L1" {
		t.Fatalf("sd section = %v", sdSec)
	}
}

func TestGetSettingsViewerForbidden(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	seedUser(t, st, "viewer", "viewerpass", "viewer")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer", "password": "viewerpass",
	}, nil)
	vtok := decodeLogin(t, rr).AccessToken
	rr = doJSON(t, h, "GET", "/api/v1/admin/settings", nil, authHeader(vtok))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer GET status = %d, want 403", rr.Code)
	}
}

// --- PUT section-merge ---

func TestPutSettingsTranscodeOnlyLeavesSDUntouched(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "sduser", Password: "sdpass", LineupID: "LINE-1"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"transcode": map[string]any{"encoder": "software", "allowHevc": true},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	body := decodeSettings(t, rr)
	tc := section(body, "transcode")
	if tc["encoder"] != "software" || tc["allowHevc"] != true {
		t.Fatalf("transcode = %v", tc)
	}
	sdSec := section(body, "schedulesDirect")
	if sdSec["username"] != "sduser" || sdSec["lineupId"] != "LINE-1" || sdSec["passwordConfigured"] != true {
		t.Fatalf("SD must be untouched: %v", sdSec)
	}
	got, err := prov.SD()
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "sdpass" {
		t.Fatalf("SD password changed: %q", got.Password)
	}
}

func TestPutSettingsXMLTVOnlyLeavesTranscodeUntouched(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetTranscode(settings.Transcode{Encoder: "software", AllowHEVC: true}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"xmltv": map[string]any{"source": "https://example.com/guide.xml", "refreshHours": 6},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	body := decodeSettings(t, rr)
	xmltv := section(body, "xmltv")
	if xmltv["source"] != "https://example.com/guide.xml" || int(xmltv["refreshHours"].(float64)) != 6 {
		t.Fatalf("xmltv = %v", xmltv)
	}
	tc := section(body, "transcode")
	if tc["encoder"] != "software" || tc["allowHevc"] != true {
		t.Fatalf("transcode must be untouched: %v", tc)
	}
}

// --- Password semantics ---

func TestPutSettingsPasswordEmptyKeeps(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "u", Password: "original", LineupID: "L1"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"schedulesDirect": map[string]any{
			"username": "u2",
			"password": "",
			"lineupId": "L2",
		},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	got, err := prov.SD()
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "u2" || got.LineupID != "L2" {
		t.Fatalf("SD = %+v", got)
	}
	if got.Password != "original" {
		t.Fatalf("empty password must keep existing; got %q", got.Password)
	}
	body := decodeSettings(t, rr)
	if section(body, "schedulesDirect")["passwordConfigured"] != true {
		t.Fatal("passwordConfigured should still be true")
	}
}

func TestPutSettingsPasswordNewReplaces(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "u", Password: "original", LineupID: "L1"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"schedulesDirect": map[string]any{
			"username": "u",
			"password": "brand-new",
			"lineupId": "L1",
		},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	got, _ := prov.SD()
	if got.Password != "brand-new" {
		t.Fatalf("password = %q, want brand-new", got.Password)
	}
}

func TestPutSettingsEmptyUsernameClearsSDTrio(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "u", Password: "secret", LineupID: "L1"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"schedulesDirect": map[string]any{
			"username": "",
			"password": "ignored-when-clearing",
			"lineupId": "should-clear",
		},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	got, _ := prov.SD()
	if got.Username != "" || got.Password != "" || got.LineupID != "" {
		t.Fatalf("empty username must clear trio; got %+v", got)
	}
	sdSec := section(decodeSettings(t, rr), "schedulesDirect")
	if sdSec["passwordConfigured"] != false || sdSec["username"] != "" || sdSec["lineupId"] != "" {
		t.Fatalf("GET shape after clear = %v", sdSec)
	}
}

// --- Validation ---

func TestPutSettingsValidationRefreshHours(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	for _, hours := range []int{0, -1, 169} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"xmltv": map[string]any{"source": "", "refreshHours": hours},
		}, authHeader(tok))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("refreshHours=%d status = %d, want 400 body=%q", hours, rr.Code, rr.Body.String())
		}
	}
	// Bounds OK.
	for _, hours := range []int{1, 168} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"xmltv": map[string]any{"source": "", "refreshHours": hours},
		}, authHeader(tok))
		if rr.Code != http.StatusOK {
			t.Fatalf("refreshHours=%d status = %d body=%q", hours, rr.Code, rr.Body.String())
		}
	}
}

func TestPutSettingsValidationXMLTVSource(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	// Invalid: relative path, bare host, ftp.
	for _, src := range []string{"relative/guide.xml", "ftp://example.com/x", "not-a-url"} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"xmltv": map[string]any{"source": src, "refreshHours": 12},
		}, authHeader(tok))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("source %q status = %d, want 400 body=%q", src, rr.Code, rr.Body.String())
		}
	}
	// Valid: empty, http(s), absolute path.
	for _, src := range []string{"", "http://example.com/g.xml", "https://example.com/g.xml", "/var/lib/bowtie/guide.xml"} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"xmltv": map[string]any{"source": src, "refreshHours": 12},
		}, authHeader(tok))
		if rr.Code != http.StatusOK {
			t.Fatalf("source %q status = %d body=%q", src, rr.Code, rr.Body.String())
		}
	}
}

func TestPutSettingsValidationEncoder(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"transcode": map[string]any{"encoder": "nvenc", "allowHevc": false},
	}, authHeader(tok))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unavailable encoder status = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}

	for _, enc := range []string{"auto", "software", "videotoolbox"} {
		rr = doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"transcode": map[string]any{"encoder": enc, "allowHevc": false},
		}, authHeader(tok))
		if rr.Code != http.StatusOK {
			t.Fatalf("encoder %q status = %d body=%q", enc, rr.Code, rr.Body.String())
		}
	}
}

// --- streaming section (v0.5.0 Task 2) ---

func TestPutSettingsStreamingOnlyLeavesOtherSectionsUntouched(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetTranscode(settings.Transcode{Encoder: "software", AllowHEVC: true}); err != nil {
		t.Fatal(err)
	}
	if err := prov.SetXMLTV(settings.XMLTV{Source: "https://keep.example/g.xml", RefreshHours: 6}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"streaming": map[string]any{"bufferMinutes": 30},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	body := decodeSettings(t, rr)
	streamSec := section(body, "streaming")
	if int(streamSec["bufferMinutes"].(float64)) != 30 {
		t.Fatalf("streaming = %v", streamSec)
	}
	tc := section(body, "transcode")
	if tc["encoder"] != "software" || tc["allowHevc"] != true {
		t.Fatalf("transcode touched: %v", tc)
	}
	xmltv := section(body, "xmltv")
	if xmltv["source"] != "https://keep.example/g.xml" || int(xmltv["refreshHours"].(float64)) != 6 {
		t.Fatalf("xmltv touched: %v", xmltv)
	}
}

func TestPutSettingsStreamingOmittedRoundTrip(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if err := prov.SetStreaming(settings.Streaming{BufferMinutes: 45}); err != nil {
		t.Fatal(err)
	}

	// PUT without streaming section must leave bufferMinutes alone.
	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"transcode": map[string]any{"encoder": "auto", "allowHevc": false},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	body := decodeSettings(t, rr)
	streamSec := section(body, "streaming")
	if int(streamSec["bufferMinutes"].(float64)) != 45 {
		t.Fatalf("omitted streaming section changed bufferMinutes to %v", streamSec["bufferMinutes"])
	}
	// GET always returns streaming.
	rr = doJSON(t, h, "GET", "/api/v1/admin/settings", nil, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}
	streamSec = section(decodeSettings(t, rr), "streaming")
	if int(streamSec["bufferMinutes"].(float64)) != 45 {
		t.Fatalf("GET streaming = %v", streamSec)
	}
}

func TestPutSettingsValidationBufferMinutes(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	for _, mins := range []int{0, 1, 61, -5} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"streaming": map[string]any{"bufferMinutes": mins},
		}, authHeader(tok))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("bufferMinutes=%d status = %d, want 400 body=%q", mins, rr.Code, rr.Body.String())
		}
	}
	for _, mins := range []int{2, 15, 60} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"streaming": map[string]any{"bufferMinutes": mins},
		}, authHeader(tok))
		if rr.Code != http.StatusOK {
			t.Fatalf("bufferMinutes=%d status = %d body=%q", mins, rr.Code, rr.Body.String())
		}
		body := decodeSettings(t, rr)
		if int(section(body, "streaming")["bufferMinutes"].(float64)) != mins {
			t.Fatalf("bufferMinutes=%d not reflected in response", mins)
		}
	}
}

// --- A3: validate-all-then-single-transaction; nothing written on partial invalid ---

func TestPutSettingsNothingWrittenOnPartialInvalid(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	// Establish known baseline.
	if err := prov.SetXMLTV(settings.XMLTV{Source: "https://old.example/guide.xml", RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}
	if err := prov.SetTranscode(settings.Transcode{Encoder: "auto", AllowHEVC: false}); err != nil {
		t.Fatal(err)
	}

	// Valid xmltv + invalid encoder in one PUT → nothing written.
	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"xmltv":     map[string]any{"source": "https://new.example/guide.xml", "refreshHours": 3},
		"transcode": map[string]any{"encoder": "not-a-backend", "allowHevc": true},
	}, authHeader(tok))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}

	xmltv, err := prov.XMLTV()
	if err != nil {
		t.Fatal(err)
	}
	if xmltv.Source != "https://old.example/guide.xml" || xmltv.RefreshHours != 12 {
		t.Fatalf("xmltv partially applied: %+v (A3: nothing must be written)", xmltv)
	}
	tc, err := prov.Transcode()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Encoder != "auto" || tc.AllowHEVC {
		t.Fatalf("transcode partially applied: %+v", tc)
	}
}

// --- Lineups error table (A2) ---

// fakeSDAPI is a minimal Schedules Direct stand-in for lineups handler tests.
type fakeSDAPI struct {
	// mode: "ok", "badcreds4003", "token200reject", "down"
	mode string
}

func (f *fakeSDAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sum := sha1.Sum([]byte("secret"))
		wantPass := hex.EncodeToString(sum[:])

		switch f.mode {
		case "down":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("maintenance"))
			return
		case "badcreds4003":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response": "INVALID_USER",
				"code":     4003,
				"message":  "Invalid username or password.",
			})
			return
		case "token200reject":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    4003,
				"message": "Invalid username or password.",
			})
			return
		default:
			if body.Username != "user" || body.Password != wantPass {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"response": "INVALID_USER",
					"code":     4003,
					"message":  "Invalid username or password.",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    0,
				"message": "OK",
				"token":   "tok-test",
			})
		}
	})
	mux.HandleFunc("/lineups", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.mode == "down" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"lineups": []map[string]string{
				{
					"lineup":    "USA-OTA-60030",
					"name":      "Local Over the Air Broadcast",
					"transport": "Antenna",
					"location":  "60030",
				},
			},
		})
	})
	return mux
}

func startFakeSD(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	f := &fakeSDAPI{mode: mode}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestLineupsNoCreds422(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	rr := doJSON(t, h, "GET", "/api/v1/admin/epg/lineups", nil, authHeader(tok))
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["error"] != "schedules direct credentials not configured" {
		t.Fatalf("error = %q", body["error"])
	}
}

func TestLineupsAuthClass4003MapsTo401(t *testing.T) {
	// As-built fake SD: HTTP 400 + code 4003 INVALID_USER → admin API 401.
	srv := startFakeSD(t, "badcreds4003")
	h, st, prov := testAPIWithSettings(t, srv.URL, srv.Client())
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "bad", Password: "wrong", LineupID: "L"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "GET", "/api/v1/admin/epg/lineups", nil, authHeader(tok))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["error"] != "schedules direct rejected the credentials" {
		t.Fatalf("error = %q", body["error"])
	}
}

func TestLineupsSDDown502(t *testing.T) {
	srv := startFakeSD(t, "down")
	h, st, prov := testAPIWithSettings(t, srv.URL, srv.Client())
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "user", Password: "secret", LineupID: "L"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "GET", "/api/v1/admin/epg/lineups", nil, authHeader(tok))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["error"] != "schedules direct is unreachable" {
		t.Fatalf("error = %q", body["error"])
	}
	if strings.Contains(rr.Body.String(), "secret") {
		t.Fatal("must never echo secrets")
	}
}

func TestLineupsSuccess(t *testing.T) {
	srv := startFakeSD(t, "ok")
	h, st, prov := testAPIWithSettings(t, srv.URL, srv.Client())
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "user", Password: "secret", LineupID: "USA-OTA-60030"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "GET", "/api/v1/admin/epg/lineups", nil, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rr.Code, rr.Body.String())
	}
	var list []struct {
		LineupID  string `json:"lineupId"`
		Name      string `json:"name"`
		Location  string `json:"location"`
		Transport string `json:"transport"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].LineupID != "USA-OTA-60030" || list[0].Transport != "Antenna" {
		t.Fatalf("list = %+v", list)
	}
}

func TestLineupsTokenHTTP200NonzeroCodeMapsTo401(t *testing.T) {
	// Creds present but SD returns HTTP 200 with nonzero code → auth-class → 401.
	srv := startFakeSD(t, "token200reject")
	h, st, prov := testAPIWithSettings(t, srv.URL, srv.Client())
	tok := adminAuth(t, h, st)
	if err := prov.SetSD(settings.SD{Username: "user", Password: "secret", LineupID: "L"}); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, h, "GET", "/api/v1/admin/epg/lineups", nil, authHeader(tok))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body=%q", rr.Code, rr.Body.String())
	}
}
