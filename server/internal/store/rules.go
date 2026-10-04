package store

import (
	"database/sql"
	"strings"
	"time"
)

// RecordingRule records every (new) airing of a show.
type RecordingRule struct {
	ID         int64
	UserID     int64
	Title      string
	SeriesID   string // "" = match the title exactly (any case)
	ChannelID  int64  // 0 = any channel
	NewOnly    bool
	KeepLatest int // 0 = keep every recording
	CreatedAt  time.Time
}

// CreateRule inserts a rule and returns its ID.
func (s *Store) CreateRule(r RecordingRule) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO recording_rules (user_id, title, series_id, channel_id, new_only, keep_latest, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, r.UserID, r.Title, r.SeriesID, r.ChannelID, boolToInt(r.NewOnly), r.KeepLatest, formatTime(r.CreatedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListRules returns every rule, oldest first.
func (s *Store) ListRules() ([]RecordingRule, error) {
	rows, err := s.db.Query(`SELECT id, user_id, title, series_id, channel_id, new_only, keep_latest, created_at
		FROM recording_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RecordingRule{}
	for rows.Next() {
		var r RecordingRule
		var newOnly int
		var created string
		if err := rows.Scan(&r.ID, &r.UserID, &r.Title, &r.SeriesID, &r.ChannelID, &newOnly, &r.KeepLatest, &created); err != nil {
			return nil, err
		}
		r.NewOnly = newOnly != 0
		if r.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RuleByID returns one rule.
func (s *Store) RuleByID(id int64) (RecordingRule, error) {
	rules, err := s.ListRules()
	if err != nil {
		return RecordingRule{}, err
	}
	for _, r := range rules {
		if r.ID == id {
			return r, nil
		}
	}
	return RecordingRule{}, sql.ErrNoRows
}

// DeleteRule removes a rule (its recordings stay).
func (s *Store) DeleteRule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM recording_rules WHERE id = ?`, id)
	return err
}

// RuleMatches lists airings starting in [from, to) on enabled channels that
// the rule records: same series (or exact title, any case), the rule's
// channel if set, and only new airings if NewOnly. Ordered by start.
func (s *Store) RuleMatches(r RecordingRule, from, to time.Time) ([]ProgramHit, error) {
	q := `SELECT p.id, p.epg_channel_id, p.start, p.stop, p.title, p.subtitle, p.description,
		       p.category, p.icon_url, p.rating, p.program_id, p.series_id, p.is_new,
		       c.id, c.guide_number, c.name
		FROM programs p JOIN channels c ON c.epg_channel_id = p.epg_channel_id
		WHERE c.enabled = 1 AND p.start >= ? AND p.start < ?`
	args := []any{formatTime(from), formatTime(to)}
	if r.SeriesID != "" {
		q += ` AND p.series_id = ?`
		args = append(args, r.SeriesID)
	} else {
		q += ` AND lower(p.title) = ?`
		args = append(args, strings.ToLower(strings.TrimSpace(r.Title)))
	}
	if r.ChannelID != 0 {
		q += ` AND c.id = ?`
		args = append(args, r.ChannelID)
	}
	if r.NewOnly {
		q += ` AND p.is_new = 1`
	}
	rows, err := s.db.Query(q+` ORDER BY p.start, c.guide_number`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ProgramHit{}
	for rows.Next() {
		var h ProgramHit
		var start, stop string
		var isNew int
		if err := rows.Scan(&h.ID, &h.EPGChannelID, &start, &stop, &h.Title, &h.Subtitle, &h.Description,
			&h.Category, &h.IconURL, &h.Rating, &h.ProgramID, &h.SeriesID, &isNew,
			&h.ChannelID, &h.GuideNumber, &h.ChannelName); err != nil {
			return nil, err
		}
		h.IsNew = isNew != 0
		if h.Start, err = parseTime(start); err != nil {
			return nil, err
		}
		if h.Stop, err = parseTime(stop); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
