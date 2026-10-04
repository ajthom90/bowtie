package stream

import (
	"context"
	"testing"
	"time"
)

// A program that changes into a blocked rating stops that viewer (others on
// the same session keep watching), and the reason is kept for its next
// request.
func TestEnforceParentalStopsBlockedViewer(t *testing.T) {
	m, _, clock, ch, alice, bob := limitsEnv(t)
	ha, err := m.Start(context.Background(), alice, ch[0], clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	hb, err := m.Start(context.Background(), bob, ch[0], clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	m.blockedFor = func(userID, channelID int64, _ time.Time) string {
		if userID == bob.ID && channelID == ch[0] {
			return "Blocked by parental controls (rated TV-MA)"
		}
		return ""
	}
	m.enforceParental(clock.Now())
	if !m.Touch(ha.ViewerID) {
		t.Fatal("unblocked viewer removed")
	}
	if m.Touch(hb.ViewerID) {
		t.Fatal("blocked viewer still watching")
	}
	if why, ok := m.BlockedReason(hb.ViewerID); !ok || why != "Blocked by parental controls (rated TV-MA)" {
		t.Fatalf("reason %q %v", why, ok)
	}
	if _, ok := m.BlockedReason(ha.ViewerID); ok {
		t.Fatal("reason for an unblocked viewer")
	}
}
