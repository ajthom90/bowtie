package tsloop

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Track classifies an elementary stream by its PES stream_id.
type Track int

const (
	TrackOther Track = iota
	TrackVideo
	TrackAudio
)

// Source is an analyzed, immutable MPEG-TS clip ready to be looped.
type Source struct {
	name      string
	pkts      [][]byte
	sched     []time.Duration // due offset of each packet within one loop
	loop90k   int64
	loop      time.Duration
	origin90k int64
	ccShift   map[uint16]byte
	tracks    map[uint16]Track
}

// LoadFile loads a .ts file; the source is named after the file's base name.
func LoadFile(path string) (*Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return Load(name, f)
}

// Load reads and analyzes a whole transport stream.
func Load(name string, r io.Reader) (*Source, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%PacketSize != 0 {
		return nil, fmt.Errorf("tsloop: %s: length %d is not a multiple of %d", name, len(data), PacketSize)
	}
	s := &Source{
		name:    name,
		ccShift: map[uint16]byte{},
		tracks:  map[uint16]Track{},
	}
	for off := 0; off < len(data); off += PacketSize {
		p := data[off : off+PacketSize : off+PacketSize]
		if p[0] != 0x47 {
			return nil, fmt.Errorf("tsloop: %s: packet %d has no sync byte", name, off/PacketSize)
		}
		s.pkts = append(s.pkts, p)
	}
	if err := s.analyzeSchedule(); err != nil {
		return nil, fmt.Errorf("tsloop: %s: %w", name, err)
	}
	s.analyzeTimestamps()
	s.analyzeCC()
	return s, nil
}

// analyzeSchedule interpolates a 27 MHz PCR for every packet and derives each
// packet's due offset relative to packet 0.
func (s *Source) analyzeSchedule() error {
	pcrPID := -1
	var idx []int
	var val []float64
	for i, p := range s.pkts {
		base, ext, ok := PCR(p)
		if !ok {
			continue
		}
		if pcrPID < 0 {
			pcrPID = int(PID(p))
		}
		if int(PID(p)) != pcrPID {
			continue
		}
		v := float64(base*300 + int64(ext))
		if len(val) > 0 && v <= val[len(val)-1] {
			return errors.New("PCR is not increasing within the clip")
		}
		idx = append(idx, i)
		val = append(val, v)
	}
	if len(idx) < 2 {
		return errors.New("need at least two PCRs")
	}
	n := len(idx) - 1
	rate := (val[n] - val[0]) / float64(idx[n]-idx[0]) // 27 MHz ticks per packet
	pcr27 := func(i int) float64 {
		switch {
		case i <= idx[0]:
			return val[0] - float64(idx[0]-i)*rate
		case i >= idx[n]:
			return val[n] + float64(i-idx[n])*rate
		}
		k := 0
		for idx[k+1] < i {
			k++
		}
		f := float64(i-idx[k]) / float64(idx[k+1]-idx[k])
		return val[k] + f*(val[k+1]-val[k])
	}
	origin := pcr27(0)
	s.origin90k = int64(math.Floor(origin / 300))
	s.sched = make([]time.Duration, len(s.pkts))
	for i := range s.pkts {
		s.sched[i] = time.Duration(math.Round((pcr27(i) - origin) * 1000 / 27))
	}
	// The loop must at least cover the PCR timeline plus one packet interval.
	pcrSpan90k := int64(math.Ceil((pcr27(len(s.pkts)-1) + rate - origin) / 300))
	s.loop90k = pcrSpan90k
	return nil
}

// analyzeTimestamps sets the loop length to the longest PES track so that
// looping never overlaps a track with itself, and classifies tracks.
func (s *Source) analyzeTimestamps() {
	seen := map[uint16][]int64{}
	var order []uint16
	for _, p := range s.pkts {
		pid := PID(p)
		if sid, ok := StreamID(p); ok {
			if _, known := s.tracks[pid]; !known {
				s.tracks[pid] = trackOf(sid)
			}
		}
		pts, dts, hasPTS, hasDTS := PESTimestamps(p)
		if !hasPTS {
			continue
		}
		ts := pts
		if hasDTS {
			ts = dts
		}
		if _, ok := seen[pid]; !ok {
			order = append(order, pid)
		}
		seen[pid] = append(seen[pid], ts)
	}
	for _, pid := range order {
		ts := seen[pid]
		if len(ts) < 2 {
			continue
		}
		if span := ts[len(ts)-1] - ts[0] + typicalDelta(ts); span > s.loop90k {
			s.loop90k = span
		}
	}
	s.loop = time.Duration(s.loop90k) * time.Second / 90000
}

// typicalDelta is the most common positive delta between consecutive values.
func typicalDelta(ts []int64) int64 {
	counts := map[int64]int{}
	var best int64
	for i := 1; i < len(ts); i++ {
		d := ts[i] - ts[i-1]
		if d <= 0 {
			continue
		}
		counts[d]++
		if counts[d] > counts[best] || (counts[d] == counts[best] && d < best) {
			best = d
		}
	}
	return best
}

func trackOf(sid byte) Track {
	switch {
	case sid >= 0xE0 && sid <= 0xEF:
		return TrackVideo
	case sid >= 0xC0 && sid <= 0xDF, sid == 0xBD:
		return TrackAudio
	}
	return TrackOther
}

// analyzeCC computes, per PID, how far the continuity counter must shift each
// loop so the last packet of one loop flows into the first of the next.
func (s *Source) analyzeCC() {
	first := map[uint16]byte{}
	last := map[uint16]byte{}
	for _, p := range s.pkts {
		if !HasPayload(p) {
			continue
		}
		pid := PID(p)
		if _, ok := first[pid]; !ok {
			first[pid] = CC(p)
		}
		last[pid] = CC(p)
	}
	for pid, f := range first {
		s.ccShift[pid] = (last[pid] + 1 - f) & 0x0F
	}
}

// Name returns the source's name.
func (s *Source) Name() string { return s.name }

// Packets returns the number of packets in one loop.
func (s *Source) Packets() int { return len(s.pkts) }

// LoopDuration returns how long one pass over the clip lasts.
func (s *Source) LoopDuration() time.Duration { return s.loop }

// Origin90k returns packet 0's interpolated PCR in 90 kHz ticks. Passing it as
// NewChannel's base leaves the first loop's timestamps unchanged.
func (s *Source) Origin90k() int64 { return s.origin90k }

// TrackOf classifies a PID; unknown PIDs are TrackOther.
func (s *Source) TrackOf(pid uint16) Track { return s.tracks[pid] }
