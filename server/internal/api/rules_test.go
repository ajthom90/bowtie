package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
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
