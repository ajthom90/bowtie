package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/ajthom90/bowtie/server/internal/store"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour
	refreshRawBytes = 32
)

// Auth issues and validates access/refresh tokens.
type Auth struct {
	Secret []byte
	Store  *store.Store
}

// Claims are the identity fields carried by an access JWT.
type Claims struct {
	UserID   int64
	Username string
	Role     string
}

type accessClaims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// NewAccessToken issues an HS256 JWT for u, expiring at now+15m.
// Claims: sub (userID as string), "username", "role".
func (a *Auth) NewAccessToken(u store.User, now time.Time) (string, error) {
	if len(a.Secret) == 0 {
		return "", errors.New("auth: empty JWT secret")
	}
	claims := accessClaims{
		Username: u.Username,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(u.ID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTokenTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(a.Secret)
}

// ParseAccessToken validates tok at the given now and returns claims.
func (a *Auth) ParseAccessToken(tok string, now time.Time) (Claims, error) {
	if len(a.Secret) == 0 {
		return Claims{}, errors.New("auth: empty JWT secret")
	}
	parsed, err := jwt.ParseWithClaims(
		tok,
		&accessClaims{},
		func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return a.Secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return Claims{}, err
	}
	ac, ok := parsed.Claims.(*accessClaims)
	if !ok || !parsed.Valid {
		return Claims{}, errors.New("invalid access token")
	}
	uid, err := strconv.ParseInt(ac.Subject, 10, 64)
	if err != nil {
		return Claims{}, fmt.Errorf("invalid subject: %w", err)
	}
	return Claims{
		UserID:   uid,
		Username: ac.Username,
		Role:     ac.Role,
	}, nil
}

// NewRefreshToken creates a raw base64url token (32 random bytes), stores its
// SHA-256 hex hash with expiry now+30d, and returns the raw token.
func (a *Auth) NewRefreshToken(userID int64, now time.Time) (string, error) {
	rawBytes := make([]byte, refreshRawBytes)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	hash := hashRefreshToken(raw)
	err := a.Store.SaveRefreshToken(store.RefreshToken{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: now.Add(refreshTokenTTL),
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// Rotate validates raw, deletes it, and issues a new refresh token.
// Returns the user and new raw token, or an error if unknown/expired.
func (a *Auth) Rotate(raw string, now time.Time) (store.User, string, error) {
	hash := hashRefreshToken(raw)
	tok, err := a.Store.RefreshTokenByHash(hash)
	if err != nil {
		return store.User{}, "", err
	}
	if !tok.ExpiresAt.After(now) {
		_ = a.Store.DeleteRefreshToken(hash)
		return store.User{}, "", errors.New("refresh token expired")
	}
	if err := a.Store.DeleteRefreshToken(hash); err != nil {
		return store.User{}, "", err
	}
	u, err := a.Store.UserByID(tok.UserID)
	if err != nil {
		return store.User{}, "", err
	}
	newRaw, err := a.NewRefreshToken(tok.UserID, now)
	if err != nil {
		return store.User{}, "", err
	}
	return u, newRaw, nil
}

// Revoke deletes the stored refresh token for raw (no error if already gone).
func (a *Auth) Revoke(raw string) error {
	return a.Store.DeleteRefreshToken(hashRefreshToken(raw))
}

func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
