package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// Recording JSON carries "commercials" ([{start,end}] seconds) only when
// detection found some.
func TestRecordingJSONCommercials(t *testing.T) {
	e := newDVREnv(t)
	mk := func(title string, ago time.Duration) int64 {
		id, err := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1 B",
			Title: title, Start: e.showAt.Add(-ago), Stop: e.showAt.Add(-ago + time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := e.st.RecordingByID(id)
		r.State, r.DurationSec = store.RecReady, 3600
		if err := e.st.UpdateRecording(r); err != nil {
			t.Fatal(err)
		}
		return id
	}
	found := mk("found", 48*time.Hour)
	none := mk("none", 72*time.Hour)
	notRun := mk("not run", 96*time.Hour)
	if err := e.st.SetRecordingCommercials(found, []store.Commercial{{Start: 312.5, End: 498.25}, {Start: 1500, End: 1680}}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetRecordingCommercials(none, nil); err != nil {
		t.Fatal(err)
	}

	rr := doJSON(t, e.h, "GET", "/api/v1/recordings?state=recorded", nil, e.alice)
	if rr.Code != http.StatusOK {
		t.Fatalf("list %d %s", rr.Code, rr.Body.String())
	}
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, r := range list {
		byID[string(r["id"])] = r
	}
	got := byID[fmt.Sprint(found)]["commercials"]
	var segs []map[string]float64
	if err := json.Unmarshal(got, &segs); err != nil {
		t.Fatalf("commercials %s: %v", got, err)
	}
	if want := []map[string]float64{{"start": 312.5, "end": 498.25}, {"start": 1500, "end": 1680}}; !reflect.DeepEqual(segs, want) {
		t.Fatalf("commercials %v, want %v", segs, want)
	}
	for _, id := range []int64{none, notRun} {
		if c, ok := byID[fmt.Sprint(id)]["commercials"]; ok && string(c) != "[]" {
			t.Fatalf("recording %d: commercials %s, want omitted", id, c)
		}
	}

	// The single-recording response (PATCH) carries them too.
	rr = doJSON(t, e.h, "PATCH", fmt.Sprintf("/api/v1/recordings/%d", found), map[string]any{"protected": true}, e.alice)
	var one struct {
		Commercials []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"commercials"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &one) != nil || len(one.Commercials) != 2 || one.Commercials[1].End != 1680 {
		t.Fatalf("patch %d %s", rr.Code, rr.Body.String())
	}
}

// POST /api/v1/recordings/{id}/commercials/detect: admins only; 409 when
// detection isn't available.
func TestRedetectEndpoint(t *testing.T) {
	e := newDVREnv(t)
	id, err := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1 B",
		Title: "show", Start: e.showAt, Stop: e.showAt.Add(time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/recordings/%d/commercials/detect", id)
	if rr := doJSON(t, e.h, "POST", path, nil, e.alice); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer %d", rr.Code)
	}
	if rr := doJSON(t, e.h, "POST", "/api/v1/recordings/99999/commercials/detect", nil, e.admin); rr.Code != http.StatusNotFound {
		t.Fatalf("missing %d", rr.Code)
	}
	rr := doJSON(t, e.h, "POST", path, nil, e.admin)
	if rr.Code != http.StatusConflict {
		t.Fatalf("no detector %d %s", rr.Code, rr.Body.String())
	}
}
