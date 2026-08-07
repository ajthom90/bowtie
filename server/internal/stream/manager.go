package stream

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/settings"
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
	startMaxAttempts    = 3
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
	// Settings is optional. When non-nil, Start reads encoder/allowHevc from the
	// provider per session. When nil, falls back to Cfg.Encoder/Cfg.AllowHEVC
	// (keeps existing test fixtures compiling without a provider).
	Settings *settings.Provider
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
	settings  *settings.Provider

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
		settings:  deps.Settings,
		sessions:  make(map[string]*session),
		byKey:     make(map[string]*session),
		viewers:   make(map[string]*Viewer),
	}
}

func (m *Manager) now() time.Time {
	return m.clock()
}

// transcodePrefs returns encoder + allowHEVC for this Start. Provider when set;
// otherwise cfg (nil-safe Settings for fixtures that never inject a provider).
func (m *Manager) transcodePrefs() (encoder string, allowHEVC bool, err error) {
	if m.settings == nil {
		return m.cfg.Encoder, m.cfg.AllowHEVC, nil
	}
	t, err := m.settings.Transcode()
	if err != nil {
		return "", false, fmt.Errorf("transcode settings: %w", err)
	}
	return t.Encoder, t.AllowHEVC, nil
}

// Start joins or creates a session for the channel and returns a viewer handle.
// Errors: negotiation failure, unknown/disabled channel, stream URL / runner failure,
// playlist timeout (or process exit before playlist).
//
// Create-or-join is retried up to startMaxAttempts times when a duplicate-key race
// loses to a competing session that then vanishes before we can join it — so we never
// register a session whose process was already stopped and dir already deleted.
func (m *Manager) Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (ViewerHandle, error) {
	ch, err := m.store.ChannelByID(channelID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ViewerHandle{}, fmt.Errorf("unknown channel %d: %w", channelID, err)
		}
		return ViewerHandle{}, fmt.Errorf("channel %d: %w", channelID, err)
	}
	// Admin may preview disabled channels (EPG-less smoke test). Viewers cannot
	// join those sessions either: the enabled check runs before session-key join.
	if !ch.Enabled && user.Role != "admin" {
		return ViewerHandle{}, fmt.Errorf("channel %d is disabled", channelID)
	}

	encoder, allowHEVC, err := m.transcodePrefs()
	if err != nil {
		return ViewerHandle{}, err
	}
	decision, err := transcode.Negotiate(caps, user.MaxQuality, m.caps, encoder, allowHEVC, transcode.DefaultProfiles())
	if err != nil {
		return ViewerHandle{}, fmt.Errorf("negotiate: %w", err)
	}
	key := transcode.SessionKey(channelID, decision)

	inputURL, err := m.streamURL(ch)
	if err != nil {
		// Surface underlying error (Task 15 maps acquisition failures to 503).
		return ViewerHandle{}, err
	}

	var lastErr error
	for attempt := 0; attempt < startMaxAttempts; attempt++ {
		h, err, retry := m.startAttempt(ctx, user, ch, key, decision, inputURL)
		if err == nil {
			return h, nil
		}
		if !retry {
			return ViewerHandle{}, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("session start failed after %d attempts", startMaxAttempts)
	}
	return ViewerHandle{}, fmt.Errorf("session start failed after %d attempts: %w", startMaxAttempts, lastErr)
}

// startAttempt tries one join-or-create. retry=true means the caller should try again
// with a fresh sessionID/dir/process (duplicate-key race left us with a dead candidate).
func (m *Manager) startAttempt(ctx context.Context, user store.User, ch store.Channel, key string, decision transcode.Decision, inputURL string) (ViewerHandle, error, bool) {
	m.mu.Lock()
	if existing, ok := m.byKey[key]; ok && !existing.terminated {
		h, err := m.addViewerLocked(existing, user.Username)
		m.mu.Unlock()
		return h, err, false
	}
	m.mu.Unlock()

	sessionID, err := randomID()
	if err != nil {
		return ViewerHandle{}, err, false
	}
	dir := filepath.Join(m.cfg.SegmentDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ViewerHandle{}, fmt.Errorf("mkdir session dir: %w", err), false
	}

	procCtx, procCancel := context.WithCancel(context.Background())
	spec := transcode.JobSpec{InputURL: inputURL, OutDir: dir, D: decision}
	proc, err := m.runner.Start(procCtx, spec)
	if err != nil {
		procCancel()
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err, false
	}

	if err := m.waitPlaylist(ctx, dir, proc); err != nil {
		proc.Stop()
		procCancel()
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err, false
	}

	now := m.now()
	sess := &session{
		id:          sessionID,
		channelID:   ch.ID,
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
		// Abandon our candidate (stop process, remove dir) then try to join the winner.
		proc.Stop()
		procCancel()
		_ = os.RemoveAll(dir)
		m.mu.Lock()
		if existing, ok := m.byKey[key]; ok && !existing.terminated {
			h, err := m.addViewerLocked(existing, user.Username)
			m.mu.Unlock()
			return h, err, false
		}
		m.mu.Unlock()
		// Competing session vanished after we tore down ours. Do not register the
		// dead candidate (stopped proc, deleted dir) — retry with a fresh attempt.
		return ViewerHandle{}, fmt.Errorf("duplicate-key race: competing session gone"), true
	}
	m.sessions[sessionID] = sess
	m.byKey[key] = sess
	h, err := m.addViewerLocked(sess, user.Username)
	m.mu.Unlock()
	if err != nil {
		// Unlikely (randomID failure); tear down what we registered.
		m.mu.Lock()
		m.teardownSessionLocked(sess)
		m.mu.Unlock()
		return ViewerHandle{}, err, false
	}

	m.wg.Add(1)
	go m.supervise(sess)

	return h, nil, false
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

// SessionInfoOf returns a snapshot of the session the viewer is attached to.
func (m *Manager) SessionInfoOf(viewerID string) (SessionInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return SessionInfo{}, false
	}
	sess, ok := m.sessions[v.SessionID]
	if !ok || sess.terminated {
		return SessionInfo{}, false
	}
	viewers := make([]ViewerInfo, 0, len(sess.viewers))
	for _, vv := range sess.viewers {
		viewers = append(viewers, ViewerInfo{
			ID:       vv.ID,
			Username: vv.Username,
			LastSeen: vv.LastSeen,
		})
	}
	return SessionInfo{
		ID:          sess.id,
		ChannelID:   sess.channelID,
		ChannelName: sess.channelName,
		Key:         sess.key,
		VideoCodec:  sess.decision.VideoCodec,
		Profile:     sess.decision.Profile.Name,
		Backend:     string(sess.decision.Backend),
		Viewers:     viewers,
		StartedAt:   sess.startedAt,
	}, true
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
	// Defensive: ensure the segment dir exists (e.g. was removed by a failed race path).
	if err := os.MkdirAll(sess.dir, 0o755); err != nil {
		log.Printf("stream: mkdir session dir %s for restart: %v", sess.dir, err)
		sess.backoff = nextBackoff(sess.backoff)
		sess.restartAfter = m.now().Add(sess.backoff)
		return
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
			<-time.After(playlistPollEvery)
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
