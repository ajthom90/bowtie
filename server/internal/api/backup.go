package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// handleAdminBackup serves GET /api/v1/admin/backup: a snapshot of the
// database (accounts, channels, guide, recording list and settings — not the
// recordings themselves, signing secrets or sign-in sessions). Restore by
// stopping Bowtie and replacing bowtie.db.
func (s *Server) handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	f, done, err := s.deps.Store.Backup()
	if err != nil {
		log.Printf("backup: %v", err)
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}
	defer done()
	name := fmt.Sprintf("bowtie-backup-%s.db", time.Now().Format("20060102-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	}
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("backup: send: %v", err)
	}
}
