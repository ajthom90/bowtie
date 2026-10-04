package dvr

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

// --- fakes -------------------------------------------------------------------

// fakeSource hands out readers that stream bytes until closed. busy makes the
// next N opens fail with ErrTunersBusy; eofAfter makes a reader end on its own
// after that many chunks (a dropped tuner).
type fakeSource struct {
	mu       sync.Mutex
	busy     int
	always   error
	eofAfter int
	opens    int
}

func (f *fakeSource) Open(ctx context.Context, ch store.Channel) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	if f.always != nil {
		return nil, f.always
	}
	if f.busy > 0 {
		f.busy--
		return nil, stream.ErrTunersBusy
	}
	r := &chunkReader{done: make(chan struct{}), left: f.eofAfter}
	f.eofAfter = 0
	return r, nil
}

func (f *fakeSource) Opens() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

type chunkReader struct {
	once sync.Once
	done chan struct{}
	left int // 0 = unlimited
	n    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	select {
	case <-r.done:
		return 0, io.EOF
	case <-time.After(time.Millisecond):
	}
	if r.left > 0 {
		r.n++
		if r.n > r.left {
			return 0, io.EOF
		}
	}
	n := copy(p, strings.Repeat("G", 188))
	return n, nil
}

func (r *chunkReader) Close() error {
	r.once.Do(func() { close(r.done) })
	return nil
}

// fakeConverter records its inputs and writes a VOD playlist.
type fakeConverter struct {
	mu    sync.Mutex
	parts [][]string
	fail  bool
}

func (c *fakeConverter) Convert(_ context.Context, parts []string, outDir string) (time.Duration, error) {
	c.mu.Lock()
	c.parts = append(c.parts, parts)
	fail := c.fail
	c.mu.Unlock()
	if fail {
		return 0, errors.New("ffmpeg failed")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	return 90 * time.Second, os.WriteFile(filepath.Join(outDir, "v720.m3u8"), []byte("#EXTM3U\n#EXT-X-ENDLIST\n"), 0o644)
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Set(t time.Time) { c.mu.Lock(); c.now = t; c.mu.Unlock() }

// --- harness -----------------------------------------------------------------

var t0 = time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)

type env struct {
	svc   *Service
	st    *store.Store
	src   *fakeSource
	conv  *fakeConverter
	clock *clock
	dir   string
	chans map[string]store.Channel
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.UpsertDevice(store.Device{DeviceID: "d", IP: "1.2.3.4", Model: "DUO", TunerCount: 2, StreamPort: 5004, LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncLineup("d", []store.Channel{
		{DeviceID: "d", GuideNumber: "5.1", Name: "A"},
		{DeviceID: "d", GuideNumber: "9.1", Name: "B"},
		{DeviceID: "d", GuideNumber: "11.1", Name: "C"},
	}); err != nil {
		t.Fatal(err)
	}
	chans := map[string]store.Channel{}
	all, _ := st.ListChannels(false)
	for _, c := range all {
		_ = st.UpdateChannel(c.ID, true, "")
		c.Enabled = true
		chans[c.GuideNumber] = c
	}
	e := &env{st: st, src: &fakeSource{}, conv: &fakeConverter{}, clock: &clock{now: t0}, dir: t.TempDir(), chans: chans}
	e.svc = e.newService()
	t.Cleanup(e.svc.Shutdown)
	return e
}

func (e *env) newService() *Service {
	return New(Deps{
		Store:      e.st,
		Source:     e.src,
		Converter:  e.conv,
		Dir:        e.dir,
		Clock:      e.clock.Now,
		RetryEvery: 5 * time.Millisecond,
		FreeBytes:  func(string) (int64, error) { return 1 << 40, nil },
	})
}

func (e *env) schedule(t *testing.T, guide string, start, stop time.Time) store.Recording {
	t.Helper()
	r, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans[guide], Title: "Show " + guide, Start: start, Stop: stop})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitState(t *testing.T, st *store.Store, id int64, want string) store.Recording {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, err := st.RecordingByID(id)
		if err == nil && r.State == want {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("recording %d state=%q (err %v), want %q", id, r.State, err, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// --- tests -------------------------------------------------------------------

func TestScheduleConflictsCountDistinctChannels(t *testing.T) {
	e := newEnv(t)
	e.schedule(t, "5.1", t0.Add(time.Hour), t0.Add(2*time.Hour))
	_, warn, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["5.1"], Title: "same channel",
		Start: t0.Add(90 * time.Minute), Stop: t0.Add(3 * time.Hour)})
	if err != nil || len(warn) != 0 {
		t.Fatalf("same channel: warn=%v err=%v", warn, err)
	}
	_, warn, err = e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["9.1"], Title: "second tuner",
		Start: t0.Add(90 * time.Minute), Stop: t0.Add(3 * time.Hour)})
	if err != nil || len(warn) != 1 || warn[0].Code != "usesAllTuners" {
		t.Fatalf("second tuner: warn=%v err=%v", warn, err)
	}
	_, _, err = e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["11.1"], Title: "third",
		Start: t0.Add(100 * time.Minute), Stop: t0.Add(110 * time.Minute)})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.TunerCount != 2 || len(ce.Conflicts) != 3 {
		t.Fatalf("third: err=%v", err)
	}
	if _, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["11.1"], Title: "third",
		Start: t0.Add(100 * time.Minute), Stop: t0.Add(110 * time.Minute), Force: true}); err != nil {
		t.Fatalf("force: %v", err)
	}
	// Padding alone doesn't conflict: 11.1 right after the 9.1 show ends.
	if _, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["11.1"], Title: "after",
		Start: t0.Add(3 * time.Hour), Stop: t0.Add(4 * time.Hour)}); err != nil {
		t.Fatalf("back-to-back: %v", err)
	}
}

func TestScheduleRejectsPastAndBackwards(t *testing.T) {
	e := newEnv(t)
	for _, w := range [][2]time.Time{{t0.Add(-2 * time.Hour), t0.Add(-time.Hour)}, {t0.Add(2 * time.Hour), t0.Add(time.Hour)}} {
		if _, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["5.1"], Title: "x", Start: w[0], Stop: w[1]}); !errors.Is(err, ErrBadWindow) {
			t.Fatalf("window %v: err=%v", w, err)
		}
	}
}

func TestRecordsWindowThenConverts(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.svc.Tick() // before the window: nothing
	if e.src.Opens() != 0 {
		t.Fatal("opened before window")
	}
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	rec := waitState(t, e.st, r.ID, store.RecRecording)
	if !rec.ActualStart.Equal(r.WindowStart()) || rec.Dir == "" {
		t.Fatalf("recording row %+v", rec)
	}
	time.Sleep(20 * time.Millisecond)
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	done := waitState(t, e.st, r.ID, store.RecReady)
	if done.Partial || done.DurationSec != 90 || done.SizeBytes == 0 {
		t.Fatalf("ready row %+v", done)
	}
	if len(e.conv.parts) != 1 || len(e.conv.parts[0]) != 1 || filepath.Base(e.conv.parts[0][0]) != "part-001.ts" {
		t.Fatalf("converter parts %v", e.conv.parts)
	}
	if _, err := os.Stat(e.conv.parts[0][0]); !os.IsNotExist(err) {
		t.Fatalf("capture part not removed after conversion: %v", err)
	}
}

func TestWaitsForTunerAndMarksPartial(t *testing.T) {
	e := newEnv(t)
	e.src.busy = 1 << 30 // until we say so
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(30*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecWaiting)
	e.clock.Set(r.WindowStart().Add(5 * time.Minute)) // tuner frees 5 min late
	e.src.mu.Lock()
	e.src.busy = 0
	e.src.mu.Unlock()
	rec := waitState(t, e.st, r.ID, store.RecRecording)
	if rec.MissedSec != 300 {
		t.Fatalf("missedSec=%d want 300", rec.MissedSec)
	}
	time.Sleep(20 * time.Millisecond) // let some bytes land before the window ends
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	if done := waitState(t, e.st, r.ID, store.RecReady); !done.Partial {
		t.Fatalf("late start not partial: %+v", done)
	}
}

func TestNoTunerForWholeWindowFails(t *testing.T) {
	e := newEnv(t)
	e.src.always = stream.ErrTunersBusy
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecWaiting)
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	if f := waitState(t, e.st, r.ID, store.RecFailed); f.Failure != "noTuner" {
		t.Fatalf("failure=%q", f.Failure)
	}
	if len(e.conv.parts) != 0 {
		t.Fatal("converted an empty recording")
	}
}

func TestDroppedStreamContinuesInNewPart(t *testing.T) {
	e := newEnv(t)
	e.src.eofAfter = 3 // first reader drops after 3 chunks
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	waitPartData(t, e.st, r.ID, "part-002.ts")
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecReady)
	if got := e.conv.parts[0]; len(got) != 2 || filepath.Base(got[1]) != "part-002.ts" {
		t.Fatalf("parts %v", got)
	}
}

func TestRestartResumesAndFinishes(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(10*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	time.Sleep(10 * time.Millisecond)
	e.svc.Shutdown() // server stops mid-recording; row stays "recording"
	if rec, _ := e.st.RecordingByID(r.ID); rec.State != store.RecRecording {
		t.Fatalf("after shutdown state=%q", rec.State)
	}

	e.svc = e.newService()
	t.Cleanup(e.svc.Shutdown)
	e.clock.Set(r.WindowStart().Add(2 * time.Minute))
	e.svc.Tick() // still inside the window: resume in a new part
	waitPartData(t, e.st, r.ID, "part-002.ts")
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecReady)
	if got := e.conv.parts[0]; len(got) != 2 {
		t.Fatalf("parts after restart %v", got)
	}
}

func TestRestartAfterWindowConvertsWhatWasCaptured(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(10*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	time.Sleep(10 * time.Millisecond)
	e.svc.Shutdown()

	e.svc = e.newService()
	t.Cleanup(e.svc.Shutdown)
	e.clock.Set(r.WindowStop().Add(time.Hour))
	e.svc.Tick()
	if done := waitState(t, e.st, r.ID, store.RecReady); !done.Partial {
		t.Fatalf("interrupted recording not partial: %+v", done)
	}
}

func TestDeleteStopsCaptureAndRemovesFiles(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(10*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	rec := waitState(t, e.st, r.ID, store.RecRecording)
	if err := e.svc.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.RecordingByID(r.ID); err == nil {
		t.Fatal("row survived delete")
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Fatalf("dir survived delete: %v", err)
	}
}

func TestStopEarlyKeepsRecording(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(60*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	time.Sleep(10 * time.Millisecond)
	e.clock.Set(r.WindowStart().Add(10 * time.Minute))
	if err := e.svc.StopNow(r.ID); err != nil {
		t.Fatal(err)
	}
	done := waitState(t, e.st, r.ID, store.RecReady)
	if !done.Stop.Equal(r.WindowStart().Add(10*time.Minute)) || done.PadEndSec != 0 {
		t.Fatalf("stopped row %+v", done)
	}
}

func TestRetentionDeletesOldestUnprotectedWhenLowOnSpace(t *testing.T) {
	e := newEnv(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		start := t0.Add(-time.Duration(10-i) * time.Hour)
		id, _ := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "old",
			Start: start, Stop: start.Add(time.Hour), State: store.RecReady, Dir: filepath.Join(e.dir, "r", string(rune('a'+i))), CreatedAt: t0})
		_ = os.MkdirAll(filepath.Join(e.dir, "r", string(rune('a'+i))), 0o755)
		ids = append(ids, id)
	}
	_ = e.st.SetRecordingProtected(ids[0], true)

	free := int64(0)
	e.svc.deps.MinFreeBytes = 100
	e.svc.deps.FreeBytes = func(string) (int64, error) { return free, nil }
	// Each deletion "frees" 60 bytes: two deletions needed.
	e.svc.onDeleted = func() { free += 60 }
	e.svc.sweep()
	if _, err := e.st.RecordingByID(ids[0]); err != nil {
		t.Fatal("protected recording deleted")
	}
	for _, id := range ids[1:] {
		if _, err := e.st.RecordingByID(id); err == nil {
			t.Fatalf("recording %d kept while low on space", id)
		}
	}
}

// "Record now": the early padding is already in the past when scheduled, and
// can't count as missed.
func TestRecordNowIsNotPartial(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0, t0.Add(10*time.Minute))
	e.svc.Tick()
	rec := waitState(t, e.st, r.ID, store.RecRecording)
	if rec.MissedSec != 0 {
		t.Fatalf("missedSec=%d", rec.MissedSec)
	}
	time.Sleep(10 * time.Millisecond)
	e.clock.Set(t0.Add(2 * time.Minute))
	if err := e.svc.StopNow(r.ID); err != nil {
		t.Fatal(err)
	}
	if done := waitState(t, e.st, r.ID, store.RecReady); done.Partial {
		t.Fatalf("record-now + stop marked partial: %+v", done)
	}
}

// waitPartData waits until a capture part holds at least one TS packet.
func waitPartData(t *testing.T, st *store.Store, id int64, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := st.RecordingByID(id); err == nil && r.Dir != "" {
			if fi, err := os.Stat(filepath.Join(r.Dir, name)); err == nil && fi.Size() >= minPartBytes {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("recording %d: %s never got data", id, name)
}
