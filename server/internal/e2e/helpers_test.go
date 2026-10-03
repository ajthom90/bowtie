package e2e

import (
	"fmt"
	"time"
)

// within polls cond every 50ms until it holds or d elapses.
func within(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }
