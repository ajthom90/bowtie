package testplayer

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var c0 = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)

func at(sec float64) time.Time { return c0.Add(time.Duration(sec * float64(time.Second))) }

type ent struct {
	pdt  time.Time
	disc bool
	dur  float64 // 0 means 4 s
}

// mk renders a media playlist.
func mk(name string, mseq, dseq int64, es ...ent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
	if dseq > 0 {
		fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", dseq)
	}
	for i, e := range es {
		if e.disc {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		d := e.dur
		if d == 0 {
			d = 4
		}
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.6f,\n/s/%s_%05d.ts?token=x\n",
			e.pdt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), d, name, mseq+int64(i))
	}
	return b.String()
}

func snap(name, body string, n int) Snapshot {
	return ParseSnapshot(name, []byte(body), at(float64(n)))
}

// restartRun is two renditions across a restart at 70 s: video seq 0..2 then
// 3.. (new run); audio had one more segment before FFmpeg died (0..3 then 4..).
func restartRun(audioBoundary time.Time) []Snapshot {
	return []Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(8)}, ent{pdt: at(70), disc: true}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(8)}, ent{pdt: at(12)}, ent{pdt: audioBoundary, disc: true}), 1),
		snap("v480", mk("v480", 3, 0, ent{pdt: at(70), disc: true}, ent{pdt: at(74)}), 2),
		snap("aac0", mk("aac0", 4, 0, ent{pdt: audioBoundary, disc: true}, ent{pdt: audioBoundary.Add(4 * time.Second)}), 2),
		snap("v480", mk("v480", 4, 1, ent{pdt: at(74)}, ent{pdt: at(78)}), 3),
		snap("aac0", mk("aac0", 5, 1, ent{pdt: audioBoundary.Add(4 * time.Second)}, ent{pdt: audioBoundary.Add(8 * time.Second)}), 3),
	}
}

func allOK(c Consistency) bool {
	return c.RenditionsMonotonic && c.DiscontinuitySequenceOK && c.PDTMonotonic && c.PDTAligned && c.DiscontinuitiesAligned
}

func TestConsistencyPairsByPDTWhenAudioWasAhead(t *testing.T) {
	c := CheckConsistency(restartRun(at(70)))
	if !allOK(c) {
		t.Fatalf("consistent run flagged: %+v", c)
	}
	if c.MaxDiscontinuitySequence != 1 {
		t.Fatalf("MaxDiscontinuitySequence=%d, want 1", c.MaxDiscontinuitySequence)
	}
}

func TestConsistencyFlagsMisalignedBoundary(t *testing.T) {
	c := CheckConsistency(restartRun(at(74)))
	if c.DiscontinuitiesAligned {
		t.Fatalf("audio boundary 4 s late not flagged: %+v", c)
	}
}

// The original bug: after a restart one rendition carries a tag the other
// doesn't, so the same broadcast moment has two discontinuity numbers.
func TestConsistencyFlagsDifferentNumbersForTheSameMoment(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(70), disc: true}, ent{pdt: at(74)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(70)}, ent{pdt: at(74)}), 1),
	})
	if c.DiscontinuitiesAligned {
		t.Fatalf("discontinuity 1 vs 0 at 70 s not flagged: %+v", c)
	}
	if !strings.Contains(strings.Join(c.Problems, "\n"), "is discontinuity 0, v480 says 1") {
		t.Fatalf("problem does not name the numbers: %q", c.Problems)
	}
}

// The original bug, second form: a rendition that missed runs and came back
// under DISCONTINUITY-SEQUENCE 0 while the other's old segments are still
// in its window.
func TestConsistencyFlagsReturningRenditionNumberedFromZero(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(40), disc: true}, ent{pdt: at(80), disc: true}, ent{pdt: at(84)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(80), disc: true}, ent{pdt: at(84)}), 1),
	})
	if c.DiscontinuitiesAligned {
		t.Fatalf("audio at 80 s as discontinuity 1 vs video 2 not flagged: %+v", c)
	}
}

func TestConsistencyFlagsCoverageHole(t *testing.T) {
	// Audio skips 8–20 s while video, with no gap, has it. (A hole shorter
	// than one segment is inside the pairing tolerance.)
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(8)}, ent{pdt: at(12)}, ent{pdt: at(16)}, ent{pdt: at(20)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(20)}, ent{pdt: at(24)}), 1),
	})
	if c.PDTAligned {
		t.Fatalf("audio hole at 8–20 s not flagged: %+v", c)
	}
}

func TestConsistencyAllowsAGapBothRenditionsJumped(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(30)}, ent{pdt: at(34)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(30.2)}, ent{pdt: at(34.2)}), 1),
	})
	if !allOK(c) {
		t.Fatalf("shared gap flagged: %+v", c)
	}
}

// An hour of 4.004 s video against 4.032 s audio drifts by whole segments;
// (run, index) keys would misalign, PDT overlap must not.
func TestConsistencyAlignsAnHourOfUnequalDurations(t *testing.T) {
	var v, a []ent
	for i := 0; float64(i)*4.004 < 3600; i++ {
		v = append(v, ent{pdt: at(float64(i) * 4.004), dur: 4.004})
	}
	for i := 0; float64(i)*4.032 < 3600; i++ {
		a = append(a, ent{pdt: at(float64(i) * 4.032), dur: 4.032})
	}
	c := CheckConsistency([]Snapshot{snap("v480", mk("v480", 0, 0, v...), 1), snap("aac0", mk("aac0", 0, 0, a...), 1)})
	if !allOK(c) {
		t.Fatalf("drifting durations flagged: %+v", c.Problems)
	}
}

func TestPairByPDT(t *testing.T) {
	others := []PDTSeg{{Start: at(0), Dur: 4}, {Start: at(4), Dur: 4}, {Start: at(20), Dur: 4}}
	for _, tc := range []struct {
		start float64
		want  int
		ok    bool
	}{
		{0.1, 0, true},  // mostly overlaps the first
		{2.5, 1, true},  // overlaps the second more
		{8, 1, true},    // no overlap; nearest start within one segment
		{13, -1, false}, // nothing within one segment
		{19, 2, true},
	} {
		got, ok := PairByPDT(PDTSeg{Start: at(tc.start), Dur: 4}, others, 4*time.Second)
		if got != tc.want || ok != tc.ok {
			t.Errorf("start %v: got (%d,%v), want (%d,%v)", tc.start, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConsistencyFlagsPDTBackwards(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(6), disc: true}), 1),
	})
	if c.PDTMonotonic {
		t.Fatalf("PDT 6 s after a segment ending at 8 s not flagged: %+v", c)
	}
}

func TestConsistencyFlagsStuckDiscontinuitySequence(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 2, 0, ent{pdt: at(8), disc: true}, ent{pdt: at(12)}), 1),
		// The tagged segment slid out but the sequence stayed 0.
		snap("v480", mk("v480", 3, 0, ent{pdt: at(12)}, ent{pdt: at(16)}), 2),
	})
	if c.DiscontinuitySequenceOK {
		t.Fatalf("missing DISCONTINUITY-SEQUENCE increment not flagged: %+v", c)
	}
}

func TestConsistencyFlagsMediaSequenceBackwards(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 5, 0, ent{pdt: at(20)}), 1),
		snap("v480", mk("v480", 0, 0, ent{pdt: at(24)}), 2),
	})
	if c.RenditionsMonotonic {
		t.Fatalf("MEDIA-SEQUENCE reset not flagged: %+v", c)
	}
}

func TestConsistencyRequiresPDT(t *testing.T) {
	c := CheckConsistency([]Snapshot{snap("v480", "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.0,\na.ts\n", 1)})
	if c.PDTMonotonic {
		t.Fatal("playlist without PDT passed pdtMonotonic")
	}
}

func TestParseSnapshotReadsFFmpegPDT(t *testing.T) {
	s := ParseSnapshot("v480", []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:7\n#EXT-X-DISCONTINUITY-SEQUENCE:2\n"+
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T20:00:04.500+0000\n#EXTINF:4.004000,\nv480_00007.ts\n"+
		"#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:2026-10-11T20:00:30.000+0000\n#EXTINF:4.0,\nv480_00008.ts\n"), at(0))
	if s.MediaSequence != 7 || s.DiscontinuitySequence != 2 || len(s.Segments) != 2 {
		t.Fatalf("snapshot %+v", s)
	}
	g := s.Segments[0]
	if g.Seq != 7 || g.Duration != 4.004 || !g.PDT.Equal(at(4.5)) || g.URI != "v480_00007.ts" || g.DNum != 2 {
		t.Fatalf("segment %+v", g)
	}
	if h := s.Segments[1]; !h.Discontinuity || h.DNum != 3 {
		t.Fatalf("tagged segment %+v, want DNum 3", h)
	}
}
