package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
)

func testAPI(t *testing.T) (http.Handler, *store.Store, *auth.Auth) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := &auth.Auth{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		Store:  st,
	}
	h := api.New(api.Deps{
		Cfg:   config.Config{ListenAddr: ":0"},
		Store: st,
		Auth:  a,
	})
	return h, st, a
}

func seedUser(t *testing.T, st *store.Store, username, password, role string) store.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	id, err := st.CreateUser(store.User{
		Username:     username,
		PasswordHash: hash,
		Role:         role,
		MaxQuality:   "",
		CreatedAt:    time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, err := st.UserByID(id)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	return u
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

type loginResp struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	User         struct {
		ID         int64  `json:"id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		MaxQuality string `json:"maxQuality"`
	} `json:"user"`
}

func decodeLogin(t *testing.T, rr *httptest.ResponseRecorder) loginResp {
	t.Helper()
	var out loginResp
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode login: %v body=%q", err, rr.Body.String())
	}
	return out
}

func TestLoginSuccessAndMe(t *testing.T) {
	h, st, _ := testAPI(t)
	u := seedUser(t, st, "alice", "s3cret", "admin")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "s3cret",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%q", rr.Code, rr.Body.String())
	}
	tok := decodeLogin(t, rr)
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", tok)
	}
	if tok.User.ID != u.ID || tok.User.Username != "alice" || tok.User.Role != "admin" {
		t.Fatalf("user = %+v, want id=%d alice admin", tok.User, u.ID)
	}

	rr = doJSON(t, h, "GET", "/api/v1/me", nil, map[string]string{
		"Authorization": "Bearer " + tok.AccessToken,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("me status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var me struct {
		ID         int64  `json:"id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		MaxQuality string `json:"maxQuality"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.ID != u.ID || me.Username != "alice" || me.Role != "admin" {
		t.Fatalf("me = %+v", me)
	}
}

func TestLoginBadPassword401(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "alice", "s3cret", "admin")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "wrong",
	}, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%q", rr.Code, rr.Body.String())
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
}

func TestRefreshRotation(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "alice", "s3cret", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "s3cret",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	first := decodeLogin(t, rr)

	rr = doJSON(t, h, "POST", "/api/v1/auth/refresh", map[string]string{
		"refreshToken": first.RefreshToken,
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rr.Code, rr.Body.String())
	}
	second := decodeLogin(t, rr)
	if second.AccessToken == "" || second.RefreshToken == "" {
		t.Fatal("refresh missing tokens")
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	// Old refresh token must no longer work.
	rr = doJSON(t, h, "POST", "/api/v1/auth/refresh", map[string]string{
		"refreshToken": first.RefreshToken,
	}, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("old refresh status = %d, want 401", rr.Code)
	}

	// New refresh token works.
	rr = doJSON(t, h, "POST", "/api/v1/auth/refresh", map[string]string{
		"refreshToken": second.RefreshToken,
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("new refresh: %d %s", rr.Code, rr.Body.String())
	}
}

func TestMeRequiresAuth401(t *testing.T) {
	h, _, _ := testAPI(t)

	rr := doJSON(t, h, "GET", "/api/v1/me", nil, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}

	rr = doJSON(t, h, "GET", "/api/v1/me", nil, map[string]string{
		"Authorization": "Bearer not-a-jwt",
	})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", rr.Code)
	}
}

func TestPasswordChange(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "alice", "oldpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "oldpass",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	tok := decodeLogin(t, rr)

	// Wrong current password → 403
	rr = doJSON(t, h, "POST", "/api/v1/me/password", map[string]string{
		"currentPassword": "nope",
		"newPassword":     "newpass",
	}, map[string]string{
		"Authorization": "Bearer " + tok.AccessToken,
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("wrong current status = %d, want 403", rr.Code)
	}

	// Correct current password → 204
	rr = doJSON(t, h, "POST", "/api/v1/me/password", map[string]string{
		"currentPassword": "oldpass",
		"newPassword":     "newpass",
	}, map[string]string{
		"Authorization": "Bearer " + tok.AccessToken,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("password change status = %d, want 204, body=%q", rr.Code, rr.Body.String())
	}

	// Old refresh still valid after password change.
	rr = doJSON(t, h, "POST", "/api/v1/auth/refresh", map[string]string{
		"refreshToken": tok.RefreshToken,
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("old refresh after password change: %d %s", rr.Code, rr.Body.String())
	}

	// Login with new password works.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "newpass",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login with new password: %d %s", rr.Code, rr.Body.String())
	}

	// Login with old password fails.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "oldpass",
	}, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password status = %d, want 401", rr.Code)
	}
}

func TestLogout(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "alice", "s3cret", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "s3cret",
	}, nil)
	tok := decodeLogin(t, rr)

	rr = doJSON(t, h, "POST", "/api/v1/auth/logout", map[string]string{
		"refreshToken": tok.RefreshToken,
	}, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rr.Code)
	}

	rr = doJSON(t, h, "POST", "/api/v1/auth/refresh", map[string]string{
		"refreshToken": tok.RefreshToken,
	}, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status = %d, want 401", rr.Code)
	}
}
