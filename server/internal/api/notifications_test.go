package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/settings"
)

func TestSettingsNotificationsDefaultsAndRoundTrip(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	rr := doJSON(t, h, "GET", "/api/v1/admin/settings", nil, authHeader(tok))
	n := section(decodeSettings(t, rr), "notifications")
	ev, _ := n["events"].(map[string]any)
	if n["url"] != "" || ev["recordingFailed"] != true || ev["diskLow"] != true || ev["guideFailed"] != true || ev["recordingReady"] != false {
		t.Fatalf("defaults = %v", n)
	}

	rr = doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{
			"url":    " https://ntfy.sh/bowtie-test ",
			"events": map[string]any{"recordingReady": true, "diskLow": false},
		},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	n = section(decodeSettings(t, rr), "notifications")
	if n["url"] != "https://ntfy.sh/bowtie-test" {
		t.Fatalf("url = %v (want trimmed)", n["url"])
	}
	got, _ := prov.Notifications()
	want := settings.Notifications{URL: "https://ntfy.sh/bowtie-test", Events: settings.NotificationEvents{
		RecordingFailed: true, DiskLow: false, RecordingReady: true, GuideFailed: true,
	}}
	if got != want {
		t.Fatalf("provider = %+v, want %+v", got, want)
	}

	// Events omitted: kept. Empty URL: off.
	rr = doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{"url": ""},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT clear status = %d", rr.Code)
	}
	want.URL = ""
	if got, _ := prov.Notifications(); got != want {
		t.Fatalf("after clear = %+v, want %+v", got, want)
	}
}

func TestSettingsNotificationsValidation(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	for _, body := range []map[string]any{
		{"notifications": map[string]any{"events": map[string]any{"diskLow": false}}}, // url required
		{"notifications": map[string]any{"url": "ntfy.sh/topic"}},
		{"notifications": map[string]any{"url": "ftp://ntfy.sh/topic"}},
		{"notifications": map[string]any{"url": "/var/hook"}},
		// One bad section writes nothing from the others.
		{"streaming": map[string]any{"bufferMinutes": 30}, "notifications": map[string]any{"url": "nope"}},
	} {
		rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", body, authHeader(tok))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%v: status = %d, want 400", body, rr.Code)
		}
	}
	if s, _ := prov.Streaming(); s.BufferMinutes == 30 {
		t.Fatal("partial write after a notifications validation error")
	}
	if n, _ := prov.Notifications(); n.URL != "" || !n.Events.DiskLow {
		t.Fatalf("notifications changed: %+v", n)
	}
}

type hookServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
	status int
}

func newHookServer(t *testing.T, status int) *hookServer {
	hs := &hookServer{status: status}
	hs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hs.mu.Lock()
		hs.bodies = append(hs.bodies, string(b))
		hs.mu.Unlock()
		w.WriteHeader(hs.status)
	}))
	t.Cleanup(hs.Close)
	return hs
}

func (hs *hookServer) count() int {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return len(hs.bodies)
}

type testResult struct {
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Status int    `json:"status"`
	Error  string `json:"error"`
}

func decodeTestResult(t *testing.T, rr *httptest.ResponseRecorder) testResult {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rr.Code, rr.Body.String())
	}
	var out testResult
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNotificationTestUsesSavedOrGivenURL(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	hook := newHookServer(t, http.StatusOK)

	// Nothing saved, nothing given.
	rr := doJSON(t, h, "POST", "/api/v1/admin/notifications/test", nil, authHeader(tok))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("no URL: status = %d", rr.Code)
	}

	// The saved URL (empty body).
	if err := prov.SetNotifications(settings.Notifications{URL: hook.URL + "/saved"}); err != nil {
		t.Fatal(err)
	}
	res := decodeTestResult(t, doJSON(t, h, "POST", "/api/v1/admin/notifications/test", nil, authHeader(tok)))
	if !res.OK || res.Status != 200 || res.Target != "webhook" || hook.count() != 1 {
		t.Fatalf("saved URL result = %+v (requests %d)", res, hook.count())
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(hook.bodies[0]), &payload)
	if payload["event"] != "test" || payload["title"] != "Bowtie test notification" {
		t.Fatalf("payload = %v", payload)
	}

	// A URL in the body wins (try before saving), and ignores the event choices.
	other := newHookServer(t, http.StatusOK)
	res = decodeTestResult(t, doJSON(t, h, "POST", "/api/v1/admin/notifications/test",
		map[string]any{"url": other.URL + "/try"}, authHeader(tok)))
	if !res.OK || other.count() != 1 || hook.count() != 1 {
		t.Fatalf("body URL result = %+v", res)
	}

	// Bad URL.
	rr = doJSON(t, h, "POST", "/api/v1/admin/notifications/test", map[string]any{"url": "nope"}, authHeader(tok))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad URL: status = %d", rr.Code)
	}
}

func TestNotificationTestReportsFailureWithoutURL(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	hook := newHookServer(t, http.StatusUnauthorized)
	res := decodeTestResult(t, doJSON(t, h, "POST", "/api/v1/admin/notifications/test",
		map[string]any{"url": hook.URL + "/secret-token"}, authHeader(tok)))
	if res.OK || res.Status != 401 || res.Error == "" || strings.Contains(res.Error, "secret-token") {
		t.Fatalf("result = %+v", res)
	}
}

func TestNotificationTestAdminOnly(t *testing.T) {
	h, st, _ := testAPIWithSettings(t, "", nil)
	seedUser(t, st, "viewer", "viewerpass", "viewer")
	rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "viewer", "password": "viewerpass"}, nil)
	tok := decodeLogin(t, rr).AccessToken
	rr = doJSON(t, h, "POST", "/api/v1/admin/notifications/test", map[string]any{"url": "http://127.0.0.1:1/x"}, authHeader(tok))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer: status = %d, want 403", rr.Code)
	}
}

// authHook records the Basic auth of each request it receives.
type authHook struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
}

func newAuthHook(t *testing.T) *authHook {
	t.Helper()
	a := &authHook{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		a.mu.Lock()
		if ok {
			a.auths = append(a.auths, u+":"+p)
		} else {
			a.auths = append(a.auths, "(none)")
		}
		a.mu.Unlock()
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *authHook) last(t *testing.T) string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.auths) == 0 {
		t.Fatal("no request received")
	}
	return a.auths[len(a.auths)-1]
}

func TestSettingsNotificationsCredentials(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)

	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{
			"url": "https://ntfy.example.com/alerts", "username": " alice ", "password": "p@ss:w/rd",
		},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "p@ss") {
		t.Fatal("password must never be returned")
	}
	n := section(decodeSettings(t, rr), "notifications")
	if n["username"] != "alice" || n["passwordConfigured"] != true {
		t.Fatalf("notifications = %v", n)
	}
	if got, _ := prov.Notifications(); got.Username != "alice" || got.Password != "p@ss:w/rd" {
		t.Fatalf("provider = %+v", got)
	}

	// Password absent or empty keeps it; username is updated.
	rr = doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{"url": "https://ntfy.example.com/alerts", "username": "", "password": ""},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT keep status = %d", rr.Code)
	}
	if got, _ := prov.Notifications(); got.Username != "" || got.Password != "p@ss:w/rd" {
		t.Fatalf("after keep = %+v", got)
	}

	// clearPassword removes it.
	doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{"url": "https://ntfy.example.com/alerts", "clearPassword": true},
	}, authHeader(tok))
	if got, _ := prov.Notifications(); got.Password != "" {
		t.Fatalf("after clearPassword = %+v", got)
	}

	// Turning notifications off (empty URL) forgets the credentials.
	doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{"url": "https://ntfy.example.com/alerts", "username": "bob", "password": "x"},
	}, authHeader(tok))
	doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{"url": ""},
	}, authHeader(tok))
	if got, _ := prov.Notifications(); got.Username != "" || got.Password != "" {
		t.Fatalf("after off = %+v", got)
	}
}

func TestSettingsNotificationsMovesURLCredentialsIntoFields(t *testing.T) {
	// user:pass@ in the URL would be shown back on the settings page.
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	rr := doJSON(t, h, "PUT", "/api/v1/admin/settings", map[string]any{
		"notifications": map[string]any{"url": "https://alice:s%40cret@ntfy.example.com/alerts"},
	}, authHeader(tok))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q", rr.Code, rr.Body.String())
	}
	n := section(decodeSettings(t, rr), "notifications")
	if n["url"] != "https://ntfy.example.com/alerts" || n["username"] != "alice" || n["passwordConfigured"] != true {
		t.Fatalf("notifications = %v", n)
	}
	if got, _ := prov.Notifications(); got.Password != "s@cret" {
		t.Fatalf("provider password = %q", got.Password)
	}
}

func TestNotificationTestSendsCredentials(t *testing.T) {
	h, st, prov := testAPIWithSettings(t, "", nil)
	tok := adminAuth(t, h, st)
	hook := newAuthHook(t)

	// Saved destination: saved credentials.
	if err := prov.SetNotifications(settings.Notifications{URL: hook.URL + "/saved", Username: "alice", Password: "saved-pw"}); err != nil {
		t.Fatal(err)
	}
	if res := decodeTestResult(t, doJSON(t, h, "POST", "/api/v1/admin/notifications/test", nil, authHeader(tok))); !res.OK {
		t.Fatalf("saved: %+v", res)
	}
	if got := hook.last(t); got != "alice:saved-pw" {
		t.Fatalf("saved auth = %q", got)
	}

	// Form values (before saving); an empty password falls back to the saved one.
	doJSON(t, h, "POST", "/api/v1/admin/notifications/test",
		map[string]any{"url": hook.URL + "/try", "username": "bob", "password": "typed"}, authHeader(tok))
	if got := hook.last(t); got != "bob:typed" {
		t.Fatalf("typed auth = %q", got)
	}
	doJSON(t, h, "POST", "/api/v1/admin/notifications/test",
		map[string]any{"url": hook.URL + "/try", "username": "bob"}, authHeader(tok))
	if got := hook.last(t); got != "bob:saved-pw" {
		t.Fatalf("fallback auth = %q", got)
	}
}
