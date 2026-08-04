package stream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// --- stub Runner / Process ---------------------------------------------------

type stubProcess struct {
	done   chan error
	stopCh chan struct{}
	once   sync.Once
}

func newStubProcess() *stubProcess {
	return &stubProcess{
		done:   make(chan error, 1),
		stopCh: make(chan struct{}),
	}
}

func (p *stubProcess) Done() <-chan error { return p.done }

func (p *stubProcess) Stop() {
	p.once.Do(func() {
		close(p.stopCh)
		select {
		case p.done <- errors.New("stopped"):
		default:
		}
	})
}

// Crash sends an error on Done without going through Stop.
func (p *stubProcess) Crash(err error) {
	select {
	case p.done <- err:
	default:
	}
}

type stubRunner struct {
	mu       sync.Mutex
	starts   int
	procs    []*stubProcess
	writeM3U bool
	startErr error
	// lastSpec is the most recent JobSpec
	lastSpec transcode.JobSpec
	// onStart optional hook
	onStart func(spec transcode.JobSpec)
}

func (r *stubRunner) Start(_ context.Context, spec transcode.JobSpec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.starts++
	r.lastSpec = spec
	if r.onStart != nil {
		r.onStart(spec)
	}
	p := newStubProcess()
	r.procs = append(r.procs, p)
	if r.writeM3U {
		path := filepath.Join(spec.OutDir, "live.m3u8")
		if err := os.WriteFile(path, []byte("#EXTM3U\n"), 0o644); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (r *stubRunner) Starts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

func (r *stubRunner) LastProc() *stubProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.procs) == 0 {
		return nil
	}
	return r.procs[len(r.procs)-1]
}

func (r *stubRunner) Proc(i int) *stubProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.procs[i]
}

// --- test harness ------------------------------------------------------------

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func softwareCaps() transcode.Capabilities {
	return transcode.Capabilities{
		Available: []transcode.Backend{transcode.BackendSoftware},
		HEVC:      map[transcode.Backend]bool{},
	}
}

func clientCaps(profile string) transcode.ClientCaps {
	return transcode.ClientCaps{
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
		MaxHeight:   0,
		Profile:     profile,
	}
}

func setupEnv(t *testing.T) (*store.Store, config.Config, *fakeClock, *stubRunner, int64, store.User) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	if err := st.UpsertDevice(store.Device{
		DeviceID:   "dev1",
		IP:         "127.0.0.1",
		Model:      "fake",
		TunerCount: 2,
		Manual:     true,
		LastSeen:   now,
		StreamPort: 5004,
	}); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	if err := st.SyncLineup("dev1", []store.Channel{
		{DeviceID: "dev1", GuideNumber: "5.1", Name: "NEWS"},
	}); err != nil {
		t.Fatalf("SyncLineup: %v", err)
	}
	chans, err := st.ListChannels(false)
	if err != nil || len(chans) != 1 {
		t.Fatalf("ListChannels: %v len=%d", err, len(chans))
	}
	chID := chans[0].ID
	if err := st.UpdateChannel(chID, true, ""); err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}

	uid, err := st.CreateUser(store.User{
		Username:     "alice",
		PasswordHash: "x",
		Role:         "viewer",
		MaxQuality:   "",
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	user := store.User{ID: uid, Username: "alice", Role: "viewer"}

	segDir := filepath.Join(t.TempDir(), "segments")
	if err := os.MkdirAll(segDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		SegmentDir: segDir,
		Encoder:    "auto",
		AllowHEVC:  false,
		FFmpegPath: "ffmpeg",
	}
	clock := newFakeClock(now)
	runner := &stubRunner{writeM3U: true}
	return st, cfg, clock, runner, chID, user
}

func newTestManager(st *store.Store, cfg config.Config, clock *fakeClock, runner *stubRunner) *Manager {
	return NewManager(ManagerDeps{
		Cfg:   cfg,
		Store: st,
		// Tuners nil; StreamURL injected
		StreamURL: func(ch store.Channel) (string, error) {
			return "http://127.0.0.1:5004/auto/v" + ch.GuideNumber, nil
		},
		Caps:   softwareCaps(),
		Runner: runner,
		Clock:  clock.Now,
	})
}

// --- tests -------------------------------------------------------------------

func TestStartCreatesSession(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.ViewerID == "" || h.SessionID == "" || h.SessionDir == "" {
		t.Fatalf("empty handle: %+v", h)
	}
	if _, err := os.Stat(filepath.Join(h.SessionDir, "live.m3u8")); err != nil {
		t.Fatalf("live.m3u8 missing: %v", err)
	}
	if runner.Starts() != 1 {
		t.Fatalf("starts=%d, want 1", runner.Starts())
	}
	sessions := m.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("sessions=%d, want 1", len(sessions))
	}
	if sessions[0].ChannelID != chID || sessions[0].ChannelName != "NEWS" {
		t.Errorf("session info: %+v", sessions[0])
	}
	if sessions[0].Profile != "original" || sessions[0].VideoCodec != "h264" {
		t.Errorf("codec/profile: %+v", sessions[0])
	}
	if len(sessions[0].Viewers) != 1 || sessions[0].Viewers[0].Username != "alice" {
		t.Errorf("viewers: %+v", sessions[0].Viewers)
	}
	dir, ok := m.SessionDirOf(h.ViewerID)
	if !ok || dir != h.SessionDir {
		t.Errorf("SessionDirOf = %q %v, want %q true", dir, ok, h.SessionDir)
	}
	if !m.Touch(h.ViewerID) {
		t.Error("Touch returned false")
	}
	if m.Touch("nope") {
		t.Error("Touch unknown should be false")
	}
}

func TestSecondViewerSharesSession(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h1, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start1: %v", err)
	}
	user2 := store.User{ID: 2, Username: "bob", Role: "viewer"}
	h2, err := m.Start(context.Background(), user2, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start2: %v", err)
	}
	if h1.SessionID != h2.SessionID {
		t.Fatalf("session IDs differ: %s vs %s", h1.SessionID, h2.SessionID)
	}
	if h1.ViewerID == h2.ViewerID {
		t.Fatal("viewer IDs should differ")
	}
	if runner.Starts() != 1 {
		t.Fatalf("starts=%d, want 1 (shared)", runner.Starts())
	}
	sessions := m.Sessions()
	if len(sessions) != 1 || len(sessions[0].Viewers) != 2 {
		t.Fatalf("want 1 session with 2 viewers, got %+v", sessions)
	}
}

func TestDifferentQualityNewSession(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h1, err := m.Start(context.Background(), user, chID, clientCaps("original"))
	if err != nil {
		t.Fatalf("Start original: %v", err)
	}
	h2, err := m.Start(context.Background(), user, chID, clientCaps("low"))
	if err != nil {
		t.Fatalf("Start low: %v", err)
	}
	if h1.SessionID == h2.SessionID {
		t.Fatal("different quality should create different sessions")
	}
	if runner.Starts() != 2 {
		t.Fatalf("starts=%d, want 2", runner.Starts())
	}
	if len(m.Sessions()) != 2 {
		t.Fatalf("sessions=%d, want 2", len(m.Sessions()))
	}
}

func TestViewerReapAndSessionGrace(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionDir := h.SessionDir

	// Idle >30s → viewer reaped; session still alive (grace starts).
	clock.Advance(31 * time.Second)
	m.maintain()

	if m.Touch(h.ViewerID) {
		t.Fatal("viewer should have been reaped")
	}
	if len(m.Sessions()) != 1 {
		t.Fatalf("session should still exist during grace, got %d", len(m.Sessions()))
	}
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("session dir should still exist: %v", err)
	}

	// 60s after empty → tear down.
	clock.Advance(61 * time.Second)
	m.maintain()

	if len(m.Sessions()) != 0 {
		t.Fatalf("session should be gone after grace, got %d", len(m.Sessions()))
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed, err=%v", err)
	}
}

func TestCrashRestartBackoff(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if runner.Starts() != 1 {
		t.Fatalf("starts=%d", runner.Starts())
	}

	// Crash process 0.
	runner.Proc(0).Crash(errors.New("boom"))

	// Wait for supervisor to observe Done and schedule restart.
	waitFor(t, 2*time.Second, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		sess := m.sessions[h.SessionID]
		return sess != nil && sess.crashed
	})

	// Before 1s: no restart.
	m.maintain()
	if runner.Starts() != 1 {
		t.Fatalf("premature restart: starts=%d", runner.Starts())
	}

	// Advance 1s → first backoff elapsed → restart.
	clock.Advance(1 * time.Second)
	m.maintain()
	if runner.Starts() != 2 {
		t.Fatalf("after 1s backoff starts=%d, want 2", runner.Starts())
	}

	// Crash again quickly (not healthy 60s).
	runner.Proc(1).Crash(errors.New("boom2"))
	waitFor(t, 2*time.Second, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		sess := m.sessions[h.SessionID]
		return sess != nil && sess.crashed
	})

	// 1s not enough for 2s backoff.
	clock.Advance(1 * time.Second)
	m.maintain()
	if runner.Starts() != 2 {
		t.Fatalf("should still wait for 2s backoff: starts=%d", runner.Starts())
	}

	// Another 1s → 2s total → restart.
	clock.Advance(1 * time.Second)
	m.maintain()
	if runner.Starts() != 3 {
		t.Fatalf("after 2s backoff starts=%d, want 3", runner.Starts())
	}

	// Session still has the viewer.
	if len(m.Sessions()) != 1 || len(m.Sessions()[0].Viewers) != 1 {
		t.Fatalf("viewer should remain across restarts: %+v", m.Sessions())
	}
}

func TestTerminate(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionDir := h.SessionDir

	m.Terminate(h.SessionID)

	if len(m.Sessions()) != 0 {
		t.Fatalf("sessions after terminate: %d", len(m.Sessions()))
	}
	if m.Touch(h.ViewerID) {
		t.Fatal("viewer should be gone")
	}
	if _, ok := m.SessionDirOf(h.ViewerID); ok {
		t.Fatal("SessionDirOf should be false after terminate")
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("dir should be removed: %v", err)
	}
	// Second terminate is a no-op.
	m.Terminate(h.SessionID)
}

func TestRunCancelTeardown(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionDir := h.SessionDir

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if len(m.Sessions()) != 0 {
		t.Fatalf("sessions after cancel: %d", len(m.Sessions()))
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("dir should be removed on cancel: %v", err)
	}
}

func TestStartUnknownChannel(t *testing.T) {
	st, cfg, clock, runner, _, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)
	_, err := m.Start(context.Background(), user, 99999, clientCaps(""))
	if err == nil {
		t.Fatal("want error for unknown channel")
	}
}

func TestStartNegotiationFailure(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)
	_, err := m.Start(context.Background(), user, chID, transcode.ClientCaps{
		VideoCodecs: []string{"av1"}, // unsupported
		AudioCodecs: []string{"aac"},
	})
	if err == nil {
		t.Fatal("want negotiate error")
	}
	if runner.Starts() != 0 {
		t.Fatalf("runner should not start on negotiate failure")
	}
}

func TestStartStreamURLError(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	want := errors.New("all tuners in use")
	m := NewManager(ManagerDeps{
		Cfg:   cfg,
		Store: st,
		StreamURL: func(store.Channel) (string, error) {
			return "", want
		},
		Caps:   softwareCaps(),
		Runner: runner,
		Clock:  clock.Now,
	})
	_, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if !errors.Is(err, want) {
		t.Fatalf("err=%v, want wrapped %v", err, want)
	}
}

func TestStartPlaylistTimeout(t *testing.T) {
	st, cfg, clock, _, chID, user := setupEnv(t)
	// Runner never writes live.m3u8; advance clock past 15s via concurrent advance.
	// waitPlaylist uses clock for deadline but real sleep for poll — advance clock
	// from another goroutine so the loop observes timeout.
	runner := &stubRunner{writeM3U: false}
	m := newTestManager(st, cfg, clock, runner)

	// Advance clock while Start is polling.
	var advanced atomic.Bool
	go func() {
		// Let Start enter the poll loop first.
		time.Sleep(50 * time.Millisecond)
		clock.Advance(16 * time.Second)
		advanced.Store(true)
	}()

	_, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err == nil {
		t.Fatal("want playlist timeout")
	}
	if !advanced.Load() {
		t.Log("clock may not have advanced (fast fail via other path)")
	}
	if runner.Starts() != 1 {
		t.Fatalf("starts=%d, want 1", runner.Starts())
	}
	// No session registered on failure.
	if len(m.Sessions()) != 0 {
		t.Fatalf("sessions=%d after timeout", len(m.Sessions()))
	}
}

func TestStopViewerStartsGrace(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sessionDir := h.SessionDir
	m.StopViewer(h.ViewerID)

	if m.Touch(h.ViewerID) {
		t.Fatal("viewer gone after StopViewer")
	}
	if len(m.Sessions()) != 1 {
		t.Fatal("session should remain during grace")
	}

	clock.Advance(61 * time.Second)
	m.maintain()
	if len(m.Sessions()) != 0 {
		t.Fatal("session should be torn down after grace")
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("dir removed: %v", err)
	}
}

func TestHealthyResetBackoff(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)

	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First crash → 1s backoff.
	runner.Proc(0).Crash(errors.New("c1"))
	waitFor(t, 2*time.Second, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		s := m.sessions[h.SessionID]
		return s != nil && s.crashed && s.backoff == time.Second
	})
	clock.Advance(1 * time.Second)
	m.maintain()
	if runner.Starts() != 2 {
		t.Fatalf("starts=%d want 2", runner.Starts())
	}

	// Run healthy for 60s then crash → backoff resets to 1s (not 2s).
	clock.Advance(60 * time.Second)
	// Touch so viewer isn't reaped.
	m.Touch(h.ViewerID)

	runner.Proc(1).Crash(errors.New("c2"))
	waitFor(t, 2*time.Second, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		s := m.sessions[h.SessionID]
		return s != nil && s.crashed && s.backoff == time.Second
	})

	// Only 1s needed (not 2s).
	clock.Advance(1 * time.Second)
	m.maintain()
	if runner.Starts() != 3 {
		t.Fatalf("healthy reset should use 1s backoff: starts=%d", runner.Starts())
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// gateProcess is a Process whose Stop signals then blocks until release,
// letting tests force work between unlock and re-lock in Start's race path.
type gateProcess struct {
	*stubProcess
	stopEntered chan struct{} // closed when Stop is entered (once)
	stopBlock   <-chan struct{}
	stopOnce    sync.Once
}

func (p *gateProcess) Stop() {
	p.stopOnce.Do(func() {
		if p.stopEntered != nil {
			close(p.stopEntered)
		}
	})
	if p.stopBlock != nil {
		<-p.stopBlock
	}
	p.stubProcess.Stop()
}

// raceRunner coordinates the duplicate-key race:
//   start #1 (B): signals entered, waits for release, returns process with Stop gate
//   start #2 (A): succeeds immediately (A wins the key)
//   start #3+ (B retry): succeeds immediately with a live dir
type raceRunner struct {
	mu          sync.Mutex
	starts      int
	bEntered    chan struct{} // closed when B's first Start begins blocking
	bRelease    chan struct{} // B waits here before returning from Start
	bStopGate   chan struct{} // B's abandoned process Stop waits here
	bStopEnter  chan struct{} // closed when B's abandoned Stop is entered
	procs       []*stubProcess
}

func newRaceRunner() *raceRunner {
	return &raceRunner{
		bEntered:   make(chan struct{}),
		bRelease:   make(chan struct{}),
		bStopGate:  make(chan struct{}),
		bStopEnter: make(chan struct{}),
	}
}

func (r *raceRunner) Start(_ context.Context, spec transcode.JobSpec) (Process, error) {
	r.mu.Lock()
	r.starts++
	n := r.starts
	r.mu.Unlock()

	if n == 1 {
		// Viewer B's first create attempt: park until A is fully registered.
		close(r.bEntered)
		<-r.bRelease
	}

	p := newStubProcess()
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()

	path := filepath.Join(spec.OutDir, "live.m3u8")
	if err := os.WriteFile(path, []byte("#EXTM3U\n"), 0o644); err != nil {
		return nil, err
	}

	if n == 1 {
		// Only the abandoned candidate gates Stop so the test can Terminate A
		// while B is between unlock and re-lock.
		return &gateProcess{
			stubProcess: p,
			stopEntered: r.bStopEnter,
			stopBlock:   r.bStopGate,
		}, nil
	}
	return p, nil
}

func (r *raceRunner) Starts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts
}

// TestDuplicateKeyRaceDoesNotRegisterDeadSession forces the Start race where B
// loses to A, cleans up its candidate, and A is terminated before B re-locks.
// B must end with a working session (fresh dir + SessionDirOf), not a dead one.
func TestDuplicateKeyRaceDoesNotRegisterDeadSession(t *testing.T) {
	st, cfg, clock, _, chID, userA := setupEnv(t)
	runner := newRaceRunner()
	m := NewManager(ManagerDeps{
		Cfg:   cfg,
		Store: st,
		StreamURL: func(ch store.Channel) (string, error) {
			return "http://127.0.0.1:5004/auto/v" + ch.GuideNumber, nil
		},
		Caps:   softwareCaps(),
		Runner: runner,
		Clock:  clock.Now,
	})

	userB := store.User{ID: 2, Username: "bob", Role: "viewer"}

	// 1) Begin B first so it enters create (byKey empty) and blocks in Runner.Start.
	type startResult struct {
		h   ViewerHandle
		err error
	}
	bDone := make(chan startResult, 1)
	go func() {
		h, err := m.Start(context.Background(), userB, chID, clientCaps(""))
		bDone <- startResult{h, err}
	}()

	select {
	case <-runner.bEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("B never entered Runner.Start")
	}

	// 2) A creates and registers the session for this key.
	hA, err := m.Start(context.Background(), userA, chID, clientCaps(""))
	if err != nil {
		t.Fatalf("Start A: %v", err)
	}
	if len(m.Sessions()) != 1 {
		t.Fatalf("want 1 session after A, got %d", len(m.Sessions()))
	}

	// 3) Release B so it finishes Start → waitPlaylist → double-check sees A.
	close(runner.bRelease)

	// 4) Wait until B is parked inside proc.Stop() on the race cleanup path.
	select {
	case <-runner.bStopEnter:
	case <-time.After(3 * time.Second):
		t.Fatal("B never entered race-path Stop")
	}

	// Terminate A while B holds the unlock-window; re-lock will find no competitor.
	m.Terminate(hA.SessionID)
	if len(m.Sessions()) != 0 {
		t.Fatalf("want 0 sessions after terminate A, got %d", len(m.Sessions()))
	}

	// 5) Unblock B's abandoned Stop; B should retry and create a live session.
	close(runner.bStopGate)

	var bRes startResult
	select {
	case bRes = <-bDone:
	case <-time.After(3 * time.Second):
		t.Fatal("B Start did not return")
	}
	if bRes.err != nil {
		t.Fatalf("Start B: %v", bRes.err)
	}
	if bRes.h.ViewerID == "" || bRes.h.SessionID == "" || bRes.h.SessionDir == "" {
		t.Fatalf("empty B handle: %+v", bRes.h)
	}

	// Working session: dir exists with playlist, SessionDirOf resolves.
	if _, err := os.Stat(filepath.Join(bRes.h.SessionDir, "live.m3u8")); err != nil {
		t.Fatalf("B playlist dir not usable: %v (dir=%s)", err, bRes.h.SessionDir)
	}
	dir, ok := m.SessionDirOf(bRes.h.ViewerID)
	if !ok || dir != bRes.h.SessionDir {
		t.Fatalf("SessionDirOf = %q %v, want %q true", dir, ok, bRes.h.SessionDir)
	}
	if bRes.h.SessionID == hA.SessionID {
		t.Fatal("B should not reuse terminated A session id")
	}
	// Retry means at least 3 Runner.Start calls: B attempt1, A, B retry.
	if runner.Starts() < 3 {
		t.Fatalf("starts=%d, want >= 3 (B abandoned + A + B retry)", runner.Starts())
	}
	sessions := m.Sessions()
	if len(sessions) != 1 || len(sessions[0].Viewers) != 1 {
		t.Fatalf("want 1 session with B only, got %+v", sessions)
	}
	if sessions[0].Viewers[0].Username != "bob" {
		t.Errorf("viewer username = %q, want bob", sessions[0].Viewers[0].Username)
	}
}
