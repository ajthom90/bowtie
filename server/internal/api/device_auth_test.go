package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDeviceSignIn(t *testing.T) {
	h, st, _ := testAPI(t)
	seedUser(t, st, "alice", "pw", "viewer")

	rr := doJSON(t, h, "POST", "/api/v1/auth/device", map[string]string{"deviceName": "Living room Apple TV"}, nil)
	var start struct {
		DeviceCode string `json:"deviceCode"`
		UserCode   string `json:"userCode"`
		VerifyURL  string `json:"verifyUrl"`
		ExpiresIn  int    `json:"expiresIn"`
		Interval   int    `json:"interval"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &start) != nil || len(start.UserCode) != 9 ||
		start.DeviceCode == "" || !strings.HasSuffix(start.VerifyURL, "/link?code="+strings.ReplaceAll(start.UserCode, "-", "")) || start.ExpiresIn != 600 {
		t.Fatalf("start %d %s", rr.Code, rr.Body.String())
	}
	poll := func() (int, string) {
		rr := doJSON(t, h, "POST", "/api/v1/auth/device/token", map[string]string{"deviceCode": start.DeviceCode}, nil)
		return rr.Code, rr.Body.String()
	}
	if code, body := poll(); code != http.StatusPreconditionRequired || !strings.Contains(body, "authorization_pending") {
		t.Fatalf("pending %d %s", code, body)
	}

	tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "pw"}, nil))
	auth := map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	// The phone can look the code up first ("Sign in Living room Apple TV?").
	rr = doJSON(t, h, "GET", "/api/v1/auth/device/"+strings.ToLower(strings.ReplaceAll(start.UserCode, "-", "")), nil, auth)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Living room Apple TV") {
		t.Fatalf("lookup %d %s", rr.Code, rr.Body.String())
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/device/approve", map[string]string{"userCode": "ZZZZ-ZZZZ"}, auth); rr.Code != http.StatusNotFound {
		t.Fatalf("bad code %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/device/approve", map[string]string{"userCode": start.UserCode}, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated approve %d", rr.Code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/device/approve", map[string]string{"userCode": start.UserCode}, auth); rr.Code != http.StatusNoContent {
		t.Fatalf("approve %d %s", rr.Code, rr.Body.String())
	}
	code, body := poll()
	var pair struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		User         struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &pair) != nil || pair.AccessToken == "" || pair.User.Username != "alice" {
		t.Fatalf("token %d %s", code, body)
	}
	// One use only.
	if code, _ := poll(); code != http.StatusGone {
		t.Fatalf("second poll %d", code)
	}
	if rr := doJSON(t, h, "POST", "/api/v1/auth/device/token", map[string]string{"deviceCode": "nope"}, nil); rr.Code != http.StatusGone {
		t.Fatalf("unknown device code %d", rr.Code)
	}
}
