package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Backup writes a consistent snapshot of the whole database to w (a SQLite
// file that Open can read). It is safe while the server runs.
func (s *Store) Backup(w io.Writer) error {
	dir, err := os.MkdirTemp("", "bowtie-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	snap := filepath.Join(dir, "bowtie.db")
	if _, err := s.db.Exec(`VACUUM INTO ?`, snap); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	f, err := os.Open(snap)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}
