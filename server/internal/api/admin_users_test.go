package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestAdminListCreateUsers(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	// Viewer cannot list users.
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	viewerTok := decodeLogin(t, rr)
	rr = doJSON(t, h, "GET", "/api/v1/admin/users", nil, map[string]string{
		"Authorization": "Bearer " + viewerTok.AccessToken,
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer list status = %d, want 403, body=%q", rr.Code, rr.Body.String())
	}

	// Admin can list users.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)
	rr = doJSON(t, h, "GET", "/api/v1/admin/users", nil, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin list status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var users []struct {
		ID         int64  `json:"id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		MaxQuality string `json:"maxQuality"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&users); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("list len = %d, want 2", len(users))
	}

	// Admin creates a user.
	rr = doJSON(t, h, "POST", "/api/v1/admin/users", map[string]string{
		"username":   "bob",
		"password":   "bobpass",
		"role":       "viewer",
		"maxQuality": "medium",
	}, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusCreated && rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var created struct {
		ID         int64  `json:"id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		MaxQuality string `json:"maxQuality"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == 0 || created.Username != "bob" || created.Role != "viewer" || created.MaxQuality != "medium" {
		t.Fatalf("created = %+v", created)
	}

	// New user can login.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "bob",
		"password": "bobpass",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob login status = %d, body=%q", rr.Code, rr.Body.String())
	}
}

func TestAdminPatchUser(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "admin", "adminpass", "admin")
	viewer := seedUser(t, st, "viewer", "viewerpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)

	path := "/api/v1/admin/users/" + strconv.FormatInt(viewer.ID, 10)
	rr = doJSON(t, h, "PATCH", path, map[string]any{
		"role":       "admin",
		"maxQuality": "low",
		"password":   "newpass",
	}, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%q", rr.Code, rr.Body.String())
	}
	var updated struct {
		ID         int64  `json:"id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		MaxQuality string `json:"maxQuality"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if updated.Role != "admin" || updated.MaxQuality != "low" {
		t.Fatalf("updated = %+v", updated)
	}

	// Password was changed.
	rr = doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "newpass",
	}, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("login with new password: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAdminDeleteLastAdmin409(t *testing.T) {
	h, st, _ := testAPI(t)
	admin := seedUser(t, st, "admin", "adminpass", "admin")
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin",
		"password": "adminpass",
	}, nil)
	adminTok := decodeLogin(t, rr)

	// Cannot delete the last admin.
	path := "/api/v1/admin/users/" + strconv.FormatInt(admin.ID, 10)
	rr = doJSON(t, h, "DELETE", path, nil, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete last admin status = %d, want 409, body=%q", rr.Code, rr.Body.String())
	}

	// Promote viewer to admin, then deleting original admin succeeds.
	viewerList := doJSON(t, h, "GET", "/api/v1/admin/users", nil, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	var users []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(viewerList.Body).Decode(&users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	var viewerID int64
	for _, u := range users {
		if u.Username == "viewer" {
			viewerID = u.ID
		}
	}
	if viewerID == 0 {
		t.Fatal("viewer not found")
	}
	rr = doJSON(t, h, "PATCH", "/api/v1/admin/users/"+strconv.FormatInt(viewerID, 10), map[string]any{
		"role": "admin",
	}, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("promote viewer: %d %s", rr.Code, rr.Body.String())
	}

	rr = doJSON(t, h, "DELETE", path, nil, map[string]string{
		"Authorization": "Bearer " + adminTok.AccessToken,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete admin status = %d, want 204, body=%q", rr.Code, rr.Body.String())
	}
}

func TestAdminUsersForbiddenForViewer(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "viewer", "viewerpass", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "viewer",
		"password": "viewerpass",
	}, nil)
	tok := decodeLogin(t, rr)
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	checks := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/admin/users", nil},
		{"POST", "/api/v1/admin/users", map[string]string{"username": "x", "password": "y", "role": "viewer", "maxQuality": ""}},
		{"PATCH", "/api/v1/admin/users/1", map[string]any{"role": "admin"}},
		{"DELETE", "/api/v1/admin/users/1", nil},
	}
	for _, c := range checks {
		rr := doJSON(t, h, c.method, c.path, c.body, authH)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403, body=%q", c.method, c.path, rr.Code, rr.Body.String())
		}
	}
}
