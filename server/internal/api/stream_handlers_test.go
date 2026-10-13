package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	mu            sync.Mutex
	startFn       func(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	joinFn        func(ctx context.Context, user store.User, sessionID string, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	touchCalls    []string
	stopped       []string
	terminated    []string
	sessions      []stream.SessionInfo
	dirs          map[string]string // viewerID → dir
	viewers       map[string]bool
	reception     map[int64]stream.Reception
	media         map[string]stream.SessionMedia // viewerID → media
	blocked       map[string]string              // viewerID → parental reason
	onStop        func(id string)
	retime        func(viewerID, rendition string, raw []byte) ([]byte, bool)
	timelines     map[string]timelineStub // viewerID → window + owner
	timelineBuilt []string                // viewers whose timeline was built
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

func (s *stubStreams) RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool) {
	if s.retime == nil {
		// Tests that don't install a timeline still serve the file they wrote.
		return raw, true
	}
	return s.retime(viewerID, rendition, raw)
}

type timelineStub struct {
	owner int64
	win   stream.WindowTimeline
}

func (s *stubStreams) ViewerOwner(viewerID string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tl, ok := s.timelines[viewerID]
	return tl.owner, ok
}

// TimelineOf records that a timeline was built, so the test can check that
// authorization ran first.
func (s *stubStreams) TimelineOf(viewerID string) (stream.WindowTimeline, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timelineBuilt = append(s.timelineBuilt, viewerID)
	tl, ok := s.timelines[viewerID]
	return tl.win, ok
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
	// Realistic rendition playlists with EXTINF + segment files. Each file starts
	// with a TS packet that carries a PTS: the session timeline publishes a
	// segment only once that PTS is read, and an unpublished segment is left
	// out of the served playlist. The FAKE-TS marker stays so the lifecycle
	// test can still read the file back.
	for _, name := range spec.Layout.Renditions() {
		m3u := mediaPlaylist(name, "ts")
		if err := os.WriteFile(filepath.Join(spec.OutDir, name+".m3u8"), []byte(m3u), 0o644); err != nil {
			return nil, err
		}
		for i, seg := range []string{name + "_00000.ts", name + "_00001.ts"} {
			if err := os.WriteFile(filepath.Join(spec.OutDir, seg), segmentWithPTS(seg, 1.4+4*float64(i)), 0o644); err != nil {
				return nil, err
			}
		}
	}
	return newE2EProc(), nil
}

// segmentWithPTS is one TS packet whose first PES has pts (seconds), then the
// FAKE-TS marker the lifecycle test expects in the segment body.
func segmentWithPTS(name string, pts float64) []byte {
	v := int64(math.Round(pts*90000)) & (int64(1)<<33 - 1)
	p := make([]byte, 188)
	for i := range p {
		p[i] = 0xFF
	}
	copy(p, []byte{0x47, 0x41, 0x00, 0x10, 0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 5})
	p[13] = byte(0x21 | (v>>29)&0x0E)
	p[14] = byte(v >> 22)
	p[15] = byte((v>>14)&0xFE | 1)
	p[16] = byte(v >> 7)
	p[17] = byte((v<<1)&0xFE | 1)
	return append(p, []byte("FAKE-TS-"+name)...)
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
			viewerMsg:   "This channel isn't coming in right now — your antenna isn't getting a picture from it. Try again later or pick another channel.",
			adminSubstr: "(Details: ingest dial: device returned HTTP 503: 807 No Video Data)",
		},
		{
			name:        "ffmpeg died",
			err:         errors.New("ffmpeg exited before playlist ready: exit status 1"),
			status:      http.StatusInternalServerError,
			viewerMsg:   "Something went wrong starting this channel. Try again in a moment.",
			adminSubstr: "(Details: ffmpeg exited before playlist ready: exit status 1)",
		},
		{
			name:        "can't play on this device",
			err:         errors.New("negotiate: no compatible video codec for client"),
			status:      http.StatusUnprocessableEntity,
			viewerMsg:   "This channel can't play on this device.",
			adminSubstr: "(Details: negotiate: no compatible video codec for client)",
		},
		{
			name:        "channel gone",
			err:         errors.New("unknown channel 7"),
			status:      http.StatusNotFound,
			viewerMsg:   "This channel isn't available anymore.",
			adminSubstr: "",
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

// Rendition and caption playlists go through the session timeline before
// segment URLs are signed, each under its own name (the timeline maps the
// captions to their rung's stamps and keeps a MEDIA-SEQUENCE floor each).
func TestMediaPlaylistServedThroughTimeline(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	// FFmpeg 5.1 append shape: "." instead of a caption name, plus ENDLIST.
	// repairCaptionPlaylist rebuilds v7200.vtt and drops ENDLIST. retime has
	// to see that, with segment names still bare.
	mangled := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.000000,\n.\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(dir, "v720_vtt.m3u8"), []byte(mangled), 0o644); err != nil {
		t.Fatal(err)
	}
	const repairedVTT = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.000000,\nv7200.vtt\n"
	var renditions []string
	ss.retime = func(viewerID, rendition string, raw []byte) ([]byte, bool) {
		renditions = append(renditions, rendition)
		body := string(raw)
		if strings.Contains(body, "/api/v1/stream/") {
			t.Fatalf("retime of %s saw rewritten URLs:\n%s", rendition, body)
		}
		switch rendition {
		case "v720":
			if !strings.Contains(body, "\nv720_00000.ts\n") {
				t.Fatalf("retime of v720 missing bare segment name:\n%s", body)
			}
		case "aac0":
			if !strings.Contains(body, "\naac0_00000.ts\n") {
				t.Fatalf("retime of aac0 missing bare segment name:\n%s", body)
			}
		case "v720_vtt":
			if body != repairedVTT {
				t.Fatalf("retime of v720_vtt saw:\n%q\nwant repaired:\n%q", body, repairedVTT)
			}
		}
		return append([]byte("#EXT-X-BOWTIE-TIMELINE\n"), raw...), true
	}
	ss.register("viewer-a", dir)
	tok := stream.SignStreamToken([]byte(streamSecret), "viewer-a", time.Now().UTC().Add(time.Hour))
	for _, name := range []string{"v720.m3u8", "aac0.m3u8", "v720_vtt.m3u8"} {
		rr := doJSON(t, h, "GET", "/api/v1/stream/viewer-a/"+name+"?token="+tok, nil, nil)
		body := rr.Body.String()
		if rr.Code != http.StatusOK || !strings.HasPrefix(body, "#EXT-X-BOWTIE-TIMELINE\n") ||
			!strings.Contains(body, "/api/v1/stream/viewer-a/") {
			t.Fatalf("%s: status %d body:\n%s", name, rr.Code, body)
		}
	}
	if got := strings.Join(renditions, ","); got != "v720,aac0,v720_vtt" {
		t.Fatalf("retimed playlists %s, want v720,aac0,v720_vtt", got)
	}
}

// A session removed after the playlist was read (budget teardown, admin
// terminate) makes RetimePlaylist return false. The handler answers as it
// does for a gone viewer and does not serve FFmpeg's bytes.
func TestRetimeMissDoesNotServeRawPlaylist(t *testing.T) {
	ss := newStubStreams()
	ss.retime = func(viewerID, rendition string, raw []byte) ([]byte, bool) {
		return nil, false
	}
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	ss.register("viewer-a", dir)
	tok := stream.SignStreamToken([]byte(streamSecret), "viewer-a", time.Now().UTC().Add(time.Hour))
	rr := doJSON(t, h, "GET", "/api/v1/stream/viewer-a/v720.m3u8?token="+tok, nil, nil)
	body := rr.Body.String()
	if rr.Code == http.StatusOK || strings.Contains(body, "#EXTM3U") || strings.Contains(body, "v720_00000.ts") {
		t.Fatalf("raw playlist served: status %d body:\n%s", rr.Code, body)
	}
	if rr.Code != http.StatusNotFound || !strings.Contains(body, "viewer not found") {
		t.Fatalf("status %d body %s, want 404 viewer not found", rr.Code, body)
	}
}

// Only the owner's Bearer token sees the timeline. Another user (even an
// admin) gets the same 404 as a missing viewer; no Bearer (a stream token
// included) gets 401. No timeline is built for a refused request.
func TestSessionTimelineOwnerOnly(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	alice := seedUser(t, st, "alice", "pass-a", "viewer")
	seedUser(t, st, "bob", "pass-b", "admin")
	start := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	ss.timelines = map[string]timelineStub{"viewer-a": {owner: alice.ID, win: stream.WindowTimeline{
		MediaSequence: 7,
		Segments: []stream.TimelineSegment{
			{Start: start, Duration: 4.004},
			{Start: start.Add(4004 * time.Millisecond), Duration: 4.004},
		},
	}}}
	login := func(user, pass string) string {
		rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("login %s: %d", user, rr.Code)
		}
		return decodeLogin(t, rr).AccessToken
	}
	aliceTok, bobTok := login("alice", "pass-a"), login("bob", "pass-b")
	const path = "/api/v1/sessions/viewer-a/timeline"
	viewerTok := stream.SignStreamToken([]byte(streamSecret), "viewer-a", time.Now().UTC().Add(time.Hour))

	for name, req := range map[string]struct {
		path string
		hdr  map[string]string
		want int
	}{
		"another user's bearer (admin)": {path, map[string]string{"Authorization": "Bearer " + bobTok}, http.StatusNotFound},
		"owner, unknown viewer":         {"/api/v1/sessions/viewer-x/timeline", map[string]string{"Authorization": "Bearer " + aliceTok}, http.StatusNotFound},
		"viewer's own stream token":     {path + "?token=" + viewerTok, nil, http.StatusUnauthorized},
		"no auth":                       {path, nil, http.StatusUnauthorized},
		"garbage bearer":                {path, map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
	} {
		if rr := doJSON(t, h, "GET", req.path, nil, req.hdr); rr.Code != req.want {
			t.Errorf("%s: status %d, want %d", name, rr.Code, req.want)
		}
	}
	if len(ss.timelineBuilt) != 0 {
		t.Fatalf("timelines built for refused requests: %v", ss.timelineBuilt)
	}

	rr := doJSON(t, h, "GET", path, nil, map[string]string{"Authorization": "Bearer " + aliceTok})
	if rr.Code != http.StatusOK {
		t.Fatalf("owner: status %d body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		MediaSequence int64   `json:"mediaSequence"`
		WindowStart   string  `json:"windowStart"`
		LiveEdge      string  `json:"liveEdge"`
		Segments      [][]any `json:"segments"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.MediaSequence != 7 || body.WindowStart != "2026-10-11T20:00:00.000Z" || body.LiveEdge != "2026-10-11T20:00:08.008Z" ||
		len(body.Segments) != 2 || body.Segments[1][0] != "2026-10-11T20:00:04.004Z" || body.Segments[1][1] != 4.004 {
		t.Fatalf("body %+v", body)
	}
}

// An empty window is null bounds and an empty segments array. A start 900 µs
// past the second rounds to the next millisecond, and the live edge is that
// start plus 4.004 s, not a millisecond off from truncating the duration.
func TestSessionTimelineEmptyWindowAndSubMillisecondStart(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	alice := seedUser(t, st, "alice", "pass-a", "viewer")
	start := time.Date(2026, 10, 11, 20, 0, 0, 900_000, time.UTC)
	ss.timelines = map[string]timelineStub{
		"viewer-empty": {owner: alice.ID, win: stream.WindowTimeline{MediaSequence: 3}},
		"viewer-subms": {owner: alice.ID, win: stream.WindowTimeline{
			MediaSequence: 1,
			Segments:      []stream.TimelineSegment{{Start: start, Duration: 4.004}},
		}},
	}
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "pass-a"}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d", rr.Code)
	}
	hdr := map[string]string{"Authorization": "Bearer " + decodeLogin(t, rr).AccessToken}

	empty := doJSON(t, h, "GET", "/api/v1/sessions/viewer-empty/timeline", nil, hdr)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty: status %d body %s", empty.Code, empty.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(empty.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["mediaSequence"]) != "3" || string(raw["windowStart"]) != "null" ||
		string(raw["liveEdge"]) != "null" || string(raw["segments"]) != "[]" {
		t.Fatalf("empty window: %s", empty.Body.String())
	}

	sub := doJSON(t, h, "GET", "/api/v1/sessions/viewer-subms/timeline", nil, hdr)
	if sub.Code != http.StatusOK {
		t.Fatalf("sub-ms: status %d body %s", sub.Code, sub.Body.String())
	}
	var body struct {
		WindowStart string  `json:"windowStart"`
		LiveEdge    string  `json:"liveEdge"`
		Segments    [][]any `json:"segments"`
	}
	if err := json.Unmarshal(sub.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.WindowStart != "2026-10-11T20:00:00.001Z" || body.LiveEdge != "2026-10-11T20:00:04.005Z" ||
		len(body.Segments) != 1 || body.Segments[0][0] != "2026-10-11T20:00:00.001Z" || body.Segments[0][1] != 4.004 {
		t.Fatalf("sub-ms window: %+v", body)
	}
}

func TestIndexOmitsMissingRenditions(t *testing.T) {
	ss := newStubStreams()
	h, _, _ := testAPIWithStreams(t, ss)
	dir := t.TempDir()
	writeFixtureSession(t, dir)
	viewer := "aabbccddeeff00112233445566778899"
	ss.register(viewer, dir)
	tok := stream.SignStreamToken([]byte(streamSecret), viewer, time.Now().UTC().Add(time.Hour))
	get := func() *httptest.ResponseRecorder {
		return doJSON(t, h, "GET", "/api/v1/stream/"+viewer+"/index.m3u8?token="+tok, nil, nil)
	}
	// Remove the first AAC track and the entire AC-3 group; a later AAC track
	// must retain its URI/index and become default, without dangling groups.
	for _, name := range []string{"aac0", "ac30", "v720"} {
		if err := os.Remove(filepath.Join(dir, name+".m3u8")); err != nil {
			t.Fatal(err)
		}
	}
	rr := get()
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, absent := range []string{"aac0.m3u8", "ac30.m3u8", `AUDIO="ac3"`, "\nv720.m3u8"} {
		if strings.Contains(body, absent) {
			t.Errorf("unexpected %s in %s", absent, body)
		}
	}
	if !strings.Contains(body, `DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="aac1.m3u8?token=`) {
		t.Fatalf("remaining AAC not default: %s", body)
	}
	if err := os.Remove(filepath.Join(dir, "aac1.m3u8")); err != nil {
		t.Fatal(err)
	}
	rr = get()
	// No audio at all: list everything rather than a silent video-only master.
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "aac0.m3u8") || !strings.Contains(rr.Body.String(), "\nv360.m3u8") {
		t.Fatalf("no-audio master: %d %s", rr.Code, rr.Body.String())
	}
	// A later request picks up tracks once FFmpeg publishes them.
	writeFixtureSession(t, dir)
	rr = get()
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "aac0.m3u8") || !strings.Contains(rr.Body.String(), `AUDIO="ac3"`) {
		t.Fatalf("recovered master: %d %s", rr.Code, rr.Body.String())
	}
	if err := os.Remove(filepath.Join(dir, fixtureLayout().ReadyPlaylist())); err != nil {
		t.Fatal(err)
	}
	rr = get()
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "playlist not ready") {
		t.Fatalf("missing lowest video: %d %s", rr.Code, rr.Body.String())
	}
}
