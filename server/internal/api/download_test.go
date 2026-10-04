package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/config"
)

// /android and /tv are short enough to type on a TV remote (Downloader app)
// and hand out the APK that matches the running server.
func TestAppDownloadRedirects(t *testing.T) {
	cases := []struct {
		version, path, want string
	}{
		{"0.8.1", "/android", "https://github.com/ajthom90/bowtie/releases/download/v0.8.1/bowtie-0.8.1.apk"},
		{"0.8.1", "/tv", "https://github.com/ajthom90/bowtie/releases/download/v0.8.1/bowtie-tv-0.8.1.apk"},
		{"v1.2.3", "/tv", "https://github.com/ajthom90/bowtie/releases/download/v1.2.3/bowtie-tv-1.2.3.apk"},
		// Dev and pre-release builds have no matching release: send the latest.
		{"0.1.0-dev", "/android", "https://github.com/ajthom90/bowtie/releases/latest/download/bowtie-android.apk"},
		{"", "/tv", "https://github.com/ajthom90/bowtie/releases/latest/download/bowtie-tv.apk"},
	}
	for _, c := range cases {
		h := api.New(api.Deps{Cfg: config.Config{ListenAddr: ":0"}, Version: c.version})
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rr.Code != http.StatusFound || rr.Header().Get("Location") != c.want {
			t.Errorf("version %q %s: status=%d location=%q want %q", c.version, c.path, rr.Code, rr.Header().Get("Location"), c.want)
		}
	}
}
