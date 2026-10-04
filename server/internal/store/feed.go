package store

import "database/sql"

// SetFeedKeyHash stores the SHA-256 hex of a user's IPTV feed key ("" turns
// the feed off).
func (s *Store) SetFeedKeyHash(userID int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET feed_key_hash = ? WHERE id = ?`, hash, userID)
	return err
}

// UserByFeedKeyHash returns the user whose feed key hashes to hash.
func (s *Store) UserByFeedKeyHash(hash string) (User, error) {
	if hash == "" {
		return User{}, sql.ErrNoRows
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM users WHERE feed_key_hash = ?`, hash).Scan(&id); err != nil {
		return User{}, err
	}
	return s.UserByID(id)
}
