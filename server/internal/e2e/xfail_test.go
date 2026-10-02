package e2e

import (
	"fmt"
	"testing"
	"time"
)

// xfail pins a known Bowtie gap: check describes the correct behavior and is
// expected to fail today. If it passes, the gap is fixed and the marker must go.
func xfail(t *testing.T, reason string, check func() error) {
	t.Helper()
	if err := check(); err != nil {
		t.Logf("xfail (%s): %v", reason, err)
		return
	}
	t.Fatalf("known gap fixed (%s): remove the xfail marker", reason)
}

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
