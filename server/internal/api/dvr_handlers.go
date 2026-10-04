package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/dvr"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

// vodFileRe allows only the files a recording's HLS VOD contains.
var vodFileRe = regexp.MustCompile(`^(index|v\d{3,4}|aac\d)(\.m3u8|_\d{5}\.ts)$`)

type recordingJSON struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	Subtitle      string    `json:"subtitle"`
	Description   string    `json:"description"`
	Category      string    `json:"category"`
	ChannelID     int64     `json:"channelId"`
	ChannelName   string    `json:"channelName"`
	Start         time.Time `json:"start"`
	Stop          time.Time `json:"stop"`
	State         string    `json:"state"`
	Partial       bool      `json:"partial"`
	Failure       string    `json:"failure"`
	FailureDetail string    `json:"failureDetail"`
	DurationSec   int       `json:"durationSec"`
	SizeBytes     int64     `json:"sizeBytes"`
	Protected     bool      `json:"protected"`
	PositionSec   int       `json:"positionSec"`
	// PositionUpdatedAt: when the caller last saved a position (omitted if never).
	PositionUpdatedAt *time.Time `json:"positionUpdatedAt,omitempty"`
	ScheduledBy       string     `json:"scheduledBy"`
	// CanManage: the caller may stop, delete or protect it (owner or admin).
	CanManage bool   `json:"canManage"`
	Rating    string `json:"rating"`
	RuleID    int64  `json:"ruleId"` // series rule that scheduled it (0 = one-off)
	// Locked: parental controls block it for the caller (no description, no play).
	Locked bool `json:"locked"`
	// Commercials: breaks found by commercial detection (seconds on the
	// playback timeline, sorted); omitted when none were found or it hasn't run.
	Commercials []store.Commercial `json:"commercials,omitempty"`
}

// recordingToJSON renders r for the caller; pos is the caller's saved
// position for it (zero value when none).
func recordingToJSON(r store.Recording, claims auth.Claims, names map[int64]string, pos store.PlaybackPosition) recordingJSON {
	j := recordingJSON{
		ID: r.ID, Title: r.Title, Subtitle: r.Subtitle, Description: r.Description, Category: r.Category,
		ChannelID: r.ChannelID, ChannelName: r.ChannelName, Start: r.Start.UTC(), Stop: r.Stop.UTC(),
		State: r.State, Partial: r.Partial, Failure: r.Failure, FailureDetail: r.FailureDetail,
		DurationSec: r.DurationSec, SizeBytes: r.SizeBytes, Protected: r.Protected, PositionSec: pos.Sec,
		ScheduledBy: names[r.UserID], CanManage: claims.Role == "admin" || claims.UserID == r.UserID,
		Rating: r.Rating, RuleID: r.RuleID, Commercials: r.Commercials,
	}
	if !pos.UpdatedAt.IsZero() {
		at := pos.UpdatedAt.UTC()
		j.PositionUpdatedAt = &at
	}
	return j
}

// oneRecordingJSON renders a single recording (one position lookup).
func (s *Server) oneRecordingJSON(r store.Recording, claims auth.Claims, names map[int64]string) recordingJSON {
	pos, _ := s.deps.Store.RecordingPositionInfo(r.ID, claims.UserID)
	return recordingToJSON(r, claims, names, pos)
}

// positionsFor loads all of the caller's saved positions in one query, for
// lists (empty on error: positions are a nicety, not a reason to fail a list).
func (s *Server) positionsFor(userID int64) map[int64]store.PlaybackPosition {
	m, err := s.deps.Store.RecordingPositions(userID)
	if err != nil {
		return map[int64]store.PlaybackPosition{}
	}
	return m
}

func (s *Server) userNames() map[int64]string {
	out := map[int64]string{}
	if users, err := s.deps.Store.ListUsers(); err == nil {
		for _, u := range users {
			out[u.ID] = u.Username
		}
	}
	return out
}

func (s *Server) dvrReady(w http.ResponseWriter) bool {
	if s.deps.DVR == nil {
		writeError(w, http.StatusServiceUnavailable, "recording is not available")
		return false
	}
	return true
}

// handleListRecordings serves GET /api/v1/recordings?state=upcoming|recorded|failed.
func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	var states []string
	newestFirst := false
	switch r.URL.Query().Get("state") {
	case "":
	case "upcoming":
		states = []string{store.RecScheduled, store.RecWaiting, store.RecRecording}
	case "recorded":
		states, newestFirst = []string{store.RecConverting, store.RecReady}, true
	case "failed":
		states, newestFirst = []string{store.RecFailed}, true
	default:
		writeError(w, http.StatusBadRequest, "state must be upcoming, recorded or failed")
		return
	}
	rows, err := s.deps.Store.ListRecordings(states...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list recordings")
		return
	}
	if newestFirst {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Start.After(rows[j].Start) })
	}
	names := s.userNames()
	positions := s.positionsFor(claims.UserID)
	policy := s.callerPolicy(r)
	out := make([]recordingJSON, 0, len(rows))
	for _, rec := range rows {
		if !policy.ChannelAllowed(rec.ChannelID) {
			continue
		}
		j := recordingToJSON(rec, claims, names, positions[rec.ID])
		if !policy.ProgramAllowed(rec.Rating) {
			j.Locked, j.Description = true, ""
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateRecording serves POST /api/v1/recordings: a guide program
// (channelId + programStart) or a manual window (channelId + start + stop).
func (s *Server) handleCreateRecording(w http.ResponseWriter, r *http.Request) {
	if !s.dvrReady(w) {
		return
	}
	claims, _ := auth.ClaimsFrom(r.Context())
	var req struct {
		ChannelID    int64      `json:"channelId"`
		ProgramStart *time.Time `json:"programStart"`
		Start        *time.Time `json:"start"`
		Stop         *time.Time `json:"stop"`
		Title        string     `json:"title"`
		Force        bool       `json:"force"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ch, err := s.deps.Store.ChannelByID(req.ChannelID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !ch.Enabled && claims.Role != "admin") {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	sr := dvr.ScheduleRequest{UserID: claims.UserID, Channel: ch, Title: req.Title, Force: req.Force}
	switch {
	case req.ProgramStart != nil:
		p, ok := s.findProgram(r, ch.ID, req.ProgramStart.UTC())
		if !ok {
			writeError(w, http.StatusNotFound, "program not found in the guide")
			return
		}
		sr.Title, sr.Subtitle, sr.Description, sr.Category, sr.Rating = p.Title, p.Subtitle, p.Description, p.Category, p.Rating
		sr.Start, sr.Stop = p.Start, p.Stop
	case req.Start != nil && req.Stop != nil:
		sr.Start, sr.Stop = req.Start.UTC(), req.Stop.UTC()
		// A manual window holds whatever airs in it: keep the strictest rating.
		sr.Rating = s.windowRating(r, ch.ID, sr.Start, sr.Stop)
	default:
		writeError(w, http.StatusBadRequest, "send programStart, or start and stop")
		return
	}
	u, err := s.deps.Store.UserByID(claims.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if p := policyFor(u); !p.ChannelAllowed(ch.ID) {
		writeParentalBlock(w, "Blocked by parental controls (this channel isn't allowed)")
		return
	} else if !p.ProgramAllowed(sr.Rating) {
		writeParentalBlock(w, p.Reason(sr.Rating))
		return
	}

	rec, warnings, err := s.deps.DVR.Schedule(sr)
	var conflict *dvr.ConflictError
	switch {
	case errors.Is(err, dvr.ErrBadWindow):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.As(err, &conflict):
		names := s.userNames()
		positions := s.positionsFor(claims.UserID)
		list := make([]recordingJSON, 0, len(conflict.Conflicts))
		pol := policyFor(u)
		for _, c := range conflict.Conflicts {
			j := recordingToJSON(c, claims, names, positions[c.ID])
			if !pol.ChannelAllowed(c.ChannelID) || !pol.ProgramAllowed(c.Rating) {
				j.Title, j.Subtitle, j.Description, j.Locked = "Another recording", "", "", true
			}
			list = append(list, j)
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      fmt.Sprintf("Only %d tuners: other recordings already need them then. Record anyway to try if one frees up.", conflict.TunerCount),
			"tunerCount": conflict.TunerCount,
			"conflicts":  list,
		})
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to schedule recording")
		return
	}
	if warnings == nil {
		warnings = []dvr.Warning{}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"recording": s.oneRecordingJSON(rec, claims, s.userNames()),
		"warnings":  warnings,
	})
}

// findProgram looks up the guide program on channelID starting at start.
func (s *Server) findProgram(r *http.Request, channelID int64, start time.Time) (epg.GuideProgram, bool) {
	if s.deps.EPG == nil {
		return epg.GuideProgram{}, false
	}
	guide, err := s.deps.EPG.Guide(r.Context(), start, start.Add(time.Minute))
	if err != nil {
		return epg.GuideProgram{}, false
	}
	for _, g := range guide {
		if g.ChannelID != channelID {
			continue
		}
		for _, p := range g.Programs {
			if p.Start.Equal(start) {
				return p, true
			}
		}
	}
	return epg.GuideProgram{}, false
}

// markRecordings sets GuideProgram.Recording for scheduled/in-progress/ready
// recordings that match a program's channel and start.
func (s *Server) markRecordings(guide []epg.GuideChannel) {
	recs := s.recordingsByProgram()
	if len(recs) == 0 {
		return
	}
	for i := range guide {
		for j := range guide[i].Programs {
			p := &guide[i].Programs[j]
			p.Recording = recs[programKey{guide[i].ChannelID, p.Start.Unix()}]
		}
	}
}

// recordingForManage loads a recording the caller may change (owner/admin).
func (s *Server) recordingForManage(w http.ResponseWriter, r *http.Request) (store.Recording, bool) {
	claims, _ := auth.ClaimsFrom(r.Context())
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid recording id")
		return store.Recording{}, false
	}
	rec, err := s.deps.Store.RecordingByID(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "recording not found")
		return store.Recording{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return store.Recording{}, false
	}
	if claims.Role != "admin" && claims.UserID != rec.UserID {
		writeError(w, http.StatusForbidden, "only the person who scheduled it (or an admin) can change this recording")
		return store.Recording{}, false
	}
	return rec, true
}

// handleDeleteRecording serves DELETE /api/v1/recordings/{id}.
func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	if !s.dvrReady(w) {
		return
	}
	rec, ok := s.recordingForManage(w, r)
	if !ok {
		return
	}
	if err := s.deps.DVR.Delete(rec.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete recording")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStopRecording serves POST /api/v1/recordings/{id}/stop.
func (s *Server) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	if !s.dvrReady(w) {
		return
	}
	rec, ok := s.recordingForManage(w, r)
	if !ok {
		return
	}
	if err := s.deps.DVR.StopNow(rec.ID); err != nil {
		if errors.Is(err, dvr.ErrNotStoppable) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to stop recording")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRedetectCommercials serves POST
// /api/v1/recordings/{id}/commercials/detect (admin): run commercial
// detection on a ready recording again.
func (s *Server) handleRedetectCommercials(w http.ResponseWriter, r *http.Request) {
	if !s.dvrReady(w) {
		return
	}
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid recording id")
		return
	}
	switch err := s.deps.DVR.Redetect(id); {
	case err == nil:
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "recording not found")
	case errors.Is(err, dvr.ErrDetectionOff), errors.Is(err, dvr.ErrNotReady):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "failed to start detection")
	}
}

// handlePatchRecording serves PATCH /api/v1/recordings/{id} ({protected}).
func (s *Server) handlePatchRecording(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.recordingForManage(w, r)
	if !ok {
		return
	}
	var req struct {
		Protected *bool `json:"protected"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Protected != nil {
		if err := s.deps.Store.SetRecordingProtected(rec.ID, *req.Protected); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update recording")
			return
		}
		rec.Protected = *req.Protected
	}
	claims, _ := auth.ClaimsFrom(r.Context())
	writeJSON(w, http.StatusOK, s.oneRecordingJSON(rec, claims, s.userNames()))
}

func recTokenSubject(id int64) string { return fmt.Sprintf("rec-%d", id) }

// handlePlayRecording serves POST /api/v1/recordings/{id}/play.
func (s *Server) handlePlayRecording(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid recording id")
		return
	}
	rec, err := s.deps.Store.RecordingByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "recording not found")
		return
	}
	if rec.State != store.RecReady {
		writeError(w, http.StatusConflict, "this recording isn't ready to play yet")
		return
	}
	u, err := s.deps.Store.UserByID(claims.UserID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	if p := policyFor(u); !p.ChannelAllowed(rec.ChannelID) {
		writeParentalBlock(w, "Blocked by parental controls (this channel isn't allowed)")
		return
	} else if !p.ProgramAllowed(rec.Rating) {
		writeParentalBlock(w, p.Reason(rec.Rating))
		return
	}
	tok := stream.SignStreamToken(s.deps.StreamTokenSecret, recTokenSubject(id), time.Now().UTC().Add(streamTokenTTL))
	pos, _ := s.deps.Store.RecordingPosition(id, claims.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"playlistUrl": fmt.Sprintf("/api/v1/recordings/%d/hls/%s?token=%s", id, dvr.MasterName, tok),
		"positionSec": pos,
		"durationSec": rec.DurationSec,
	})
}

// handleRecordingPosition serves PUT /api/v1/recordings/{id}/position.
func (s *Server) handleRecordingPosition(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid recording id")
		return
	}
	var req struct {
		PositionSec int `json:"positionSec"`
	}
	if err := decodeJSON(r, &req); err != nil || req.PositionSec < 0 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := s.deps.Store.RecordingByID(id); err != nil {
		writeError(w, http.StatusNotFound, "recording not found")
		return
	}
	if err := s.deps.Store.SetRecordingPosition(id, claims.UserID, req.PositionSec); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save position")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRecordingFile serves GET /api/v1/recordings/{id}/hls/{file}?token=…
// (playlists get the token appended to every URI).
func (s *Server) handleRecordingFile(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid recording id")
		return
	}
	name := r.PathValue("file")
	if !vodFileRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	tok := r.URL.Query().Get("token")
	sub, err := stream.VerifyStreamToken(s.deps.StreamTokenSecret, tok, time.Now().UTC())
	if err != nil || sub != recTokenSubject(id) {
		writeError(w, http.StatusForbidden, "invalid token")
		return
	}
	rec, err := s.deps.Store.RecordingByID(id)
	if err != nil || rec.State != store.RecReady {
		writeError(w, http.StatusNotFound, "recording not found")
		return
	}
	path := filepath.Join(dvr.PlaylistDir(rec), name)
	if strings.HasSuffix(name, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeFile(w, r, path)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(withToken(string(raw), tok)))
}

var uriAttrRe = regexp.MustCompile(`URI="([^"]+)"`)

// withToken appends ?token= to every URI line and URI="…" attribute.
func withToken(pl, tok string) string {
	q := "?token=" + tok
	lines := strings.Split(pl, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
			lines[i] = uriAttrRe.ReplaceAllString(l, `URI="${1}`+q+`"`)
		default:
			lines[i] = t + q
		}
	}
	return strings.Join(lines, "\n")
}
