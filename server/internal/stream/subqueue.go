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
			q.bytes -= len(q.chunks[0])
			q.chunks[0] = nil
			q.chunks = q.chunks[1:]
		}
	}
	if dropped > 0 && len(tables) > 0 {
		q.chunks = append([][]byte{tables}, q.chunks...)
		q.bytes += len(tables)
	}
	q.chunks = append(q.chunks, chunk)
	q.bytes += len(chunk)
	q.cond.Signal()
	return dropped
}

// pop blocks until a chunk is available; ok is false once the queue is closed.
func (q *subQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false
	}
	c := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.bytes -= len(c)
	q.inFlight += len(c)
	return c, true
}

func (q *subQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.chunks = nil
	q.bytes = 0
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
	q.inFlight -= n
	if q.inFlight < 0 {
		q.inFlight = 0
	}
	q.written += uint64(n)
	q.lastProgress = q.now()
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
	if q.closed {
		return 0
	}
	dropped := q.bytes
	for i := range q.chunks {
		q.chunks[i] = nil
	}
	q.chunks, q.bytes = q.chunks[:0], 0
	q.gen++
	if len(head) > 0 {
		q.chunks = append(q.chunks, head)
		q.bytes = len(head)
		q.cond.Signal()
	}
	return dropped
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
