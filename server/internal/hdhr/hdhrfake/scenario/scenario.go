// Package scenario loads YAML fault timelines for the fake HDHomeRun and checks
// a run's observed viewer experience against the file's expectations. A
// scenario file with an expect block is a regression test.
package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/faults"
)

// Scenario is one fault timeline plus optional Bowtie-side actions and
// expectations.
type Scenario struct {
	Name      string        `yaml:"name"`
	Source    string        `yaml:"source"`  // synthetic-480i (default) | synthetic-720p | synthetic-1080i | file path
	Channel   string        `yaml:"channel"` // default "90.1"
	Duration  time.Duration `yaml:"duration"`
	Soak      bool          `yaml:"soak"`
	PTSWrapIn time.Duration `yaml:"ptsWrapIn"` // > 0: start the channel clock this long before the 33-bit wrap
	Timeline  []Step        `yaml:"timeline"`
	Bowtie    *BowtieSpec   `yaml:"bowtie"`
	Expect    *Expect       `yaml:"expect"`
}

// Step fires a fault At after the scenario starts.
type Step struct {
	At          time.Duration `yaml:"at"`
	Scope       faults.Scope  `yaml:"scope"`
	faults.Spec `yaml:",inline"`
}

// BowtieSpec holds actions taken on Bowtie's side of the pipe.
type BowtieSpec struct {
	// SlowConsumer pauses FFmpeg's stdin reads for the window.
	SlowConsumer *Window `yaml:"slowConsumer"`
	// KillTranscoderAt stops the running FFmpeg at this point in the scenario,
	// so Bowtie restarts it into the same session.
	KillTranscoderAt time.Duration `yaml:"killTranscoderAt"`
}

// Window is a span of scenario time.
type Window struct {
	At  time.Duration `yaml:"at"`
	For time.Duration `yaml:"for"`
}

// Expect lists what a viewer should experience. Nil fields are not checked.
type Expect struct {
	SequenceMonotonic *bool    `yaml:"sequenceMonotonic"`
	MaxGapSeconds     *float64 `yaml:"maxGapSeconds"`
	SessionSurvives   *bool    `yaml:"sessionSurvives"`
	MaxFfmpegRestarts *int     `yaml:"maxFfmpegRestarts"`
	TunersBusy        *bool    `yaml:"tunersBusy"`
	// XFail marks expectations known to fail today; the test then requires
	// them to still fail, so a fix forces removing the marker.
	XFail string `yaml:"xfail"`
	// Flaky marks expectations that fail intermittently because of a known
	// gap; violations are logged but never fail the test, in either direction.
	Flaky string `yaml:"flaky"`
}

// Load reads and parses a scenario file.
func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Parse decodes and validates a scenario; unknown fields are errors.
func Parse(b []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var s Scenario
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	if s.Channel == "" {
		s.Channel = "90.1"
	}
	if s.Source == "" {
		s.Source = "synthetic-480i"
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Scenario) validate() error {
	if s.Name == "" {
		return errors.New("scenario: name is required")
	}
	if s.Duration <= 0 {
		return fmt.Errorf("scenario %s: duration must be > 0", s.Name)
	}
	for i, st := range s.Timeline {
		if st.At < 0 || st.At > s.Duration {
			return fmt.Errorf("scenario %s: step %d at %v outside 0..%v", s.Name, i, st.At, s.Duration)
		}
		switch st.Scope {
		case "", faults.ScopeChannel, faults.ScopeConnections, faults.ScopeDevice:
		default:
			return fmt.Errorf("scenario %s: step %d: unknown scope %q", s.Name, i, st.Scope)
		}
		if err := st.Validate(); err != nil {
			return fmt.Errorf("scenario %s: step %d: %w", s.Name, i, err)
		}
		for j := 0; j < i; j++ {
			if overlaps(s.Timeline[j], st, s.Duration) {
				return fmt.Errorf("scenario %s: steps %d and %d: overlapping %s", s.Name, j, i, st.Fault)
			}
		}
	}
	if s.Bowtie != nil && s.Bowtie.KillTranscoderAt != 0 &&
		(s.Bowtie.KillTranscoderAt < 0 || s.Bowtie.KillTranscoderAt >= s.Duration) {
		return fmt.Errorf("scenario %s: killTranscoderAt %v outside 0..%v", s.Name, s.Bowtie.KillTranscoderAt, s.Duration)
	}
	if s.Bowtie != nil && s.Bowtie.SlowConsumer != nil {
		w := s.Bowtie.SlowConsumer
		if w.At < 0 || w.For <= 0 || w.At+w.For > s.Duration {
			return fmt.Errorf("scenario %s: slowConsumer window %v+%v outside 0..%v", s.Name, w.At, w.For, s.Duration)
		}
	}
	if e := s.Expect; e != nil {
		if e.XFail != "" && e.Flaky != "" {
			return fmt.Errorf("scenario %s: xfail and flaky are mutually exclusive", s.Name)
		}
		if (e.XFail != "" || e.Flaky != "") && e.empty() {
			return fmt.Errorf("scenario %s: xfail/flaky needs at least one expectation", s.Name)
		}
	}
	return nil
}

// overlaps reports same-kind, same-scope steps whose active windows intersect.
// One-shot kinds (drop and timeline faults) never overlap.
func overlaps(a, b Step, end time.Duration) bool {
	if a.Fault != b.Fault || a.Scope != b.Scope || a.Fault == faults.Drop || a.Fault.IsTimeline() {
		return false
	}
	window := func(s Step) (time.Duration, time.Duration) {
		if s.For == 0 {
			return s.At, end
		}
		return s.At, s.At + s.For
	}
	as, ae := window(a)
	bs, be := window(b)
	return as < be && bs < ae
}

func (e *Expect) empty() bool {
	return e.SequenceMonotonic == nil && e.MaxGapSeconds == nil && e.SessionSurvives == nil &&
		e.MaxFfmpegRestarts == nil && e.TunersBusy == nil
}
