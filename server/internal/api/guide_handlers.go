package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// handleGuide serves GET /api/v1/guide?start=&stop= for authenticated users.
// Defaults to now..now+4h; span > 24h → 422.
func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	if s.deps.EPG == nil {
		writeError(w, http.StatusInternalServerError, "epg not configured")
		return
	}

	now := time.Now().UTC()
	start := now
	stop := now.Add(4 * time.Hour)

	if v := r.URL.Query().Get("start"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid start time (want RFC3339)")
			return
		}
		start = t.UTC()
	}
	if v := r.URL.Query().Get("stop"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid stop time (want RFC3339)")
			return
		}
		stop = t.UTC()
	}

	if !stop.After(start) {
		writeError(w, http.StatusBadRequest, "stop must be after start")
		return
	}
	if stop.Sub(start) > 24*time.Hour {
		writeError(w, http.StatusUnprocessableEntity, "span must not exceed 24 hours")
		return
	}

	guide, err := s.deps.EPG.Guide(r.Context(), start, stop)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load guide")
		return
	}
	writeJSON(w, http.StatusOK, guide)
}

// handleAdminEPGStatus serves GET /api/v1/admin/epg/status.
func (s *Server) handleAdminEPGStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.EPG == nil {
		writeError(w, http.StatusInternalServerError, "epg not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.deps.EPG.Status())
}

// handleAdminEPGRefresh serves POST /api/v1/admin/epg/refresh — 202, background RefreshAll.
func (s *Server) handleAdminEPGRefresh(w http.ResponseWriter, r *http.Request) {
	if s.deps.EPG == nil {
		writeError(w, http.StatusInternalServerError, "epg not configured")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.deps.EPG.RefreshAll(ctx); err != nil {
			log.Printf("epg refresh: %v", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// handleAdminEPGChannels serves GET /api/v1/admin/epg/channels (mapping dropdown).
func (s *Server) handleAdminEPGChannels(w http.ResponseWriter, r *http.Request) {
	chans, err := s.deps.Store.ListEPGChannels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list epg channels")
		return
	}
	out := make([]epgChannelJSON, 0, len(chans))
	for _, c := range chans {
		out = append(out, epgChannelToJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

type epgChannelJSON struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Callsign    string `json:"callsign"`
	IconURL     string `json:"iconUrl"`
	Source      string `json:"source"`
}

func epgChannelToJSON(c store.EPGChannel) epgChannelJSON {
	return epgChannelJSON{
		ID:          c.ID,
		DisplayName: c.DisplayName,
		Callsign:    c.Callsign,
		IconURL:     c.IconURL,
		Source:      c.Source,
	}
}
