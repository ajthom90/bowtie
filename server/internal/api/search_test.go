package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestGuideSearch(t *testing.T) {
	e := newDVREnv(t) // channels 5.1/9.1/11.1 mapped; "Show on <g>" at showAt
	_, rec := e.record(t, e.alice, map[string]any{"channelId": e.ids["9.1"], "programStart": e.showAt})

	rr := doJSON(t, e.h, "GET", "/api/v1/guide/search?q="+url.QueryEscape("show on 9"), nil, e.bob)
	var hits []struct {
		ChannelID   int64     `json:"channelId"`
		GuideNumber string    `json:"guideNumber"`
		ChannelName string    `json:"channelName"`
		Start       time.Time `json:"start"`
		Title       string    `json:"title"`
		Recording   *struct {
			ID int64 `json:"id"`
		} `json:"recording"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &hits) != nil || len(hits) != 1 {
		t.Fatalf("search %d %s", rr.Code, rr.Body.String())
	}
	h := hits[0]
	if h.GuideNumber != "9.1" || h.ChannelID != e.ids["9.1"] || !h.Start.Equal(e.showAt) || h.Recording == nil || h.Recording.ID != rec.ID {
		t.Fatalf("hit %+v", h)
	}
	if rr := doJSON(t, e.h, "GET", "/api/v1/guide/search?q=", nil, e.bob); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty q %d", rr.Code)
	}
	if rr := doJSON(t, e.h, "GET", "/api/v1/guide/search?q=show", nil, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauth %d", rr.Code)
	}
}

// Restricted accounts: matches on blocked channels don't eat the limit, and
// a blocked program's hidden description can't be searched.
func TestGuideSearchRestrictedAccount(t *testing.T) {
	e := newDVREnv(t)
	at := e.showAt.Add(5 * time.Hour)
	var progs []store.Program
	for i := 0; i < 5; i++ { // five matches on 5.1, which the kid can't see
		st := at.Add(time.Duration(i) * time.Hour)
		progs = append(progs, store.Program{EPGChannelID: "epg-5.1", Start: st, Stop: st.Add(time.Hour), Title: "Game Night"})
	}
	progs = append(progs,
		store.Program{EPGChannelID: "epg-9.1", Start: at.Add(10 * time.Hour), Stop: at.Add(11 * time.Hour), Title: "Game Night"},
		store.Program{EPGChannelID: "epg-9.1", Start: at.Add(12 * time.Hour), Stop: at.Add(13 * time.Hour), Title: "Late Movie", Description: "secret plot", Rating: "TV-MA"},
	)
	_ = e.st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "epg-5.1", Source: "xmltv"}, {ID: "epg-9.1", Source: "xmltv"}}, progs)
	kid := seedUser(t, e.st, "kid", "pw", "viewer")
	kidTok := decodeLogin(t, doJSON(t, e.h, "POST", "/api/v1/auth/login", map[string]string{"username": "kid", "password": "pw"}, nil))
	kidH := map[string]string{"Authorization": "Bearer " + kidTok.AccessToken}
	doJSON(t, e.h, "PATCH", fmt.Sprintf("/api/v1/admin/users/%d", kid.ID), map[string]any{"allowedChannelIds": []int64{e.ids["9.1"]}, "maxRating": "TV-PG"}, e.admin)

	rr := doJSON(t, e.h, "GET", "/api/v1/guide/search?q=game&limit=3", nil, kidH)
	var hits []struct {
		GuideNumber string `json:"guideNumber"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &hits) != nil || len(hits) != 1 || hits[0].GuideNumber != "9.1" {
		t.Fatalf("limit ate the allowed match: %s", rr.Body.String())
	}
	rr = doJSON(t, e.h, "GET", "/api/v1/guide/search?q=secret", nil, kidH)
	if rr.Body.String() != "[]\n" {
		t.Fatalf("blocked description searchable: %s", rr.Body.String())
	}
}
