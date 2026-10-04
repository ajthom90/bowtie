package api

import (
	"encoding/json"
	"net/http"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/dvr"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
	"github.com/ajthom90/bowtie/server/internal/web"
)

// Deps holds dependencies for the HTTP API.
// Later tasks add fields when their packages exist.
type Deps struct {
	Cfg    config.Config
	Store  *store.Store
	Auth   *auth.Auth
	Tuners *tuner.Manager                // Task 7
	EPG    *epg.Service                  // Task 10
	Probe  func() transcode.Capabilities // Task 11
	// Version is the release version, served by GET /api/v1/version.
	Version string
	// ServerID (stable, random) and ServerName identify this server to apps
	// (SharePlay: is a participant signed in to the sharer's server?).
	ServerID          string
	ServerName        string
	Streams           StreamController // Task 15
	StreamTokenSecret []byte           // Task 15 signed playlist/segment tokens
	// Settings is the DB-backed product settings provider (v0.4.0). Used for
	// admin transcode "selected" and settings API routes.
	Settings *settings.Provider
	// SDBaseURL and SDHTTP optionally override the Schedules Direct client used
	// by GET /admin/epg/lineups (tests inject an httptest fake; production leaves
	// both zero so the client uses the SD default base URL).
	SDBaseURL string
	SDHTTP    *http.Client
	// DVR schedules and records (nil: recording endpoints answer 503).
	DVR *dvr.Service
}

// Server is the HTTP API surface.
type Server struct {
	deps    Deps
	devices *deviceAuths // quick sign-in (device_auth.go)
	iptv    *iptvViewers // IPTV feed viewers per account (iptv_handlers.go)
}

// New builds the API handler (stdlib ServeMux with Go 1.22 method patterns).
func New(deps Deps) http.Handler {
	s := &Server{deps: deps, devices: newDeviceAuths(), iptv: &iptvViewers{}}
	mux := http.NewServeMux()
	s.mountAPI(mux)
	// Short APK download links for sideloading (docs/install/android.md).
	mux.HandleFunc("GET /android", s.appDownload("android"))
	mux.HandleFunc("GET /tv", s.appDownload("tv"))
	// Embedded SPA (Task 17): catch-all for non-/api paths. More-specific
	// /api/v1/... patterns above take precedence in Go 1.22 ServeMux.
	mux.Handle("/", web.Handler())
	return mux
}

// Routes returns every registered /api/v1 pattern as "METHOD /path" (Go 1.22
// ServeMux form, including {param} placeholders). Built alongside mountAPI so
// OpenAPI coverage tests stay in lockstep with registration.
func Routes() []string {
	return (&Server{}).mountAPI(http.NewServeMux())
}

// mountAPI registers all /api/v1 routes on mux and returns their patterns.
// The SPA catch-all is registered separately in New (not part of the API surface).
func (s *Server) mountAPI(mux *http.ServeMux) []string {
	var routes []string
	handle := func(pattern string, h http.Handler) {
		routes = append(routes, pattern)
		mux.Handle(pattern, h)
	}
	handleFunc := func(pattern string, h http.HandlerFunc) {
		routes = append(routes, pattern)
		mux.HandleFunc(pattern, h)
	}

	handleFunc("GET /api/v1/version", s.handleVersion)
	handleFunc("POST /api/v1/auth/login", s.handleLogin)
	handleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	handleFunc("POST /api/v1/auth/logout", s.handleLogout)
	handleFunc("POST /api/v1/auth/device", s.handleDeviceStart)
	handleFunc("POST /api/v1/auth/device/token", s.handleDeviceToken)
	handleFunc("GET /api/v1/auth/device/qr/{file}", s.handleDeviceQR)
	handle("GET /api/v1/auth/device/{userCode}", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleDeviceLookup)))
	handle("POST /api/v1/auth/device/approve", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleDeviceApprove)))

	handle("GET /api/v1/me", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleMe)))
	handle("POST /api/v1/me/password", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleChangePassword)))
	handle("PUT /api/v1/me/favorites/{channelId}", auth.RequireUser(s.deps.Auth)(s.handleSetFavorite(true)))
	handle("DELETE /api/v1/me/favorites/{channelId}", auth.RequireUser(s.deps.Auth)(s.handleSetFavorite(false)))
	handle("GET /api/v1/recording-rules", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleListRules)))
	handle("POST /api/v1/recording-rules", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleCreateRule)))
	handle("DELETE /api/v1/recording-rules/{id}", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleDeleteRule)))
	handle("GET /api/v1/recordings", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleListRecordings)))
	handle("POST /api/v1/recordings", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleCreateRecording)))
	handle("PATCH /api/v1/recordings/{id}", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handlePatchRecording)))
	handle("DELETE /api/v1/recordings/{id}", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleDeleteRecording)))
	handle("POST /api/v1/recordings/{id}/stop", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleStopRecording)))
	handle("POST /api/v1/recordings/{id}/play", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handlePlayRecording)))
	handle("PUT /api/v1/recordings/{id}/position", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleRecordingPosition)))
	handleFunc("GET /api/v1/recordings/{id}/hls/{file}", s.handleRecordingFile)
	handle("POST /api/v1/me/feed", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleCreateFeed)))
	handle("DELETE /api/v1/me/feed", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleDeleteFeed)))
	handleFunc("GET /api/v1/iptv/{key}/playlist.m3u", s.handleIPTVPlaylist)
	handleFunc("GET /api/v1/iptv/{key}/guide.xml", s.handleIPTVGuide)
	handleFunc("GET /api/v1/iptv/{key}/stream/{channelId}", s.handleIPTVStream)
	handle("GET /api/v1/me/recents", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleRecents)))
	handle("DELETE /api/v1/me/recents", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleClearRecents)))

	// Viewer channel list (enabled only).
	handle("GET /api/v1/channels", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleListChannels)))

	// Viewer guide (Task 10).
	handle("GET /api/v1/guide", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleGuide)))
	handle("GET /api/v1/guide/search", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleGuideSearch)))

	// Admin user management (Task 5).
	admin := auth.RequireAdmin(s.deps.Auth)
	handle("GET /api/v1/admin/users", admin(http.HandlerFunc(s.handleAdminListUsers)))
	handle("POST /api/v1/admin/users", admin(http.HandlerFunc(s.handleAdminCreateUser)))
	handle("PATCH /api/v1/admin/users/{id}", admin(http.HandlerFunc(s.handleAdminPatchUser)))
	handle("DELETE /api/v1/admin/users/{id}", admin(http.HandlerFunc(s.handleAdminDeleteUser)))

	// Admin tuners / devices / channels (Task 7).
	handle("GET /api/v1/admin/tuners", admin(http.HandlerFunc(s.handleAdminListTuners)))
	handle("POST /api/v1/admin/devices", admin(http.HandlerFunc(s.handleAdminAddDevice)))
	handle("DELETE /api/v1/admin/devices/{deviceId}", admin(http.HandlerFunc(s.handleAdminDeleteDevice)))
	handle("POST /api/v1/admin/channels/sync", admin(http.HandlerFunc(s.handleAdminSyncChannels)))
	handle("GET /api/v1/admin/channels", admin(http.HandlerFunc(s.handleAdminListChannels)))
	handle("PATCH /api/v1/admin/channels/{id}", admin(http.HandlerFunc(s.handleAdminPatchChannel)))

	// Admin EPG (Task 10).
	handle("GET /api/v1/admin/epg/status", admin(http.HandlerFunc(s.handleAdminEPGStatus)))
	handle("POST /api/v1/admin/epg/refresh", admin(http.HandlerFunc(s.handleAdminEPGRefresh)))
	handle("GET /api/v1/admin/epg/channels", admin(http.HandlerFunc(s.handleAdminEPGChannels)))
	handle("GET /api/v1/admin/epg/lineups", admin(http.HandlerFunc(s.handleAdminEPGLineups)))

	handle("GET /api/v1/admin/backup", admin(http.HandlerFunc(s.handleAdminBackup)))

	// Admin product settings (v0.4.0 Task 4).
	handle("GET /api/v1/admin/settings", admin(http.HandlerFunc(s.handleAdminGetSettings)))
	handle("PUT /api/v1/admin/settings", admin(http.HandlerFunc(s.handleAdminPutSettings)))

	// Admin transcode probe (Task 11).
	handle("GET /api/v1/admin/transcode", admin(http.HandlerFunc(s.handleAdminTranscode)))

	// Stream sessions (Task 15 / v0.5.0 heartbeat).
	handle("POST /api/v1/sessions", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleCreateSession)))
	handleFunc("GET /api/v1/stream/{viewerId}/index.m3u8", s.handlePlaylist)
	handleFunc("GET /api/v1/stream/{viewerId}/{segment}", s.handleSegment)
	handleFunc("DELETE /api/v1/sessions/{viewerId}", s.handleDeleteSession)
	handleFunc("POST /api/v1/sessions/{viewerId}/heartbeat", s.handleHeartbeat)
	handle("GET /api/v1/admin/sessions", admin(http.HandlerFunc(s.handleAdminListSessions)))
	handle("DELETE /api/v1/admin/sessions/{sessionId}", admin(http.HandlerFunc(s.handleAdminTerminateSession)))

	return routes
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// handleVersion reports the running release so deployments can be checked
// remotely. Public: it reveals nothing the startup log doesn't.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":    s.deps.Version,
		"serverId":   s.deps.ServerID,
		"serverName": s.deps.ServerName,
	})
}
