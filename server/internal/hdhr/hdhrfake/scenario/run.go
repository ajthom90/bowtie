package scenario

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/faults"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
)

// Applier starts faults; *hdhrfake.Fake implements it.
type Applier interface {
	Apply(faults.Target, faults.Spec) (string, error)
}

// Run fires each step at its offset and returns when Duration has elapsed.
// Timed faults expire on their own (the engine honors Spec.For).
func (s *Scenario) Run(ctx context.Context, a Applier, clock tsloop.Clock) error {
	steps := append([]Step(nil), s.Timeline...)
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At < steps[j].At })
	start := clock.Now()
	wait := func(until time.Duration) error {
		if d := start.Add(until).Sub(clock.Now()); d > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-clock.After(d):
			}
		}
		return ctx.Err()
	}
	for _, st := range steps {
		if err := wait(st.At); err != nil {
			return err
		}
		target := faults.Target{Channel: s.Channel, Scope: st.Scope}
		if st.Fault.IsDevice() {
			target = faults.Target{Scope: faults.ScopeDevice}
		}
		if _, err := a.Apply(target, st.Spec); err != nil {
			return fmt.Errorf("scenario %s: at %v: %w", s.Name, st.At, err)
		}
	}
	return wait(s.Duration)
}

// Observed is what a run measured from the viewer's side.
type Observed struct {
	SequenceMonotonic          bool
	MaxGap                     time.Duration
	SessionSurvived            bool
	FfmpegRestarts             int
	TunersBusy                 bool
	RenditionsMonotonic        bool
	DiscontinuitiesAligned     bool
	DiscontinuitySequenceOK    bool
	MaxDiscontinuitySequence   int64
	PDTMonotonic               bool
	PDTAligned                 bool
	TimelineExcess             float64 // seconds
	MaxAudioHole               time.Duration
	MaxAVStartSkew             time.Duration
	MaxStartAVOffset           time.Duration // |first audio PTS − first video PTS|, max over runs
	StartAVRuns                int           // discontinuities seen on video or the chosen audio
	StartAVMeasured            int           // those with both sides probed
	UnmeasuredStartAV          []int64       // discontinuity numbers with no start pair, ascending
	ProbedAudio, ProbedVideo   int
	MinSegmentsAfterFault      int // fewest segments any rendition fetched after the last fault
	FetchErrors                int // testplayer Report.FetchErrors: every failed fetch
	AVPairs                    int
	UnpairedCoverage           []string // testplayer Report.UnpairedAfterFaults
	MaxTailGap                 time.Duration
	MaxTailGapRendition        string
	Cues, CuesTimed, CueErrors int
	UntimedCueWindows          []string
	RenditionsCovered          bool
	Renditions                 int
	StartLatency               time.Duration
}

// Check returns one message per unmet expectation (XFail and Flaky are not consulted).
func (e *Expect) Check(o Observed) []string {
	var v []string
	if e.SequenceMonotonic != nil && *e.SequenceMonotonic != o.SequenceMonotonic {
		v = append(v, fmt.Sprintf("sequence monotonic = %v, want %v", o.SequenceMonotonic, *e.SequenceMonotonic))
	}
	if e.MaxGapSeconds != nil && o.MaxGap.Seconds() > *e.MaxGapSeconds {
		v = append(v, fmt.Sprintf("max gap without a new segment = %.1fs, want ≤ %.1fs", o.MaxGap.Seconds(), *e.MaxGapSeconds))
	}
	if e.SessionSurvives != nil && *e.SessionSurvives != o.SessionSurvived {
		v = append(v, fmt.Sprintf("session survived = %v, want %v", o.SessionSurvived, *e.SessionSurvives))
	}
	if e.MaxFfmpegRestarts != nil && o.FfmpegRestarts > *e.MaxFfmpegRestarts {
		v = append(v, fmt.Sprintf("ffmpeg restarts = %d, want ≤ %d", o.FfmpegRestarts, *e.MaxFfmpegRestarts))
	}
	if e.TunersBusy != nil && *e.TunersBusy != o.TunersBusy {
		v = append(v, fmt.Sprintf("tuners busy = %v, want %v", o.TunersBusy, *e.TunersBusy))
	}
	flag := func(want *bool, got bool, what string) {
		if want != nil && *want != got {
			v = append(v, fmt.Sprintf("%s = %v, want %v", what, got, *want))
		}
	}
	flag(e.RenditionsMonotonic, o.RenditionsMonotonic, "renditions monotonic")
	flag(e.DiscontinuitiesAligned, o.DiscontinuitiesAligned, "discontinuities aligned")
	flag(e.DiscontinuitySequenceOK, o.DiscontinuitySequenceOK, "discontinuity sequence ok")
	flag(e.PDTMonotonic, o.PDTMonotonic, "PDT monotonic")
	flag(e.PDTAligned, o.PDTAligned, "PDT aligned")
	if e.MinDiscontinuitySequence != nil && o.MaxDiscontinuitySequence < *e.MinDiscontinuitySequence {
		v = append(v, fmt.Sprintf("discontinuity sequence reached %d, want ≥ %d", o.MaxDiscontinuitySequence, *e.MinDiscontinuitySequence))
	}
	if e.MaxTimelineExcessSeconds != nil && o.TimelineExcess > *e.MaxTimelineExcessSeconds {
		v = append(v, fmt.Sprintf("timeline excess = %.1fs, want ≤ %.1fs", o.TimelineExcess, *e.MaxTimelineExcessSeconds))
	}
	if e.MinTimelineExcessSeconds != nil && o.TimelineExcess < *e.MinTimelineExcessSeconds {
		v = append(v, fmt.Sprintf("timeline excess = %.1fs, want ≥ %.1fs", o.TimelineExcess, *e.MinTimelineExcessSeconds))
	}
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	if e.MaxAudioHoleMs != nil {
		switch {
		case o.ProbedAudio == 0:
			v = append(v, "no audio segments probed")
		case ms(o.MaxAudioHole) > *e.MaxAudioHoleMs:
			v = append(v, fmt.Sprintf("audio hole = %.0fms, want ≤ %.0fms", ms(o.MaxAudioHole), *e.MaxAudioHoleMs))
		}
	}
	if e.MaxAVStartSkewMs != nil {
		switch {
		case o.AVPairs == 0:
			v = append(v, "no audio/video segment pairs probed")
		case ms(o.MaxAVStartSkew) > *e.MaxAVStartSkewMs:
			v = append(v, fmt.Sprintf("A/V start skew = %.0fms, want ≤ %.0fms", ms(o.MaxAVStartSkew), *e.MaxAVStartSkewMs))
		}
	}
	if e.MaxStartAVOffsetMs != nil {
		if o.ProbedAudio == 0 || o.ProbedVideo == 0 {
			v = append(v, "no audio/video start offset measured")
		}
		for _, d := range o.UnmeasuredStartAV {
			v = append(v, fmt.Sprintf("start A/V offset not measured for discontinuity %d", d))
		}
		if ms(o.MaxStartAVOffset) > *e.MaxStartAVOffsetMs {
			v = append(v, fmt.Sprintf("A/V start offset = %.0fms, want ≤ %.0fms", ms(o.MaxStartAVOffset), *e.MaxStartAVOffsetMs))
		}
	}
	if e.MinFfmpegRestarts != nil && o.FfmpegRestarts < *e.MinFfmpegRestarts {
		v = append(v, fmt.Sprintf("ffmpeg restarts = %d, want ≥ %d", o.FfmpegRestarts, *e.MinFfmpegRestarts))
	}
	if e.MinSegmentsAfterFault != nil && o.MinSegmentsAfterFault < *e.MinSegmentsAfterFault {
		v = append(v, fmt.Sprintf("segments after the last fault = %d in the slowest rendition, want ≥ %d", o.MinSegmentsAfterFault, *e.MinSegmentsAfterFault))
	}
	if e.PairedAfterEachFault != nil && *e.PairedAfterEachFault && len(o.UnpairedCoverage) > 0 {
		v = append(v, fmt.Sprintf("A/V coverage after faults: %s", strings.Join(firstN(o.UnpairedCoverage, 3), "; ")))
	}
	if e.MaxTailGapSeconds != nil && o.MaxTailGap.Seconds() > *e.MaxTailGapSeconds {
		v = append(v, fmt.Sprintf("%s stopped %.1fs before the end, want ≤ %.1fs", o.MaxTailGapRendition, o.MaxTailGap.Seconds(), *e.MaxTailGapSeconds))
	}
	if e.CaptionsTimed != nil && *e.CaptionsTimed && (o.CuesTimed == 0 || o.CueErrors > 0 || len(o.UntimedCueWindows) > 0) {
		v = append(v, fmt.Sprintf("cues = %d, timed = %d, mistimed or unmatched = %d, windows without timed cues: %s",
			o.Cues, o.CuesTimed, o.CueErrors, strings.Join(firstN(o.UntimedCueWindows, 3), "; ")))
	}
	if e.NoFetchErrors != nil && *e.NoFetchErrors && (o.FetchErrors > 0 || !o.RenditionsCovered) {
		v = append(v, fmt.Sprintf("fetch errors = %d, every rendition delivered = %v", o.FetchErrors, o.RenditionsCovered))
	}
	if e.MinRenditions != nil && o.Renditions < *e.MinRenditions {
		v = append(v, fmt.Sprintf("renditions followed = %d, want ≥ %d", o.Renditions, *e.MinRenditions))
	}
	if e.MaxStartSeconds != nil && o.StartLatency.Seconds() > *e.MaxStartSeconds {
		v = append(v, fmt.Sprintf("start latency = %.1fs, want ≤ %.1fs", o.StartLatency.Seconds(), *e.MaxStartSeconds))
	}
	return v
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
