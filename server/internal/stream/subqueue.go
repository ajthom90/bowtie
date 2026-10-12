package stream

import (
	"sync"
	"time"
)

// subQueue is one subscriber's byte-bounded FIFO of whole-TS-packet chunks.
// push never blocks: when full it drops the oldest chunks, and after any drop
// it puts the current PAT+PMT at the head so the demuxer resyncs immediately.
type subQueue struct {
	mu           sync.Mutex
	cond         *sync.Cond
	chunks       [][]byte
	bytes        int
	max          int
	closed       bool
	now          func() time.Time
	lastProgress time.Time
	inFlight     int    // popped bytes not yet written to the pipe
	written      uint64 // bytes written to the pipe
	gen          uint64 // bumped by reset
	// fresh counts written bytes that came from the live source. Cached
	// copies (the join snapshot, a reset head, the PAT+PMT put back after
	// an overflow) are written but not fresh. They only ever enter at the
	// front of the queue, so cachedFront counts them there.
	fresh       uint64
	cachedFront int

	// betweenResetAndBaseline, if set, runs with q.mu held after beginDrain has
	// copied the written-byte baseline and before it releases the lock. The
	// hook must not lock q.mu. A write it performs is progress after the
	// drain; reading q.written after the hook would swallow that write into
	// the baseline. Nil outside tests.
	betweenResetAndBaseline func()
}

func newSubQueue(max int, now func() time.Time) *subQueue {
	q := &subQueue{max: max, now: now, lastProgress: now()}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends chunk, dropping the oldest chunks until it fits, and returns
// how many bytes were dropped.
func (q *subQueue) push(chunk, tables []byte) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0
	}
	if len(q.chunks) == 0 && q.inFlight == 0 {
		// Data starts waiting now: a quiet input (no signal) is not a stuck
		// reader, so the stuck clock must not count the empty time.
		q.lastProgress = q.now()
	}
	dropped := 0
	if q.bytes+len(chunk) > q.max {
		// Make room for the chunk and the PAT+PMT that will follow a drop.
		for q.bytes+len(chunk)+len(tables) > q.max && len(q.chunks) > 0 {
			dropped += len(q.chunks[0])
			if q.cachedFront > 0 {
				q.cachedFront-- // the dropped front chunk was a cached copy
			}
			q.bytes -= len(q.chunks[0])
			q.chunks[0] = nil
			q.chunks = q.chunks[1:]
		}
	}
	if dropped > 0 && len(tables) > 0 {
		q.chunks = append([][]byte{tables}, q.chunks...)
		q.bytes += len(tables)
		q.cachedFront++
	}
	q.chunks = append(q.chunks, chunk)
	q.bytes += len(chunk)
	q.cond.Signal()
	return dropped
}

// pop blocks until a chunk is available; ok is false once the queue is closed.
func (q *subQueue) pop() ([]byte, bool) {
	c, _, ok := q.popChunk()
	return c, ok
}

// popChunk is pop, and whether the chunk is a cached copy (a reset head or
// PAT+PMT put back after an overflow) rather than live source bytes.
func (q *subQueue) popChunk() (c []byte, cached, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false, false
	}
	c = q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.bytes -= len(c)
	q.inFlight += len(c)
	if q.cachedFront > 0 {
		q.cachedFront--
		cached = true
	}
	return c, cached, true
}

func (q *subQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.chunks = nil
	q.bytes = 0
	q.cachedFront = 0
	q.cond.Broadcast()
}

// idleFor is how long data has waited (queued or in flight) without a byte
// reaching the reader; 0 when nothing waits.
func (q *subQueue) idleFor(now time.Time) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bytes == 0 && q.inFlight == 0 {
		return 0
	}
	return now.Sub(q.lastProgress)
}

// wrote records n bytes written to the reader's pipe: the only progress.
func (q *subQueue) wrote(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.wroteLocked(n)
}

// wroteLocked is wrote for a caller that holds q.mu.
func (q *subQueue) wroteLocked(n int) {
	q.inFlight -= n
	if q.inFlight < 0 {
		q.inFlight = 0
	}
	q.written += uint64(n)
	q.lastProgress = q.now()
}

// wroteFrom is wrote, also counting the bytes as fresh unless they came
// from a cached copy. written and fresh move under one hold.
func (q *subQueue) wroteFrom(n int, cached bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.wroteLocked(n)
	if !cached {
		q.fresh += uint64(n)
	}
}

// freshBytes is the live source bytes written to the reader's pipe.
func (q *subQueue) freshBytes() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fresh
}

// abandon drops the unwritten rest of the current chunk (after a reset).
func (q *subQueue) abandon(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inFlight -= n
	if q.inFlight < 0 {
		q.inFlight = 0
	}
}

func (q *subQueue) writtenBytes() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.written
}

func (q *subQueue) resetGen() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.gen
}

// size is the queued byte count (not counting the chunk in flight).
func (q *subQueue) size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}

// reset replaces the queued backlog with head and returns the bytes dropped.
// The stuck clock, the in-flight count and the written count are left
// alone: a drain is not progress.
func (q *subQueue) reset(head []byte) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.resetLocked(head)
}

// resetLocked is reset for a caller that holds q.mu.
func (q *subQueue) resetLocked(head []byte) int {
	if q.closed {
		return 0
	}
	dropped := q.bytes
	for i := range q.chunks {
		q.chunks[i] = nil
	}
	q.chunks, q.bytes = q.chunks[:0], 0
	q.cachedFront = 0
	q.gen++
	if len(head) > 0 {
		q.chunks = append(q.chunks, head)
		q.bytes = len(head)
		q.cachedFront = 1
		q.cond.Signal()
	}
	return dropped
}

// beginDrain, when data has waited at least minIdle without a byte written,
// replaces the backlog with head and returns the bytes dropped and the
// written-byte baseline. The idle re-check, the reset and that baseline
// share one hold of q.mu, so a slice that finishes cannot be stored as the
// baseline and also count as later progress. drained is false when nothing
// is waiting or a write has already ended the idle.
func (q *subQueue) beginDrain(now time.Time, minIdle time.Duration, head []byte) (dropped int, baseline uint64, idle time.Duration, drained bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || (q.bytes == 0 && q.inFlight == 0) {
		return 0, q.written, 0, false
	}
	idle = now.Sub(q.lastProgress)
	if idle < minIdle {
		return 0, q.written, idle, false
	}
	dropped = q.resetLocked(head)
	baseline = q.written
	// The baseline is the count above, not a later read. The hook runs in
	// the old gap — after the reset, before the lock is released — so a
	// write here is progress. Sampling q.written after this hook would
	// store that write as the baseline.
	if q.betweenResetAndBaseline != nil {
		q.betweenResetAndBaseline()
	}
	return dropped, baseline, idle, true
}

// addInFlight counts bytes handed to the writer outside pop (the join).
func (q *subQueue) addInFlight(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bytes == 0 && q.inFlight == 0 {
		q.lastProgress = q.now()
	}
	q.inFlight += n
}

// alignTS joins carry and data and splits off the whole TS packets. Both
// results are fresh slices (safe to keep after buf is reused).
func alignTS(carry, data []byte) (whole, rest []byte) {
	all := make([]byte, 0, len(carry)+len(data))
	all = append(all, carry...)
	all = append(all, data...)
	n := len(all) - len(all)%tsPacketSize
	return all[:n:n], append([]byte(nil), all[n:]...)
}
