// Package faults injects tuner and network failures into a fake HDHomeRun's
// packet stream: signal degradation, stalls, drops, corruption and more.
package faults

import (
	"errors"
	"fmt"
	"time"
)

// Kind names a fault.
type Kind string

const (
	Signal        Kind = "signal"
	Stall         Kind = "stall"
	Drop          Kind = "drop"
	Corrupt       Kind = "corrupt"
	Slow          Kind = "slow"
	TimestampJump Kind = "timestamp-jump"
	PIDLoss       Kind = "pid-loss"
	SourceSwitch  Kind = "source-switch"
	Busy          Kind = "busy"
	Hang          Kind = "hang"
)

// Spec describes one fault. JSON/YAML field names are shared by scenario files
// and the control API.
type Spec struct {
	Fault    Kind          `yaml:"fault" json:"fault"`
	For      time.Duration `yaml:"for,omitempty" json:"for,omitempty"` // 0 = until removed
	Strength *int          `yaml:"strength,omitempty" json:"strength,omitempty"`
	Quality  *int          `yaml:"quality,omitempty" json:"quality,omitempty"`
	Symbol   *int          `yaml:"symbol,omitempty" json:"symbol,omitempty"`
	Burst    bool          `yaml:"burst,omitempty" json:"burst,omitempty"` // stall: deliver backlog afterwards
	Mode     string        `yaml:"mode,omitempty" json:"mode,omitempty"`   // drop: clean|reset
	Rate     float64       `yaml:"rate,omitempty" json:"rate,omitempty"`   // corrupt probability | slow fraction
	Type     string        `yaml:"type,omitempty" json:"type,omitempty"`   // corrupt: bitflip|tei|sync
	By       time.Duration `yaml:"by,omitempty" json:"by,omitempty"`       // timestamp-jump
	Track    string        `yaml:"track,omitempty" json:"track,omitempty"` // pid-loss: audio|video
	To       string        `yaml:"to,omitempty" json:"to,omitempty"`       // source-switch: source name
}

// Scope selects what a fault hits.
type Scope string

const (
	// ScopeChannel hits every connection on the channel, present and future.
	ScopeChannel Scope = "channel"
	// ScopeConnections hits only the connections open when the fault starts.
	ScopeConnections Scope = "connections"
	// ScopeDevice is for device-wide faults (busy, hang).
	ScopeDevice Scope = "device"
)

// Target is where a fault applies.
type Target struct {
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
	Scope   Scope  `yaml:"scope,omitempty" json:"scope,omitempty"`
}

// ErrTimelineFault is returned by Engine.Add for faults that act on the
// channel timeline (timestamp-jump, source-switch) rather than on packets.
var ErrTimelineFault = errors.New("faults: timeline fault must be applied to the channel")

// IsTimeline reports whether the kind acts on the channel timeline.
func (k Kind) IsTimeline() bool { return k == TimestampJump || k == SourceSwitch }

// IsDevice reports whether the kind is device-wide.
func (k Kind) IsDevice() bool { return k == Busy || k == Hang }

func pct(name string, v *int) error {
	if v != nil && (*v < 0 || *v > 100) {
		return fmt.Errorf("faults: %s %d out of range 0-100", name, *v)
	}
	return nil
}

// Validate checks that the spec is complete and in range.
func (s Spec) Validate() error {
	if s.For < 0 {
		return fmt.Errorf("faults: negative duration %v", s.For)
	}
	switch s.Fault {
	case Signal:
		if s.Strength == nil && s.Quality == nil && s.Symbol == nil {
			return errors.New("faults: signal needs strength, quality or symbol")
		}
		for _, e := range []error{pct("strength", s.Strength), pct("quality", s.Quality), pct("symbol", s.Symbol)} {
			if e != nil {
				return e
			}
		}
	case Stall, Busy, Hang:
	case Drop:
		if s.Mode != "" && s.Mode != "clean" && s.Mode != "reset" {
			return fmt.Errorf("faults: drop mode %q (want clean|reset)", s.Mode)
		}
	case Corrupt:
		if s.Rate <= 0 || s.Rate > 1 {
			return fmt.Errorf("faults: corrupt rate %v out of (0,1]", s.Rate)
		}
		switch s.Type {
		case "", "bitflip", "tei", "sync":
		default:
			return fmt.Errorf("faults: corrupt type %q (want bitflip|tei|sync)", s.Type)
		}
	case Slow:
		if s.Rate <= 0 || s.Rate >= 1 {
			return fmt.Errorf("faults: slow rate %v out of (0,1)", s.Rate)
		}
	case PIDLoss:
		if s.Track != "audio" && s.Track != "video" {
			return fmt.Errorf("faults: pid-loss track %q (want audio|video)", s.Track)
		}
	case TimestampJump:
		if s.By == 0 {
			return errors.New("faults: timestamp-jump needs a non-zero by")
		}
	case SourceSwitch:
		if s.To == "" {
			return errors.New("faults: source-switch needs to")
		}
	default:
		return fmt.Errorf("faults: unknown fault %q", s.Fault)
	}
	return nil
}
