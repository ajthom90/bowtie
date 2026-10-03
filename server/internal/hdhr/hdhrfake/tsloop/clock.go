package tsloop

import "time"

// Clock is injectable time, in the same shape as stream.WithIngestClock so one
// fake clock can drive both Bowtie's ingest and the fake device.
type Clock struct {
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

// RealClock returns wall-clock time.
func RealClock() Clock {
	return Clock{Now: time.Now, After: time.After}
}
