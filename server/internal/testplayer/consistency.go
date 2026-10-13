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

// rendView is what the checks keep of one rendition: its last snapshot (the
// pair checks compare consecutive polls only), the first observation of
// each segment, and the problems found as snapshots arrived. It grows with
// the segments seen, not with the polls.
type rendView struct {
	prev     *Snapshot
	segs     map[int64]segRec    // seq → first observation
	boundary map[int64]time.Time // discontinuity number → PDT of its tagged first segment
	final    int64
	noPDT    bool
	found    []problemRec   // in the order found, at most maxProblemsPerKind per kind
	kinds    map[string]int // problems found per kind
}

// problemRec is one problem found as a snapshot arrived.
type problemRec struct{ kind, msg string }

func (v *rendView) problemf(kind, format string, a ...any) {
	v.kinds[kind]++
	if v.kinds[kind] <= maxProblemsPerKind { // more can never be reported
		v.found = append(v.found, problemRec{kind, fmt.Sprintf(format, a...)})
	}
}

// consistencyChecker checks snapshots as they arrive, so a long run keeps no
// more than each rendition's last snapshot and its segments' first
// observations. result equals CheckConsistency over every snapshot added.
type consistencyChecker struct {
	views   map[string]*rendView
	maxDSeq int64
}

// CheckConsistency checks snapshots (in poll order) within and across renditions.
func CheckConsistency(snaps []Snapshot) Consistency {
	var k consistencyChecker
	for _, s := range snaps {
		k.add(s)
	}
	return k.result()
}

// add runs the checks that compare a snapshot with the rendition's previous
// one and with the first observation of its segments.
func (k *consistencyChecker) add(s Snapshot) {
	if k.views == nil {
		k.views = map[string]*rendView{}
	}
	name := s.Rendition
	v := k.views[name]
	if v == nil {
		v = &rendView{segs: map[int64]segRec{}, boundary: map[int64]time.Time{}, kinds: map[string]int{}}
		k.views[name] = v
	}
	if s.DiscontinuitySequence > k.maxDSeq {
		k.maxDSeq = s.DiscontinuitySequence
	}
	if p := v.prev; p != nil {
		if s.MediaSequence < p.MediaSequence {
			v.problemf("sequence", "%s: MEDIA-SEQUENCE %d after %d", name, s.MediaSequence, p.MediaSequence)
		}
		if len(s.Segments) > 0 && len(p.Segments) > 0 && s.Segments[len(s.Segments)-1].Seq < p.Segments[len(p.Segments)-1].Seq {
			v.problemf("sequence", "%s: newest segment went down", name)
		}
		if s.DiscontinuitySequence < p.DiscontinuitySequence {
			v.problemf("dseq", "%s: DISCONTINUITY-SEQUENCE %d after %d", name, s.DiscontinuitySequence, p.DiscontinuitySequence)
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
				v.problemf("dseq", "%s: DISCONTINUITY-SEQUENCE %d at MEDIA-SEQUENCE %d, want %d", name, s.DiscontinuitySequence, s.MediaSequence, want)
			}
		}
	}
	for _, g := range s.Segments {
		if g.Discontinuity && !g.PDT.IsZero() {
			v.boundary[g.DNum] = g.PDT
		}
		if g.PDT.IsZero() && !v.noPDT {
			v.noPDT = true
			v.problemf("pdt", "%s: segment %d has no PROGRAM-DATE-TIME", name, g.Seq)
		}
		if old, ok := v.segs[g.Seq]; ok {
			if old.disc != g.Discontinuity {
				v.problemf("dseq", "%s: DISCONTINUITY on segment %d came and went", name, g.Seq)
			}
			if old.dnum != g.DNum {
				v.problemf("dseq", "%s: segment %d moved from discontinuity %d to %d", name, g.Seq, old.dnum, g.DNum)
			}
			if d := g.PDT.Sub(old.pdt); d > pdtSlack || d < -pdtSlack {
				v.problemf("pdt", "%s: PDT of segment %d changed by %v", name, g.Seq, d)
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
	v.prev = &s
}

// retained counts the segment records the checker holds (for the bound test).
func (k *consistencyChecker) retained() int {
	n := 0
	for _, v := range k.views {
		n += len(v.segs)
		if v.prev != nil {
			n += len(v.prev.Segments)
		}
	}
	return n
}

// result reports the problems found so far plus the timeline checks, which
// depend on every segment seen. It changes nothing, so it may run any time.
func (k *consistencyChecker) result() Consistency {
	c := Consistency{RenditionsMonotonic: true, DiscontinuitySequenceOK: true, PDTMonotonic: true, PDTAligned: true, DiscontinuitiesAligned: true,
		MaxDiscontinuitySequence: k.maxDSeq}
	counts := map[string]int{}
	report := func(kind, msg string) {
		switch kind {
		case "sequence":
			c.RenditionsMonotonic = false
		case "dseq":
			c.DiscontinuitySequenceOK = false
		case "pdt":
			c.PDTMonotonic = false
		case "align":
			c.PDTAligned = false
		case "boundary":
			c.DiscontinuitiesAligned = false
		}
		counts[kind]++
		if counts[kind] <= maxProblemsPerKind {
			c.Problems = append(c.Problems, kind+": "+msg)
		}
	}
	problem := func(kind string, format string, a ...any) { report(kind, fmt.Sprintf(format, a...)) }

	names := make([]string, 0, len(k.views))
	for name := range k.views {
		names = append(names, name)
	}
	sort.Strings(names)

	ordered := map[string][]PDTSeg{} // segments with a PDT, by start
	for _, name := range names {
		v := k.views[name]
		for _, f := range v.found {
			report(f.kind, f.msg)
		}
		seqs := make([]int64, 0, len(v.segs))
		for q := range v.segs {
			seqs = append(seqs, q)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for i, q := range seqs {
			r := v.segs[q]
			if !r.pdt.IsZero() {
				ordered[name] = append(ordered[name], PDTSeg{Start: r.pdt, Dur: r.dur, DNum: r.dnum})
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
				problem("pdt", "%s: segment %d starts %v before segment %d ends", name, q, end.Sub(r.pdt), seqs[i-1])
			}
		}
	}

	for i := range names {
		for j := range names {
			if i == j {
				continue
			}
			a, b := k.views[names[i]], k.views[names[j]]
			ao, bo := ordered[names[i]], ordered[names[j]]
			if len(bo) == 0 {
				continue
			}
			first, last := bo[0].Start, bo[len(bo)-1].End()
			for _, s := range ao {
				tol := time.Duration(s.Dur * float64(time.Second))
				m, ok := PairByPDT(s, bo, tol)
				if !ok {
					// Only inside the other rendition's observed span: segments
					// beyond its window edges were never compared. A gap the
					// timeline jumped is in both renditions, so it leaves no
					// segment here to miss.
					if !s.Start.Before(first) && !s.End().After(last) {
						problem("align", "%s: no %s segment within one segment of %s", names[i], names[j], s.Start.Format(time.RFC3339Nano))
					}
					continue
				}
				if i < j && bo[m].DNum != s.DNum {
					problem("boundary", "%s at %s is discontinuity %d, %s says %d",
						names[i], s.Start.Format(time.RFC3339Nano), s.DNum, names[j], bo[m].DNum)
				}
			}
			if i > j {
				continue
			}
			for e, ta := range a.boundary {
				if tb, ok := b.boundary[e]; ok {
					if d := ta.Sub(tb); d > maxAlign || d < -maxAlign {
						problem("boundary", "%s/%s: discontinuity %d starts %v apart", names[i], names[j], e, d)
					}
				}
			}
			if a.final != b.final {
				problem("boundary", "%s ends on discontinuity %d, %s on %d", names[i], a.final, names[j], b.final)
			}
		}
	}
	return c
}
