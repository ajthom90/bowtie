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

// resolveSource returns the scenario's source and whether it is generated
// (synthetic sources carry no captions); BOWTIE_FAKE_SOURCE (a .ts path,
// e.g. a local capture) overrides it for every scenario.
func resolveSource(t *testing.T, name string) (*tsloop.Source, bool) {
	t.Helper()
	if p := os.Getenv("BOWTIE_FAKE_SOURCE"); p != "" {
		name = p
	}
	path := name
	preset, synthetic := strings.CutPrefix(name, "synthetic-")
	if synthetic {
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
	return src, synthetic
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

// runScenario plays sc and checks its expectations. It returns when the fake's
// channel timeline started (a clip's offset 0), for judging FFmpeg's re-bases
// against the source.
func runScenario(t *testing.T, sc *scenario.Scenario) time.Time {
	src, synthetic := resolveSource(t, sc.Source)
	gate := NewGate()
	count := &CountingRunner{Inner: &GatedRunner{Inner: &stream.FFmpegRunner{Path: ffmpegPath()}, Gate: gate}}
	// After loading the source (a large clip takes seconds), before New
	// starts the fake's channel timeline.
	sourceStart := time.Now()
	// captionsTimed is the caption row (TestOTAClipsWithFaults). Synthetic
	// sources still set NoCaptions, which wins over LiveCaptions.
	liveCaptions := sc.Expect != nil && sc.Expect.CaptionsTimed != nil && *sc.Expect.CaptionsTimed
	h := New(t, Options{
		Fake: hdhrfake.Options{
			TunerCount: 2,
			Channels:   []hdhrfake.Channel{{GuideNumber: sc.Channel, Name: "FAKE " + sc.Name, Source: src, PTSWrapIn: sc.PTSWrapIn}},
		},
		Runner:       count,
		FFmpegPath:   ffmpegPath(),
		HLSListSize:  sc.WindowSegments,
		Production:   sc.Bowtie != nil && sc.Bowtie.Layout == "production",
		LiveCaptions: liveCaptions,
		NoCaptions:   synthetic,
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
	cfg := h.PlayerConfig(t, sc.Channel)
	cfg.AllRenditions = true
	cfg.Probe = FFprobe(ffprobePath())
	startAt := time.Now()
	defer func() {
		ff, err := ffLogSince(startAt, time.Now())
		if err != nil {
			t.Errorf("ffmpeg log: %v", err)
		}
		t.Logf("ff: rebases=%d dupFrames=%d maxDupBurst=%d dupWarnings=%d", len(ff.Rebases), ff.DupFrames, ff.MaxDupBurst, ff.DupWarnings)
		for _, rb := range ff.Rebases {
			t.Logf("rebase at +%v: %s", rb.At.Round(100*time.Millisecond), rb.Line)
		}
	}()
	p, err := testplayer.Start(context.Background(), cfg)
	obs.StartLatency = time.Since(startAt)
	var se *testplayer.StartError
	switch {
	case errors.As(err, &se) && se.Status == http.StatusServiceUnavailable:
		obs.TunersBusy = true
	case err != nil:
		t.Fatalf("start viewer: %v", err)
	default:
		ctx, cancel := context.WithTimeout(context.Background(), sc.Duration)
		defer cancel()
		runStart := time.Now()
		go func() {
			for _, end := range sc.FaultEnds() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(runStart.Add(end))):
					p.Mark()
					t.Logf("coverage window opened at %v (a fault is over)", end)
				}
			}
		}()
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
		if sc.Bowtie != nil && sc.Bowtie.CloseTranscoderInputAt > 0 {
			go func() {
				select {
				case <-ctx.Done():
				case <-time.After(sc.Bowtie.CloseTranscoderInputAt):
					count.CloseCurrentInput()
				}
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
		// A playlist that said #EXT-X-ENDLIST ended playback in a real player.
		obs.SessionSurvived = !r.SessionGone && !r.EndList && len(h.Sessions(t)) > 0
		obs.FfmpegRestarts = count.Starts() - 1
		c := r.Consistency
		obs.RenditionsMonotonic = c.RenditionsMonotonic && r.BackwardJumps == 0
		obs.DiscontinuitiesAligned = c.DiscontinuitiesAligned
		obs.DiscontinuitySequenceOK = c.DiscontinuitySequenceOK
		obs.MaxDiscontinuitySequence = c.MaxDiscontinuitySequence
		obs.PDTMonotonic = c.PDTMonotonic
		obs.PDTAligned = c.PDTAligned
		obs.TimelineExcess = r.TimelineExcess
		obs.MaxAudioHole = r.MaxAudioHole
		obs.MaxAVStartSkew = r.MaxAVStartSkew
		obs.MaxStartAVOffset = r.MaxStartAVOffset
		obs.StartAVRuns, obs.StartAVMeasured = r.StartAVRuns, r.StartAVMeasured
		obs.UnmeasuredStartAV = append([]int64(nil), r.UnmeasuredStartAV...)
		obs.ProbedAudio, obs.ProbedVideo = r.ProbedAudio, r.ProbedVideo
		obs.FetchErrors = r.FetchErrors
		obs.AVPairs = r.AVPairs
		obs.UnpairedCoverage = r.UnpairedAfterFaults()
		obs.MaxTailGapRendition, obs.MaxTailGap = r.MaxTailGap()
		obs.Cues, obs.CuesTimed, obs.CueErrors = r.Cues, r.CuesTimed, r.CueErrors
		obs.UntimedCueWindows = r.UntimedCueWindows()
		obs.Renditions = len(r.Renditions)
		obs.RenditionsCovered = true
		obs.MinSegmentsAfterFault = -1
		for _, name := range r.Renditions {
			if r.RenditionSegments[name] == 0 {
				obs.RenditionsCovered = false
			}
			if n := r.SegmentsSinceMark[name]; obs.MinSegmentsAfterFault < 0 || n < obs.MinSegmentsAfterFault {
				obs.MinSegmentsAfterFault = n
			}
		}
		t.Logf("player: since mark %v, total %v, audioHoleBy=%v fetchErrors=%d (playlist %d, segment %d, probe %d) tailGap=%v start=%v",
			r.SegmentsSinceMark, r.RenditionSegments, r.AudioHoleBy, r.FetchErrors, r.PlaylistErrors, r.SegmentErrors, r.ProbeErrors,
			r.TailGap, obs.StartLatency.Round(100*time.Millisecond))
		for i, w := range r.Windows {
			t.Logf("window %d (%v): segments %v paired %v cues %v timed %v", i, w.End.Sub(w.Start).Round(time.Second), w.Segments, w.Paired, w.Cues, w.CuesTimed)
		}
		for _, u := range obs.UnpairedCoverage {
			t.Logf("coverage: %s", u)
		}
		for _, pr := range c.Problems {
			t.Logf("consistency: %s", pr)
		}
		t.Logf("player: renditions=%v excess=%.2fs audioHole=%v avSkew=%v startAV=%v runs=%d/%d missing=%v over %d pairs probed=%d/%d cues=%d (%d timed, %d mistimed or unmatched)",
			r.Renditions, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.MaxStartAVOffset, r.StartAVMeasured, r.StartAVRuns, r.UnmeasuredStartAV, r.AVPairs, r.ProbedAudio, r.ProbedVideo, r.Cues, r.CuesTimed, r.CueErrors)
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
		var pinned, other []string
		for _, v := range violations {
			if strings.Contains(v, sc.Expect.XFailOn) {
				pinned = append(pinned, v)
			} else {
				other = append(other, v)
			}
		}
		if len(pinned) == 0 {
			t.Fatalf("known gap fixed (%s): no %q violation; remove xfail from %s", sc.Expect.XFail, sc.Expect.XFailOn, sc.Name)
		}
		t.Logf("xfail (%s): %s", sc.Expect.XFail, strings.Join(pinned, "; "))
		for _, v := range other {
			t.Errorf("unexpected (not the known gap): %s", v)
		}
		return sourceStart
	}
	if sc.Expect.Flaky != "" {
		if len(violations) > 0 {
			t.Logf("flaky (%s): %s", sc.Expect.Flaky, strings.Join(violations, "; "))
		}
		return sourceStart
	}
	for _, v := range violations {
		t.Error(v)
	}
	return sourceStart
}

func slowWindow(sc *scenario.Scenario) *scenario.Window {
	if sc.Bowtie == nil {
		return nil
	}
	return sc.Bowtie.SlowConsumer
}
