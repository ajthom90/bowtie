//go:build ffmpeg

package e2e

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/scenario"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/synth"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

// synthLoop is the length of generated sources (the fake loops them).
const synthLoop = 10 * time.Second

func ffmpegPath() string {
	if p := os.Getenv("BOWTIE_FFMPEG_PATH"); p != "" {
		return p
	}
	return "ffmpeg"
}

var sourceMu sync.Mutex

// resolveSource returns the scenario's source; BOWTIE_FAKE_SOURCE (a .ts
// path, e.g. a local capture) overrides it for every scenario.
func resolveSource(t *testing.T, name string) *tsloop.Source {
	t.Helper()
	if p := os.Getenv("BOWTIE_FAKE_SOURCE"); p != "" {
		name = p
	}
	path := name
	if preset, ok := strings.CutPrefix(name, "synthetic-"); ok {
		dir := filepath.Join(os.TempDir(), "bowtie-fakehdhr")
		if c, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(c, "bowtie-fakehdhr")
		}
		sourceMu.Lock()
		var err error
		path, err = synth.Cached(context.Background(), ffmpegPath(), synth.Preset(preset), synthLoop, dir)
		sourceMu.Unlock()
		if err != nil {
			t.Fatalf("generate %s: %v", name, err)
		}
	}
	src, err := tsloop.LoadFile(path)
	if err != nil {
		t.Fatalf("load source %s: %v", path, err)
	}
	return src
}

func TestScenarios(t *testing.T) {
	files, err := filepath.Glob("testdata/scenarios/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	for _, f := range files {
		sc, err := scenario.Load(f)
		if err != nil {
			t.Fatal(err)
		}
		if sc.Expect == nil {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			if sc.Soak && !soakEnabled {
				t.Skip("soak scenario (build with -tags ffmpeg,soak)")
			}
			t.Parallel()
			runScenario(t, sc)
		})
	}
}

func runScenario(t *testing.T, sc *scenario.Scenario) {
	src := resolveSource(t, sc.Source)
	gate := NewGate()
	count := &CountingRunner{Inner: &GatedRunner{Inner: &stream.FFmpegRunner{Path: ffmpegPath()}, Gate: gate}}
	h := New(t, Options{
		Fake: hdhrfake.Options{
			TunerCount: 2,
			Channels:   []hdhrfake.Channel{{GuideNumber: sc.Channel, Name: "FAKE " + sc.Name, Source: src, PTSWrapIn: sc.PTSWrapIn}},
		},
		Runner:     count,
		FFmpegPath: ffmpegPath(),
	})

	// Steps at 0s happen before the viewer tunes in (e.g. a busy device);
	// the rest are timed from the moment playback starts.
	pre, rest := *sc, *sc
	pre.Timeline, rest.Timeline = nil, nil
	for _, st := range sc.Timeline {
		if st.At == 0 {
			pre.Timeline = append(pre.Timeline, st)
		} else {
			rest.Timeline = append(rest.Timeline, st)
		}
	}
	pre.Duration = 0
	if err := pre.Run(context.Background(), h.Fake, tsloop.RealClock()); err != nil {
		t.Fatal(err)
	}

	var obs scenario.Observed
	p, err := h.Player(t, sc.Channel)
	var se *testplayer.StartError
	switch {
	case errors.As(err, &se) && se.Status == http.StatusServiceUnavailable:
		obs.TunersBusy = true
	case err != nil:
		t.Fatalf("start viewer: %v", err)
	default:
		ctx, cancel := context.WithTimeout(context.Background(), sc.Duration)
		defer cancel()
		if w := slowWindow(sc); w != nil {
			go func() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(w.At):
				}
				gate.Pause()
				t.Logf("slow consumer: paused at %v for %v", w.At, w.For)
				select {
				case <-ctx.Done():
				case <-time.After(w.For):
				}
				gate.Resume()
			}()
		}
		if sc.Bowtie != nil && sc.Bowtie.KillTranscoderAt > 0 {
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(sc.Bowtie.KillTranscoderAt):
					count.KillCurrent()
				}
			}()
		}
		go func() {
			if err := rest.Run(ctx, h.Fake, tsloop.RealClock()); err != nil && ctx.Err() == nil {
				t.Errorf("scenario run: %v", err)
			}
		}()
		p.Run(ctx)
		gate.Resume()
		r := p.Report()
		obs.SequenceMonotonic = r.BackwardJumps == 0
		obs.MaxGap = r.MaxGap
		obs.SessionSurvived = !r.SessionGone && len(h.Sessions(t)) > 0
		obs.FfmpegRestarts = count.Starts() - 1
		t.Logf("player: polls=%d segments=%d segErrors=%d backward=%d discontinuities=%d maxGap=%v gone=%v newest=%v errors=%v",
			r.Polls, r.SegmentsFetched, r.SegmentErrors, r.BackwardJumps, r.Discontinuities, r.MaxGap.Round(100*time.Millisecond), r.SessionGone, r.Newest, r.Errors)
		p.Stop(context.Background())
	}
	t.Logf("observed: %+v", obs)
	for _, e := range h.Fake.Events() {
		t.Logf("fake: %s %s %s %s %s", e.At.Format("15:04:05.000"), e.Kind, e.Channel, e.Conn, e.Detail)
	}

	for _, l := range count.Log() {
		t.Logf("transcoder: %s", l)
	}
	for _, d := range h.DialLog() {
		t.Logf("ingest: %s", d)
	}
	violations := sc.Expect.Check(obs)
	if sc.Expect.XFail != "" {
		if len(violations) == 0 {
			t.Fatalf("known gap fixed (%s): remove xfail from %s", sc.Expect.XFail, sc.Name)
		}
		t.Logf("xfail (%s): %s", sc.Expect.XFail, strings.Join(violations, "; "))
		return
	}
	if sc.Expect.Flaky != "" {
		if len(violations) > 0 {
			t.Logf("flaky (%s): %s", sc.Expect.Flaky, strings.Join(violations, "; "))
		}
		return
	}
	for _, v := range violations {
		t.Error(v)
	}
}

func slowWindow(sc *scenario.Scenario) *scenario.Window {
	if sc.Bowtie == nil {
		return nil
	}
	return sc.Bowtie.SlowConsumer
}
