package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/notify"
)

// testNotificationRequest: url is optional (absent or empty: the saved
// destination with its credentials), so the admin can try a URL before
// saving it. With a url, username is used as given and an empty password
// falls back to the saved one (the settings form never shows it).
type testNotificationRequest struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleAdminTestNotification sends a test message and reports how the target
// answered (200 with ok=false when delivery failed).
func (s *Server) handleAdminTestNotification(w http.ResponseWriter, r *http.Request) {
	var req testNotificationRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dest := notify.Destination{URL: strings.TrimSpace(req.URL), Username: strings.TrimSpace(req.Username), Password: req.Password}
	if s.deps.Settings != nil {
		cfg, err := s.deps.Settings.Notifications()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load settings")
			return
		}
		if dest.URL == "" {
			dest = notify.Destination{URL: cfg.URL, Username: cfg.Username, Password: cfg.Password}
		} else if dest.Password == "" {
			dest.Password = cfg.Password
		}
	}
	target := dest.URL
	if target == "" {
		writeError(w, http.StatusBadRequest, "no notification URL is set")
		return
	}
	if err := notify.ValidateURL(target); err != nil {
		writeError(w, http.StatusBadRequest, "url "+err.Error())
		return
	}
	ev := notify.TestEvent(time.Now())
	var res notify.Result
	if s.deps.Notifications != nil {
		res = s.deps.Notifications.Send(r.Context(), dest, ev)
	} else {
		res = notify.SendTo(r.Context(), nil, dest, ev)
	}
	writeJSON(w, http.StatusOK, res)
}
