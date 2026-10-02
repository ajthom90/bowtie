// Package e2e wires the real Bowtie stack (store, tuner manager, ingest, stream
// manager, HTTP API) to a fault-injecting fake HDHomeRun so tests can watch
// what a viewer experiences when the device or the transcoder misbehaves.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	"github.com/ajthom90/bowtie/server/internal/testplayer"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

const (
	adminUser = "admin"
	adminPass = "e2e-admin-pass"
)

// Options configures a Harness.
type Options struct {
	Fake   hdhrfake.Options
	Runner stream.Runner
	// Backends the probe reports; default software only.
	Backends []transcode.Backend
	// FFmpegPath for the config (only used by real runners); default "ffmpeg".
	FFmpegPath string
	// PollEvery for players; default 1s.
	PollEvery time.Duration
}

// Harness is a running Bowtie stack pointed at a fake HDHomeRun.
type Harness struct {
	Fake       *hdhrfake.Fake
	Store      *store.Store
	Streams    *stream.Manager
	Ingest     *stream.IngestManager
	Server     *httptest.Server
	AdminToken string
	// Channels maps guide number → Bowtie channel id (all enabled).
	Channels map[string]int64

	pollEvery time.Duration
	dialMu    sync.Mutex
	dials     []string
}

// DialLog returns every device dial Bowtie's ingest made, with outcome.
func (h *Harness) DialLog() []string {
	h.dialMu.Lock()
	defer h.dialMu.Unlock()
	return append([]string(nil), h.dials...)
}

// New builds the stack, logs in as admin, adds the fake by address and
// enables every channel. Everything is torn down via t.Cleanup.
func New(t testing.TB, o Options) *Harness {
	t.Helper()
	fake := hdhrfake.New(t, o.Fake)

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	segDir := filepath.Join(dir, "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpeg := o.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	cfg := config.Config{ListenAddr: ":0", SegmentDir: segDir, Encoder: "auto", FFmpegPath: ffmpeg}
	backends := o.Backends
	if len(backends) == 0 {
		backends = []transcode.Backend{transcode.BackendSoftware}
	}
	caps := transcode.Capabilities{Available: backends, HEVC: map[transcode.Backend]bool{}}

	tuners := tuner.New(st, cfg)
	tuners.SetDiscoverFunc(func(context.Context, time.Duration) ([]hdhr.DiscoverInfo, error) { return nil, nil })
	h := &Harness{Channels: map[string]int64{}}
	ingest := stream.NewIngestManager(func(ctx context.Context, u string) (io.ReadCloser, int, error) {
		start := time.Now()
		body, status, err := stream.HTTPDial(ctx, u)
		h.dialMu.Lock()
		h.dials = append(h.dials, fmt.Sprintf("%s dial %s → status=%d err=%v (%v)", start.Format("15:04:05.000"), u, status, err, time.Since(start).Round(time.Millisecond)))
		h.dialMu.Unlock()
		return body, status, err
	})
	mgr := stream.NewManager(stream.ManagerDeps{
		Cfg: cfg, Store: st, Tuners: tuners, Caps: caps, Runner: o.Runner, Ingest: ingest,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.Run(ctx)
	t.Cleanup(func() {
		cancel()
		ingest.Shutdown()
	})

	secret := []byte("e2e-jwt-secret-0123456789abcdef!")
	handler := api.New(api.Deps{
		Cfg:               cfg,
		Store:             st,
		Auth:              &auth.Auth{Secret: secret, Store: st},
		Tuners:            tuners,
		Streams:           mgr,
		StreamTokenSecret: []byte("e2e-stream-secret-0123456789abcd"),
		Probe:             func() transcode.Capabilities { return caps },
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	hash, err := auth.HashPassword(adminPass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(store.User{Username: adminUser, PasswordHash: hash, Role: "admin", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	poll := o.PollEvery
	if poll <= 0 {
		poll = time.Second
	}
	h.Fake, h.Store, h.Streams, h.Ingest, h.Server, h.pollEvery = fake, st, mgr, ingest, srv, poll
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	h.call(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": adminUser, "password": adminPass}, &login)
	h.AdminToken = login.AccessToken

	u, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	h.call(t, http.MethodPost, "/api/v1/admin/devices", map[string]string{"ip": u.Host}, nil)
	var chans []struct {
		ID          int64  `json:"id"`
		GuideNumber string `json:"guideNumber"`
	}
	h.call(t, http.MethodGet, "/api/v1/admin/channels", nil, &chans)
	if len(chans) == 0 {
		t.Fatal("e2e: fake lineup produced no channels")
	}
	for _, c := range chans {
		h.call(t, http.MethodPatch, fmt.Sprintf("/api/v1/admin/channels/%d", c.ID), map[string]bool{"enabled": true}, nil)
		h.Channels[c.GuideNumber] = c.ID
	}
	return h
}

// call performs an authenticated JSON request and fails the test on non-2xx.
func (h *Harness) call(t testing.TB, method, path string, body, out any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.Server.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.AdminToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.AdminToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		t.Fatalf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, buf.String())
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
}

// Player starts a viewer on a channel (by guide number).
func (h *Harness) Player(t testing.TB, guide string) (*testplayer.Player, error) {
	return h.PlayerWith(t, guide, nil)
}

// PlayerWith starts a viewer using client for HTTP (nil = default).
func (h *Harness) PlayerWith(t testing.TB, guide string, client *http.Client) (*testplayer.Player, error) {
	t.Helper()
	id, ok := h.Channels[guide]
	if !ok {
		t.Fatalf("e2e: no channel %s", guide)
	}
	return testplayer.Start(context.Background(), testplayer.Config{
		BaseURL: h.Server.URL, Token: h.AdminToken, ChannelID: id, PollEvery: h.pollEvery, Client: client,
	})
}

// Sessions returns the live sessions from the admin API.
func (h *Harness) Sessions(t testing.TB) []stream.SessionInfo {
	t.Helper()
	var out []stream.SessionInfo
	h.call(t, http.MethodGet, "/api/v1/admin/sessions", nil, &out)
	return out
}
