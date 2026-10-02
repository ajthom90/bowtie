package tsloop

import (
	"context"
	"sort"
	"sync"
	"time"
)

// maxBatch caps how many packets one Reader.Next returns.
const maxBatch = 512

// segment is an immutable stretch of a channel's timeline: a source looping
// from epoch, whose packet 0 carries clock value base90k (+ jump90k).
type segment struct {
	src     *Source
	epoch   time.Time
	base90k int64
	jump90k int64
}

// Channel is one broadcast timeline. Its clock advances whether or not anyone
// is reading, and every Reader joins at the current position — like tuning a
// real channel.
type Channel struct {
	clock Clock
	mu    sync.Mutex
	seg   *segment
}

// NewChannel starts a timeline now; base90k is the channel clock value (PCR,
// 90 kHz) at this instant. Pass src.Origin90k() to keep the clip's own values.
func NewChannel(src *Source, clock Clock, base90k int64) *Channel {
	if clock.Now == nil || clock.After == nil {
		clock = RealClock()
	}
	return &Channel{
		clock: clock,
		seg:   &segment{src: src, epoch: clock.Now(), base90k: base90k},
	}
}

func (c *Channel) current() *segment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seg
}

// Source returns the source currently on air.
func (c *Channel) Source() *Source { return c.current().src }

// Now90k returns the channel clock (33-bit, 90 kHz) at the current time.
func (c *Channel) Now90k() int64 {
	return c.at(c.current(), c.clock.Now())
}

func (c *Channel) at(seg *segment, t time.Time) int64 {
	elapsed := t.Sub(seg.epoch)
	return (seg.base90k + seg.jump90k + int64(elapsed/time.Microsecond)*9/100) & tsMask
}

// Switch puts src on air now; the timeline continues from the current clock.
func (c *Channel) Switch(src *Source) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now()
	c.seg = &segment{src: src, epoch: now, base90k: c.at(c.seg, now)}
}

// Jump shifts every subsequent timestamp by d (a splice); content continues.
func (c *Channel) Jump(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := *c.seg
	next.jump90k += int64(d/time.Microsecond) * 9 / 100
	c.seg = &next
}

// NewReader returns a reader that joins the timeline at its current position.
func (c *Channel) NewReader() *Reader { return &Reader{c: c} }

// Reader paces one connection along a Channel.
type Reader struct {
	c   *Channel
	seg *segment
	l   int64 // loop number within seg
	i   int   // packet index within the loop
}

func (r *Reader) join(seg *segment, now time.Time) {
	r.seg = seg
	src := seg.src
	elapsed := now.Sub(seg.epoch)
	if elapsed < 0 {
		elapsed = 0
	}
	r.l = int64(elapsed / src.loop)
	within := elapsed - time.Duration(r.l)*src.loop
	r.i = sort.Search(len(src.sched), func(i int) bool { return src.sched[i] >= within })
	if r.i == len(src.sched) {
		r.i = 0
		r.l++
	}
}

func (r *Reader) due() time.Time {
	src := r.seg.src
	return r.seg.epoch.Add(time.Duration(r.l)*src.loop + src.sched[r.i])
}

// Next blocks until at least one packet is due and returns every due packet
// (up to maxBatch), each a rewritten copy.
func (r *Reader) Next(ctx context.Context) ([][]byte, error) {
	clock := r.c.clock
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		now := clock.Now()
		seg := r.c.current()
		switch {
		case r.seg == nil:
			r.join(seg, now)
		case seg != r.seg:
			if seg.src == r.seg.src && seg.epoch.Equal(r.seg.epoch) {
				r.seg = seg // a Jump: same content position, new offset
			} else {
				r.join(seg, now)
			}
		}
		if wait := r.due().Sub(now); wait > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-clock.After(wait):
			}
			continue
		}
		var out [][]byte
		for len(out) < maxBatch && !r.due().After(now) {
			out = append(out, r.rewrite())
			r.i++
			if r.i == len(r.seg.src.pkts) {
				r.i = 0
				r.l++
			}
		}
		return out, nil
	}
}

// rewrite returns a copy of the current packet with timestamps and continuity
// counter moved onto the channel timeline.
func (r *Reader) rewrite() []byte {
	src := r.seg.src
	p := append([]byte(nil), src.pkts[r.i]...)
	off := r.seg.base90k + r.seg.jump90k - src.origin90k + r.l*src.loop90k
	if base, ext, ok := PCR(p); ok {
		SetPCR(p, base+off, ext)
	}
	if pts, dts, hasPTS, _ := PESTimestamps(p); hasPTS {
		SetPESTimestamps(p, pts+off, dts+off)
	}
	if HasPayload(p) {
		shift := byte((r.l % 16) * int64(src.ccShift[PID(p)]))
		SetCC(p, CC(p)+shift)
	}
	return p
}
