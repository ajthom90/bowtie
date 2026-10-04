package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

const streamSecret = "0123456789abcdef0123456789abcdef"

// --- stub StreamController ---------------------------------------------------

type stubStreams struct {
	mu         sync.Mutex
	startFn    func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	joinFn     func(ctx context.Context, user store.User, sessionID string, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	touchCalls []string
	stopped    []string
	terminated []string
	sessions   []stream.SessionInfo
	dirs       map[string]string // viewerID → dir
	viewers    map[string]bool
	reception  map[int64]stream.Reception
	media      map[string]stream.SessionMedia // viewerID → media
	blocked    map[string]string              // viewerID → parental reason
	onStop     func(id string)
}

func newStubStreams() *stubStreams {
	return &stubStreams{
		dirs:    make(map[string]string),
		viewers: make(map[string]bool),
		media:   make(map[string]stream.SessionMedia),
	}
}

func (s *stubStreams) Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
	if s.startFn != nil {
		return s.startFn(ctx, user, channelID, caps)
	}
	return stream.ViewerHandle{}, errors.New("start not configured")
}

func (s *stubStreams) Join(ctx context.Context, user store.User, sessionID string, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
	if s.joinFn != nil {
		return s.joinFn(ctx, user, sessionID, channelID, caps)
	}
	return stream.ViewerHandle{}, stream.ErrNotJoinable
}

func (s *stubStreams) BlockedReason(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	why, ok := s.blocked[id]
	return why, ok
}

func (s *stubStreams) Touch(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchCalls = append(s.touchCalls, id)
	return s.viewers[id]
}

func (s *stubStreams) StopViewer(id string) {
	s.mu.Lock()
	s.stopped = append(s.stopped, id)
	delete(s.viewers, id)
	onStop := s.onStop
	s.mu.Unlock()
	if onStop != nil {
		onStop(id)
	}
}

func (s *stubStreams) Sessions() []stream.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stream.SessionInfo(nil), s.sessions...)
}

func (s *stubStreams) Terminate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminated = append(s.terminated, id)
}

func (s *stubStreams) SessionDirOf(viewerID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dirs[viewerID]
	return d, ok
}

func (s *stubStreams) SessionInfoOf(viewerID string) (stream.SessionInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.viewers[viewerID] {
		return stream.SessionInfo{}, false
	}
	for _, info := range s.sessions {
		for _, v := range info.Viewers {
			if v.ID == viewerID {
				return info, true
			}
		}
	}
	// Default minimal info when registered without an explicit sessions entry.
	return stream.SessionInfo{
		VideoCodec:  "h264",
		Profile:     "high",
		Backend:     "software",
		ChannelName: "TEST",
	}, true
}

func (s *stubStreams) IngestChannels() []int64 { return nil }

func (s *stubStreams) ChannelReception(id int64) (stream.Reception, bool) {
	r, ok := s.reception[id]
	return r, ok
}

func (s *stubStreams) register(viewerID, dir string) {
	s.registerCapped(viewerID, dir, 1080)
}

// registerCapped registers a viewer of the fixture ladder session whose
// quality ceiling is maxHeight.
func (s *stubStreams) registerCapped(viewerID, dir string, maxHeight int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirs[viewerID] = dir
	s.viewers[viewerID] = true
	s.media[viewerID] = stream.SessionMedia{Dir: dir, Layout: fixtureLayout(), MaxHeight: maxHeight}
}

func (s *stubStreams) SessionMediaOf(viewerID string) (stream.SessionMedia, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.viewers[viewerID] {
		return stream.SessionMedia{}, false
	}
	m, ok := s.media[viewerID]
	return m, ok
}

// fixtureLayout matches writeFixtureSession: 720/480/360, eng+spa (AC-3), captions.
func fixtureLayout() transcode.Layout {
	return transcode.Layout{
		Rungs:      transcode.Ladder(720),
		Audio:      []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}},
		AudioKbps:  128,
		Captions:   true,
		AC3Copy:    true,
		VideoCodec: "h264",
	}
}

func testAPIWithStreams(t *testing.T, streams api.StreamController) (http.Handler, *store.Store, *auth.Auth) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	h := api.New(api.Deps{
		Cfg: config.Config{
			ListenAddr: ":0",
			Encoder:    "auto",
		},
		Store:             st,
		Auth:              a,
		Streams:           streams,
		StreamTokenSecret: []byte(streamSecret),
		Probe: func() transcode.Capabilities {
			return transcode.Capabilities{
				Available:     []transcode.Backend{transcode.BackendSoftware},
				HEVC:          map[transcode.Backend]bool{transcode.BackendSoftware: false},
				FFmpegVersion: "test-8.0",
			}
		},
	})
	return h, st, a
}

// mediaPlaylist is a two-segment media playlist for rendition name.
func mediaPlaylist(name, ext string) string {
	seg := func(i int) string {
		if ext == "vtt" {
			return fmt.Sprintf("%s%d.vtt", strings.TrimSuffix(name, "_vtt"), i)
		}
		return fmt.Sprintf("%s_%05d.ts", name, i)
	}
	return strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXT-X-TARGETDURATION:4",
		"#EXT-X-MEDIA-SEQUENCE:0",
		"#EXTINF:4.000000,",
		seg(0),
		"#EXTINF:4.000000,",
		seg(1),
		"",
	}, "\n")
}

// writeFixtureSession writes the fixtureLayout() session files: three rungs,
// AAC and AC-3 renditions, captions playlist and a WebVTT segment.
func writeFixtureSession(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"v720.m3u8":     mediaPlaylist("v720", "ts"),
		"v480.m3u8":     mediaPlaylist("v480", "ts"),
		"v360.m3u8":     mediaPlaylist("v360", "ts"),
		"aac0.m3u8":     mediaPlaylist("aac0", "ts"),
		"aac1.m3u8":     mediaPlaylist("aac1", "ts"),
		"ac30.m3u8":     mediaPlaylist("ac30", "ts"),
		"v720_vtt.m3u8": mediaPlaylist("v720_vtt", "vtt"),
		"v7200.vtt":     "WEBVTT\n\n00:00.000 --> 00:02.000\nHello\n",
		"v720_00000.ts": "TSSEG0",
		"v720_00001.ts": "TSSEG1",
		"aac0_00000.ts": "AACSEG0",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaylistRewriteAndTouch(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")

	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	viewerID := "aabbccddeeff00112233445566778899"
	ss.register(viewerID, dir)

	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	path := "/api/v1/stream/" + viewerID + "/v720.m3u8?token=" + tok
	rr := doJSON(t, h, "GET", path, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type=%q", ct)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q", cc)
	}
	body := rr.Body.String()
	want0 := "/api/v1/stream/" + viewerID + "/v720_00000.ts?token=" + tok
	want1 := "/api/v1/stream/" + viewerID + "/v720_00001.ts?token=" + tok
	if !strings.Contains(body, want0) || !strings.Contains(body, want1) {
		t.Fatalf("playlist rewrite missing URLs:\n%s", body)
	}
	if strings.Contains(body, "\nv720_00000.ts\n") {
		t.Fatalf("bare segment name still present:\n%s", body)
	}
	if !strings.Contains(body, "#EXTINF:4.000000,") {
		t.Fatalf("EXTINF tags missing:\n%s", body)
	}

	ss.mu.Lock()
	n := len(ss.touchCalls)
	ss.mu.Unlock()
	if n != 1 || ss.touchCalls[0] != viewerID {
		t.Fatalf("Touch calls=%v, want [%s]", ss.touchCalls, viewerID)
	}
}

func TestPlaylistTokenViewerMismatch403(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")

	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	ss.register("viewer-a", dir)

	// Token for a different viewer.
	tok := stream.SignStreamToken([]byte(streamSecret), "viewer-b", time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "GET", "/api/v1/stream/viewer-a/index.m3u8?token="+tok, nil, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%q, want 403", rr.Code, rr.Body.String())
	}
}

func TestSegmentNameTraversal400(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")

	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	viewerID := "viewer-a"
	ss.register(viewerID, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))

	// Path traversal attempts — Go ServeMux path values won't include ../
	// but invalid names like these must 400.
	for _, bad := range []string{"../etc/passwd", "seg.ts", "seg0.ts", "seg0000a.ts", "SEG00000.ts"} {
		path := "/api/v1/stream/" + viewerID + "/" + bad + "?token=" + tok
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		// Some paths may 404 from mux if they don't match the route pattern;
		// either 400 or 404 is fine for traversal; never 200.
		if rr.Code == http.StatusOK {
			t.Fatalf("segment %q returned 200", bad)
		}
		if bad == "seg.ts" || bad == "seg0.ts" || bad == "seg0000a.ts" || bad == "SEG00000.ts" {
			// These match the route pattern {segment} so our handler runs.
			if rr.Code != http.StatusBadRequest {
				t.Errorf("segment %q status=%d, want 400 body=%q", bad, rr.Code, rr.Body.String())
			}
		}
	}

	// Valid segment serves content.
	path := "/api/v1/stream/" + viewerID + "/v720_00000.ts?token=" + tok
	rr := doJSON(t, h, "GET", path, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid segment status=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "video/mp2t" {
		t.Errorf("Content-Type=%q", rr.Header().Get("Content-Type"))
	}
	if rr.Body.String() != "TSSEG0" {
		t.Errorf("body=%q", rr.Body.String())
	}
}

func TestCreateSession503Shape(t *testing.T) {
	ss := newStubStreams()
	ss.startFn = func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
		return stream.ViewerHandle{}, stream.ErrTunersBusy
	}
	h, st, _ := testAPIWithStreams(t, ss)
	// Viewer 503 filtering uses enabled-channel IDs — seed a matching enabled channel.
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertDevice(store.Device{
		DeviceID: "dev-503shape", IP: "127.0.0.1", Model: "fake", TunerCount: 1,
		Manual: true, LastSeen: now, StreamPort: 5004,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("dev-503shape", []store.Channel{
		{DeviceID: "dev-503shape", GuideNumber: "5.1", Name: "NEWS"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := st.ListChannels(false)
	if err != nil || len(chans) != 1 {
		t.Fatalf("channels: %v len=%d", err, len(chans))
	}
	chID := chans[0].ID
	if err := st.UpdateChannel(chID, true, ""); err != nil {
		t.Fatal(err)
	}
	ss.sessions = []stream.SessionInfo{{
		ID:          "sess-1",
		ChannelID:   chID,
		ChannelName: "NEWS",
		Key:         "ch1|h264|original|aac",
		VideoCodec:  "h264",
		Profile:     "original",
		Backend:     "software",
		Viewers:     []stream.ViewerInfo{{ID: "v1", Username: "bob"}},
		StartedAt:   time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}}
	seedUser(t, st, "alice", "pass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil)
	tok := decodeLogin(t, rr)
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": chID,
		"caps": map[string]any{
			"videoCodecs": []string{"h264"},
			"audioCodecs": []string{"aac"},
		},
	}, authH)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var body struct {
		Error    string               `json:"error"`
		Sessions []stream.SessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "all tuners in use" {
		t.Errorf("error=%q", body.Error)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].ID != "sess-1" {
		t.Errorf("sessions=%+v", body.Sessions)
	}
}

func TestCreateSessionErrors(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil)
	tok := decodeLogin(t, rr)
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	// 404 unknown channel
	ss.startFn = func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
		return stream.ViewerHandle{}, errors.New("unknown channel 99: sql: no rows in result set")
	}
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": 99,
		"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
	}, authH)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown channel status=%d body=%q", rr.Code, rr.Body.String())
	}

	// 404 disabled
	ss.startFn = func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
		return stream.ViewerHandle{}, errors.New("channel 1 is disabled")
	}
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": 1,
		"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
	}, authH)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("disabled status=%d body=%q", rr.Code, rr.Body.String())
	}

	// 422 negotiate
	ss.startFn = func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
		return stream.ViewerHandle{}, errors.New("negotiate: no usable video codec: client supports [av1]")
	}
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": 1,
		"caps":      map[string]any{"videoCodecs": []string{"av1"}, "audioCodecs": []string{"aac"}},
	}, authH)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("negotiate status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestDeleteSessionBearerOrToken(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	viewerID := "viewer-del"
	ss.register(viewerID, t.TempDir())

	// Via stream token.
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "DELETE", "/api/v1/sessions/"+viewerID+"?token="+tok, nil, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("token delete status=%d body=%q", rr.Code, rr.Body.String())
	}
	ss.mu.Lock()
	if len(ss.stopped) != 1 || ss.stopped[0] != viewerID {
		t.Fatalf("stopped=%v", ss.stopped)
	}
	ss.mu.Unlock()

	// Via Bearer.
	ss.register(viewerID, t.TempDir())
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil)
	login := decodeLogin(t, rr)
	rr = doJSON(t, h, "DELETE", "/api/v1/sessions/"+viewerID, nil, map[string]string{
		"Authorization": "Bearer " + login.AccessToken,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("bearer delete status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestHeartbeatStreamTokenAndBearer(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	viewerID := "viewer-hb"
	ss.register(viewerID, t.TempDir())

	// Stream token → 204 + Touch.
	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?token="+tok, nil, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("token heartbeat status=%d body=%q", rr.Code, rr.Body.String())
	}
	ss.mu.Lock()
	if len(ss.touchCalls) != 1 || ss.touchCalls[0] != viewerID {
		t.Fatalf("Touch calls=%v, want [%s]", ss.touchCalls, viewerID)
	}
	ss.mu.Unlock()

	// Bearer → 204 + Touch.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil)
	login := decodeLogin(t, rr)
	rr = doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat", nil, map[string]string{
		"Authorization": "Bearer " + login.AccessToken,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("bearer heartbeat status=%d body=%q", rr.Code, rr.Body.String())
	}
	ss.mu.Lock()
	if len(ss.touchCalls) != 2 {
		t.Fatalf("Touch calls after bearer=%v, want 2", ss.touchCalls)
	}
	ss.mu.Unlock()
}

func TestHeartbeatAuthFailure401(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	viewerID := "viewer-hb-bad"
	ss.register(viewerID, t.TempDir())

	// Bad stream token → 401 (mirrors DELETE; A5).
	rr := doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?token=not-a-valid-token", nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status=%d body=%q, want 401", rr.Code, rr.Body.String())
	}

	// Token for a different viewer → 401.
	otherTok := stream.SignStreamToken([]byte(streamSecret), "other-viewer", time.Now().UTC().Add(time.Hour))
	rr = doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?token="+otherTok, nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("mismatch token status=%d body=%q, want 401", rr.Code, rr.Body.String())
	}

	// No auth at all → 401.
	rr = doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat", nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status=%d body=%q, want 401", rr.Code, rr.Body.String())
	}

	ss.mu.Lock()
	if len(ss.touchCalls) != 0 {
		t.Fatalf("Touch must not run on auth failure, got %v", ss.touchCalls)
	}
	ss.mu.Unlock()
}

func TestHeartbeatUnknownViewer404(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	viewerID := "viewer-missing"
	// Not registered on stub → Touch returns false.

	tok := stream.SignStreamToken([]byte(streamSecret), viewerID, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "POST", "/api/v1/sessions/"+viewerID+"/heartbeat?token="+tok, nil, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown viewer status=%d body=%q, want 404", rr.Code, rr.Body.String())
	}
}

func TestHeartbeatAdvancesLastSeen(t *testing.T) {
	// Real Manager + fake clock: heartbeat must advance viewer's LastSeen.
	st, err := store.Open(filepath.Join(t.TempDir(), "hb-lastseen.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertDevice(store.Device{
		DeviceID: "dev-hb", IP: "127.0.0.1", Model: "fake", TunerCount: 2,
		Manual: true, LastSeen: now, StreamPort: 5004,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("dev-hb", []store.Channel{
		{DeviceID: "dev-hb", GuideNumber: "5.1", Name: "NEWS"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := st.ListChannels(false)
	if err != nil || len(chans) != 1 {
		t.Fatalf("channels: %v len=%d", err, len(chans))
	}
	chID := chans[0].ID
	if err := st.UpdateChannel(chID, true, ""); err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, st, "alice", "pass", "viewer")

	clockNow := now
	clock := func() time.Time { return clockNow }

	segDir := filepath.Join(t.TempDir(), "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ListenAddr: ":0",
		SegmentDir: segDir,
		Encoder:    "auto",
		FFmpegPath: "ffmpeg",
	}
	mgr := stream.NewManager(stream.ManagerDeps{
		TrackProbeTimeout: time.Millisecond, // tests: no PMT wait
		Cfg:               cfg,
		Store:             st,
		StreamURL: func(ch store.Channel) (string, error) {
			return "http://127.0.0.1/auto/v" + ch.GuideNumber, nil
		},
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
		Clock:  clock,
		Ingest: hangIngest(),
	})

	h, err := mgr.Start(context.Background(), user, chID, transcode.ClientCaps{
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	info, ok := mgr.SessionInfoOf(h.ViewerID)
	if !ok || len(info.Viewers) != 1 {
		t.Fatalf("SessionInfoOf: %+v ok=%v", info, ok)
	}
	if !info.Viewers[0].LastSeen.Equal(now) {
		t.Fatalf("initial LastSeen=%v, want %v", info.Viewers[0].LastSeen, now)
	}

	apiH := api.New(api.Deps{
		Cfg:               cfg,
		Store:             st,
		Auth:              &auth.Auth{Secret: []byte("0123456789abcdef0123456789abcdef"), Store: st},
		Streams:           mgr,
		StreamTokenSecret: []byte(streamSecret),
	})

	// Advance manager clock; heartbeat should stamp LastSeen to the new time.
	// Stream-token expiry is wall-clock (verifyStreamAccess uses time.Now), not the fake clock.
	clockNow = now.Add(20 * time.Second)
	tok := stream.SignStreamToken([]byte(streamSecret), h.ViewerID, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, apiH, "POST", "/api/v1/sessions/"+h.ViewerID+"/heartbeat?token="+tok, nil, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("heartbeat status=%d body=%q", rr.Code, rr.Body.String())
	}
	info, ok = mgr.SessionInfoOf(h.ViewerID)
	if !ok || len(info.Viewers) != 1 {
		t.Fatalf("after beat SessionInfoOf: %+v ok=%v", info, ok)
	}
	if !info.Viewers[0].LastSeen.Equal(clockNow) {
		t.Fatalf("LastSeen after heartbeat=%v, want %v", info.Viewers[0].LastSeen, clockNow)
	}
}

func TestAdminSessionsAndTerminate(t *testing.T) {
	ss := newStubStreams()
	ss.sessions = []stream.SessionInfo{{
		ID: "sess-kill", ChannelID: 2, ChannelName: "SPORTS",
		Key: "k", VideoCodec: "h264", Profile: "high", Backend: "software",
		StartedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}}
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}

	rr = doJSON(t, h, "GET", "/api/v1/admin/sessions", nil, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", rr.Code, rr.Body.String())
	}
	var list []stream.SessionInfo
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "sess-kill" {
		t.Fatalf("list=%+v", list)
	}

	rr = doJSON(t, h, "DELETE", "/api/v1/admin/sessions/sess-kill", nil, adminAuth)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("terminate status=%d", rr.Code)
	}
	ss.mu.Lock()
	if len(ss.terminated) != 1 || ss.terminated[0] != "sess-kill" {
		t.Fatalf("terminated=%v", ss.terminated)
	}
	ss.mu.Unlock()

	// Viewer forbidden.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer", "password": "viewerpass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	rr = doJSON(t, h, "GET", "/api/v1/admin/sessions", nil, map[string]string{
		"Authorization": "Bearer " + viewerTok.AccessToken,
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer list status=%d", rr.Code)
	}
}

func TestAdminTranscode(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "admin", "adminpass", "admin")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	rr = doJSON(t, h, "GET", "/api/v1/admin/transcode", nil, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var body struct {
		Available     []string        `json:"available"`
		HEVC          map[string]bool `json:"hevc"`
		FFmpegVersion string          `json:"ffmpegVersion"`
		Selected      string          `json:"selected"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Available) != 1 || body.Available[0] != "software" {
		t.Errorf("available=%v", body.Available)
	}
	if body.Selected != "software" {
		t.Errorf("selected=%q", body.Selected)
	}
	if body.FFmpegVersion != "test-8.0" {
		t.Errorf("ffmpegVersion=%q", body.FFmpegVersion)
	}
}

// --- E2E with real stream.Manager + hdhrfake + stub Runner -------------------

type e2eStubProcess struct {
	done   chan error
	stopCh chan struct{}
	once   sync.Once
}

func newE2EProc() *e2eStubProcess {
	return &e2eStubProcess{done: make(chan error, 1), stopCh: make(chan struct{})}
}
func (p *e2eStubProcess) Done() <-chan error { return p.done }
func (p *e2eStubProcess) Stop() {
	p.once.Do(func() {
		close(p.stopCh)
		select {
		case p.done <- errors.New("stopped"):
		default:
		}
	})
}

// apiHangBody blocks Read until Close — unit dial for fixtures without real TS.
type apiHangBody struct {
	done chan struct{}
	once sync.Once
}

func newAPIHangBody() *apiHangBody {
	return &apiHangBody{done: make(chan struct{})}
}

func (h *apiHangBody) Read(_ []byte) (int, error) {
	<-h.done
	return 0, io.EOF
}

func (h *apiHangBody) Close() error {
	h.once.Do(func() { close(h.done) })
	return nil
}

func hangIngest() *stream.IngestManager {
	return stream.NewIngestManager(func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		return newAPIHangBody(), 200, nil
	})
}

type e2eStubRunner struct {
	mu sync.Mutex
}

func (r *e2eStubRunner) Start(_ context.Context, spec transcode.JobSpec) (stream.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Drain ingest pipe so fan-out does not stall-force-Close the sub.
	if spec.Stdin != nil {
		go func() { _, _ = io.Copy(io.Discard, spec.Stdin) }()
	}
	// Realistic ready playlist with EXTINF + segment files.
	rung := strings.TrimSuffix(spec.Layout.ReadyPlaylist(), ".m3u8")
	m3u := mediaPlaylist(rung, "ts")
	if err := os.WriteFile(filepath.Join(spec.OutDir, rung+".m3u8"), []byte(m3u), 0o644); err != nil {
		return nil, err
	}
	for _, name := range []string{rung + "_00000.ts", rung + "_00001.ts"} {
		if err := os.WriteFile(filepath.Join(spec.OutDir, name), []byte("FAKE-TS-"+name), 0o644); err != nil {
			return nil, err
		}
	}
	return newE2EProc(), nil
}

func TestTunersBusyFilteredForViewers(t *testing.T) {
	// Two live sessions: one on an enabled channel, one on a disabled channel.
	// Viewer 503 must list only the enabled-channel session; admin sees both.
	ss := newStubStreams()
	ss.sessions = []stream.SessionInfo{
		{
			ID: "sess-enabled", ChannelID: 0, ChannelName: "NEWS",
			Key: "k1", VideoCodec: "h264", Profile: "original", Backend: "software",
			StartedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		},
		{
			ID: "sess-disabled", ChannelID: 0, ChannelName: "SECRET",
			Key: "k2", VideoCodec: "h264", Profile: "original", Backend: "software",
			StartedAt: time.Date(2026, 8, 4, 12, 1, 0, 0, time.UTC),
		},
	}
	ss.startFn = func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
		return stream.ViewerHandle{}, stream.ErrTunersBusy
	}
	h, st, _ := testAPIWithStreams(t, ss)

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertDevice(store.Device{
		DeviceID: "dev-503", IP: "127.0.0.1", Model: "fake", TunerCount: 2,
		Manual: true, LastSeen: now, StreamPort: 5004,
	}); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	if err := st.SyncLineup("dev-503", []store.Channel{
		{DeviceID: "dev-503", GuideNumber: "5.1", Name: "NEWS"},
		{DeviceID: "dev-503", GuideNumber: "9.1", Name: "SECRET"},
	}); err != nil {
		t.Fatalf("SyncLineup: %v", err)
	}
	chans, err := st.ListChannels(false)
	if err != nil || len(chans) != 2 {
		t.Fatalf("ListChannels: %v len=%d", err, len(chans))
	}
	var enabledID, disabledID int64
	for _, c := range chans {
		switch c.GuideNumber {
		case "5.1":
			enabledID = c.ID
			if err := st.UpdateChannel(c.ID, true, ""); err != nil {
				t.Fatal(err)
			}
		case "9.1":
			disabledID = c.ID
			// leave disabled
		}
	}
	ss.sessions[0].ChannelID = enabledID
	ss.sessions[1].ChannelID = disabledID

	seedUser(t, st, "alice", "pass", "viewer")
	seedUser(t, st, "admin", "adminpass", "admin")

	// Viewer: only enabled session.
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	viewerAuth := map[string]string{"Authorization": "Bearer " + viewerTok.AccessToken}
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": enabledID,
		"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
	}, viewerAuth)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("viewer 503 status=%d body=%q", rr.Code, rr.Body.String())
	}
	var viewerBody struct {
		Error    string               `json:"error"`
		Sessions []stream.SessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&viewerBody); err != nil {
		t.Fatal(err)
	}
	if len(viewerBody.Sessions) != 1 || viewerBody.Sessions[0].ID != "sess-enabled" {
		t.Fatalf("viewer sessions=%+v, want only sess-enabled", viewerBody.Sessions)
	}

	// Admin: both sessions.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": enabledID,
		"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
	}, adminAuth)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin 503 status=%d body=%q", rr.Code, rr.Body.String())
	}
	var adminBody struct {
		Sessions []stream.SessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&adminBody); err != nil {
		t.Fatal(err)
	}
	if len(adminBody.Sessions) != 2 {
		t.Fatalf("admin sessions=%+v, want 2", adminBody.Sessions)
	}
}

func TestAdminPreviewDisabledChannelE2E(t *testing.T) {
	fake := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "PREVDEV01",
		TunerCount: 2,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "7.1", GuideName: "PREVIEW"},
		},
	})
	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	deviceIP := u.Host

	st, err := store.Open(filepath.Join(t.TempDir(), "preview.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	segDir := filepath.Join(t.TempDir(), "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ListenAddr: ":0",
		SegmentDir: segDir,
		Encoder:    "auto",
		AllowHEVC:  false,
		FFmpegPath: "ffmpeg",
	}
	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	tuners := tuner.New(st, cfg)
	tuners.SetDiscoverFunc(func(ctx context.Context, timeout time.Duration) ([]hdhr.DiscoverInfo, error) {
		return nil, nil
	})
	mgr := stream.NewManager(stream.ManagerDeps{
		TrackProbeTimeout: time.Millisecond, // tests: no PMT wait
		Cfg:               cfg,
		Store:             st,
		Tuners:            tuners,
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
		Ingest: hangIngest(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Run(ctx)

	h := api.New(api.Deps{
		Cfg:               cfg,
		Store:             st,
		Auth:              a,
		Tuners:            tuners,
		Streams:           mgr,
		StreamTokenSecret: []byte(streamSecret),
		Probe: func() transcode.Capabilities {
			return transcode.Capabilities{
				Available: []transcode.Backend{transcode.BackendSoftware},
				HEVC:      map[transcode.Backend]bool{},
			}
		},
	})

	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}

	rr = doJSON(t, h, "POST", "/api/v1/admin/devices", map[string]string{"ip": deviceIP}, adminAuth)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("add device status=%d body=%q", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h, "GET", "/api/v1/admin/channels", nil, adminAuth)
	var adminChans []struct {
		ID      int64 `json:"id"`
		Enabled bool  `json:"enabled"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&adminChans); err != nil {
		t.Fatal(err)
	}
	if len(adminChans) == 0 {
		t.Fatal("no channels")
	}
	chID := adminChans[0].ID
	// Channel stays disabled — admin preview must still work.
	if adminChans[0].Enabled {
		t.Fatal("expected newly synced channel to be disabled by default")
	}

	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": chID,
		"caps": map[string]any{
			"videoCodecs": []string{"h264"},
			"audioCodecs": []string{"aac"},
		},
	}, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin preview create status=%d body=%q", rr.Code, rr.Body.String())
	}
	var sessResp struct {
		ViewerID    string `json:"viewerId"`
		PlaylistURL string `json:"playlistUrl"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&sessResp); err != nil {
		t.Fatal(err)
	}
	if sessResp.PlaylistURL == "" {
		t.Fatal("empty playlistUrl")
	}
	rr = doJSON(t, h, "GET", sessResp.PlaylistURL, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin preview playlist status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Viewer still gets 404 for the same disabled channel.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer", "password": "viewerpass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": chID,
		"caps": map[string]any{
			"videoCodecs": []string{"h264"},
			"audioCodecs": []string{"aac"},
		},
	}, map[string]string{"Authorization": "Bearer " + viewerTok.AccessToken})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("viewer disabled status=%d body=%q, want 404", rr.Code, rr.Body.String())
	}
}

func TestE2EStreamLifecycle(t *testing.T) {
	fake := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "E2EDEV01",
		TunerCount: 2,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "5.1", GuideName: "WABC"},
		},
	})
	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	deviceIP := u.Host

	st, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	segDir := filepath.Join(t.TempDir(), "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		ListenAddr: ":0",
		SegmentDir: segDir,
		Encoder:    "auto",
		AllowHEVC:  false,
		FFmpegPath: "ffmpeg",
	}

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	tuners := tuner.New(st, cfg)
	tuners.SetDiscoverFunc(func(ctx context.Context, timeout time.Duration) ([]hdhr.DiscoverInfo, error) {
		return nil, nil
	})

	mgr := stream.NewManager(stream.ManagerDeps{
		TrackProbeTimeout: time.Millisecond, // tests: no PMT wait
		Cfg:               cfg,
		Store:             st,
		Tuners:            tuners,
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
		// Real HTTP dial so ActiveStreams / tuner-busy paths hit the fake device.
		Ingest: stream.NewIngestManager(stream.HTTPDial),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Run(ctx)

	h := api.New(api.Deps{
		Cfg:               cfg,
		Store:             st,
		Auth:              a,
		Tuners:            tuners,
		Streams:           mgr,
		StreamTokenSecret: []byte(streamSecret),
		Probe: func() transcode.Capabilities {
			return transcode.Capabilities{
				Available: []transcode.Backend{transcode.BackendSoftware},
				HEVC:      map[transcode.Backend]bool{},
			}
		},
	})

	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	// Admin: add device + enable channel.
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}

	rr = doJSON(t, h, "POST", "/api/v1/admin/devices", map[string]string{"ip": deviceIP}, adminAuth)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("add device status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h, "GET", "/api/v1/admin/channels", nil, adminAuth)
	var adminChans []struct {
		ID          int64  `json:"id"`
		GuideNumber string `json:"guideNumber"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&adminChans); err != nil {
		t.Fatal(err)
	}
	if len(adminChans) == 0 {
		t.Fatal("no channels")
	}
	chID := adminChans[0].ID
	rr = doJSON(t, h, "PATCH", "/api/v1/admin/channels/"+strconv.FormatInt(chID, 10), map[string]any{
		"enabled": true,
	}, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable channel status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Viewer: login → channels → create session → playlist → segment → DELETE.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer", "password": "viewerpass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	viewerAuth := map[string]string{"Authorization": "Bearer " + viewerTok.AccessToken}

	rr = doJSON(t, h, "GET", "/api/v1/channels", nil, viewerAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("channels status=%d", rr.Code)
	}
	var chans []struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&chans); err != nil {
		t.Fatal(err)
	}
	if len(chans) != 1 || chans[0].ID != chID {
		t.Fatalf("viewer channels=%+v", chans)
	}

	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": chID,
		"caps": map[string]any{
			"videoCodecs": []string{"h264"},
			"audioCodecs": []string{"aac"},
			"maxHeight":   0,
			"profile":     "high",
		},
	}, viewerAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%q", rr.Code, rr.Body.String())
	}
	var sessResp struct {
		ViewerID    string `json:"viewerId"`
		PlaylistURL string `json:"playlistUrl"`
		Session     *struct {
			VideoCodec  string `json:"videoCodec"`
			Profile     string `json:"profile"`
			Backend     string `json:"backend"`
			ChannelName string `json:"channelName"`
		} `json:"session"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&sessResp); err != nil {
		t.Fatal(err)
	}
	if sessResp.ViewerID == "" || !strings.Contains(sessResp.PlaylistURL, "token=") {
		t.Fatalf("session resp=%+v", sessResp)
	}
	if sessResp.Session == nil {
		t.Fatal("create session response missing session object")
	}
	if sessResp.Session.VideoCodec != "h264" {
		t.Errorf("session.videoCodec=%q, want h264", sessResp.Session.VideoCodec)
	}
	if sessResp.Session.Profile != "high" {
		t.Errorf("session.profile=%q, want high", sessResp.Session.Profile)
	}
	if sessResp.Session.Backend != "software" {
		t.Errorf("session.backend=%q, want software", sessResp.Session.Backend)
	}
	if sessResp.Session.ChannelName != "WABC" {
		t.Errorf("session.channelName=%q, want WABC", sessResp.Session.ChannelName)
	}

	// Fetch the master, then its first variant — assert rewrite.
	rr = doJSON(t, h, "GET", sessResp.PlaylistURL, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("master status=%d body=%q", rr.Code, rr.Body.String())
	}
	var variant string
	for _, line := range strings.Split(rr.Body.String(), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			variant = line
			break
		}
	}
	if !strings.HasPrefix(variant, "v") || !strings.Contains(variant, ".m3u8?token=") {
		t.Fatalf("master has no variant:\n%s", rr.Body.String())
	}
	rr = doJSON(t, h, "GET", "/api/v1/stream/"+sessResp.ViewerID+"/"+variant, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("playlist status=%d body=%q", rr.Code, rr.Body.String())
	}
	pl := rr.Body.String()
	rung := strings.SplitN(variant, ".", 2)[0]
	if !strings.Contains(pl, "/api/v1/stream/"+sessResp.ViewerID+"/"+rung+"_00000.ts?token=") {
		t.Fatalf("playlist not rewritten:\n%s", pl)
	}
	if !strings.Contains(pl, "#EXTINF") {
		t.Fatalf("missing EXTINF:\n%s", pl)
	}

	// Extract first segment URL and fetch.
	var segURL string
	for _, line := range strings.Split(pl, "\n") {
		if strings.HasPrefix(line, "/api/v1/stream/") && strings.Contains(line, "_00000.ts") {
			segURL = line
			break
		}
	}
	if segURL == "" {
		t.Fatalf("no segment URL in playlist:\n%s", pl)
	}
	rr = doJSON(t, h, "GET", segURL, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("segment status=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "video/mp2t" {
		t.Errorf("segment Content-Type=%q", rr.Header().Get("Content-Type"))
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "FAKE-TS-"+rung+"_00000.ts") {
		t.Errorf("segment body=%q", body)
	}

	// DELETE session.
	rr = doJSON(t, h, "DELETE", "/api/v1/sessions/"+sessResp.ViewerID, nil, viewerAuth)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Playlist should 404 after stop (viewer gone).
	rr = doJSON(t, h, "GET", sessResp.PlaylistURL, nil, nil)
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusForbidden {
		// Touch returns false → 404; acceptable either way once viewer stopped.
		t.Logf("post-delete playlist status=%d (ok if not 200)", rr.Code)
	}
	if rr.Code == http.StatusOK {
		t.Fatal("playlist still 200 after viewer stop")
	}
}

// TestStartDial503SurfacesTunersBusy: fake with 0 free tuners → 503 payload shape via errors.Is.
func TestStartDial503SurfacesTunersBusy(t *testing.T) {
	// Occupy both tuners so the next dial gets 503.
	fake := hdhrfake.New(t, hdhrfake.Options{
		DeviceID:   "BUSY01",
		TunerCount: 1,
		Lineup: []hdhrfake.LineupEntry{
			{GuideNumber: "5.1", GuideName: "NEWS"},
			{GuideNumber: "7.1", GuideName: "OTHER"},
		},
	})
	// Hold the single tuner open.
	resp, err := http.Get(fake.URL + "/auto/v7.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hold stream status=%d", resp.StatusCode)
	}

	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "busy.db"))
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
		TrackProbeTimeout: time.Millisecond, // tests: no PMT wait
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
	h := api.New(api.Deps{
		Cfg: cfg, Store: st, Auth: a, Tuners: tuners, Streams: mgr,
		StreamTokenSecret: []byte(streamSecret),
	})
	seedUser(t, st, "alice", "pass", "viewer")
	seedUser(t, st, "admin", "adminpass", "admin")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	adminAuth := map[string]string{"Authorization": "Bearer " + adminTok.AccessToken}
	rr = doJSON(t, h, "POST", "/api/v1/admin/devices", map[string]string{"ip": u.Host}, adminAuth)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("add device: %d %s", rr.Code, rr.Body.String())
	}
	rr = doJSON(t, h, "GET", "/api/v1/admin/channels", nil, adminAuth)
	var chans []struct {
		ID          int64  `json:"id"`
		GuideNumber string `json:"guideNumber"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&chans); err != nil {
		t.Fatal(err)
	}
	var chID int64
	for _, c := range chans {
		if c.GuideNumber == "5.1" {
			chID = c.ID
		}
	}
	if chID == 0 {
		t.Fatal("channel 5.1 not found")
	}
	rr = doJSON(t, h, "PATCH", "/api/v1/admin/channels/"+strconv.FormatInt(chID, 10), map[string]any{
		"enabled": true,
	}, adminAuth)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable: %d", rr.Code)
	}

	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": chID,
		"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
	}, map[string]string{"Authorization": "Bearer " + viewerTok.AccessToken})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q, want 503", rr.Code, rr.Body.String())
	}
	var body struct {
		Error      string               `json:"error"`
		Sessions   []stream.SessionInfo `json:"sessions"`
		OtherInUse *int                 `json:"otherInUse"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "all tuners in use" {
		t.Fatalf("error=%q", body.Error)
	}
	// sessions field present (may be empty — no bowtie sessions holding tuners).
	if body.Sessions == nil {
		t.Fatal("sessions field missing")
	}
	// The one tuner is held by something other than Bowtie (e.g. Plex).
	if body.OtherInUse == nil || *body.OtherInUse != 1 {
		t.Fatalf("otherInUse = %v, want 1", body.OtherInUse)
	}
}

func TestCreateSessionStartErrors(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		status      int
		viewerMsg   string // exact
		adminSubstr string // admins also see the cause
	}{
		{
			name:        "no signal",
			err:         fmt.Errorf("ingest dial: %w", &stream.DeviceError{Status: 503, Reason: "807 No Video Data"}),
			status:      http.StatusBadGateway,
			viewerMsg:   "no signal on this channel",
			adminSubstr: "807 No Video Data",
		},
		{
			name:        "ffmpeg died",
			err:         errors.New("ffmpeg exited before playlist ready: exit status 1"),
			status:      http.StatusInternalServerError,
			viewerMsg:   "failed to start session",
			adminSubstr: "ffmpeg exited before playlist ready: exit status 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ss := newStubStreams()
			ss.startFn = func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
				return stream.ViewerHandle{}, tc.err
			}
			h, st, _ := testAPIWithStreams(t, ss)
			seedUser(t, st, "viewer1", "pass", "viewer")
			seedUser(t, st, "admin1", "pass", "admin")
			for _, who := range []string{"viewer1", "admin1"} {
				rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": who, "password": "pass"}, nil)
				authH := map[string]string{"Authorization": "Bearer " + decodeLogin(t, rr).AccessToken}
				rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
					"channelId": 1,
					"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
				}, authH)
				if rr.Code != tc.status {
					t.Fatalf("%s: status=%d body=%q", who, rr.Code, rr.Body.String())
				}
				var body struct {
					Error string `json:"error"`
				}
				if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
					t.Fatalf("%s: decode: %v", who, err)
				}
				if who == "viewer1" && body.Error != tc.viewerMsg {
					t.Errorf("viewer error=%q, want %q", body.Error, tc.viewerMsg)
				}
				if who == "admin1" && (!strings.HasPrefix(body.Error, tc.viewerMsg) || !strings.Contains(body.Error, tc.adminSubstr)) {
					t.Errorf("admin error=%q, want %q plus %q", body.Error, tc.viewerMsg, tc.adminSubstr)
				}
			}
		})
	}
}

// Viewers see each channel's last known reception so the apps can mark
// channels the antenna can't receive right now.
func TestChannelsIncludeReception(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	if err := st.UpsertDevice(store.Device{DeviceID: "dev-rx", IP: "127.0.0.1", Model: "fake", TunerCount: 2, Manual: true, LastSeen: now, StreamPort: 5004}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("dev-rx", []store.Channel{
		{DeviceID: "dev-rx", GuideNumber: "9.1", Name: "FOX 9"},
		{DeviceID: "dev-rx", GuideNumber: "11.1", Name: "KARE"},
	}); err != nil {
		t.Fatal(err)
	}
	chans, err := st.ListChannels(false)
	if err != nil || len(chans) != 2 {
		t.Fatalf("channels: %v len=%d", err, len(chans))
	}
	ids := map[string]int64{}
	for _, c := range chans {
		ids[c.GuideNumber] = c.ID
		if err := st.UpdateChannel(c.ID, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	ss.reception = map[int64]stream.Reception{ids["11.1"]: {State: stream.ReceptionNoSignal, CheckedAt: now}}
	seedUser(t, st, "viewer1", "pass", "viewer")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "viewer1", "password": "pass"}, nil)
	authH := map[string]string{"Authorization": "Bearer " + decodeLogin(t, rr).AccessToken}
	rr = doJSON(t, h, "GET", "/api/v1/channels", nil, authH)
	var got []struct {
		GuideNumber        string  `json:"guideNumber"`
		Reception          string  `json:"reception"`
		ReceptionCheckedAt *string `json:"receptionCheckedAt"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for i, c := range got {
		by[c.GuideNumber] = i
	}
	kare, fox := got[by["11.1"]], got[by["9.1"]]
	if kare.Reception != "noSignal" || kare.ReceptionCheckedAt == nil || *kare.ReceptionCheckedAt != "2026-10-03T10:00:00Z" {
		t.Errorf("11.1 = %+v, want noSignal at 2026-10-03T10:00:00Z", kare)
	}
	if fox.Reception != "unknown" || fox.ReceptionCheckedAt != nil {
		t.Errorf("9.1 = %+v, want unknown with no timestamp", fox)
	}
}

func TestIndexIsPerViewerMaster(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	full, capped := "aabbccddeeff00112233445566778899", "bbbbccddeeff00112233445566778899"
	ss.register(full, dir)
	ss.registerCapped(capped, dir, 480)
	get := func(v string) string {
		tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
		rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/index.m3u8?token="+tok, nil, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
		}
		return rr.Body.String()
	}
	if body := get(full); !strings.Contains(body, "v720.m3u8?token=") || !strings.Contains(body, `GROUP-ID="ac3"`) || !strings.Contains(body, `SUBTITLES="subs"`) {
		t.Fatalf("full master:\n%s", body)
	}
	if body := get(capped); strings.Contains(body, "\nv720.m3u8") || !strings.Contains(body, "\nv480.m3u8") {
		t.Fatalf("capped master must omit 720:\n%s", body)
	}
	_ = os.Remove(filepath.Join(dir, "v720_vtt.m3u8"))
	if strings.Contains(get(full), "SUBTITLES") {
		t.Fatal("captions listed before the caption playlist exists")
	}
}

func TestRenditionPlaylistsTouchAndRewrite(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	for name, seg := range map[string]string{"v720.m3u8": "v720_00000.ts", "aac0.m3u8": "aac0_00000.ts", "v720_vtt.m3u8": "v7200.vtt"} {
		rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/"+name+"?token="+tok, nil, nil)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/api/v1/stream/"+v+"/"+seg+"?token="+tok) {
			t.Fatalf("%s: %d\n%s", name, rr.Code, rr.Body.String())
		}
	}
	ss.mu.Lock()
	n := len(ss.touchCalls)
	ss.mu.Unlock()
	if n != 3 {
		t.Fatalf("every rendition playlist fetch must Touch; touches=%d", n)
	}
}

func TestVTTSegmentGetsTimestampMap(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/v7200.vtt?token="+tok, nil, nil)
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/vtt") {
		t.Fatalf("status=%d ct=%q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(rr.Body.String(), "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n") {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

func TestStreamFileNamesValidated(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, t.TempDir())
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	for _, bad := range []string{"seg00000.ts", "live.m3u8", "x.m3u8", "v72_00000.ts", "aac9.m3u8", "v720.vtt", "..%2Fv720.m3u8"} {
		if rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/"+bad+"?token="+tok, nil, nil); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d want 400", bad, rr.Code)
		}
	}
}

// The caption playlist endpoint repairs FFmpeg 5.1's append_list mangling
// (see repairCaptionPlaylist): names rebuilt, ENDLIST dropped.
func TestCaptionPlaylistEndpointRepairsFFmpeg51Append(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	for _, name := range []string{"v720_vtt.m3u8", "v720.m3u8"} {
		b, err := os.ReadFile("testdata/ffmpeg51-append-" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v := "aabbccddeeff00112233445566778899"
	ss.register(v, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), v, time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "GET", "/api/v1/stream/"+v+"/v720_vtt.m3u8?token="+tok, nil, nil)
	body := rr.Body.String()
	if rr.Code != 200 || strings.Contains(body, "#EXT-X-ENDLIST") || !strings.Contains(body, "/api/v1/stream/"+v+"/v7200.vtt?token=") {
		t.Fatalf("status=%d body:\n%s", rr.Code, body)
	}
}

// A viewer parental controls stopped gets 403 {code: parental} with the
// reason on its next heartbeat or playlist request, not a bare 404.
func TestBlockedViewerGets403WithReason(t *testing.T) {
	ss := newStubStreams()
	ss.blocked = map[string]string{"vb": "Blocked by parental controls (rated TV-MA)"}
	h, _, _ := testAPIWithStreams(t, ss)
	tok := stream.SignStreamToken([]byte(streamSecret), "vb", time.Now().UTC().Add(time.Hour))
	for _, path := range []string{
		"/api/v1/sessions/vb/heartbeat?token=" + tok,
		"/api/v1/stream/vb/index.m3u8?token=" + tok,
	} {
		method := "GET"
		if strings.Contains(path, "heartbeat") {
			method = "POST"
		}
		rr := doJSON(t, h, method, path, nil, nil)
		if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), `"code":"parental"`) || !strings.Contains(rr.Body.String(), "TV-MA") {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
		}
	}
}
