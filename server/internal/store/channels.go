package store

import "database/sql"

// Channel is a lineup entry from an HDHomeRun device.
type Channel struct {
	ID           int64
	DeviceID     string
	GuideNumber  string
	Name         string
	Enabled      bool
	EPGChannelID string // "" = unmapped
}

// SyncLineup upserts channels by (deviceID, guideNumber), preserves Enabled and
// EPGChannelID on existing rows, and deletes rows for the device that are absent
// from the new lineup.
func (s *Store) SyncLineup(deviceID string, chans []Channel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Load existing (guide_number -> id, enabled, epg_channel_id)
	rows, err := tx.Query(`
		SELECT id, guide_number, enabled, epg_channel_id
		FROM channels WHERE device_id = ?
	`, deviceID)
	if err != nil {
		return err
	}
	type existing struct {
		id           int64
		enabled      bool
		epgChannelID string
	}
	byGuide := map[string]existing{}
	for rows.Next() {
		var id int64
		var guide string
		var en int
		var epg string
		if err := rows.Scan(&id, &guide, &en, &epg); err != nil {
			_ = rows.Close()
			return err
		}
		byGuide[guide] = existing{id: id, enabled: en != 0, epgChannelID: epg}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	seen := map[string]bool{}
	for _, c := range chans {
		seen[c.GuideNumber] = true
		if ex, ok := byGuide[c.GuideNumber]; ok {
			// Preserve enabled + epg mapping; update name.
			if _, err := tx.Exec(`
				UPDATE channels SET name = ?, enabled = ?, epg_channel_id = ?
				WHERE id = ?
			`, c.Name, boolToInt(ex.enabled), ex.epgChannelID, ex.id); err != nil {
				return err
			}
			continue
		}
		// New channel: disabled, unmapped.
		if _, err := tx.Exec(`
			INSERT INTO channels (device_id, guide_number, name, enabled, epg_channel_id)
			VALUES (?, ?, ?, 0, '')
		`, deviceID, c.GuideNumber, c.Name); err != nil {
			return err
		}
	}

	// Delete absent guide numbers for this device.
	for guide, ex := range byGuide {
		if seen[guide] {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM channels WHERE id = ?`, ex.id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ListChannels returns channels ordered by device_id, guide_number.
// If enabledOnly is true, only enabled channels are returned.
func (s *Store) ListChannels(enabledOnly bool) ([]Channel, error) {
	q := `
		SELECT id, device_id, guide_number, name, enabled, epg_channel_id
		FROM channels
	`
	if enabledOnly {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY device_id, guide_number`

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChannelByID returns a channel by primary key.
func (s *Store) ChannelByID(id int64) (Channel, error) {
	return scanChannel(s.db.QueryRow(`
		SELECT id, device_id, guide_number, name, enabled, epg_channel_id
		FROM channels WHERE id = ?
	`, id))
}

// UpdateChannel sets enabled and epgChannelID for a channel.
func (s *Store) UpdateChannel(id int64, enabled bool, epgChannelID string) error {
	res, err := s.db.Exec(`
		UPDATE channels SET enabled = ?, epg_channel_id = ? WHERE id = ?
	`, boolToInt(enabled), epgChannelID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanChannel(row scannable) (Channel, error) {
	var c Channel
	var en int
	err := row.Scan(&c.ID, &c.DeviceID, &c.GuideNumber, &c.Name, &en, &c.EPGChannelID)
	if err != nil {
		return Channel{}, err
	}
	c.Enabled = en != 0
	return c, nil
}
