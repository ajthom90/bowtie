package testplayer

import (
	"fmt"
	"sort"
	"time"
)

// checkedWindows are the windows whose coverage is checked: every window
// opened by a Mark (the end of a fault cluster; scenario.FaultEnds spaces
// them at least 8 s apart) and the last window, which is the only one
// when nothing was marked.
func (r Report) checkedWindows() []int {
	var out []int
	for i := range r.Windows {
		if i > 0 || i == len(r.Windows)-1 {
			out = append(out, i)
		}
	}
	return out
}

// UnpairedAfterFaults lists coverage holes in the checked windows: an audio
// or video rendition with no segment PDT-paired with the other kind, or a
// discontinuity number it delivered in the window with none of its
// segments paired. The only exemption is a single segment of the
// rendition's last discontinuity in the last window: observation ended
// before its partner could arrive. Empty means every rendition's A/V
// alignment was measured after every fault cluster, in every discontinuity.
func (r Report) UnpairedAfterFaults() []string {
	var av []string
	audio, video := false, false
	for _, name := range r.Renditions {
		if isSubtitleRendition(name) {
			continue
		}
		av = append(av, name)
		if isAudioRendition(name) {
			audio = true
		} else {
			video = true
		}
	}
	if !audio || !video {
		return []string{fmt.Sprintf("renditions %v: no audio and video to pair", r.Renditions)}
	}
	final := map[string]int64{} // each rendition's last discontinuity number
	for _, w := range r.Windows {
		for name, m := range w.Epochs {
			for d := range m {
				if f, ok := final[name]; !ok || d > f {
					final[name] = d
				}
			}
		}
	}
	var out []string
	for _, i := range r.checkedWindows() {
		w, last := r.Windows[i], i == len(r.Windows)-1
		for _, name := range av {
			paired := 0
			for _, n := range w.Paired[name] {
				paired += n
			}
			if paired == 0 {
				out = append(out, fmt.Sprintf("window %d: %s has no A/V-paired segment (%d fetched)", i, name, w.Segments[name]))
				continue
			}
			var dnums []int64
			for d := range w.Epochs[name] {
				dnums = append(dnums, d)
			}
			sort.Slice(dnums, func(a, b int) bool { return dnums[a] < dnums[b] })
			for _, d := range dnums {
				n := w.Epochs[name][d]
				if w.Paired[name][d] > 0 || (last && d == final[name] && n == 1) {
					continue
				}
				out = append(out, fmt.Sprintf("window %d: %s discontinuity %d has %d segment(s), none A/V-paired", i, name, d, n))
			}
		}
	}
	return out
}

// UntimedCueWindows lists checked windows in which a subtitle rendition
// had no cue timed against its video segment (no cues, or none matched).
func (r Report) UntimedCueWindows() []string {
	var out []string
	for _, i := range r.checkedWindows() {
		for _, name := range r.Renditions {
			if isSubtitleRendition(name) && r.Windows[i].CuesTimed[name] == 0 {
				out = append(out, fmt.Sprintf("window %d: %s has no timed cues (%d cues)", i, name, r.Windows[i].Cues[name]))
			}
		}
	}
	return out
}

// MaxTailGap is the longest TailGap and its rendition.
func (r Report) MaxTailGap() (string, time.Duration) {
	var name string
	var max time.Duration
	for _, n := range r.Renditions {
		if g := r.TailGap[n]; g > max || name == "" {
			name, max = n, g
		}
	}
	return name, max
}
