package stream

import "time"

const (
	// parentalEvery: how often playing viewers are re-checked (a program can
	// change into a blocked rating mid-stream).
	parentalEvery = 30 * time.Second
	// blockedKeep: how long a stopped viewer's reason is kept for its
	// client's next request.
	blockedKeep = 10 * time.Minute
)

type blockedViewer struct {
	reason string
	at     time.Time
}

// enforceParental stops viewers whose account may no longer watch what's on
// (blockedFor returns a reason) and remembers why. Called from Run.
func (m *Manager) enforceParental(now time.Time) {
	if m.blockedFor == nil {
		return
	}
	type check struct {
		viewerID          string
		userID, channelID int64
	}
	m.mu.Lock()
	m.lastParental = now
	for id, b := range m.blocked {
		if now.Sub(b.at) > blockedKeep {
			delete(m.blocked, id)
		}
	}
	checks := make([]check, 0, len(m.viewers))
	for id, v := range m.viewers {
		if sess, ok := m.sessions[v.SessionID]; ok {
			checks = append(checks, check{id, v.UserID, sess.channelID})
		}
	}
	m.mu.Unlock()

	for _, c := range checks {
		why := m.blockedFor(c.userID, c.channelID, now)
		if why == "" {
			continue
		}
		m.mu.Lock()
		if _, ok := m.viewers[c.viewerID]; ok {
			m.removeViewerLocked(c.viewerID)
			m.blocked[c.viewerID] = blockedViewer{reason: why, at: now}
		}
		m.mu.Unlock()
	}
}

// BlockedReason reports why parental controls stopped viewerID, if they did.
func (m *Manager) BlockedReason(viewerID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocked[viewerID]
	return b.reason, ok
}
