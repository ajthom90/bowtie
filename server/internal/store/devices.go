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
	// StreamPort is the port for /auto/v* stream URLs.
	// Default 5004 (real HDHomeRun). Non-80 BaseURL ports (e.g. hdhrfake) store that port.
	StreamPort int
}

// UpsertDevice inserts or updates a device by DeviceID.
func (s *Store) UpsertDevice(d Device) error {
	if d.StreamPort <= 0 {
		d.StreamPort = 5004
	}
	_, err := s.db.Exec(`
		INSERT INTO devices (device_id, ip, model, tuner_count, manual, last_seen, stream_port)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			ip = excluded.ip,
			model = excluded.model,
			tuner_count = excluded.tuner_count,
			manual = excluded.manual,
			last_seen = excluded.last_seen,
			stream_port = excluded.stream_port
	`, d.DeviceID, d.IP, d.Model, d.TunerCount, boolToInt(d.Manual), formatTime(d.LastSeen), d.StreamPort)
	return err
}

// ListDevices returns all devices ordered by device_id.
func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`
		SELECT device_id, ip, model, tuner_count, manual, last_seen, stream_port
		FROM devices ORDER BY device_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Device
	for rows.Next() {
		var d Device
		var manual int
		var lastSeen string
		if err := rows.Scan(&d.DeviceID, &d.IP, &d.Model, &d.TunerCount, &manual, &lastSeen, &d.StreamPort); err != nil {
			return nil, err
		}
		d.Manual = manual != 0
		d.LastSeen, err = parseTime(lastSeen)
		if err != nil {
			return nil, err
		}
		if d.StreamPort <= 0 {
			d.StreamPort = 5004
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeviceByID returns a device by id, or sql.ErrNoRows.
func (s *Store) DeviceByID(deviceID string) (Device, error) {
	var d Device
	var manual int
	var lastSeen string
	err := s.db.QueryRow(`
		SELECT device_id, ip, model, tuner_count, manual, last_seen, stream_port
		FROM devices WHERE device_id = ?
	`, deviceID).Scan(&d.DeviceID, &d.IP, &d.Model, &d.TunerCount, &manual, &lastSeen, &d.StreamPort)
	if err != nil {
		return Device{}, err
	}
	d.Manual = manual != 0
	d.LastSeen, err = parseTime(lastSeen)
	if err != nil {
		return Device{}, err
	}
	if d.StreamPort <= 0 {
		d.StreamPort = 5004
	}
	return d, nil
}

// DeleteDevice removes a device by id.
func (s *Store) DeleteDevice(deviceID string) error {
	_, err := s.db.Exec(`DELETE FROM devices WHERE device_id = ?`, deviceID)
	return err
}
