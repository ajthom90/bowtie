package sd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSD is an httptest-backed Schedules Direct stand-in.
type fakeSD struct {
	token           string
	requireToken    bool
	tokenCalls      atomic.Int32
	programsPOSTs   atomic.Int32
	lastSchedBody   []byte
	expiredOnce     bool // first protected call returns TOKEN_EXPIRED once
	expiredReturned atomic.Bool
}

func (f *fakeSD) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Username != "user" || body.Password != sha1Hex("secret") {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"response": "INVALID_USER",
				"code":     4003,
				"message":  "Invalid username or password.",
			})
			return
		}
		f.token = "tok-abc"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":         0,
			"message":      "OK",
			"token":        f.token,
			"tokenExpires": time.Now().Add(24 * time.Hour).Unix(),
		})
	})
	mux.HandleFunc("/lineups/", func(w http.ResponseWriter, r *http.Request) {
		if !f.checkToken(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"map": []map[string]string{
				{"stationID": "20454", "channel": "2.1"},
			},
			"stations": []map[string]any{
				{
					"stationID": "20454",
					"callsign":  "WBBMDT",
					"name":      "WBBM-DT",
					"logo":      map[string]string{"URL": "https://example.com/logo.png"},
				},
			},
		})
	})
	mux.HandleFunc("/schedules", func(w http.ResponseWriter, r *http.Request) {
		if !f.checkToken(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.lastSchedBody = body
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"stationID": "20454",
				"programs": []map[string]any{
					{
						"programID":   "EP0001",
						"airDateTime": "2026-08-04T12:00:00Z",
						"duration":    1800,
					},
				},
			},
		})
	})
	mux.HandleFunc("/programs", func(w http.ResponseWriter, r *http.Request) {
		if !f.checkToken(w, r) {
			return
		}
		f.programsPOSTs.Add(1)
		var ids []string
		if err := json.NewDecoder(r.Body).Decode(&ids); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, map[string]any{
				"programID": id,
				"titles":    []map[string]string{{"title120": "Title " + id}},
				"descriptions": map[string]any{
					"description1000": []map[string]string{
						{"description": "Desc " + id},
					},
				},
				"episodeTitle150": "Ep " + id,
				"genres":          []string{"Drama"},
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}

func (f *fakeSD) checkToken(w http.ResponseWriter, r *http.Request) bool {
	tok := r.Header.Get("token")
	if f.requireToken && tok == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": "TOKEN_MISSING",
			"code":     1004,
			"message":  "Token required but not provided in header or query parameter.",
		})
		return false
	}
	if f.expiredOnce && !f.expiredReturned.Load() && tok != "" {
		f.expiredReturned.Store(true)
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": "TOKEN_EXPIRED",
			"code":     4006,
			"message":  "Token has expired. Request new token.",
		})
		return false
	}
	if f.requireToken && tok != f.token {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": "TOKEN_INVALID",
			"code":     4006,
			"message":  "Token invalid.",
		})
		return false
	}
	return true
}

func newTestClient(t *testing.T, f *fakeSD) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return &Client{
		BaseURL:  srv.URL,
		HTTP:     srv.Client(),
		Username: "user",
		Password: "secret",
	}
}

func TestTokenFlow(t *testing.T) {
	f := &fakeSD{requireToken: true}
	c := newTestClient(t, f)

	// Without token, protected endpoint must fail with 401.
	// Call Lineup before Token — client may auto-token; force empty by
	// using a raw HTTP call first to assert the fake, then client Token.
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/lineups/USA-OTA-00000", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token header, got %d", resp.StatusCode)
	}

	if err := c.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if c.token != "tok-abc" {
		t.Fatalf("token = %q, want tok-abc", c.token)
	}
	if f.tokenCalls.Load() != 1 {
		t.Fatalf("token calls = %d, want 1", f.tokenCalls.Load())
	}

	// Password must be sent as lowercase sha1 hex (verified by fake).
	// Second call should re-use cache if we only call Lineup.
	lu, err := c.Lineup(context.Background(), "USA-OTA-00000")
	if err != nil {
		t.Fatalf("Lineup: %v", err)
	}
	if len(lu.Stations) != 1 || lu.Stations[0].StationID != "20454" {
		t.Fatalf("lineup stations = %+v", lu.Stations)
	}
	if f.tokenCalls.Load() != 1 {
		t.Fatalf("unexpected re-auth: token calls = %d", f.tokenCalls.Load())
	}
}

func TestTokenExpiredRetries(t *testing.T) {
	f := &fakeSD{requireToken: true, expiredOnce: true}
	c := newTestClient(t, f)

	if err := c.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate an already-cached expired token path: force expired on next call.
	// Token was already obtained; Lineup should hit 403/4006 once then re-auth.
	lu, err := c.Lineup(context.Background(), "USA-OTA-00000")
	if err != nil {
		t.Fatalf("Lineup after expiry: %v", err)
	}
	if len(lu.Stations) != 1 {
		t.Fatalf("stations = %+v", lu.Stations)
	}
	if f.tokenCalls.Load() < 2 {
		t.Fatalf("expected re-auth after expiry, token calls = %d", f.tokenCalls.Load())
	}
}

func TestLineupParse(t *testing.T) {
	f := &fakeSD{requireToken: true}
	c := newTestClient(t, f)
	if err := c.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	lu, err := c.Lineup(context.Background(), "USA-OTA-90210")
	if err != nil {
		t.Fatal(err)
	}
	if len(lu.Map) != 1 || lu.Map[0].StationID != "20454" || lu.Map[0].Channel != "2.1" {
		t.Fatalf("map = %+v", lu.Map)
	}
	if len(lu.Stations) != 1 {
		t.Fatalf("stations len = %d", len(lu.Stations))
	}
	st := lu.Stations[0]
	if st.Callsign != "WBBMDT" || st.Name != "WBBM-DT" || st.Logo.URL != "https://example.com/logo.png" {
		t.Fatalf("station = %+v", st)
	}
}

func TestSchedulesRequestBody(t *testing.T) {
	f := &fakeSD{requireToken: true}
	c := newTestClient(t, f)
	if err := c.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	dates := []string{"2026-08-04", "2026-08-05"}
	scheds, err := c.Schedules(context.Background(), []string{"20454", "10021"}, dates)
	if err != nil {
		t.Fatal(err)
	}
	if len(scheds) != 1 || scheds[0].StationID != "20454" {
		t.Fatalf("scheds = %+v", scheds)
	}

	var body []struct {
		StationID string   `json:"stationID"`
		Date      []string `json:"date"`
	}
	if err := json.Unmarshal(f.lastSchedBody, &body); err != nil {
		t.Fatalf("unmarshal body: %v\n%s", err, f.lastSchedBody)
	}
	if len(body) != 2 {
		t.Fatalf("body stations = %d, want 2: %s", len(body), f.lastSchedBody)
	}
	wantIDs := map[string]bool{"20454": true, "10021": true}
	for _, b := range body {
		if !wantIDs[b.StationID] {
			t.Fatalf("unexpected stationID %q", b.StationID)
		}
		if len(b.Date) != 2 || b.Date[0] != "2026-08-04" || b.Date[1] != "2026-08-05" {
			t.Fatalf("dates for %s = %v", b.StationID, b.Date)
		}
	}
}

func TestProgramsBatching(t *testing.T) {
	f := &fakeSD{requireToken: true}
	c := newTestClient(t, f)
	if err := c.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 600)
	for i := range ids {
		ids[i] = fmt.Sprintf("EP%04d", i)
	}
	details, err := c.Programs(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if f.programsPOSTs.Load() != 2 {
		t.Fatalf("programs POSTs = %d, want 2", f.programsPOSTs.Load())
	}
	if len(details) != 600 {
		t.Fatalf("details len = %d, want 600", len(details))
	}
	if details["EP0000"].Titles[0].Title120 != "Title EP0000" {
		t.Fatalf("first detail = %+v", details["EP0000"])
	}
	if details["EP0599"].Titles[0].Title120 != "Title EP0599" {
		t.Fatalf("last detail = %+v", details["EP0599"])
	}
}

func TestToStore(t *testing.T) {
	air := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	lineup := Lineup{
		Map: []struct {
			StationID string `json:"stationID"`
			Channel   string `json:"channel"`
		}{
			{StationID: "20454", Channel: "2.1"},
		},
		Stations: []struct {
			StationID string `json:"stationID"`
			Callsign  string `json:"callsign"`
			Name      string `json:"name"`
			Logo      struct {
				URL string `json:"URL"`
			} `json:"logo"`
		}{
			{
				StationID: "20454",
				Callsign:  "WBBMDT",
				Name:      "WBBM-DT",
			},
		},
	}
	lineup.Stations[0].Logo.URL = "https://example.com/logo.png"

	scheds := []StationSchedule{
		{
			StationID: "20454",
			Programs: []struct {
				ProgramID   string    `json:"programID"`
				AirDateTime time.Time `json:"airDateTime"`
				Duration    int       `json:"duration"`
			}{
				{ProgramID: "EP0001", AirDateTime: air, Duration: 1800},
			},
		},
	}

	d1 := ProgramDetail{
		Titles: []struct {
			Title120 string `json:"title120"`
		}{{Title120: "Blue Bloods"}},
		EpisodeTitle150: "Drawing Dead",
		Genres:          []string{"Crime drama", "Drama"},
	}
	d1.Descriptions.Description1000 = []struct {
		Description string `json:"description"`
	}{{Description: "Frank deals with a case."}}
	details := map[string]ProgramDetail{"EP0001": d1}

	chans, progs := ToStore(lineup, scheds, details)
	if len(chans) != 1 {
		t.Fatalf("chans = %+v", chans)
	}
	ch := chans[0]
	if ch.ID != "sd-20454" || ch.Source != "sd" || ch.DisplayName != "WBBM-DT" ||
		ch.Callsign != "WBBMDT" || ch.IconURL != "https://example.com/logo.png" {
		t.Fatalf("channel = %+v", ch)
	}
	if len(progs) != 1 {
		t.Fatalf("progs = %+v", progs)
	}
	p := progs[0]
	if p.EPGChannelID != "sd-20454" {
		t.Fatalf("EPGChannelID = %q", p.EPGChannelID)
	}
	if !p.Start.Equal(air) {
		t.Fatalf("Start = %v, want %v", p.Start, air)
	}
	wantStop := air.Add(1800 * time.Second)
	if !p.Stop.Equal(wantStop) {
		t.Fatalf("Stop = %v, want %v", p.Stop, wantStop)
	}
	if p.Title != "Blue Bloods" || p.Subtitle != "Drawing Dead" ||
		p.Description != "Frank deals with a case." || p.Category != "Crime drama" {
		t.Fatalf("program fields = %+v", p)
	}
}

func TestProgramsEmpty(t *testing.T) {
	f := &fakeSD{requireToken: true}
	c := newTestClient(t, f)
	details, err := c.Programs(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 0 {
		t.Fatalf("details = %v", details)
	}
	if f.programsPOSTs.Load() != 0 {
		t.Fatalf("unexpected POSTs = %d", f.programsPOSTs.Load())
	}
}
