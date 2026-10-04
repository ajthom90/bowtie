package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/notify"
)

// testNotificationRequest: url is optional (absent or empty: the saved URL),
// so the admin can try a URL before saving it.
type testNotificationRequest struct {
	URL string `json:"url"`
}

// handleAdminTestNotification sends a test message and reports how the target
// answered (200 with ok=false when delivery failed).
func (s *Server) handleAdminTestNotification(w http.ResponseWriter, r *http.Request) {
	var req testNotificationRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target := strings.TrimSpace(req.URL)
	if target == "" && s.deps.Settings != nil {
		cfg, err := s.deps.Settings.Notifications()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load settings")
			return
		}
		target = cfg.URL
	}
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
		res = s.deps.Notifications.Send(r.Context(), target, ev)
	} else {
		res = notify.Send(r.Context(), nil, target, ev)
	}
	writeJSON(w, http.StatusOK, res)
}
