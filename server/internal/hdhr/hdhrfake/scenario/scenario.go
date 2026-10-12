// Package scenario loads YAML fault timelines for the fake HDHomeRun and checks
// a run's observed viewer experience against the file's expectations. A
// scenario file with an expect block is a regression test.
package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
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
	// WindowSegments is the session's -hls_list_size (0: Bowtie's default).
	// Small windows let a run watch a restart slide out of the playlist.
	WindowSegments int         `yaml:"windowSegments"`
	Timeline       []Step      `yaml:"timeline"`
	Bowtie         *BowtieSpec `yaml:"bowtie"`
	Expect         *Expect     `yaml:"expect"`
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
	// CloseTranscoderInputAt ends FFmpeg's input (EOF) at this point, as the
	// ingest does when it gives up on a transcoder; FFmpeg exits gracefully.
	CloseTranscoderInputAt time.Duration `yaml:"closeTranscoderInputAt"`
	// Layout: "" (one rung, first audio as AAC) or "production" (ladder even
	// with libx264, every audio track, the AC-3 group, captions).
	Layout string `yaml:"layout"`
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
	// Timeline and rendition checks (testplayer.Consistency).
	RenditionsMonotonic      *bool  `yaml:"renditionsMonotonic"`
	DiscontinuitiesAligned   *bool  `yaml:"discontinuitiesAligned"`
	DiscontinuitySequenceOK  *bool  `yaml:"discontinuitySequenceOK"`
	MinDiscontinuitySequence *int64 `yaml:"minDiscontinuitySequence"`
	PDTMonotonic             *bool  `yaml:"pdtMonotonic"`
	PDTAligned               *bool  `yaml:"pdtAligned"`
	// Σ EXTINF minus wall time, in seconds (negative: an outage was skipped).
	MaxTimelineExcessSeconds *float64 `yaml:"maxTimelineExcessSeconds"`
	MinTimelineExcessSeconds *float64 `yaml:"minTimelineExcessSeconds"`
	// ffprobe checks; a run that probed no audio violates them. The start
	// offset also requires a probed video segment.
	MaxAudioHoleMs     *float64 `yaml:"maxAudioHoleMs"`
	MaxAVStartSkewMs   *float64 `yaml:"maxAVStartSkewMs"`
	MaxStartAVOffsetMs *float64 `yaml:"maxStartAVOffsetMs"`
	MinFfmpegRestarts  *int     `yaml:"minFfmpegRestarts"`
	// XFailOn is the violation text an xfail run must show; other
	// violations fail it (a dead session can't pass for the known gap).
	XFailOn string `yaml:"xfailOn"`
	// Progress and coverage after the faults (every followed rendition).
	MinSegmentsAfterFault *int     `yaml:"minSegmentsAfterFault"`
	PairedAfterEachFault  *bool    `yaml:"pairedAfterEachFault"`
	MaxTailGapSeconds     *float64 `yaml:"maxTailGapSeconds"`
	CaptionsTimed         *bool    `yaml:"captionsTimed"`
	NoFetchErrors         *bool    `yaml:"noFetchErrors"`
	MinRenditions         *int     `yaml:"minRenditions"`
	MaxStartSeconds       *float64 `yaml:"maxStartSeconds"`
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
	if s.WindowSegments < 0 || (s.WindowSegments > 0 && s.WindowSegments < 3) {
		return fmt.Errorf("scenario %s: windowSegments %d (want 0 or ≥ 3)", s.Name, s.WindowSegments)
	}
	if s.Bowtie != nil && s.Bowtie.Layout != "" && s.Bowtie.Layout != "production" {
		return fmt.Errorf("scenario %s: layout %q (want production or empty)", s.Name, s.Bowtie.Layout)
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
	if s.Bowtie != nil && s.Bowtie.CloseTranscoderInputAt != 0 &&
		(s.Bowtie.CloseTranscoderInputAt < 0 || s.Bowtie.CloseTranscoderInputAt >= s.Duration) {
		return fmt.Errorf("scenario %s: closeTranscoderInputAt %v outside 0..%v", s.Name, s.Bowtie.CloseTranscoderInputAt, s.Duration)
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
		if e.XFail != "" && e.XFailOn == "" {
			return fmt.Errorf("scenario %s: xfail needs xfailOn (the violation that must occur)", s.Name)
		}
	}
	return nil
}

// overlaps reports same-kind, same-scope steps whose active windows intersect.
// One-shot kinds (drop, pts-jump and timeline faults) never overlap.
func overlaps(a, b Step, end time.Duration) bool {
	if a.Fault != b.Fault || a.Scope != b.Scope || a.Fault == faults.Drop || a.Fault == faults.PTSJump || a.Fault.IsTimeline() {
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
		e.MaxFfmpegRestarts == nil && e.TunersBusy == nil &&
		e.RenditionsMonotonic == nil && e.DiscontinuitiesAligned == nil && e.DiscontinuitySequenceOK == nil &&
		e.MinDiscontinuitySequence == nil && e.PDTMonotonic == nil && e.PDTAligned == nil &&
		e.MaxTimelineExcessSeconds == nil && e.MinTimelineExcessSeconds == nil &&
		e.MaxAudioHoleMs == nil && e.MaxAVStartSkewMs == nil && e.MaxStartAVOffsetMs == nil && e.MinFfmpegRestarts == nil &&
		e.MinSegmentsAfterFault == nil && e.NoFetchErrors == nil && e.MinRenditions == nil && e.MaxStartSeconds == nil &&
		e.PairedAfterEachFault == nil && e.MaxTailGapSeconds == nil && e.CaptionsTimed == nil
}

// FaultClusterGap: fault ends closer together than this form one cluster,
// with one coverage window after the cluster's last end.
const FaultClusterGap = 8 * time.Second

// blocksDelivery reports whether a fault kind stops segments from arriving
// while it lasts (an outage), as opposed to damaging what arrives.
func blocksDelivery(k faults.Kind) bool {
	switch k {
	case faults.Signal, faults.Stall, faults.Hang, faults.PIDLoss:
		return true
	}
	return false
}

// FaultEnds lists where the e2e runner opens a coverage window: the end of
// each fault cluster, ascending. A fault ends at a timeline step's at + for,
// the slow-consumer window's end, or the kill or close time. Ends closer
// together than FaultClusterGap form one cluster, whose window opens at
// its last end and must show coverage for all of them. An end strictly
// inside a delivery-blocking fault (an outage, a stall, a hung tuner, a
// lost PID, FFmpeg not reading) opens no window: nothing can arrive until
// that fault is over, and its own end opens one. Corruption and timestamp
// faults never suppress another fault's window.
func (s *Scenario) FaultEnds() []time.Duration {
	type span struct {
		from, to time.Duration
		blocks   bool
	}
	var spans []span
	for _, st := range s.Timeline {
		spans = append(spans, span{st.At, st.At + st.For, blocksDelivery(st.Fault)})
	}
	if b := s.Bowtie; b != nil {
		if w := b.SlowConsumer; w != nil {
			spans = append(spans, span{w.At, w.At + w.For, true})
		}
		for _, t := range []time.Duration{b.KillTranscoderAt, b.CloseTranscoderInputAt} {
			if t > 0 {
				spans = append(spans, span{t, t, true})
			}
		}
	}
	var ends []time.Duration
	for _, a := range spans {
		inside := false
		for _, b := range spans {
			if b.blocks && b.from < a.to && a.to < b.to {
				inside = true
			}
		}
		if !inside {
			ends = append(ends, a.to)
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
	var out []time.Duration
	for i, e := range ends {
		if i+1 < len(ends) && ends[i+1]-e < FaultClusterGap {
			continue // the cluster goes on: its window opens at a later end
		}
		out = append(out, e)
	}
	return out
}

// LastFaultEnd is when the last fault of the run is over (0: no faults).
func (s *Scenario) LastFaultEnd() time.Duration {
	ends := s.FaultEnds()
	if len(ends) == 0 {
		return 0
	}
	return ends[len(ends)-1]
}
