package testplayer

import (
	"strings"
	"testing"
	"time"
)

func TestCoverageAfterFaults(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	win := func(start, end float64, paired, epochs map[string]map[int64]int, timed int) CoverageWindow {
		segs := map[string]int{}
		for n, m := range epochs {
			for _, c := range m {
				segs[n] += c
			}
		}
		return CoverageWindow{
			Start: t0.Add(time.Duration(start * float64(time.Second))), End: t0.Add(time.Duration(end * float64(time.Second))),
			Segments: segs, Epochs: epochs, Paired: paired,
			Cues: map[string]int{"v480_vtt": timed + 1}, CuesTimed: map[string]int{"v480_vtt": timed},
		}
	}
	r := Report{
		Renditions: []string{"v480", "aac0", "v480_vtt"},
		TailGap:    map[string]time.Duration{"v480": time.Second, "aac0": 9 * time.Second, "v480_vtt": 2 * time.Second},
		Windows: []CoverageWindow{
			// Before the first fault cluster: not checked.
			win(0, 10, nil, map[string]map[int64]int{"v480": {0: 2}}, 0),
			// After a restart: audio's epoch 1 delivered segments but none
			// paired, and video's one-segment epoch 2 never paired (a short
			// restart; not exempt, because it isn't the end of observation).
			win(10, 40,
				map[string]map[int64]int{"v480": {0: 1, 1: 2}, "aac0": {0: 1}},
				map[string]map[int64]int{"v480": {0: 1, 1: 5, 2: 1}, "aac0": {0: 1, 1: 5}}, 3),
			// The last window: video's single segment of its last epoch is
			// exempt; audio stopped.
			win(40, 52, map[string]map[int64]int{"v480": {3: 2}}, map[string]map[int64]int{"v480": {3: 2, 4: 1}}, 0),
		},
	}
	got := strings.Join(r.UnpairedAfterFaults(), "\n")
	want := "window 1: v480 discontinuity 2 has 1 segment(s), none A/V-paired\n" +
		"window 1: aac0 discontinuity 1 has 5 segment(s), none A/V-paired\n" +
		"window 2: aac0 has no A/V-paired segment (0 fetched)"
	if got != want {
		t.Fatalf("unpaired:\n%s\nwant:\n%s", got, want)
	}
	if got := strings.Join(r.UntimedCueWindows(), "\n"); got != "window 2: v480_vtt has no timed cues (1 cues)" {
		t.Fatalf("untimed: %q", got)
	}
	if n, g := r.MaxTailGap(); n != "aac0" || g != 9*time.Second {
		t.Fatalf("max tail gap %s %v", n, g)
	}
	if got := (Report{Renditions: []string{"v480"}, Windows: r.Windows[:1]}).UnpairedAfterFaults(); len(got) != 1 {
		t.Fatalf("video alone must be reported: %v", got)
	}
	// With no faults, the only window is the last and is checked.
	if got := (Report{Renditions: []string{"v480", "aac0"}, Windows: r.Windows[:1]}).UnpairedAfterFaults(); len(got) != 2 {
		t.Fatalf("a run without faults is checked as one window: %v", got)
	}
}
