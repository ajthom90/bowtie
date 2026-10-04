package dvr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/store"
)

type seg = store.Commercial

func c(start, end float64) seg { return seg{Start: start, End: end} }

func TestParseEDL(t *testing.T) {
	in := strings.Join([]string{
		"0.00\t12.51\t0",
		"",
		"300.30   480.48 3", // MythTV commercial-break type
		"600.00\t610.00\t1", // mute: not a commercial
		"700\t710\t2",       // scene marker: not a commercial
		"garbage line",
		"800.5\t900.25", // no type: a cut
		"1000\tabc\t0",  // unparsable
	}, "\n")
	got, err := ParseEDL(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []seg{{Start: 0, End: 12.51}, {Start: 300.30, End: 480.48}, {Start: 800.5, End: 900.25}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got, _ := ParseEDL(strings.NewReader("")); len(got) != 0 {
		t.Fatalf("empty EDL: %v", got)
	}
}

func TestCleanSegments(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []seg
		dur  float64
		want []seg
	}{
		{"none", nil, 100, nil},
		{"sorted and merged", []seg{c(200, 260), c(10, 40), c(38, 60)}, 600, []seg{c(10, 60), c(200, 260)}},
		{"abutting merge", []seg{c(10, 40), c(40.5, 70)}, 600, []seg{c(10, 70)}},
		{"short dropped", []seg{c(10, 14.9), c(100, 105)}, 600, []seg{c(100, 105)}},
		{"short after clamp dropped", []seg{c(97, 130)}, 100, nil},
		{"clamped to duration", []seg{c(80, 130)}, 100, []seg{c(80, 100)}},
		{"negative start clamped", []seg{c(-3, 30)}, 100, []seg{c(0, 30)}},
		{"backwards dropped", []seg{c(50, 20), c(60, 60)}, 100, nil},
		{"unknown duration keeps end", []seg{c(80, 130)}, 0, []seg{c(80, 130)}},
		{"contained", []seg{c(10, 100), c(20, 30)}, 600, []seg{c(10, 100)}},
		{"past the end dropped", []seg{c(120, 180)}, 100, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanSegments(tc.in, tc.dur)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeComskip writes a shell script standing in for comskip: it writes edl
// into --output as <input basename>.edl and exits with code.
func fakeComskip(t *testing.T, edl string, code int, writeEDL bool) (bin, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	edlFile := filepath.Join(dir, "edl")
	if err := os.WriteFile(edlFile, []byte(edl), 0o644); err != nil {
		t.Fatal(err)
	}
	write := ""
	if writeEDL {
		write = `cp "` + edlFile + `" "$out/$(basename "$in" .m3u8).edl"`
	}
	script := `#!/bin/sh
echo "$@" > "` + argsFile + `"
out=""; in=""
for a in "$@"; do
  case "$a" in
    --output=*) out="${a#--output=}" ;;
    --*) ;;
    *) in="$a" ;;
  esac
done
` + write + `
echo "some log output" >&2
exit ` + string(rune('0'+code)) + "\n"
	bin = filepath.Join(dir, "comskip")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestComskipDetectorRunsAndParses(t *testing.T) {
	bin, argsFile := fakeComskip(t, "10.00\t70.00\t0\n300.0\t420.5\t0\n", 0, true)
	dataDir := t.TempDir()
	work := t.TempDir()
	d := ComskipDetector{Path: bin, DataDir: dataDir}
	got, err := d.Detect(context.Background(), "/rec/1/hls/index.m3u8", work)
	if err != nil {
		t.Fatal(err)
	}
	if want := []seg{c(10, 70), c(300, 420.5)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	args, _ := os.ReadFile(argsFile)
	ini := filepath.Join(dataDir, "comskip.ini")
	for _, want := range []string{"--ini=" + ini, "--output=" + work, "/rec/1/hls/index.m3u8"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("args %q lack %q", args, want)
		}
	}
	// The built-in ini is written on first use, and asks for an EDL.
	b, err := os.ReadFile(ini)
	if err != nil || !strings.Contains(string(b), "output_edl=1") {
		t.Fatalf("built-in ini: %q err=%v", b, err)
	}
	// A user's edit to it survives.
	if err := os.WriteFile(ini, []byte("output_edl=1\n; mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Detect(context.Background(), "/rec/1/hls/index.m3u8", work); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ini); !strings.Contains(string(b), "; mine") {
		t.Fatal("built-in ini overwrote the user's edit")
	}
}

func TestComskipDetectorExplicitINI(t *testing.T) {
	bin, argsFile := fakeComskip(t, "", 1, true)
	d := ComskipDetector{Path: bin, INI: "/etc/my-comskip.ini", DataDir: t.TempDir()}
	got, err := d.Detect(context.Background(), "/x/index.m3u8", t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("exit 1 (none found) should succeed empty: %v %v", got, err)
	}
	if args, _ := os.ReadFile(argsFile); !strings.Contains(string(args), "--ini=/etc/my-comskip.ini") {
		t.Fatalf("args %q", args)
	}
	if _, err := os.Stat(filepath.Join(d.DataDir, "comskip.ini")); err == nil {
		t.Fatal("wrote the built-in ini although one was configured")
	}
}

func TestComskipDetectorFailures(t *testing.T) {
	bin, _ := fakeComskip(t, "", 2, false)
	if _, err := (ComskipDetector{Path: bin, DataDir: t.TempDir()}).Detect(context.Background(), "/x/index.m3u8", t.TempDir()); err == nil {
		t.Fatal("exit 2: want an error")
	}
	bin, _ = fakeComskip(t, "", 0, false)
	if _, err := (ComskipDetector{Path: bin, DataDir: t.TempDir()}).Detect(context.Background(), "/x/index.m3u8", t.TempDir()); !errors.Is(err, ErrDetectorSetup) {
		t.Fatalf("no .edl written (ini without output_edl): err=%v, want ErrDetectorSetup", err)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := (ComskipDetector{Path: missing, DataDir: t.TempDir()}).Detect(context.Background(), "/x/index.m3u8", t.TempDir()); !errors.Is(err, ErrDetectorSetup) {
		t.Fatalf("missing binary: err=%v, want ErrDetectorSetup", err)
	}
	if _, err := (ComskipDetector{Path: bin, DataDir: filepath.Join(missing, "dir")}).Detect(context.Background(), "/x/index.m3u8", t.TempDir()); !errors.Is(err, ErrDetectorSetup) {
		t.Fatalf("unwritable ini: err=%v, want ErrDetectorSetup", err)
	}
}

// --- the worker ----------------------------------------------------------------

// fakeDetector returns segs (or err) and records what it was asked; block
// makes it wait until its context ends.
type fakeDetector struct {
	mu      sync.Mutex
	segs    []seg
	err     error
	block   bool
	calls   []string
	started chan string
	ended   chan error
}

func newFakeDetector() *fakeDetector {
	return &fakeDetector{started: make(chan string, 16), ended: make(chan error, 16)}
}

func (d *fakeDetector) Detect(ctx context.Context, playlist, workDir string) ([]seg, error) {
	d.mu.Lock()
	d.calls = append(d.calls, playlist)
	segs, err, block := d.segs, d.err, d.block
	d.mu.Unlock()
	if fi, serr := os.Stat(workDir); serr != nil || !fi.IsDir() {
		return nil, errors.New("work dir missing")
	}
	d.started <- playlist
	if block {
		<-ctx.Done()
		d.ended <- ctx.Err()
		return nil, ctx.Err()
	}
	return segs, err
}

func (d *fakeDetector) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (e *env) withDetector(t *testing.T, d Detector) {
	t.Helper()
	e.svc.Shutdown()
	e.svc = New(Deps{Store: e.st, Source: e.src, Converter: e.conv, Detector: d, Dir: e.dir, Clock: e.clock.Now,
		RetryEvery: 5 * time.Millisecond, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }})
	t.Cleanup(e.svc.Shutdown)
}

// record runs a recording through capture and conversion to ready.
func (e *env) record(t *testing.T, guide string, start time.Time) store.Recording {
	t.Helper()
	r := e.schedule(t, guide, start, start.Add(2*time.Minute))
	e.clock.Set(r.WindowStart())
	e.svc.Tick()
	waitPartData(t, e.st, r.ID, "part-001.ts")
	e.clock.Set(r.WindowStop())
	e.svc.Tick()
	return waitState(t, e.st, r.ID, store.RecReady)
}

func waitDetected(t *testing.T, st *store.Store, id int64) store.Recording {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, err := st.RecordingByID(id)
		if err == nil && r.CommercialsDetected {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("recording %d: detection never stored (err %v)", id, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestDetectionRunsAfterReadyAndStoresCleanSegments(t *testing.T) {
	e := newEnv(t)
	d := newFakeDetector()
	// The fake VOD is 90 s long.
	d.segs = []seg{c(10, 40), c(38, 60), c(1, 3), c(80, 200)}
	e.withDetector(t, d)
	r := e.record(t, "9.1", t0.Add(time.Minute))
	got := waitDetected(t, e.st, r.ID)
	if want := []seg{c(10, 60), c(80, 90)}; !reflect.DeepEqual(got.Commercials, want) {
		t.Fatalf("commercials %v, want %v", got.Commercials, want)
	}
	calls := d.Calls()
	if len(calls) != 1 || calls[0] != filepath.Join(PlaylistDir(got), MasterName) {
		t.Fatalf("detector calls %v", calls)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, detectWorkDir)); !os.IsNotExist(err) {
		t.Fatalf("work dir left behind: %v", err)
	}
}

// A failed run stores nothing (the recording isn't marked "no commercials"):
// it isn't tried again in this process, but a restart tries again.
func TestDetectionFailureStoresNothingAndRetriesAfterRestart(t *testing.T) {
	e := newEnv(t)
	d := newFakeDetector()
	d.err = errors.New("comskip crashed")
	e.withDetector(t, d)
	r := e.record(t, "9.1", t0.Add(time.Minute))
	<-d.started
	e.svc.pokeDetect()
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.st.RecordingByID(r.ID); got.CommercialsDetected {
		t.Fatal("a failed run was stored as detected")
	}
	if n := len(d.Calls()); n != 1 {
		t.Fatalf("detector ran %d times in one process", n)
	}
	d.mu.Lock()
	d.err = nil
	d.mu.Unlock()
	e.withDetector(t, d)
	waitDetected(t, e.st, r.ID)
}

// A setup problem (bad ini, binary that won't start) stops detection until
// restart instead of marking the whole library "no commercials".
func TestDetectionSetupErrorPausesWorker(t *testing.T) {
	e := newEnv(t)
	a := e.readyRow(t, "a", t0.Add(-2*time.Hour))
	b := e.readyRow(t, "b", t0.Add(-time.Hour))
	d := newFakeDetector()
	d.err = fmt.Errorf("%w: exec: no such file", ErrDetectorSetup)
	e.withDetector(t, d)
	<-d.started
	e.svc.pokeDetect()
	time.Sleep(50 * time.Millisecond)
	if n := len(d.Calls()); n != 1 {
		t.Fatalf("detector ran %d times after a setup error", n)
	}
	for _, r := range []store.Recording{a, b} {
		if got, _ := e.st.RecordingByID(r.ID); got.CommercialsDetected {
			t.Fatalf("recording %d marked detected after a setup error", r.ID)
		}
	}
}

// readyRow inserts a ready recording with a VOD on disk, as a server that
// had no Comskip before would have left it.
func (e *env) readyRow(t *testing.T, title string, start time.Time) store.Recording {
	t.Helper()
	id, err := e.st.CreateRecording(store.Recording{UserID: 1, ChannelID: e.chans["9.1"].ID, ChannelName: "9.1 B",
		Title: title, Start: start, Stop: start.Add(time.Hour), State: store.RecScheduled, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.st.RecordingByID(id)
	r.Dir = filepath.Join(e.dir, title)
	r.State, r.DurationSec = store.RecReady, 3600
	if err := os.MkdirAll(PlaylistDir(r), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(PlaylistDir(r), MasterName), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpdateRecording(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDetectionBackfillsNewestFirstAndSkipsOnesThatRan(t *testing.T) {
	e := newEnv(t)
	old := e.readyRow(t, "old", t0.Add(-72*time.Hour))
	newer := e.readyRow(t, "newer", t0.Add(-24*time.Hour))
	ran := e.readyRow(t, "ran", t0.Add(-1*time.Hour))
	if err := e.st.SetRecordingCommercials(ran.ID, []seg{c(100, 200)}); err != nil {
		t.Fatal(err)
	}
	d := newFakeDetector()
	d.segs = []seg{c(600, 700)}
	e.withDetector(t, d)
	waitDetected(t, e.st, old.ID)
	waitDetected(t, e.st, newer.ID)
	calls := d.Calls()
	want := []string{filepath.Join(PlaylistDir(newer), MasterName), filepath.Join(PlaylistDir(old), MasterName)}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}
	if r, _ := e.st.RecordingByID(ran.ID); !reflect.DeepEqual(r.Commercials, []seg{c(100, 200)}) {
		t.Fatalf("re-ran a detected recording: %v", r.Commercials)
	}
}

func TestDeleteDuringDetectionCancelsIt(t *testing.T) {
	e := newEnv(t)
	r := e.readyRow(t, "show", t0.Add(-time.Hour))
	d := newFakeDetector()
	d.block = true
	e.withDetector(t, d)
	select {
	case <-d.started:
	case <-time.After(3 * time.Second):
		t.Fatal("detection never started")
	}
	if err := e.svc.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-d.ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ended with %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("detection kept running after the recording was deleted")
	}
	if _, err := os.Stat(r.Dir); !os.IsNotExist(err) {
		t.Fatalf("recording folder left: %v", err)
	}
}

func TestShutdownDuringDetectionLeavesItForNextStart(t *testing.T) {
	e := newEnv(t)
	r := e.readyRow(t, "show", t0.Add(-time.Hour))
	d := newFakeDetector()
	d.block = true
	e.withDetector(t, d)
	select {
	case <-d.started:
	case <-time.After(3 * time.Second):
		t.Fatal("detection never started")
	}
	e.svc.Shutdown()
	select {
	case <-d.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("detection kept running after shutdown")
	}
	if got, _ := e.st.RecordingByID(r.ID); got.CommercialsDetected {
		t.Fatal("an interrupted detection was stored as done")
	}
}

func TestNoDetectorNoDetection(t *testing.T) {
	e := newEnv(t)
	r := e.record(t, "9.1", t0.Add(time.Minute))
	time.Sleep(20 * time.Millisecond)
	if got, _ := e.st.RecordingByID(r.ID); got.CommercialsDetected {
		t.Fatal("detection ran without a detector")
	}
}

// Redetect runs detection again (say after editing comskip.ini) and stores
// the new breaks.
func TestRedetectRunsAgain(t *testing.T) {
	e := newEnv(t)
	r := e.readyRow(t, "show", t0.Add(-time.Hour))
	d := newFakeDetector()
	d.segs = []seg{c(100, 200)}
	e.withDetector(t, d)
	waitDetected(t, e.st, r.ID)

	d.mu.Lock()
	d.segs = []seg{c(300, 400)}
	d.mu.Unlock()
	if err := e.svc.Redetect(r.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := e.st.RecordingByID(r.ID)
		if reflect.DeepEqual(got.Commercials, []seg{c(300, 400)}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("commercials %v after redetect", got.Commercials)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRedetectRefusals(t *testing.T) {
	e := newEnv(t)
	sched := e.schedule(t, "9.1", t0.Add(time.Hour), t0.Add(2*time.Hour))
	if err := e.svc.Redetect(sched.ID); !errors.Is(err, ErrDetectionOff) {
		t.Fatalf("no detector: err=%v, want ErrDetectionOff", err)
	}
	e.withDetector(t, newFakeDetector())
	if err := e.svc.Redetect(sched.ID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("scheduled recording: err=%v, want ErrNotReady", err)
	}
}

// Comskip exits 1 when it finds no commercials; if it also skips writing an
// .edl that's still "none found", not a setup problem.
func TestComskipNoneFoundWithoutEDL(t *testing.T) {
	bin, _ := fakeComskip(t, "", 1, false)
	got, err := (ComskipDetector{Path: bin, DataDir: t.TempDir()}).Detect(context.Background(), "/x/index.m3u8", t.TempDir())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want none and no error", got, err)
	}
}

// "Find ads again" while a scan is running restarts it (the admin probably
// just edited comskip.ini), and the new run's result is what's kept.
func TestRedetectDuringARunRestartsIt(t *testing.T) {
	e := newEnv(t)
	r := e.readyRow(t, "show", t0.Add(-time.Hour))
	d := newFakeDetector()
	d.block = true
	e.withDetector(t, d)
	<-d.started
	d.mu.Lock()
	d.block, d.segs = false, []seg{c(300, 400)}
	d.mu.Unlock()
	if err := e.svc.Redetect(r.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, _ := e.st.RecordingByID(r.ID)
		if reflect.DeepEqual(got.Commercials, []seg{c(300, 400)}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("commercials %v; the restart didn't happen", got.Commercials)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
