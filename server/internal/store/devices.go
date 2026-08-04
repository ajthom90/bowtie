package store

import "time"

// Device is a known HDHomeRun tuner device.
type Device struct {
	DeviceID   string
	IP         string
	Model      string
	TunerCount int
	Manual     bool
	LastSeen   time.Time
}

// UpsertDevice inserts or updates a device by DeviceID.
func (s *Store) UpsertDevice(d Device) error {
	_, err := s.db.Exec(`
		INSERT INTO devices (device_id, ip, model, tuner_count, manual, last_seen)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			ip = excluded.ip,
			model = excluded.model,
			tuner_count = excluded.tuner_count,
			manual = excluded.manual,
			last_seen = excluded.last_seen
	`, d.DeviceID, d.IP, d.Model, d.TunerCount, boolToInt(d.Manual), formatTime(d.LastSeen))
	return err
}

// ListDevices returns all devices ordered by device_id.
func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`
		SELECT device_id, ip, model, tuner_count, manual, last_seen
		FROM devices ORDER BY device_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		var d Device
		var manual int
		var lastSeen string
		if err := rows.Scan(&d.DeviceID, &d.IP, &d.Model, &d.TunerCount, &manual, &lastSeen); err != nil {
			return nil, err
		}
		d.Manual = manual != 0
		d.LastSeen, err = parseTime(lastSeen)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice removes a device by id.
func (s *Store) DeleteDevice(deviceID string) error {
	_, err := s.db.Exec(`DELETE FROM devices WHERE device_id = ?`, deviceID)
	return err
}
