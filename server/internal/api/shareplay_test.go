package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestVersionCarriesServerIdentity(t *testing.T) {
	h := api.New(api.Deps{Cfg: config.Config{ListenAddr: ":0"}, Version: "1.0.0", ServerID: "abc123", ServerName: "truenas"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	var body struct {
		Version, ServerID, ServerName string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.ServerID != "abc123" || body.ServerName != "truenas" {
		t.Fatalf("%s", rr.Body.String())
	}
}

func TestCreateSessionJoinsOrFallsBack(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "bob", "pass", "viewer")
	tok := decodeLogin(t, doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "pass"}, nil))
	authH := map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	caps := map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}}

	var joined, started string
	ss.joinFn = func(_ context.Context, _ store.User, sessionID string, _ int64, _ transcode.ClientCaps) (stream.ViewerHandle, error) {
		joined = sessionID
		if sessionID == "gone" {
			return stream.ViewerHandle{}, stream.ErrNotJoinable
		}
		ss.register("vj", t.TempDir())
		return stream.ViewerHandle{ViewerID: "vj", SessionID: sessionID}, nil
	}
	ss.startFn = func(context.Context, store.User, int64, transcode.ClientCaps) (stream.ViewerHandle, error) {
		started = "yes"
		ss.register("vs", t.TempDir())
		return stream.ViewerHandle{ViewerID: "vs", SessionID: "s-new"}, nil
	}

	rr := doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{"channelId": 1, "caps": caps, "joinSessionId": "s-alice"}, authH)
	var resp struct {
		ViewerID string `json:"viewerId"`
		Session  struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil || resp.ViewerID != "vj" || joined != "s-alice" || started != "" {
		t.Fatalf("join: %d %s joined=%q started=%q", rr.Code, rr.Body.String(), joined, started)
	}
	if resp.Session.ID != "s-alice" {
		t.Fatalf("session id %q", resp.Session.ID)
	}

	rr = doJSON(t, h, "POST", "/api/v1/sessions", map[string]any{"channelId": 1, "caps": caps, "joinSessionId": "gone"}, authH)
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &resp) != nil || resp.ViewerID != "vs" || started != "yes" {
		t.Fatalf("fallback: %d %s", rr.Code, rr.Body.String())
	}
}
