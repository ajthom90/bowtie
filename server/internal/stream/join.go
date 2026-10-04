package stream

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// ErrNotJoinable: the session to join is gone, on another channel, or plays
// a codec this client can't; the caller starts normally instead.
var ErrNotJoinable = errors.New("session can't be joined")

// Join adds user's viewer to an existing session (SharePlay: everyone in a
// group plays the sharer's playlist, so their program dates agree). Account
// limits apply; the viewer's quality ceiling is its own negotiated one.
func (m *Manager) Join(_ context.Context, user store.User, sessionID string, channelID int64, caps transcode.ClientCaps) (ViewerHandle, error) {
	ch, err := m.store.ChannelByID(channelID)
	if err != nil || (!ch.Enabled && user.Role != "admin") {
		return ViewerHandle{}, fmt.Errorf("%w: channel %d", ErrNotJoinable, channelID)
	}
	encoder, allowHEVC, err := m.transcodePrefs()
	if err != nil {
		return ViewerHandle{}, err
	}
	decision, err := transcode.Negotiate(caps, user.MaxQuality, m.caps, encoder, allowHEVC, transcode.DefaultProfiles())
	if err != nil {
		return ViewerHandle{}, fmt.Errorf("negotiate: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok || sess.terminated || sess.channelID != channelID {
		return ViewerHandle{}, fmt.Errorf("%w: %s", ErrNotJoinable, sessionID)
	}
	if !slices.Contains(caps.VideoCodecs, sess.decision.VideoCodec) {
		return ViewerHandle{}, fmt.Errorf("%w: client can't play %s", ErrNotJoinable, sess.decision.VideoCodec)
	}
	res, err := m.reserveLocked(user, channelID)
	if err != nil {
		return ViewerHandle{}, err
	}
	h, err := m.addViewerLocked(sess, user, res, decision.Profile.Height)
	if err != nil {
		delete(m.pending, res)
	}
	return h, err
}
