package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestAdminRouteForbiddenForViewer(t *testing.T) {
	a, s := openTestAuth(t)
	// Middleware validates JWTs with time.Now(); issue tokens at now.
	now := time.Now().UTC()
	createdAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	viewerID, err := s.CreateUser(store.User{
		Username:     "viewer1",
		PasswordHash: "hash",
		Role:         "viewer",
		CreatedAt:    createdAt,
	})
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}
	viewer, err := s.UserByID(viewerID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}

	adminID, err := s.CreateUser(store.User{
		Username:     "admin1",
		PasswordHash: "hash",
		Role:         "admin",
		CreatedAt:    createdAt,
	})
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}
	admin, err := s.UserByID(adminID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}

	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := auth.RequireAdmin(a)(stub)

	// No auth → 401
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/admin", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want 401", rr.Code)
	}

	// Viewer → 403
	viewerTok, err := a.NewAccessToken(viewer, now)
	if err != nil {
		t.Fatalf("NewAccessToken viewer: %v", err)
	}
	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+viewerTok)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403, body=%q", rr.Code, rr.Body.String())
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error == "" {
		t.Fatal("expected error message")
	}

	// Admin → 200
	adminTok, err := a.NewAccessToken(admin, now)
	if err != nil {
		t.Fatalf("NewAccessToken admin: %v", err)
	}
	req = httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200, body=%q", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body = %q", rr.Body.String())
	}
}
