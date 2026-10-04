package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestFavoritesAndRecents(t *testing.T) {
	h, st, _ := testAPIWithEPG(t, config.Config{})
	alice := seedUser(t, st, "alice", "pw", "viewer")
	seedUser(t, st, "bob", "pw", "viewer")
	if err := st.UpsertDevice(store.Device{DeviceID: "d", IP: "1.2.3.4", Model: "X", TunerCount: 1, StreamPort: 5004, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("d", []store.Channel{
		{DeviceID: "d", GuideNumber: "5.1", Name: "A"},
		{DeviceID: "d", GuideNumber: "9.1", Name: "B"},
		{DeviceID: "d", GuideNumber: "13.1", Name: "Off"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, _ := st.ListChannels(false)
	ids := map[string]int64{}
	for _, c := range chans {
		ids[c.GuideNumber] = c.ID
		if c.GuideNumber != "13.1" {
			_ = st.UpdateChannel(c.ID, true, "")
		}
	}
	login := func(u string) map[string]string {
		tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": u, "password": "pw"}, nil))
		return map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	}
	ah, bh := login("alice"), login("bob")
	fav := func(hdr map[string]string, method string, id int64) int {
		return doJSON(t, h, method, fmt.Sprintf("/api/v1/me/favorites/%d", id), nil, hdr).Code
	}

	if c := fav(ah, "PUT", ids["9.1"]); c != http.StatusNoContent {
		t.Fatalf("PUT %d", c)
	}
	if c := fav(ah, "PUT", ids["9.1"]); c != http.StatusNoContent {
		t.Fatalf("PUT again %d", c)
	}
	if c := fav(ah, "PUT", ids["13.1"]); c != http.StatusNotFound {
		t.Fatalf("PUT disabled channel %d, want 404", c)
	}
	if c := fav(ah, "PUT", 9999); c != http.StatusNotFound {
		t.Fatalf("PUT unknown channel %d, want 404", c)
	}

	favorites := func(hdr map[string]string, path string) map[string]bool {
		rr := doJSON(t, h, "GET", path, nil, hdr)
		var rows []struct {
			GuideNumber string `json:"guideNumber"`
			Favorite    *bool  `json:"favorite"`
		}
		if rr.Code != http.StatusOK || json.NewDecoder(rr.Body).Decode(&rows) != nil {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
		}
		out := map[string]bool{}
		for _, r := range rows {
			if r.Favorite == nil {
				t.Fatalf("%s: favorite missing on %s", path, r.GuideNumber)
			}
			out[r.GuideNumber] = *r.Favorite
		}
		return out
	}
	if f := favorites(ah, "/api/v1/channels"); !f["9.1"] || f["5.1"] {
		t.Fatalf("alice channels %v", f)
	}
	if f := favorites(bh, "/api/v1/channels"); f["9.1"] {
		t.Fatalf("bob sees alice's star: %v", f)
	}
	if f := favorites(ah, "/api/v1/guide"); !f["9.1"] || f["5.1"] {
		t.Fatalf("alice guide %v", f)
	}
	if c := fav(ah, "DELETE", ids["9.1"]); c != http.StatusNoContent {
		t.Fatalf("DELETE %d", c)
	}
	if f := favorites(ah, "/api/v1/channels"); f["9.1"] {
		t.Fatalf("unstar: %v", f)
	}

	// Recents: newest first, limited.
	t0 := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	_ = st.RecordWatch(alice.ID, ids["5.1"], t0)
	_ = st.RecordWatch(alice.ID, ids["9.1"], t0.Add(time.Minute))
	rr := doJSON(t, h, "GET", "/api/v1/me/recents?limit=1", nil, ah)
	var recents []struct {
		ChannelID   int64     `json:"channelId"`
		GuideNumber string    `json:"guideNumber"`
		Name        string    `json:"name"`
		WatchedAt   time.Time `json:"watchedAt"`
	}
	if rr.Code != http.StatusOK || json.NewDecoder(rr.Body).Decode(&recents) != nil ||
		len(recents) != 1 || recents[0].GuideNumber != "9.1" || recents[0].ChannelID != ids["9.1"] {
		t.Fatalf("recents %d %+v", rr.Code, recents)
	}
	if rr := doJSON(t, h, "GET", "/api/v1/me/recents?limit=abc", nil, ah); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad limit %d", rr.Code)
	}
	if rr := doJSON(t, h, "DELETE", "/api/v1/me/recents", nil, ah); rr.Code != http.StatusNoContent {
		t.Fatalf("clear %d", rr.Code)
	}
	rr = doJSON(t, h, "GET", "/api/v1/me/recents", nil, ah)
	if rr.Body.String() != "[]\n" {
		t.Fatalf("after clear: %q", rr.Body.String())
	}
}
