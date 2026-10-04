package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
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
