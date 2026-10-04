package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
)

const (
	defaultRecents = 8
	maxRecentsAPI  = 20
)

type recentJSON struct {
	ChannelID   int64     `json:"channelId"`
	GuideNumber string    `json:"guideNumber"`
	Name        string    `json:"name"`
	LogoURL     string    `json:"logoUrl"`
	WatchedAt   time.Time `json:"watchedAt"`
}

// callerFavorites returns the signed-in user's starred channel IDs (empty on
// any error: favorites never fail a channel or guide read).
func (s *Server) callerFavorites(r *http.Request) map[int64]bool {
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		return map[int64]bool{}
	}
	favs, err := s.deps.Store.FavoriteIDs(claims.UserID)
	if err != nil {
		return map[int64]bool{}
	}
	return favs
}

// handleSetFavorite serves PUT/DELETE /api/v1/me/favorites/{channelId}.
func (s *Server) handleSetFavorite(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		id, err := parsePathID(r, "channelId")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid channel id")
			return
		}
		if on {
			ch, err := s.deps.Store.ChannelByID(id)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !ch.Enabled) {
				writeError(w, http.StatusNotFound, "channel not found")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, "lookup failed")
				return
			}
		}
		if err := s.deps.Store.SetFavorite(claims.UserID, id, on); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update favorite")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleRecents serves GET /api/v1/me/recents?limit=N (default 8, max 20).
func (s *Server) handleRecents(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit := defaultRecents
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxRecentsAPI)
	}
	rows, err := s.deps.Store.Recents(claims.UserID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load recents")
		return
	}
	icons, err := epgIconByID(s.deps.Store)
	if err != nil {
		icons = map[string]string{}
	}
	policy := s.callerPolicy(r)
	out := make([]recentJSON, 0, len(rows))
	for _, rc := range rows {
		if !policy.ChannelAllowed(rc.ChannelID) {
			continue
		}
		logo := ""
		if ch, err := s.deps.Store.ChannelByID(rc.ChannelID); err == nil && ch.EPGChannelID != "" {
			logo = icons[ch.EPGChannelID]
		}
		out = append(out, recentJSON{
			ChannelID: rc.ChannelID, GuideNumber: rc.GuideNumber, Name: rc.Name,
			LogoURL: logo, WatchedAt: rc.WatchedAt.UTC(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleClearRecents serves DELETE /api/v1/me/recents.
func (s *Server) handleClearRecents(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.deps.Store.ClearRecents(claims.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear recents")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
