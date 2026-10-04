package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// backupOmitted are settings a backup leaves out: with them, anyone holding
// the file could sign tokens for the running server. A restored server makes
// new ones (everyone signs in again).
var backupOmitted = []string{"jwt_secret", "stream_token_secret"}

// Backup snapshots the whole database (minus signing secrets and sign-in
// sessions) into a file next to it and returns it open for reading; call done
// when finished to remove it. Safe while the server runs, but other database
// calls wait while the snapshot is written.
func (s *Store) Backup() (f *os.File, done func(), err error) {
	dir, err := os.MkdirTemp(filepath.Dir(s.path), ".backup-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	snap := filepath.Join(dir, "bowtie.db")
	if _, err = s.db.Exec(`VACUUM INTO ?`, snap); err != nil {
		return nil, nil, fmt.Errorf("snapshot: %w", err)
	}
	if err = scrubBackup(snap); err != nil {
		return nil, nil, fmt.Errorf("scrub: %w", err)
	}
	if f, err = os.Open(snap); err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close(); cleanup() }, nil
}

func scrubBackup(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	for _, k := range backupOmitted {
		if _, err := db.Exec(`DELETE FROM settings WHERE key = ?`, k); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`DELETE FROM refresh_tokens`); err != nil {
		return err
	}
	// Leave no deleted secret bytes in free pages.
	_, err = db.Exec(`VACUUM`)
	return err
}
