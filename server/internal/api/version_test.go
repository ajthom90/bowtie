package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/config"
)

func TestVersionIsPublic(t *testing.T) {
	h := api.New(api.Deps{Cfg: config.Config{ListenAddr: ":0"}, Version: "9.8.7"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil || body.Version != "9.8.7" {
		t.Fatalf("version=%q err=%v", body.Version, err)
	}
}
