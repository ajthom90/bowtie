package epg

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/notify"
)

// guideFailedAfter: a source must fail this long without a success before the
// admin hears about it (the HDHomeRun guide's daily limit answers 403 for a
// while, and that's normal).
const guideFailedAfter = 24 * time.Hour

// SetNotifier sends a notification when a guide source has failed for more
// than a day (nil = off). Call before Run.
func (s *Service) SetNotifier(n notify.Notifier) {
	s.failMu.Lock()
	s.notifier = n
	s.failMu.Unlock()
}

// trackFailure follows each source's run of failures (in memory: a restart
// starts the count again) and notifies once per run, after guideFailedAfter.
func (s *Service) trackFailure(name string, err error) {
	s.failMu.Lock()
	if s.failingSince == nil {
		s.failingSince = map[string]time.Time{}
		s.failNotified = map[string]bool{}
	}
	if err == nil {
		delete(s.failingSince, name)
		delete(s.failNotified, name)
		s.failMu.Unlock()
		return
	}
	now := s.now()
	since, failing := s.failingSince[name]
	if !failing {
		s.failingSince[name] = now
	}
	n := s.notifier
	due := failing && now.Sub(since) > guideFailedAfter && !s.failNotified[name] && n != nil
	if due {
		s.failNotified[name] = true
	}
	s.failMu.Unlock()
	if !due {
		return
	}
	n.Notify(notify.Event{
		Kind:    notify.EventGuideFailed,
		Key:     notify.EventGuideFailed + ":" + name,
		Title:   "Guide data isn't updating",
		Message: fmt.Sprintf("Guide data hasn't updated for a day: %s: %s", sourceLabel(name), errorText(err)),
		Time:    now,
	})
}

func sourceLabel(name string) string {
	switch name {
	case "xmltv":
		return "XMLTV"
	case "sd":
		return "Schedules Direct"
	case sourceHDHomeRun:
		return "HDHomeRun guide"
	}
	return name
}

// errorText is err without any URL's path or query (an XMLTV URL may carry an
// API key, and the notification goes to a third party).
func errorText(err error) string {
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil && u.Hostname() != "" {
			msg = strings.ReplaceAll(msg, ue.URL, u.Hostname())
		}
	}
	return msg
}
