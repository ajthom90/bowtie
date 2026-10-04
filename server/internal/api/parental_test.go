package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestParentalControls(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	prov := settings.NewProvider(st)
	_ = prov.SeedFromConfig(config.Config{})
	ss := newStubStreams()
	started := 0
	ss.startFn = func(context.Context, store.User, int64, transcode.ClientCaps) (stream.ViewerHandle, error) {
		started++
		ss.register("v1", t.TempDir())
		return stream.ViewerHandle{ViewerID: "v1", SessionID: "s1"}, nil
	}
	h := api.New(api.Deps{Cfg: config.Config{}, Store: st, EPG: epg.NewService(st, prov), Streams: ss,
		Auth:              &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st},
		StreamTokenSecret: []byte(streamSecret)})

	_ = st.UpsertDevice(store.Device{DeviceID: "d", IP: "1.2.3.4", Model: "X", TunerCount: 2, StreamPort: 5004, LastSeen: time.Now()})
	_ = st.SyncLineup("d", []store.Channel{{DeviceID: "d", GuideNumber: "5.1", Name: "A"}, {DeviceID: "d", GuideNumber: "9.1", Name: "B"}})
	ids := map[string]int64{}
	chans, _ := st.ListChannels(false)
	for _, c := range chans {
		ids[c.GuideNumber] = c.ID
		_ = st.UpdateChannel(c.ID, true, "e"+c.GuideNumber)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	_ = st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "e5.1", Source: "xmltv"}, {ID: "e9.1", Source: "xmltv"}}, []store.Program{
		{EPGChannelID: "e9.1", Start: now, Stop: now.Add(time.Hour), Title: "Late Movie", Description: "graphic", Rating: "TV-MA"},
		{EPGChannelID: "e9.1", Start: now.Add(time.Hour), Stop: now.Add(2 * time.Hour), Title: "Cartoons", Description: "fun", Rating: "TV-G"},
		{EPGChannelID: "e5.1", Start: now, Stop: now.Add(time.Hour), Title: "News"},
	})
	seedUser(t, st, "root", "pw", "admin")
	kid := seedUser(t, st, "kid", "pw", "viewer")
	login := func(u string) map[string]string {
		tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": u, "password": "pw"}, nil))
		return map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	}
	admin, kidH := login("root"), login("kid")

	path := fmt.Sprintf("/api/v1/admin/users/%d", kid.ID)
	if rr := doJSON(t, h, "PATCH", path, map[string]any{"maxRating": "XYZ"}, admin); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad rating %d", rr.Code)
	}
	rr := doJSON(t, h, "PATCH", path, map[string]any{"allowedChannelIds": []int64{ids["9.1"]}, "maxRating": "TV-PG"}, admin)
	var u struct {
		AllowedChannelIDs []int64 `json:"allowedChannelIds"`
		MaxRating         string  `json:"maxRating"`
		BlockUnrated      bool    `json:"blockUnrated"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &u) != nil || len(u.AllowedChannelIDs) != 1 || u.MaxRating != "TV-PG" {
		t.Fatalf("patch %d %s", rr.Code, rr.Body.String())
	}

	var list []struct {
		GuideNumber string `json:"guideNumber"`
	}
	rr = doJSON(t, h, "GET", "/api/v1/channels", nil, kidH)
	if json.Unmarshal(rr.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].GuideNumber != "9.1" {
		t.Fatalf("kid channels %s", rr.Body.String())
	}

	q := fmt.Sprintf("/api/v1/guide?start=%s&stop=%s", now.Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339))
	var guide []struct {
		GuideNumber string `json:"guideNumber"`
		Programs    []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Rating      string `json:"rating"`
			Locked      bool   `json:"locked"`
		} `json:"programs"`
	}
	rr = doJSON(t, h, "GET", q, nil, kidH)
	if json.Unmarshal(rr.Body.Bytes(), &guide) != nil || len(guide) != 1 || len(guide[0].Programs) != 2 {
		t.Fatalf("kid guide %s", rr.Body.String())
	}
	movie, cartoons := guide[0].Programs[0], guide[0].Programs[1]
	if !movie.Locked || movie.Description != "" || movie.Rating != "TV-MA" || movie.Title != "Late Movie" || cartoons.Locked || cartoons.Description != "fun" {
		t.Fatalf("locks %+v %+v", movie, cartoons)
	}
	// Admins see everything, unlocked.
	rr = doJSON(t, h, "GET", q, nil, admin)
	if json.Unmarshal(rr.Body.Bytes(), &guide) != nil || len(guide) != 2 {
		t.Fatalf("admin guide %s", rr.Body.String())
	}

	start := func(guideNum string) (int, map[string]any) {
		rr := doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{"channelId": ids[guideNum],
			"caps": map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}}}, kidH)
		var body map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		return rr.Code, body
	}
	if code, body := start("5.1"); code != http.StatusForbidden || body["code"] != "parental" {
		t.Fatalf("disallowed channel %d %v", code, body)
	}
	if code, body := start("9.1"); code != http.StatusForbidden || body["error"] != "Blocked by parental controls (rated TV-MA)" {
		t.Fatalf("TV-MA now %d %v", code, body)
	}
	if started != 0 {
		t.Fatal("stream started despite parental controls")
	}
	// Lifting the rating limit lets it play.
	doJSON(t, h, "PATCH", path, map[string]any{"maxRating": ""}, admin)
	if code, _ := start("9.1"); code != http.StatusOK || started != 1 {
		t.Fatalf("after lifting: %d started=%d", code, started)
	}
}

// Restricted accounts don't see or play recordings from channels or ratings
// they're blocked from.
func TestParentalRecordings(t *testing.T) {
	e := newDVREnv(t) // programs "Show on <g>" at showAt, unrated
	kid := seedUser(t, e.st, "kid", "pw", "viewer")
	kidTok := decodeLogin(t, doJSON(t, e.h, "POST", "/api/v1/auth/login", map[string]string{"username": "kid", "password": "pw"}, nil))
	kidH := map[string]string{"Authorization": "Bearer " + kidTok.AccessToken}

	mk := func(guide, rating string) int64 {
		id, _ := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids[guide], ChannelName: guide, Title: "R " + guide + " " + rating,
			Rating: rating, Start: e.showAt.Add(-48 * time.Hour), Stop: e.showAt.Add(-47 * time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
		r, _ := e.st.RecordingByID(id)
		r.State, r.Dir = store.RecReady, filepath.Join(e.dir, fmt.Sprint(id))
		_ = e.st.UpdateRecording(r)
		return id
	}
	okID, maID, otherCh := mk("9.1", "TV-G"), mk("9.1", "TV-MA"), mk("5.1", "TV-G")
	if r, _ := e.st.RecordingByID(maID); r.Rating != "TV-MA" {
		t.Fatalf("rating not stored: %+v", r)
	}
	path := fmt.Sprintf("/api/v1/admin/users/%d", kid.ID)
	if rr := doJSON(t, e.h, "PATCH", path, map[string]any{"allowedChannelIds": []int64{e.ids["9.1"]}, "maxRating": "TV-PG"}, e.admin); rr.Code != http.StatusOK {
		t.Fatalf("patch %d", rr.Code)
	}
	var list []struct {
		ID     int64 `json:"id"`
		Locked bool  `json:"locked"`
	}
	rr := doJSON(t, e.h, "GET", "/api/v1/recordings?state=recorded", nil, kidH)
	if json.Unmarshal(rr.Body.Bytes(), &list) != nil || len(list) != 2 {
		t.Fatalf("kid list %s", rr.Body.String())
	}
	for _, r := range list {
		if r.ID == otherCh || (r.ID == maID) != r.Locked {
			t.Fatalf("list %+v", list)
		}
	}
	play := func(id int64) int {
		return doJSON(t, e.h, "POST", fmt.Sprintf("/api/v1/recordings/%d/play", id), nil, kidH).Code
	}
	if play(maID) != http.StatusForbidden || play(otherCh) != http.StatusForbidden {
		t.Fatalf("blocked plays: %d %d", play(maID), play(otherCh))
	}
	if c := play(okID); c != http.StatusOK {
		t.Fatalf("allowed play %d", c)
	}
	// Scheduling from the guide snapshots the program's rating.
	_ = e.st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "epg-9.1", Source: "xmltv"}}, []store.Program{
		{EPGChannelID: "epg-9.1", Start: e.showAt, Stop: e.showAt.Add(time.Hour), Title: "Rated", Rating: "TV-14"}})
	_, rec := e.record(t, e.alice, map[string]any{"channelId": e.ids["9.1"], "programStart": e.showAt})
	if r, _ := e.st.RecordingByID(rec.ID); r.Rating != "TV-14" {
		t.Fatalf("scheduled rating %q", r.Rating)
	}
}
