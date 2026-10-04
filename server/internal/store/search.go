package store

import (
	"strings"
	"time"
)

// ProgramHit is a guide program found by SearchPrograms, with its channel.
type ProgramHit struct {
	Program
	ChannelID   int64
	GuideNumber string
	ChannelName string
}

// SearchPrograms finds programs that haven't ended by `now` on enabled
// channels whose title, episode title or description contains q (case-
// insensitive; % and _ are literal). Title matches come first, then by start.
func (s *Store) SearchPrograms(q string, now time.Time, limit int) ([]ProgramHit, error) {
	q = strings.TrimSpace(q)
	if q == "" || limit <= 0 {
		return []ProgramHit{}, nil
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	rows, err := s.db.Query(`
		SELECT p.id, p.epg_channel_id, p.start, p.stop, p.title, p.subtitle, p.description,
		       p.category, p.icon_url, c.id, c.guide_number, c.name
		FROM programs p JOIN channels c ON c.epg_channel_id = p.epg_channel_id
		WHERE c.enabled = 1 AND p.stop > ?
		  AND (p.title LIKE ? ESCAPE '\' OR p.subtitle LIKE ? ESCAPE '\' OR p.description LIKE ? ESCAPE '\')
		ORDER BY (p.title LIKE ? ESCAPE '\') DESC, p.start, c.guide_number
		LIMIT ?`,
		formatTime(now), like, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ProgramHit{}
	for rows.Next() {
		var h ProgramHit
		var start, stop string
		if err := rows.Scan(&h.ID, &h.EPGChannelID, &start, &stop, &h.Title, &h.Subtitle, &h.Description,
			&h.Category, &h.IconURL, &h.ChannelID, &h.GuideNumber, &h.ChannelName); err != nil {
			return nil, err
		}
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
