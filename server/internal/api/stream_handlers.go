package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
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

// Names FFmpeg writes for a session layout (transcode.Layout): rung, AAC and
// AC-3 rendition segments; per-rung WebVTT segments; rendition playlists.
var (
	segmentNameRe   = regexp.MustCompile(`^(v\d{3,4}|aac[0-2]|ac3[0-2])_\d{5}\.ts$`)
	captionNameRe   = regexp.MustCompile(`^v\d{3,4}\d+\.vtt$`)
	renditionNameRe = regexp.MustCompile(`^(v\d{3,4}|v\d{3,4}_vtt|aac[0-2]|ac3[0-2])\.m3u8$`)
)

// vttTimestampMap aligns FFmpeg's WebVTT cue times (which start at 0) with the
// video's MPEG-TS clock (the mpegts muxer starts video at 1.4 s).
const vttTimestampMap = "X-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000"

// StreamController is the stream manager surface consumed by HTTP handlers.
type StreamController interface {
	Start(ctx context.Context, user store.User, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	// Join adds a viewer to an existing session (SharePlay); stream.ErrNotJoinable
	// means start normally instead.
	Join(ctx context.Context, user store.User, sessionID string, channelID int64, caps transcode.ClientCaps) (stream.ViewerHandle, error)
	Touch(string) bool
	StopViewer(string)
	Sessions() []stream.SessionInfo
	Terminate(string)
	SessionDirOf(viewerID string) (string, bool)
	SessionInfoOf(viewerID string) (stream.SessionInfo, bool)
	// IngestChannels returns channel IDs with an open device ingest (admin tuners payload).
	IngestChannels() []int64
	// ChannelReception returns a channel's last tune outcome; false if never tuned.
	ChannelReception(channelID int64) (stream.Reception, bool)
	// SessionMediaOf returns the viewer's session layout and quality ceiling.
	SessionMediaOf(viewerID string) (stream.SessionMedia, bool)
	// BlockedReason reports why parental controls stopped a viewer.
	BlockedReason(viewerID string) (string, bool)
}

// viewerGone answers a request for a viewer that no longer exists: 403 with
// the reason if parental controls stopped it, else 404.
func (s *Server) viewerGone(w http.ResponseWriter, viewerID string) {
	if why, ok := s.deps.Streams.BlockedReason(viewerID); ok {
		writeParentalBlock(w, why)
		return
	}
	writeError(w, http.StatusNotFound, "viewer not found")
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
		// JoinSessionID: watch in the same session as a SharePlay sharer.
		JoinSessionID string `json:"joinSessionId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ChannelID == 0 {
		writeError(w, http.StatusBadRequest, "channelId required")
		return
	}

	if why := s.parentalStartBlock(r, u, req.ChannelID); why != "" {
		writeParentalBlock(w, why)
		return
	}

	var h stream.ViewerHandle
	if req.JoinSessionID != "" {
		h, err = s.deps.Streams.Join(r.Context(), u, req.JoinSessionID, req.ChannelID, req.Caps)
		if errors.Is(err, stream.ErrNotJoinable) {
			h, err = s.deps.Streams.Start(r.Context(), u, req.ChannelID, req.Caps)
		}
	} else {
		h, err = s.deps.Streams.Start(r.Context(), u, req.ChannelID, req.Caps)
	}
	if err != nil {
		s.writeStartError(w, err, u)
		return
	}

	exp := time.Now().UTC().Add(streamTokenTTL)
	tok := stream.SignStreamToken(s.deps.StreamTokenSecret, h.ViewerID, exp)
	playlistURL := "/api/v1/stream/" + h.ViewerID + "/index.m3u8?token=" + tok

	// Session overlay fields for the client stats UI (codec/profile/backend/channel).
	resp := map[string]any{
		"viewerId":    h.ViewerID,
		"playlistUrl": playlistURL,
	}
	if info, ok := s.deps.Streams.SessionInfoOf(h.ViewerID); ok {
		resp["session"] = map[string]string{
			"id":          h.SessionID,
			"videoCodec":  info.VideoCodec,
			"profile":     info.Profile,
			"backend":     info.Backend,
			"channelName": info.ChannelName,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeStartError maps stream.Start failures to HTTP. user is required for
// role-based 503 filtering: non-admins only see sessions on enabled channels
// (admin preview of disabled channels must not leak unlisted names to viewers).
func (s *Server) writeStartError(w http.ResponseWriter, err error, user store.User) {
	msg := err.Error()
	var limitErr *stream.UserLimitError
	switch {
	case errors.As(err, &limitErr):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": limitErr.Error(),
			"code":  "user_limit",
			"kind":  limitErr.Kind,
			"limit": limitErr.Limit,
		})
	case errors.Is(err, stream.ErrTunersBusy):
		sessions := []stream.SessionInfo{}
		if s.deps.Streams != nil {
			sessions = s.deps.Streams.Sessions()
		}
		if user.Role != "admin" {
			sessions = filterSessionsEnabledOnly(sessions, s.enabledChannelIDs())
			// Parental controls: don't reveal channels the account can't see.
			p := policyFor(user)
			kept := sessions[:0]
			for _, se := range sessions {
				if p.ChannelAllowed(se.ChannelID) {
					kept = append(kept, se)
				}
			}
			sessions = kept
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":      "all tuners in use",
			"sessions":   sessions,
			"otherInUse": s.otherTunersInUse(),
		})
	case strings.Contains(msg, "unknown channel"),
		strings.Contains(msg, "is disabled"),
		errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, msgChannelGone)
	case strings.Contains(msg, "negotiate:"):
		writeError(w, http.StatusUnprocessableEntity, startErrorMessage(msgCantPlayHere, err, user))
	case errors.Is(err, stream.ErrNoSignal):
		log.Printf("session start: user=%s: %v", user.Username, err)
		writeError(w, http.StatusBadGateway, startErrorMessage(msgNoSignal, err, user))
	default:
		log.Printf("session start failed: user=%s: %v", user.Username, err)
		writeError(w, http.StatusInternalServerError, startErrorMessage(msgStartFailed, err, user))
	}
}

// What the apps show when a channel won't start: plain words for everyone
// (the apps display the server's text as-is).
const (
	msgNoSignal     = "This channel isn't coming in right now — your antenna isn't getting a picture from it. Try again later or pick another channel."
	msgStartFailed  = "Something went wrong starting this channel. Try again in a moment."
	msgCantPlayHere = "This channel can't play on this device."
	msgChannelGone  = "This channel isn't available anymore."
)

// startErrorMessage adds the technical cause for admins, who can act on it
// ("… (Details: device returned HTTP 503: 807 No Video Data)"); viewers get
// only the plain message.
func startErrorMessage(msg string, err error, user store.User) string {
	if user.Role != "admin" {
		return msg
	}
	return msg + " (Details: " + err.Error() + ")"
}

// otherTunersInUse counts tuners busy for something other than Bowtie (e.g.
// Plex): tuners the devices report streaming, minus one per channel Bowtie
// itself is streaming. IPs can't tell them apart (Bowtie and Plex often share
// a host), so the count is by difference.
func (s *Server) otherTunersInUse() int {
	if s.deps.Tuners == nil {
		return 0
	}
	busy := 0
	for _, d := range s.deps.Tuners.Devices() {
		for _, t := range d.Tuners {
			if t.TargetIP != "" || t.VctNumber != "" {
				busy++
			}
		}
	}
	if s.deps.Streams != nil {
		busy -= len(s.deps.Streams.IngestChannels())
	}
	if busy < 0 {
		return 0
	}
	return busy
}

// enabledChannelIDs returns the set of currently enabled channel IDs (one store
// lookup). Empty set on store error — safer to hide sessions than leak names.
func (s *Server) enabledChannelIDs() map[int64]struct{} {
	out := make(map[int64]struct{})
	if s.deps.Store == nil {
		return out
	}
	chans, err := s.deps.Store.ListChannels(true)
	if err != nil {
		return out
	}
	for _, c := range chans {
		out[c.ID] = struct{}{}
	}
	return out
}

// filterSessionsEnabledOnly keeps sessions whose ChannelID is in enabledIDs.
func filterSessionsEnabledOnly(sessions []stream.SessionInfo, enabledIDs map[int64]struct{}) []stream.SessionInfo {
	if len(sessions) == 0 {
		return sessions
	}
	out := make([]stream.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		if _, ok := enabledIDs[sess.ChannelID]; ok {
			out = append(out, sess)
		}
	}
	return out
}

// handlePlaylist serves index.m3u8: the viewer's master playlist, rebuilt on
// every fetch (rungs above the viewer's ceiling are left out; captions appear
// once their playlist exists).
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
		s.viewerGone(w, viewerID)
		return
	}
	media, ok := s.deps.Streams.SessionMediaOf(viewerID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if !fileExists(filepath.Join(media.Dir, media.Layout.ReadyPlaylist())) {
		writeError(w, http.StatusNotFound, "playlist not ready")
		return
	}
	captionsReady := media.Layout.Captions && fileExists(filepath.Join(media.Dir, media.Layout.CaptionPlaylist()))
	query := "?token=" + url.QueryEscape(r.URL.Query().Get("token"))
	body := transcode.MasterPlaylist(media.Layout, media.MaxHeight, query, captionsReady)

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// serveMediaPlaylist serves one rendition playlist with token-signed segment
// URLs. Players poll these (not index.m3u8), so each fetch keeps the viewer alive.
func (s *Server) serveMediaPlaylist(w http.ResponseWriter, r *http.Request, viewerID, name string) {
	if !s.deps.Streams.Touch(viewerID) {
		s.viewerGone(w, viewerID)
		return
	}
	dir, ok := s.deps.Streams.SessionDirOf(viewerID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "playlist not ready")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read playlist")
		return
	}
	if rung, ok := strings.CutSuffix(name, "_vtt.m3u8"); ok {
		// FFmpeg 5.1 mangles caption playlists continued after a restart.
		if video, err := os.ReadFile(filepath.Join(dir, rung+".m3u8")); err == nil {
			raw = repairCaptionPlaylist(raw, video, rung)
		}
	}
	rewritten := rewritePlaylist(string(raw), viewerID, r.URL.Query().Get("token"))
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rewritten))
}

// rewritePlaylist rewrites bare segment lines to absolute stream URLs with the token.
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
		if segmentNameRe.MatchString(trimmed) || captionNameRe.MatchString(trimmed) {
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

// withTimestampMap inserts vttTimestampMap after the WEBVTT line unless present.
func withTimestampMap(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("WEBVTT")) || bytes.Contains(b, []byte("X-TIMESTAMP-MAP")) {
		return b
	}
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return append(append(b, '\n'), vttTimestampMap+"\n"...)
	}
	out := make([]byte, 0, len(b)+len(vttTimestampMap)+1)
	out = append(out, b[:i+1]...)
	out = append(out, vttTimestampMap...)
	out = append(out, '\n')
	return append(out, b[i+1:]...)
}

// handleSegment serves a session file: rendition playlist, WebVTT segment or
// TS segment. Names are validated before any filesystem access (no traversal).
func (s *Server) handleSegment(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	name := r.PathValue("segment")
	isPlaylist := renditionNameRe.MatchString(name)
	isCaption := captionNameRe.MatchString(name)
	if !isPlaylist && !isCaption && !segmentNameRe.MatchString(name) {
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
	if isPlaylist {
		s.serveMediaPlaylist(w, r, viewerID, name)
		return
	}
	dir, ok := s.deps.Streams.SessionDirOf(viewerID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	path := filepath.Join(dir, name)
	if isCaption {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "segment not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to read segment")
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(withTimestampMap(raw))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "segment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to open segment")
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, time.Time{}, f)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	if !s.authorizeViewerRequest(viewerID, r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.deps.Streams != nil {
		s.deps.Streams.StopViewer(viewerID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	if !s.authorizeViewerRequest(viewerID, r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	if !s.deps.Streams.Touch(viewerID) {
		s.viewerGone(w, viewerID)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorizeViewerRequest accepts a valid Bearer JWT or a stream token matching viewerID.
// Shared by DELETE session and POST heartbeat (auth failure → 401).
func (s *Server) authorizeViewerRequest(viewerID string, r *http.Request) bool {
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
	forced := s.transcodeEncoderSelected()
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

// transcodeEncoderSelected is the runtime encoder preference used for admin
// probe "selected". Same source of truth as stream.Manager.Start (settings
// provider when wired; otherwise boot-time cfg).
func (s *Server) transcodeEncoderSelected() string {
	if s.deps.Settings != nil {
		if t, err := s.deps.Settings.Transcode(); err == nil && t.Encoder != "" {
			return t.Encoder
		}
	}
	forced := s.deps.Cfg.Encoder
	if forced == "" {
		return "auto"
	}
	return forced
}
