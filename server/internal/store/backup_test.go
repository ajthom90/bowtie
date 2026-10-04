package store_test

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// backupCopy takes a backup of s and writes it to a new file.
func backupCopy(t *testing.T, s *store.Store) string {
	t.Helper()
	f, done, err := s.Backup()
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	defer done()
	dst := filepath.Join(t.TempDir(), "copy.db")
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, f); err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
	return dst
}

// A backup is a complete, openable copy of the database.
func TestBackupWritesOpenableCopy(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateUser(store.User{Username: "alice", PasswordHash: "h", Role: "admin", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(backupCopy(t, s))
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer func() { _ = c.Close() }()
	if n, err := c.CountUsers(); err != nil || n != 1 {
		t.Fatalf("copy users = %d, %v; want 1", n, err)
	}
}

// The signing secrets and sign-in sessions stay out of a backup: a leaked
// file must not let anyone forge tokens for the running server.
func TestBackupOmitsSecretsAndSessions(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateUser(store.User{Username: "alice", PasswordHash: "h", Role: "admin", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"jwt_secret", "stream_token_secret"} {
		if err := s.SetSetting(k, "abcd"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveRefreshToken(store.RefreshToken{UserID: id, TokenHash: "th", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(backupCopy(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for _, k := range []string{"jwt_secret", "stream_token_secret"} {
		if v, _ := c.GetSetting(k); v != "" {
			t.Errorf("backup has %s", k)
		}
	}
	if _, err := c.RefreshTokenByHash("th"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("backup has refresh tokens (err=%v)", err)
	}
	// The live database is untouched.
	if v, _ := s.GetSetting("jwt_secret"); v != "abcd" {
		t.Fatalf("live jwt_secret = %q", v)
	}
}
