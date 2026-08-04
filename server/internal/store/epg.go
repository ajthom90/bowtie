package store

import (
	"fmt"
	"strings"
	"time"
)

// EPGChannel is a guide channel from an EPG source.
type EPGChannel struct {
	ID          string
	DisplayName string
	Callsign    string
	IconURL     string
	Source      string // "xmltv"|"sd"
}

// Program is a scheduled programme on an EPG channel.
type Program struct {
	ID           int64
	EPGChannelID string
	Start        time.Time
	Stop         time.Time
	Title        string
	Subtitle     string
	Description  string
	Category     string
	IconURL      string
}

// ReplaceEPG transactionally replaces all EPG data for the given source.
func (s *Store) ReplaceEPG(source string, chans []EPGChannel, progs []Program) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Delete programs belonging to channels of this source.
	if _, err := tx.Exec(`
		DELETE FROM programs
		WHERE epg_channel_id IN (SELECT id FROM epg_channels WHERE source = ?)
	`, source); err != nil {
		return fmt.Errorf("delete programs for source %s: %w", source, err)
	}
	if _, err := tx.Exec(`DELETE FROM epg_channels WHERE source = ?`, source); err != nil {
		return fmt.Errorf("delete epg_channels for source %s: %w", source, err)
	}

	for _, c := range chans {
		if c.Source == "" {
			c.Source = source
		}
		if _, err := tx.Exec(`
			INSERT INTO epg_channels (id, display_name, callsign, icon_url, source)
			VALUES (?, ?, ?, ?, ?)
		`, c.ID, c.DisplayName, c.Callsign, c.IconURL, c.Source); err != nil {
			return fmt.Errorf("insert epg channel %s: %w", c.ID, err)
		}
	}
	for _, p := range progs {
		if _, err := tx.Exec(`
			INSERT INTO programs (
				epg_channel_id, start, stop, title, subtitle, description, category, icon_url
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, p.EPGChannelID, formatTime(p.Start), formatTime(p.Stop),
			p.Title, p.Subtitle, p.Description, p.Category, p.IconURL); err != nil {
			return fmt.Errorf("insert program %q: %w", p.Title, err)
		}
	}

	return tx.Commit()
}

// ListEPGChannels returns all EPG channels ordered by id.
func (s *Store) ListEPGChannels() ([]EPGChannel, error) {
	rows, err := s.db.Query(`
		SELECT id, display_name, callsign, icon_url, source
		FROM epg_channels ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []EPGChannel
	for rows.Next() {
		var c EPGChannel
		if err := rows.Scan(&c.ID, &c.DisplayName, &c.Callsign, &c.IconURL, &c.Source); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ProgramsInRange returns programs on the given EPG channels that overlap
// [start, stop), i.e. Stop > start && Start < stop.
func (s *Store) ProgramsInRange(epgChannelIDs []string, start, stop time.Time) ([]Program, error) {
	if len(epgChannelIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(epgChannelIDs))
	args := make([]any, 0, len(epgChannelIDs)+2)
	for i, id := range epgChannelIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, formatTime(start), formatTime(stop))

	q := fmt.Sprintf(`
		SELECT id, epg_channel_id, start, stop, title, subtitle, description, category, icon_url
		FROM programs
		WHERE epg_channel_id IN (%s)
		  AND stop > ?
		  AND start < ?
		ORDER BY start
	`, strings.Join(placeholders, ","))

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Program
	for rows.Next() {
		var p Program
		var startS, stopS string
		if err := rows.Scan(
			&p.ID, &p.EPGChannelID, &startS, &stopS,
			&p.Title, &p.Subtitle, &p.Description, &p.Category, &p.IconURL,
		); err != nil {
			return nil, err
		}
		p.Start, err = parseTime(startS)
		if err != nil {
			return nil, err
		}
		p.Stop, err = parseTime(stopS)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PrunePrograms deletes programs that ended before olderThan.
func (s *Store) PrunePrograms(olderThan time.Time) error {
	_, err := s.db.Exec(`DELETE FROM programs WHERE stop < ?`, formatTime(olderThan))
	return err
}
