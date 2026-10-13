package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestSeriesRulesAPI(t *testing.T) {
	e := newDVREnv(t)
	_ = e.st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "epg-9.1", Source: "xmltv"}, {ID: "epg-5.1", Source: "xmltv"}}, []store.Program{
		{EPGChannelID: "epg-9.1", Start: e.showAt, Stop: e.showAt.Add(time.Hour), Title: "Drama", ProgramID: "EP1", SeriesID: "SH1", IsNew: true},
		{EPGChannelID: "epg-9.1", Start: e.showAt.Add(24 * time.Hour), Stop: e.showAt.Add(25 * time.Hour), Title: "Drama", ProgramID: "EP2", SeriesID: "SH1", IsNew: true},
		{EPGChannelID: "epg-5.1", Start: e.showAt.Add(2 * time.Hour), Stop: e.showAt.Add(3 * time.Hour), Title: "Drama", ProgramID: "EP3", SeriesID: "SH1", IsNew: true},
	})
	rr := doJSON(t, e.h, "POST", "/api/v1/recording-rules", map[string]any{
		"channelId": e.ids["9.1"], "programStart": e.showAt, "keepLatest": 3,
	}, e.alice)
	var created struct {
		Rule struct {
			ID         int64  `json:"id"`
			Title      string `json:"title"`
			ChannelID  int64  `json:"channelId"`
			NewOnly    bool   `json:"newOnly"`
			KeepLatest int    `json:"keepLatest"`
		} `json:"rule"`
		Scheduled int `json:"scheduled"`
	}
	if rr.Code != http.StatusCreated || json.Unmarshal(rr.Body.Bytes(), &created) != nil {
		t.Fatalf("create %d %s", rr.Code, rr.Body.String())
	}
	// Default: this channel only, new episodes only.
	if created.Rule.Title != "Drama" || created.Rule.ChannelID != e.ids["9.1"] || !created.Rule.NewOnly || created.Rule.KeepLatest != 3 || created.Scheduled != 2 {
		t.Fatalf("created %+v", created)
	}
	rr = doJSON(t, e.h, "GET", "/api/v1/recording-rules", nil, e.bob)
	var rules []struct {
		ID          int64  `json:"id"`
		ScheduledBy string `json:"scheduledBy"`
		CanManage   bool   `json:"canManage"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &rules) != nil || len(rules) != 1 || rules[0].ScheduledBy != "alice" || rules[0].CanManage {
		t.Fatalf("list %s", rr.Body.String())
	}
	path := fmt.Sprintf("/api/v1/recording-rules/%d", created.Rule.ID)
	if rr := doJSON(t, e.h, "DELETE", path, nil, e.bob); rr.Code != http.StatusForbidden {
		t.Fatalf("bob delete %d", rr.Code)
	}
	if rr := doJSON(t, e.h, "DELETE", path, nil, e.alice); rr.Code != http.StatusNoContent {
		t.Fatalf("delete %d", rr.Code)
	}
	if up, _ := e.st.ListRecordings(store.RecScheduled); len(up) != 0 {
		t.Fatalf("rule's upcoming recordings kept: %+v", up)
	}
	// anyChannel: every channel airing the series.
	rr = doJSON(t, e.h, "POST", "/api/v1/recording-rules", map[string]any{
		"channelId": e.ids["9.1"], "programStart": e.showAt, "anyChannel": true,
	}, e.alice)
	if json.Unmarshal(rr.Body.Bytes(), &created) != nil || created.Scheduled != 3 || created.Rule.ChannelID != 0 {
		t.Fatalf("any channel %s", rr.Body.String())
	}
}

func TestPatchRule(t *testing.T) {
	e := newDVREnv(t)
	id, err := e.st.CreateRule(store.RecordingRule{UserID: e.aliceID, Title: "Drama", NewOnly: true, KeepLatest: 5, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/recording-rules/%d", id)
	for _, tc := range []struct {
		name, path string
		token      map[string]string
		body       any
		status     int
	}{
		{"unauthenticated", path, nil, map[string]any{}, 401},
		{"other owner", path, e.bob, map[string]any{}, 403},
		{"missing", "/api/v1/recording-rules/99999", e.alice, map[string]any{}, 404},
		{"bad id", "/api/v1/recording-rules/no", e.alice, map[string]any{}, 400},
		{"negative keep", path, e.alice, map[string]any{"keepLatest": -1}, 400},
		{"fraction", path, e.alice, map[string]any{"keepLatest": 1.5}, 400},
		{"bad bool", path, e.alice, map[string]any{"newOnly": "false"}, 400},
		{"null", path, e.alice, map[string]any{"newOnly": nil}, 400},
		{"unknown field", path, e.alice, map[string]any{"title": "Other"}, 400},
		{"missing channel", path, e.alice, map[string]any{"channelId": 99999}, 400},
		{"negative channel", path, e.alice, map[string]any{"channelId": -1}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := doJSON(t, e.h, "PATCH", tc.path, tc.body, tc.token)
			if rr.Code != tc.status {
				t.Fatalf("%d %s", rr.Code, rr.Body.String())
			}
		})
	}
	for _, body := range []string{`null`, `[]`, `{"newOnly":false} {}`, `{"newOnly":false}junk`, `{"newOnly":`} {
		req := httptest.NewRequest("PATCH", path, strings.NewReader(body))
		for k, v := range e.alice {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		e.h.ServeHTTP(rr, req)
		if rr.Code != 400 {
			t.Fatalf("body %q: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	disabledID := e.ids["5.1"]
	if err := e.st.UpdateChannel(disabledID, false, "epg-5.1"); err != nil {
		t.Fatal(err)
	}
	if rr := doJSON(t, e.h, "PATCH", path, map[string]any{"channelId": disabledID}, e.alice); rr.Code != 400 {
		t.Fatalf("disabled channel: %d", rr.Code)
	}
	if rr := doJSON(t, e.h, "PATCH", path, map[string]any{"channelId": disabledID}, e.admin); rr.Code != 200 {
		t.Fatalf("admin disabled channel: %d", rr.Code)
	}
	if err := e.st.UpdateChannel(disabledID, true, "epg-5.1"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{{"newOnly": false}, {"channelId": e.ids["5.1"], "keepLatest": 0}} {
		rr := doJSON(t, e.h, "PATCH", path, body, e.alice)
		var rule struct {
			Editable, CanManage, NewOnly bool
			KeepLatest                   int
			ChannelID                    int64
		}
		if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &rule) != nil || !rule.Editable || !rule.CanManage || rule.NewOnly {
			t.Fatalf("patch %d %s", rr.Code, rr.Body.String())
		}
	}
	rr := doJSON(t, e.h, "PATCH", path, map[string]any{"channelId": 0}, e.admin)
	if rr.Code != 200 {
		t.Fatalf("admin %d %s", rr.Code, rr.Body.String())
	}
}
