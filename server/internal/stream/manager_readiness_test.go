package stream

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// readinessWait uses the manager's fake clock and real poll loop. Clock reads
// let tests wait for completed polls without racing the first grace deadline.
type readinessWait struct {
	clock  *fakeClock
	proc   *stubProcess
	cancel context.CancelFunc
	polls  chan struct{}
	done   chan error
	dir    string
}

func newReadinessWait(t *testing.T, layout transcode.Layout, files ...string) *readinessWait {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	w := &readinessWait{clock: newFakeClock(time.Unix(1000, 0)), proc: newStubProcess(), cancel: cancel,
		polls: make(chan struct{}, 1), done: make(chan error, 1), dir: t.TempDir()}
	for _, name := range files {
		w.write(t, name)
	}
	m := &Manager{clock: func() time.Time {
		now := w.clock.Now()
		select {
		case w.polls <- struct{}{}:
		default:
		}
		return now
	}}
	go func() { w.done <- m.waitPlaylist(ctx, w.dir, layout, w.proc) }()
	return w
}

func (w *readinessWait) write(t *testing.T, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.dir, name+".m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *readinessWait) pending(t *testing.T) {
	t.Helper()
	// Three clock reads cover the initial deadline and at least one full poll,
	// even if a notification from an earlier poll was still buffered.
	for i := 0; i < 3; i++ {
		select {
		case err := <-w.done:
			t.Fatalf("readiness returned early: %v", err)
		case <-w.polls:
		case <-time.After(time.Second):
			t.Fatal("readiness stopped polling")
		}
	}
}

func (w *readinessWait) result(t *testing.T) error {
	t.Helper()
	select {
	case err := <-w.done:
		return err
	case <-time.After(time.Second):
		t.Fatal("readiness did not return")
		return nil
	}
}

func readinessLayout() transcode.Layout {
	return transcode.Layout{Rungs: transcode.Ladder(480), Audio: []transcode.AudioTrack{{AC3: true}}, AC3Copy: true, Captions: true}
}

func TestWaitPlaylistWaitsForEveryRendition(t *testing.T) {
	l := readinessLayout()
	l.Audio = append(l.Audio, transcode.AudioTrack{Lang: "spa", AC3: true})
	for _, missing := range l.Renditions() {
		t.Run(missing, func(t *testing.T) {
			var present []string
			for _, name := range l.Renditions() {
				if name != missing {
					present = append(present, name)
				}
			}
			w := newReadinessWait(t, l, present...)
			w.pending(t)
			w.write(t, missing)
			if err := w.result(t); err != nil {
				t.Fatal(err)
			}
			// Captions were never written: they remain optional.
		})
	}
}

func TestWaitPlaylistGraceStartsAtLowestVideo(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	w := newReadinessWait(t, readinessLayout(), "v480", "aac0")
	w.pending(t)
	w.clock.Advance(5 * time.Second)
	w.pending(t) // No lowest video yet: no grace fallback.
	w.write(t, "v360")
	w.pending(t)
	w.clock.Advance(renditionReadyGrace - time.Millisecond)
	w.pending(t)
	w.clock.Advance(time.Millisecond)
	if err := w.result(t); err != nil {
		t.Fatal(err)
	}
	if got := logs.String(); !strings.Contains(got, "missing renditions: ac30") {
		t.Fatalf("missing-rendition log: %s", got)
	}
}

func TestWaitPlaylistOverallTimeoutBoundsGrace(t *testing.T) {
	w := newReadinessWait(t, readinessLayout(), "v480", "aac0")
	w.pending(t)
	w.clock.Advance(playlistTimeout - time.Second)
	w.pending(t)
	w.write(t, "v360")
	w.pending(t)
	w.clock.Advance(time.Second)
	if err := w.result(t); !errors.Is(err, errPlaylistNotReady) || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("want overall timeout during grace, got %v", err)
	}
}

func TestWaitPlaylistCannotSkipLowestVideo(t *testing.T) {
	w := newReadinessWait(t, readinessLayout(), "v480", "aac0", "ac30")
	w.pending(t)
	w.clock.Advance(playlistTimeout)
	if err := w.result(t); !errors.Is(err, errPlaylistNotReady) {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestWaitPlaylistCancellationAndExitDuringGrace(t *testing.T) {
	for _, stop := range []string{"cancel", "exit", "clean exit"} {
		t.Run(stop, func(t *testing.T) {
			w := newReadinessWait(t, readinessLayout(), "v480", "v360", "aac0")
			w.pending(t)
			crash := errors.New("encoder failed")
			switch stop {
			case "cancel":
				w.cancel()
			case "exit":
				w.proc.Crash(crash)
			case "clean exit":
				w.proc.Crash(nil)
			}
			w.clock.Advance(renditionReadyGrace)
			err := w.result(t)
			if stop == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("want canceled, got %v", err)
				}
			} else if !errors.Is(err, errPlaylistNotReady) || (stop == "exit" && !errors.Is(err, crash)) {
				t.Fatalf("want process exit, got %v", err)
			}
		})
	}
}
