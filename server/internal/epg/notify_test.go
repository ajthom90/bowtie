package epg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/notify"
	"github.com/ajthom90/bowtie/server/internal/settings"
)

type fakeNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (f *fakeNotifier) Notify(ev notify.Event) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
}

func (f *fakeNotifier) all() []notify.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notify.Event(nil), f.events...)
}

type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *stepClock) Add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

// A guide source that keeps failing is reported once, only after more than a
// day of failures; a success ends the run, and the next run counts afresh.
func TestGuideFailureNotifiesAfterADay(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	missing := filepath.Join(t.TempDir(), "missing.xml")
	if err := prov.SetXMLTV(settings.XMLTV{Source: missing, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, prov)
	clk := &stepClock{now: time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)}
	svc.now = clk.Now
	f := &fakeNotifier{}
	svc.SetNotifier(f)

	refresh := func() { _ = svc.RefreshAll(context.Background()) }
	refresh() // first failure starts the run
	clk.Add(12 * time.Hour)
	refresh()
	clk.Add(12 * time.Hour) // exactly a day: not yet
	refresh()
	if got := f.all(); len(got) != 0 {
		t.Fatalf("notified within a day: %+v", got)
	}
	clk.Add(time.Hour)
	refresh()
	got := f.all()
	if len(got) != 1 {
		t.Fatalf("events = %+v, want 1", got)
	}
	ev := got[0]
	if ev.Kind != notify.EventGuideFailed || ev.Key != "guideFailed:xmltv" ||
		!strings.HasPrefix(ev.Message, "Guide data hasn't updated for a day: XMLTV: open file:") {
		t.Fatalf("event = %+v", ev)
	}
	clk.Add(12 * time.Hour)
	refresh()
	if n := len(f.all()); n != 1 {
		t.Fatalf("same run notified %d times", n)
	}

	// A success ends the run; a new failure starts counting again.
	fixture, _ := filepath.Abs(filepath.Join("xmltv", "testdata", "guide.xml"))
	_ = prov.SetXMLTV(settings.XMLTV{Source: fixture, RefreshHours: 12})
	refresh()
	_ = prov.SetXMLTV(settings.XMLTV{Source: missing, RefreshHours: 12})
	refresh()
	clk.Add(20 * time.Hour)
	refresh()
	if n := len(f.all()); n != 1 {
		t.Fatalf("new run notified before a day: %d", n)
	}
	clk.Add(5 * time.Hour)
	refresh()
	if n := len(f.all()); n != 2 {
		t.Fatalf("new run after a day: %d events, want 2", n)
	}
}

// Without a notifier nothing breaks.
func TestGuideFailureWithoutNotifier(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	_ = prov.SetXMLTV(settings.XMLTV{Source: filepath.Join(t.TempDir(), "x.xml"), RefreshHours: 12})
	svc := NewService(st, prov)
	clk := &stepClock{now: time.Now()}
	svc.now = clk.Now
	for i := 0; i < 3; i++ {
		_ = svc.RefreshAll(context.Background())
		clk.Add(20 * time.Hour)
	}
}

// The XMLTV URL (which may hold an API key) stays out of the message.
func TestGuideFailureMessageHidesURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close() // unreachable
	st := testStore(t)
	prov := testProvider(t, st)
	_ = prov.SetXMLTV(settings.XMLTV{Source: "http://" + addr + "/guide.xml?apikey=SECRET", RefreshHours: 12})
	svc := NewService(st, prov)
	clk := &stepClock{now: time.Now()}
	svc.now = clk.Now
	f := &fakeNotifier{}
	svc.SetNotifier(f)
	_ = svc.RefreshAll(context.Background())
	clk.Add(25 * time.Hour)
	_ = svc.RefreshAll(context.Background())
	got := f.all()
	if len(got) != 1 || strings.Contains(got[0].Message, "SECRET") || !strings.Contains(got[0].Message, "127.0.0.1") {
		t.Fatalf("events = %+v", got)
	}
}

// The run of failures survives restarts: a server restarted nightly still
// hears about a guide that's been failing for over a day, and only once.
func TestGuideFailureSurvivesRestarts(t *testing.T) {
	st := testStore(t)
	prov := testProvider(t, st)
	missing := filepath.Join(t.TempDir(), "missing.xml")
	if err := prov.SetXMLTV(settings.XMLTV{Source: missing, RefreshHours: 12}); err != nil {
		t.Fatal(err)
	}
	clk := &stepClock{now: time.Date(2026, 10, 4, 6, 0, 0, 0, time.UTC)}
	f := &fakeNotifier{}
	start := func() *Service { // a fresh process on the same database
		svc := NewService(st, prov)
		svc.now = clk.Now
		svc.SetNotifier(f)
		return svc
	}
	_ = start().RefreshAll(context.Background())
	clk.Add(13 * time.Hour)
	_ = start().RefreshAll(context.Background())
	if n := len(f.all()); n != 0 {
		t.Fatalf("notified within a day: %d", n)
	}
	clk.Add(12 * time.Hour)
	_ = start().RefreshAll(context.Background())
	if n := len(f.all()); n != 1 {
		t.Fatalf("after a day across restarts: %d events, want 1", n)
	}
	clk.Add(12 * time.Hour)
	_ = start().RefreshAll(context.Background())
	if n := len(f.all()); n != 1 {
		t.Fatalf("a restart repeated the alert: %d events", n)
	}
}
