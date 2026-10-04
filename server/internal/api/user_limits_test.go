package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

type limitsJSON struct {
	Username   string `json:"username"`
	MaxStreams int    `json:"maxStreams"`
	MaxTuners  int    `json:"maxTuners"`
}

func TestAdminUserLimitsCreatePatch(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "admin", "adminpass", "admin")
	tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "admin", "password": "adminpass",
	}, nil))
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}

	rr := doJSON(t, h, "POST", "/api/v1/admin/users", map[string]any{
		"username": "friend", "password": "pw", "role": "viewer",
		"maxStreams": 2, "maxTuners": 1,
	}, authH)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
		limitsJSON
	}
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil || created.MaxStreams != 2 || created.MaxTuners != 1 {
		t.Fatalf("created %+v err=%v", created, err)
	}

	path := "/api/v1/admin/users/" + strconv.FormatInt(created.ID, 10)
	rr = doJSON(t, h, "PATCH", path, map[string]any{"maxTuners": 3}, authH)
	var patched limitsJSON
	if rr.Code != http.StatusOK || json.NewDecoder(rr.Body).Decode(&patched) != nil || patched.MaxTuners != 3 || patched.MaxStreams != 2 {
		t.Fatalf("patch %d %+v", rr.Code, patched)
	}

	for _, bad := range []map[string]any{{"maxTuners": 9}, {"maxStreams": -1}} {
		if rr := doJSON(t, h, "PATCH", path, bad, authH); rr.Code != http.StatusBadRequest {
			t.Fatalf("patch %v: %d, want 400", bad, rr.Code)
		}
	}
	if rr := doJSON(t, h, "POST", "/api/v1/admin/users", map[string]any{
		"username": "x", "password": "pw", "role": "viewer", "maxStreams": 9,
	}, authH); rr.Code != http.StatusBadRequest {
		t.Fatalf("create maxStreams 9: %d, want 400", rr.Code)
	}

	// /me reports the account's own limits.
	ftok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "friend", "password": "pw",
	}, nil))
	rr = doJSON(t, h, "GET", "/api/v1/me", nil, map[string]string{"Authorization": "Bearer " + ftok.AccessToken})
	var me limitsJSON
	if rr.Code != http.StatusOK || json.NewDecoder(rr.Body).Decode(&me) != nil || me.MaxStreams != 2 || me.MaxTuners != 3 {
		t.Fatalf("me %d %+v", rr.Code, me)
	}
}

func TestCreateSessionUserLimit429(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "pass",
	}, nil))
	ss.startFn = func(context.Context, store.User, int64, transcode.ClientCaps) (stream.ViewerHandle, error) {
		return stream.ViewerHandle{}, &stream.UserLimitError{Kind: "tuners", Limit: 1}
	}
	rr := doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{
		"channelId": 1,
		"caps":      map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}},
	}, map[string]string{"Authorization": "Bearer " + tok.AccessToken})
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Kind  string `json:"kind"`
		Limit int    `json:"limit"`
	}
	if rr.Code != http.StatusTooManyRequests || json.NewDecoder(rr.Body).Decode(&body) != nil {
		t.Fatalf("status %d body %q", rr.Code, rr.Body.String())
	}
	if body.Code != "user_limit" || body.Kind != "tuners" || body.Limit != 1 ||
		body.Error != "Your account can use 1 tuner at a time. Stop another channel first." {
		t.Fatalf("body %+v", body)
	}
}
