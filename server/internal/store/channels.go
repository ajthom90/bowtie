package store

import (
	"cmp"
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

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
		for _, q := range []string{
			`DELETE FROM channels WHERE id = ?`,
			`DELETE FROM user_favorites WHERE channel_id = ?`,
			`DELETE FROM user_recents WHERE channel_id = ?`,
		} {
			if _, err := tx.Exec(q, ex.id); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// ListChannels returns channels in channel-number order (5.1, 5.2, 5.10,
// 11.1), then by device_id. If enabledOnly is true, only enabled channels
// are returned.
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if d := CompareGuideNumbers(out[i].GuideNumber, out[j].GuideNumber); d != 0 {
			return d < 0
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out, nil
}

// CompareGuideNumbers orders guide numbers ("9.1", "11.2", "5.10") part by
// part, numerically; a part that isn't a number sorts after numeric ones,
// then as text. Negative when a comes first.
func CompareGuideNumbers(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		switch {
		case i >= len(pa):
			return -1
		case i >= len(pb):
			return 1
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil:
			if na != nb {
				return cmp.Compare(na, nb)
			}
		case ea == nil:
			return -1
		case eb == nil:
			return 1
		default:
			if d := strings.Compare(pa[i], pb[i]); d != 0 {
				return d
			}
		}
	}
	return 0
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

// NoGuide is stored as a channel's EPG mapping when an admin chose "no
// guide", so automatic mapping leaves it alone ("" = never mapped).
const NoGuide = "-"

// AutoMapChannel sets a channel's guide mapping only if it was never mapped
// (never touching an admin's choice or Enabled). It reports whether it did.
func (s *Store) AutoMapChannel(id int64, epgChannelID string) (bool, error) {
	res, err := s.db.Exec(`UPDATE channels SET epg_channel_id = ? WHERE id = ? AND epg_channel_id = ''`, epgChannelID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearMappingsWithPrefix unmaps every channel whose guide mapping starts
// with prefix (an automatic source's mappings), leaving others alone.
func (s *Store) ClearMappingsWithPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE channels SET epg_channel_id = '' WHERE substr(epg_channel_id, 1, ?) = ?`, len(prefix), prefix)
	return err
}
