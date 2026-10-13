package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

// watchableFixture: one HDHomeRun with a single tuner and two enabled
// channels; returns the handler, a viewer auth header and the fake.
func watchableFixture(t *testing.T) (http.Handler, map[string]string, *hdhrfake.Fake) {
	h, viewerH, fake, _ := watchableFixtureStore(t)
	return h, viewerH, fake
}

func watchableFixtureStore(t *testing.T) (http.Handler, map[string]string, *hdhrfake.Fake, *store.Store) {
	t.Helper()
	fake := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "ONE001",
		TunerCount: 1,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "5.1", GuideName: "NEWS"},
			{GuideNumber: "7.1", GuideName: "OTHER"},
		},
	})
	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	segDir := filepath.Join(t.TempDir(), "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ListenAddr: ":0", SegmentDir: segDir, Encoder: "auto", FFmpegPath: "ffmpeg"}
	a := &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st}
	tuners := tuner.New(st, cfg)
	tuners.SetDiscoverFunc(func(ctx context.Context, timeout time.Duration) ([]hdhr.DiscoverInfo, error) {
		return nil, nil
	})
	mgr := stream.NewManager(stream.ManagerDeps{
		TrackProbeTimeout: time.Millisecond,
		Cfg:               cfg,
		Store:             st,
		Tuners:            tuners,
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
		Ingest: stream.NewIngestManager(stream.HTTPDial),
	})
	prov := settings.NewProvider(st)
	h := api.New(api.Deps{
		Cfg: cfg, Store: st, Auth: a, Tuners: tuners, Streams: mgr, EPG: epg.NewService(st, prov),
		StreamTokenSecret: []byte(streamSecret),
	})
	seedUser(t, st, "alice", "pass", "viewer")
	seedUser(t, st, "admin", "adminpass", "admin")
	adminH := map[string]string{"Authorization": "Bearer " + decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "adminpass"}, nil)).AccessToken}
	if rr := doJSON(t, h, "POST", "/api/v1/admin/devices", map[string]string{"ip": u.Host}, adminH); rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("add device: %d %s", rr.Code, rr.Body.String())
	}
	var chans []struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(doJSON(t, h, "GET", "/api/v1/admin/channels", nil, adminH).Body).Decode(&chans)
	for _, c := range chans {
		doJSON(t, h, "PATCH", "/api/v1/admin/channels/"+strconv.FormatInt(c.ID, 10), map[string]any{"enabled": true}, adminH)
	}
	viewerH := map[string]string{"Authorization": "Bearer " + decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login",
		map[string]string{"username": "alice", "password": "pass"}, nil)).AccessToken}
	return h, viewerH, fake, st
}

func watchableByGuide(t *testing.T, h http.Handler, path string, authH map[string]string) map[string]bool {
	t.Helper()
	rr := doJSON(t, h, "GET", path, nil, authH)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
	}
	var list []struct {
		GuideNumber string `json:"guideNumber"`
		Watchable   *bool  `json:"watchable"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range list {
		if c.Watchable == nil {
			t.Fatalf("%s: channel %s has no watchable field", path, c.GuideNumber)
		}
		out[c.GuideNumber] = *c.Watchable
	}
	return out
}

func guidePath() string {
	start := time.Now().UTC().Truncate(time.Hour)
	q := url.Values{"start": {start.Format(time.RFC3339)}, "stop": {start.Add(time.Hour).Format(time.RFC3339)}}
	return "/api/v1/guide?" + q.Encode()
}

func TestChannelsWatchableWithFreeTuner(t *testing.T) {
	h, viewerH, _ := watchableFixture(t)
	for _, path := range []string{"/api/v1/channels", guidePath()} {
		got := watchableByGuide(t, h, path, viewerH)
		if !got["5.1"] || !got["7.1"] {
			t.Fatalf("%s: watchable = %v, want all true", path, got)
		}
	}
}

func TestChannelsNotWatchableWhenTunersHeldElsewhere(t *testing.T) {
	h, viewerH, fake := watchableFixture(t)
	// Another app (e.g. Plex) holds the only tuner.
	resp, err := http.Get(fake.URL + "/auto/v7.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	for _, path := range []string{"/api/v1/channels", guidePath()} {
		got := watchableByGuide(t, h, path, viewerH)
		if got["5.1"] || got["7.1"] {
			t.Fatalf("%s: watchable = %v, want all false", path, got)
		}
	}
}

// Search results carry watchable too, so the apps can hide Watch on a busy
// channel found through search.
func TestSearchHitsWatchable(t *testing.T) {
	h, viewerH, fake, st := watchableFixtureStore(t)
	chans, _ := st.ListChannels(false)
	var ch71 store.Channel
	for _, c := range chans {
		if c.GuideNumber == "7.1" {
			ch71 = c
		}
	}
	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "other.us", DisplayName: "OTHER", Source: "xmltv"}},
		[]store.Program{{EPGChannelID: "other.us", Start: time.Now().Add(-10 * time.Minute), Stop: time.Now().Add(50 * time.Minute), Title: "Evening Movie"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateChannel(ch71.ID, true, "other.us"); err != nil {
		t.Fatal(err)
	}
	search := func() bool {
		rr := doJSON(t, h, "GET", "/api/v1/guide/search?q=evening", nil, viewerH)
		var hits []struct {
			Watchable *bool `json:"watchable"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&hits); err != nil || len(hits) != 1 || hits[0].Watchable == nil {
			t.Fatalf("search: %d %v hits=%v", rr.Code, err, hits)
		}
		return *hits[0].Watchable
	}
	if !search() {
		t.Fatal("free tuner: want watchable")
	}
	resp, err := http.Get(fake.URL + "/auto/v5.1") // another app takes the only tuner
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if search() {
		t.Fatal("only tuner held elsewhere: want not watchable")
	}
}
