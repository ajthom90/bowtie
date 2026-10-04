package dvr

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

// C1: a relative recordings dir (the default ./data/recordings) still gives
// the converter absolute part paths (FFmpeg's concat list resolves relative
// entries against the list file's own directory).
func TestPartsAreAbsoluteWithRelativeDir(t *testing.T) {
	e := newEnv(t)
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, e.dir)
	if err != nil {
		t.Skip("temp dir not relative to cwd")
	}
	e.svc.Shutdown()
	e.svc = New(Deps{Store: e.st, Source: e.src, Converter: e.conv, Dir: rel, Clock: e.clock.Now,
		RetryEvery: 5 * time.Millisecond, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }})
	t.Cleanup(e.svc.Shutdown)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecRecording)
	time.Sleep(20 * time.Millisecond)
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	waitState(t, e.st, r.ID, store.RecReady)
	for _, p := range e.conv.parts[0] {
		if !filepath.IsAbs(p) {
			t.Fatalf("relative part %q", p)
		}
	}
}

// instantEOF hands out readers that end immediately (a stream that closes at
// once, or a disk that won't take writes).
type instantEOF struct{ opens atomic.Int64 }

func (s *instantEOF) Open(context.Context, store.Channel) (io.ReadCloser, error) {
	s.opens.Add(1)
	return io.NopCloser(&emptyReader{}), nil
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// C2: a stream that ends at once is retried at RetryEvery, not in a hot loop,
// and leaves no empty parts behind.
func TestInstantEOFBacksOff(t *testing.T) {
	e := newEnv(t)
	src := &instantEOF{}
	e.svc.Shutdown()
	e.svc = New(Deps{Store: e.st, Source: src, Converter: e.conv, Dir: e.dir, Clock: e.clock.Now,
		RetryEvery: 40 * time.Millisecond, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }})
	t.Cleanup(e.svc.Shutdown)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(3*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	time.Sleep(200 * time.Millisecond)
	if n := src.opens.Load(); n > 8 {
		t.Fatalf("hot loop: %d opens in 200ms with a 40ms retry", n)
	}
	rec, _ := e.st.RecordingByID(r.ID)
	if parts := partFiles(rec.Dir); len(parts) != 0 {
		t.Fatalf("empty parts kept: %v", parts)
	}
}

// C2: parts sort numerically past 999.
func TestPartFilesSortNumerically(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"part-999.ts", "part-1000.ts", "part-101.ts"} {
		_ = os.WriteFile(filepath.Join(dir, n), make([]byte, 376), 0o644)
	}
	got := partFiles(dir)
	want := []string{"part-101.ts", "part-999.ts", "part-1000.ts"}
	for i := range want {
		if filepath.Base(got[i]) != want[i] {
			t.Fatalf("order %v", got)
		}
	}
}

// I1: Delete while Tick keeps running never leaves a capture behind.
func TestDeleteDuringTicksLeavesNoCapture(t *testing.T) {
	e := newEnv(t)
	r := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(30*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	rec := waitState(t, e.st, r.ID, store.RecRecording)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				e.svc.Tick()
			}
		}
	}()
	if err := e.svc.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	close(stop)
	wg.Wait()
	e.svc.mu.Lock()
	n := len(e.svc.captures)
	e.svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d captures left after delete", n)
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Fatalf("dir recreated after delete: %v", err)
	}
}

// I2: a converting row whose parts are gone but whose VOD exists becomes
// ready instead of failing (and its VOD is kept).
func TestConvertingWithVODButNoPartsBecomesReady(t *testing.T) {
	e := newEnv(t)
	id, _ := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "t",
		Start: t0, Stop: t0.Add(time.Hour), State: store.RecScheduled, CreatedAt: t0})
	r, _ := e.st.RecordingByID(id)
	r.Dir = filepath.Join(e.dir, "1-t")
	_ = os.MkdirAll(filepath.Join(r.Dir, hlsDir), 0o755)
	_ = os.WriteFile(filepath.Join(r.Dir, hlsDir, "v720.m3u8"), []byte("#EXTM3U\n#EXTINF:6.0,\nv720_00000.ts\n#EXT-X-ENDLIST\n"), 0o644)
	_ = os.WriteFile(filepath.Join(r.Dir, hlsDir, MasterName), []byte("#EXTM3U\n"), 0o644)
	r.State = store.RecConverting
	_ = e.st.UpdateRecording(r)
	e.svc.Tick()
	done := waitState(t, e.st, id, store.RecReady)
	if _, err := os.Stat(filepath.Join(done.Dir, hlsDir, MasterName)); err != nil {
		t.Fatalf("VOD deleted: %v", err)
	}
}

// I3: Stop only acts on pending recordings, and never clobbers other fields.
func TestStopNowOnlyForPendingAndKeepsFields(t *testing.T) {
	e := newEnv(t)
	id, _ := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "t",
		Start: t0, Stop: t0.Add(time.Hour), State: store.RecScheduled, CreatedAt: t0})
	r, _ := e.st.RecordingByID(id)
	r.State, r.Dir = store.RecReady, "/rec/1"
	_ = e.st.UpdateRecording(r)
	if err := e.svc.StopNow(id); !errors.Is(err, ErrNotStoppable) {
		t.Fatalf("stop on ready: %v", err)
	}
	// Protect isn't lost when the DVR rewrites the row.
	r2 := e.schedule(t, "9.1", t0.Add(time.Minute), t0.Add(10*time.Minute))
	e.clock.Set(r2.WindowStart())
	e.svc.Tick()
	waitState(t, e.st, r2.ID, store.RecRecording)
	_ = e.st.SetRecordingProtected(r2.ID, true)
	time.Sleep(10 * time.Millisecond)
	e.clock.Set(r2.WindowStart().Add(2 * time.Minute))
	if err := e.svc.StopNow(r2.ID); err != nil {
		t.Fatal(err)
	}
	if done := waitState(t, e.st, r2.ID, store.RecReady); !done.Protected || done.Dir == "" {
		t.Fatalf("fields lost: %+v", done)
	}
}

// I4: conflicts count the peak at one moment, not every channel in the window.
func TestConflictCountsPeakNotWindow(t *testing.T) {
	e := newEnv(t)
	at := func(m int) time.Time { return t0.Add(time.Duration(60+m) * time.Minute) }
	_, _, _ = e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["5.1"], Title: "A", Start: at(0), Stop: at(30)})
	_, _, _ = e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["9.1"], Title: "B", Start: at(30), Stop: at(60)})
	if _, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["11.1"], Title: "C", Start: at(0), Stop: at(60)}); err != nil {
		t.Fatalf("two at a time fits two tuners: %v", err)
	}
	_, _, err := e.svc.Schedule(ScheduleRequest{UserID: 1, Channel: e.chans["5.1"], Title: "D", Start: at(10), Stop: at(20)})
	if err != nil {
		t.Fatalf("same channel as a running one shares its tuner: %v", err)
	}
}

// M3: scheduling the same channel and start twice returns the existing one.
func TestScheduleTwiceReturnsExisting(t *testing.T) {
	e := newEnv(t)
	a := e.schedule(t, "9.1", t0.Add(time.Hour), t0.Add(2*time.Hour))
	b := e.schedule(t, "9.1", t0.Add(time.Hour), t0.Add(2*time.Hour))
	if a.ID != b.ID {
		t.Fatalf("duplicate rows %d %d", a.ID, b.ID)
	}
}

// I5: the sweep never deletes a recording someone is watching, and stops
// when deleting doesn't free space.
func TestSweepSkipsRecordingInUseAndStopsWhenNotFreeing(t *testing.T) {
	e := newEnv(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		st := t0.Add(-time.Duration(10-i) * time.Hour)
		dir := filepath.Join(e.dir, "s", string(rune('a'+i)))
		_ = os.MkdirAll(dir, 0o755)
		id, _ := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: 1, ChannelName: "x", Title: "old",
			Start: st, Stop: st.Add(time.Hour), State: store.RecScheduled, CreatedAt: t0})
		r, _ := e.st.RecordingByID(id)
		r.State, r.Dir = store.RecReady, dir
		_ = e.st.UpdateRecording(r)
		ids = append(ids, id)
	}
	_ = e.st.SetRecordingPosition(ids[0], 1, 30) // being watched now
	e.svc.deps.MinFreeBytes = 100
	e.svc.deps.FreeBytes = func(string) (int64, error) { return 0, nil } // deleting never helps
	e.svc.sweep()
	if _, err := e.st.RecordingByID(ids[0]); err != nil {
		t.Fatal("deleted a recording being watched")
	}
	left := 0
	for _, id := range ids[1:] {
		if _, err := e.st.RecordingByID(id); err == nil {
			left++
		}
	}
	if left != 1 {
		t.Fatalf("sweep kept deleting although space didn't come back (left %d of 2)", left)
	}
}
