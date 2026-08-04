package stream

import (
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// Viewer is a single client attached to a shared transcode session.
type Viewer struct {
	ID        string
	SessionID string
	Username  string
	LastSeen  time.Time
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
	SessionDir string // contains live.m3u8
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
	procStart    time.Time // when current process was started (for 60s healthy reset)
	inputURL     string    // kept for restart JobSpec
	terminated   bool

	// empty grace: set when viewers drop to 0
	emptySince time.Time // zero if has viewers

	viewers map[string]*Viewer
}
