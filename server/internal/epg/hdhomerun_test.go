package epg

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// hdhrGuideXML mimics SiliconDust's api.hdhomerun.com/api/xmltv response.
const hdhrGuideXML = `<?xml version="1.0" encoding="UTF-8"?>
<tv source-info-name="HDHomeRun">
  <channel id="US1">
    <display-name>9.1</display-name>
    <display-name>KMSP</display-name>
    <icon src="https://example.com/kmsp.png"/>
  </channel>
  <channel id="US2">
    <display-name>11.1 WTCN</display-name>
    <display-name>WTCN</display-name>
  </channel>
  <channel id="US3">
    <display-name>WUCW</display-name>
    <lcn>23.1</lcn>
  </channel>
  <programme start="20261004180000 +0000" stop="20261004190000 +0000" channel="US1">
    <title>Drama</title>
    <series-id system="cseries">C20814443ENX3UM</series-id>
    <episode-num system="dd_progid">EP00001648.0025</episode-num>
    <episode-num system="xmltv_ns">1.4.</episode-num>
    <episode-num system="onscreen">S02E05</episode-num>
    <new/>
    <rating system="VCHIP"><value>TV-14</value></rating>
  </programme>
  <programme start="20261004190000 +0000" stop="20261004200000 +0000" channel="US1">
    <title>Drama</title>
    <series-id system="cseries">C20814443ENX3UM</series-id>
    <episode-num system="dd_progid">EP00001648.0024</episode-num>
    <previously-shown/>
  </programme>
  <programme start="20261004180000 +0000" stop="20261004190000 +0000" channel="US2">
    <title>News</title>
  </programme>
</tv>`

// fakeTuner serves /discover.json with the given DeviceAuth.
func fakeTuner(t *testing.T, deviceID, auth string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discover.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"DeviceID":"` + deviceID + `","DeviceAuth":"` + auth + `","TunerCount":2}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeGuideAPI serves gzip XMLTV only for the expected DeviceAuth and only
// when the client accepts gzip.
func fakeGuideAPI(t *testing.T, wantAuth string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(hdhrGuideXML))
	_ = zw.Close()
	body := buf.Bytes()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/xmltv" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("DeviceAuth"); got != wantAuth {
			http.Error(w, "bad DeviceAuth "+got, http.StatusForbidden)
			return
		}
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			http.Error(w, "gzip required", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func addDevice(t *testing.T, st *store.Store, id, hostPort string) {
	t.Helper()
	if err := st.UpsertDevice(store.Device{DeviceID: id, IP: hostPort, TunerCount: 2, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func hostPort(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func channelByGuide(t *testing.T, st *store.Store, deviceID, guide string) store.Channel {
	t.Helper()
	all, err := st.ListChannels(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.DeviceID == deviceID && c.GuideNumber == guide {
			return c
		}
	}
	t.Fatalf("channel %s/%s not found", deviceID, guide)
	return store.Channel{}
}

// hdhrFixture: two reachable tuners plus one unreachable, a lineup with
// unmapped, pre-mapped and unmatched channels, and the fake guide API.
func hdhrFixture(t *testing.T) (*Service, *store.Store, *settings.Provider, *atomic.Int32) {
	t.Helper()
	st := testStore(t)
	prov := testProvider(t, st)
	a := fakeTuner(t, "AAAA0001", "authA")
	b := fakeTuner(t, "BBBB0002", "authB")
	// Stored out of order: DeviceAuth must be concatenated in DeviceID order.
	addDevice(t, st, "BBBB0002", hostPort(b))
	addDevice(t, st, "CCCC0003", "127.0.0.1:1") // unreachable → skipped
	addDevice(t, st, "AAAA0001", hostPort(a))

	if err := st.SyncLineup("AAAA0001", []store.Channel{
		{GuideNumber: "9.1", Name: "KMSP"},
		{GuideNumber: "11.1", Name: "WTCN"},
		{GuideNumber: "23.1", Name: "WUCW"},
		{GuideNumber: "45.1", Name: "Nothing"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("BBBB0002", []store.Channel{{GuideNumber: "9.1", Name: "KMSP"}}); err != nil {
		t.Fatal(err)
	}
	// 9.1 on tuner A enabled (must stay enabled); 9.1 on tuner B already mapped by hand.
	c := channelByGuide(t, st, "AAAA0001", "9.1")
	if err := st.UpdateChannel(c.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	c = channelByGuide(t, st, "BBBB0002", "9.1")
	if err := st.UpdateChannel(c.ID, true, "my.custom.id"); err != nil {
		t.Fatal(err)
	}

	api, hits := fakeGuideAPI(t, "authAauthB")
	svc := NewService(st, prov)
	svc.hdhrGuideURL = api.URL + "/api/xmltv"
	// The fake guide's airings are on 2026-10-04; pin the clock so the
	// refresh's prune of day-old programs doesn't drop them as time passes.
	svc.now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	return svc, st, prov, hits
}

func TestHDHomeRunRefreshStoresGuideAndAutoMaps(t *testing.T) {
	svc, st, _, hits := hdhrFixture(t)
	// An XMLTV source using the same raw channel ids must coexist.
	if err := st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "US1", DisplayName: "x", Callsign: "x"}}, nil); err != nil {
		t.Fatal(err)
	}

	if err := svc.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("guide API hits = %d, want 1", hits.Load())
	}

	epgChans, err := st.ListEPGChannels()
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, e := range epgChans {
		sources[e.ID] = e.Source
	}
	if sources["US1"] != "xmltv" || sources["hdhomerun:US1"] != "hdhomerun" || sources["hdhomerun:US3"] != "hdhomerun" {
		t.Fatalf("epg channels = %v", sources)
	}

	progs, err := st.ProgramsInRange([]string{"hdhomerun:US1"},
		time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 2 {
		t.Fatalf("programs = %+v", progs)
	}
	p0, p1 := progs[0], progs[1]
	if p0.ProgramID != "EP000016480025" || p0.SeriesID != "C20814443ENX3UM" || !p0.IsNew || p0.Rating != "TV-14" {
		t.Fatalf("p0 = %+v", p0)
	}
	if p1.IsNew {
		t.Fatalf("previously-shown must not be new: %+v", p1)
	}

	// Auto-mapping by guide number: exact display-name, "<guide> " prefix, lcn.
	if c := channelByGuide(t, st, "AAAA0001", "9.1"); c.EPGChannelID != "hdhomerun:US1" || !c.Enabled {
		t.Fatalf("9.1 = %+v", c)
	}
	if c := channelByGuide(t, st, "AAAA0001", "11.1"); c.EPGChannelID != "hdhomerun:US2" || c.Enabled {
		t.Fatalf("11.1 = %+v (must stay disabled)", c)
	}
	if c := channelByGuide(t, st, "AAAA0001", "23.1"); c.EPGChannelID != "hdhomerun:US3" {
		t.Fatalf("23.1 = %+v", c)
	}
	if c := channelByGuide(t, st, "AAAA0001", "45.1"); c.EPGChannelID != "" {
		t.Fatalf("45.1 = %+v (no match)", c)
	}
	// An existing mapping is never overwritten.
	if c := channelByGuide(t, st, "BBBB0002", "9.1"); c.EPGChannelID != "my.custom.id" {
		t.Fatalf("hand-mapped 9.1 = %+v", c)
	}

	status := svc.Status()
	if !status.HDHomeRun.Configured || status.HDHomeRun.LastSuccess.IsZero() || status.HDHomeRun.LastError != "" || status.HDHomeRun.Stale {
		t.Fatalf("status = %+v", status.HDHomeRun)
	}
}

func TestHDHomeRunDisabledNeverFetches(t *testing.T) {
	svc, _, prov, hits := hdhrFixture(t)
	if err := prov.SetHDHomeRunGuide(settings.HDHomeRunGuide{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	gate := &afterGate{}
	startSupervisor(t, svc, gate)
	waitUntil(t, 2*time.Second, func() bool { return svc.lastWaitFor("hdhomerun") == unconfiguredPoll })
	gate.AdvanceAll()
	waitUntil(t, 2*time.Second, func() bool { return gate.PendingCount() >= 1 })

	if hits.Load() != 0 {
		t.Fatalf("guide API hits = %d, want 0 when off", hits.Load())
	}
	if svc.Status().HDHomeRun.Configured {
		t.Fatal("Configured must be false when off")
	}
}

func TestHDHomeRunNoReachableTuner(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	addDevice(t, st, "CCCC0003", "127.0.0.1:1")
	svc := NewService(st, prov)
	api, hits := fakeGuideAPI(t, "x")
	svc.hdhrGuideURL = api.URL + "/api/xmltv"

	err := svc.RefreshAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no HDHomeRun found") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("hits = %d", hits.Load())
	}
	if got := svc.Status().HDHomeRun.LastError; !strings.Contains(got, "no HDHomeRun found") {
		t.Fatalf("lastError = %q", got)
	}
}

// With no tuners stored the source is not configured: nothing to fetch and
// no error (first boot before discovery, and API tests with empty stores).
func TestHDHomeRunNoDevicesIsUnconfigured(t *testing.T) {
	st := testStore(t)
	svc := NewService(st, testProvider(t, st))
	if err := svc.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
	if svc.Status().HDHomeRun.Configured {
		t.Fatal("no devices → not configured")
	}
}

// The supervisor fetches once, then waits 20-28 hours.
func TestHDHomeRunSupervisorPoliteInterval(t *testing.T) {
	svc, _, _, hits := hdhrFixture(t)
	gate := &afterGate{}
	startSupervisor(t, svc, gate)
	waitUntil(t, 2*time.Second, func() bool {
		w := svc.lastWaitFor("hdhomerun")
		return hits.Load() == 1 && w != 0 && w != unconfiguredPoll
	})
	w := svc.lastWaitFor("hdhomerun")
	if w < 20*time.Hour-time.Minute || w > 28*time.Hour {
		t.Fatalf("wait = %v, want 20-28h", w)
	}
}

// After a restart, a guide fetched recently is not fetched again right away.
func TestHDHomeRunSupervisorRespectsRecentSuccess(t *testing.T) {
	svc, st, _, hits := hdhrFixture(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	if err := st.SetSetting(settingHDHomeRunLastSuccess, now.Add(-2*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	gate := &afterGate{}
	startSupervisor(t, svc, gate)
	waitUntil(t, 2*time.Second, func() bool {
		w := svc.lastWaitFor("hdhomerun")
		return w != 0 && w != unconfiguredPoll
	})
	w := svc.lastWaitFor("hdhomerun")
	if w < 18*time.Hour || w > 26*time.Hour {
		t.Fatalf("wait = %v, want lastSuccess + 20-28h - now", w)
	}
	if hits.Load() != 0 {
		t.Fatalf("hits = %d, want 0", hits.Load())
	}
}

// Turning the free guide off removes its programs and the mappings it made
// (admin mappings stay).
func TestClearHDHomeRunRemovesItsDataAndMappings(t *testing.T) {
	svc, st, _, _ := hdhrFixture(t)
	if err := svc.refreshHDHomeRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := channelByGuide(t, st, "AAAA0001", "9.1"); !strings.HasPrefix(c.EPGChannelID, hdhomerunIDPrefix) {
		t.Fatalf("precondition: 9.1 mapped to %q", c.EPGChannelID)
	}
	if err := svc.ClearHDHomeRun(); err != nil {
		t.Fatal(err)
	}
	if c := channelByGuide(t, st, "AAAA0001", "9.1"); c.EPGChannelID != "" {
		t.Fatalf("auto mapping kept: %q", c.EPGChannelID)
	}
	if c := channelByGuide(t, st, "BBBB0002", "9.1"); c.EPGChannelID != "my.custom.id" {
		t.Fatalf("admin mapping changed: %q", c.EPGChannelID)
	}
	chans, _ := st.ListEPGChannels()
	for _, c := range chans {
		if c.Source == sourceHDHomeRun {
			t.Fatalf("hdhomerun guide channel kept: %+v", c)
		}
	}
}
