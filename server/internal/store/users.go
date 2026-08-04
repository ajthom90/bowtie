package store

import (
	"database/sql"
	"time"
)

// User is an authenticated account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string // "admin"|"viewer"
	MaxQuality   string // profile name or "" = unlimited
	CreatedAt    time.Time
}

// CreateUser inserts a user and returns its ID.
func (s *Store) CreateUser(u User) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO users (username, password_hash, role, max_quality, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, u.Username, u.PasswordHash, u.Role, u.MaxQuality, formatTime(u.CreatedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UserByUsername returns the user with the given username, or sql.ErrNoRows.
func (s *Store) UserByUsername(name string) (User, error) {
	return s.scanUser(s.db.QueryRow(`
		SELECT id, username, password_hash, role, max_quality, created_at
		FROM users WHERE username = ?
	`, name))
}

// UserByID returns the user with the given id, or sql.ErrNoRows.
func (s *Store) UserByID(id int64) (User, error) {
	return s.scanUser(s.db.QueryRow(`
		SELECT id, username, password_hash, role, max_quality, created_at
		FROM users WHERE id = ?
	`, id))
}

// ListUsers returns all users ordered by id.
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`
		SELECT id, username, password_hash, role, max_quality, created_at
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

// UpdateUser updates username, role, and maxQuality for the user with u.ID.
func (s *Store) UpdateUser(u User) error {
	res, err := s.db.Exec(`
		UPDATE users SET username = ?, role = ?, max_quality = ? WHERE id = ?
	`, u.Username, u.Role, u.MaxQuality, u.ID)
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

// DeleteUser removes a user by id.
func (s *Store) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
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
	var created string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.MaxQuality, &created)
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, err = parseTime(created)
	if err != nil {
		return User{}, err
	}
	return u, nil
}
