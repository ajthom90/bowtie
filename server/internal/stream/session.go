package stream

import (
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// Viewer is a single client attached to a shared transcode session.
type Viewer struct {
	ID        string
	SessionID string
	UserID    int64
	Username  string
	LastSeen  time.Time
	// MaxHeight is the viewer's quality ceiling (negotiated profile height);
	// the master playlist omits rungs above it.
	MaxHeight int
}

// SessionInfo is the admin-facing snapshot of an active session.
type SessionInfo struct {
	ID          string       `json:"id"`
	ChannelID   int64        `json:"channelId"`
	ChannelName string       `json:"channelName"`
	Key         string       `json:"key"`
	VideoCodec  string       `json:"videoCodec"`
	Profile     string       `json:"profile"`
	Backend     string       `json:"backend"`
	Viewers     []ViewerInfo `json:"viewers"`
	StartedAt   time.Time    `json:"startedAt"`
}

// ViewerInfo is a viewer entry inside SessionInfo.
type ViewerInfo struct {
	ID       string    `json:"id"`
	Username string    `json:"username"`
	LastSeen time.Time `json:"lastSeen"`
}

// ViewerHandle is returned from Start for the calling client.
type ViewerHandle struct {
	ViewerID   string
	SessionID  string
	SessionDir string // contains the session's playlists
}

// SessionMedia is what the HTTP layer needs to write a viewer's master playlist.
type SessionMedia struct {
	Dir       string
	Layout    transcode.Layout
	MaxHeight int
}

// session is the internal shared transcode session state.
type session struct {
	id          string
	channelID   int64
	channelName string
	key         string
	decision    transcode.Decision
	dir         string
	startedAt   time.Time

	// process supervision
	proc       Process
	procCancel func() // cancels the process-scoped context

	// restart / crash state
	backoff      time.Duration // last applied backoff; 0 = never crashed this "streak"
	restartAfter time.Time     // zero if not waiting to restart
	crashed      bool
	// restarting: maintain handed this session to restartSession (device
	// attach + FFmpeg start run without m.mu); not eligible again until done.
	restarting  bool
	procStart   time.Time        // when current process was started (for 60s healthy reset)
	inputURL    string           // device stream URL for re-Attach on restart (not JobSpec input)
	sub         *IngestSub       // process-scoped ingest; closed on every process death
	capSub      *IngestSub       // caption tap (fd 3); nil when the layout has no captions
	layout      transcode.Layout // fixed at start; restarts reuse it
	hlsListSize int              // fixed at session start (streaming.bufferMinutes → segments)
	terminated  bool

	// empty grace: set when viewers drop to 0
	emptySince time.Time // zero if has viewers

	viewers map[string]*Viewer
}

// closeSubs closes the process-scoped ingest subscribers (video, caption tap).
func (s *session) closeSubs() {
	if s.sub != nil {
		_ = s.sub.Close()
		s.sub = nil
	}
	if s.capSub != nil {
		_ = s.capSub.Close()
		s.capSub = nil
	}
}
