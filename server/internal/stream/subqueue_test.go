package stream

import (
	"bytes"
	"testing"
	"time"
)

func TestSubQueueDropsOldestAndResendsTables(t *testing.T) {
	q := newSubQueue(3*188, time.Now)
	a, b, c, d := bytes.Repeat([]byte{1}, 188), bytes.Repeat([]byte{2}, 188), bytes.Repeat([]byte{3}, 188), bytes.Repeat([]byte{4}, 188)
	tables := bytes.Repeat([]byte{9}, 188)
	for _, ch := range [][]byte{a, b, c} {
		if n := q.push(ch, tables); n != 0 {
			t.Fatalf("dropped %d before full", n)
		}
	}
	if n := q.push(d, tables); n == 0 {
		t.Fatal("want a drop when full")
	}
	got, _ := q.pop()
	if !bytes.Equal(got, tables) {
		t.Fatalf("after a drop the queue head must be PAT+PMT, got %v", got[:1])
	}
	var rest []byte
	for {
		q.mu.Lock()
		empty := len(q.chunks) == 0
		q.mu.Unlock()
		if empty {
			break
		}
		ch, _ := q.pop()
		rest = append(rest, ch[0])
	}
	if !bytes.Equal(rest, []byte{3, 4}) && !bytes.Equal(rest, []byte{2, 3, 4}) {
		t.Fatalf("remaining order %v: oldest must go first, newest must stay", rest)
	}
}

func TestSubQueuePopBlocksUntilPushOrClose(t *testing.T) {
	q := newSubQueue(1<<20, time.Now)
	got := make(chan bool, 1)
	go func() { _, ok := q.pop(); got <- ok }()
	select {
	case <-got:
		t.Fatal("pop returned on an empty queue")
	case <-time.After(50 * time.Millisecond):
	}
	q.close()
	if ok := <-got; ok {
		t.Fatal("pop after close must report !ok")
	}
}

func TestSubQueueIdleFor(t *testing.T) {
	now := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(1<<20, func() time.Time { return now })
	if d := q.idleFor(now.Add(time.Hour)); d != 0 {
		t.Fatalf("empty queue idleFor = %v, want 0", d)
	}
	q.push(make([]byte, 188), nil)
	if d := q.idleFor(now.Add(31 * time.Second)); d < 30*time.Second {
		t.Fatalf("idleFor = %v, want ≥30s with bytes queued and no pop", d)
	}
}

// Device reads can split a TS packet; only whole packets are handed on and the
// remainder carries into the next read.
func TestAlignTS(t *testing.T) {
	whole, rest := alignTS(nil, make([]byte, 100))
	if len(whole) != 0 || len(rest) != 100 {
		t.Fatalf("100 bytes: whole=%d rest=%d, want 0/100", len(whole), len(rest))
	}
	whole, rest = alignTS(rest, make([]byte, 188*2-100+50))
	if len(whole) != 188*2 || len(rest) != 50 {
		t.Fatalf("carry+426: whole=%d rest=%d, want 376/50", len(whole), len(rest))
	}
	whole, rest = alignTS(nil, make([]byte, 188*3))
	if len(whole) != 188*3 || len(rest) != 0 {
		t.Fatalf("exact: whole=%d rest=%d", len(whole), len(rest))
	}
}

// A quiet input (e.g. a long loss of signal) is not a stuck reader: when data
// arrives after the queue sat empty, it has only just started waiting.
func TestSubQueueIdleForIgnoresQuietInput(t *testing.T) {
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	q := newSubQueue(1<<20, clock)
	q.push(make([]byte, 188), nil)
	if _, ok := q.pop(); !ok {
		t.Fatal("pop failed")
	}
	q.wrote(188)
	now = now.Add(40 * time.Second) // no input for 40s; queue empty
	q.push(make([]byte, 188), nil)
	if d := q.idleFor(now); d != 0 {
		t.Fatalf("idleFor = %v right after data resumed, want 0", d)
	}
	now = now.Add(31 * time.Second) // now the reader really is stuck
	if d := q.idleFor(now); d < 30*time.Second {
		t.Fatalf("idleFor = %v after 31s unconsumed, want ≥30s", d)
	}
}

// Progress is bytes written to FFmpeg, not pops: a 4 MiB chunk popped once
// and then written slowly keeps the stuck clock fresh with every slice; a
// reset (drain) never counts as progress.
func TestSubQueueProgressCountsWrittenBytesNotPops(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(8<<20, func() time.Time { return now })
	q.push(make([]byte, 4<<20), nil)
	if _, ok := q.pop(); !ok {
		t.Fatal("pop")
	}
	now = now.Add(20 * time.Second)
	if got := q.idleFor(now); got != 20*time.Second {
		t.Fatalf("idleFor=%v with 4 MiB in flight and nothing written, want 20s", got)
	}
	q.wrote(pipeWriteSlice)
	now = now.Add(5 * time.Second)
	if got := q.idleFor(now); got != 5*time.Second {
		t.Fatalf("idleFor=%v 5 s after a slice was written, want 5s", got)
	}
	q.push([]byte("aaaa"), nil)
	gen := q.resetGen()
	if n := q.reset([]byte("HEAD")); n != 4 {
		t.Fatalf("reset dropped %d bytes, want 4 (the in-flight chunk isn't queued)", n)
	}
	if q.resetGen() == gen || q.size() != 4 {
		t.Fatalf("resetGen unchanged or size=%d after reset", q.size())
	}
	now = now.Add(10 * time.Second)
	if got := q.idleFor(now); got != 15*time.Second {
		t.Fatalf("idleFor=%v after reset, want 15s (a reset is not progress)", got)
	}
	if q.writtenBytes() != pipeWriteSlice {
		t.Fatalf("writtenBytes=%d", q.writtenBytes())
	}
}

// With nothing queued and nothing in flight the reader isn't stuck (a quiet input).
func TestSubQueueIdleIsZeroWhenNothingWaits(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(1<<20, func() time.Time { return now })
	q.push([]byte("abcd"), nil)
	c, _ := q.pop()
	q.wrote(len(c))
	now = now.Add(time.Hour)
	if got := q.idleFor(now); got != 0 {
		t.Fatalf("idleFor=%v with nothing waiting", got)
	}
}
