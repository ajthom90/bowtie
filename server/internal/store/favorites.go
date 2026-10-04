package store

import "time"

// maxRecents is how many recently watched channels are kept per user.
const maxRecents = 20

// RecentChannel is a recently watched (enabled) channel.
type RecentChannel struct {
	ChannelID   int64
	GuideNumber string
	Name        string
	WatchedAt   time.Time
}

// SetFavorite stars (on) or unstars a channel for a user. Idempotent.
func (s *Store) SetFavorite(userID, channelID int64, on bool) error {
	if !on {
		_, err := s.db.Exec(`DELETE FROM user_favorites WHERE user_id = ? AND channel_id = ?`, userID, channelID)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO user_favorites (user_id, channel_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id, channel_id) DO NOTHING
	`, userID, channelID, formatTime(time.Now().UTC()))
	return err
}

// FavoriteIDs returns the user's starred channel IDs.
func (s *Store) FavoriteIDs(userID int64) (map[int64]bool, error) {
	rows, err := s.db.Query(`SELECT channel_id FROM user_favorites WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RecordWatch marks channelID as watched by userID at `at`, keeping only the
// newest maxRecents rows for the user.
func (s *Store) RecordWatch(userID, channelID int64, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO user_recents (user_id, channel_id, watched_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id, channel_id) DO UPDATE SET watched_at = excluded.watched_at
	`, userID, channelID, formatTime(at)); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		DELETE FROM user_recents WHERE user_id = ? AND channel_id NOT IN (
			SELECT channel_id FROM user_recents WHERE user_id = ?
			ORDER BY watched_at DESC LIMIT ?)
	`, userID, userID, maxRecents); err != nil {
		return err
	}
	return tx.Commit()
}

// Recents returns up to limit recently watched enabled channels, newest first.
func (s *Store) Recents(userID int64, limit int) ([]RecentChannel, error) {
	rows, err := s.db.Query(`
		SELECT r.channel_id, c.guide_number, c.name, r.watched_at
		FROM user_recents r JOIN channels c ON c.id = r.channel_id
		WHERE r.user_id = ? AND c.enabled = 1
		ORDER BY r.watched_at DESC LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RecentChannel{}
	for rows.Next() {
		var r RecentChannel
		var at string
		if err := rows.Scan(&r.ChannelID, &r.GuideNumber, &r.Name, &at); err != nil {
			return nil, err
		}
		if r.WatchedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClearRecents forgets the user's watch history.
func (s *Store) ClearRecents(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM user_recents WHERE user_id = ?`, userID)
	return err
}
