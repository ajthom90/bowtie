package stream

import (
	"fmt"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// limitStaleAfter: a viewer not seen for this long (two missed 15 s
// heartbeats) no longer counts toward its account's limits, so an app that
// was force-quit without a DELETE doesn't block the reopened app for the full
// viewerIdleTimeout.
const limitStaleAfter = 30 * time.Second

// UserLimitError is returned by Start when the account's stream or tuner
// limit (store.User.MaxStreams / MaxTuners) would be exceeded.
type UserLimitError struct {
	Kind  string // "streams" | "tuners"
	Limit int
}

func (e *UserLimitError) Error() string {
	if e.Kind == "tuners" {
		return fmt.Sprintf("Your account can use %d %s at a time. Stop another channel first.", e.Limit, plural(e.Limit, "tuner"))
	}
	return fmt.Sprintf("You're already watching on %d %s — your account allows %d.", e.Limit, plural(e.Limit, "device"), e.Limit)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// reservation holds a limited account's place while its Start runs (FFmpeg
// start happens outside m.mu), so concurrent starts can't all pass the check.
// addViewerLocked consumes it in the same critical section that adds the
// viewer; releaseReservation drops it if the start fails.
type reservation struct {
	userID    int64
	channelID int64
}

// reserveLocked checks user's limits for a new viewer on channelID and
// returns a reservation (nil when the account is unlimited). Joining a channel
// another account is watching costs no tuner; a channel is counted once
// however many sessions (qualities) it has. Caller holds m.mu.
func (m *Manager) reserveLocked(user store.User, channelID int64) (*reservation, error) {
	if user.Role == "admin" || (user.MaxStreams <= 0 && user.MaxTuners <= 0) {
		return nil, nil
	}
	now := m.now()
	streams := 0
	mine := map[int64]bool{}
	others := map[int64]bool{}
	for _, v := range m.viewers {
		if now.Sub(v.LastSeen) >= limitStaleAfter {
			continue
		}
		sess, ok := m.sessions[v.SessionID]
		if !ok || sess.terminated {
			continue
		}
		if v.UserID == user.ID {
			streams++
			mine[sess.channelID] = true
		} else {
			others[sess.channelID] = true
		}
	}
	for r := range m.pending {
		if r.userID == user.ID {
			streams++
			mine[r.channelID] = true
		} else {
			others[r.channelID] = true
		}
	}

	if user.MaxStreams > 0 && streams >= user.MaxStreams {
		return nil, &UserLimitError{Kind: "streams", Limit: user.MaxStreams}
	}
	if user.MaxTuners > 0 && !mine[channelID] && !others[channelID] {
		sole := 0
		for ch := range mine {
			if !others[ch] {
				sole++
			}
		}
		if sole >= user.MaxTuners {
			return nil, &UserLimitError{Kind: "tuners", Limit: user.MaxTuners}
		}
	}
	r := &reservation{userID: user.ID, channelID: channelID}
	m.pending[r] = struct{}{}
	return r, nil
}

// releaseReservation drops r if Start didn't consume it (failed start).
func (m *Manager) releaseReservation(r *reservation) {
	if r == nil {
		return
	}
	m.mu.Lock()
	delete(m.pending, r)
	m.mu.Unlock()
}
