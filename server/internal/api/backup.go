package api

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"time"
)

// handleAdminBackup serves GET /api/v1/admin/backup: a snapshot of the
// database (accounts, channels, guide, recording list and settings — not the
// recordings themselves). Restore by stopping Bowtie and replacing bowtie.db.
func (s *Server) handleAdminBackup(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if err := s.deps.Store.Backup(&buf); err != nil {
		log.Printf("backup: %v", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	name := fmt.Sprintf("bowtie-backup-%s.db", time.Now().Format("20060102-1504"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(buf.Len()))
	_, _ = w.Write(buf.Bytes())
}
