package api

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

const streamTokenTTL = 12 * time.Hour

var segmentNameRe = regexp.MustCompile(`^seg\d{5}\.ts$`)

// StreamController is the stream manager surface consumed by HTTP handlers.
type StreamController interface {
	Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	Touch(string) bool
	StopViewer(string)
	Sessions() []stream.SessionInfo
	Terminate(string)
	SessionDirOf(viewerID string) (string, bool)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := s.deps.Store.UserByID(claims.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}

	var req struct {
		ChannelID int64                `json:"channelId"`
		Caps      transcode.ClientCaps `json:"caps"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ChannelID == 0 {
		writeError(w, http.StatusBadRequest, "channelId required")
		return
	}

	h, err := s.deps.Streams.Start(r.Context(), u, req.ChannelID, req.Caps)
	if err != nil {
		s.writeStartError(w, err)
		return
	}

	exp := time.Now().UTC().Add(streamTokenTTL)
	tok := stream.SignStreamToken(s.deps.StreamTokenSecret, h.ViewerID, exp)
	playlistURL := "/api/v1/stream/" + h.ViewerID + "/index.m3u8?token=" + tok
	writeJSON(w, http.StatusOK, map[string]string{
		"viewerId":    h.ViewerID,
		"playlistUrl": playlistURL,
	})
}

func (s *Server) writeStartError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "all tuners in use"):
		sessions := []stream.SessionInfo{}
		if s.deps.Streams != nil {
			sessions = s.deps.Streams.Sessions()
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":    "all tuners in use",
			"sessions": sessions,
		})
	case strings.Contains(msg, "unknown channel"),
		strings.Contains(msg, "is disabled"),
		errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "channel not found")
	case strings.Contains(msg, "negotiate:"):
		writeError(w, http.StatusUnprocessableEntity, msg)
	default:
		writeError(w, http.StatusInternalServerError, "failed to start session")
	}
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	if err := s.verifyStreamAccess(viewerID, r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	if !s.deps.Streams.Touch(viewerID) {
		writeError(w, http.StatusNotFound, "viewer not found")
		return
	}
	dir, ok := s.deps.Streams.SessionDirOf(viewerID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, "live.m3u8"))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "playlist not ready")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read playlist")
		return
	}

	token := r.URL.Query().Get("token")
	rewritten := rewritePlaylist(string(raw), viewerID, token)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rewritten))
}

// rewritePlaylist rewrites bare seg*.ts lines to absolute stream URLs with the token.
func rewritePlaylist(body, viewerID, token string) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(body))
	// Allow long lines (M3U tags can be lengthy).
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if segmentNameRe.MatchString(trimmed) {
			b.WriteString("/api/v1/stream/")
			b.WriteString(viewerID)
			b.WriteByte('/')
			b.WriteString(trimmed)
			b.WriteString("?token=")
			b.WriteString(token)
			continue
		}
		b.WriteString(line)
	}
	// Always end with newline for well-formed M3U.
	out := b.String()
	if len(out) > 0 && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func (s *Server) handleSegment(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	segment := r.PathValue("segment")
	if !segmentNameRe.MatchString(segment) {
		writeError(w, http.StatusBadRequest, "invalid segment name")
		return
	}
	if err := s.verifyStreamAccess(viewerID, r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	dir, ok := s.deps.Streams.SessionDirOf(viewerID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	// segment is validated against ^seg\d{5}\.ts$ — no path traversal possible.
	path := filepath.Join(dir, segment)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "segment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to open segment")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, segment, time.Time{}, f)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	if !s.authorizeSessionDelete(viewerID, r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.deps.Streams != nil {
		s.deps.Streams.StopViewer(viewerID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorizeSessionDelete accepts a valid Bearer JWT or a stream token matching viewerID.
func (s *Server) authorizeSessionDelete(viewerID string, r *http.Request) bool {
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		raw := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if raw != "" && s.deps.Auth != nil {
			if _, err := s.deps.Auth.ParseAccessToken(raw, time.Now().UTC()); err == nil {
				return true
			}
		}
	}
	if err := s.verifyStreamAccess(viewerID, r); err == nil {
		return true
	}
	return false
}

func (s *Server) verifyStreamAccess(viewerID string, r *http.Request) error {
	token := r.URL.Query().Get("token")
	if token == "" {
		return errors.New("missing stream token")
	}
	vid, err := stream.VerifyStreamToken(s.deps.StreamTokenSecret, token, time.Now().UTC())
	if err != nil {
		return errors.New("invalid stream token")
	}
	if vid != viewerID {
		return errors.New("token viewer mismatch")
	}
	return nil
}

func (s *Server) handleAdminListSessions(w http.ResponseWriter, r *http.Request) {
	if s.deps.Streams == nil {
		writeJSON(w, http.StatusOK, []stream.SessionInfo{})
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Streams.Sessions())
}

func (s *Server) handleAdminTerminateSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")
	if s.deps.Streams != nil {
		s.deps.Streams.Terminate(sessionID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminTranscode(w http.ResponseWriter, r *http.Request) {
	var caps transcode.Capabilities
	if s.deps.Probe != nil {
		caps = s.deps.Probe()
	}
	if caps.HEVC == nil {
		caps.HEVC = map[transcode.Backend]bool{}
	}

	available := make([]string, 0, len(caps.Available))
	for _, b := range caps.Available {
		available = append(available, string(b))
	}
	hevc := make(map[string]bool, len(caps.HEVC))
	for b, ok := range caps.HEVC {
		hevc[string(b)] = ok
	}

	selected := ""
	forced := s.deps.Cfg.Encoder
	if forced == "" {
		forced = "auto"
	}
	if sel, err := caps.Select(forced); err == nil {
		selected = string(sel)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"available":     available,
		"hevc":          hevc,
		"ffmpegVersion": caps.FFmpegVersion,
		"selected":      selected,
	})
}
