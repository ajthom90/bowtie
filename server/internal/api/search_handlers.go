package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/store"
)

type searchHitJSON struct {
	ChannelID   int64               `json:"channelId"`
	GuideNumber string              `json:"guideNumber"`
	ChannelName string              `json:"channelName"`
	LogoURL     string              `json:"logoUrl"`
	Start       time.Time           `json:"start"`
	Stop        time.Time           `json:"stop"`
	Title       string              `json:"title"`
	Subtitle    string              `json:"subtitle"`
	Description string              `json:"description"`
	Category    string              `json:"category"`
	Rating      string              `json:"rating"`
	Locked      bool                `json:"locked"`
	Recording   *epg.GuideRecording `json:"recording,omitempty"`
}

// handleGuideSearch serves GET /api/v1/guide/search?q=…&limit=N (default 50,
// max 200): programs not yet over on enabled channels.
func (s *Server) handleGuideSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, 200)
	}
	hits, err := s.deps.Store.SearchPrograms(q, time.Now().UTC(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	icons, err := epgIconByID(s.deps.Store)
	if err != nil {
		icons = map[string]string{}
	}
	recs := s.recordingsByProgram()
	policy := s.callerPolicy(r)
	out := make([]searchHitJSON, 0, len(hits))
	for _, h := range hits {
		if !policy.ChannelAllowed(h.ChannelID) {
			continue
		}
		if !policy.ProgramAllowed(h.Rating) {
			h.Description = ""
		}
		out = append(out, searchHitJSON{
			Rating: h.Rating, Locked: !policy.ProgramAllowed(h.Rating),
			ChannelID: h.ChannelID, GuideNumber: h.GuideNumber, ChannelName: h.ChannelName,
			LogoURL: icons[h.EPGChannelID], Start: h.Start.UTC(), Stop: h.Stop.UTC(),
			Title: h.Title, Subtitle: h.Subtitle, Description: h.Description, Category: h.Category,
			Recording: recs[programKey{h.ChannelID, h.Start.Unix()}],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type programKey struct {
	ch    int64
	start int64
}

// recordingsByProgram maps (channel, program start) to its recording, for
// scheduled, in-progress and ready recordings.
func (s *Server) recordingsByProgram() map[programKey]*epg.GuideRecording {
	out := map[programKey]*epg.GuideRecording{}
	rows, err := s.deps.Store.ListRecordings(store.RecScheduled, store.RecWaiting, store.RecRecording, store.RecConverting, store.RecReady)
	if err != nil {
		return out
	}
	for _, rec := range rows {
		out[programKey{rec.ChannelID, rec.Start.Unix()}] = &epg.GuideRecording{ID: rec.ID, State: rec.State}
	}
	return out
}
