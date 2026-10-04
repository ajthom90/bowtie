package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/dvr"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

type busySource struct{}

func (busySource) Open(context.Context, store.Channel) (io.ReadCloser, error) {
	return nil, stream.ErrTunersBusy
}

type dvrEnv struct {
	h       http.Handler
	st      *store.Store
	svc     *dvr.Service
	prov    *settings.Provider
	dir     string
	ids     map[string]int64
	showAt  time.Time
	alice   map[string]string
	bob     map[string]string
	admin   map[string]string
	aliceID int64
}

func newDVREnv(t *testing.T) *dvrEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	prov := settings.NewProvider(st)
	if err := prov.SeedFromConfig(config.Config{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	svc := dvr.New(dvr.Deps{Store: st, Source: busySource{}, Dir: dir, Padding: prov.DVRPadding})
	t.Cleanup(svc.Shutdown)
	h := api.New(api.Deps{
		Cfg: config.Config{}, Store: st, EPG: epg.NewService(st, prov), DVR: svc, Settings: prov,
		Auth:              &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st},
		StreamTokenSecret: []byte("stream-secret-stream-secret-0123"),
	})
	e := &dvrEnv{h: h, st: st, svc: svc, prov: prov, dir: dir, ids: map[string]int64{}}
	if err := st.UpsertDevice(store.Device{DeviceID: "d", IP: "1.2.3.4", Model: "DUO", TunerCount: 2, StreamPort: 5004, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("d", []store.Channel{
		{DeviceID: "d", GuideNumber: "5.1", Name: "A"},
		{DeviceID: "d", GuideNumber: "9.1", Name: "B"},
		{DeviceID: "d", GuideNumber: "11.1", Name: "C"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, _ := st.ListChannels(false)
	for _, c := range chans {
		e.ids[c.GuideNumber] = c.ID
		_ = st.UpdateChannel(c.ID, true, "epg-"+c.GuideNumber)
	}
	e.showAt = time.Now().UTC().Add(2 * time.Hour).Truncate(time.Hour)
	var progs []store.Program
	var ecs []store.EPGChannel
	for _, g := range []string{"5.1", "9.1", "11.1"} {
		ecs = append(ecs, store.EPGChannel{ID: "epg-" + g, DisplayName: g, Source: "xmltv"})
		progs = append(progs, store.Program{EPGChannelID: "epg-" + g, Start: e.showAt, Stop: e.showAt.Add(time.Hour),
			Title: "Show on " + g, Subtitle: "Pilot", Description: "desc", Category: "Drama"})
	}
	if err := st.ReplaceEPG("xmltv", ecs, progs); err != nil {
		t.Fatal(err)
	}
	a := seedUser(t, st, "alice", "pw", "viewer")
	e.aliceID = a.ID
	seedUser(t, st, "bob", "pw", "viewer")
	seedUser(t, st, "root", "pw", "admin")
	login := func(u string) map[string]string {
		tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": u, "password": "pw"}, nil))
		return map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	}
	e.alice, e.bob, e.admin = login("alice"), login("bob"), login("root")
	return e
}

type recJSON struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	ChannelID   int64     `json:"channelId"`
	ChannelName string    `json:"channelName"`
	Start       time.Time `json:"start"`
	Stop        time.Time `json:"stop"`
	State       string    `json:"state"`
	ScheduledBy string    `json:"scheduledBy"`
	CanManage   bool      `json:"canManage"`
	PositionSec int       `json:"positionSec"`
	// Absent (nil) when the caller never saved a position.
	PositionUpdatedAt *time.Time `json:"positionUpdatedAt"`
}

func (e *dvrEnv) record(t *testing.T, hdr map[string]string, body map[string]any) (*httptest.ResponseRecorder, recJSON) {
	t.Helper()
	rr := doJSON(t, e.h, "POST", "/api/v1/recordings", body, hdr)
	var out struct {
		Recording recJSON `json:"recording"`
	}
	if rr.Code == http.StatusCreated {
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
	}
	return rr, out.Recording
}

func TestRecordFromGuideAndList(t *testing.T) {
	e := newDVREnv(t)
	rr, rec := e.record(t, e.alice, map[string]any{"channelId": e.ids["9.1"], "programStart": e.showAt})
	if rr.Code != http.StatusCreated || rec.Title != "Show on 9.1" || rec.Subtitle != "Pilot" || !rec.Stop.Equal(e.showAt.Add(time.Hour)) || rec.State != "scheduled" {
		t.Fatalf("record %d %s", rr.Code, rr.Body.String())
	}
	if rr, _ := e.record(t, e.alice, map[string]any{"channelId": e.ids["9.1"], "programStart": e.showAt.Add(time.Minute)}); rr.Code != http.StatusNotFound {
		t.Fatalf("no such program: %d", rr.Code)
	}

	// The guide marks the scheduled program.
	q := fmt.Sprintf("/api/v1/guide?start=%s&stop=%s", e.showAt.Format(time.RFC3339), e.showAt.Add(time.Hour).Format(time.RFC3339))
	grr := doJSON(t, e.h, "GET", q, nil, e.bob)
	var guide []struct {
		GuideNumber string `json:"guideNumber"`
		Programs    []struct {
			Recording *struct {
				ID    int64  `json:"id"`
				State string `json:"state"`
			} `json:"recording"`
		} `json:"programs"`
	}
	if err := json.Unmarshal(grr.Body.Bytes(), &guide); err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, g := range guide {
		for _, p := range g.Programs {
			if p.Recording != nil {
				if g.GuideNumber != "9.1" || p.Recording.ID != rec.ID {
					t.Fatalf("wrong program marked: %s %+v", g.GuideNumber, p.Recording)
				}
				marked++
			}
		}
	}
	if marked != 1 {
		t.Fatalf("marked %d programs", marked)
	}

	lrr := doJSON(t, e.h, "GET", "/api/v1/recordings?state=upcoming", nil, e.bob)
	var list []recJSON
	if lrr.Code != http.StatusOK || json.Unmarshal(lrr.Body.Bytes(), &list) != nil || len(list) != 1 {
		t.Fatalf("list %d %s", lrr.Code, lrr.Body.String())
	}
	if list[0].ScheduledBy != "alice" || list[0].CanManage {
		t.Fatalf("bob's view %+v", list[0])
	}
	if lrr := doJSON(t, e.h, "GET", "/api/v1/recordings?state=recorded", nil, e.bob); lrr.Body.String() != "[]\n" {
		t.Fatalf("recorded: %s", lrr.Body.String())
	}
}

func TestRecordManualAndConflict409(t *testing.T) {
	e := newDVREnv(t)
	start := e.showAt.Add(10 * time.Hour)
	for _, g := range []string{"5.1", "9.1"} {
		rr, _ := e.record(t, e.alice, map[string]any{"channelId": e.ids[g], "start": start, "stop": start.Add(time.Hour), "title": "Game"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("manual %s: %d %s", g, rr.Code, rr.Body.String())
		}
		if g == "9.1" && !strings.Contains(rr.Body.String(), "usesAllTuners") {
			t.Fatalf("no warning: %s", rr.Body.String())
		}
	}
	rr, _ := e.record(t, e.alice, map[string]any{"channelId": e.ids["11.1"], "start": start, "stop": start.Add(time.Hour)})
	var conflict struct {
		Error      string    `json:"error"`
		TunerCount int       `json:"tunerCount"`
		Conflicts  []recJSON `json:"conflicts"`
	}
	if rr.Code != http.StatusConflict || json.Unmarshal(rr.Body.Bytes(), &conflict) != nil || conflict.TunerCount != 2 || len(conflict.Conflicts) != 2 {
		t.Fatalf("conflict %d %s", rr.Code, rr.Body.String())
	}
	if rr, _ := e.record(t, e.alice, map[string]any{"channelId": e.ids["11.1"], "start": start, "stop": start.Add(time.Hour), "force": true}); rr.Code != http.StatusCreated {
		t.Fatalf("force %d", rr.Code)
	}
	if rr, _ := e.record(t, e.alice, map[string]any{"channelId": e.ids["11.1"], "start": start, "stop": start.Add(-time.Hour)}); rr.Code != http.StatusBadRequest {
		t.Fatalf("backwards window %d", rr.Code)
	}
	if rr, _ := e.record(t, e.alice, map[string]any{"channelId": 9999, "start": start, "stop": start.Add(time.Hour)}); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown channel %d", rr.Code)
	}
}

func TestDeleteOnlyOwnUnlessAdmin(t *testing.T) {
	e := newDVREnv(t)
	_, rec := e.record(t, e.alice, map[string]any{"channelId": e.ids["9.1"], "programStart": e.showAt})
	path := fmt.Sprintf("/api/v1/recordings/%d", rec.ID)
	if rr := doJSON(t, e.h, "DELETE", path, nil, e.bob); rr.Code != http.StatusForbidden {
		t.Fatalf("bob delete %d", rr.Code)
	}
	if rr := doJSON(t, e.h, "DELETE", path, nil, e.admin); rr.Code != http.StatusNoContent {
		t.Fatalf("admin delete %d", rr.Code)
	}
	if rr := doJSON(t, e.h, "DELETE", path, nil, e.admin); rr.Code != http.StatusNotFound {
		t.Fatalf("delete again %d", rr.Code)
	}
}

func TestRecordingJSONPositionUpdatedAt(t *testing.T) {
	e := newDVREnv(t)
	mk := func(title string, hoursAgo int) int64 {
		at := e.showAt.Add(-time.Duration(hoursAgo) * time.Hour)
		id, err := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1 B",
			Title: title, Start: at, Stop: at.Add(time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := e.st.RecordingByID(id)
		r.State, r.DurationSec = store.RecReady, 3600
		_ = e.st.UpdateRecording(r)
		return id
	}
	watched, untouched := mk("Watched", 48), mk("Untouched", 72)

	before := time.Now().UTC().Add(-time.Second)
	if rr := doJSON(t, e.h, "PUT", fmt.Sprintf("/api/v1/recordings/%d/position", watched),
		map[string]any{"positionSec": 900}, e.alice); rr.Code != http.StatusNoContent {
		t.Fatalf("position %d", rr.Code)
	}
	after := time.Now().UTC().Add(time.Second)

	list := func(hdr map[string]string) map[int64]recJSON {
		t.Helper()
		rr := doJSON(t, e.h, "GET", "/api/v1/recordings?state=recorded", nil, hdr)
		var rows []recJSON
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &rows) != nil {
			t.Fatalf("list %d %s", rr.Code, rr.Body.String())
		}
		out := map[int64]recJSON{}
		for _, r := range rows {
			out[r.ID] = r
		}
		return out
	}

	a := list(e.alice)
	w := a[watched]
	if w.PositionSec != 900 || w.PositionUpdatedAt == nil || w.PositionUpdatedAt.Before(before) || w.PositionUpdatedAt.After(after) {
		t.Fatalf("alice watched %+v", w)
	}
	if u := a[untouched]; u.PositionSec != 0 || u.PositionUpdatedAt != nil {
		t.Fatalf("alice untouched %+v", u)
	}
	// Omitted entirely (not null / zero time) when never saved.
	rr := doJSON(t, e.h, "GET", "/api/v1/recordings?state=recorded", nil, e.bob)
	if strings.Contains(rr.Body.String(), "positionUpdatedAt") {
		t.Fatalf("bob sees a position time: %s", rr.Body.String())
	}
	if b := list(e.bob)[watched]; b.PositionSec != 0 || b.PositionUpdatedAt != nil {
		t.Fatalf("bob watched %+v", b)
	}

	// Single-recording responses carry it too.
	prr := doJSON(t, e.h, "PATCH", fmt.Sprintf("/api/v1/recordings/%d", watched), map[string]any{"protected": true}, e.alice)
	var patched recJSON
	if prr.Code != http.StatusOK || json.Unmarshal(prr.Body.Bytes(), &patched) != nil ||
		patched.PositionUpdatedAt == nil || !patched.PositionUpdatedAt.Equal(*w.PositionUpdatedAt) {
		t.Fatalf("patch %d %s", prr.Code, prr.Body.String())
	}

	// Resetting to 0 ("remove from Continue watching") is still a saved position.
	if rr := doJSON(t, e.h, "PUT", fmt.Sprintf("/api/v1/recordings/%d/position", watched),
		map[string]any{"positionSec": 0}, e.alice); rr.Code != http.StatusNoContent {
		t.Fatalf("reset %d", rr.Code)
	}
	if w := list(e.alice)[watched]; w.PositionSec != 0 || w.PositionUpdatedAt == nil {
		t.Fatalf("after reset %+v", w)
	}
}

func TestPlayReadyRecordingWithResume(t *testing.T) {
	e := newDVREnv(t)
	recDir := filepath.Join(e.dir, "1-show")
	hls := filepath.Join(recDir, "hls")
	if err := os.MkdirAll(hls, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.m3u8":    "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aac\",NAME=\"English\",URI=\"aac0.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aac\"\nv720.m3u8\n",
		"v720.m3u8":     "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:6.0,\nv720_00000.ts\n#EXT-X-ENDLIST\n",
		"v720_00000.ts": "TSDATA",
	}
	for n, b := range files {
		_ = os.WriteFile(filepath.Join(hls, n), []byte(b), 0o644)
	}
	id, _ := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1 B", Title: "Done",
		Start: e.showAt.Add(-48 * time.Hour), Stop: e.showAt.Add(-47 * time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
	r, _ := e.st.RecordingByID(id)
	r.State, r.Dir, r.DurationSec = store.RecReady, recDir, 6
	_ = e.st.UpdateRecording(r)

	pos := fmt.Sprintf("/api/v1/recordings/%d/position", id)
	if rr := doJSON(t, e.h, "PUT", pos, map[string]any{"positionSec": 3}, e.alice); rr.Code != http.StatusNoContent {
		t.Fatalf("position %d", rr.Code)
	}
	rr := doJSON(t, e.h, "POST", fmt.Sprintf("/api/v1/recordings/%d/play", id), nil, e.alice)
	var play struct {
		PlaylistURL string `json:"playlistUrl"`
		PositionSec int    `json:"positionSec"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &play) != nil || play.PositionSec != 3 ||
		!strings.HasPrefix(play.PlaylistURL, fmt.Sprintf("/api/v1/recordings/%d/hls/index.m3u8?token=", id)) {
		t.Fatalf("play %d %s", rr.Code, rr.Body.String())
	}
	tok := play.PlaylistURL[strings.Index(play.PlaylistURL, "token=")+len("token="):]

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	base := fmt.Sprintf("/api/v1/recordings/%d/hls/", id)
	m := get(base + "index.m3u8?token=" + tok)
	if m.Code != http.StatusOK || !strings.Contains(m.Body.String(), "\nv720.m3u8?token="+tok+"\n") || !strings.Contains(m.Body.String(), `URI="aac0.m3u8?token=`+tok+`"`) {
		t.Fatalf("master %d:\n%s", m.Code, m.Body.String())
	}
	v := get(base + "v720.m3u8?token=" + tok)
	if !strings.Contains(v.Body.String(), "\nv720_00000.ts?token="+tok+"\n") || !strings.Contains(v.Body.String(), "#EXT-X-ENDLIST") {
		t.Fatalf("media:\n%s", v.Body.String())
	}
	if seg := get(base + "v720_00000.ts?token=" + tok); seg.Code != http.StatusOK || seg.Body.String() != "TSDATA" {
		t.Fatalf("segment %d", seg.Code)
	}
	if bad := get(base + "v720_00000.ts?token=nope"); bad.Code != http.StatusForbidden {
		t.Fatalf("bad token %d", bad.Code)
	}
	if bad := get(base + "..%2Fparts.txt?token=" + tok); bad.Code != http.StatusBadRequest && bad.Code != http.StatusNotFound {
		t.Fatalf("traversal %d", bad.Code)
	}
	// Another recording's token doesn't open this one.
	other := get(fmt.Sprintf("/api/v1/recordings/%d/hls/index.m3u8?token=%s", id+1, tok))
	if other.Code != http.StatusForbidden {
		t.Fatalf("cross-recording token %d", other.Code)
	}
}

// A recording converted at the 1080p setting plays the same way: its master
// names v1080.m3u8 and those files are served.
func TestPlay1080Recording(t *testing.T) {
	e := newDVREnv(t)
	recDir := filepath.Join(e.dir, "2-show")
	hls := filepath.Join(recDir, "hls")
	if err := os.MkdirAll(hls, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.m3u8":     "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aac\",NAME=\"Audio\",URI=\"aac0.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=9152000,RESOLUTION=1920x1080,AUDIO=\"aac\"\nv1080.m3u8\n",
		"v1080.m3u8":     "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:6.0,\nv1080_00000.ts\n#EXT-X-ENDLIST\n",
		"v1080_00000.ts": "TS1080",
	}
	for n, b := range files {
		_ = os.WriteFile(filepath.Join(hls, n), []byte(b), 0o644)
	}
	id, _ := e.st.CreateRecording(store.Recording{UserID: e.aliceID, ChannelID: e.ids["9.1"], ChannelName: "9.1 B", Title: "Done",
		Start: e.showAt.Add(-48 * time.Hour), Stop: e.showAt.Add(-47 * time.Hour), State: store.RecScheduled, CreatedAt: time.Now()})
	r, _ := e.st.RecordingByID(id)
	r.State, r.Dir, r.DurationSec = store.RecReady, recDir, 6
	_ = e.st.UpdateRecording(r)

	rr := doJSON(t, e.h, "POST", fmt.Sprintf("/api/v1/recordings/%d/play", id), nil, e.alice)
	var play struct {
		PlaylistURL string `json:"playlistUrl"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &play) != nil {
		t.Fatalf("play %d %s", rr.Code, rr.Body.String())
	}
	tok := play.PlaylistURL[strings.Index(play.PlaylistURL, "token=")+len("token="):]
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	base := fmt.Sprintf("/api/v1/recordings/%d/hls/", id)
	if m := get(base + "index.m3u8?token=" + tok); m.Code != http.StatusOK || !strings.Contains(m.Body.String(), "\nv1080.m3u8?token="+tok+"\n") {
		t.Fatalf("master %d:\n%s", m.Code, m.Body.String())
	}
	if v := get(base + "v1080.m3u8?token=" + tok); v.Code != http.StatusOK || !strings.Contains(v.Body.String(), "\nv1080_00000.ts?token="+tok+"\n") {
		t.Fatalf("media %d:\n%s", v.Code, v.Body.String())
	}
	if seg := get(base + "v1080_00000.ts?token=" + tok); seg.Code != http.StatusOK || seg.Body.String() != "TS1080" {
		t.Fatalf("segment %d", seg.Code)
	}
}
