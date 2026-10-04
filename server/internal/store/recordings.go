package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// Recording states.
const (
	RecScheduled  = "scheduled"  // waiting for its start time
	RecWaiting    = "waiting"    // inside its window, no tuner free yet (retrying)
	RecRecording  = "recording"  // capturing
	RecConverting = "converting" // capture done, building the playable HLS
	RecReady      = "ready"      // playable
	RecFailed     = "failed"     // nothing usable (see Failure)
)

// Recording is a scheduled, in-progress or finished DVR recording.
type Recording struct {
	ID            int64
	UserID        int64
	ChannelID     int64
	ChannelName   string
	Title         string
	Subtitle      string
	Description   string
	Category      string
	IconURL       string
	Rating        string // the program's rating when scheduled (parental controls)
	RuleID        int64  // the series rule that scheduled it (0 = one-off)
	ProgramID     string // guide program ID (series de-duplication)
	Start, Stop   time.Time
	PadStartSec   int
	PadEndSec     int
	State         string
	Partial       bool
	Failure       string // noTuner|noSignal|diskFull|error
	FailureDetail string
	ActualStart   time.Time
	ActualStop    time.Time
	MissedSec     int
	Dir           string
	SizeBytes     int64
	DurationSec   int
	Protected     bool
	CreatedAt     time.Time
	// Commercials are the commercial breaks on the playback timeline (only
	// SetRecordingCommercials writes them). CommercialsDetected: detection
	// ran (an empty list then means none were found).
	Commercials         []Commercial
	CommercialsDetected bool
}

// Commercial is one commercial break, in seconds from the start of playback.
type Commercial struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// WindowStart is when capture begins (start minus padding).
func (r Recording) WindowStart() time.Time {
	return r.Start.Add(-time.Duration(r.PadStartSec) * time.Second)
}

// WindowStop is when capture ends (stop plus padding).
func (r Recording) WindowStop() time.Time {
	return r.Stop.Add(time.Duration(r.PadEndSec) * time.Second)
}

const recordingCols = `id, user_id, channel_id, channel_name, title, subtitle, description, category,
	icon_url, start, stop, pad_start_sec, pad_end_sec, state, partial, failure, failure_detail,
	actual_start, actual_stop, missed_sec, dir, size_bytes, duration_sec, protected, created_at, rating, rule_id, program_id, commercials`

// CreateRecording inserts r and returns its ID.
func (s *Store) CreateRecording(r Recording) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO recordings (user_id, channel_id, channel_name, title, subtitle,
		description, category, icon_url, start, stop, pad_start_sec, pad_end_sec, state, created_at, rating,
		rule_id, program_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.UserID, r.ChannelID, r.ChannelName, r.Title, r.Subtitle, r.Description, r.Category,
		r.IconURL, formatTime(r.Start), formatTime(r.Stop), r.PadStartSec, r.PadEndSec, r.State,
		formatTime(r.CreatedAt), r.Rating, r.RuleID, r.ProgramID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateRecording writes every mutable field of r except Protected, which
// only SetRecordingProtected changes (so the DVR never clobbers a Keep).
func (s *Store) UpdateRecording(r Recording) error {
	res, err := s.db.Exec(`UPDATE recordings SET start = ?, stop = ?, pad_start_sec = ?, pad_end_sec = ?,
		state = ?, partial = ?, failure = ?, failure_detail = ?, actual_start = ?, actual_stop = ?,
		missed_sec = ?, dir = ?, size_bytes = ?, duration_sec = ? WHERE id = ?`,
		formatTime(r.Start), formatTime(r.Stop), r.PadStartSec, r.PadEndSec, r.State,
		boolToInt(r.Partial), r.Failure, r.FailureDetail, formatOptTime(r.ActualStart),
		formatOptTime(r.ActualStop), r.MissedSec, r.Dir, r.SizeBytes, r.DurationSec, r.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecordingByID returns one recording or sql.ErrNoRows.
func (s *Store) RecordingByID(id int64) (Recording, error) {
	return scanRecording(s.db.QueryRow(`SELECT `+recordingCols+` FROM recordings WHERE id = ?`, id))
}

// ListRecordings returns recordings in the given states (all when none),
// ordered by start time.
func (s *Store) ListRecordings(states ...string) ([]Recording, error) {
	q := `SELECT ` + recordingCols + ` FROM recordings`
	args := make([]any, 0, len(states))
	if len(states) > 0 {
		q += ` WHERE state IN (?` + strings.Repeat(", ?", len(states)-1) + `)`
		for _, st := range states {
			args = append(args, st)
		}
	}
	rows, err := s.db.Query(q+` ORDER BY start, id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Recording{}
	for rows.Next() {
		r, err := scanRecording(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRecording removes the row and its playback positions.
func (s *Store) DeleteRecording(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM recordings WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM recording_positions WHERE recording_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRecordingPosition stores where userID stopped watching a recording.
func (s *Store) SetRecordingPosition(recordingID, userID int64, sec int) error {
	_, err := s.db.Exec(`INSERT INTO recording_positions (recording_id, user_id, position_sec, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT (recording_id, user_id)
		DO UPDATE SET position_sec = excluded.position_sec, updated_at = excluded.updated_at`,
		recordingID, userID, sec, formatTime(time.Now().UTC()))
	return err
}

// RecordingPosition returns userID's resume position (0 if none).
func (s *Store) RecordingPosition(recordingID, userID int64) (int, error) {
	var sec int
	err := s.db.QueryRow(`SELECT position_sec FROM recording_positions WHERE recording_id = ? AND user_id = ?`,
		recordingID, userID).Scan(&sec)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return sec, err
}

func formatOptTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}

func scanRecording(row scannable) (Recording, error) {
	var r Recording
	var start, stop, aStart, aStop, created string
	var partial, protected int
	var commercials sql.NullString
	if err := row.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.ChannelName, &r.Title, &r.Subtitle,
		&r.Description, &r.Category, &r.IconURL, &start, &stop, &r.PadStartSec, &r.PadEndSec,
		&r.State, &partial, &r.Failure, &r.FailureDetail, &aStart, &aStop, &r.MissedSec, &r.Dir,
		&r.SizeBytes, &r.DurationSec, &protected, &created, &r.Rating, &r.RuleID, &r.ProgramID,
		&commercials); err != nil {
		return Recording{}, err
	}
	r.Partial, r.Protected = partial != 0, protected != 0
	if commercials.Valid {
		r.CommercialsDetected = true
		var segs []Commercial
		if json.Unmarshal([]byte(commercials.String), &segs) == nil && len(segs) > 0 {
			r.Commercials = segs
		}
	}
	var err error
	for _, p := range []struct {
		dst *time.Time
		src string
	}{{&r.Start, start}, {&r.Stop, stop}, {&r.ActualStart, aStart}, {&r.ActualStop, aStop}, {&r.CreatedAt, created}} {
		if *p.dst, err = parseTime(p.src); err != nil {
			return Recording{}, err
		}
	}
	return r, nil
}

// SetRecordingCommercials stores the commercial breaks detection found (nil
// or empty: it ran and found none), touching nothing else.
func (s *Store) SetRecordingCommercials(id int64, segs []Commercial) error {
	if segs == nil {
		segs = []Commercial{}
	}
	b, err := json.Marshal(segs)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE recordings SET commercials = ? WHERE id = ?`, string(b), id)
	return err
}

// RecordingsNeedingCommercials returns up to limit ready recordings that
// commercial detection hasn't run on, newest first.
func (s *Store) RecordingsNeedingCommercials(limit int) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM recordings WHERE state = ? AND commercials IS NULL
		ORDER BY start DESC, id DESC LIMIT ?`, RecReady, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetRecordingProtected sets only the protected flag (safe while the DVR is
// updating the same row).
func (s *Store) SetRecordingProtected(id int64, on bool) error {
	_, err := s.db.Exec(`UPDATE recordings SET protected = ? WHERE id = ?`, boolToInt(on), id)
	return err
}

// StopRecordingAt moves a pending recording's window so it ends at stop (and
// starts no later than stop), touching nothing else. It reports false when
// the recording isn't scheduled, waiting or recording.
func (s *Store) StopRecordingAt(id int64, stop time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE recordings SET
			stop = CASE WHEN stop > ? THEN ? ELSE stop END,
			pad_end_sec = 0,
			pad_start_sec = CASE WHEN start > ? THEN 0 ELSE pad_start_sec END,
			start = CASE WHEN start > ? THEN ? ELSE start END
		WHERE id = ? AND state IN (?, ?, ?)`,
		formatTime(stop), formatTime(stop), formatTime(stop), formatTime(stop), formatTime(stop),
		id, RecScheduled, RecWaiting, RecRecording)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RecordingWatchedSince reports whether anyone saved a playback position for
// the recording since t (someone is watching it).
func (s *Store) RecordingWatchedSince(id int64, t time.Time) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM recording_positions WHERE recording_id = ? AND updated_at >= ?`,
		id, formatTime(t)).Scan(&n)
	return n > 0, err
}
