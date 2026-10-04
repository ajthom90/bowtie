package dvr

import (
	"fmt"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/notify"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// FailureText is a recording failure in words (matches the web app's).
func FailureText(failure, detail string) string {
	switch failure {
	case "noTuner":
		return "No tuner was free"
	case "noSignal":
		return "No signal"
	case "diskFull":
		return "Disk full"
	case "skipped":
		return "Skipped"
	}
	if d := strings.TrimSpace(detail); d != "" {
		return d
	}
	return "Something went wrong"
}

func (s *Service) notify(ev notify.Event) {
	if s.deps.Notifier != nil {
		ev.Time = s.deps.Clock()
		s.deps.Notifier.Notify(ev)
	}
}

// notifyFailed reports a recording that just failed (never a skipped
// episode: that's the admin's own choice).
func (s *Service) notifyFailed(r store.Recording) {
	if r.State != store.RecFailed || r.Failure == "skipped" {
		return
	}
	s.notify(notify.Event{
		Kind:        notify.EventRecordingFailed,
		Key:         fmt.Sprintf("%s:%d", notify.EventRecordingFailed, r.ID),
		RecordingID: r.ID,
		Title:       "Recording failed: " + r.Title,
		Message: fmt.Sprintf("%s on %s (%s) didn't record: %s.", showName(r), r.ChannelName,
			r.Start.In(time.Local).Format("Mon Jan 2, 3:04 PM"), strings.TrimSuffix(FailureText(r.Failure, r.FailureDetail), ".")),
	})
}

// notifyReady reports a recording that finished converting.
func (s *Service) notifyReady(r store.Recording) {
	msg := fmt.Sprintf("%s on %s is ready to watch.", showName(r), r.ChannelName)
	if r.Partial {
		msg = fmt.Sprintf("%s on %s is ready to watch (part of it is missing).", showName(r), r.ChannelName)
	}
	s.notify(notify.Event{
		Kind:        notify.EventRecordingReady,
		Key:         fmt.Sprintf("%s:%d", notify.EventRecordingReady, r.ID),
		RecordingID: r.ID,
		Title:       "Recording ready: " + r.Title,
		Message:     msg,
	})
}

// checkDisk reports free space below the capture floor or the retention
// target (run after the sweep, so only space it couldn't free counts).
func (s *Service) checkDisk() {
	if s.deps.Notifier == nil {
		return
	}
	free, err := s.deps.FreeBytes(s.deps.Dir)
	if err != nil {
		return
	}
	var msg, level string
	switch {
	case free < diskFloor:
		level = "floor"
		msg = fmt.Sprintf("Only %s free for recordings in %s. New recordings won't start until at least %s is free.",
			gb(free), s.deps.Dir, gb(diskFloor))
	case free < s.deps.MinFreeBytes:
		level = "retention"
		msg = fmt.Sprintf("Only %s free for recordings in %s. Bowtie deletes the oldest unprotected recordings to keep %s free, but couldn't free enough.",
			gb(free), s.deps.Dir, gb(s.deps.MinFreeBytes))
	default:
		return
	}
	// Separate keys: reaching the floor (recordings stop) isn't muted by an
	// earlier retention alert.
	s.notify(notify.Event{Kind: notify.EventDiskLow, Key: notify.EventDiskLow + ":" + level,
		Title: "Bowtie is low on disk space", Message: msg})
}

func showName(r store.Recording) string {
	if r.Subtitle != "" {
		return r.Title + " — " + r.Subtitle
	}
	return r.Title
}

func gb(n int64) string {
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}
