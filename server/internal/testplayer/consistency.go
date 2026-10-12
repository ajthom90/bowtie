package testplayer

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxAlign is how far apart two renditions may start the same discontinuity.
const maxAlign = 500 * time.Millisecond

// pdtSlack absorbs millisecond rounding in served PDTs.
const pdtSlack = 5 * time.Millisecond

// maxProblemsPerKind keeps a broken run's report readable.
const maxProblemsPerKind = 5

// SegmentObs is one segment as a media playlist listed it.
type SegmentObs struct {
	Seq           int64
	Duration      float64
	PDT           time.Time // zero when the segment had no #EXT-X-PROGRAM-DATE-TIME
	Discontinuity bool
	// DNum is the discontinuity number a client computes for the segment:
	// DISCONTINUITY-SEQUENCE plus the tags up to and including it.
	DNum int64
	URI  string
}

// Snapshot is one poll of one rendition's media playlist.
type Snapshot struct {
	Rendition             string
	At                    time.Time
	MediaSequence         int64
	DiscontinuitySequence int64
	Segments              []SegmentObs
}

// PDTSeg is a segment's place on the wall clock and its discontinuity number.
type PDTSeg struct {
	Start time.Time
	Dur   float64
	DNum  int64
}

// End is when the segment ends on the wall clock.
func (s PDTSeg) End() time.Time { return s.Start.Add(time.Duration(s.Dur * float64(time.Second))) }

// PairByPDT returns the index in others (sorted by Start, not overlapping)
// of the segment whose PDT range overlaps a's the most. With no overlap it
// is the one starting nearest to a, within tol (one segment). False when
// none qualifies.
func PairByPDT(a PDTSeg, others []PDTSeg, tol time.Duration) (int, bool) {
	best, bestOv, bestDist := -1, time.Duration(0), time.Duration(1<<62)
	i := sort.Search(len(others), func(i int) bool { return !others[i].End().Before(a.Start.Add(-tol)) })
	for ; i < len(others) && !others[i].Start.After(a.End().Add(tol)); i++ {
		o := others[i]
		lo, hi := a.Start, a.End()
		if o.Start.After(lo) {
			lo = o.Start
		}
		if o.End().Before(hi) {
			hi = o.End()
		}
		ov := hi.Sub(lo)
		dist := o.Start.Sub(a.Start)
		if dist < 0 {
			dist = -dist
		}
		switch {
		case ov > bestOv:
			best, bestOv, bestDist = i, ov, dist
		case bestOv <= 0 && dist <= tol && dist < bestDist:
			best, bestDist = i, dist
		}
	}
	return best, best >= 0
}

// pdtLayouts: RFC 3339 (Bowtie) and FFmpeg's "+0000" offset.
var pdtLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"}

func parsePDT(v string) (time.Time, bool) {
	for _, l := range pdtLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseSnapshot reads the parts of a media playlist the checks use.
func ParseSnapshot(rendition string, body []byte, at time.Time) Snapshot {
	s := Snapshot{Rendition: rendition, At: at}
	var cur SegmentObs
	idx := int64(0)
	dnum := int64(-1) // set once DISCONTINUITY-SEQUENCE (or the first segment) is seen
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			s.MediaSequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			s.DiscontinuitySequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			cur.Discontinuity = true
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			if t, ok := parsePDT(strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:")); ok {
				cur.PDT = t
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			cur.Duration, _ = strconv.ParseFloat(v, 64)
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if dnum < 0 {
				dnum = s.DiscontinuitySequence
			}
			if cur.Discontinuity {
				dnum++
			}
			cur.DNum = dnum
			cur.Seq = s.MediaSequence + idx
			cur.URI = line
			s.Segments = append(s.Segments, cur)
			cur = SegmentObs{}
			idx++
		}
	}
	return s
}

// Consistency is what a viewer switching renditions or seeking by wall
// clock would rely on, checked across every poll of every rendition.
type Consistency struct {
	// RenditionsMonotonic: no rendition's MEDIA-SEQUENCE or newest segment went down.
	RenditionsMonotonic bool
	// DiscontinuitySequenceOK: each DISCONTINUITY-SEQUENCE equals the previous
	// one plus the tagged segments that slid out, and tags never move.
	DiscontinuitySequenceOK bool
	// MaxDiscontinuitySequence is the largest DISCONTINUITY-SEQUENCE served.
	MaxDiscontinuitySequence int64
	// PDTMonotonic: every segment has a PDT, a segment's PDT never changes, and
	// no segment starts before the previous one ends.
	PDTMonotonic bool
	// PDTAligned: wherever two renditions both have segments, each segment
	// of one overlaps (within one segment) a segment of the other.
	PDTAligned bool
	// DiscontinuitiesAligned: segments paired by PDT overlap have the same
	// discontinuity number, each discontinuity starts at the same PDT
	// (±500 ms) in every rendition, and every rendition ends on the same number.
	DiscontinuitiesAligned bool
	Problems               []string
}

type segRec struct {
	dnum int64
	pdt  time.Time
	dur  float64
	disc bool
}

type rendView struct {
	snaps    []Snapshot
	segs     map[int64]segRec    // seq → first observation
	boundary map[int64]time.Time // discontinuity number → PDT of its tagged first segment
	final    int64
	ordered  []PDTSeg // segments with a PDT, by start
}

// CheckConsistency checks snapshots (in poll order) within and across renditions.
func CheckConsistency(snaps []Snapshot) Consistency {
	c := Consistency{RenditionsMonotonic: true, DiscontinuitySequenceOK: true, PDTMonotonic: true, PDTAligned: true, DiscontinuitiesAligned: true}
	counts := map[string]int{}
	problem := func(kind string, flag *bool, format string, a ...any) {
		*flag = false
		counts[kind]++
		if counts[kind] <= maxProblemsPerKind {
			c.Problems = append(c.Problems, kind+": "+fmt.Sprintf(format, a...))
		}
	}

	views := map[string]*rendView{}
	var names []string
	for _, s := range snaps {
		v := views[s.Rendition]
		if v == nil {
			v = &rendView{segs: map[int64]segRec{}, boundary: map[int64]time.Time{}}
			views[s.Rendition] = v
			names = append(names, s.Rendition)
		}
		v.snaps = append(v.snaps, s)
	}
	sort.Strings(names)

	for _, name := range names {
		v := views[name]
		noPDT := false
		for i, s := range v.snaps {
			if s.DiscontinuitySequence > c.MaxDiscontinuitySequence {
				c.MaxDiscontinuitySequence = s.DiscontinuitySequence
			}
			if i > 0 {
				p := v.snaps[i-1]
				if s.MediaSequence < p.MediaSequence {
					problem("sequence", &c.RenditionsMonotonic, "%s: MEDIA-SEQUENCE %d after %d", name, s.MediaSequence, p.MediaSequence)
				}
				if len(s.Segments) > 0 && len(p.Segments) > 0 && s.Segments[len(s.Segments)-1].Seq < p.Segments[len(p.Segments)-1].Seq {
					problem("sequence", &c.RenditionsMonotonic, "%s: newest segment went down", name)
				}
				if s.DiscontinuitySequence < p.DiscontinuitySequence {
					problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY-SEQUENCE %d after %d", name, s.DiscontinuitySequence, p.DiscontinuitySequence)
				}
				lastP := p.MediaSequence + int64(len(p.Segments)) - 1
				if s.MediaSequence <= lastP+1 { // the window did not slide past unseen segments
					want := p.DiscontinuitySequence
					for _, g := range p.Segments {
						if g.Discontinuity && g.Seq < s.MediaSequence {
							want++
						}
					}
					if s.DiscontinuitySequence != want {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY-SEQUENCE %d at MEDIA-SEQUENCE %d, want %d", name, s.DiscontinuitySequence, s.MediaSequence, want)
					}
				}
			}
			for _, g := range s.Segments {
				if g.Discontinuity && !g.PDT.IsZero() {
					v.boundary[g.DNum] = g.PDT
				}
				if g.PDT.IsZero() && !noPDT {
					noPDT = true
					problem("pdt", &c.PDTMonotonic, "%s: segment %d has no PROGRAM-DATE-TIME", name, g.Seq)
				}
				if old, ok := v.segs[g.Seq]; ok {
					if old.disc != g.Discontinuity {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY on segment %d came and went", name, g.Seq)
					}
					if old.dnum != g.DNum {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: segment %d moved from discontinuity %d to %d", name, g.Seq, old.dnum, g.DNum)
					}
					if d := g.PDT.Sub(old.pdt); d > pdtSlack || d < -pdtSlack {
						problem("pdt", &c.PDTMonotonic, "%s: PDT of segment %d changed by %v", name, g.Seq, d)
					}
					continue
				}
				v.segs[g.Seq] = segRec{dnum: g.DNum, pdt: g.PDT, dur: g.Duration, disc: g.Discontinuity}
			}
			if n := len(s.Segments); n > 0 {
				v.final = s.Segments[n-1].DNum
			} else {
				v.final = s.DiscontinuitySequence
			}
		}
		seqs := make([]int64, 0, len(v.segs))
		for q := range v.segs {
			seqs = append(seqs, q)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for i, q := range seqs {
			r := v.segs[q]
			if !r.pdt.IsZero() {
				v.ordered = append(v.ordered, PDTSeg{Start: r.pdt, Dur: r.dur, DNum: r.dnum})
			}
			if i == 0 {
				continue
			}
			p := v.segs[seqs[i-1]]
			if p.pdt.IsZero() || r.pdt.IsZero() {
				continue
			}
			end := p.pdt.Add(time.Duration(p.dur * float64(time.Second)))
			if r.pdt.Before(end.Add(-pdtSlack)) {
				problem("pdt", &c.PDTMonotonic, "%s: segment %d starts %v before segment %d ends", name, q, end.Sub(r.pdt), seqs[i-1])
			}
		}
	}

	for i := range names {
		for j := range names {
			if i == j {
				continue
			}
			a, b := views[names[i]], views[names[j]]
			if len(b.ordered) == 0 {
				continue
			}
			first, last := b.ordered[0].Start, b.ordered[len(b.ordered)-1].End()
			for _, s := range a.ordered {
				tol := time.Duration(s.Dur * float64(time.Second))
				k, ok := PairByPDT(s, b.ordered, tol)
				if !ok {
					// Only inside the other rendition's observed span: segments
					// beyond its window edges were never compared. A gap the
					// timeline jumped is in both renditions, so it leaves no
					// segment here to miss.
					if !s.Start.Before(first) && !s.End().After(last) {
						problem("align", &c.PDTAligned, "%s: no %s segment within one segment of %s", names[i], names[j], s.Start.Format(time.RFC3339Nano))
					}
					continue
				}
				if i < j && b.ordered[k].DNum != s.DNum {
					problem("boundary", &c.DiscontinuitiesAligned, "%s at %s is discontinuity %d, %s says %d",
						names[i], s.Start.Format(time.RFC3339Nano), s.DNum, names[j], b.ordered[k].DNum)
				}
			}
			if i > j {
				continue
			}
			for e, ta := range a.boundary {
				if tb, ok := b.boundary[e]; ok {
					if d := ta.Sub(tb); d > maxAlign || d < -maxAlign {
						problem("boundary", &c.DiscontinuitiesAligned, "%s/%s: discontinuity %d starts %v apart", names[i], names[j], e, d)
					}
				}
			}
			if a.final != b.final {
				problem("boundary", &c.DiscontinuitiesAligned, "%s ends on discontinuity %d, %s on %d", names[i], a.final, names[j], b.final)
			}
		}
	}
	return c
}
