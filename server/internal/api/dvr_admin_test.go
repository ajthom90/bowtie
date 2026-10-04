package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestGetSettingsIncludesDVRDefaults(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	m := decodeSettings(t, doJSON(t, h, "GET", "/api/v1/admin/settings", nil, authHeader(tok)))
	d := section(m, "dvr")
	if d["padStartSeconds"] != float64(60) || d["padEndSeconds"] != float64(180) {
		t.Fatalf("dvr = %v, want 60/180", d)
	}
}

func TestPutSettingsDVR(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"dvr": map[string]any{"padStartSeconds": 0, "padEndSeconds": 600},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rr.Code, rr.Body.String())
	}
	if d := section(decodeSettings(t, rr), "dvr"); d["padStartSeconds"] != float64(0) || d["padEndSeconds"] != float64(600) {
		t.Fatalf("response dvr = %v", d)
	}
	if d, _ := prov.DVR(); d.PadStartSeconds != 0 || d.PadEndSeconds != 600 {
		t.Fatalf("stored = %+v", d)
	}

	for name, body := range map[string]map[string]any{
		"missing end":    {"padStartSeconds": 30},
		"missing start":  {"padEndSeconds": 30},
		"start too big":  {"padStartSeconds": 1801, "padEndSeconds": 30},
		"end too big":    {"padStartSeconds": 30, "padEndSeconds": 3601},
		"negative start": {"padStartSeconds": -1, "padEndSeconds": 30},
		"negative end":   {"padStartSeconds": 30, "padEndSeconds": -1},
	} {
		// A valid section alongside must not be written either.
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
			"dvr":       body,
			"streaming": map[string]any{"bufferMinutes": 40},
		}, authHeader(tok))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rr.Code)
		}
	}
	if d, _ := prov.DVR(); d.PadStartSeconds != 0 || d.PadEndSeconds != 600 {
		t.Fatalf("invalid PUT changed dvr: %+v", d)
	}
	if s, _ := prov.Streaming(); s.BufferMinutes != settings.DefaultBufferMinutes {
		t.Fatalf("invalid PUT changed streaming: %+v", s)
	}

	rr = doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"dvr": map[string]any{"padStartSeconds": 1800, "padEndSeconds": 3600},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT at the limits = %d %s", rr.Code, rr.Body.String())
	}
}

// New manual recordings use the padding set in Admin; existing ones keep
// theirs.
func TestManualRecordingUsesPaddingSetting(t *testing.T) {
	e := newDVREnv(t)
	start := e.showAt.Add(10 * time.Hour)
	_, before := e.record(t, e.alice, map[string]any{"channelId": e.ids["5.1"], "start": start, "stop": start.Add(time.Hour)})
	if rr := doJSON(t, e.h, "PUT", "/api/v1/admin/settings", map[string]any{
		"dvr": map[string]any{"padStartSeconds": 300, "padEndSeconds": 900},
	}, e.admin); rr.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", rr.Code, rr.Body.String())
	}
	rr, after := e.record(t, e.alice, map[string]any{"channelId": e.ids["9.1"], "start": start, "stop": start.Add(time.Hour)})
	if rr.Code != http.StatusCreated {
		t.Fatalf("record %d %s", rr.Code, rr.Body.String())
	}
	if r, _ := e.st.RecordingByID(after.ID); r.PadStartSec != 300 || r.PadEndSec != 900 {
		t.Fatalf("new recording padding %d/%d, want 300/900", r.PadStartSec, r.PadEndSec)
	}
	if r, _ := e.st.RecordingByID(before.ID); r.PadStartSec != 60 || r.PadEndSec != 180 {
		t.Fatalf("existing recording padding %d/%d, want 60/180", r.PadStartSec, r.PadEndSec)
	}
}

func TestDVRStorage(t *testing.T) {
	e := newDVREnv(t)
	start := e.showAt.Add(10 * time.Hour)
	e.record(t, e.alice, map[string]any{"channelId": e.ids["5.1"], "start": start, "stop": start.Add(time.Hour)})
	id, _ := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1", Title: "Old",
		Start: start.Add(-48 * time.Hour), Stop: start.Add(-47 * time.Hour), State: store.RecReady, CreatedAt: time.Now()})
	r, _ := e.st.RecordingByID(id)
	r.SizeBytes = 12345
	_ = e.st.UpdateRecording(r)

	if rr := doJSON(t, e.h, "GET", "/api/v1/admin/dvr/storage", nil, e.alice); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d, want 403", rr.Code)
	}
	if rr := doJSON(t, e.h, "GET", "/api/v1/admin/dvr/storage", nil, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d, want 401", rr.Code)
	}
	rr := doJSON(t, e.h, "GET", "/api/v1/admin/dvr/storage", nil, e.admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", rr.Code, rr.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"dir", "usedBytes", "freeBytes", "totalBytes", "floorBytes", "minFreeBytes", "recordings"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing %q in %s", k, rr.Body.String())
		}
	}
	var out struct {
		Dir          string `json:"dir"`
		UsedBytes    int64  `json:"usedBytes"`
		FreeBytes    int64  `json:"freeBytes"`
		TotalBytes   int64  `json:"totalBytes"`
		FloorBytes   int64  `json:"floorBytes"`
		MinFreeBytes int64  `json:"minFreeBytes"`
		Recordings   struct {
			Ready     int `json:"ready"`
			Scheduled int `json:"scheduled"`
			Recording int `json:"recording"`
			Failed    int `json:"failed"`
		} `json:"recordings"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Dir == "" || out.UsedBytes != 12345 || out.TotalBytes <= 0 || out.FreeBytes <= 0 || out.FreeBytes > out.TotalBytes ||
		out.FloorBytes != 2<<30 || out.MinFreeBytes != 0 {
		t.Fatalf("storage %+v", out)
	}
	if out.Recordings.Ready != 1 || out.Recordings.Scheduled != 1 || out.Recordings.Recording != 0 || out.Recordings.Failed != 0 {
		t.Fatalf("counts %+v", out.Recordings)
	}
}

func TestDVRStorageWithoutDVR(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	if rr := doJSON(t, h, "GET", "/api/v1/admin/dvr/storage", nil, authHeader(tok)); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no DVR: %d, want 503", rr.Code)
	}
	// Viewers still get 403, not a hint that the DVR is off.
	h2 := api.New(api.Deps{Cfg: config.Config{}, Store: st, Auth: &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st}})
	seedUser(t, st, "viewer", "viewerpass", "viewer")
	vtok := decodeLogin(t, doJSON(t, h2, "POST", "/api/v1/auth/login", map[string]string{"username": "viewer", "password": "viewerpass"}, nil)).AccessToken
	if rr := doJSON(t, h2, "GET", "/api/v1/admin/dvr/storage", nil, authHeader(vtok)); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer without DVR: %d, want 403", rr.Code)
	}
}
