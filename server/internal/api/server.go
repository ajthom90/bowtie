package api

import (
	"encoding/json"
	"net/http"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// Deps holds dependencies for the HTTP API.
// Later tasks add fields when their packages exist (Tuners, EPG, Probe, Streams).
type Deps struct {
	Cfg   config.Config
	Store *store.Store
	Auth  *auth.Auth
}

// Server is the HTTP API surface.
type Server struct {
	deps Deps
}

// New builds the API handler (stdlib ServeMux with Go 1.22 method patterns).
func New(deps Deps) http.Handler {
	s := &Server{deps: deps}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)

	mux.Handle("GET /api/v1/me", auth.RequireUser(deps.Auth)(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/v1/me/password", auth.RequireUser(deps.Auth)(http.HandlerFunc(s.handleChangePassword)))

	// Admin user management (Task 5).
	admin := auth.RequireAdmin(deps.Auth)
	mux.Handle("GET /api/v1/admin/users", admin(http.HandlerFunc(s.handleAdminListUsers)))
	mux.Handle("POST /api/v1/admin/users", admin(http.HandlerFunc(s.handleAdminCreateUser)))
	mux.Handle("PATCH /api/v1/admin/users/{id}", admin(http.HandlerFunc(s.handleAdminPatchUser)))
	mux.Handle("DELETE /api/v1/admin/users/{id}", admin(http.HandlerFunc(s.handleAdminDeleteUser)))

	return mux
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
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}
