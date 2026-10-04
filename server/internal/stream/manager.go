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
	"sync/atomic"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

const (
	// viewerIdleTimeout is how long a viewer may go without playlist/heartbeat
	// Touch before the reaper drops them. 90s survives throttled background tabs
	// (timers clamped ~1/min) and short client hiccups between 15s heartbeats.
	viewerIdleTimeout   = 90 * time.Second
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
	// Ingest is required for process-scoped device fan-out (one dial per channel).
	// Production and tests always set it (A4). Start fails if nil.
	Ingest *IngestManager
	// Multitrack adds the caption tap, every broadcast audio track and the
	// AC-3 (5.1) copy. Off: video + first audio as AAC.
	Multitrack bool
	// TrackProbeTimeout bounds the wait for the channel's PMT; 0 → 3s.
	TrackProbeTimeout time.Duration
	// BlockedFor reports why a user may not keep watching a channel now
	// (parental controls; "" = allowed). Checked every 30 s; nil = never.
	BlockedFor func(userID, channelID int64, now time.Time) string
	// OnWatched is called once per viewer after watchedAfter of watching
	// (recently watched channels); nil = no-op. Called without m.mu held.
	OnWatched func(userID, channelID int64, at time.Time)
}

// watchedAfter: a channel counts as watched (recents) after this long, so
// zapping through channels doesn't fill the Recent row.
const watchedAfter = 30 * time.Second

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
	ingest    *IngestManager

	multitrack bool
	trackProbe time.Duration
	onWatched  func(userID, channelID int64, at time.Time)
	blockedFor func(userID, channelID int64, now time.Time) string

	mu              sync.Mutex
	sessions        map[string]*session       // by session ID
	byKey           map[string]*session       // by SessionKey
	viewers         map[string]*Viewer        // by viewer ID
	pending         map[*reservation]struct{} // limited accounts' in-flight starts (limits.go)
	blocked         map[string]blockedViewer  // viewers parental controls stopped (parental.go)
	lastParental    time.Time
	parentalRunning atomic.Bool

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
	trackProbe := deps.TrackProbeTimeout
	if trackProbe <= 0 {
		trackProbe = 3 * time.Second
	}
	return &Manager{
		multitrack: deps.Multitrack,
		onWatched:  deps.OnWatched,
		blockedFor: deps.BlockedFor,
		trackProbe: trackProbe,
		cfg:        deps.Cfg,
		store:      deps.Store,
		tuners:     deps.Tuners,
		streamURL:  streamURL,
		caps:       deps.Caps,
		runner:     deps.Runner,
		clock:      clock,
		settings:   deps.Settings,
		ingest:     deps.Ingest,
		sessions:   make(map[string]*session),
		byKey:      make(map[string]*session),
		viewers:    make(map[string]*Viewer),
		pending:    make(map[*reservation]struct{}),
		blocked:    make(map[string]blockedViewer),
	}
}

// IngestChannels returns channel IDs with an open device ingest (including the
// 5s post-last-Close tail). Empty when Ingest is unset.
func (m *Manager) IngestChannels() []int64 {
	if m.ingest == nil {
		return nil
	}
	return m.ingest.ActiveChannels()
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

// hlsListSizeForStart returns -hls_list_size for a new session process.
// Derived from streaming.bufferMinutes (segments = minutes*60/4 at 4s segments).
// Nil provider (fixtures) keeps the historical default of 30.
func (m *Manager) hlsListSizeForStart() (int, error) {
	if m.settings == nil {
		return transcode.DefaultHLSListSize, nil
	}
	s, err := m.settings.Streaming()
	if err != nil {
		return 0, fmt.Errorf("streaming settings: %w", err)
	}
	if s.BufferMinutes <= 0 {
		return transcode.DefaultHLSListSize, nil
	}
	return s.BufferMinutes * 60 / 4, nil
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
	m.mu.Lock()
	res, err := m.reserveLocked(user, channelID)
	m.mu.Unlock()
	if err != nil {
		return ViewerHandle{}, err
	}
	defer m.releaseReservation(res)

	encoder, allowHEVC, err := m.transcodePrefs()
	if err != nil {
		return ViewerHandle{}, err
	}
	decision, err := transcode.Negotiate(caps, user.MaxQuality, m.caps, encoder, allowHEVC, transcode.DefaultProfiles())
	if err != nil {
		return ViewerHandle{}, fmt.Errorf("negotiate: %w", err)
	}
	adaptive, err := m.adaptiveForStart(decision)
	if err != nil {
		return ViewerHandle{}, err
	}
	if adaptive && decision.VideoCodec == "hevc" {
		// The shared ladder is H.264 only.
		if decision, err = transcode.Negotiate(caps, user.MaxQuality, m.caps, encoder, false, transcode.DefaultProfiles()); err != nil {
			return ViewerHandle{}, fmt.Errorf("negotiate: %w", err)
		}
	}
	key := sessionKey(channelID, decision, adaptive)

	inputURL, err := m.streamURL(ch)
	if err != nil {
		// Surface underlying error (Task 15 maps acquisition failures to 503).
		return ViewerHandle{}, err
	}

	fallback := false
	var lastErr error
	for attempt := 0; attempt < startMaxAttempts; attempt++ {
		tracks := m.multitrack && !fallback
		h, err, retry := m.startAttempt(ctx, user, res, ch, key, decision, inputURL, adaptive, tracks)
		if err == nil {
			return h, nil
		}
		if !fallback && (tracks || adaptive) && errors.Is(err, errPlaylistNotReady) {
			// One retry in the simplest shape: one rung at this viewer's
			// quality (a per-quality session, not the shared ladder), first
			// audio only, no captions.
			log.Printf("stream: channel %d: full layout failed (%v); retrying with one rung, first audio, no captions", ch.ID, err)
			fallback = true
			if adaptive {
				adaptive = false
				key = sessionKey(ch.ID, decision, false)
			}
			attempt-- // the fallback is not a duplicate-key retry
			continue
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
//
// Process-start ordered contract (A4 / Lifecycle BINDING):
//
//	Close(old sub if any) → Attach → JobSpec.Stdin=sub.R → runner.Start
//
// Close on: proc-death, abandon, waitPlaylist failure, Terminate, teardown.
func (m *Manager) startAttempt(ctx context.Context, user store.User, res *reservation, ch store.Channel, key string, decision transcode.Decision, inputURL string, adaptive, tracks bool) (ViewerHandle, error, bool) {
	m.mu.Lock()
	if existing, ok := m.byKey[key]; ok && !existing.terminated {
		h, err := m.addViewerLocked(existing, user, res, decision.Profile.Height)
		m.mu.Unlock()
		return h, err, false
	}
	m.mu.Unlock()

	if m.ingest == nil {
		return ViewerHandle{}, errors.New("ingest not configured"), false
	}

	sessionID, err := randomID()
	if err != nil {
		return ViewerHandle{}, err, false
	}
	dir := filepath.Join(m.cfg.SegmentDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ViewerHandle{}, fmt.Errorf("mkdir session dir: %w", err), false
	}

	listSize, err := m.hlsListSizeForStart()
	if err != nil {
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err, false
	}

	// Initial process start: no prior sub. Attach → Stdin → Start.
	sub, err := m.ingest.Attach(ctx, ch.ID, inputURL)
	if err != nil {
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err, false
	}

	layout := m.layoutFor(ch.ID, decision, adaptive, tracks)
	capSub, err := m.attachCaptions(ctx, ch.ID, inputURL, layout)
	if err != nil {
		log.Printf("stream: channel %d: caption tap attach failed: %v (continuing without captions)", ch.ID, err)
		layout.Captions = false
	}

	procCtx, procCancel := context.WithCancel(context.Background())
	spec := transcode.JobSpec{Stdin: sub.R, OutDir: dir, D: decision, HLSListSize: listSize, Layout: layout}
	if capSub != nil {
		spec.CaptionInput = capSub.R
	}
	proc, err := m.runner.Start(procCtx, spec)
	if err != nil {
		_ = sub.Close()
		closeSub(capSub)
		procCancel()
		_ = os.RemoveAll(dir)
		return ViewerHandle{}, err, false
	}
	// If ingest gives up on this transcoder, stop it even if it ignores EOF.
	// The caption tap gets no such hook: a stalled tap stops captions only.
	sub.OnForceClose(proc.Stop)

	if err := m.waitPlaylist(ctx, dir, layout.ReadyPlaylist(), proc); err != nil {
		proc.Stop()
		procCancel()
		_ = sub.Close()
		closeSub(capSub)
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
		sub:         sub,
		capSub:      capSub,
		layout:      layout,
		hlsListSize: listSize,
		viewers:     make(map[string]*Viewer),
	}

	m.mu.Lock()
	// Race: another Start may have registered the same key while we waited.
	if existing, ok := m.byKey[key]; ok && !existing.terminated {
		m.mu.Unlock()
		// Abandon our candidate (Close sub, stop process, remove dir) then join winner.
		proc.Stop()
		procCancel()
		_ = sub.Close()
		closeSub(capSub)
		_ = os.RemoveAll(dir)
		m.mu.Lock()
		if existing, ok := m.byKey[key]; ok && !existing.terminated {
			h, err := m.addViewerLocked(existing, user, res, decision.Profile.Height)
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
	h, err := m.addViewerLocked(sess, user, res, decision.Profile.Height)
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

// addViewerLocked adds user's viewer to sess and consumes res (the start's
// limit reservation) in the same critical section, so the slot is never
// counted twice.
func (m *Manager) addViewerLocked(sess *session, user store.User, res *reservation, maxHeight int) (ViewerHandle, error) {
	viewerID, err := randomID()
	if err != nil {
		return ViewerHandle{}, err
	}
	now := m.now()
	v := &Viewer{
		ID:        viewerID,
		SessionID: sess.id,
		UserID:    user.ID,
		Username:  user.Username,
		LastSeen:  now,
		JoinedAt:  now,
		MaxHeight: maxHeight,
	}
	sess.viewers[viewerID] = v
	sess.emptySince = time.Time{}
	m.viewers[viewerID] = v
	delete(m.pending, res)
	return ViewerHandle{
		ViewerID:   viewerID,
		SessionID:  sess.id,
		SessionDir: sess.dir,
	}, nil
}

// waitPlaylist polls for the layout's ready playlist up to playlistTimeout
// using the injectable clock. FFmpeg failures match errPlaylistNotReady.
func (m *Manager) waitPlaylist(ctx context.Context, dir, name string, proc Process) error {
	deadline := m.now().Add(playlistTimeout)
	path := filepath.Join(dir, name)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-proc.Done():
			if err != nil {
				return playlistNotReadyError{fmt.Errorf("ffmpeg exited before playlist ready: %w", err)}
			}
			return playlistNotReadyError{fmt.Errorf("ffmpeg exited before playlist ready")}
		default:
		}
		if !m.now().Before(deadline) {
			return playlistNotReadyError{fmt.Errorf("playlist timeout waiting for %s", name)}
		}
		time.Sleep(playlistPollEvery)
	}
}

// Touch records a heartbeat for viewerID. Returns false if unknown.
func (m *Manager) Touch(viewerID string) bool {
	m.mu.Lock()
	v, ok := m.viewers[viewerID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	now := m.now()
	v.LastSeen = now
	var watchedCh int64
	if !v.watched && now.Sub(v.JoinedAt) >= watchedAfter {
		if sess, ok := m.sessions[v.SessionID]; ok {
			v.watched = true
			watchedCh = sess.channelID
		}
	}
	userID, onWatched := v.UserID, m.onWatched
	m.mu.Unlock()
	if watchedCh != 0 && onWatched != nil {
		onWatched(userID, watchedCh, now)
	}
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

// ChannelReception returns a channel's last tune outcome (see Reception).
func (m *Manager) ChannelReception(channelID int64) (Reception, bool) {
	if m.ingest == nil {
		return Reception{}, false
	}
	return m.ingest.Reception(channelID)
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
			if now := m.now(); now.Sub(m.lastParental) >= parentalEvery && m.parentalRunning.CompareAndSwap(false, true) {
				m.lastParental = now
				// Off the main loop: the checks read the store and guide.
				go func() {
					defer m.parentalRunning.Store(false)
					m.enforceParental(now)
				}()
			}
		}
	}
}

// maintain reaps idle viewers, tears down empty sessions past grace, and
// restarts crashed processes after backoff. Called from Run's ticker; tests
// invoke it directly after advancing the fake clock.
func (m *Manager) maintain() {
	m.mu.Lock()
	now := m.now()

	// Reap idle viewers (>viewerIdleTimeout since last heartbeat/playlist Touch).
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

	var due []*session
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
		if sess.crashed && !sess.restarting && !sess.restartAfter.IsZero() && !now.Before(sess.restartAfter) {
			if m.prepareRestartLocked(sess) {
				due = append(due, sess)
			}
		}
	}
	m.mu.Unlock()

	// Device attach and FFmpeg start can block (a slow tuner answers 807
	// after ~10s), so they run without m.mu; viewers keep being served.
	for _, sess := range due {
		m.restartSession(sess)
	}
}

// prepareRestartLocked does the in-lock part of a restart: cancel the old
// process context, make sure the dir exists, drop the old sub, and mark the
// session as restarting. It reports false (with backoff applied) on failure.
func (m *Manager) prepareRestartLocked(sess *session) bool {
	if sess.procCancel != nil {
		sess.procCancel()
		sess.procCancel = nil
	}
	// Defensive: ensure the segment dir exists (e.g. was removed by a failed race path).
	if err := os.MkdirAll(sess.dir, 0o755); err != nil {
		log.Printf("stream: session %s: mkdir session dir %s for restart: %v", sess.id, sess.dir, err)
		m.restartFailedLocked(sess)
		return false
	}
	// Ordered contract: Close(old sub) → Attach → Stdin → Start.
	// Proc-death already Closes; Close again is safe (double-Close).
	sess.closeSubs()
	if m.ingest == nil {
		log.Printf("stream: restart %s: ingest not configured", sess.id)
		m.restartFailedLocked(sess)
		return false
	}
	sess.restarting = true
	return true
}

// restartSession attaches to the device and starts FFmpeg without m.mu, then
// commits under m.mu. A session torn down meanwhile discards the new process.
func (m *Manager) restartSession(sess *session) {
	sub, err := m.ingest.Attach(context.Background(), sess.channelID, sess.inputURL)
	if err != nil {
		log.Printf("stream: session %s: re-Attach channel %d for restart: %v", sess.id, sess.channelID, err)
		m.mu.Lock()
		sess.restarting = false
		m.restartFailedLocked(sess)
		m.mu.Unlock()
		return
	}

	layout := sess.layout
	capSub, err := m.attachCaptions(context.Background(), sess.channelID, sess.inputURL, layout)
	if err != nil {
		log.Printf("stream: session %s: caption tap re-attach failed: %v (this process runs without captions)", sess.id, err)
		layout.Captions = false
	}

	procCtx, procCancel := context.WithCancel(context.Background())
	// Buffer window is fixed at session start (not live-mutable mid-session).
	// Append: continue the existing playlists instead of restarting at _00000.
	spec := transcode.JobSpec{Stdin: sub.R, OutDir: sess.dir, D: sess.decision, HLSListSize: sess.hlsListSize, Append: true, Layout: layout}
	if capSub != nil {
		spec.CaptionInput = capSub.R
	}
	log.Printf("stream: session %s: restarting ffmpeg (append to playlist)", sess.id)
	proc, err := m.runner.Start(procCtx, spec)
	if err != nil {
		log.Printf("stream: session %s: ffmpeg restart failed: %v", sess.id, err)
		_ = sub.Close()
		closeSub(capSub)
		procCancel()
		m.mu.Lock()
		sess.restarting = false
		m.restartFailedLocked(sess)
		m.mu.Unlock()
		return
	}
	sub.OnForceClose(proc.Stop)

	m.mu.Lock()
	defer m.mu.Unlock()
	sess.restarting = false
	if sess.terminated {
		proc.Stop()
		procCancel()
		_ = sub.Close()
		closeSub(capSub)
		return
	}
	log.Printf("stream: session %s channel %d: ffmpeg restarted", sess.id, sess.channelID)
	sess.sub = sub
	sess.capSub = capSub
	sess.proc = proc
	sess.procCancel = procCancel
	sess.procStart = m.now()
	sess.crashed = false
	sess.restartAfter = time.Time{}
}

func (m *Manager) restartFailedLocked(sess *session) {
	sess.backoff = nextBackoff(sess.backoff)
	sess.restartAfter = m.now().Add(sess.backoff)
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
		// Proc-death: Close sub immediately. Tail absorbs quick restarts
		// (restart backoff 1s+2s < 5s tail → no redial). Longer backoff can
		// exceed the tail; that redial is CORRECT and expected (A4).
		sess.closeSubs()
		now := m.now()
		log.Printf("stream: session %s channel %d (%s): ffmpeg exited after %v: %v",
			sess.id, sess.channelID, sess.decision.Backend, now.Sub(sess.procStart).Round(time.Second), err)
		sess.backoff = computeCrashBackoff(sess.backoff, sess.procStart, now)
		sess.crashed = true
		sess.restartAfter = now.Add(sess.backoff)
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
	// Terminate/teardown Close (A4).
	sess.closeSubs()
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

	if m.ingest != nil {
		m.ingest.Shutdown()
	}

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

var errPlaylistNotReady = errors.New("playlist not ready")

// playlistNotReadyError keeps FFmpeg's message and matches errPlaylistNotReady.
type playlistNotReadyError struct{ err error }

func (e playlistNotReadyError) Error() string   { return e.err.Error() }
func (e playlistNotReadyError) Unwrap() []error { return []error{e.err, errPlaylistNotReady} }

// sessionKey: one ladder per channel when adaptive; otherwise per codec and
// profile. The audio mode is not part of the key: AAC and AC-3 are renditions
// of one output.
func sessionKey(channelID int64, d transcode.Decision, adaptive bool) string {
	if adaptive {
		return fmt.Sprintf("ch%d|ladder", channelID)
	}
	return fmt.Sprintf("ch%d|%s|%s", channelID, d.VideoCodec, d.Profile.Name)
}

// adaptiveForStart reports whether this start uses the shared ladder
// (admin switch on and a hardware encoder; libx264 never ladders).
func (m *Manager) adaptiveForStart(d transcode.Decision) (bool, error) {
	if m.settings == nil || d.VideoEncoder == "libx264" {
		return false, nil
	}
	s, err := m.settings.Streaming()
	if err != nil {
		return false, fmt.Errorf("streaming settings: %w", err)
	}
	return s.Adaptive, nil
}

// layoutFor builds the session's output once the video sub is attached. The
// rungs always follow the broadcast (ladder ≤ source, or one rung capped at
// the source height); tracks adds every audio track, the AC-3 copies and
// captions (off with the BOWTIE_MULTITRACK kill switch or in the fallback).
func (m *Manager) layoutFor(channelID int64, d transcode.Decision, adaptive, tracks bool) transcode.Layout {
	info, ok := m.ingest.ProgramInfo(channelID, m.trackProbe)
	src := info.SourceHeight
	l := transcode.Layout{VideoCodec: d.VideoCodec, AudioKbps: d.Profile.AudioKbps}
	if adaptive {
		l.Rungs = transcode.Ladder(src)
		l.AudioKbps = 128
	} else {
		h := d.Profile.Height
		if src > 0 && src < h {
			h = src // never upscale past the broadcast
		}
		l.Rungs = []transcode.Rung{{Height: h, VideoKbps: d.Profile.VideoKbps}}
	}
	if ok && len(info.Audio) > 0 {
		l.Audio = info.Audio
	}
	if !tracks {
		if len(l.Audio) > 0 {
			l.Audio = []transcode.AudioTrack{{Lang: l.Audio[0].Lang}}
		}
		return l
	}
	l.Captions = true
	l.AC3Copy = true
	return l
}

// attachCaptions attaches the caption tap when the layout has captions.
func (m *Manager) attachCaptions(ctx context.Context, channelID int64, inputURL string, l transcode.Layout) (*IngestSub, error) {
	if !l.Captions {
		return nil, nil
	}
	return m.ingest.Attach(ctx, channelID, inputURL)
}

func closeSub(s *IngestSub) {
	if s != nil {
		_ = s.Close()
	}
}

// SessionMediaOf returns what the playlist handlers need for a viewer.
func (m *Manager) SessionMediaOf(viewerID string) (SessionMedia, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return SessionMedia{}, false
	}
	sess, ok := m.sessions[v.SessionID]
	if !ok || sess.terminated {
		return SessionMedia{}, false
	}
	return SessionMedia{Dir: sess.dir, Layout: sess.layout, MaxHeight: v.MaxHeight}, true
}
