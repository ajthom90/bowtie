package stream

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

const (
	viewerIdleTimeout   = 30 * time.Second
	sessionEmptyGrace   = 60 * time.Second
	playlistTimeout     = 15 * time.Second
	healthyResetAfter   = 60 * time.Second
	restartBackoffStart = 1 * time.Second
	restartBackoffCap   = 30 * time.Second
	reaperInterval      = 5 * time.Second
	playlistPollEvery   = 20 * time.Millisecond
)

// Process is a running transcode job supervised by the manager.
type Process interface {
	Done() <-chan error
	Stop()
}

// Runner starts a transcode Process for a JobSpec.
type Runner interface {
	Start(ctx context.Context, spec transcode.JobSpec) (Process, error)
}

// ManagerDeps are injected dependencies for Manager.
type ManagerDeps struct {
	Cfg       config.Config
	Store     *store.Store
	Tuners    *tuner.Manager
	StreamURL func(store.Channel) (string, error) // nil → Tuners.StreamURL
	Caps      transcode.Capabilities
	Runner    Runner
	Clock     func() time.Time // nil → time.Now
}

// Manager owns shared HLS transcode sessions and their viewers.
type Manager struct {
	cfg       config.Config
	store     *store.Store
	tuners    *tuner.Manager
	streamURL func(store.Channel) (string, error)
	caps      transcode.Capabilities
	runner    Runner
	clock     func() time.Time

	mu       sync.Mutex
	sessions map[string]*session // by session ID
	byKey    map[string]*session // by SessionKey
	viewers  map[string]*Viewer  // by viewer ID

	wg sync.WaitGroup // session supervisors
}

// NewManager constructs a Manager from deps.
func NewManager(deps ManagerDeps) *Manager {
	clock := deps.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	streamURL := deps.StreamURL
	if streamURL == nil {
		streamURL = func(ch store.Channel) (string, error) {
			return deps.Tuners.StreamURL(ch)
		}
	}
	return &Manager{
		cfg:       deps.Cfg,
		store:     deps.Store,
		tuners:    deps.Tuners,
		streamURL: streamURL,
		caps:      deps.Caps,
		runner:    deps.Runner,
		clock:     clock,
		sessions:  make(map[string]*session),
		byKey:     make(map[string]*session),
		viewers:   make(map[string]*Viewer),
	}
}

func (m *Manager) now() time.Time {
	return m.clock()
}

// Start joins or creates a session for the channel and returns a viewer handle.
// Errors: negotiation failure, unknown/disabled channel, stream URL / runner failure,
// playlist timeout (or process exit before playlist).
func (m *Manager) Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (ViewerHandle, error) {
	ch, err := m.store.ChannelByID(channelID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ViewerHandle{}, fmt.Errorf("unknown channel %d: %w", channelID, err)
		}
		return ViewerHandle{}, fmt.Errorf("channel %d: %w", channelID, err)
	}
	if !ch.Enabled {
		return ViewerHandle{}, fmt.Errorf("channel %d is disabled", channelID)
	}

	decision, err := transcode.Negotiate(caps, user.MaxQuality, m.caps, m.cfg.Encoder, m.cfg.AllowHEVC, transcode.DefaultProfiles())
	if err != nil {
		return ViewerHandle{}, fmt.Errorf("negotiate: %w", err)
	}
	key := transcode.SessionKey(channelID, decision)

	m.mu.Lock()
	if existing, ok := m.byKey[key]; ok && !existing.terminated {
		h, err := m.addViewerLocked(existing, user.Username)
		m.mu.Unlock()
		return h, err
	}
	m.mu.Unlock()

	inputURL, err := m.streamURL(ch)
	if err != nil {
		// Surface underlying error (Task 15 maps acquisition failures to 503).
		return ViewerHandle{}, err
	}

	sessionID, err := randomID()
	if err != nil {
		return ViewerHandle{}, err
	}
	dir := filepath.Join(m.cfg.SegmentDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ViewerHandle{}, fmt.Errorf("mkdir session dir: %w", err)
	}

	procCtx, procCancel := context.WithCancel(context.Background())
	spec := transcode.JobSpec{InputURL: inputURL, OutDir: dir, D: decision}
	proc, err := m.runner.Start(procCtx, spec)
	if err != nil {
		procCancel()
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err
	}

	if err := m.waitPlaylist(ctx, dir, proc); err != nil {
		proc.Stop()
		procCancel()
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err
	}

	now := m.now()
	sess := &session{
		id:          sessionID,
		channelID:   channelID,
		channelName: ch.Name,
		key:         key,
		decision:    decision,
		dir:         dir,
		startedAt:   now,
		proc:        proc,
		procCancel:  procCancel,
		procStart:   now,
		inputURL:    inputURL,
		viewers:     make(map[string]*Viewer),
	}

	m.mu.Lock()
	// Race: another Start may have registered the same key while we waited.
	if existing, ok := m.byKey[key]; ok && !existing.terminated {
		m.mu.Unlock()
		proc.Stop()
		procCancel()
		_ = os.RemoveAll(dir)
		m.mu.Lock()
		if existing, ok := m.byKey[key]; ok && !existing.terminated {
			h, err := m.addViewerLocked(existing, user.Username)
			m.mu.Unlock()
			return h, err
		}
		// Other session vanished; fall through and register ours.
	}
	m.sessions[sessionID] = sess
	m.byKey[key] = sess
	h, err := m.addViewerLocked(sess, user.Username)
	m.mu.Unlock()
	if err != nil {
		return ViewerHandle{}, err
	}

	m.wg.Add(1)
	go m.supervise(sess)

	return h, nil
}

func (m *Manager) addViewerLocked(sess *session, username string) (ViewerHandle, error) {
	viewerID, err := randomID()
	if err != nil {
		return ViewerHandle{}, err
	}
	now := m.now()
	v := &Viewer{
		ID:        viewerID,
		SessionID: sess.id,
		Username:  username,
		LastSeen:  now,
	}
	sess.viewers[viewerID] = v
	sess.emptySince = time.Time{}
	m.viewers[viewerID] = v
	return ViewerHandle{
		ViewerID:   viewerID,
		SessionID:  sess.id,
		SessionDir: sess.dir,
	}, nil
}

// waitPlaylist polls for live.m3u8 up to playlistTimeout using the injectable clock.
func (m *Manager) waitPlaylist(ctx context.Context, dir string, proc Process) error {
	deadline := m.now().Add(playlistTimeout)
	path := filepath.Join(dir, "live.m3u8")
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-proc.Done():
			if err != nil {
				return fmt.Errorf("ffmpeg exited before playlist ready: %w", err)
			}
			return fmt.Errorf("ffmpeg exited before playlist ready")
		default:
		}
		if !m.now().Before(deadline) {
			return fmt.Errorf("playlist timeout waiting for live.m3u8")
		}
		time.Sleep(playlistPollEvery)
	}
}

// Touch records a heartbeat for viewerID. Returns false if unknown.
func (m *Manager) Touch(viewerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return false
	}
	v.LastSeen = m.now()
	return true
}

// StopViewer removes a viewer. Session may enter empty grace.
func (m *Manager) StopViewer(viewerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeViewerLocked(viewerID)
}

func (m *Manager) removeViewerLocked(viewerID string) {
	v, ok := m.viewers[viewerID]
	if !ok {
		return
	}
	delete(m.viewers, viewerID)
	sess, ok := m.sessions[v.SessionID]
	if !ok {
		return
	}
	delete(sess.viewers, viewerID)
	if len(sess.viewers) == 0 && sess.emptySince.IsZero() {
		sess.emptySince = m.now()
	}
}

// Sessions returns snapshots of all live sessions.
func (m *Manager) Sessions() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, sess := range m.sessions {
		if sess.terminated {
			continue
		}
		viewers := make([]ViewerInfo, 0, len(sess.viewers))
		for _, v := range sess.viewers {
			viewers = append(viewers, ViewerInfo{
				ID:       v.ID,
				Username: v.Username,
				LastSeen: v.LastSeen,
			})
		}
		out = append(out, SessionInfo{
			ID:          sess.id,
			ChannelID:   sess.channelID,
			ChannelName: sess.channelName,
			Key:         sess.key,
			VideoCodec:  sess.decision.VideoCodec,
			Profile:     sess.decision.Profile.Name,
			Backend:     string(sess.decision.Backend),
			Viewers:     viewers,
			StartedAt:   sess.startedAt,
		})
	}
	return out
}

// SessionDirOf returns the session segment directory for a viewer.
func (m *Manager) SessionDirOf(viewerID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return "", false
	}
	sess, ok := m.sessions[v.SessionID]
	if !ok || sess.terminated {
		return "", false
	}
	return sess.dir, true
}

// Terminate admin-kills a session: stop ffmpeg, remove dir, drop viewers.
func (m *Manager) Terminate(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return
	}
	m.teardownSessionLocked(sess)
}

// Run is the reaper / restart loop. Cancelling ctx stops all sessions and returns.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.shutdownAll()
			return
		case <-ticker.C:
			m.maintain()
		}
	}
}

// maintain reaps idle viewers, tears down empty sessions past grace, and
// restarts crashed processes after backoff. Called from Run's ticker; tests
// invoke it directly after advancing the fake clock.
func (m *Manager) maintain() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()

	// Reap idle viewers (>30s since last heartbeat).
	var idle []string
	for id, v := range m.viewers {
		if now.Sub(v.LastSeen) > viewerIdleTimeout {
			idle = append(idle, id)
		}
	}
	for _, id := range idle {
		m.removeViewerLocked(id)
	}

	// Snapshot session pointers so we can teardown while ranging safely.
	sessions := make([]*session, 0, len(m.sessions))
	for _, sess := range m.sessions {
		sessions = append(sessions, sess)
	}

	for _, sess := range sessions {
		if sess.terminated {
			continue
		}
		if len(sess.viewers) == 0 && !sess.emptySince.IsZero() {
			if now.Sub(sess.emptySince) > sessionEmptyGrace {
				m.teardownSessionLocked(sess)
				continue
			}
		}
		if sess.crashed && !sess.restartAfter.IsZero() && !now.Before(sess.restartAfter) {
			m.restartSessionLocked(sess)
		}
	}
}

func (m *Manager) restartSessionLocked(sess *session) {
	if sess.procCancel != nil {
		sess.procCancel()
	}
	procCtx, procCancel := context.WithCancel(context.Background())
	spec := transcode.JobSpec{InputURL: sess.inputURL, OutDir: sess.dir, D: sess.decision}
	proc, err := m.runner.Start(procCtx, spec)
	if err != nil {
		procCancel()
		sess.procCancel = nil
		sess.backoff = nextBackoff(sess.backoff)
		sess.restartAfter = m.now().Add(sess.backoff)
		return
	}
	now := m.now()
	sess.proc = proc
	sess.procCancel = procCancel
	sess.procStart = now
	sess.crashed = false
	sess.restartAfter = time.Time{}
}

// nextBackoff doubles previous (or starts at 1s), capped at 30s.
func nextBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return restartBackoffStart
	}
	n := prev * 2
	if n > restartBackoffCap {
		return restartBackoffCap
	}
	return n
}

// computeCrashBackoff returns the wait before restart after a crash.
// Resets to 1s if the process ran healthy for ≥60s; otherwise doubles previous.
func computeCrashBackoff(prev time.Duration, procStart, now time.Time) time.Duration {
	if now.Sub(procStart) >= healthyResetAfter {
		return restartBackoffStart
	}
	return nextBackoff(prev)
}

func (m *Manager) supervise(sess *session) {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		if sess.terminated {
			m.mu.Unlock()
			return
		}
		proc := sess.proc
		crashed := sess.crashed
		m.mu.Unlock()

		if proc == nil || crashed {
			// Wait for maintain() to restart, or termination.
			select {
			case <-time.After(playlistPollEvery):
			}
			continue
		}

		err := <-proc.Done()

		m.mu.Lock()
		if sess.terminated {
			m.mu.Unlock()
			return
		}
		// Ignore Done from a process we already replaced.
		if sess.proc != proc {
			m.mu.Unlock()
			continue
		}
		now := m.now()
		sess.backoff = computeCrashBackoff(sess.backoff, sess.procStart, now)
		sess.crashed = true
		sess.restartAfter = now.Add(sess.backoff)
		_ = err
		m.mu.Unlock()
	}
}

func (m *Manager) teardownSessionLocked(sess *session) {
	if sess.terminated {
		return
	}
	sess.terminated = true
	for id := range sess.viewers {
		delete(m.viewers, id)
	}
	sess.viewers = make(map[string]*Viewer)
	if sess.proc != nil {
		sess.proc.Stop()
	}
	if sess.procCancel != nil {
		sess.procCancel()
	}
	delete(m.sessions, sess.id)
	if m.byKey[sess.key] == sess {
		delete(m.byKey, sess.key)
	}
	_ = os.RemoveAll(sess.dir)
}

func (m *Manager) shutdownAll() {
	m.mu.Lock()
	ids := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		ids = append(ids, s)
	}
	for _, s := range ids {
		m.teardownSessionLocked(s)
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
