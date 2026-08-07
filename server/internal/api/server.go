package api

import (
	"encoding/json"
	"net/http"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
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
	Cfg               config.Config
	Store             *store.Store
	Auth              *auth.Auth
	Tuners            *tuner.Manager // Task 7
	EPG               *epg.Service   // Task 10
	Probe             func() transcode.Capabilities // Task 11
	Streams           StreamController              // Task 15
	StreamTokenSecret []byte                        // Task 15 signed playlist/segment tokens
	// Settings is the DB-backed product settings provider (v0.4.0). Used for
	// admin transcode "selected" and settings API routes.
	Settings *settings.Provider
	// SDBaseURL and SDHTTP optionally override the Schedules Direct client used
	// by GET /admin/epg/lineups (tests inject an httptest fake; production leaves
	// both zero so the client uses the SD default base URL).
	SDBaseURL string
	SDHTTP    *http.Client
}

// Server is the HTTP API surface.
type Server struct {
	deps Deps
}

// New builds the API handler (stdlib ServeMux with Go 1.22 method patterns).
func New(deps Deps) http.Handler {
	s := &Server{deps: deps}
	mux := http.NewServeMux()
	s.mountAPI(mux)
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

	handleFunc("POST /api/v1/auth/login", s.handleLogin)
	handleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	handleFunc("POST /api/v1/auth/logout", s.handleLogout)

	handle("GET /api/v1/me", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleMe)))
	handle("POST /api/v1/me/password", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleChangePassword)))

	// Viewer channel list (enabled only).
	handle("GET /api/v1/channels", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleListChannels)))

	// Viewer guide (Task 10).
	handle("GET /api/v1/guide", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleGuide)))

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
