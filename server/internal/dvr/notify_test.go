package dvr

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/notify"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
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

func (f *fakeNotifier) waitFor(t *testing.T, kind string) notify.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range f.all() {
			if ev.Kind == kind {
				return ev
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no %s event; got %+v", kind, f.all())
	return notify.Event{}
}

func (f *fakeNotifier) count(kind string) int {
	n := 0
	for _, ev := range f.all() {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// withNotifier gives the env's service a fake notifier and lets the hourly
// sweep run on the first Tick without deleting anything.
func withNotifier(e *env) *fakeNotifier {
	f := &fakeNotifier{}
	e.svc.deps.Notifier = f
	return f
}

func TestNotifiesFailedRecordingWithReason(t *testing.T) {
	e := newEnv(t)
	f := withNotifier(e)
	e.src.always = stream.ErrNoSignal
	r, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["9.1"], Title: "The Evening News",
		Subtitle: "Oct 4", Start: t0.Add(time.Minute), Stop: t0.Add(3 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecWaiting)
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecFailed)
	ev := f.waitFor(t, notify.EventRecordingFailed)
	if ev.Title != "Recording failed: The Evening News" || ev.RecordingID != r.ID || ev.Key != "recordingFailed:"+strconv.FormatInt(r.ID, 10) {
		t.Fatalf("event = %+v", ev)
	}
	for _, want := range []string{"The Evening News — Oct 4", "on 9.1 B", "didn't record: No signal."} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("message %q lacks %q", ev.Message, want)
		}
	}
	if !ev.Time.Equal(r.WindowStop()) {
		t.Errorf("time = %v, want the DVR clock", ev.Time)
	}
	if f.count(notify.EventRecordingReady) != 0 {
		t.Fatal("a failed recording must not report ready")
	}
}

func TestNotifiesConversionFailure(t *testing.T) {
	e := newEnv(t)
	f := withNotifier(e)
	e.conv.fail = true
	r := e.schedule(t, "5.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	time.Sleep(20 * time.Millisecond)
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecFailed)
	if ev := f.waitFor(t, notify.EventRecordingFailed); !strings.Contains(ev.Message, "conversion failed: ffmpeg failed") {
		t.Fatalf("message = %q", ev.Message)
	}
}

func TestNotifiesReadyAfterConversion(t *testing.T) {
	e := newEnv(t)
	f := withNotifier(e)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	time.Sleep(20 * time.Millisecond)
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecReady)
	ev := f.waitFor(t, notify.EventRecordingReady)
	if ev.Title != "Recording ready: Show 9.1" || ev.Message != "Show 9.1 on 9.1 B is ready to watch." || ev.RecordingID != r.ID {
		t.Fatalf("event = %+v", ev)
	}
	if f.count(notify.EventRecordingFailed) != 0 {
		t.Fatal("unexpected failure event")
	}
}

// Skipping a series episode is the admin's choice, not a failure to report.
func TestSkippedEpisodeIsNotNotified(t *testing.T) {
	e := newEnv(t)
	f := withNotifier(e)
	start := t0.Add(time.Hour)
	id, err := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: e.chans["9.1"].ID, ChannelName: "9.1 B",
		Title: "Series", RuleID: 1, Start: start, Stop: start.Add(time.Hour), State: store.RecScheduled, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(id); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.st.RecordingByID(id); r.Failure != "skipped" {
		t.Fatalf("not skipped: %+v", r)
	}
	if got := f.all(); len(got) != 0 {
		t.Fatalf("events = %+v", got)
	}
}

func TestNotifiesFailedCaptureWhenDiskFull(t *testing.T) {
	e := newEnv(t)
	f := withNotifier(e)
	e.svc.deps.FreeBytes = func(string) (int64, error) { return 100 << 20, nil }
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecFailed)
	if ev := f.waitFor(t, notify.EventRecordingFailed); !strings.HasSuffix(ev.Message, "didn't record: Disk full.") {
		t.Fatalf("message = %q", ev.Message)
	}
	// The hourly check on that Tick also reported the disk.
	if ev := f.waitFor(t, notify.EventDiskLow); !strings.Contains(ev.Message, "Only 0.1 GB free") || !strings.Contains(ev.Message, "won't start") {
		t.Fatalf("disk message = %q", ev.Message)
	}
}

func TestDiskLowCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		free    int64
		minFree int64
		want    string // "" = no event
	}{
		{"plenty", 50 << 30, 10 << 30, ""},
		{"below floor", 1 << 30, 0, "won't start until at least 2.0 GB is free"},
		{"below retention target", 5 << 30, 10 << 30, "to keep 10.0 GB free, but couldn't free enough"},
		{"retention off, above floor", 5 << 30, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := withNotifier(e)
			e.svc.deps.MinFreeBytes = tc.minFree
			e.svc.deps.FreeBytes = func(string) (int64, error) { return tc.free, nil }
			e.svc.Tick() // first Tick runs the hourly sweep + check
			evs := f.all()
			if tc.want == "" {
				if len(evs) != 0 {
					t.Fatalf("events = %+v", evs)
				}
				return
			}
			if len(evs) != 1 || evs[0].Kind != notify.EventDiskLow || !strings.Contains(evs[0].Message, tc.want) {
				t.Fatalf("events = %+v, want diskLow with %q", evs, tc.want)
			}
			// Not again until the next hourly check.
			e.clock.Set(t0.Add(30 * time.Minute))
			e.svc.Tick()
			if n := f.count(notify.EventDiskLow); n != 1 {
				t.Fatalf("diskLow events = %d after a non-hourly tick", n)
			}
		})
	}
}
