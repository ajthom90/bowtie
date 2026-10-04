package api

import (
	"log"
	"net/http"
)

type dvrStorageJSON struct {
	Dir          string               `json:"dir"`
	UsedBytes    int64                `json:"usedBytes"`
	FreeBytes    int64                `json:"freeBytes"`
	TotalBytes   int64                `json:"totalBytes"`
	FloorBytes   int64                `json:"floorBytes"`
	MinFreeBytes int64                `json:"minFreeBytes"`
	Recordings   dvrStorageCountsJSON `json:"recordings"`
}

type dvrStorageCountsJSON struct {
	Ready     int `json:"ready"`
	Scheduled int `json:"scheduled"`
	Recording int `json:"recording"`
	Failed    int `json:"failed"`
}

// handleAdminDVRStorage serves GET /api/v1/admin/dvr/storage.
func (s *Server) handleAdminDVRStorage(w http.ResponseWriter, r *http.Request) {
	if !s.dvrReady(w) {
		return
	}
	st, err := s.deps.DVR.Storage()
	if err != nil {
		log.Printf("dvr storage: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to read recording storage")
		return
	}
	writeJSON(w, http.StatusOK, dvrStorageJSON{
		Dir: st.Dir, UsedBytes: st.UsedBytes, FreeBytes: st.FreeBytes, TotalBytes: st.TotalBytes,
		FloorBytes: st.FloorBytes, MinFreeBytes: st.MinFreeBytes,
		Recordings: dvrStorageCountsJSON{
			Ready: st.Recordings.Ready, Scheduled: st.Recordings.Scheduled,
			Recording: st.Recordings.Recording, Failed: st.Recordings.Failed,
		},
	})
}
