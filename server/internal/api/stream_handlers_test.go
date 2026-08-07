package api_test

import (
	"context"
	"encoding/json"
	"errors"
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
	touchCalls []string
	stopped    []string
	terminated []string
	sessions   []stream.SessionInfo
	dirs       map[string]string // viewerID → dir
	viewers    map[string]bool
}

func newStubStreams() *stubStreams {
	return &stubStreams{
		dirs:    make(map[string]string),
		viewers: make(map[string]bool),
	}
}

func (s *stubStreams) Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error) {
	if s.startFn != nil {
		return s.startFn(ctx, user, channelID, caps)
	}
	return stream.ViewerHandle{}, errors.New("start not configured")
}

func (s *stubStreams) Touch(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touchCalls = append(s.touchCalls, id)
	return s.viewers[id]
}

func (s *stubStreams) StopViewer(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, id)
	delete(s.viewers, id)
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

func (s *stubStreams) register(viewerID, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirs[viewerID] = dir
	s.viewers[viewerID] = true
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

func fixturePlaylist() string {
	return strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXT-X-TARGETDURATION:4",
		"#EXT-X-MEDIA-SEQUENCE:0",
		"#EXTINF:4.000000,",
		"seg00000.ts",
		"#EXTINF:4.000000,",
		"seg00001.ts",
		"#EXT-X-ENDLIST",
		"",
	}, "\n")
}

func writeFixtureSession(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "live.m3u8"), []byte(fixturePlaylist()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seg00000.ts"), []byte("TSSEG0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seg00001.ts"), []byte("TSSEG1"), 0o644); err != nil {
		t.Fatal(err)
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
	path := "/api/v1/stream/" + viewerID + "/index.m3u8?token=" + tok
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
	want0 := "/api/v1/stream/" + viewerID + "/seg00000.ts?token=" + tok
	want1 := "/api/v1/stream/" + viewerID + "/seg00001.ts?token=" + tok
	if !strings.Contains(body, want0) || !strings.Contains(body, want1) {
		t.Fatalf("playlist rewrite missing URLs:\n%s", body)
	}
	if strings.Contains(body, "\nseg00000.ts\n") {
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
	path := "/api/v1/stream/" + viewerID + "/seg00000.ts?token=" + tok
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
		return stream.ViewerHandle{}, errors.New("all tuners in use")
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
		Cfg:   cfg,
		Store: st,
		StreamURL: func(ch store.Channel) (string, error) {
			return "http://127.0.0.1/auto/v" + ch.GuideNumber, nil
		},
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
		Clock:  clock,
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

type e2eStubRunner struct {
	mu sync.Mutex
}

func (r *e2eStubRunner) Start(_ context.Context, spec transcode.JobSpec) (stream.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Realistic live.m3u8 with EXTINF + segment files.
	m3u := fixturePlaylist()
	if err := os.WriteFile(filepath.Join(spec.OutDir, "live.m3u8"), []byte(m3u), 0o644); err != nil {
		return nil, err
	}
	for _, name := range []string{"seg00000.ts", "seg00001.ts"} {
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
		return stream.ViewerHandle{}, errors.New("all tuners in use")
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
		Cfg:    cfg,
		Store:  st,
		Tuners: tuners,
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
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
		Cfg:    cfg,
		Store:  st,
		Tuners: tuners,
		Caps: transcode.Capabilities{
			Available: []transcode.Backend{transcode.BackendSoftware},
			HEVC:      map[transcode.Backend]bool{},
		},
		Runner: &e2eStubRunner{},
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

	// Fetch playlist — assert rewrite.
	rr = doJSON(t, h, "GET", sessResp.PlaylistURL, nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("playlist status=%d body=%q", rr.Code, rr.Body.String())
	}
	pl := rr.Body.String()
	if !strings.Contains(pl, "/api/v1/stream/"+sessResp.ViewerID+"/seg00000.ts?token=") {
		t.Fatalf("playlist not rewritten:\n%s", pl)
	}
	if !strings.Contains(pl, "#EXTINF") {
		t.Fatalf("missing EXTINF:\n%s", pl)
	}

	// Extract first segment URL and fetch.
	var segURL string
	for _, line := range strings.Split(pl, "\n") {
		if strings.HasPrefix(line, "/api/v1/stream/") && strings.Contains(line, "seg00000.ts") {
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
	if !strings.Contains(string(body), "FAKE-TS-seg00000.ts") {
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

