package store

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// User is an authenticated account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string // "admin"|"viewer"
	MaxQuality   string // profile name or "" = unlimited
	MaxStreams   int    // concurrent streams; 0 = unlimited
	MaxTuners    int    // tuners used alone (channels no other account watches); 0 = unlimited
	// Parental controls (see internal/parental). nil AllowedChannels = every
	// channel; MaxRating is a ladder level (0 = no limit).
	AllowedChannels []int64
	MaxRating       int
	BlockUnrated    bool
	CreatedAt       time.Time
}

// CreateUser inserts a user and returns its ID.
func (s *Store) CreateUser(u User) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO users (username, password_hash, role, max_quality, max_streams, max_tuners,
			allowed_channels, max_rating, block_unrated, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, u.Username, u.PasswordHash, u.Role, u.MaxQuality, u.MaxStreams, u.MaxTuners,
		joinIDs(u.AllowedChannels), u.MaxRating, boolToInt(u.BlockUnrated), formatTime(u.CreatedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UserByUsername returns the user with the given username, or sql.ErrNoRows.
func (s *Store) UserByUsername(name string) (User, error) {
	return s.scanUser(s.db.QueryRow(`
		SELECT id, username, password_hash, role, max_quality, max_streams, max_tuners, allowed_channels, max_rating, block_unrated, created_at
		FROM users WHERE username = ?
	`, name))
}

// UserByID returns the user with the given id, or sql.ErrNoRows.
func (s *Store) UserByID(id int64) (User, error) {
	return s.scanUser(s.db.QueryRow(`
		SELECT id, username, password_hash, role, max_quality, max_streams, max_tuners, allowed_channels, max_rating, block_unrated, created_at
		FROM users WHERE id = ?
	`, id))
}

// ListUsers returns all users ordered by id.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, password_hash, role, max_quality, max_streams, max_tuners, allowed_channels, max_rating, block_unrated, created_at
		FROM users ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUser updates username, role, maxQuality and limits for the user with u.ID.
func (s *Store) UpdateUser(u User) error {
	res, err := s.db.Exec(`
		UPDATE users SET username = ?, role = ?, max_quality = ?, max_streams = ?, max_tuners = ?,
			allowed_channels = ?, max_rating = ?, block_unrated = ? WHERE id = ?
	`, u.Username, u.Role, u.MaxQuality, u.MaxStreams, u.MaxTuners,
		joinIDs(u.AllowedChannels), u.MaxRating, boolToInt(u.BlockUnrated), u.ID)
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

// UpdatePassword sets the password hash for the user.
func (s *Store) UpdatePassword(id int64, hash string) error {
	res, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
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

// DeleteUser removes a user by id, with their favorites and recents.
func (s *Store) DeleteUser(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id)
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
	for _, q := range []string{
		`DELETE FROM user_favorites WHERE user_id = ?`,
		`DELETE FROM user_recents WHERE user_id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountUsers returns the number of users.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

type scannable interface {
	Scan(dest ...any) error
}

func (s *Store) scanUser(row scannable) (User, error) {
	return scanUserRow(row)
}

func scanUserRow(row scannable) (User, error) {
	var u User
	var created, allowed string
	var blockUnrated int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.MaxQuality, &u.MaxStreams, &u.MaxTuners,
		&allowed, &u.MaxRating, &blockUnrated, &created)
	if err != nil {
		return User{}, err
	}
	u.AllowedChannels, u.BlockUnrated = splitIDs(allowed), blockUnrated != 0
	u.CreatedAt, err = parseTime(created)
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// joinIDs stores a channel allowlist ("" for nil = every channel). An empty
// non-nil list (nothing allowed) is stored as "-".
func joinIDs(ids []int64) string {
	if ids == nil {
		return ""
	}
	if len(ids) == 0 {
		return "-"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func splitIDs(s string) []int64 {
	switch s {
	case "":
		return nil
	case "-":
		return []int64{}
	}
	var out []int64
	for _, p := range strings.Split(s, ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
			out = append(out, id)
		}
	}
	if out == nil {
		out = []int64{}
	}
	return out
}
