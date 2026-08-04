//go:build hdhr_live

package hdhr_test

import (
	"context"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr"
)

// TestDiscoverLive broadcasts a real UDP discover on the local network.
// Not run in CI — no HDHomeRun is guaranteed on the machine.
//
//	go test -tags hdhr_live ./internal/hdhr/ -run TestDiscoverLive -v
func TestDiscoverLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	devices, err := hdhr.Discover(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	t.Logf("found %d device(s)", len(devices))
	for _, d := range devices {
		t.Logf("  DeviceID=%s BaseURL=%s TunerCount=%d", d.DeviceID, d.BaseURL, d.TunerCount)
	}
}
