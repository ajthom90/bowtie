package scenario

import (
	"context"
	"fmt"
	"sort"
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
	SequenceMonotonic bool
	MaxGap            time.Duration
	SessionSurvived   bool
	FfmpegRestarts    int
	TunersBusy        bool
}

// Check returns one message per unmet expectation (XFail is not consulted).
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
	return v
}
