package epg

import (
	"errors"
	"fmt"
	"log"
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

// trackFailure follows each source's run of failures and notifies once per
// run, after guideFailedAfter. The run's start and whether it was reported
// are kept in the settings table, so restarts neither reset the clock nor
// repeat the alert.
func (s *Service) trackFailure(name string, err error) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	sinceKey, notifiedKey := failKeys(name)
	if err == nil {
		s.clearFailureLocked(name)
		return
	}
	now := s.now()
	raw, _ := s.store.GetSetting(sinceKey)
	since, perr := time.Parse(time.RFC3339, raw)
	if perr != nil {
		if serr := s.store.SetSetting(sinceKey, now.UTC().Format(time.RFC3339)); serr != nil {
			log.Printf("epg %s: persist failingSince: %v", name, serr)
		}
		return
	}
	notified, _ := s.store.GetSetting(notifiedKey)
	n := s.notifier
	if n == nil || notified != "" || now.Sub(since) <= guideFailedAfter {
		return
	}
	if serr := s.store.SetSetting(notifiedKey, "1"); serr != nil {
		log.Printf("epg %s: persist failNotified: %v", name, serr)
	}
	n.Notify(notify.Event{
		Kind:    notify.EventGuideFailed,
		Key:     notify.EventGuideFailed + ":" + name,
		Title:   "Guide data isn't updating",
		Message: fmt.Sprintf("Guide data hasn't updated for a day: %s: %s", sourceLabel(name), errorText(err)),
		Time:    now,
	})
}

// clearFailure forgets a source's run of failures (it succeeded, or it was
// turned off — turning it back on starts counting afresh).
func (s *Service) clearFailure(name string) {
	s.failMu.Lock()
	defer s.failMu.Unlock()
	s.clearFailureLocked(name)
}

func (s *Service) clearFailureLocked(name string) {
	since, notified := failKeys(name)
	for _, k := range []string{since, notified} {
		if v, _ := s.store.GetSetting(k); v != "" {
			_ = s.store.SetSetting(k, "")
		}
	}
}

func failKeys(name string) (since, notified string) {
	return "epg." + name + ".failingSince", "epg." + name + ".failNotified"
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
