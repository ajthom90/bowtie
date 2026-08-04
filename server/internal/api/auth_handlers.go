package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/store"
)

type userJSON struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	MaxQuality string `json:"maxQuality"`
}

type tokenPairJSON struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	User         userJSON `json:"user"`
}

func userToJSON(u store.User) userJSON {
	return userJSON{
		ID:         u.ID,
		Username:   u.Username,
		Role:       u.Role,
		MaxQuality: u.MaxQuality,
	}
}

func (s *Server) issueTokens(w http.ResponseWriter, u store.User, now time.Time) {
	access, err := s.deps.Auth.NewAccessToken(u, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue access token")
		return
	}
	refresh, err := s.deps.Auth.NewRefreshToken(u.ID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue refresh token")
		return
	}
	writeJSON(w, http.StatusOK, tokenPairJSON{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         userToJSON(u),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}

	u, err := s.deps.Store.UserByUsername(req.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.issueTokens(w, u, time.Now().UTC())
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refreshToken required")
		return
	}

	u, newRaw, err := s.deps.Auth.Rotate(req.RefreshToken, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	access, err := s.deps.Auth.NewAccessToken(u, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue access token")
		return
	}
	writeJSON(w, http.StatusOK, tokenPairJSON{
		AccessToken:  access,
		RefreshToken: newRaw,
		User:         userToJSON(u),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refreshToken required")
		return
	}
	// Revoke is idempotent (no error if already gone).
	_ = s.deps.Auth.Revoke(req.RefreshToken)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, userToJSON(u))
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "currentPassword and newPassword required")
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
	ok, err = auth.VerifyPassword(req.CurrentPassword, u.PasswordHash)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	if err := s.deps.Store.UpdatePassword(u.ID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
