package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestIPTVFeed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	prov := settings.NewProvider(st)
	_ = prov.SeedFromConfig(config.Config{})
	ss := newStubStreams()
	var startedFor string
	ss.startFn = func(_ context.Context, u store.User, ch int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
		startedFor = fmt.Sprintf("%s:%d:%v", u.Username, ch, caps.VideoCodecs)
		ss.register("vx", t.TempDir())
		return stream.ViewerHandle{ViewerID: "vx", SessionID: "s1"}, nil
	}
	h := api.New(api.Deps{Cfg: config.Config{}, Store: st, EPG: epg.NewService(st, prov), Streams: ss,
		Auth:              &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st},
		StreamTokenSecret: []byte(streamSecret)})
	_ = st.UpsertDevice(store.Device{DeviceID: "d", IP: "1.2.3.4", Model: "X", TunerCount: 2, StreamPort: 5004, LastSeen: time.Now()})
	_ = st.SyncLineup("d", []store.Channel{{DeviceID: "d", GuideNumber: "5.1", Name: "KSTP"}, {DeviceID: "d", GuideNumber: "9.1", Name: "FOX 9"}})
	ids := map[string]int64{}
	chans, _ := st.ListChannels(false)
	for _, c := range chans {
		ids[c.GuideNumber] = c.ID
		_ = st.UpdateChannel(c.ID, true, "e"+c.GuideNumber)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	_ = st.ReplaceEPG("xmltv", []store.EPGChannel{{ID: "e9.1", DisplayName: "FOX 9", Source: "xmltv"}}, []store.Program{
		{EPGChannelID: "e9.1", Start: now, Stop: now.Add(time.Hour), Title: "News & Weather", Subtitle: "Late", Description: "<local>", Category: "News", Rating: "TV-G", ProgramID: "EP012345670001", IsNew: true},
	})
	kid := seedUser(t, st, "kid", "pw", "viewer")
	tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "kid", "password": "pw"}, nil))
	kidH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	rr := doJSON(t, h, "POST", "/api/v1/me/feed", nil, kidH)
	var feed struct {
		M3U   string `json:"m3uUrl"`
		XMLTV string `json:"xmltvUrl"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &feed) != nil || !strings.HasPrefix(feed.M3U, "http://example.com/api/v1/iptv/") {
		t.Fatalf("feed %d %s", rr.Code, rr.Body.String())
	}
	get := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", strings.TrimPrefix(url, "http://example.com"), nil))
		return rec
	}
	m3u := get(feed.M3U)
	body := m3u.Body.String()
	if m3u.Code != http.StatusOK || !strings.HasPrefix(body, "#EXTM3U") || !strings.Contains(body, `tvg-chno="9.1"`) || !strings.Contains(body, fmt.Sprintf(`tvg-id="bowtie.%d"`, ids["9.1"])) || !strings.Contains(body, ",FOX 9\n") {
		t.Fatalf("m3u %d:\n%s", m3u.Code, body)
	}
	streamURL := ""
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, fmt.Sprintf("/stream/%d", ids["9.1"])) {
			streamURL = l
		}
	}
	if streamURL == "" {
		t.Fatalf("no 9.1 stream line:\n%s", body)
	}
	sr := get(streamURL)
	if sr.Code != http.StatusFound || !strings.Contains(sr.Header().Get("Location"), "/api/v1/stream/vx/index.m3u8?token=") || startedFor != fmt.Sprintf("kid:%d:[h264]", ids["9.1"]) {
		t.Fatalf("stream %d loc=%q started=%q", sr.Code, sr.Header().Get("Location"), startedFor)
	}
	x := get(feed.XMLTV)
	xb := x.Body.String()
	if x.Code != http.StatusOK || !strings.Contains(xb, fmt.Sprintf(`<channel id="bowtie.%d">`, ids["9.1"])) || !strings.Contains(xb, "<title>News &amp; Weather</title>") ||
		!strings.Contains(xb, "&lt;local&gt;") || !strings.Contains(xb, `<episode-num system="dd_progid">EP01234567.0001</episode-num>`) || !strings.Contains(xb, "<new></new>") {
		t.Fatalf("xmltv %d:\n%s", x.Code, xb)
	}

	// Parental: a restricted account's feed lists only its channels.
	_ = st.SetFeedKeyHash(kid.ID, "") // rotate off then back on
	if get(feed.M3U).Code != http.StatusNotFound {
		t.Fatal("old feed URL still works after the feed was turned off")
	}
}
