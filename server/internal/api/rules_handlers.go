package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/store"
)

type ruleJSON struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	SeriesID    string    `json:"seriesId"`
	ChannelID   int64     `json:"channelId"` // 0 = any channel
	ChannelName string    `json:"channelName"`
	NewOnly     bool      `json:"newOnly"`
	KeepLatest  int       `json:"keepLatest"`
	ScheduledBy string    `json:"scheduledBy"`
	Editable    bool      `json:"editable"`
	CanManage   bool      `json:"canManage"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *Server) ruleToJSON(r store.RecordingRule, claims auth.Claims, names map[int64]string) ruleJSON {
	name := ""
	if r.ChannelID != 0 {
		if ch, err := s.deps.Store.ChannelByID(r.ChannelID); err == nil {
			name = ch.GuideNumber + " " + ch.Name
		}
	}
	return ruleJSON{ID: r.ID, Title: r.Title, SeriesID: r.SeriesID, ChannelID: r.ChannelID, ChannelName: name,
		NewOnly: r.NewOnly, KeepLatest: r.KeepLatest, ScheduledBy: names[r.UserID],
		Editable: true, CanManage: claims.Role == "admin" || claims.UserID == r.UserID, CreatedAt: r.CreatedAt.UTC()}
}

// handleListRules serves GET /api/v1/recording-rules.
func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	rules, err := s.deps.Store.ListRules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list rules")
		return
	}
	names := s.userNames()
	out := make([]ruleJSON, 0, len(rules))
	for _, rule := range rules {
		out = append(out, s.ruleToJSON(rule, claims, names))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateRule serves POST /api/v1/recording-rules: "record this show"
// from a guide program. Default: this channel, new episodes only.
func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	if !s.dvrReady(w) {
		return
	}
	claims, _ := auth.ClaimsFrom(r.Context())
	var req struct {
		ChannelID    int64     `json:"channelId"`
		ProgramStart time.Time `json:"programStart"`
		AnyChannel   bool      `json:"anyChannel"`
		NewOnly      *bool     `json:"newOnly"`
		KeepLatest   int       `json:"keepLatest"`
	}
	if err := decodeJSON(r, &req); err != nil || req.KeepLatest < 0 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ch, err := s.deps.Store.ChannelByID(req.ChannelID)
	if err != nil || (!ch.Enabled && claims.Role != "admin") {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	p, ok := s.findProgram(r, ch.ID, req.ProgramStart.UTC())
	if !ok {
		writeError(w, http.StatusNotFound, "program not found in the guide")
		return
	}
	u, err := s.deps.Store.UserByID(claims.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if pol := policyFor(u); !pol.ChannelAllowed(ch.ID) {
		writeParentalBlock(w, "Blocked by parental controls (this channel isn't allowed)")
		return
	} else if !pol.ProgramAllowed(p.Rating) {
		writeParentalBlock(w, pol.Reason(p.Rating))
		return
	}
	rule := store.RecordingRule{UserID: claims.UserID, Title: p.Title, SeriesID: p.SeriesID,
		NewOnly: true, KeepLatest: req.KeepLatest, CreatedAt: time.Now().UTC()}
	if req.NewOnly != nil {
		rule.NewOnly = *req.NewOnly
	}
	if !req.AnyChannel {
		rule.ChannelID = ch.ID
	}
	id, err := s.deps.Store.CreateRule(rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create rule")
		return
	}
	rule.ID = id
	n := s.deps.DVR.ApplyRules()
	writeJSON(w, http.StatusCreated, map[string]any{
		"rule":      s.ruleToJSON(rule, claims, s.userNames()),
		"scheduled": n,
	})
}

// handleDeleteRule serves DELETE /api/v1/recording-rules/{id}: removes the
// rule and cancels its upcoming recordings (recorded ones stay).
func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	rule, err := s.deps.Store.RuleByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if claims.Role != "admin" && claims.UserID != rule.UserID {
		writeError(w, http.StatusForbidden, "only the person who set it up (or an admin) can remove this rule")
		return
	}
	if err := s.deps.Store.DeleteRule(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete rule")
		return
	}
	if s.deps.DVR != nil {
		s.deps.DVR.CancelRule(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePatchRule serves PATCH /api/v1/recording-rules/{id}.
func (s *Server) handlePatchRule(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	rule, err := s.deps.Store.RuleByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if claims.Role != "admin" && claims.UserID != rule.UserID {
		writeError(w, http.StatusForbidden, "only the person who set it up (or an admin) can edit this rule")
		return
	}
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil || fields == nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	for key, value := range fields {
		var dst any
		switch key {
		case "newOnly":
			dst = &rule.NewOnly
		case "channelId":
			dst = &rule.ChannelID
		case "keepLatest":
			dst = &rule.KeepLatest
		default:
			writeError(w, http.StatusBadRequest, "unknown rule setting")
			return
		}
		if string(value) == "null" || json.Unmarshal(value, dst) != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	if rule.KeepLatest < 0 || rule.ChannelID < 0 {
		writeError(w, http.StatusBadRequest, "invalid rule settings")
		return
	}
	if _, changed := fields["channelId"]; changed && rule.ChannelID != 0 {
		ch, err := s.deps.Store.ChannelByID(rule.ChannelID)
		if err != nil || (!ch.Enabled && claims.Role != "admin") {
			writeError(w, http.StatusBadRequest, "channel not found")
			return
		}
		u, err := s.deps.Store.UserByID(claims.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		if !policyFor(u).ChannelAllowed(ch.ID) {
			writeParentalBlock(w, "Blocked by parental controls (this channel isn't allowed)")
			return
		}
	}
	if err := s.deps.Store.UpdateRule(rule); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update rule")
		return
	}
	if s.deps.DVR != nil {
		if err := s.deps.DVR.ReconcileRule(id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reconcile rule")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.ruleToJSON(rule, claims, s.userNames()))
}
