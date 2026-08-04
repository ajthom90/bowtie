package auth_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func openTestAuth(t *testing.T) (*auth.Auth, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"), // 32 bytes
		Store:  s,
	}
	return a, s
}

func seedUser(t *testing.T, s *store.Store) store.User {
	t.Helper()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	id, err := s.CreateUser(store.User{
		Username:     "alice",
		PasswordHash: "hash",
		Role:         "admin",
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, err := s.UserByID(id)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	return u
}

func TestAccessTokenRoundTrip(t *testing.T) {
	a, s := openTestAuth(t)
	u := seedUser(t, s)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tok, err := a.NewAccessToken(u, now)
	if err != nil {
		t.Fatalf("NewAccessToken: %v", err)
	}
	if tok == "" {
		t.Fatal("NewAccessToken returned empty token")
	}

	claims, err := a.ParseAccessToken(tok, now)
	if err != nil {
		t.Fatalf("ParseAccessToken: %v", err)
	}
	if claims.UserID != u.ID || claims.Username != u.Username || claims.Role != u.Role {
		t.Fatalf("claims = %+v, want userID=%d username=%q role=%q", claims, u.ID, u.Username, u.Role)
	}

	// Still valid near end of window
	claims, err = a.ParseAccessToken(tok, now.Add(14*time.Minute))
	if err != nil {
		t.Fatalf("ParseAccessToken at +14m: %v", err)
	}
	if claims.UserID != u.ID {
		t.Fatalf("claims.UserID = %d", claims.UserID)
	}
}

func TestAccessTokenExpired(t *testing.T) {
	a, s := openTestAuth(t)
	u := seedUser(t, s)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tok, err := a.NewAccessToken(u, now)
	if err != nil {
		t.Fatalf("NewAccessToken: %v", err)
	}

	_, err = a.ParseAccessToken(tok, now.Add(16*time.Minute))
	if err == nil {
		t.Fatal("ParseAccessToken at +16m: want error")
	}
}

func TestRefreshRotate(t *testing.T) {
	a, s := openTestAuth(t)
	u := seedUser(t, s)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	raw, err := a.NewRefreshToken(u.ID, now)
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	if raw == "" {
		t.Fatal("NewRefreshToken returned empty")
	}

	gotUser, newRaw, err := a.Rotate(raw, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if gotUser.ID != u.ID || gotUser.Username != u.Username {
		t.Fatalf("Rotate user = %+v, want id=%d username=%q", gotUser, u.ID, u.Username)
	}
	if newRaw == "" || newRaw == raw {
		t.Fatalf("Rotate new token = %q, want different non-empty", newRaw)
	}

	// Old token must now be invalid
	_, _, err = a.Rotate(raw, now.Add(2*time.Hour))
	if err == nil {
		t.Fatal("Rotate with old token: want error")
	}

	// New token still works
	_, newer, err := a.Rotate(newRaw, now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Rotate with new token: %v", err)
	}
	if newer == "" || newer == newRaw {
		t.Fatalf("second rotate token = %q", newer)
	}
}

func TestRefreshExpired(t *testing.T) {
	a, s := openTestAuth(t)
	u := seedUser(t, s)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	raw, err := a.NewRefreshToken(u.ID, now)
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}

	// 30 days + 1 second past issue time
	_, _, err = a.Rotate(raw, now.Add(30*24*time.Hour+time.Second))
	if err == nil {
		t.Fatal("Rotate expired refresh: want error")
	}
}
