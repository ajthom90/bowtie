// Package notify sends admin notifications (a recording failed, the disk is
// nearly full, …) to one configured URL: ntfy, a Discord webhook, or any URL
// that accepts a JSON POST. Delivery never blocks the caller: events go
// through a small queue to one worker, which applies the admin's event
// choices and a rate limit, sends with a 5 s timeout, and retries once.
package notify

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/ajthom90/bowtie/server/internal/settings"
)

// Event kinds (also the generic webhook's "event" field).
const (
	EventRecordingFailed = "recordingFailed"
	EventRecordingReady  = "recordingReady"
	EventDiskLow         = "diskLow"
	EventGuideFailed     = "guideFailed"
	EventTest            = "test"
)

// Defaults for the delivery queue.
const (
	DefaultRetryAfter = 30 * time.Second
	DefaultRateWindow = 6 * time.Hour
	defaultQueueSize  = 32
)

// Event is one notification.
type Event struct {
	Kind    string
	Title   string
	Message string
	// RecordingID is the recording the event is about (0: none).
	RecordingID int64
	// Key groups events for the rate limit ("" = Kind): the same key is sent
	// at most once per rate window, and a recordingFailed key only once.
	Key  string
	Time time.Time
}

// Notifier accepts events without blocking (nil = notifications off).
type Notifier interface {
	Notify(Event)
}

// TestEvent is what "Send test" delivers.
func TestEvent(now time.Time) Event {
	return Event{
		Kind:    EventTest,
		Title:   "Bowtie test notification",
		Message: "Notifications from Bowtie are working.",
		Time:    now,
	}
}

// Options tune a Service (zero values: production defaults).
type Options struct {
	Client     *http.Client
	Now        func() time.Time
	RetryAfter time.Duration
	RateWindow time.Duration
	QueueSize  int
}

// Service is the production Notifier.
type Service struct {
	cfg        func() (settings.Notifications, error)
	client     *http.Client
	now        func() time.Time
	retryAfter time.Duration
	rateWindow time.Duration
	queue      chan job

	mu   sync.Mutex
	sent map[string]time.Time
	once map[string]bool
}

type job struct {
	ev    Event
	retry bool
}

// New builds a Service that reads its URL and event choices from cfg on
// every event (settings changes apply at once). Call Run to deliver.
func New(cfg func() (settings.Notifications, error), opts Options) *Service {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: SendTimeout}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RetryAfter <= 0 {
		opts.RetryAfter = DefaultRetryAfter
	}
	if opts.RateWindow <= 0 {
		opts.RateWindow = DefaultRateWindow
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	return &Service{
		cfg:        cfg,
		client:     opts.Client,
		now:        opts.Now,
		retryAfter: opts.RetryAfter,
		rateWindow: opts.RateWindow,
		queue:      make(chan job, opts.QueueSize),
		sent:       map[string]time.Time{},
		once:       map[string]bool{},
	}
}

// Notify queues ev; when the queue is full the event is dropped (and logged).
func (s *Service) Notify(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = s.now()
	}
	s.enqueue(job{ev: ev})
}

func (s *Service) enqueue(j job) {
	select {
	case s.queue <- j:
	default:
		log.Printf("notify: queue full; dropped %s notification %q", j.ev.Kind, j.ev.Title)
	}
}

// Run delivers queued events until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			s.handle(ctx, j)
		}
	}
}

// Send delivers ev to rawURL now, once, with the Service's client (the admin
// "Send test" button). It ignores the event choices and the rate limit.
func (s *Service) Send(ctx context.Context, rawURL string, ev Event) Result {
	return Send(ctx, s.client, rawURL, ev)
}

func (s *Service) handle(ctx context.Context, j job) {
	cfg, err := s.cfg()
	if err != nil {
		log.Printf("notify: read settings: %v", err)
		return
	}
	if cfg.URL == "" {
		return
	}
	if !j.retry && (!enabled(cfg.Events, j.ev.Kind) || !s.allow(j.ev)) {
		return
	}
	res := s.Send(ctx, cfg.URL, j.ev)
	if res.OK {
		return
	}
	if j.retry || !res.retryable() {
		log.Printf("notify: %s notification to %s failed: %s", j.ev.Kind, Host(cfg.URL), res.Error)
		return
	}
	log.Printf("notify: %s notification to %s failed (retrying in %s): %s", j.ev.Kind, Host(cfg.URL), s.retryAfter, res.Error)
	retry := job{ev: j.ev, retry: true}
	go func() {
		t := time.NewTimer(s.retryAfter)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			s.enqueue(retry)
		}
	}()
}

// allow applies the rate limit and records the send (a delivery that then
// fails still counts, so a wrong URL never turns into a flood).
func (s *Service) allow(ev Event) bool {
	key := ev.Key
	if key == "" {
		key = ev.Kind
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Kind == EventRecordingFailed {
		if s.once[key] {
			return false
		}
		s.once[key] = true
		return true
	}
	now := s.now()
	if last, ok := s.sent[key]; ok && now.Sub(last) < s.rateWindow {
		return false
	}
	s.sent[key] = now
	return true
}

func enabled(e settings.NotificationEvents, kind string) bool {
	switch kind {
	case EventRecordingFailed:
		return e.RecordingFailed
	case EventRecordingReady:
		return e.RecordingReady
	case EventDiskLow:
		return e.DiskLow
	case EventGuideFailed:
		return e.GuideFailed
	case EventTest:
		return true
	}
	return false
}
