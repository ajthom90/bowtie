package api

import (
	"net/http"
	"regexp"
	"strings"
)

const releasesURL = "https://github.com/ajthom90/bowtie/releases"

var releaseVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// appDownload redirects to the Android APK (app "android" or "tv") that
// matches this server's release, so a short URL typed on a TV remote installs
// a client the server speaks. Dev and pre-release builds have no matching
// release asset and get the latest release instead.
func (s *Server) appDownload(app string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := strings.TrimPrefix(s.deps.Version, "v")
		target := releasesURL + "/latest/download/bowtie-" + app + ".apk"
		if releaseVersionRe.MatchString(v) {
			name := "bowtie-" + v + ".apk"
			if app == "tv" {
				name = "bowtie-tv-" + v + ".apk"
			}
			target = releasesURL + "/download/v" + v + "/" + name
		}
		http.Redirect(w, r, target, http.StatusFound)
	}
}
