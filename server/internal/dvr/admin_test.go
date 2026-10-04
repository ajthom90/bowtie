package dvr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

func TestSchedulePadding(t *testing.T) {
	e := newEnv(t)
	start := t0.Add(time.Hour)

	// No Padding func: the defaults.
	r := e.schedule(t, "5.1", start, start.Add(time.Hour))
	if r.PadStartSec != 60 || r.PadEndSec != 180 {
		t.Fatalf("default padding = %d/%d, want 60/180", r.PadStartSec, r.PadEndSec)
	}

	// Read at schedule time: a change applies to the next recording only.
	pad := [2]time.Duration{2 * time.Minute, 10 * time.Minute}
	e.svc.deps.Padding = func() (time.Duration, time.Duration, error) { return pad[0], pad[1], nil }
	r2 := e.schedule(t, "9.1", start, start.Add(time.Hour))
	if r2.PadStartSec != 120 || r2.PadEndSec != 600 {
		t.Fatalf("custom padding = %d/%d, want 120/600", r2.PadStartSec, r2.PadEndSec)
	}
	if got, _ := e.st.RecordingByID(r2.ID); got.PadStartSec != 120 || got.PadEndSec != 600 {
		t.Fatalf("stored padding = %d/%d", got.PadStartSec, got.PadEndSec)
	}
	pad = [2]time.Duration{0, 0}
	r3 := e.schedule(t, "11.1", start.Add(2*time.Hour), start.Add(3*time.Hour))
	if r3.PadStartSec != 0 || r3.PadEndSec != 0 {
		t.Fatalf("zero padding = %d/%d", r3.PadStartSec, r3.PadEndSec)
	}
	if got, _ := e.st.RecordingByID(r.ID); got.PadStartSec != 60 || got.PadEndSec != 180 {
		t.Fatalf("existing recording changed: %d/%d", got.PadStartSec, got.PadEndSec)
	}

	// A settings read failure falls back to the defaults.
	e.svc.deps.Padding = func() (time.Duration, time.Duration, error) { return 0, 0, errors.New("db gone") }
	r4 := e.schedule(t, "5.1", start.Add(4*time.Hour), start.Add(5*time.Hour))
	if r4.PadStartSec != 60 || r4.PadEndSec != 180 {
		t.Fatalf("fallback padding = %d/%d, want 60/180", r4.PadStartSec, r4.PadEndSec)
	}
}

func TestApplyRulesUsesPadding(t *testing.T) {
	e := newEnv(t)
	e.svc.deps.Padding = func() (time.Duration, time.Duration, error) { return 5 * time.Minute, 30 * time.Minute, nil }
	_ = e.st.UpdateChannel(e.chans["5.1"].ID, true, "e5.1")
	_ = e.st.ReplaceEPG("sd", []store.EPGChannel{{ID: "e5.1", Source: "sd"}}, []store.Program{
		{EPGChannelID: "e5.1", Start: t0.Add(time.Hour), Stop: t0.Add(2 * time.Hour), Title: "Game", ProgramID: "EP1", SeriesID: "SH1"},
	})
	_, _ = e.st.CreateRule(store.RecordingRule{UserID: 1, Title: "Game", SeriesID: "SH1", CreatedAt: t0})
	if n := e.svc.ApplyRules(); n != 1 {
		t.Fatalf("scheduled %d, want 1", n)
	}
	recs, _ := e.st.ListRecordings(store.RecScheduled)
	if len(recs) != 1 || recs[0].PadStartSec != 300 || recs[0].PadEndSec != 1800 {
		t.Fatalf("rule recording %+v", recs)
	}
}

func TestStorage(t *testing.T) {
	e := newEnv(t)
	e.svc.deps.MinFreeBytes = 50 << 30
	e.svc.deps.FreeBytes = func(string) (int64, error) { return 100 << 30, nil }
	e.svc.deps.TotalBytes = func(string) (int64, error) { return 500 << 30, nil }

	mk := func(state, failure string, size int64, files int) {
		t.Helper()
		id, err := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: e.chans["5.1"].ID, ChannelName: "x", Title: "t",
			Start: t0.Add(time.Hour), Stop: t0.Add(2 * time.Hour), State: state, CreatedAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := e.st.RecordingByID(id)
		r.Failure, r.SizeBytes = failure, size
		if files > 0 {
			r.Dir = filepath.Join(e.dir, "rec", string(rune('a'+id)))
			_ = os.MkdirAll(r.Dir, 0o755)
			_ = os.WriteFile(filepath.Join(r.Dir, "part-001.ts"), make([]byte, files), 0o644)
		}
		if err := e.st.UpdateRecording(r); err != nil {
			t.Fatal(err)
		}
	}
	mk(store.RecReady, "", 1000, 0)
	mk(store.RecReady, "", 2000, 0)
	mk(store.RecScheduled, "", 0, 0)
	mk(store.RecWaiting, "", 0, 0)
	mk(store.RecRecording, "", 0, 300)
	mk(store.RecConverting, "", 0, 200)
	mk(store.RecFailed, "noTuner", 0, 0)
	mk(store.RecFailed, "error", 0, 50)
	mk(store.RecFailed, "skipped", 0, 0) // a series-rule skip marker, not a failure

	got, err := e.svc.Storage()
	if err != nil {
		t.Fatal(err)
	}
	want := Storage{
		Dir: e.svc.deps.Dir, UsedBytes: 1000 + 2000 + 300 + 200 + 50,
		FreeBytes: 100 << 30, TotalBytes: 500 << 30, FloorBytes: diskFloor, MinFreeBytes: 50 << 30,
		Recordings: StorageCounts{Ready: 2, Scheduled: 2, Recording: 2, Failed: 2},
	}
	if got != want {
		t.Fatalf("Storage = %+v\nwant      %+v", got, want)
	}

	// usedBytes is cached briefly; counts and free space are live.
	mk(store.RecReady, "", 5000, 0)
	got, _ = e.svc.Storage()
	if got.UsedBytes != want.UsedBytes || got.Recordings.Ready != 3 {
		t.Fatalf("cached Storage = %+v", got)
	}
	e.svc.usedAt = time.Time{}
	if got, _ = e.svc.Storage(); got.UsedBytes != want.UsedBytes+5000 {
		t.Fatalf("after cache expiry usedBytes = %d", got.UsedBytes)
	}

	// Deleting a recording refreshes the figure right away.
	ready, _ := e.st.ListRecordings(store.RecReady)
	for _, r := range ready {
		if r.SizeBytes == 5000 {
			if err := e.svc.Delete(r.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got, _ = e.svc.Storage(); got.UsedBytes != want.UsedBytes {
		t.Fatalf("after delete usedBytes = %d, want %d", got.UsedBytes, want.UsedBytes)
	}
}

func TestDiskSpaceOfRealDir(t *testing.T) {
	dir := t.TempDir()
	free, err := freeBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	total, err := totalBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if total <= 0 || free <= 0 || free > total {
		t.Fatalf("free=%d total=%d", free, total)
	}
}
