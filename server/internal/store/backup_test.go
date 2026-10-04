package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// A backup is a complete, openable copy of the database.
func TestBackupWritesOpenableCopy(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateUser(store.User{Username: "alice", PasswordHash: "h", Role: "admin", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy.db")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(f); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	_ = f.Close()

	c, err := store.Open(dst)
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer func() { _ = c.Close() }()
	if n, err := c.CountUsers(); err != nil || n != 1 {
		t.Fatalf("copy users = %d, %v; want 1", n, err)
	}
}
