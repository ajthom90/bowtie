package api

import (
	"testing"

	"github.com/ajthom90/bowtie/server/internal/hdhr"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

func busy(guide string) hdhr.TunerStatus {
	return hdhr.TunerStatus{VctNumber: guide, TargetIP: "10.0.0.9"}
}

func TestWatchableChannels(t *testing.T) {
	chans := []store.Channel{
		{ID: 1, DeviceID: "A", GuideNumber: "5.1"},
		{ID: 2, DeviceID: "A", GuideNumber: "9.1"},
		{ID: 3, DeviceID: "A", GuideNumber: "11.1"},
		{ID: 4, DeviceID: "B", GuideNumber: "23.1"},
		{ID: 5, DeviceID: "C", GuideNumber: "45.1"},
	}
	devices := []tuner.DeviceStatus{
		// A: both tuners busy (Bowtie on 9.1, something else on 11.1).
		{Device: store.Device{DeviceID: "A"}, Reachable: true, Tuners: []hdhr.TunerStatus{busy("9.1"), busy("11.1")}},
		// B: one free tuner.
		{Device: store.Device{DeviceID: "B"}, Reachable: true, Tuners: []hdhr.TunerStatus{busy("2.1"), {}}},
		// C: unreachable — unknown, so not hidden.
		{Device: store.Device{DeviceID: "C"}, Reachable: false},
	}
	got := watchableChannels(chans, devices, []int64{2})
	want := map[int64]bool{
		1: false, // A has no free tuner and nobody in Bowtie watches 5.1
		2: true,  // Bowtie is streaming 9.1: joinable
		3: false, // 11.1 is held by another app (e.g. Plex): not joinable
		4: true,  // B has a free tuner
		5: true,  // C's status is unknown
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("channel %d watchable = %v, want %v", id, got[id], w)
		}
	}
}

func TestWatchableChannelsReachableWithoutStatus(t *testing.T) {
	// A reachable tuner whose status couldn't be read: unknown, not hidden.
	chans := []store.Channel{{ID: 1, DeviceID: "A", GuideNumber: "5.1"}}
	devices := []tuner.DeviceStatus{{Device: store.Device{DeviceID: "A"}, Reachable: true}}
	if !watchableChannels(chans, devices, nil)[1] {
		t.Fatal("unknown status must stay watchable")
	}
}
