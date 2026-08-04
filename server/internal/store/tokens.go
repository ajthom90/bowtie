package store

import "time"

// RefreshToken is a stored (hashed) refresh token.
type RefreshToken struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
}

// SaveRefreshToken inserts a refresh token row.
func (s *Store) SaveRefreshToken(t RefreshToken) error {
	_, err := s.db.Exec(`
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES (?, ?, ?)
	`, t.UserID, t.TokenHash, formatTime(t.ExpiresAt))
	return err
}

// RefreshTokenByHash returns the token with the given hash, or sql.ErrNoRows.
func (s *Store) RefreshTokenByHash(hash string) (RefreshToken, error) {
	var t RefreshToken
	var exp string
	err := s.db.QueryRow(`
		SELECT id, user_id, token_hash, expires_at
		FROM refresh_tokens WHERE token_hash = ?
	`, hash).Scan(&t.ID, &t.UserID, &t.TokenHash, &exp)
	if err != nil {
		return RefreshToken{}, err
	}
	t.ExpiresAt, err = parseTime(exp)
	if err != nil {
		return RefreshToken{}, err
	}
	return t, nil
}

// DeleteRefreshToken removes a token by hash.
func (s *Store) DeleteRefreshToken(hash string) error {
	_, err := s.db.Exec(`DELETE FROM refresh_tokens WHERE token_hash = ?`, hash)
	return err
}

// DeleteExpiredRefreshTokens removes tokens with ExpiresAt before now.
func (s *Store) DeleteExpiredRefreshTokens(now time.Time) error {
	_, err := s.db.Exec(`DELETE FROM refresh_tokens WHERE expires_at < ?`, formatTime(now))
	return err
}
